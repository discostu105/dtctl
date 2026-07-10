package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
)

// TestTablePreviewPane: tab toggles the peek pane; it renders the selected
// record's priority fields (severity colored) entirely client-side.
func TestTablePreviewPane(t *testing.T) {
	a := testApp(t, "logs")
	seedRows(t, a, []map[string]any{{
		"timestamp": "2026-07-06T16:18:52.000000000Z",
		"loglevel":  "ERROR",
		"content":   "connection refused to db",
		"trace_id":  "7485b342cc6046e1d820a3a397141d12",
	}})
	tv := a.top().(*tableView)
	if tv.previewOn {
		t.Fatal("preview should start off")
	}
	press(a, key("tab"))
	if !tv.previewOn || !tv.previewSide() {
		t.Fatalf("tab should toggle the side preview on a 120-wide screen (on=%v side=%v)", tv.previewOn, tv.previewSide())
	}
	body := ansi.Strip(tv.View(120, 30))
	for _, want := range []string{"loglevel: ERROR", "connection refused", "trace:"} {
		if !strings.Contains(body, want) {
			t.Errorf("preview missing %q:\n%s", want, body)
		}
	}
	// Narrow screens fall back to the bottom panel, and paging accounts for it.
	tv.width = 80
	if !tv.previewBottom() {
		t.Fatal("narrow screens should use the bottom panel")
	}
	withPreview := tv.pageSize()
	tv.previewOn = false
	if tv.pageSize() <= withPreview {
		t.Error("bottom preview must shrink the page size")
	}
}

// TestTablePreviewEntityRow: entity rows preview their curated key facts from
// the list row itself — no extra query.
func TestTablePreviewEntityRow(t *testing.T) {
	a := testApp(t, "hosts")
	seedRows(t, a, []map[string]any{hostRow()})
	tv := a.top().(*tableView)
	press(a, key("tab"))
	body := ansi.Strip(tv.View(120, 30))
	for _, want := range []string{"web-01.example.invalid", "HOST", "os:", "memory:"} {
		if !strings.Contains(body, want) {
			t.Errorf("entity preview missing %q:\n%s", want, body)
		}
	}
}

// TestWaterfallAnchorsToOriginSpan: entering a trace from a specific span row
// selects that span instead of leaving the cursor at the trace root.
func TestWaterfallAnchorsToOriginSpan(t *testing.T) {
	a := testApp(t, "traces")
	seedRows(t, a, []map[string]any{{
		"trace.id": "140ea4cf0d16aa99aadde231773bd127", "span.id": "bbbbbbbbbbbbbbbb",
		"span.name": "child op", "span.kind": "internal",
		"start_time": "2026-07-06T16:18:52.100000000Z", "end_time": "2026-07-06T16:18:52.200000000Z",
	}})
	press(a, key("enter"))
	wf, ok := a.top().(*waterfallView)
	if !ok {
		t.Fatalf("enter on a span should open the waterfall, top = %T", a.top())
	}
	if wf.focusSpan != "bbbbbbbbbbbbbbbb" {
		t.Fatalf("focus span = %q, want the originating span", wf.focusSpan)
	}
	wf.Update(dataMsg{owner: wf, seq: wf.seq, records: []map[string]any{
		{"span.id": "aaaaaaaaaaaaaaaa", "span.name": "GET /", "span.kind": "server",
			"start_time": "2026-07-06T16:18:52.000000000Z", "end_time": "2026-07-06T16:18:53.000000000Z"},
		{"span.id": "bbbbbbbbbbbbbbbb", "span.parent_id": "aaaaaaaaaaaaaaaa",
			"span.name": "child op", "span.kind": "internal",
			"start_time": "2026-07-06T16:18:52.100000000Z", "end_time": "2026-07-06T16:18:52.200000000Z"},
	}})
	if wf.cursor != 1 {
		t.Errorf("cursor = %d, want anchored on the originating span", wf.cursor)
	}
	if wf.focusSpan != "" {
		t.Error("the anchor must be consumed so refreshes keep the user's cursor")
	}
}

// TestHomeSelectionAndYank: home rows are first-class selections — y copies
// the row's identity and rows with a Smartscape id yield an entity for the
// global actions (pin, x/X, o).
func TestHomeSelectionAndYank(t *testing.T) {
	a := testApp(t, "home")
	h := a.top().(*homeView)
	h.Update(dataMsg{owner: panelOwner{v: h, idx: 0}, seq: h.seq,
		records: []map[string]any{{"display_id": "P-77", "name": "cpu saturation"}}})
	h.Update(dataMsg{owner: panelOwner{v: h, idx: 1}, seq: h.seq,
		records: []map[string]any{{"dt.smartscape.service": "SERVICE-0000000000000001", "svc": "checkout"}}})

	h.focus, h.cursor = 0, 0
	if text, _, ok := h.YankText(); !ok || text != "P-77" {
		t.Errorf("problems panel yank = %q, %v", text, ok)
	}
	if _, e := h.Selection(); e != nil {
		t.Error("summarized problem rows carry no entity")
	}

	h.focus, h.cursor = 1, 0
	rec, e := h.Selection()
	if rec == nil || e == nil || e.ID != "SERVICE-0000000000000001" || e.Type != "SERVICE" {
		t.Fatalf("services panel selection = %+v", e)
	}
	// The global x now works from home: it walks the selected service.
	press(a, key("x"))
	nv, ok := a.top().(*navView)
	if !ok || nv.mode != navWalk || nv.root.ID != "SERVICE-0000000000000001" {
		t.Fatalf("x on a home service row should open the walk, top = %T", a.top())
	}
}

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

// TestNavigatorSpacePagesZFolds: space pages down like every other list; z
// folds the selected group.
func TestNavigatorSpacePagesZFolds(t *testing.T) {
	a := testApp(t, "pods")
	seedRows(t, a, []map[string]any{podRow("checkout-1", "shop", "Running", 0)})
	press(a, key("X"))
	v := seedNav(t, a, []map[string]any{
		edgeRec("K8S_POD-checkout-1", "K8S_POD", "runs_on", "K8S_NODE-1", "K8S_NODE"),
		edgeRec("SERVICE-1", "SERVICE", "routes_to", "K8S_POD-checkout-1", "K8S_POD"),
	})
	v.previewOn = false

	press(a, key("j")) // onto the runs_on group row
	rows := len(v.rows)
	press(a, key("z"))
	if len(v.rows) >= rows {
		t.Errorf("z should fold the group (%d → %d rows)", rows, len(v.rows))
	}
	press(a, key("z")) // unfold again
	before := v.cursor
	press(a, key(" "))
	if len(v.rows) != rows {
		t.Error("space must not fold groups anymore")
	}
	if v.cursor <= before {
		t.Errorf("space should page the cursor down (%d → %d)", before, v.cursor)
	}
}
