package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestTablePreviewPane: the peek pane is on by default (it renders the
// selected record's priority fields entirely client-side); P is the app-wide
// toggle, and cramped terminals auto-hide the bottom panel.
func TestTablePreviewPane(t *testing.T) {
	a := testApp(t, "logs")
	seedRows(t, a, []map[string]any{{
		"timestamp": "2026-07-06T16:18:52.000000000Z",
		"loglevel":  "ERROR",
		"content":   "connection refused to db",
		"trace_id":  "7485b342cc6046e1d820a3a397141d12",
	}})
	tv := a.top().(*tableView)
	if !tv.previewSide() {
		t.Fatal("preview should be on by default (side pane on a 120-wide screen)")
	}
	body := ansi.Strip(tv.View(120, 30))
	for _, want := range []string{"level: ERROR", "connection refused", "trace:", "(s opens)"} {
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
	// P flips the app-wide preference — the pane disappears everywhere.
	press(a, key("P"))
	if a.ds.previewOn() || tv.previewBottom() {
		t.Fatal("P should turn the preview off")
	}
	if tv.pageSize() <= withPreview {
		t.Error("hiding the bottom preview must grow the page size")
	}
	press(a, key("P"))
	if !a.ds.previewOn() || !tv.previewBottom() {
		t.Fatal("P should turn the preview back on")
	}
	// Short screens auto-hide the bottom panel: its ten-line bite would
	// starve the table.
	tv.height = previewBottomMinHeight - 1
	if tv.previewBottom() {
		t.Error("short screens must auto-hide the bottom panel")
	}
}

// TestTablePreviewSignalKinds: signal rows that reference an entity (a
// session's frontend, a RUM event's app) preview as the SIGNAL they are —
// curated per-kind facts, humanized durations, cross-signal jump hints — not
// as a sparse entity-facts pane.
func TestTablePreviewSignalKinds(t *testing.T) {
	a := testApp(t, "sessions")
	seedRows(t, a, []map[string]any{{
		"start_time": "2026-07-06T16:18:52.000000000Z", "duration": "185000000000",
		"view_summary_count": "3", "user_action_count": "12", "request_count": "87",
		"error.count": "2", "browser.name": "Chrome", "os.name": "Windows",
		"end_reason":             "timeout",
		"dt.smartscape.frontend": []any{"FRONTEND-0000000000000001"},
		"frontend.name":          []any{"www.example.invalid"},
	}})
	body := ansi.Strip(a.top().(*tableView).View(120, 30))
	for _, want := range []string{"www.example.invalid session", "duration: 3m05s",
		"activity: 3 views · 12 actions · 87 requests", "errors: 2", "end: timeout"} {
		if !strings.Contains(body, want) {
			t.Errorf("session preview missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "sparse list row") {
		t.Error("a session row must not fall into the entity key-facts path")
	}

	// A RUM request event: classifier-specific facts plus both jump hints —
	// the backend trace (s) and the owning session (u).
	a = testApp(t, "userevents")
	seedRows(t, a, []map[string]any{{
		"characteristics.classifier": "request", "start_time": "2026-07-06T16:18:52.000000000Z",
		"http.request.method": "POST", "http.response.status_code": "500",
		"url.path": "/api/orders", "url.domain": "www.example.invalid",
		"duration": "230000000", "trace.id": "140ea4cf0d16aa99aadde231773bd127",
		"dt.rum.session.id": "abcdef0123456789-0",
	}})
	body = ansi.Strip(a.top().(*tableView).View(120, 30))
	for _, want := range []string{"/api/orders", "response: POST 500", "duration: 230.0ms",
		"trace: 140ea4cf… (s opens)", "session: abcdef01… (u opens)"} {
		if !strings.Contains(body, want) {
			t.Errorf("RUM event preview missing %q:\n%s", want, body)
		}
	}

	// A span row: humanized duration, and the trace hint names enter (the
	// traces view has no 's' drill — enter opens the waterfall).
	a = testApp(t, "traces")
	seedRows(t, a, []map[string]any{{
		"span.name": "GET /orders", "span.kind": "server", "service.name": "checkout",
		"duration": "4800000", "start_time": "2026-07-06T16:18:52.000000000Z",
		"trace.id": "140ea4cf0d16aa99aadde231773bd127", "request.is_failed": true,
	}})
	body = ansi.Strip(a.top().(*tableView).View(120, 30))
	for _, want := range []string{"status: failed", "duration: 4.8ms", "kind: server",
		"trace: 140ea4cf… (enter opens)"} {
		if !strings.Contains(body, want) {
			t.Errorf("span preview missing %q:\n%s", want, body)
		}
	}
}

// TestWaterfallAndTimelinePreview: the trace waterfall and the session
// timeline carry the same peek pane as the tables — the highlighted
// span/event previews its curated facts plus its offset on the local time
// axis, and the P toggle governs it like everywhere else.
func TestWaterfallAndTimelinePreview(t *testing.T) {
	a := testApp(t, "traces")
	seedRows(t, a, []map[string]any{{
		"trace.id": "140ea4cf0d16aa99aadde231773bd127", "span.id": "aaaaaaaaaaaaaaaa",
		"span.name": "GET /checkout", "span.kind": "server",
		"start_time": "2026-07-06T16:18:52.000000000Z", "end_time": "2026-07-06T16:18:53.000000000Z",
	}})
	press(a, key("enter"))
	wf := a.top().(*waterfallView)
	wf.Update(dataMsg{owner: wf, seq: wf.seq, records: []map[string]any{
		{"span.id": "aaaaaaaaaaaaaaaa", "span.name": "GET /checkout", "span.kind": "server",
			"service.name": "checkout", "duration": "1000000000",
			"start_time": "2026-07-06T16:18:52.000000000Z", "end_time": "2026-07-06T16:18:53.000000000Z"},
		{"span.id": "bbbbbbbbbbbbbbbb", "span.parent_id": "aaaaaaaaaaaaaaaa",
			"span.name": "SELECT orders", "span.kind": "client", "service.name": "checkout",
			"db.system.name": "postgres", "db.namespace": "orders",
			"db.query.text": "SELECT * FROM orders", "duration": "120000000",
			"start_time": "2026-07-06T16:18:52.200000000Z", "end_time": "2026-07-06T16:18:52.320000000Z"},
	}})
	wf.cursor = 1
	body := ansi.Strip(wf.View(120, 30))
	for _, want := range []string{"db: postgres · orders", "SELECT * FROM orders",
		"duration: 120.0ms", "kind: client", "offset: 200.0ms into the trace"} {
		if !strings.Contains(body, want) {
			t.Errorf("waterfall preview missing %q:\n%s", want, body)
		}
	}
	// P hides the pane here too.
	press(a, key("P"))
	if body := ansi.Strip(wf.View(120, 30)); strings.Contains(body, "db: postgres") {
		t.Error("P must hide the waterfall preview")
	}
	press(a, key("P"))

	a = testApp(t, "sessions")
	seedRows(t, a, []map[string]any{{"dt.rum.session.id": "abcdef0123456789-0"}})
	press(a, key("enter"))
	tl, ok := a.top().(*timelineView)
	if !ok {
		t.Fatalf("enter on a session should open the timeline, top = %T", a.top())
	}
	tl.Update(dataMsg{owner: tl, seq: tl.seq, records: []map[string]any{
		{"characteristics.classifier": "view_summary", "view.name": "/checkout",
			"start_time": "2026-07-06T16:18:52.000000000Z", "duration": "60000000000",
			"web_vitals.largest_contentful_paint": "3000000000"},
		{"characteristics.classifier": "request", "http.request.method": "POST",
			"http.response.status_code": "500", "url.path": "/api/orders",
			"url.domain": "www.example.invalid", "duration": "230000000",
			"start_time": "2026-07-06T16:18:53.000000000Z",
			"trace.id":   "140ea4cf0d16aa99aadde231773bd127"},
	}})
	tl.cursor = 1
	body = ansi.Strip(tl.View(120, 30))
	for _, want := range []string{"response: POST 500", "domain: www.example.invalid",
		"duration: 230.0ms", "offset: 1.00s into the session", "trace: 140ea4cf… (s opens)"} {
		if !strings.Contains(body, want) {
			t.Errorf("timeline preview missing %q:\n%s", want, body)
		}
	}
	// The view row previews its vitals under its view-name title.
	tl.cursor = 0
	body = ansi.Strip(tl.View(120, 30))
	for _, want := range []string{"/checkout", "LCP: 3000ms"} {
		if !strings.Contains(body, want) {
			t.Errorf("timeline view-row preview missing %q:\n%s", want, body)
		}
	}
}

// TestTablePreviewEntityRow: entity rows preview their curated key facts from
// the list row itself — no extra query, no toggle needed.
func TestTablePreviewEntityRow(t *testing.T) {
	a := testApp(t, "hosts")
	seedRows(t, a, []map[string]any{hostRow()})
	tv := a.top().(*tableView)
	body := ansi.Strip(tv.View(120, 30))
	for _, want := range []string{"web-01.example.invalid", "HOST", "os:", "memory:"} {
		if !strings.Contains(body, want) {
			t.Errorf("entity preview missing %q:\n%s", want, body)
		}
	}
}
