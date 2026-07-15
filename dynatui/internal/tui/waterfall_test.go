package tui

import (
	"testing"
)

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
