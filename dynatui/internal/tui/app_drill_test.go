package tui

import (
	"strings"
	"testing"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
)

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
func TestPatternRowDrillsIntoMatchingLogs(t *testing.T) {
	a := testApp(t, "logs")
	// 'a' on logs opens the patterns view, inheriting scope.
	press(a, key("a"))
	tv, ok := a.top().(*tableView)
	if !ok || tv.spec.Name != "patterns" {
		t.Fatalf("a on logs → %s", a.top().Crumb())
	}
	seedRows(t, a, []map[string]any{{
		"patternExpression": `IPADDR:f_0 ' - - ' DQS:f_4`,
		"numberOfMatches":   float64(185),
		"sampleMatches":     []any{`10.0.0.1 - - "GET /"`},
	}})
	press(a, key("enter"))
	logs, ok := a.top().(*tableView)
	if !ok || logs.spec.Name != "logs" {
		t.Fatalf("enter on a pattern → %s", a.top().Crumb())
	}
	if logs.scope.Pattern != `IPADDR:f_0 ' - - ' DQS:f_4` {
		t.Errorf("pattern scope = %q", logs.scope.Pattern)
	}
	if !strings.Contains(logs.dql, "matchesPattern(content,") {
		t.Errorf("logs query must filter by the pattern:\n%s", logs.dql)
	}
	if !strings.Contains(logs.Crumb(), "[pattern]") {
		t.Errorf("breadcrumb must show the pattern narrowing: %s", logs.Crumb())
	}
}
func TestSessionEnterOpensTimeline(t *testing.T) {
	a := testApp(t, "sessions")
	seedRows(t, a, []map[string]any{{
		"dt.rum.session.id": "ABCD-0",
		"frontend.name":     []any{"shop"},
		"start_time":        "2026-07-07T10:00:00Z",
	}})
	press(a, key("enter"))
	tl, ok := a.top().(*timelineView)
	if !ok {
		t.Fatalf("enter on a session → %s", a.top().Crumb())
	}
	if tl.sessionID != "ABCD-0" {
		t.Errorf("session id = %q", tl.sessionID)
	}
	if !strings.Contains(tl.dql, `dt.rum.session.id == "ABCD-0"`) {
		t.Errorf("timeline query:\n%s", tl.dql)
	}
	// The default lens shows the journey skeleton, not the request firehose.
	if !strings.Contains(tl.dql, "view_summary") || strings.Contains(tl.dql, `"request"`) {
		t.Errorf("journey lens must skip requests:\n%s", tl.dql)
	}
	// 'e' drops into the session's flat, sortable events table.
	press(a, key("e"))
	tv, ok := a.top().(*tableView)
	if !ok || tv.spec.Name != "userevents" || tv.scope.Arg != "ABCD-0" {
		t.Fatalf("e on the timeline → %s", a.top().Crumb())
	}
}

// TestGenAITracesDrill: 's' on a GenAI entity lands on the traces view with
// the genai lens and the dot-namespace scope filter.
func TestGenAITracesDrill(t *testing.T) {
	a := testApp(t, "genai")
	seedRows(t, a, []map[string]any{{"id": "GENAI_AGENT-1", "name": "sre-agent", "type": "GENAI_AGENT"}})
	press(a, key("s"))
	tv, ok := a.top().(*tableView)
	if !ok || tv.spec.Name != "traces" {
		t.Fatalf("s on a genai entity → %s", a.top().Crumb())
	}
	if got := tv.spec.LensAt(tv.scope.Lens).Name; got != "genai" {
		t.Errorf("genai traces drill must open the genai lens, got %s", got)
	}
	if !strings.Contains(tv.dql, `dt.smartscape.gen_ai.agent == toSmartscapeId("GENAI_AGENT-1")`) {
		t.Errorf("traces must scope via dt.smartscape.gen_ai.agent:\n%s", tv.dql)
	}
}

// TestDictionaryModelDrill: the dictionary is one lensed view — enter on a
// model opens its fields (fields lens, Arg = model); enter on a field opens
// the full definition.
func TestDictionaryModelDrill(t *testing.T) {
	a := testApp(t, "dictionary")
	tv, ok := a.top().(*tableView)
	if !ok || tv.spec.LensAt(tv.scope.Lens).Name != "models" {
		t.Fatalf("dictionary must open on the models lens, top = %s", a.top().Crumb())
	}
	seedRows(t, a, []map[string]any{{"name": "span", "fields": []any{"span.id"}, "title": "Span"}})
	press(a, key("enter"))
	fieldsView, ok := a.top().(*tableView)
	if !ok || fieldsView.spec.Name != "dictionary" {
		t.Fatalf("enter on a model → %s", a.top().Crumb())
	}
	if fieldsView.scope.Arg != "span" || fieldsView.scope.Lens != catalog.DictFieldsLens {
		t.Errorf("model drill scope = %+v", fieldsView.scope)
	}
	if !strings.Contains(fieldsView.dql, "| expand fields") {
		t.Errorf("model drill must expand the model's declared fields:\n%s", fieldsView.dql)
	}
	seedRows(t, a, []map[string]any{{"field_name": "span.id", "type": "string", "stability": "stable"}})
	press(a, key("enter"))
	if _, ok := a.top().(*inspectorView); !ok {
		t.Fatalf("enter on a field → %s", a.top().Crumb())
	}
}
