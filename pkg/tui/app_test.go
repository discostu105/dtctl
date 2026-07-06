package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
)

// Bubbletea models are pure enough to drive with synthetic messages; no TTY
// or network is needed. Commands returned by Update are executed manually
// only when they are known to be side-effect free (navigation messages) —
// data-source commands are never run.

func testApp(t *testing.T, initial string) *app {
	t.Helper()
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

func key(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
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
	if _, isData := msg.(dataMsg); isData {
		return
	}
	// Spinner ticks would re-arm themselves forever while a view waits on
	// data that never arrives in tests.
	if _, isTick := msg.(spinnerTickMsg); isTick {
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
	tv.Update(dataMsg{owner: tv, seq: tv.seq, records: rows, elapsed: time.Second, dql: tv.spec.Query(tv.scope)})
}

func problemRow() map[string]any {
	return map[string]any{
		"display_id":   "P-100",
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
	for _, want := range []string{"1 · details", "2 · metrics", "3 · logs", "4 · events", "5 · problems",
		"7.6 GiB", "2 logical / 1 physical", "aws us-east-1b", "HOST-AAAABBBBCCCCDDDD"} {
		if !strings.Contains(body, want) {
			t.Errorf("details tab missing %q:\n%s", want, body)
		}
	}

	// tab switches to metrics and lazily starts its query.
	press(a, key("tab"))
	if dv.active != 1 {
		t.Fatalf("tab should move to metrics, active = %d", dv.active)
	}
	mv, ok := dv.tabs[1].view.(*metricsView)
	if !ok || !strings.Contains(mv.dql, "dt.host.cpu.usage") ||
		!strings.Contains(mv.dql, `toSmartscapeId("HOST-AAAABBBBCCCCDDDD")`) {
		t.Fatalf("metrics tab dql = %q", mv.dql)
	}

	// Digits jump straight to a tab; the logs tab is pre-scoped to the host.
	press(a, key("3"))
	logs, ok := dv.tabs[dv.active].view.(*tableView)
	if !ok || logs.spec.Name != "logs" {
		t.Fatalf("'3' should activate logs tab, active view = %T", dv.tabs[dv.active].view)
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
	press(a, key("3")) // logs tab

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

func TestInspectorSearchFiltersProperties(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{problemRow()})
	press(a, key("enter")) // problems are signal rows → raw inspector

	insp, ok := a.top().(*inspectorView)
	if !ok {
		t.Fatalf("enter on a problem should open the inspector, top = %T", a.top())
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

func TestEnterOpensInspector(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{problemRow()})

	press(a, key("enter"))
	if _, ok := a.top().(*inspectorView); !ok {
		t.Fatalf("enter should open inspector, top = %T", a.top())
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
	if !strings.Contains(mv.dql, "dt.service.request.count") {
		t.Errorf("service metrics dql = %s", mv.dql)
	}
	press(a, key("esc"))

	// An uncurated entity type shows a status message instead of navigating.
	row["smartscape.affected_entities"] = []any{
		map[string]any{"id": "AWS_X-1", "name": "x", "type": "AWS_X"},
	}
	press(a, key("m"))
	if _, ok := a.top().(*metricsView); ok {
		t.Fatal("m must not navigate for uncurated entity types")
	}
	if a.status == "" || !a.statusErr {
		t.Errorf("expected error status, got %q", a.status)
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
	press(a, key("enter"))

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
	press(a, key("enter"))

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
	press(a, key("enter"))

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

	// JSON objects read as structure — expanded block by default.
	if r := find("details"); !r.expandable || !r.expanded || r.span < 2 {
		t.Fatalf("object should default to a block: %+v", *r)
	}
	// Scalars (and arrays — problemRow's affected_entities) stay one line,
	// truncated preview marked ▸.
	if r := find("long_text"); !r.expandable || r.expanded || r.span != 1 {
		t.Fatalf("long scalar should default to one line: %+v", *r)
	}
	if r := find("smartscape.affected_entities"); r.expanded || r.span != 1 {
		t.Fatalf("array should default to one line: %+v", *r)
	}
	if body := insp.View(120, 40); !strings.Contains(body, "▸") {
		t.Errorf("collapsed marker missing:\n%s", body)
	}

	// enter collapses a default-expanded object, and toggles back.
	target := 0
	for i, r := range insp.rows {
		if r.key == "details" {
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
	press(a, key("enter"))
	if got := find("details").span; got < 2 {
		t.Fatalf("re-expand failed, span = %d", got)
	}
	if len(a.stack) != 2 {
		t.Fatalf("expand toggle must not navigate, depth = %d", len(a.stack))
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
	if _, isData := msg.(dataMsg); isData {
		return
	}
	deliverView(v, v.Update(msg))
}
