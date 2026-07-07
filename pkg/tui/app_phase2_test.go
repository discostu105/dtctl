package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
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

func TestHotkeysJumpAndDetailDigitsStayTabs(t *testing.T) {
	a := testApp(t, "problems")
	press(a, key("4"))
	if tv, ok := a.top().(*tableView); !ok || tv.spec.Name != "pods" {
		t.Fatalf("hotkey 4 should jump to pods, top = %v", a.top().Crumb())
	}
	if len(a.stack) != 1 {
		t.Fatalf("hotkey jump should replace the stack")
	}

	// On a detail page digits switch tabs, not views.
	seedRows(t, a, []map[string]any{podRow("a", "ns1", "Running", 0)})
	press(a, key("enter"))
	dv, ok := a.top().(*detailView)
	if !ok {
		t.Fatalf("enter on pod should open detail, top = %T", a.top())
	}
	press(a, key("3"))
	if _, stillDetail := a.top().(*detailView); !stillDetail || dv.active != 2 {
		t.Fatalf("digit on detail page must switch tabs (active=%d, top=%T)", dv.active, a.top())
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

	// Digits pick a lens directly (claimed from the hotkey map, like detail
	// tabs); the crumb names any non-default lens.
	press(a, key("2"))
	if a.top() != tv {
		t.Fatalf("digit on a lensed table must switch lens, not views (top = %T)", a.top())
	}
	if !strings.Contains(tv.dql, `span.status_code == "error"`) {
		t.Errorf("lens 2 (errors) not composed:\n%s", tv.dql)
	}
	if tv.Crumb() != "traces·errors" {
		t.Errorf("crumb = %q, want traces·errors", tv.Crumb())
	}

	// tab cycles forward, shift+tab back.
	press(a, key("tab"))
	if !strings.Contains(tv.dql, `span.kind == "server"`) {
		t.Errorf("tab should advance to server lens:\n%s", tv.dql)
	}
	press(a, key("shift+tab"))
	if !strings.Contains(tv.dql, `span.status_code == "error"`) {
		t.Errorf("shift+tab should return to errors lens:\n%s", tv.dql)
	}

	// The db lens swaps in its curated statement columns.
	press(a, key("5"))
	if got := tv.columns()[1].Title; got != "STATEMENT" {
		t.Errorf("db lens column[1] = %q, want STATEMENT", got)
	}

	// Digits past the lens list still hit their global hotkey (9 → aws).
	press(a, key("9"))
	if top, ok := a.top().(*tableView); !ok || top.spec.Name != "aws" {
		t.Fatalf("digit 9 should stay a hotkey jump, top = %v", a.top().Crumb())
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

func TestRelationsKeyOpensPanel(t *testing.T) {
	a := testApp(t, "pods")
	seedRows(t, a, []map[string]any{podRow("checkout-1", "shop", "Running", 0)})
	press(a, key("x"))
	rel, ok := a.top().(*relationsView)
	if !ok || rel.entity.ID != "K8S_POD-checkout-1" {
		t.Fatalf("x should open relations, top = %T", a.top())
	}
	if !strings.Contains(rel.dql, `source_id == toSmartscapeId("K8S_POD-checkout-1") or target_id == toSmartscapeId("K8S_POD-checkout-1")`) {
		t.Errorf("relations dql:\n%s", rel.dql)
	}
}

func TestBuildRelationsSplitsDirections(t *testing.T) {
	rows := buildRelations("K8S_POD-1", []map[string]any{
		{"source_id": "K8S_POD-1", "target_id": "K8S_NODE-1", "type": "runs_on", "target_type": "K8S_NODE"},
		{"source_id": "K8S_SERVICE-1", "target_id": "K8S_POD-1", "type": "routes_to", "source_type": "K8S_SERVICE"},
	})
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	if !rows[0].outgoing || rows[0].otherID != "K8S_NODE-1" || rows[0].otherType != "K8S_NODE" {
		t.Errorf("outgoing row = %+v", rows[0])
	}
	if rows[1].outgoing || rows[1].otherID != "K8S_SERVICE-1" {
		t.Errorf("incoming row = %+v", rows[1])
	}
}

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
	if len(hv.panels) != 5 {
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
	problem := linkFor(env, map[string]any{"event.kind": "DAVIS_PROBLEM", "event.id": "e-1"}, nil, "")
	if !strings.Contains(problem, "/ui/intent/dynatrace.davis.problems/view-problem#") {
		t.Errorf("problem link = %s", problem)
	}
	trace := linkFor(env, nil, nil, "abc123")
	if !strings.Contains(trace, "/ui/intent/dynatrace.distributedtracing/view-trace#") {
		t.Errorf("trace link = %s", trace)
	}
	pod := linkFor(env, nil, &catalog.Entity{ID: "K8S_POD-1", Type: "K8S_POD"}, "")
	if !strings.Contains(pod, "/ui/intent/dynatrace.kubernetes/view-entity-dt.smartscape.k8s_pod#") {
		t.Errorf("pod link = %s", pod)
	}
	generic := linkFor(env, nil, &catalog.Entity{ID: "DISK-1", Type: "DISK"}, "")
	if !strings.Contains(generic, "/ui/intent/dynatrace.smartscape/view_topology_in_context#") {
		t.Errorf("generic link = %s", generic)
	}
}
