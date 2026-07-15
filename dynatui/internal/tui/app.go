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

	// overlay is the open modal (nil = none): command palette, help,
	// timeframe/history/open-with/segment/context pickers. One at a time —
	// opening another replaces it; the segment picker nests its variable
	// sub-picker internally.
	overlay overlay

	// Global segment scope (the fourth global state after context, timeframe,
	// and pin) — mirrored here for the header and picker; the query-side truth
	// lives on ds.segments.
	segApplied []SegmentOption
	segVars    map[string][]exec.FilterSegmentVariable // uid → workspace bindings
	segPending bool                                    // workspace refs still resolving against the tenant list
	segPaused  bool                                    // alt+s — selection kept, nothing sent to queries
	segList    []SegmentOption                         // cached lister result (picker rows)
	segLoading bool
	segErr     string

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
		// The segment variable value fetch belongs to the sub-picker; results
		// for a closed or replaced picker instance die unrouted.
		if svp, ok := msg.owner.(*segVarPicker); ok {
			if sp, open := a.overlay.(*segmentPicker); open && sp.vars == svp {
				return svp.handleData(msg)
			}
			return nil
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

	// An open overlay owns the whole keyboard.
	if a.overlay != nil {
		return a.overlay.HandleKey(msg)
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
			return a.openCmdPalette()
		case "?":
			return a.openHelp()
		case "t":
			return a.openTimeframePicker()
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
			a.ds.previewOff = !a.ds.previewOff
			if a.ds.previewOff {
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
	a.segApplied, a.segVars, a.segList = nil, nil, nil
	a.segPending, a.segPaused, a.segLoading = false, false, false
	a.segErr = ""
	a.overlay = nil            // an open picker would show the old tenant's rows
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
	if cp, ok := a.overlay.(*cmdPalette); ok {
		// The command bar types into the header's breadcrumb line.
		line2 = " " + cp.input.View()
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

func (a *app) renderBody() string {
	if a.overlay != nil {
		return a.overlay.View(a.width, a.bodyHeight())
	}
	return a.top().View(a.width, a.bodyHeight())
}

func (a *app) renderFooter() string {
	var hints []keyHint
	overlayHints := []keyHint(nil)
	if a.overlay != nil {
		overlayHints = a.overlay.Hints()
	}
	switch {
	case overlayHints != nil:
		// The overlay owns the keyboard: only its keys work.
		hints = overlayHints
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
