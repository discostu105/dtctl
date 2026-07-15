// Package tui implements the interactive terminal UI behind the dynatui binary
// (`dtctl tui` forwards to it) — a k9s-style navigator over observability
// primitives (see docs/design/tui.md). It is a presentation layer over
// dtctl's pkg/exec and the catalog's DQL templates; nothing outside the dynatui
// main package imports it.
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtctl/pkg/exec"
	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
	"github.com/dynatrace-oss/dynatui/internal/tui/theme"
)

// Options wires the TUI to the current dtctl context.
type Options struct {
	ContextName string
	Environment string
	SafetyLevel string
	Executor    *exec.DQLExecutor
	// Sources backs the API views (catalog.Spec.API): "slos",
	// "anomaly-detectors", "log-patterns". Constructed in the dynatui main
	// package so no HTTP lives in this package.
	Sources     map[string]Source
	InitialView string // catalog view name or alias; "" = problems
	// InitialArg is the optional CLI argument for the initial view — only
	// nav takes one (`dynatui nav <type|id|name>`); ignored elsewhere.
	InitialArg       string
	HistoryPath      string // navigation-history file; "" = in-memory only
	QueryHistoryPath string // submitted-DQL history file; "" = in-memory only

	// SegmentSource backs the segment picker and workspace seeding; nil
	// disables both. Constructed in the dynatui main package (no HTTP here).
	SegmentSource SegmentLister
	// SwitchContext rebuilds the tenant wiring for another dtctl context —
	// the :ctx command. nil disables in-session switching. Constructed in
	// the dynatui main package (no HTTP here); session-local by contract, it
	// must never write the shared config.
	SwitchContext func(name string) (*ContextWiring, error)
	// Contexts are the configured context names (:ctx without an argument
	// lists them).
	Contexts []string
	// WorkspaceSegments are .dynatrace.yaml refs resolved and applied on
	// startup (async — the first paint is never blocked on them).
	WorkspaceSegments []WorkspaceSegment
	// InitialTimeframe is the workspace default window ("" = built-in 2h).
	InitialTimeframe string
	// StartupNotice is a one-line message shown on launch — how a committed
	// .dynatrace.yaml announces it is steering the session (or failed to).
	StartupNotice    string
	StartupNoticeErr bool
}

// ContextWiring is everything in Options that depends on the tenant client
// — what Options.SwitchContext rebuilds when :ctx moves the session to
// another dtctl context.
type ContextWiring struct {
	ContextName   string
	Environment   string
	SafetyLevel   string
	Executor      *exec.DQLExecutor
	Sources       map[string]Source
	SegmentSource SegmentLister
}

// Run launches the TUI and blocks until the user quits.
func Run(opts Options) error {
	app, err := newApp(opts)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(app, tea.WithAltScreen()).Run()
	return err
}

// refreshIntervals are the R-key auto-refresh cycle (0 = off).
var refreshIntervals = []time.Duration{0, 10 * time.Second, 30 * time.Second, 60 * time.Second}

// hotkeys are the digit bookmarks (k9s-style); ? shows them.
var hotkeys = map[string]string{
	"0": "home", "1": "problems", "2": "services", "3": "hosts", "4": "pods",
	"5": "logs", "6": "traces", "7": "workloads", "8": "events", "9": "aws",
}

// selectionProvider is implemented by views that expose a highlighted row
// (and its entity) for app-level actions: pin, relations, yank, open.
type selectionProvider interface {
	Selection() (map[string]any, *catalog.Entity)
}

// dqlProvider is implemented by views backed by a revealable query (ctrl+q).
type dqlProvider interface {
	DQL() string
}

// traceProvider is implemented by views standing on one trace (waterfall).
type traceProvider interface {
	TraceID() string
}

// yankProvider is implemented by views that can supply the exact text the
// global 'y' copies (the inspector's selected field value).
type yankProvider interface {
	YankText() (text, label string, ok bool)
}

// digitClaimer is implemented by entered pages whose visible numbered strip
// (the tab bar) owns the digit keys while the page is on top; views without
// numbers on screen leave the digits to the global bookmarks.
type digitClaimer interface {
	ClaimsDigits() bool
}

type app struct {
	opts Options
	ds   *dataSource
	tf   catalog.Timeframe
	pin  *catalog.Entity // '.' — global scope for command-bar jumps

	width, height int

	stack []viewModel
	prev  []viewModel // '-' toggles between the two most recent stacks

	// Overlays.
	cmdActive  bool
	cmdInput   textinput.Model
	cmdMatches []*catalog.Spec
	cmdSel     int
	helpActive bool
	tfActive   bool
	tfSel      int // picker highlight; len(Timeframes) = the custom entry
	tfCustom   bool
	tfInput    textinput.Model
	histActive bool
	histSel    int
	histList   []historyEntry // snapshot shown by the open picker

	// "open with" picker ('o' with several browser targets).
	openActive bool
	openSel    int
	openList   []linkOption

	// Global segment scope (the fourth global state after context, timeframe,
	// and pin) — mirrored here for the header and picker; the query-side truth
	// lives on ds.segments.
	segApplied []SegmentOption
	segVars    map[string][]exec.FilterSegmentVariable // uid → workspace bindings
	segPending bool                                    // workspace refs still resolving against the tenant list
	segPaused  bool                                    // alt+s — selection kept, nothing sent to queries

	// Segment picker overlay ('S').
	segPickActive bool
	segPickSel    int
	segChecked    map[string]bool // scratch selection until enter
	segList       []SegmentOption // cached lister result
	segLoading    bool
	segErr        string

	// Variable value sub-picker (space on an unbound segment, or 'v').
	segVar segVarState

	// Context picker overlay (':ctx' without a name).
	ctxPickActive bool
	ctxPickSel    int

	hist  *historyStore
	qhist *queryHistory

	status    string
	statusErr bool

	refreshIdx int
	refreshGen int

	ctxSwitchSeq int // drops a stale async :ctx switch result

	spinning bool // spinner ticker scheduled
}

// ctxSwitchedMsg delivers an async :ctx context switch (or its failure).
type ctxSwitchedMsg struct {
	seq    int
	wiring *ContextWiring
	err    error
}

type refreshTickMsg struct{ gen int }

// spinnerTickMsg advances the loading-spinner animation while the visible
// view is busy.
type spinnerTickMsg struct{}

func newApp(opts Options) (*app, error) {
	ci := newTextInput()
	ci.Prompt = ":"
	ci.PromptStyle = theme.Crumb
	ci.CharLimit = 64
	ti := newTextInput()
	ti.Prompt = "last "
	ti.PromptStyle = theme.Crumb
	ti.Placeholder = "45m · 12h · 3d"
	ti.CharLimit = 8
	a := &app{
		opts:  opts,
		ds:    &dataSource{exec: opts.Executor, sources: opts.Sources},
		tf:    catalog.DefaultTimeframe,
		hist:  loadHistory(opts.HistoryPath, opts.ContextName),
		qhist: loadQueryHistory(opts.QueryHistoryPath),
	}
	if tf, ok := catalog.ParseTimeframe(opts.InitialTimeframe); ok {
		a.tf = tf
	}
	a.cmdInput = ci
	a.tfInput = ti
	if i, preset := presetIndex(a.tf.Label); preset {
		a.tfSel = i
	} else {
		a.tfSel = len(catalog.Timeframes)
	}

	initial, err := a.viewFor(opts.InitialView)
	if err != nil {
		return nil, err
	}
	// `dynatui nav <arg>` — same routing as the :nav command-bar argument.
	if _, isNav := initial.(*navView); isNav && strings.TrimSpace(opts.InitialArg) != "" {
		initial = a.navViewFor(strings.TrimSpace(opts.InitialArg))
	}
	a.stack = []viewModel{initial}
	return a, nil
}

// navViewFor builds the navigator view a :nav / CLI argument means: an
// entity id walks from it, a type-shaped token (case-insensitive — ":nav
// service" keeps meaning the SERVICE browser) browses the type, anything
// else resolves as a name. A lowercase token is ambiguous — "payments" is
// type-shaped too — so its type browse carries the original term as a
// fallback: zero instances re-shape it into the name search.
func (a *app) navViewFor(arg string) viewModel {
	if entityIDRe.MatchString(arg) {
		return newNavWalkView(a.ds, *entityFromID(arg), a.tf)
	}
	if typ := strings.ToUpper(arg); navTypeRe.MatchString(typ) {
		v := newNavBrowserView(a.ds, typ, a.tf)
		if arg != typ {
			v.searchFallback = arg
		}
		return v
	}
	return newNavSearchView(a.ds, arg, a.tf)
}

// viewFor resolves a view name to a fresh view: the bespoke screens (home,
// query) or a catalog table.
func (a *app) viewFor(name string) (viewModel, error) {
	switch name {
	case "", "home":
		return newHomeView(a.ds, a.tf), nil
	case "query", "dql":
		return newQueryView(a.ds, "", a.tf, a.qhist), nil
	case "nav", "smartscape", "navigator":
		return newNavView(a.ds, a.tf), nil
	}
	spec := catalog.Lookup(name)
	if spec == nil {
		return nil, fmt.Errorf("unknown view %q (available: home, query, nav, %s)", name, strings.Join(catalog.Names(), ", "))
	}
	// The record sampler without an argument would just re-browse the table
	// catalog — that browser already exists as :tables, so go there instead
	// (drills reach the sampler with an Arg via pushViewMsg, never here).
	if spec.Name == "records" {
		spec = catalog.Lookup("tables")
	}
	scope := catalog.Scope{Timeframe: a.tf}
	// Only hand the pin to a view whose query actually composes it, so the
	// breadcrumb never claims a scope that was never applied.
	if a.pin != nil && spec.UsesScope() && spec.CanScope(a.tf, *a.pin) {
		scope.Entity = a.pin
	}
	return newTableView(a.ds, spec, scope), nil
}

// pinRejected reports whether a pin exists but the named view cannot scope to
// it (so jumpTo can tell the user the pin was ignored rather than leave them
// reading an unfiltered list under a pinned header).
func (a *app) pinRejected(name string) bool {
	if a.pin == nil {
		return false
	}
	spec := catalog.Lookup(name)
	return spec != nil && spec.UsesScope() && !spec.CanScope(a.tf, *a.pin)
}

func (a *app) top() viewModel { return a.stack[len(a.stack)-1] }

func (a *app) Init() tea.Cmd {
	cmds := []tea.Cmd{a.top().Init()}
	if a.opts.StartupNotice != "" {
		if a.opts.StartupNoticeErr {
			cmds = append(cmds, statusErr(a.opts.StartupNotice))
		} else {
			cmds = append(cmds, status(a.opts.StartupNotice))
		}
	}
	// Workspace segments resolve asynchronously: the first paint is never
	// blocked, and the initial unsegmented queries are superseded by the
	// applySegments refetch (seq guards drop them).
	if len(a.opts.WorkspaceSegments) > 0 {
		a.segPending = true
		cmds = append(cmds, a.loadSegmentList())
	}
	return tea.Batch(cmds...)
}

func (a *app) bodyHeight() int { return max(a.height-4, 1) }

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := a.dispatch(msg)
	// Keep the loading spinner animated whenever the visible view is busy.
	if spin := a.ensureSpin(); spin != nil {
		cmd = tea.Batch(cmd, spin)
	}
	return a, cmd
}

// dispatch routes one message through the app.
func (a *app) dispatch(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		return a.broadcast(bodySizeMsg{width: a.width, height: a.bodyHeight()})

	case tea.KeyMsg:
		// Any key clears the transient status line.
		a.status = ""
		return a.handleKey(msg)

	case dataMsg:
		// The one-shot semantic-dictionary fetch fills the shared cache — no
		// view owns it; every inspector reads it at render time.
		if _, ok := msg.owner.(dictOwner); ok {
			if msg.err == nil {
				a.ds.dict = catalog.ParseFieldDocs(msg.records)
			}
			return nil
		}
		// The segment variable value fetch belongs to the sub-picker.
		if _, ok := msg.owner.(segVarOwner); ok {
			return a.handleSegVarData(msg)
		}
		// Deliver to every view on either stack (deduped — the stacks share
		// views), so a parent still loading below a drill-down completes and
		// detail pages can forward results to their tabs. Views drop results
		// they don't own.
		seen := map[viewModel]bool{}
		var cmds []tea.Cmd
		for _, v := range append(append([]viewModel{}, a.stack...), a.prev...) {
			if seen[v] {
				continue
			}
			seen[v] = true
			if cmd := v.Update(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return tea.Batch(cmds...)

	case pushViewMsg:
		view := newTableView(a.ds, msg.spec, msg.scope)
		if msg.filter != "" {
			view.setFilter(msg.filter)
		}
		// Inherited server narrowing must be in place before navigate runs
		// Init (which composes and fires the query).
		view.searches = msg.searches
		view.facets = msg.facets
		return a.navigate(view, msg.replace)

	case inspectMsg:
		return a.navigate(newInspectorView(a.ds, msg.title, msg.rec), false)

	case detailMsg:
		return a.navigate(newDetailView(a.ds, msg.entity, msg.rec, a.tf), false)

	case problemMsg:
		return a.navigate(newProblemView(a.ds, msg.rec, time.Now()), false)

	case vulnMsg:
		return a.navigate(newVulnerabilityView(a.ds, msg.rec, a.tf), false)

	case metricsMsg:
		return a.navigate(newMetricsView(a.ds, msg.entity, a.tf), false)

	case metricChartMsg:
		var entity catalog.Entity
		if msg.entity != nil {
			entity = *msg.entity
		}
		return a.navigate(newMetricChartView(a.ds, msg.key, entity, a.tf), false)

	case waterfallMsg:
		return a.navigate(newWaterfallView(a.ds, msg.traceID, msg.focusSpanID, a.tf), false)

	case timelineMsg:
		return a.navigate(newTimelineView(a.ds, msg.sessionID, msg.rec, a.tf), false)

	case navMsg:
		switch {
		case msg.root != nil:
			return a.navigate(newNavWalkView(a.ds, *msg.root, a.tf), msg.replace)
		case msg.typ != "":
			return a.navigate(newNavBrowserView(a.ds, msg.typ, a.tf), msg.replace)
		case msg.search != "":
			return a.navigate(newNavSearchView(a.ds, msg.search, a.tf), msg.replace)
		default:
			return a.navigate(newNavView(a.ds, a.tf), msg.replace)
		}

	case queryMsg:
		return a.navigate(newQueryView(a.ds, msg.dql, a.tf, a.qhist), false)

	case segmentListMsg:
		return a.handleSegmentList(msg)

	case ctxSwitchedMsg:
		return a.handleCtxSwitched(msg)

	case statusMsg:
		a.status, a.statusErr = msg.text, msg.isErr
		return nil

	case historyMarkMsg:
		a.recordHistory()
		return nil

	case applyFacetMsg:
		return a.applyFacetBelow(msg)

	case refreshTickMsg:
		if msg.gen != a.refreshGen || refreshIntervals[a.refreshIdx] == 0 {
			return nil
		}
		return tea.Batch(a.top().Refresh(), a.scheduleRefresh())

	case spinnerTickMsg:
		if a.busy() {
			theme.Tick()
			return spinTick()
		}
		a.spinning = false
		return nil
	}

	return a.top().Update(msg)
}

// busy reports whether the visible view is waiting on data.
func (a *app) busy() bool {
	if br, ok := a.top().(busyReporter); ok {
		return br.Busy()
	}
	return false
}

// ensureSpin starts the spinner ticker when the visible view turns busy.
func (a *app) ensureSpin() tea.Cmd {
	if a.spinning || !a.busy() {
		return nil
	}
	a.spinning = true
	return spinTick()
}

func spinTick() tea.Cmd {
	return tick(90*time.Millisecond, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

// navigate pushes a view (or replaces the stack for command-bar jumps),
// remembering the previous stack for the '-' toggle and recording the new
// trail in the persistent history.
func (a *app) navigate(view viewModel, replace bool) tea.Cmd {
	a.prev = a.stack
	if replace {
		a.stack = []viewModel{view}
	} else {
		a.stack = append(append([]viewModel{}, a.stack...), view)
	}
	a.recordHistory()
	return tea.Batch(
		view.Update(bodySizeMsg{width: a.width, height: a.bodyHeight()}),
		view.Init(),
	)
}

func (a *app) handleKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()

	if key == "ctrl+c" {
		return a.quit()
	}

	if a.cmdActive {
		return a.updateCmdbar(msg)
	}
	if a.tfActive {
		return a.updateTfPicker(msg)
	}
	if a.histActive {
		return a.updateHistPicker(msg)
	}
	if a.openActive {
		return a.updateOpenPicker(msg)
	}
	if a.segVar.active {
		return a.updateSegVarPicker(msg)
	}
	if a.segPickActive {
		return a.updateSegPicker(msg)
	}
	if a.ctxPickActive {
		return a.updateCtxPicker(msg)
	}
	if a.helpActive {
		a.helpActive = false
		return nil
	}

	top := a.top()
	if !top.InputActive() {
		// Digit hotkeys jump to bookmarked views. Entering a page rescopes
		// the keyboard to it: a view showing a numbered strip (the tab bar
		// on detail/problem pages) claims 1-9 while it is on top — the
		// numbering on screen is the mode indicator — and esc restores the
		// global bookmarks. 0 never appears on a tab bar, so it stays the
		// jump home from anywhere; top-level tables, home, and the
		// navigator show no numbers, so all ten keys stay global there.
		if name, ok := hotkeys[key]; ok {
			if c, entered := top.(digitClaimer); key == "0" || !entered || !c.ClaimsDigits() {
				return a.jumpTo(name, "")
			}
		}
		switch key {
		case "q":
			return a.quit()
		case "H":
			return a.openHistory()
		case ":":
			a.cmdActive = true
			a.cmdInput.SetValue("")
			a.cmdInput.Focus()
			a.updateCmdMatches()
			return textinput.Blink
		case "?":
			a.helpActive = true
			return nil
		case "t":
			a.tfActive = true
			// A window typed via the custom entry is not a preset — land the
			// highlight on custom so the picker reflects what is applied.
			if i, preset := presetIndex(a.tf.Label); preset {
				a.tfSel = i
			} else {
				a.tfSel = len(catalog.Timeframes)
			}
			return nil
		case "S":
			return a.openSegmentPicker()
		case "alt+s":
			return a.toggleSegmentsPaused()
		case "x", "X":
			// Both cases walk the topology: the navigator's trail-based walk
			// strictly dominates the old one-hop relations page (which
			// survives as the detail page's related tab), so x stopped being
			// a separate, weaker surface.
			if _, entity := a.selection(); entity != nil {
				e := *entity
				return func() tea.Msg { return navMsg{root: &e} }
			}
			return statusErr("selection carries no entity to walk")
		case ".":
			return a.togglePin()
		case "P":
			// The peek pane is an app-wide preference, not per-view state:
			// every table and navigator pane follows it, nested ones
			// included, and views pushed later inherit it.
			previewEnabled = !previewEnabled
			if !previewEnabled {
				return status("preview pane off (P restores it)")
			}
			cmd := status("preview pane on")
			if a.width < previewPaneMinWidth && a.bodyHeight() < previewBottomMinHeight {
				cmd = status("preview on — hidden until the terminal grows")
			}
			if nv, ok := top.(*navView); ok {
				if nv.rightPaneVisible() {
					cmd = status("preview pane on")
				}
				// The pane may have been off since the walk opened — arm the
				// debounced detail fetch for the current selection.
				return tea.Batch(cmd, nv.schedulePreview())
			}
			return cmd
		case "ctrl+x":
			if a.pin == nil {
				return nil
			}
			a.pin = nil
			return status("scope unpinned")
		case "ctrl+q":
			dql := ""
			if p, ok := top.(dqlProvider); ok {
				dql = p.DQL()
			}
			return func() tea.Msg { return queryMsg{dql: dql} }
		case "y":
			return a.yankSelection()
		case "c":
			if echo := top.Echo(); echo != "" {
				yank(echo)
				return status("copied: " + echo)
			}
			return nil
		case "o":
			return a.openSelection()
		case "r":
			return top.Refresh()
		case "R":
			a.refreshIdx = (a.refreshIdx + 1) % len(refreshIntervals)
			a.refreshGen++
			if refreshIntervals[a.refreshIdx] == 0 {
				return status("auto-refresh off")
			}
			return tea.Batch(
				status(fmt.Sprintf("auto-refresh every %s", refreshIntervals[a.refreshIdx])),
				a.scheduleRefresh(),
			)
		case "-":
			if a.prev != nil {
				a.stack, a.prev = a.prev, a.stack
				return a.top().Update(bodySizeMsg{width: a.width, height: a.bodyHeight()})
			}
			return nil
		case "esc":
			// Give the view first claim on esc (clearing an active filter);
			// tableView returns nil when it has nothing to clear.
			if cmd := top.Update(msg); cmd != nil {
				return cmd
			}
			if len(a.stack) > 1 {
				a.prev = a.stack
				a.stack = a.stack[:len(a.stack)-1]
			}
			return nil
		}
	}
	return top.Update(msg)
}

// jumpTo replaces the stack with a named view (hotkeys, command bar), an
// optional argument pre-filling the table filter.
func (a *app) jumpTo(name, filter string) tea.Cmd {
	view, err := a.viewFor(name)
	if err != nil {
		return statusErr(err.Error())
	}
	if tv, ok := view.(*tableView); ok && filter != "" {
		tv.setFilter(filter)
	}
	nav := a.navigate(view, true)
	// The view was built unscoped when the pin didn't apply — say so, so the
	// user isn't left reading an unfiltered list wondering why.
	if a.pinRejected(name) {
		return tea.Batch(nav, statusErr(fmt.Sprintf("%s can't scope to %s — showing all (ctrl+x unpins)", name, a.pin.Type)))
	}
	// Same honesty for segments: API-backed views bypass query:execute, so
	// the global scope doesn't reach them (the header pill dims too).
	if a.segmentsRejected(name) {
		return tea.Batch(nav, statusErr(fmt.Sprintf("%s is API-backed — segments don't apply", name)))
	}
	return nav
}

// openNav routes a command-bar navigator jump: no argument opens the
// overview, an entity id walks from it, anything else browses it as a type
// (case-insensitive — Smartscape types are upper snake case).
// switchContext handles :ctx — without an argument it opens the picker over
// the configured contexts; with a name it rebuilds the tenant wiring in a
// background command and resets the session onto the new context. Session-local
// by contract (the same rule as --context / DTCTL_CONTEXT): the switcher never
// writes the shared config, so an open TUI cannot repoint scripts and agents
// using dtctl on the same machine.
func (a *app) switchContext(arg string) tea.Cmd {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return a.openContextPicker()
	}
	if a.opts.SwitchContext == nil {
		return statusErr("context switching is not wired up in this session")
	}
	if arg == a.opts.ContextName {
		return status(fmt.Sprintf("already on context %s", arg))
	}
	a.ctxSwitchSeq++
	seq := a.ctxSwitchSeq
	switchFn := a.opts.SwitchContext
	return tea.Batch(
		status(fmt.Sprintf("switching to context %s…", arg)),
		func() tea.Msg {
			w, err := switchFn(arg)
			return ctxSwitchedMsg{seq: seq, wiring: w, err: err}
		},
	)
}

// handleCtxSwitched applies a finished context switch: swap the tenant
// wiring and drop every piece of tenant-specific state — the pin, applied
// segments, the semantic-dictionary cache, and both view stacks (entity ids
// and fetched data don't survive the tenant boundary; in-flight results
// route to the discarded views and die with them). The session lands on the
// home view; the timeframe is the one global that carries over.
func (a *app) handleCtxSwitched(msg ctxSwitchedMsg) tea.Cmd {
	if msg.seq != a.ctxSwitchSeq {
		return nil
	}
	if msg.err != nil {
		return statusErr(fmt.Sprintf("context switch failed: %v", msg.err))
	}
	w := msg.wiring
	a.opts.ContextName, a.opts.Environment, a.opts.SafetyLevel = w.ContextName, w.Environment, w.SafetyLevel
	a.opts.SegmentSource = w.SegmentSource
	a.ds.exec = w.Executor
	a.ds.sources = w.Sources
	a.ds.segments = nil
	a.ds.dict, a.ds.dictRequested = nil, false
	a.pin = nil
	a.segApplied, a.segVars, a.segList, a.segChecked = nil, nil, nil, nil
	a.segPending, a.segPaused, a.segLoading = false, false, false
	a.segErr = ""
	a.hist.ctx = w.ContextName // H now records and lists the new context
	cmd := a.navigate(newHomeView(a.ds, a.tf), true)
	a.prev = nil // '-' must not resurrect the old tenant's views
	return tea.Batch(cmd, status(fmt.Sprintf("context %s — %s", w.ContextName, envHost(w.Environment))))
}

// openNav routes a :nav argument — entity id, type, or name.
func (a *app) openNav(arg string) tea.Cmd {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return a.jumpTo("nav", "")
	}
	return a.navigate(a.navViewFor(arg), true)
}

// applyFacetBelow routes an inspector's facet request to the nearest list
// beneath the current page — a table on the stack, or a detail page whose
// active tab is one — popping down to it. The target must actually carry the
// field: a facet on a missing field would silently empty the list, the exact
// trap the picker avoids by deriving attributes from fetched records.
func (a *app) applyFacetBelow(msg applyFacetMsg) tea.Cmd {
	for i := len(a.stack) - 2; i >= 0; i-- {
		var tv *tableView
		switch v := a.stack[i].(type) {
		case *tableView:
			tv = v
		case *detailView:
			tv, _ = v.tabs[v.active].view.(*tableView)
		case *problemView:
			tv, _ = v.tabs[v.active].view.(*tableView)
		}
		if tv == nil {
			continue
		}
		if tv.spec.API != "" {
			// API-backed lists can't take a record-attribute facet: either
			// no DQL exists (slos), or the records aren't the query's rows
			// (patterns) and the filter would null out on the pipeline.
			return statusErr(fmt.Sprintf("%s is API-backed — facets don't apply", tv.spec.Name))
		}
		if !tv.hasField(msg.field) {
			return statusErr(fmt.Sprintf("%s rows carry no %s field", tv.spec.Name, msg.field))
		}
		a.prev = a.stack
		a.stack = a.stack[:i+1]
		return tv.addFacet(catalog.Facet{Field: msg.field, Value: msg.value, Tokens: msg.tokens})
	}
	return statusErr("no list view beneath to facet")
}

// selection asks the current view for its highlighted row and entity.
func (a *app) selection() (map[string]any, *catalog.Entity) {
	if sp, ok := a.top().(selectionProvider); ok {
		return sp.Selection()
	}
	return nil, nil
}

// togglePin pins the selected entity as the global scope (⌖ in the header);
// pinning the pinned entity — or pressing '.' with nothing to pin — unpins.
func (a *app) togglePin() tea.Cmd {
	_, entity := a.selection()
	if entity == nil || (a.pin != nil && a.pin.ID == entity.ID) {
		if a.pin == nil {
			return statusErr("selection carries no entity to pin")
		}
		a.pin = nil
		return status("scope unpinned")
	}
	a.pin = entity
	return status(fmt.Sprintf("pinned %s — command-bar views now scope to it (ctrl+x unpins)", entityName(*entity)))
}

// yankSelection copies the most specific value under the cursor: the
// selected field on an inspector, trace id on a waterfall, entity id
// elsewhere.
func (a *app) yankSelection() tea.Cmd {
	if yp, ok := a.top().(yankProvider); ok {
		if text, label, ok := yp.YankText(); ok {
			yank(text)
			return status("copied " + label)
		}
	}
	if tp, ok := a.top().(traceProvider); ok {
		yank(tp.TraceID())
		return status("copied trace id " + tp.TraceID())
	}
	rec, entity := a.selection()
	switch {
	case entity != nil && entity.ID != "":
		yank(entity.ID)
		return status("copied " + entity.ID)
	case rec != nil && catalog.Str(rec, "id") != "":
		yank(catalog.Str(rec, "id"))
		return status("copied " + catalog.Str(rec, "id"))
	}
	return statusErr("nothing to copy here")
}

// openSelection deep-links the selection into the Dynatrace UI. One target
// opens directly; several open the "open with" picker (record app, carried
// URLs, trace, entity app, topology, query as notebook).
func (a *app) openSelection() tea.Cmd {
	rec, entity := a.selection()
	traceID := ""
	if tp, ok := a.top().(traceProvider); ok {
		traceID = tp.TraceID()
	}
	dql := ""
	if p, ok := a.top().(dqlProvider); ok {
		dql = p.DQL()
	}
	opts := linkOptionsFor(a.opts.Environment, rec, entity, traceID, dql)
	if len(opts) == 0 {
		return statusErr("nothing to open here")
	}
	if len(opts) == 1 {
		return a.openLink(opts[0])
	}
	a.openList = opts
	a.openSel = 0
	a.openActive = true
	return nil
}

// openLink launches the browser on one picker target.
func (a *app) openLink(o linkOption) tea.Cmd {
	if err := openBrowser(o.URL); err != nil {
		return statusErr("browser: " + err.Error())
	}
	return status("opened " + o.Label)
}

// updateOpenPicker drives the "open with" overlay.
func (a *app) updateOpenPicker(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "o":
		a.openActive = false
		return nil
	case "up", "k":
		if a.openSel > 0 {
			a.openSel--
		}
		return nil
	case "down", "j", "tab":
		if a.openSel < len(a.openList)-1 {
			a.openSel++
		}
		return nil
	case "y":
		if a.openSel < len(a.openList) {
			yank(a.openList[a.openSel].URL)
			a.openActive = false
			return status("copied url — " + a.openList[a.openSel].Label)
		}
		return nil
	case "enter":
		a.openActive = false
		if a.openSel < len(a.openList) {
			return a.openLink(a.openList[a.openSel])
		}
		return nil
	}
	if len(msg.String()) == 1 {
		if idx := int(msg.String()[0] - '1'); idx >= 0 && idx < len(a.openList) {
			a.openActive = false
			return a.openLink(a.openList[idx])
		}
	}
	return nil
}

// renderOpenPicker renders the "open with" overlay.
func (a *app) renderOpenPicker() string {
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render("open with") + "\n\n")
	for i, o := range a.openList {
		label := fmt.Sprintf("%d · %s", i+1, o.Label)
		if i == a.openSel {
			b.WriteString(theme.Selected.Render(pad(" "+label, 44)))
		} else {
			b.WriteString(" " + theme.HeaderVal.Render(pad(label, 43)))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n" + theme.Dim.Render("enter/1-9 open · y yank url · esc cancel"))
	return b.String()
}

// quit records the final stack — "where I left off" for the next session —
// before stopping the program.
func (a *app) quit() tea.Cmd {
	a.recordHistory()
	return tea.Quit
}

func (a *app) scheduleRefresh() tea.Cmd {
	gen := a.refreshGen
	return tick(refreshIntervals[a.refreshIdx], func(time.Time) tea.Msg {
		return refreshTickMsg{gen: gen}
	})
}

func (a *app) broadcast(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for _, v := range a.stack {
		if cmd := v.Update(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// --- command bar --------------------------------------------------------------

func (a *app) updateCmdbar(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		a.cmdActive = false
		a.cmdInput.Blur()
		return nil
	case "tab", "down":
		if len(a.cmdMatches) > 0 {
			a.cmdSel = (a.cmdSel + 1) % len(a.cmdMatches)
		}
		return nil
	case "shift+tab", "up":
		if len(a.cmdMatches) > 0 {
			a.cmdSel = (a.cmdSel + len(a.cmdMatches) - 1) % len(a.cmdMatches)
		}
		return nil
	case "enter":
		a.cmdActive = false
		a.cmdInput.Blur()
		input := strings.Fields(strings.TrimSpace(a.cmdInput.Value()))
		if len(input) == 0 {
			// Empty input still has a highlighted suggestion (tab/down cycles
			// it) — enter opens it rather than silently dropping the choice.
			if a.cmdSel < len(a.cmdMatches) {
				return a.jumpTo(a.cmdMatches[a.cmdSel].Name, "")
			}
			return nil
		}
		arg := strings.Join(input[1:], " ")
		switch input[0] {
		case "q", "quit":
			return a.quit()
		case "help":
			a.helpActive = true
			return nil
		case "history", "hist":
			return a.openHistory()
		case "home", "query", "dql":
			return a.jumpTo(input[0], "")
		case "nav", "smartscape", "navigator":
			return a.openNav(arg)
		case "trace":
			if arg == "" {
				return statusErr("usage: trace <trace-id>")
			}
			return func() tea.Msg { return waterfallMsg{traceID: arg} }
		case "segments", "seg":
			return a.openSegmentPicker()
		case "ctx", "context":
			return a.switchContext(arg)
		}
		spec := catalog.Lookup(input[0])
		if spec == nil && a.cmdSel < len(a.cmdMatches) {
			spec = a.cmdMatches[a.cmdSel]
		}
		if spec == nil {
			return statusErr(fmt.Sprintf("unknown view %q", input[0]))
		}
		// A highlighted bespoke entry (partial input like ":na") routes like
		// its exact-name special above — never into newTableView.
		switch spec.Name {
		case "home", "query":
			return a.jumpTo(spec.Name, "")
		case "nav":
			return a.openNav(arg)
		case "segments":
			return a.openSegmentPicker()
		case "ctx":
			return a.switchContext(arg)
		}
		// Arguments narrow the jump (":pods checkout" pre-fills the filter).
		return a.jumpTo(spec.Name, arg)
	}
	var cmd tea.Cmd
	a.cmdInput, cmd = a.cmdInput.Update(msg)
	a.updateCmdMatches()
	return cmd
}

// bespokeSpecs are the command palette's entries for the non-catalog screens,
// so :nav and friends are discoverable by scanning or typing. Display-only
// stubs — deliberately NOT in the catalog registry (Lookup must never hand
// them to newTableView; the enter path routes them by name).
var bespokeSpecs = []*catalog.Spec{
	{Name: "home", Desc: "Triage landing page"},
	{Name: "query", Aliases: []string{"dql"}, Desc: "DQL escape hatch"},
	{Name: "nav", Aliases: []string{"smartscape", "navigator"}, Desc: "Smartscape topology navigator"},
	{Name: "segments", Aliases: []string{"seg"}, Desc: "Filter segments — global DQL scope"},
	{Name: "ctx", Aliases: []string{"context"}, Desc: "Switch dtctl context (session-local)"},
}

func (a *app) updateCmdMatches() {
	first := ""
	if fields := strings.Fields(a.cmdInput.Value()); len(fields) > 0 {
		first = fields[0]
	}
	candidates := append(append([]*catalog.Spec{}, bespokeSpecs...), catalog.All()...)
	a.cmdMatches = catalog.MatchSpecs(candidates, first)
	a.cmdSel = 0
}

// --- timeframe picker ---------------------------------------------------------

func (a *app) updateTfPicker(msg tea.KeyMsg) tea.Cmd {
	// The custom entry: a focused text input for any relative window ("45m",
	// "12h", "3d" — the same labels the workspace file takes).
	if a.tfCustom {
		switch msg.String() {
		case "esc":
			a.tfCustom = false
			a.tfInput.Blur()
			return nil
		case "enter":
			label := strings.TrimSpace(a.tfInput.Value())
			tf, ok := catalog.ParseTimeframe(label)
			if !ok {
				return statusErr(fmt.Sprintf("%q is not a relative window (45m, 12h, 3d)", label))
			}
			a.tfCustom = false
			a.tfInput.Blur()
			a.tfActive = false
			return a.setTimeframe(tf)
		}
		var cmd tea.Cmd
		a.tfInput, cmd = a.tfInput.Update(msg)
		return cmd
	}

	entries := len(catalog.Timeframes) + 1 // presets + the custom entry
	openCustom := func() tea.Cmd {
		a.tfSel = len(catalog.Timeframes)
		a.tfCustom = true
		a.tfInput.SetValue("")
		a.tfInput.Focus()
		return textinput.Blink
	}
	switch msg.String() {
	case "esc", "t":
		a.tfActive = false
		return nil
	case "left", "h", "up", "k":
		a.tfSel = (a.tfSel + entries - 1) % entries
		return nil
	case "right", "l", "down", "j", "tab":
		a.tfSel = (a.tfSel + 1) % entries
		return nil
	case "enter":
		if a.tfSel >= len(catalog.Timeframes) {
			return openCustom()
		}
		a.tfActive = false
		return a.setTimeframe(catalog.Timeframes[a.tfSel])
	}
	if idx := strings.IndexByte("123456789", msg.String()[0]); idx >= 0 && idx < entries && len(msg.String()) == 1 {
		if idx == len(catalog.Timeframes) {
			return openCustom()
		}
		a.tfActive = false
		a.tfSel = idx
		return a.setTimeframe(catalog.Timeframes[idx])
	}
	return nil
}

// setTimeframe applies the global window to every live view — not just the
// top one — so a covered view exposed by esc or '-' (and its 'r'/auto-refresh
// refetch) uses the window the header advertises.
func (a *app) setTimeframe(tf catalog.Timeframe) tea.Cmd {
	a.tf = tf
	seen := map[viewModel]bool{}
	var cmds []tea.Cmd
	for _, v := range append(append([]viewModel{}, a.stack...), a.prev...) {
		if seen[v] {
			continue
		}
		seen[v] = true
		if cmd := v.SetTimeframe(tf); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// --- rendering ------------------------------------------------------------------

func (a *app) View() string {
	if a.width == 0 {
		return "loading…"
	}

	header := a.renderHeader()
	body := a.renderBody()
	footer := a.renderFooter()

	// Pin the footer to the bottom row.
	bodyH := a.bodyHeight()
	body = lipgloss.NewStyle().MaxHeight(bodyH).Render(body)
	if pad := bodyH - lipgloss.Height(body); pad > 0 {
		body += strings.Repeat("\n", pad)
	}
	return header + "\n" + body + "\n" + footer
}

func (a *app) renderHeader() string {
	sep := theme.HeaderSep.Render("  ·  ")
	ctx := theme.HeaderKey.Render("ctx:") + theme.HeaderVal.Render(a.opts.ContextName)
	if host := envHost(a.opts.Environment); host != "" {
		ctx += theme.HeaderKey.Render(" @ " + host)
	}
	left := []string{
		" " + theme.Wordmark(),
		ctx,
		theme.Safety(a.opts.SafetyLevel).Render("● " + a.opts.SafetyLevel),
	}
	if a.pin != nil {
		left = append(left, theme.Pin.Render("⌖ "+strings.ToLower(a.pin.Type)+":"+entityName(*a.pin)))
	}
	switch {
	case a.segPaused && len(a.segApplied) > 0:
		left = append(left, theme.Dim.Render("◌ "+segmentSummary(a.segApplied)+" off"))
	case len(a.segApplied) > 0:
		// Dim on API-backed views — the one surface the scope doesn't reach —
		// so the header never claims a filter that wasn't applied.
		style := theme.Segment
		if tv, ok := a.top().(*tableView); ok && tv.spec.API != "" {
			style = theme.Dim
		}
		left = append(left, style.Render("◐ "+segmentSummary(a.segApplied)))
	case a.segPending:
		left = append(left, theme.Dim.Render("◌ segments…"))
	}
	var right []string
	if iv := refreshIntervals[a.refreshIdx]; iv > 0 {
		right = append(right, theme.StatusOK.Render("⟳ "+iv.String()))
	}
	right = append(right, theme.HeaderKey.Render("last ")+theme.HeaderVal.Render(a.tf.Label))

	l := strings.Join(left, sep)
	r := strings.Join(right, sep) + " "
	var line1 string
	if gap := a.width - lipgloss.Width(l) - lipgloss.Width(r); gap >= 1 {
		line1 = l + strings.Repeat(" ", gap) + r
	} else {
		line1 = ansi.Truncate(l+" "+r, a.width, "…")
	}

	var line2 string
	if a.cmdActive {
		line2 = " " + a.cmdInput.View()
	} else {
		crumbs := make([]string, len(a.stack))
		for i, v := range a.stack {
			label := v.Crumb()
			if i == len(a.stack)-1 {
				crumbs[i] = theme.Crumb.Render(label)
			} else {
				crumbs[i] = theme.CrumbDim.Render(label)
			}
		}
		line2 = " " + strings.Join(crumbs, theme.CrumbDim.Render(" › "))
		if fill := a.width - lipgloss.Width(line2) - 2; fill > 0 {
			line2 += " " + theme.Rule.Render(strings.Repeat("─", fill))
		}
	}
	return line1 + "\n" + ansi.Truncate(line2, a.width, "…")
}

// renderCmdPalette renders the command-bar matches as a picker overlay: view
// name, aliases, and description, with the current suggestion highlighted.
func (a *app) renderCmdPalette() string {
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render("views") + "\n\n")
	nameW, aliasW, descW := 14, 12, 36
	rowW := nameW + aliasW + descW + 3
	limit := len(a.cmdMatches)
	if m := max(a.bodyHeight()-8, 4); limit > m {
		limit = m
	}
	for i := 0; i < limit; i++ {
		s := a.cmdMatches[i]
		row := pad(s.Name, nameW) + " " + pad(strings.Join(s.Aliases, " "), aliasW) + " " + pad(s.Desc, descW)
		if i == a.cmdSel {
			b.WriteString(theme.Selected.Render(pad(" "+row, rowW)))
		} else {
			b.WriteString(" " + theme.HeaderVal.Render(pad(s.Name, nameW)) + " " +
				theme.Dim.Render(pad(strings.Join(s.Aliases, " "), aliasW)) + " " +
				theme.CrumbDim.Render(pad(s.Desc, descW)))
		}
		b.WriteString("\n")
	}
	if len(a.cmdMatches) == 0 {
		b.WriteString(theme.Dim.Render(" no matching view") + "\n")
	}
	if rest := len(a.cmdMatches) - limit; rest > 0 {
		b.WriteString(theme.Dim.Render(fmt.Sprintf(" … %d more", rest)) + "\n")
	}
	b.WriteString("\n" + theme.Dim.Render("tab next · enter open · also :history :trace <id>"))
	return b.String()
}

func (a *app) renderBody() string {
	bodyH := a.bodyHeight()
	if a.helpActive {
		return overlay(a.width, bodyH, a.renderHelp())
	}
	if a.tfActive {
		return overlay(a.width, bodyH, a.renderTfPicker())
	}
	if a.histActive {
		return overlay(a.width, bodyH, a.renderHistory())
	}
	if a.openActive {
		return overlay(a.width, bodyH, a.renderOpenPicker())
	}
	if a.segVar.active {
		return overlay(a.width, bodyH, a.renderSegVarPicker())
	}
	if a.segPickActive {
		return overlay(a.width, bodyH, a.renderSegPicker())
	}
	if a.ctxPickActive {
		return overlay(a.width, bodyH, a.renderCtxPicker())
	}
	if a.cmdActive {
		return lipgloss.Place(a.width, bodyH, lipgloss.Center, lipgloss.Position(0.2),
			theme.OverlayBox.Render(a.renderCmdPalette()))
	}
	return a.top().View(a.width, bodyH)
}

func (a *app) renderFooter() string {
	var hints []keyHint
	switch {
	case a.cmdActive:
		// Command bar owns the keyboard: only its keys work.
		hints = []keyHint{{"enter", "open"}, {"tab", "next match"}, {"esc", "cancel"}}
	case a.histActive:
		hints = []keyHint{{"enter", "restore"}, {"j/k", "move"}, {"esc", "close"}}
	case a.openActive:
		hints = []keyHint{{"enter", "open"}, {"y", "yank url"}, {"esc", "cancel"}}
	case a.segVar.active:
		hints = []keyHint{{"space", "toggle"}, {"/", "filter"}, {"enter", "bind"}, {"esc", "back"}}
	case a.segPickActive:
		hints = []keyHint{{"space", "toggle"}, {"v", "values"}, {"enter", "apply"}, {"c", "clear"}, {"esc", "cancel"}}
	case a.ctxPickActive:
		hints = []keyHint{{"enter", "switch"}, {"j/k", "move"}, {"esc", "cancel"}}
	case a.top().InputActive():
		// A view's text input (filter, search, query editor) is focused — the
		// global keys would just type characters, so show only the view's own
		// input-mode hints (the complete set for that mode).
		hints = a.top().Hints()
	default:
		hints = append(a.top().Hints(),
			keyHint{":", "views"}, keyHint{"t", "timeframe"}, keyHint{"S", "segments"}, keyHint{"r", "refresh"})
		// "esc back" at the stack root would advertise a no-op.
		if len(a.stack) > 1 {
			hints = append(hints, keyHint{"esc", "back"})
		}
		hints = append(hints, keyHint{"?", "help"}, keyHint{"q", "quit"})
	}
	var parts []string
	for _, h := range hints {
		parts = append(parts, theme.KeyHint.Render(h.Key)+" "+theme.KeyDesc.Render(h.Desc))
	}
	line1 := ansi.Truncate(" "+strings.Join(parts, "  "), a.width, "…")

	var line2 string
	switch {
	case a.status != "" && a.statusErr:
		line2 = " " + theme.Error.Render("✗ "+a.status)
	case a.status != "":
		line2 = " " + theme.StatusOK.Render("✓ "+a.status)
	default:
		if echo := a.top().Echo(); echo != "" {
			line2 = " " + theme.Echo.Render("≡ "+echo)
		}
	}
	return line1 + "\n" + ansi.Truncate(line2, a.width, "…")
}

func (a *app) renderHelp() string {
	sections := []struct {
		title string
		keys  []keyHint
	}{
		{"Navigation", []keyHint{
			{":", "command bar — fuzzy view names, args filter (:pods checkout, :trace <id>, :nav <type|id|name>, :ctx [name] — bare :ctx opens a picker)"},
			{"enter", "detail / drill into children / follow entity link / expand value / waterfall / session timeline"},
			{"0-9", "global bookmarks: 0 home · 1 problems · 2 services · 3 hosts · 4 pods · 5 logs · 6 traces · 7 workloads · 8 events · 9 aws — on an entered page 1-9 address the innermost numbered strip: its tabs, or the active tab's lens strip when it shows one (0 still jumps home, esc restores all bookmarks)"},
			{"esc / -", "back / toggle last two views"},
			{"tab", "cycle the view's primary strip: lens strip on tables · tabs on detail pages · panels on home"},
			{"[ / ]", "cycle the lens strip explicitly (the only way for a strip nested inside a detail tab)"},
			{"P", "preview pane on/off — the peek at the selected row is on by default and auto-hides on cramped terminals"},
			{"H", "history — restore a previous page (survives restarts)"},
			{"/", "filter table (live) — enter adds it as a server-side search, alt+enter replaces"},
			{"f / F", "facet manager: add attribute=value filters (fieldsSummary top values, * patterns), edit/remove each / clear all"},
			{"J/K", "sort column/direction"},
			{"j/k ↑/↓ g/G", "move"},
			{"pgup/pgdn", "page jump (ctrl+d/u half page in inspectors)"},
		}},
		{"Drill-down (pre-scoped to selection; on detail pages the letters jump to the matching tab)", []keyHint{
			{"l", "logs"},
			{"s", "traces (spans) / jump to a log's or RUM event's trace"},
			{"m", "metrics — canned charts, or the metric explorer for other types"},
			{"p", "problems"},
			{"v", "events"},
			{"a", "log patterns — Davis clustering of the current logs (enter: records with the pattern's fields parsed out)"},
			{"u", "sessions of a frontend / session timeline of a RUM event"},
			{"e", "user events of a frontend / of a session"},
			{"x / X", "smartscape navigator — walk the topology from the selection (:nav); one-hop relations live on the detail page's related tab"},
			{"d", "describe / details"},
		}},
		{"Scope & actions", []keyHint{
			{".", "pin selection as global scope (ctrl+x unpins)"},
			{"t", "timeframe picker — presets or a custom relative window (45m, 12h, 3d)"},
			{"S", "segments — up to 10 filter segments applied to every DQL view (:segments); v picks variable values; a .dynatrace.yaml in the project pre-selects them"},
			{"alt+s", "segments on/off — suspend the applied set for the unfiltered picture, restore it with bindings intact"},
			{"ctrl+q", "reveal query — this view's DQL in the editor"},
			{"o", "open in the Dynatrace UI — a picker appears when several targets apply"},
			{"y / c", "yank id / copy CLI command"},
		}},
		{"Global", []keyHint{
			{"r / R", "refresh / cycle auto-refresh"},
			{"?", "help"},
			{"q", "quit"},
		}},
	}
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render("dtctl tui — keys") + "\n")
	for _, s := range sections {
		b.WriteString("\n" + theme.Section(s.title) + "\n")
		for _, h := range s.keys {
			b.WriteString(fmt.Sprintf("  %s %s\n",
				theme.KeyHint.Render(fmt.Sprintf("%-12s", h.Key)), h.Desc))
		}
	}
	b.WriteString("\n" + theme.Dim.Render(wrap("views: home · query · nav · "+strings.Join(catalog.Names(), " · "), 76)))
	return b.String()
}

func (a *app) renderTfPicker() string {
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render("timeframe") + "\n\n")
	custom := "custom"
	if _, preset := presetIndex(a.tf.Label); !preset {
		custom = "custom (" + a.tf.Label + ")" // the applied window when it isn't a preset
	}
	labels := make([]string, 0, len(catalog.Timeframes)+1)
	for _, tf := range catalog.Timeframes {
		labels = append(labels, tf.Label)
	}
	labels = append(labels, custom)
	pills := make([]string, len(labels))
	for i, l := range labels {
		label := fmt.Sprintf("%d · %s", i+1, l)
		if i == a.tfSel {
			pills[i] = theme.TabActive.Render(label)
		} else {
			pills[i] = theme.TabInactive.Render(label)
		}
	}
	b.WriteString(strings.Join(pills, " ") + "\n")
	if a.tfCustom {
		b.WriteString("\n" + a.tfInput.View() + "\n")
		b.WriteString("\n" + theme.Dim.Render("a relative window: 45m · 12h · 3d — enter apply · esc back"))
		return b.String()
	}
	b.WriteString("\n" + theme.Dim.Render(fmt.Sprintf("enter/1-%d apply · esc cancel", len(labels))))
	return b.String()
}

// presetIndex finds a timeframe label among the picker presets.
func presetIndex(label string) (int, bool) {
	for i, tf := range catalog.Timeframes {
		if tf.Label == label {
			return i, true
		}
	}
	return 0, false
}

func overlay(width, height int, content string) string {
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center,
		theme.OverlayBox.Render(content))
}

// envHost extracts the environment's hostname for the header — the
// recognizable "…apps.dynatrace.com" identity next to the context name.
func envHost(env string) string {
	host := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(env, "https://"), "http://"), "/")
	return host
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
