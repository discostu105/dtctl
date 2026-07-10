package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

// Behavioral tests for the expansion wave: API-backed table views, the
// log-pattern drill, and the RUM session timeline.

func TestAPIViewRoutesThroughSource(t *testing.T) {
	a := testApp(t, "home")
	var gotScope catalog.Scope
	a.ds.sources = map[string]Source{
		"slos": func(ctx context.Context, scope catalog.Scope, dql string) ([]map[string]any, error) {
			gotScope = scope
			return []map[string]any{{"name": "checkout availability", "status": "SUCCESS", "sli": 99.98, "target": 99.9}}, nil
		},
	}
	tv := newTableView(a.ds, catalog.Lookup("slos"), catalog.Scope{Timeframe: a.tf})
	cmd := tv.Refresh()
	if cmd == nil {
		t.Fatal("API view Refresh returned no command")
	}
	msg := cmd()
	dm, ok := msg.(dataMsg)
	if !ok {
		t.Fatalf("source result = %T", msg)
	}
	tv.Update(dm)
	if len(tv.rows) != 1 || catalog.Str(tv.rows[0], "name") != "checkout availability" {
		t.Fatalf("rows = %v", tv.rows)
	}
	if gotScope.Timeframe.Label != a.tf.Label {
		t.Errorf("source received scope %+v", gotScope)
	}
	if echo := tv.Echo(); echo != "dtctl get slos" {
		t.Errorf("echo = %q", echo)
	}
	// Facets need fieldsSummary — refused on API views with a status message.
	if cmd := tv.handleKey(key("f")); cmd == nil {
		t.Error("facets on an API view must answer with a status")
	} else if sm, ok := cmd().(statusMsg); !ok || !sm.isErr {
		t.Errorf("facets on an API view = %v", cmd())
	}
}

func TestAPIViewMissingSourceErrors(t *testing.T) {
	a := testApp(t, "home")
	a.ds.sources = nil
	tv := newTableView(a.ds, catalog.Lookup("slos"), catalog.Scope{Timeframe: a.tf})
	msg := tv.Refresh()()
	dm, ok := msg.(dataMsg)
	if !ok || dm.err == nil || !strings.Contains(dm.err.Error(), "not wired") {
		t.Fatalf("missing source must surface an error, got %#v", msg)
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

func TestTimelineTraceJumpAndUserEventsDrill(t *testing.T) {
	a := testApp(t, "userevents")
	seedRows(t, a, []map[string]any{{
		"characteristics.classifier": "request",
		"dt.rum.session.id":          "SESS-0",
		"trace.id":                   "3c6d1553ae49e57bda57f9455899d471",
		"start_time":                 "2026-07-07T10:00:00Z",
	}})
	// 'u' on any RUM event opens the timeline of its session.
	press(a, key("u"))
	tl, ok := a.top().(*timelineView)
	if !ok || tl.sessionID != "SESS-0" {
		t.Fatalf("u on a RUM event → %s", a.top().Crumb())
	}
	// Seed the timeline with a request event carrying a trace — 's' jumps
	// into the backend trace waterfall.
	tl.Update(dataMsg{owner: tl, seq: tl.seq, records: []map[string]any{{
		"characteristics.classifier": "request",
		"start_time":                 "2026-07-07T10:00:01Z",
		"duration":                   "100000000",
		"trace.id":                   "3c6d1553ae49e57bda57f9455899d471",
		"url.path":                   "/v1/workspaces",
	}}})
	press(a, key("s"))
	wf, ok := a.top().(*waterfallView)
	if !ok || wf.traceID != "3c6d1553ae49e57bda57f9455899d471" {
		t.Fatalf("s on a request row → %s", a.top().Crumb())
	}
}

// TestFrontendDetailConnectsRUM: a frontend's detail page carries its user
// sessions and events as tabs — the frontend is wired into the RUM story.
func TestFrontendDetailConnectsRUM(t *testing.T) {
	a := testApp(t, "frontends")
	seedRows(t, a, []map[string]any{{"id": "FRONTEND-1", "name": "shop", "type": "FRONTEND"}})
	press(a, key("enter"))
	dv, ok := a.top().(*detailView)
	if !ok {
		t.Fatalf("enter on a frontend → %s", a.top().Crumb())
	}
	names := make([]string, len(dv.tabs))
	for i, tab := range dv.tabs {
		names[i] = tab.name
	}
	joined := strings.Join(names, " ")
	for _, want := range []string{"sessions", "userevents", "events", "problems"} {
		if !strings.Contains(joined, want) {
			t.Errorf("frontend tabs = %v, want %s", names, want)
		}
	}
	// Frontends emit no log records and spans carry no frontend field —
	// those tabs would be silently empty.
	if strings.Contains(joined, "logs") || strings.Contains(joined, "traces") {
		t.Errorf("frontend tabs must not include logs/traces: %v", names)
	}
}

// TestNestedLensStripOnDetailPage: the drill letters jump straight to their
// page tab, the brackets drive the active tab's own lens strip, and digits
// stay global hotkeys even there. GenAI entities open traces on the genai
// lens — their spans rarely include roots.
func TestNestedLensStripOnDetailPage(t *testing.T) {
	a := testApp(t, "genai")
	seedRows(t, a, []map[string]any{{"id": "GENAI_MODEL-1", "name": "claude", "type": "GENAI_MODEL"}})
	press(a, key("enter"))
	dv, ok := a.top().(*detailView)
	if !ok {
		t.Fatalf("enter on a genai entity → %s", a.top().Crumb())
	}
	traceIdx := -1
	for i, tab := range dv.tabs {
		if tab.name == "traces" {
			traceIdx = i
		}
	}
	if traceIdx < 0 {
		t.Fatalf("genai detail page has no traces tab (tabs %v)", dv.tabs)
	}
	// The drill letter jumps straight to the traces tab (digits are global).
	press(a, key("s"))
	if dv.active != traceIdx {
		t.Fatalf("s must jump to the traces tab, active = %d", dv.active)
	}
	inner, ok := dv.tabs[traceIdx].view.(*tableView)
	if !ok || inner.spec.Name != "traces" {
		t.Fatalf("traces tab view = %T", dv.tabs[traceIdx].view)
	}
	if got := inner.spec.LensAt(inner.scope.Lens).Name; got != "genai" {
		t.Errorf("genai entity's traces tab must open on the genai lens, got %s", got)
	}
	if !strings.Contains(inner.dql, "dt.smartscape.gen_ai.model") {
		t.Errorf("traces tab must scope via the gen_ai dot namespace:\n%s", inner.dql)
	}
	// The brackets drive the visible lens strip; the page tab must not change.
	press(a, key("]"))
	if _, still := a.top().(*detailView); !still {
		t.Fatalf("] on a lensed tab must not leave the page, top = %s", a.top().Crumb())
	}
	if dv.active != traceIdx {
		t.Errorf("] must not switch page tabs while a lens strip is visible")
	}
	if got := inner.spec.LensAt(inner.scope.Lens).Name; got != "all" {
		t.Errorf("] must cycle the inner lens genai → all, got %s", got)
	}
	// tab still cycles the page tabs.
	press(a, key("tab"))
	if dv.active == traceIdx {
		t.Error("tab must still cycle the page tabs")
	}
	// A digit fires its global hotkey even while a lens strip is on screen.
	press(a, key("1"))
	if tv, ok := a.top().(*tableView); !ok || tv.spec.Name != "problems" {
		t.Fatalf("digit must stay a global hotkey, top = %T", a.top())
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

// TestTimelineSessionRecord: 'd' on the timeline opens the session's own
// record — the user-event → session navigation.
func TestTimelineSessionRecord(t *testing.T) {
	a := testApp(t, "userevents")
	seedRows(t, a, []map[string]any{{
		"characteristics.classifier": "request",
		"dt.rum.session.id":          "SESS-1",
		"start_time":                 "2026-07-07T10:00:00Z",
	}})
	press(a, key("u"))
	tl, ok := a.top().(*timelineView)
	if !ok {
		t.Fatalf("u on a RUM event → %s", a.top().Crumb())
	}
	// Entered from an event drill there is no sessions-list row — the
	// record arrives via the lazy fetch.
	tl.Update(dataMsg{owner: sessOwner{tl}, records: []map[string]any{{
		"dt.rum.session.id": "SESS-1", "browser.name": "Chrome", "end_reason": "timeout",
	}}})
	press(a, key("d"))
	iv, ok := a.top().(*inspectorView)
	if !ok {
		t.Fatalf("d on the timeline → %s", a.top().Crumb())
	}
	if catalog.Str(iv.rec, "browser.name") != "Chrome" {
		t.Errorf("session record = %v", iv.rec)
	}
}

func TestRecordSamplerDerivesColumns(t *testing.T) {
	a := testApp(t, "home")
	tv := newTableView(a.ds, catalog.Lookup("records"), catalog.Scope{Timeframe: a.tf, Arg: "logs"})
	deliverView(tv, tv.Init())
	tv.Update(dataMsg{owner: tv, seq: tv.seq, records: []map[string]any{
		{"timestamp": "2026-07-07T10:00:00Z", "content": "hello", "loglevel": "INFO"},
	}})
	cols := tv.columns()
	if len(cols) < 3 {
		t.Fatalf("derived columns = %v", cols)
	}
	if cols[0].Title != "TIMESTAMP" {
		t.Errorf("timestamp must lead the derived set, got %s", cols[0].Title)
	}
}

func TestBucketEnterSamplesBucket(t *testing.T) {
	a := testApp(t, "buckets")
	seedRows(t, a, []map[string]any{{
		"name": "default_logs", "dt.system.table": "logs",
	}})
	press(a, key("enter"))
	tv, ok := a.top().(*tableView)
	if !ok || tv.spec.Name != "records" {
		t.Fatalf("enter on a bucket → %s", a.top().Crumb())
	}
	if !strings.Contains(tv.dql, `| filter dt.system.bucket == "default_logs"`) {
		t.Errorf("bucket sampler query:\n%s", tv.dql)
	}
}

func TestNonFetchableTableRefusesEnter(t *testing.T) {
	a := testApp(t, "tables")
	seedRows(t, a, []map[string]any{{
		"name": "metrics", "usable_with": []any{"fieldsSnapshot"},
	}})
	press(a, key("enter"))
	if tv, ok := a.top().(*tableView); !ok || tv.spec.Name != "tables" {
		t.Fatalf("non-fetchable enter must stay put, top = %s", a.top().Crumb())
	}
	if a.status == "" {
		t.Error("refusal must explain itself in the status line")
	}
}

func TestServerSearchRefusedWithoutQuery(t *testing.T) {
	a := testApp(t, "home")
	a.ds.sources = map[string]Source{
		"anomaly-detectors": func(context.Context, catalog.Scope, string) ([]map[string]any, error) {
			return []map[string]any{{"title": "burn rate", "enabled": "true"}}, nil
		},
	}
	tv := newTableView(a.ds, catalog.Lookup("detectors"), catalog.Scope{Timeframe: a.tf})
	deliverView(tv, tv.Init())
	tv.Update(tv.Refresh()().(dataMsg))
	// '/' then enter: the term must stay a client filter, not a server search.
	tv.handleKey(key("/"))
	for _, r := range "burn" {
		tv.handleKey(key(string(r)))
	}
	cmd := tv.handleKey(key("enter"))
	if len(tv.searches) != 0 {
		t.Errorf("API view without query must not stack server searches: %v", tv.searches)
	}
	if tv.filter != "burn" {
		t.Errorf("client filter must survive, got %q", tv.filter)
	}
	if cmd == nil {
		t.Error("the refusal must explain itself")
	}
}
