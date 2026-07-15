package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
)

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
	if got := tv.columns()[2].Title; got != "ERROR" {
		t.Errorf("errors lens column[2] = %q, want ERROR (the minimal why)", got)
	}
	// The exceptions lens: a string-match filter (iterative expressions are
	// rejected inside DQL filter) and its own thrown-first columns.
	press(a, key("]"))
	if !strings.Contains(tv.dql, `span_event.name`) {
		t.Errorf("] should advance to exceptions lens:\n%s", tv.dql)
	}
	if got := tv.columns()[1].Title; got != "EXCEPTION" {
		t.Errorf("exceptions lens column[1] = %q, want EXCEPTION", got)
	}
	press(a, key("]"))
	if !strings.Contains(tv.dql, `span.kind == "server"`) {
		t.Errorf("] should advance to server lens:\n%s", tv.dql)
	}
	press(a, key("["))
	if !strings.Contains(tv.dql, `span_event.name`) {
		t.Errorf("[ should return to exceptions lens:\n%s", tv.dql)
	}

	// The db lens swaps in its curated statement columns.
	for range 3 { // exceptions → server → client → db
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

// TestTabCyclesLensOnPlainTable: tab drives the view's primary strip — the
// lens strip on a top-level table — while digits stay global bookmarks there
// (no numbers on screen means no local claim).
func TestTabCyclesLensOnPlainTable(t *testing.T) {
	a := testApp(t, "traces")
	tv := a.top().(*tableView)
	start := tv.scope.Lens
	press(a, key("tab"))
	if tv.scope.Lens == start {
		t.Fatal("tab should advance the lens strip on a plain table")
	}
	press(a, key("shift+tab"))
	if tv.scope.Lens != start {
		t.Fatal("shift+tab should cycle the lens strip back")
	}
	press(a, key("1"))
	if top, ok := a.top().(*tableView); !ok || top.spec.Name != "problems" {
		t.Fatalf("digit on a top-level table must stay a global bookmark, top = %T", a.top())
	}
}
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
