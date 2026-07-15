package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
)

// TestSamplerDefaultsToTimestampSort: sampling a timestamped table orders the
// fetched page newest-first client-side (the server query cannot sort blindly
// — a missing field is a hard FIELD_DOES_NOT_EXIST), and severity-shaped
// columns pick up the shared classes.
func TestSamplerDefaultsToTimestampSort(t *testing.T) {
	a := testApp(t, "logs")
	tv := newTableView(a.ds, catalog.Lookup("records"), catalog.Scope{Arg: "dt.system.events", Timeframe: a.tf})
	tv.Update(bodySizeMsg{width: 120, height: 30})
	tv.seq, tv.loading = 1, true
	tv.Update(dataMsg{owner: tv, seq: 1, records: []map[string]any{
		{"timestamp": "2026-07-10T10:00:00Z", "event.kind": "OLD", "status": "WARN"},
		{"timestamp": "2026-07-10T12:00:00Z", "event.kind": "NEW", "status": "SUCCEEDED"},
	}})
	if tv.sortCol < 0 || !tv.sortDesc {
		t.Fatalf("sampler should default to timestamp desc, sortCol = %d", tv.sortCol)
	}
	if catalog.Str(tv.rows[0], "event.kind") != "NEW" {
		t.Errorf("rows not newest-first: %v", tv.rows)
	}
	var statusCol *catalog.Column
	for i := range tv.columns() {
		if tv.columns()[i].Field == "status" {
			statusCol = &tv.columns()[i]
		}
	}
	if statusCol == nil || statusCol.Class == nil || statusCol.Class("WARN") != "warn" {
		t.Error("derived status column must class like a log level")
	}
}

// TestRecordsCommandRoutesToTables: the no-argument record sampler was a
// duplicate of the tables browser — the command-bar jump lands there instead.
func TestRecordsCommandRoutesToTables(t *testing.T) {
	a := testApp(t, "records")
	tv, ok := a.top().(*tableView)
	if !ok || tv.spec.Name != "tables" {
		t.Fatalf(":records should open the tables browser, top = %v", a.top().Crumb())
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
