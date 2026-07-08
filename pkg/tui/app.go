// Package tui implements the interactive terminal UI behind `dtctl tui` — a
// k9s-style navigator over observability primitives (see docs/dev/TUI_DESIGN.md).
// It is a presentation layer over pkg/exec and the catalog's DQL templates;
// nothing outside cmd/tui.go imports it.
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
	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
	"github.com/dynatrace-oss/dtctl/pkg/tui/theme"
)

// Options wires the TUI to the current dtctl context.
type Options struct {
	ContextName string
	Environment string
	SafetyLevel string
	Executor    *exec.DQLExecutor
	// Sources backs the API views (catalog.Spec.API): "slos",
	// "anomaly-detectors", "log-patterns". Constructed in cmd/tui.go so no
	// HTTP lives in pkg/tui.
	Sources     map[string]Source
	InitialView string // catalog view name or alias; "" = problems
	HistoryPath string // navigation-history file; "" = in-memory only
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

// digitClaimer is implemented by the tabbed pages (entity detail, problem),
// whose tab strip — or the active tab's lens strip — takes digit keys over
// from the global hotkeys.
type digitClaimer interface {
	claimsDigit(d byte) bool
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
	tfSel      int
	histActive bool
	histSel    int
	histList   []historyEntry // snapshot shown by the open picker

	// "open with" picker ('o' with several browser targets).
	openActive bool
	openSel    int
	openList   []linkOption

	hist *historyStore

	status    string
	statusErr bool

	refreshIdx int
	refreshGen int

	spinning bool // spinner ticker scheduled
}

type refreshTickMsg struct{ gen int }

// spinnerTickMsg advances the loading-spinner animation while the visible
// view is busy.
type spinnerTickMsg struct{}

func newApp(opts Options) (*app, error) {
	ci := textinput.New()
	ci.Prompt = ":"
	ci.PromptStyle = theme.Crumb
	ci.CharLimit = 64
	a := &app{
		opts: opts,
		ds:   &dataSource{exec: opts.Executor, sources: opts.Sources},
		tf:   catalog.DefaultTimeframe,
		hist: loadHistory(opts.HistoryPath, opts.ContextName),
	}
	a.cmdInput = ci
	for i, tf := range catalog.Timeframes {
		if tf.Label == a.tf.Label {
			a.tfSel = i
		}
	}

	initial, err := a.viewFor(opts.InitialView)
	if err != nil {
		return nil, err
	}
	a.stack = []viewModel{initial}
	return a, nil
}

// viewFor resolves a view name to a fresh view: the bespoke screens (home,
// query) or a catalog table.
func (a *app) viewFor(name string) (viewModel, error) {
	switch name {
	case "", "home":
		return newHomeView(a.ds, a.tf), nil
	case "query", "dql":
		return newQueryView(a.ds, "", a.tf), nil
	case "nav", "smartscape", "navigator":
		return newNavView(a.ds, a.tf), nil
	}
	spec := catalog.Lookup(name)
	if spec == nil {
		return nil, fmt.Errorf("unknown view %q (available: home, query, nav, %s)", name, strings.Join(catalog.Names(), ", "))
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

func (a *app) Init() tea.Cmd { return a.top().Init() }

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

	case metricsMsg:
		return a.navigate(newMetricsView(a.ds, msg.entity, a.tf), false)

	case metricChartMsg:
		var entity catalog.Entity
		if msg.entity != nil {
			entity = *msg.entity
		}
		return a.navigate(newMetricChartView(a.ds, msg.key, entity, a.tf), false)

	case waterfallMsg:
		return a.navigate(newWaterfallView(a.ds, msg.traceID, a.tf), false)

	case timelineMsg:
		return a.navigate(newTimelineView(a.ds, msg.sessionID, msg.rec, a.tf), false)

	case relationsMsg:
		return a.navigate(newRelationsView(a.ds, msg.entity, a.tf), false)

	case navMsg:
		switch {
		case msg.root != nil:
			return a.navigate(newNavWalkView(a.ds, *msg.root, a.tf), msg.replace)
		case msg.typ != "":
			return a.navigate(newNavBrowserView(a.ds, msg.typ, a.tf), msg.replace)
		default:
			return a.navigate(newNavView(a.ds, a.tf), msg.replace)
		}

	case queryMsg:
		return a.navigate(newQueryView(a.ds, msg.dql, a.tf), false)

	case statusMsg:
		a.status, a.statusErr = msg.text, msg.isErr
		return nil

	case historyMarkMsg:
		a.recordHistory()
		return nil

	case applyFacetMsg:
		return a.applyFacetBelow(msg.field, msg.value)

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
	return tea.Tick(90*time.Millisecond, func(time.Time) tea.Msg { return spinnerTickMsg{} })
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
	if a.helpActive {
		a.helpActive = false
		return nil
	}

	top := a.top()
	if !top.InputActive() {
		// Digit hotkeys jump to bookmarked views. On a detail page the digits
		// that name a tab (1..N) switch tabs instead — likewise the digits
		// naming a lens on a lensed table; the rest (0, and any past the last
		// tab) still hit their hotkey, so '0' → home works everywhere the
		// help overlay promises it does.
		if len(key) == 1 && key[0] >= '0' && key[0] <= '9' {
			claimedByTab := false
			if dc, isTabbed := top.(digitClaimer); isTabbed {
				// The page tabs — or the active tab's own lens strip, which
				// takes the digits over while it is visible.
				claimedByTab = dc.claimsDigit(key[0])
			}
			if tv, isTable := top.(*tableView); isTable {
				claimedByTab = key[0] >= '1' && key[0] < byte('1'+len(tv.spec.Lenses))
			}
			if _, isTimeline := top.(*timelineView); isTimeline {
				claimedByTab = key[0] >= '1' && key[0] < byte('1'+len(catalog.SessionTimelineLenses))
			}
			if !claimedByTab {
				if name, ok := hotkeys[key]; ok {
					return a.jumpTo(name, "")
				}
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
			return nil
		case "x":
			if _, entity := a.selection(); entity != nil {
				e := *entity
				return func() tea.Msg { return relationsMsg{entity: e} }
			}
			return statusErr("selection carries no entity for relations")
		case "X":
			// The capital sibling of x: the full navigator, rooted here.
			if _, entity := a.selection(); entity != nil {
				e := *entity
				return func() tea.Msg { return navMsg{root: &e} }
			}
			return statusErr("selection carries no entity to walk")
		case ".":
			return a.togglePin()
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
	return nav
}

// openNav routes a command-bar navigator jump: no argument opens the
// overview, an entity id walks from it, anything else browses it as a type
// (case-insensitive — Smartscape types are upper snake case).
func (a *app) openNav(arg string) tea.Cmd {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return a.jumpTo("nav", "")
	}
	if entityIDRe.MatchString(arg) {
		root := entityFromID(arg)
		return func() tea.Msg { return navMsg{root: root, replace: true} }
	}
	if typ := strings.ToUpper(arg); navTypeRe.MatchString(typ) {
		return func() tea.Msg { return navMsg{typ: typ, replace: true} }
	}
	return statusErr("usage: nav [<TYPE> | <entity-id>]")
}

// applyFacetBelow routes an inspector's facet request to the nearest list
// beneath the current page — a table on the stack, or a detail page whose
// active tab is one — popping down to it. The target must actually carry the
// field: a facet on a missing field would silently empty the list, the exact
// trap the picker avoids by deriving attributes from fetched records.
func (a *app) applyFacetBelow(field, value string) tea.Cmd {
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
		if !tv.hasField(field) {
			return statusErr(fmt.Sprintf("%s rows carry no %s field", tv.spec.Name, field))
		}
		a.prev = a.stack
		a.stack = a.stack[:i+1]
		return tv.addFacet(catalog.Facet{Field: field, Value: value})
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
	return tea.Tick(refreshIntervals[a.refreshIdx], func(time.Time) tea.Msg {
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
	switch msg.String() {
	case "esc", "t":
		a.tfActive = false
		return nil
	case "left", "h", "up", "k":
		a.tfSel = (a.tfSel + len(catalog.Timeframes) - 1) % len(catalog.Timeframes)
		return nil
	case "right", "l", "down", "j", "tab":
		a.tfSel = (a.tfSel + 1) % len(catalog.Timeframes)
		return nil
	case "enter":
		a.tfActive = false
		return a.setTimeframe(catalog.Timeframes[a.tfSel])
	}
	if idx := strings.IndexByte("1234", msg.String()[0]); idx >= 0 && idx < len(catalog.Timeframes) && len(msg.String()) == 1 {
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
	case a.top().InputActive():
		// A view's text input (filter, search, query editor) is focused — the
		// global keys would just type characters, so show only the view's own
		// input-mode hints (the complete set for that mode).
		hints = a.top().Hints()
	default:
		hints = append(a.top().Hints(),
			keyHint{":", "views"}, keyHint{"t", "timeframe"}, keyHint{"r", "refresh"},
			keyHint{"esc", "back"}, keyHint{"?", "help"}, keyHint{"q", "quit"},
		)
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
			{":", "command bar — fuzzy view names, args filter (:pods checkout, :trace <id>)"},
			{"enter", "detail / drill into children / follow entity link / expand value / waterfall / session timeline"},
			{"0-9", "hotkeys: 0 home · 1 problems · 2 services · 3 hosts · 4 pods · 5 logs · 6 traces · 7 workloads · 8 events · 9 aws"},
			{"esc / -", "back / toggle last two views"},
			{"tab / 1-N", "switch tab (detail pages) or lens (traces: roots · errors · server · client · db · genai · all)"},
			{"H", "history — restore a previous page (survives restarts)"},
			{"/", "filter table (live) — enter adds it as a server-side search, alt+enter replaces"},
			{"f / F", "facet manager: add attribute=value filters (fieldsSummary top values, * patterns), edit/remove each / clear all"},
			{"J/K", "sort column/direction"},
			{"j/k ↑/↓ g/G", "move"},
			{"pgup/pgdn", "page jump (ctrl+d/u half page in inspectors)"},
		}},
		{"Drill-down (pre-scoped to selection)", []keyHint{
			{"l", "logs"},
			{"s", "traces (spans) / jump to a log's or RUM event's trace"},
			{"m", "metrics — canned charts, or the metric explorer for other types"},
			{"p", "problems"},
			{"v", "events"},
			{"a", "log patterns — Davis clustering of the current logs (enter: records with the pattern's fields parsed out)"},
			{"u", "sessions of a frontend / session timeline of a RUM event"},
			{"e", "user events of a frontend / of a session"},
			{"x", "relations — one hop of Smartscape topology"},
			{"X", "smartscape navigator — walk the topology from the selection (:nav)"},
			{"d", "describe / details"},
		}},
		{"Scope & actions", []keyHint{
			{".", "pin selection as global scope (ctrl+x unpins)"},
			{"t", "timeframe picker"},
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
	pills := make([]string, len(catalog.Timeframes))
	for i, tf := range catalog.Timeframes {
		label := fmt.Sprintf("%d · %s", i+1, tf.Label)
		if i == a.tfSel {
			pills[i] = theme.TabActive.Render(label)
		} else {
			pills[i] = theme.TabInactive.Render(label)
		}
	}
	b.WriteString(strings.Join(pills, " ") + "\n")
	b.WriteString("\n" + theme.Dim.Render("enter/1-4 apply · esc cancel"))
	return b.String()
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
