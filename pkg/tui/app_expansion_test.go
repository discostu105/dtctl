package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
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
	tv, ok := a.top().(*tableView)
	if !ok || tv.spec.Name != "userevents" {
		t.Fatalf("enter on a session → %s", a.top().Crumb())
	}
	if tv.scope.Arg != "ABCD-0" {
		t.Errorf("session arg = %q", tv.scope.Arg)
	}
	if !strings.Contains(tv.dql, `dt.rum.session.id == "ABCD-0"`) {
		t.Errorf("timeline query:\n%s", tv.dql)
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
