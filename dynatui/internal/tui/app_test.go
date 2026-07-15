package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
)

// Bubbletea models are pure enough to drive with synthetic messages; no TTY
// or network is needed. Commands returned by Update are executed manually
// only when they are known to be side-effect free (navigation messages) —
// data-source commands are never run.

// A blinking cursor's focus/keystroke commands block for the 530ms blink
// interval, which deliver() would pay on every simulated key press into a
// focused input — static mode never emits them. Timer commands (spinner,
// preview debounce) would likewise sleep their full interval when deliver()
// executes them, so tests run them at zero delay.
func init() {
	inputCursorMode = cursor.CursorStatic
	tickDelay = func(time.Duration) time.Duration { return 0 }
}

func testApp(t *testing.T, initial string) *app {
	t.Helper()
	previewEnabled = true // the app-wide default; tests must not leak a toggle
	a, err := newApp(Options{ContextName: "test", SafetyLevel: "readonly", InitialView: initial})
	if err != nil {
		t.Fatal(err)
	}
	// Test seam: queries resolve to empty result sets instead of hitting HTTP.
	a.ds.runFn = func(string) ([]map[string]any, error) { return nil, nil }
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	deliver(a, a.Init())
	return a
}

// disablePreview turns the app-wide peek pane off for one test: navigator
// tests stay synchronous (no debounce ticks) and table tests measure the
// bare table.
func disablePreview(t *testing.T) {
	t.Helper()
	previewEnabled = false
	t.Cleanup(func() { previewEnabled = true })
}

func key(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// press sends a key and chases returned navigation messages back into the app.
func press(a *app, k tea.KeyMsg) {
	_, cmd := a.Update(k)
	deliver(a, cmd)
}

// deliver executes a command and feeds resulting messages back, resolving
// tea.Batch trees. Data-source results (from the runFn seam) are dropped so
// tests control view data via seedRows.
func deliver(a *app, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if msg == nil {
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			deliver(a, c)
		}
		return
	}
	if dm, isData := msg.(dataMsg); isData {
		// The scope-widening hop (Spec.Hop) is control flow, not view data:
		// its result must land so the view composes and issues its real list
		// query (the runFn seam returns no hop records — the scope stays the
		// plain entity). The list query's result is still dropped below.
		if _, isHop := dm.owner.(hopOwner); isHop {
			_, next := a.Update(msg)
			deliver(a, next)
		}
		return
	}
	// Spinner ticks would re-arm themselves forever while a view waits on
	// data that never arrives in tests; refresh ticks re-arm too, and at the
	// zero test tick delay either would recurse without bound.
	switch msg.(type) {
	case spinnerTickMsg, refreshTickMsg:
		return
	}
	_, next := a.Update(msg)
	deliver(a, next)
}

// seedRows injects fetched records into the top table view, bypassing the
// data source.
func seedRows(t *testing.T, a *app, rows []map[string]any) {
	t.Helper()
	tv, ok := a.top().(*tableView)
	if !ok {
		t.Fatalf("top view is %T, want *tableView", a.top())
	}
	tv.seq++
	tv.loading = true
	dql := ""
	if tv.spec.Query != nil {
		dql = tv.spec.Query(tv.scope)
	}
	tv.Update(dataMsg{owner: tv, seq: tv.seq, records: rows, elapsed: time.Second, dql: dql})
}

func problemRow() map[string]any {
	return map[string]any{
		"display_id":   "P-100",
		"event.kind":   "DAVIS_PROBLEM",
		"event.status": "ACTIVE",
		"event.name":   "Failure rate increase",
		"event.start":  time.Now().UTC().Format(time.RFC3339Nano),
		"smartscape.affected_entities": []any{
			map[string]any{"id": "SERVICE-1", "name": "checkout", "type": "SERVICE"},
		},
	}
}

func hostRow() map[string]any {
	return map[string]any{
		"id":                    "HOST-AAAABBBBCCCCDDDD",
		"name":                  "web-01.example.invalid",
		"type":                  "HOST",
		"os.type":               "OS_TYPE_LINUX",
		"os.version":            "Test Linux 1.0",
		"logical_cores":         "2",
		"cores":                 "1",
		"memory":                "8198213632",
		"ip":                    []any{"10.0.0.1"},
		"cloud.provider":        "aws",
		"aws.availability_zone": "us-east-1b",
		"sku":                   "t3.large",
		"lifetime":              map[string]any{"start": "2026-06-18T15:00:00.000000000Z", "end": "2026-07-05T20:51:00.000000000Z"},
	}
}

func TestEnterOnEntityRowOpensDetailTabs(t *testing.T) {
	a := testApp(t, "hosts")
	seedRows(t, a, []map[string]any{hostRow()})

	press(a, key("enter"))
	dv, ok := a.top().(*detailView)
	if !ok {
		t.Fatalf("enter on entity view should open detail page, top = %T", a.top())
	}
	if dv.Crumb() != "web-01.example.invalid" {
		t.Errorf("detail crumb = %q", dv.Crumb())
	}

	// The details tab renders instantly from the list row: tab bar, curated
	// key facts, and full properties without waiting for a fetch.
	body := a.top().View(120, 30)
	for _, want := range []string{"details", "processes", "related", "metrics", "logs", "events", "problems",
		"7.6 GiB", "2 logical / 1 physical", "aws us-east-1b", "HOST-AAAABBBBCCCCDDDD"} {
		if !strings.Contains(body, want) {
			t.Errorf("details tab missing %q:\n%s", want, body)
		}
	}

	// tab switches to the processes tab — the host's most relevant next
	// entity, pre-scoped to it by host name.
	press(a, key("tab"))
	if dv.active != 1 {
		t.Fatalf("tab should move to processes, active = %d", dv.active)
	}
	procs, ok := dv.tabs[1].view.(*tableView)
	if !ok || procs.spec.Name != "processes" ||
		!strings.Contains(procs.dql, `host.name == "web-01.example.invalid"`) {
		t.Fatalf("processes tab should be scoped to the host, dql = %q", procs.dql)
	}

	// shift+tab wraps backwards past details to the page's LAST tab —
	// related everywhere — and lazily starts its edge walk.
	press(a, key("shift+tab"))
	press(a, key("shift+tab"))
	rv, ok := dv.tabs[len(dv.tabs)-1].view.(*relationsView)
	if !ok || !strings.Contains(rv.dql, `source_id == toSmartscapeId("HOST-AAAABBBBCCCCDDDD")`) {
		t.Fatalf("related tab dql = %q", rv.dql)
	}

	// The drill letters jump straight to their tab: m opens metrics, which
	// starts its availability probe (the chart query follows once the probe
	// returns).
	press(a, key("m"))
	mv, ok := dv.tabs[2].view.(*metricsView)
	if !ok || !strings.Contains(mv.dql, "metrics from:") ||
		!strings.Contains(mv.dql, `toSmartscapeId("HOST-AAAABBBBCCCCDDDD")`) {
		t.Fatalf("metrics tab dql = %q", mv.dql)
	}

	// The logs tab is pre-scoped to the host.
	press(a, key("l"))
	logs, ok := dv.tabs[dv.active].view.(*tableView)
	if !ok || logs.spec.Name != "logs" {
		t.Fatalf("'l' should activate logs tab, active view = %T", dv.tabs[dv.active].view)
	}
	if !strings.Contains(logs.dql, `dt.smartscape.host == toSmartscapeId("HOST-AAAABBBBCCCCDDDD")`) {
		t.Errorf("logs tab not scoped to host:\n%s", logs.dql)
	}
	if echo := a.top().Echo(); !strings.Contains(echo, "fetch logs") {
		t.Errorf("footer echo should follow the active tab, got %q", echo)
	}

	// esc pops the whole detail page back to the hosts list.
	press(a, key("esc"))
	if tv, ok := a.top().(*tableView); !ok || tv.spec.Name != "hosts" {
		t.Fatalf("esc should return to hosts, top = %v", a.top().Crumb())
	}
}

func TestDetailTabEscClearsChildFilterBeforePopping(t *testing.T) {
	a := testApp(t, "hosts")
	seedRows(t, a, []map[string]any{hostRow()})
	press(a, key("enter"))
	press(a, key("l")) // logs tab

	dv := a.top().(*detailView)
	logs := dv.tabs[dv.active].view.(*tableView)

	press(a, key("/"))
	press(a, key("x"))
	press(a, key("enter")) // promote to a server-side search
	if len(logs.searches) != 1 || logs.searches[0] != "x" || logs.filter != "" {
		t.Fatalf("enter should promote the filter to a server search: searches=%v filter=%q", logs.searches, logs.filter)
	}

	press(a, key("esc"))
	if len(a.stack) != 2 {
		t.Fatalf("esc should clear the tab's search, not pop the detail page (depth %d)", len(a.stack))
	}
	if len(logs.searches) != 0 {
		t.Errorf("search not cleared: %v", logs.searches)
	}

	press(a, key("esc"))
	if len(a.stack) != 1 {
		t.Fatalf("second esc should pop the detail page, depth = %d", len(a.stack))
	}
}

// TestHostDetailRelatedTab: the related tab lists the host's Smartscape
// neighbors (its processes, its K8s node), the highlighted neighbor drives
// app-level actions, and enter opens its detail page — where a K8s node
// carries a node-scoped pods tab (host → node → pods: pods edge to the
// node in Smartscape, never to the host directly).
func TestHostDetailRelatedTab(t *testing.T) {
	a := testApp(t, "hosts")
	seedRows(t, a, []map[string]any{hostRow()})
	press(a, key("enter"))
	dv := a.top().(*detailView)

	press(a, key("shift+tab")) // related — the last tab, one wrap away
	rv, ok := dv.tabs[dv.active].view.(*relationsView)
	if !ok {
		t.Fatalf("shift+tab should activate the related tab, view = %T", dv.tabs[dv.active].view)
	}

	dv.Update(dataMsg{owner: rv, seq: rv.seq, records: []map[string]any{
		{"source_id": "PROCESS-0000000000000001", "source_type": "PROCESS", "type": "runs_on",
			"target_id": "HOST-AAAABBBBCCCCDDDD", "target_type": "HOST"},
		{"source_id": "K8S_NODE-0000000000000001", "source_type": "K8S_NODE", "type": "runs_on",
			"target_id": "HOST-AAAABBBBCCCCDDDD", "target_type": "HOST"},
		{"source_id": "HOST-AAAABBBBCCCCDDDD", "source_type": "HOST", "type": "calls",
			"target_id": "HOST-0000000000000002", "target_type": "HOST"},
	}})
	body := dv.View(120, 30)
	// Each row reads type-and-verb around the arrow: the neighbor acts on us
	// ("PROCESS ← runs on") or we act on it ("calls → HOST").
	for _, want := range []string{"PROCESS ← runs on", "K8S_NODE ← runs on", "calls → HOST", "3 relations"} {
		if !strings.Contains(body, want) {
			t.Errorf("related tab missing %q:\n%s", want, body)
		}
	}

	// The highlighted neighbor wins Selection — pin/relations/open act on it
	// (incoming edges sort by neighbor type: the node row is first).
	if _, e := dv.Selection(); e == nil || e.Type != "K8S_NODE" {
		t.Fatalf("selection should be the highlighted neighbor, got %+v", e)
	}

	press(a, key("enter"))
	nd, ok := a.top().(*detailView)
	if !ok || nd.entity.Type != "K8S_NODE" {
		t.Fatalf("enter on a neighbor should open its detail page, top = %T", a.top())
	}
	podsIdx := -1
	for i, tab := range nd.tabs {
		if tab.name == "pods" {
			podsIdx = i
		}
	}
	if podsIdx < 0 {
		t.Fatalf("node detail page has no pods tab")
	}
	deliverView(nd, nd.setActive(podsIdx))
	pods := nd.tabs[podsIdx].view.(*tableView)
	if !strings.Contains(pods.dql, "k8s.node.name ==") {
		t.Errorf("pods tab not scoped to the node:\n%s", pods.dql)
	}
}

func TestInspectorSearchFiltersProperties(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{problemRow()})
	press(a, key("d")) // 'd' keeps the raw record inspector (enter = problem page)

	insp, ok := a.top().(*inspectorView)
	if !ok {
		t.Fatalf("d on a problem should open the inspector, top = %T", a.top())
	}
	if body := insp.View(120, 30); !strings.Contains(body, "highlights") {
		t.Errorf("inspector should render the highlights block:\n%s", body)
	}

	press(a, key("/"))
	for _, r := range "status" {
		press(a, key(string(r)))
	}
	body := insp.View(120, 30)
	if !strings.Contains(body, "event.status") {
		t.Errorf("search should keep matching fields:\n%s", body)
	}
	if strings.Contains(body, "display_id") {
		t.Errorf("search should hide non-matching fields:\n%s", body)
	}

	// esc clears the search (and must not pop the inspector).
	press(a, key("esc"))
	if len(a.stack) != 2 {
		t.Fatalf("esc should clear search, not pop (depth %d)", len(a.stack))
	}
	if body := insp.View(120, 30); !strings.Contains(body, "display_id") {
		t.Errorf("cleared search should restore all fields:\n%s", body)
	}

	press(a, key("esc"))
	if len(a.stack) != 1 {
		t.Fatalf("second esc should pop the inspector, depth = %d", len(a.stack))
	}
}

func TestDrillComposesScopeAndEscRestores(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{problemRow()})

	// 'l' drills into logs scoped to the problem's affected entity.
	press(a, key("l"))
	logs, ok := a.top().(*tableView)
	if !ok || logs.spec.Name != "logs" {
		t.Fatalf("after l: top = %v", a.top().Crumb())
	}
	if logs.scope.Entity == nil || logs.scope.Entity.ID != "SERVICE-1" {
		t.Fatalf("logs scope entity = %+v, want SERVICE-1", logs.scope.Entity)
	}
	if !strings.Contains(logs.dql, `dt.smartscape.service == toSmartscapeId("SERVICE-1")`) {
		t.Errorf("drill did not compose scope into DQL:\n%s", logs.dql)
	}
	if len(a.stack) != 2 {
		t.Fatalf("stack depth = %d, want 2", len(a.stack))
	}

	// esc pops back to problems with state intact.
	press(a, key("esc"))
	if top, ok := a.top().(*tableView); !ok || top.spec.Name != "problems" || len(top.rows) != 1 {
		t.Fatalf("esc did not restore problems view with data")
	}

	// '-' toggles back to the logs stack.
	press(a, key("-"))
	if top, ok := a.top().(*tableView); !ok || top.spec.Name != "logs" {
		t.Fatalf("'-' did not restore last view, top = %v", a.top().Crumb())
	}
}

func TestDescribeOpensInspector(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{problemRow()})

	press(a, key("d"))
	if _, ok := a.top().(*inspectorView); !ok {
		t.Fatalf("d should open inspector, top = %T", a.top())
	}
	if a.top().Crumb() != "checkout" {
		t.Errorf("inspector crumb = %q", a.top().Crumb())
	}
	body := a.top().View(120, 30)
	if !strings.Contains(body, "Failure rate increase") {
		t.Errorf("inspector body missing record content:\n%s", body)
	}
}

func TestMetricsDrillForCuratedAndUncuratedTypes(t *testing.T) {
	a := testApp(t, "problems")
	row := problemRow()
	seedRows(t, a, []map[string]any{row})

	press(a, key("m"))
	mv, ok := a.top().(*metricsView)
	if !ok {
		t.Fatalf("m should open metrics view for SERVICE, top = %T", a.top())
	}
	// The canned view opens on its availability probe, scoped to the service.
	if !strings.Contains(mv.dql, "metrics from:") || !strings.Contains(mv.dql, `toSmartscapeId("SERVICE-1")`) {
		t.Errorf("service metrics dql = %s", mv.dql)
	}
	press(a, key("esc"))

	// An uncurated entity type opens the metric explorer scoped to it.
	row["smartscape.affected_entities"] = []any{
		map[string]any{"id": "AWS_X-1", "name": "x", "type": "AWS_X"},
	}
	press(a, key("m"))
	tv, ok := a.top().(*tableView)
	if !ok || tv.spec.Name != "metrics" {
		t.Fatalf("m should open the metric explorer for uncurated types, top = %T (%s)", a.top(), a.top().Crumb())
	}
	for _, want := range []string{"metrics from:", `dt.smartscape_source.id == toSmartscapeId("AWS_X-1")`, "by:{metric.key}"} {
		if !strings.Contains(tv.dql, want) {
			t.Errorf("explorer dql missing %q:\n%s", want, tv.dql)
		}
	}
}

func TestCommandBarJumpReplacesStack(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{problemRow()})
	press(a, key("l")) // depth 2

	press(a, key(":"))
	if !a.cmdActive {
		t.Fatal("':' should open the command bar")
	}
	for _, r := range "ho" {
		press(a, key(string(r)))
	}
	press(a, key("enter"))

	if len(a.stack) != 1 {
		t.Fatalf("command-bar jump should replace the stack, depth = %d", len(a.stack))
	}
	if tv, ok := a.top().(*tableView); !ok || tv.spec.Name != "hosts" {
		t.Fatalf("jump target = %v, want hosts", a.top().Crumb())
	}
}

func TestTimeframePickerAppliesGlobally(t *testing.T) {
	a := testApp(t, "hosts")
	press(a, key("t"))
	if !a.tfActive {
		t.Fatal("'t' should open the timeframe picker")
	}
	press(a, key("1"))
	if a.tfActive || a.tf.Label != "30m" {
		t.Fatalf("picker did not apply: tf = %s", a.tf.Label)
	}
}

// :ctx <name> switches the session to another dtctl context (tui.md §Command
// bar): the tenant wiring swaps, every tenant-specific piece of state drops
// (pin, segments, dictionary, both stacks), and the session lands on home.
func TestCtxCommandSwitchesContext(t *testing.T) {
	a := testApp(t, "hosts")
	a.opts.Contexts = []string{"test", "prod"}
	a.opts.SwitchContext = func(name string) (*ContextWiring, error) {
		env := map[string]string{
			"test": "https://test.example.invalid",
			"prod": "https://prod.example.invalid",
		}[name]
		if env == "" {
			return nil, fmt.Errorf("context %q not found", name)
		}
		return &ContextWiring{ContextName: name, Environment: env, SafetyLevel: "readonly"}, nil
	}
	// Tenant-specific state that must not survive the switch.
	a.pin = &catalog.Entity{ID: "HOST-0000000000000001", Type: "HOST"}
	a.segApplied = []SegmentOption{{UID: "seg-1", Name: "shop"}}
	a.ds.segments = a.segmentRefs(a.segApplied)
	a.ds.dictRequested = true

	press(a, key(":"))
	press(a, key("ctx"))
	press(a, key(" "))
	press(a, key("prod"))
	press(a, key("enter"))

	if a.opts.ContextName != "prod" || a.opts.SafetyLevel != "readonly" {
		t.Fatalf("wiring not applied: %+v", a.opts.ContextName)
	}
	if a.pin != nil || a.segApplied != nil || a.ds.segments != nil || a.ds.dictRequested {
		t.Error("tenant-specific state must reset on switch")
	}
	if _, ok := a.top().(*homeView); !ok || len(a.stack) != 1 {
		t.Fatalf("switch must land on a fresh home view, top = %T", a.top())
	}
	if a.prev != nil {
		t.Error("'-' must not resurrect the old tenant's views")
	}
	if a.hist.ctx != "prod" {
		t.Errorf("history context = %q, want prod", a.hist.ctx)
	}

	// A failed switch leaves the session untouched.
	press(a, key(":"))
	press(a, key("ctx nosuch"))
	press(a, key("enter"))
	if a.opts.ContextName != "prod" || !a.statusErr {
		t.Fatalf("failed switch must keep the session and report: ctx=%s status=%q", a.opts.ContextName, a.status)
	}

	// No argument opens the picker over the configured contexts, highlighting
	// the one the session is on; selecting another switches through the same
	// session-local path.
	press(a, key(":"))
	press(a, key("ctx"))
	press(a, key("enter"))
	if !a.ctxPickActive {
		t.Fatal("bare :ctx should open the context picker")
	}
	if a.opts.Contexts[a.ctxPickSel] != "prod" {
		t.Fatalf("picker should highlight the current context, got %q", a.opts.Contexts[a.ctxPickSel])
	}
	if out := a.View(); !strings.Contains(out, "switch context") || !strings.Contains(out, "current") {
		t.Fatalf("picker should render the context list with a current badge:\n%s", out)
	}
	press(a, key("up")) // "prod" (index 1) → "test" (index 0)
	press(a, key("enter"))
	if a.ctxPickActive {
		t.Fatal("enter should close the picker")
	}
	if a.opts.ContextName != "test" {
		t.Fatalf("picker switch did not apply: ctx = %s", a.opts.ContextName)
	}

	// esc dismisses the picker without touching the session.
	press(a, key(":"))
	press(a, key("ctx"))
	press(a, key("enter"))
	press(a, key("esc"))
	if a.ctxPickActive || a.opts.ContextName != "test" {
		t.Fatalf("esc must close the picker and keep the context, ctx = %s", a.opts.ContextName)
	}
}

// The picker's fifth entry takes any relative window (tui.md §4: "30m / 2h /
// 24h / 7d / custom") — the same labels the workspace file accepts.
func TestTimeframePickerCustomEntry(t *testing.T) {
	a := testApp(t, "hosts")
	press(a, key("t"))
	press(a, key("5")) // the custom entry follows the four presets
	if !a.tfCustom {
		t.Fatal("digit 5 should open the custom window input")
	}
	press(a, key("45m"))
	press(a, key("enter"))
	if a.tfActive || a.tfCustom || a.tf.Label != "45m" {
		t.Fatalf("custom window did not apply: tf = %s", a.tf.Label)
	}

	// Reopening lands the highlight on custom (45m is no preset) and an
	// invalid label refuses with a status instead of applying garbage.
	press(a, key("t"))
	if a.tfSel != len(catalog.Timeframes) {
		t.Fatalf("picker highlight = %d, want the custom entry", a.tfSel)
	}
	press(a, key("enter"))
	press(a, key("nonsense"))
	press(a, key("enter"))
	if !a.tfCustom || a.tf.Label != "45m" {
		t.Fatalf("invalid label must keep the input open and the window unchanged, tf = %s", a.tf.Label)
	}
	press(a, key("esc")) // back to the pills
	press(a, key("esc")) // close the picker
	if a.tfCustom || a.tfActive {
		t.Fatal("esc should unwind the custom input, then the picker")
	}
}

func TestFilterNarrowsRows(t *testing.T) {
	a := testApp(t, "problems")
	other := problemRow()
	other["event.name"] = "Slowdown on payments"
	seedRows(t, a, []map[string]any{problemRow(), other})

	press(a, key("/"))
	for _, r := range "slowdown" {
		press(a, key(string(r)))
	}
	tv := a.top().(*tableView)
	if len(tv.rows) != 1 {
		t.Fatalf("filter rows = %d, want 1", len(tv.rows))
	}
	// esc clears the filter (and must not pop the view).
	press(a, key("esc"))
	press(a, key("esc"))
	tv = a.top().(*tableView)
	if len(tv.rows) != 2 || len(a.stack) != 1 {
		t.Fatalf("esc should clear filter first: rows=%d depth=%d", len(tv.rows), len(a.stack))
	}
}

func TestViewRendersChrome(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{problemRow()})
	out := a.View()
	for _, want := range []string{"ctx:", "test", "readonly", "problems", "P-100", "dtctl query"} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q", want)
		}
	}
	if h := len(strings.Split(out, "\n")); h != 40 {
		t.Errorf("View() height = %d lines, want 40", h)
	}
}

func TestQuitAndHelpKeys(t *testing.T) {
	a := testApp(t, "hosts")
	press(a, key("?")) // may batch a spinner tick alongside — helpActive is what matters
	if !a.helpActive {
		t.Fatal("'?' should open help")
	}
	if !strings.Contains(a.View(), "dtctl tui — keys") {
		t.Error("help overlay not rendered")
	}
	press(a, key("esc"))
	if a.helpActive {
		t.Error("esc should close help")
	}

	if _, cmd := a.Update(key("q")); cmd == nil {
		t.Error("'q' should quit")
	}
}

func TestUnknownInitialViewFails(t *testing.T) {
	if _, err := newApp(Options{InitialView: "bogus"}); err == nil {
		t.Fatal("bogus initial view must error")
	}
}

func TestCatalogTimeframeDefaultsAgree(t *testing.T) {
	if catalog.DefaultTimeframe.Label != "2h" {
		t.Errorf("unexpected default timeframe %s", catalog.DefaultTimeframe.Label)
	}
}

func TestInspectorFieldCursorTraversesEntityLinks(t *testing.T) {
	a := testApp(t, "problems")
	row := problemRow()
	row["dt.smartscape.host"] = "HOST-0011223344556677"
	seedRows(t, a, []map[string]any{row})
	press(a, key("d"))

	insp, ok := a.top().(*inspectorView)
	if !ok {
		t.Fatalf("top = %T, want inspector", a.top())
	}
	// Find the entity-link row and put the cursor on it.
	target := -1
	for i, r := range insp.rows {
		if r.val.entity != nil && r.val.entity.ID == "HOST-0011223344556677" {
			target = i
		}
	}
	if target < 0 {
		t.Fatalf("no navigable row for the host id; rows: %+v", insp.rows)
	}
	for insp.cursor < target {
		press(a, key("j"))
	}

	// The selection follows the cursor (pin/relations act on the link) and
	// yank copies the field value.
	if _, e := insp.Selection(); e == nil || e.ID != "HOST-0011223344556677" {
		t.Fatalf("selection entity = %+v", e)
	}
	if text, _, ok := insp.YankText(); !ok || text != "HOST-0011223344556677" {
		t.Fatalf("yank = %q %v", text, ok)
	}

	// enter traverses to the linked entity's detail page.
	press(a, key("enter"))
	dv, ok := a.top().(*detailView)
	if !ok {
		t.Fatalf("enter on entity link should open detail, top = %T", a.top())
	}
	if dv.entity.ID != "HOST-0011223344556677" || dv.entity.Type != "HOST" {
		t.Fatalf("detail entity = %+v", dv.entity)
	}
}

func TestInspectorEnterOpensTraceWaterfall(t *testing.T) {
	a := testApp(t, "problems")
	trace := strings.Repeat("ab", 16)
	row := problemRow()
	row["trace_id"] = trace
	seedRows(t, a, []map[string]any{row})
	press(a, key("d"))

	insp := a.top().(*inspectorView)
	target := -1
	for i, r := range insp.rows {
		if r.val.trace == trace {
			target = i
		}
	}
	if target < 0 {
		t.Fatal("no trace row found")
	}
	for insp.cursor < target {
		press(a, key("j"))
	}
	press(a, key("enter"))
	wf, ok := a.top().(*waterfallView)
	if !ok {
		t.Fatalf("enter on trace id should open waterfall, top = %T", a.top())
	}
	if wf.TraceID() != trace {
		t.Errorf("waterfall trace = %q", wf.TraceID())
	}
}

func TestInspectorBlockDefaultsAndCollapseToggle(t *testing.T) {
	a := testApp(t, "problems")
	obj := map[string]any{}
	for _, k := range []string{"a", "b", "c", "d", "e", "f"} {
		obj[k] = strings.Repeat(k, 3)
	}
	row := problemRow()
	row["details"] = obj
	row["long_text"] = strings.Repeat("lorem ipsum ", 30)
	seedRows(t, a, []map[string]any{row})
	press(a, key("d"))

	insp := a.top().(*inspectorView)
	find := func(key string) *fieldRow {
		for i := range insp.rows {
			if insp.rows[i].key == key {
				return &insp.rows[i]
			}
		}
		t.Fatalf("no row for %q", key)
		return nil
	}

	countSubRows := func() int {
		n := 0
		for _, r := range insp.rows {
			if r.key == "details" && r.label != "details" {
				n++
			}
		}
		return n
	}

	// JSON objects read as structure — expanded by default, exploded into
	// one selectable row per key.
	if r := find("details"); !r.expandable || !r.expanded {
		t.Fatalf("object should default to a block: %+v", *r)
	}
	if got := countSubRows(); got != 6 {
		t.Fatalf("expanded object should contribute 6 selectable sub-rows, got %d", got)
	}
	// Very long scalars wrap expanded by default too (log content must be
	// readable without a keypress).
	if r := find("long_text"); !r.expandable || !r.expanded || r.span < 2 {
		t.Fatalf("long scalar should default to a wrapped block: %+v", *r)
	}
	// Short-ish arrays stay one line, truncated preview marked ▸.
	if r := find("smartscape.affected_entities"); r.expanded || r.span != 1 {
		t.Fatalf("array should default to one line: %+v", *r)
	}
	if body := insp.View(120, 40); !strings.Contains(body, "▸") {
		t.Errorf("collapsed marker missing:\n%s", body)
	}

	// enter collapses a default-expanded object (sub-rows fold away), and
	// toggles back.
	target := 0
	for i, r := range insp.rows {
		if r.key == "details" && r.label == "details" {
			target = i
		}
	}
	for insp.cursor < target {
		press(a, key("j"))
	}
	press(a, key("enter"))
	if got := find("details").span; got != 1 {
		t.Fatalf("collapse toggle did not shrink the object to one line, got %d", got)
	}
	if got := countSubRows(); got != 0 {
		t.Fatalf("collapse should fold the sub-rows away, %d remain", got)
	}
	press(a, key("enter"))
	if got := countSubRows(); got != 6 {
		t.Fatalf("re-expand should restore the sub-rows, got %d", got)
	}
	if len(a.stack) != 2 {
		t.Fatalf("expand toggle must not navigate, depth = %d", len(a.stack))
	}
}

func TestInspectorJSONSubRowsSelectableAndYankable(t *testing.T) {
	a := testApp(t, "problems")
	row := problemRow()
	row["details"] = map[string]any{
		"nested": map[string]any{"service": "SERVICE-8899AABBCCDDEEFF"},
		"reason": "quota exceeded",
	}
	seedRows(t, a, []map[string]any{row})
	press(a, key("d"))
	insp := a.top().(*inspectorView)

	find := func(label string) int {
		t.Helper()
		for i, r := range insp.rows {
			if r.label == label {
				return i
			}
		}
		t.Fatalf("no row labeled %q", label)
		return -1
	}
	moveTo := func(i int) {
		for insp.cursor < i {
			press(a, key("j"))
		}
		for insp.cursor > i {
			press(a, key("k"))
		}
	}

	// A leaf inside the expanded block yanks its own (unquoted) value.
	moveTo(find("details.reason"))
	if text, _, ok := insp.YankText(); !ok || text != "quota exceeded" {
		t.Fatalf("leaf yank = %q %v", text, ok)
	}
	// A container row yanks its whole subtree as compact JSON.
	moveTo(find("details.nested"))
	if text, _, _ := insp.YankText(); text != `{"service":"SERVICE-8899AABBCCDDEEFF"}` {
		t.Fatalf("subtree yank = %q", text)
	}
	// An entity id nested in the document is a real link: selection carries
	// it and enter opens its detail page.
	moveTo(find("details.nested.service"))
	if _, e := insp.Selection(); e == nil || e.ID != "SERVICE-8899AABBCCDDEEFF" {
		t.Fatalf("selection = %+v", e)
	}
	press(a, key("enter"))
	dv, ok := a.top().(*detailView)
	if !ok || dv.entity.ID != "SERVICE-8899AABBCCDDEEFF" {
		t.Fatalf("enter on nested entity id should open detail, top = %T", a.top())
	}
}

func TestInspectorGroupOrderingPutsDtLast(t *testing.T) {
	a := testApp(t, "problems")
	row := problemRow()
	row["aws.region"] = "us-east-1"
	row["k8s.cluster.name"] = "prod"
	row["dt.openpipeline.pipelines"] = "default"
	seedRows(t, a, []map[string]any{row})
	press(a, key("d"))
	insp := a.top().(*inspectorView)

	sectionAt := func(name string) int {
		t.Helper()
		for i, line := range insp.lines {
			if strings.Contains(ansi.Strip(line), " "+name+" ") || strings.HasSuffix(ansi.Strip(line), " "+name) {
				return i
			}
		}
		t.Fatalf("no section header %q in:\n%s", name, strings.Join(insp.lines, "\n"))
		return -1
	}
	dt := sectionAt("dt")
	if aws := sectionAt("aws"); dt < aws {
		t.Errorf("dt section (line %d) should render after aws (line %d)", dt, aws)
	}
	if k8s := sectionAt("k8s"); dt < k8s {
		t.Errorf("dt section (line %d) should render after k8s (line %d)", dt, k8s)
	}
}

func TestInspectorPageJumpsMoveCursor(t *testing.T) {
	a := testApp(t, "problems")
	row := problemRow()
	for i := 0; i < 60; i++ {
		row[fmt.Sprintf("field_%02d", i)] = fmt.Sprintf("value %d", i)
	}
	seedRows(t, a, []map[string]any{row})
	press(a, key("d"))
	insp := a.top().(*inspectorView)

	press(a, key("ctrl+d"))
	half := insp.cursor
	if half < 2 {
		t.Fatalf("ctrl+d should jump multiple rows, cursor = %d", half)
	}
	press(a, key("ctrl+u"))
	if insp.cursor != 0 {
		t.Errorf("ctrl+u should jump back to the top, cursor = %d", insp.cursor)
	}
	press(a, key("pgdown"))
	if insp.cursor <= half {
		t.Errorf("pgdown (full page) should jump past ctrl+d (half): %d <= %d", insp.cursor, half)
	}
	press(a, key("pgup"))
	if insp.cursor != 0 {
		t.Errorf("pgup should return to the top, cursor = %d", insp.cursor)
	}
}

func TestInspectorResolvesEntityNames(t *testing.T) {
	a := testApp(t, "problems")
	var queries []string
	a.ds.runFn = func(dql string) ([]map[string]any, error) {
		queries = append(queries, dql)
		return nil, nil
	}
	row := problemRow()
	row["dt.smartscape.host"] = "HOST-0011223344556677"
	seedRows(t, a, []map[string]any{row})
	press(a, key("d"))
	insp := a.top().(*inspectorView)

	// Opening the inspector issues one batched name lookup for the ids.
	found := false
	for _, q := range queries {
		if strings.Contains(q, "smartscapeNodes") && strings.Contains(q, "HOST-0011223344556677") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no name-resolution query issued, queries: %v", queries)
	}

	// The resolved name renders next to the id and rides on the link target.
	insp.Update(dataMsg{owner: inspNameOwner{insp}, records: []map[string]any{
		{"id": "HOST-0011223344556677", "name": "web-01.example.invalid"},
	}})
	if body := insp.View(120, 40); !strings.Contains(body, "web-01.example.invalid") {
		t.Fatalf("resolved name not rendered:\n%s", body)
	}
	target := -1
	for i, r := range insp.rows {
		if r.val.entity != nil && r.val.entity.ID == "HOST-0011223344556677" {
			target = i
		}
	}
	if target < 0 {
		t.Fatal("no entity row found")
	}
	for insp.cursor < target {
		press(a, key("j"))
	}
	if _, e := insp.Selection(); e == nil || e.Name != "web-01.example.invalid" {
		t.Errorf("selection should carry the resolved name, got %+v", e)
	}
	// Yank still returns the raw id, not the decorated line.
	if text, _, _ := insp.YankText(); text != "HOST-0011223344556677" {
		t.Errorf("yank = %q", text)
	}
}

func TestDetailFetchBackfillsEntityNameIntoTabScopes(t *testing.T) {
	ds := &dataSource{runFn: func(string) ([]map[string]any, error) { return nil, nil }}
	entity := catalog.Entity{ID: "K8S_POD-0011223344556677", Type: "K8S_POD"} // id-only jump: no name
	dv := newDetailView(ds, entity, nil, catalog.DefaultTimeframe)
	dv.Update(bodySizeMsg{width: 120, height: 40})
	dv.Init() // starts the details-tab fetch (seq 1)

	iv := dv.tabs[0].view.(*inspectorView)
	dv.Update(dataMsg{owner: iv, seq: 1, records: []map[string]any{{
		"id": entity.ID, "name": "checkout-abc123", "type": "K8S_POD",
	}}})

	if dv.entity.Name != "checkout-abc123" {
		t.Fatalf("detail entity name = %q", dv.entity.Name)
	}
	if dv.Crumb() != "checkout-abc123" {
		t.Errorf("crumb = %q", dv.Crumb())
	}

	// Activating the logs tab now composes the learned name — K8s log
	// scoping matches by plain k8s.* names, so this is load-bearing.
	logsIdx := -1
	for i, tab := range dv.tabs {
		if tab.name == "logs" {
			logsIdx = i
		}
	}
	deliverView(dv, dv.setActive(logsIdx))
	logs := dv.tabs[logsIdx].view.(*tableView)
	if !strings.Contains(logs.dql, `k8s.pod.name == "checkout-abc123"`) {
		t.Errorf("logs tab did not pick up the fetched name:\n%s", logs.dql)
	}
}

// deliverView executes a view command tree just far enough to trigger query
// composition (data-source results are dropped, like deliver).
func deliverView(v viewModel, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if msg == nil {
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			deliverView(v, c)
		}
		return
	}
	if dm, isData := msg.(dataMsg); isData {
		// The scope-widening hop is control flow — see deliver.
		if _, isHop := dm.owner.(hopOwner); isHop {
			deliverView(v, v.Update(msg))
		}
		return
	}
	deliverView(v, v.Update(msg))
}
