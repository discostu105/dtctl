package tui

import (
	"testing"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
)

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
