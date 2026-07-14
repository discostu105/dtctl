package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
)

// Phase 2/3 engine behavior: sorting, pinning, hotkeys, containment enter,
// waterfall building, relations building, query-view reveal.

func podRow(name, ns, phase string, restarts float64) map[string]any {
	return map[string]any{
		"id": "K8S_POD-" + name, "name": name, "namespace": ns, "phase": phase,
		"restarts": restarts, "ready": "1", "total": "1",
		"created": "2026-07-01T10:00:00.000000000Z",
	}
}

func TestSortKeysCycleAndToggle(t *testing.T) {
	a := testApp(t, "pods")
	seedRows(t, a, []map[string]any{
		podRow("a", "ns1", "Running", 3),
		podRow("b", "ns1", "Running", 31),
		podRow("c", "ns1", "Running", 0),
	})
	tv := a.top().(*tableView)

	// J to the RST column (index 3): numeric column defaults descending.
	for range 4 {
		press(a, key("J"))
	}
	if tv.sortCol != 3 || !tv.sortDesc {
		t.Fatalf("sortCol = %d desc = %v, want 3/desc", tv.sortCol, tv.sortDesc)
	}
	if catalog.Str(tv.rows[0], "name") != "b" {
		t.Errorf("rows not sorted by restarts desc: %v", tv.rows)
	}

	// K toggles direction.
	press(a, key("K"))
	if tv.sortDesc || catalog.Str(tv.rows[0], "name") != "c" {
		t.Errorf("K should flip to ascending, first = %s", catalog.Str(tv.rows[0], "name"))
	}
}

func TestHotkeysJumpEverywhereAndLettersJumpTabs(t *testing.T) {
	a := testApp(t, "problems")
	press(a, key("4"))
	if tv, ok := a.top().(*tableView); !ok || tv.spec.Name != "pods" {
		t.Fatalf("hotkey 4 should jump to pods, top = %v", a.top().Crumb())
	}
	if len(a.stack) != 1 {
		t.Fatalf("hotkey jump should replace the stack")
	}

	// On a detail page the drill letters jump to their tab...
	seedRows(t, a, []map[string]any{podRow("a", "ns1", "Running", 0)})
	press(a, key("enter"))
	dv, ok := a.top().(*detailView)
	if !ok {
		t.Fatalf("enter on pod should open detail, top = %T", a.top())
	}
	press(a, key("v"))
	if _, stillDetail := a.top().(*detailView); !stillDetail || dv.tabs[dv.active].name != "events" {
		t.Fatalf("v on detail page must jump to the events tab (active=%s, top=%T)", dv.tabs[dv.active].name, a.top())
	}
	// The events tab shows a lens strip — the innermost numbered strip — so
	// digits address it: 2 picks the alerts lens, not a page tab.
	ev, ok := dv.activeView().(*tableView)
	if !ok {
		t.Fatalf("events tab view = %T", dv.activeView())
	}
	press(a, key("2"))
	if _, stillDetail := a.top().(*detailView); !stillDetail || dv.tabs[dv.active].name != "events" {
		t.Fatalf("digit on a lensed tab must stay there (active=%s, top=%T)", dv.tabs[dv.active].name, a.top())
	}
	if got := ev.spec.LensAt(ev.scope.Lens).Name; got != "alerts" {
		t.Fatalf("2 on the events tab must pick the alerts lens, got %s", got)
	}
	// A digit past the strip is swallowed with a hint, never a hidden jump.
	press(a, key("9"))
	if _, stillDetail := a.top().(*detailView); !stillDetail {
		t.Fatalf("out-of-range digit must stay on the page, top=%T", a.top())
	}
	// On a lens-less tab the tab bar is the numbered strip again: 2 picks
	// the second tab (a pod's containers).
	dv.setActive(0) // details — no lens strip
	press(a, key("2"))
	if _, stillDetail := a.top().(*detailView); !stillDetail || dv.tabs[dv.active].name != "containers" {
		t.Fatalf("digit on a lens-less tab must pick a page tab (active=%s, top=%T)", dv.tabs[dv.active].name, a.top())
	}
	// esc pops out — the digits are global bookmarks again.
	press(a, key("esc"))
	press(a, key("3"))
	if tv, ok := a.top().(*tableView); !ok || tv.spec.Name != "hosts" || len(a.stack) != 1 {
		t.Fatalf("digit after esc must be a global bookmark (top=%T, depth=%d)", a.top(), len(a.stack))
	}
}

func TestPinScopesCommandBarJumps(t *testing.T) {
	a := testApp(t, "pods")
	seedRows(t, a, []map[string]any{podRow("checkout-1", "shop", "Running", 0)})

	press(a, key("."))
	if a.pin == nil || a.pin.ID != "K8S_POD-checkout-1" {
		t.Fatalf("pin = %+v", a.pin)
	}

	// A command-bar jump to a signal view inherits the pin.
	press(a, key(":"))
	for _, r := range "logs" {
		press(a, key(string(r)))
	}
	press(a, key("enter"))
	logs := a.top().(*tableView)
	if logs.scope.Entity == nil || logs.scope.Entity.ID != "K8S_POD-checkout-1" {
		t.Fatalf("pinned jump not scoped: %+v", logs.scope.Entity)
	}
	if !strings.Contains(logs.dql, `k8s.pod.name == "checkout-1"`) {
		t.Errorf("pinned logs dql missing pod name filter:\n%s", logs.dql)
	}

	// ctrl+x unpins.
	press(a, key("ctrl+x"))
	if a.pin != nil {
		t.Error("ctrl+x should unpin")
	}
}

func TestWorkloadEnterDrillsIntoItsPods(t *testing.T) {
	a := testApp(t, "workloads")
	seedRows(t, a, []map[string]any{{
		"id": "K8S_DEPLOYMENT-1", "name": "checkout", "type": "K8S_DEPLOYMENT",
		"namespace": "shop", "kind": "deployment", "ready": "2", "desired": "2",
	}})
	press(a, key("enter"))
	pods, ok := a.top().(*tableView)
	if !ok || pods.spec.Name != "pods" {
		t.Fatalf("enter on workload should open pods, top = %v", a.top().Crumb())
	}
	if !strings.Contains(pods.dql, `k8s.workload.kind == "deployment" and k8s.workload.name == "checkout"`) {
		t.Errorf("pods not scoped to workload:\n%s", pods.dql)
	}

	// The detail page stays reachable on d.
	press(a, key("esc"))
	press(a, key("d"))
	if _, ok := a.top().(*detailView); !ok {
		t.Fatalf("d on workload should open detail page, top = %T", a.top())
	}
}

func TestCensusEnterOpensTypedBrowser(t *testing.T) {
	a := testApp(t, "aws")
	seedRows(t, a, []map[string]any{{"type": "AWS_EC2_INSTANCE", "count": "34"}})
	press(a, key("enter"))
	res, ok := a.top().(*tableView)
	if !ok || res.spec.Name != "resources" || res.scope.Arg != "AWS_EC2_INSTANCE" {
		t.Fatalf("census enter should open typed browser, top = %v", a.top().Crumb())
	}
	if !strings.Contains(res.dql, `smartscapeNodes "AWS_EC2_INSTANCE"`) {
		t.Errorf("typed browser dql:\n%s", res.dql)
	}
}

func TestTraceRowEnterOpensWaterfallAndLogsJump(t *testing.T) {
	a := testApp(t, "traces")
	seedRows(t, a, []map[string]any{{
		"trace.id": "140ea4cf0d16aa99aadde231773bd127", "span.name": "GET /checkout",
		"endpoint.name": "GET /checkout", "span.kind": "server",
		"service.name": "checkout", "request.is_failed": true,
		"start_time": "2026-07-06T16:17:56.000000000Z", "duration": "5800000",
	}})
	press(a, key("enter"))
	wf, ok := a.top().(*waterfallView)
	if !ok || wf.traceID != "140ea4cf0d16aa99aadde231773bd127" {
		t.Fatalf("enter on trace row should open waterfall, top = %T", a.top())
	}

	// l from the waterfall opens the trace's logs.
	press(a, key("l"))
	logs, ok := a.top().(*tableView)
	if !ok || logs.spec.Name != "logs" {
		t.Fatalf("l on waterfall should open logs, top = %T", a.top())
	}
	if !strings.Contains(logs.dql, `trace_id == "140ea4cf0d16aa99aadde231773bd127"`) {
		t.Errorf("trace logs dql:\n%s", logs.dql)
	}
}

func TestSpanLensSwitching(t *testing.T) {
	a := testApp(t, "traces")
	tv := a.top().(*tableView)
	if !strings.Contains(tv.dql, "| filter isNull(span.parent_id)") {
		t.Fatalf("traces should open on the roots lens:\n%s", tv.dql)
	}

	// ] cycles forward, [ back; the crumb names any non-default lens.
	press(a, key("]"))
	if a.top() != tv {
		t.Fatalf("] on a lensed table must switch lens, not views (top = %T)", a.top())
	}
	if !strings.Contains(tv.dql, `span.status_code == "error"`) {
		t.Errorf("lens errors not composed:\n%s", tv.dql)
	}
	if tv.Crumb() != "traces·errors" {
		t.Errorf("crumb = %q, want traces·errors", tv.Crumb())
	}
	press(a, key("]"))
	if !strings.Contains(tv.dql, `span.kind == "server"`) {
		t.Errorf("] should advance to server lens:\n%s", tv.dql)
	}
	press(a, key("["))
	if !strings.Contains(tv.dql, `span.status_code == "error"`) {
		t.Errorf("[ should return to errors lens:\n%s", tv.dql)
	}

	// The db lens swaps in its curated statement columns.
	for range 3 { // errors → server → client → db
		press(a, key("]"))
	}
	if got := tv.columns()[1].Title; got != "STATEMENT" {
		t.Errorf("db lens column[1] = %q, want STATEMENT", got)
	}

	// The category lenses carry their own columns too.
	for range 2 { // db → rpc → messaging
		press(a, key("]"))
	}
	if got := tv.columns()[1].Title; got != "DESTINATION" {
		t.Errorf("messaging lens column[1] = %q, want DESTINATION", got)
	}

	// Digits never touch the lens strip: they stay global hotkeys.
	press(a, key("5"))
	if logs, ok := a.top().(*tableView); !ok || logs.spec.Name != "logs" {
		t.Fatalf("digit 5 should jump to logs, top = %v", a.top().Crumb())
	}
	press(a, key("0"))
	if _, ok := a.top().(*homeView); !ok {
		t.Fatalf("digit 0 should stay a hotkey jump to home, top = %T", a.top())
	}
}

func TestLogRowTraceJump(t *testing.T) {
	a := testApp(t, "logs")
	seedRows(t, a, []map[string]any{{
		"timestamp": "2026-07-06T16:18:52.000000000Z", "content": "boom",
		"trace_id": "7485b342cc6046e1d820a3a397141d12",
	}})
	press(a, key("s"))
	wf, ok := a.top().(*waterfallView)
	if !ok || wf.traceID != "7485b342cc6046e1d820a3a397141d12" {
		t.Fatalf("s on a log row should open its trace, top = %T", a.top())
	}
}

func TestTopologyKeyOpensNavigatorWalk(t *testing.T) {
	a := testApp(t, "pods")
	seedRows(t, a, []map[string]any{podRow("checkout-1", "shop", "Running", 0)})
	// x and X both open the navigator walk — the trail-based walk replaced
	// the standalone one-hop relations page (which lives on as the detail
	// page's related tab).
	press(a, key("x"))
	nv, ok := a.top().(*navView)
	if !ok || nv.mode != navWalk || nv.root.ID != "K8S_POD-checkout-1" {
		t.Fatalf("x should open the navigator walk, top = %T", a.top())
	}
	if !strings.Contains(nv.dql, `source_id == toSmartscapeId("K8S_POD-checkout-1") or target_id == toSmartscapeId("K8S_POD-checkout-1")`) {
		t.Errorf("walk dql:\n%s", nv.dql)
	}
}

// Direction splitting of edge records is covered in
// pkg/tui/catalog/smartscape_test.go (BuildEdges moved into the catalog for
// the smartscape navigator).

func TestBuildWaterfallTreeAndOrphans(t *testing.T) {
	rows := buildWaterfall([]map[string]any{
		{"span.id": "root", "span.name": "GET /checkout", "span.kind": "server",
			"start_time": "2026-07-06T16:00:00.000000000Z", "end_time": "2026-07-06T16:00:00.341000000Z",
			"request.is_failed": false, "service.name": "checkout"},
		{"span.id": "child", "span.parent_id": "root", "span.name": "SELECT",
			"start_time": "2026-07-06T16:00:00.100000000Z", "end_time": "2026-07-06T16:00:00.270000000Z",
			"request.is_failed": true, "service.name": "payments"},
		{"span.id": "orphan", "span.parent_id": "missing", "span.name": "async",
			"start_time": "2026-07-06T16:00:00.200000000Z", "end_time": "2026-07-06T16:00:00.210000000Z"},
	})
	if len(rows) != 3 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0].guide != "" || rows[1].guide != "└─ " || rows[1].label != "SELECT" || !rows[1].failed {
		t.Errorf("tree wrong: %+v", rows[:2])
	}
	if rows[2].guide != "" {
		t.Errorf("orphan (parent outside window) must render as root, guide = %q", rows[2].guide)
	}
}

func TestCtrlQRevealsQuery(t *testing.T) {
	a := testApp(t, "hosts")
	press(a, tea.KeyMsg{Type: tea.KeyCtrlQ})
	qv, ok := a.top().(*queryView)
	if !ok {
		t.Fatalf("ctrl+q should open query view, top = %T", a.top())
	}
	if !strings.Contains(qv.editor.Value(), `smartscapeNodes "HOST"`) {
		t.Errorf("editor not pre-filled: %q", qv.editor.Value())
	}
}

// Submitted queries persist as history and ctrl+p / ctrl+n cycle them in the
// editor (tui.md: the escape hatch carries "query history").
func TestQueryHistoryCyclesInEditor(t *testing.T) {
	a := testApp(t, "hosts")
	press(a, key(":"))
	press(a, key("query"))
	press(a, key("enter"))
	qv := a.top().(*queryView)

	run := func(dql string) {
		press(a, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(dql)})
		press(a, key("enter"))
		press(a, tea.KeyMsg{Type: tea.KeyTab}) // back to the editor
		qv.editor.SetValue("")
	}
	run("fetch logs")
	run("fetch spans")
	if len(a.qhist.queries) != 2 || a.qhist.queries[0] != "fetch spans" {
		t.Fatalf("history = %v", a.qhist.queries)
	}

	press(a, tea.KeyMsg{Type: tea.KeyCtrlP})
	if got := qv.editor.Value(); got != "fetch spans" {
		t.Fatalf("ctrl+p should recall the last query, editor = %q", got)
	}
	press(a, tea.KeyMsg{Type: tea.KeyCtrlP})
	if got := qv.editor.Value(); got != "fetch logs" {
		t.Fatalf("second ctrl+p should step further back, editor = %q", got)
	}
	press(a, tea.KeyMsg{Type: tea.KeyCtrlN})
	press(a, tea.KeyMsg{Type: tea.KeyCtrlN})
	if got := qv.editor.Value(); got != "" {
		t.Fatalf("ctrl+n past the newest entry should restore the draft, editor = %q", got)
	}

	// Re-running an old query moves it to the front instead of duplicating.
	a.qhist.add("fetch logs")
	if len(a.qhist.queries) != 2 || a.qhist.queries[0] != "fetch logs" {
		t.Fatalf("MRU dedup failed: %v", a.qhist.queries)
	}
}

func TestDeriveColumnsPrefersIdentityAndSkipsEmpties(t *testing.T) {
	cols := deriveColumns([]map[string]any{
		{"timestamp": "2026-07-06T16:00:00Z", "content": "x", "name": "", "level": "INFO"},
		{"timestamp": "2026-07-06T16:00:01Z", "content": "y", "name": "", "level": "WARN"},
	})
	if len(cols) == 0 || cols[0].Title != "TIMESTAMP" {
		t.Fatalf("first column = %+v", cols)
	}
	for _, c := range cols {
		if c.Field == "name" {
			t.Error("all-empty field must not become a column")
		}
	}
}

func TestCommandBarArgsPrefillFilter(t *testing.T) {
	a := testApp(t, "problems")
	press(a, key(":"))
	for _, r := range "pods checkout" {
		press(a, key(string(r)))
	}
	press(a, key("enter"))
	tv, ok := a.top().(*tableView)
	if !ok || tv.spec.Name != "pods" || tv.filter != "checkout" {
		t.Fatalf("':pods checkout' → %v filter %q", a.top().Crumb(), tv.filter)
	}
}

func TestHomePanelsLoadIndependently(t *testing.T) {
	a := testApp(t, "home")
	hv, ok := a.top().(*homeView)
	if !ok {
		t.Fatalf("home initial view, top = %T", a.top())
	}
	if len(hv.panels) != 6 {
		t.Fatalf("panels = %d", len(hv.panels))
	}
	// Each panel got its own DQL.
	for _, p := range hv.panels {
		if p.dql == "" {
			t.Errorf("panel %q has no query", p.title)
		}
	}
}

func TestIntentLinks(t *testing.T) {
	env := "https://abc.apps.dynatrace.com"
	first := func(opts []linkOption) string {
		if len(opts) == 0 {
			return ""
		}
		return opts[0].URL
	}
	problem := first(linkOptionsFor(env, map[string]any{"event.kind": "DAVIS_PROBLEM", "event.id": "e-1"}, nil, "", ""))
	if !strings.Contains(problem, "/ui/intent/dynatrace.davis.problems/view-problem#") {
		t.Errorf("problem link = %s", problem)
	}
	trace := first(linkOptionsFor(env, nil, nil, "abc123", ""))
	if !strings.Contains(trace, "/ui/intent/dynatrace.distributedtracing/view-trace#") {
		t.Errorf("trace link = %s", trace)
	}
	pod := linkOptionsFor(env, nil, &catalog.Entity{ID: "K8S_POD-1", Type: "K8S_POD"}, "", "")
	if !strings.Contains(first(pod), "/ui/intent/dynatrace.kubernetes/view-entity-dt.smartscape.k8s_pod#") {
		t.Errorf("pod link = %s", first(pod))
	}
	// Typed entities offer their app plus the topology explorer.
	if len(pod) != 2 || !strings.Contains(pod[1].URL, "/ui/intent/dynatrace.smartscape/view_topology_in_context#") {
		t.Errorf("pod options = %+v, want app + topology", pod)
	}
	generic := first(linkOptionsFor(env, nil, &catalog.Entity{ID: "DISK-1", Type: "DISK"}, "", ""))
	if !strings.Contains(generic, "/ui/intent/dynatrace.smartscape/view_topology_in_context#") {
		t.Errorf("generic link = %s", generic)
	}
}

// TestOpenWithOptions covers the picker composition: record-carried URLs
// (the vulnerability page's designated link) and the query-as-notebook
// fallback both surface as targets.
func TestOpenWithOptions(t *testing.T) {
	env := "https://abc.apps.dynatrace.com"
	vuln := map[string]any{
		"display_id": "S-4",
		"url":        "https://abc.live.dynatrace.com/ui/security/problem/129",
	}
	opts := linkOptionsFor(env, vuln, nil, "", "fetch security.events")
	if len(opts) != 2 {
		t.Fatalf("options = %+v, want record url + notebook", opts)
	}
	if opts[0].URL != vuln["url"] {
		t.Errorf("first option = %+v, want the record's url field", opts[0])
	}
	if !strings.Contains(opts[1].URL, "/ui/intent/dynatrace.notebooks/view-query#") {
		t.Errorf("second option = %+v, want notebook query", opts[1])
	}
	// Nothing at all → no options.
	if got := linkOptionsFor(env, nil, nil, "", ""); len(got) != 0 {
		t.Errorf("empty selection produced options: %+v", got)
	}
}
