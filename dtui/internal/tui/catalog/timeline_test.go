package catalog

import (
	"strings"
	"testing"
)

func TestSessionTimelineQuery(t *testing.T) {
	q := SessionTimelineQuery("SESS-0", DefaultTimeframe, 0)
	if !strings.Contains(q, `| filter dt.rum.session.id == "SESS-0"`) {
		t.Errorf("query must scope to the session:\n%s", q)
	}
	// The 2h default floors to 24h — sessions picked from the (24h-floored)
	// sessions list may predate the global window.
	if !strings.Contains(q, "from:now() - 24h") {
		t.Errorf("window must floor at 24h:\n%s", q)
	}
	if !strings.Contains(q, "view_summary") || strings.Contains(q, `"request"`) {
		t.Errorf("journey lens must show the skeleton without requests:\n%s", q)
	}
	if !strings.Contains(q, "| sort start_time asc") {
		t.Errorf("timeline must be chronological:\n%s", q)
	}
	// The requests lens adds the request classifier; "all" drops the filter.
	if q := SessionTimelineQuery("SESS-0", DefaultTimeframe, 1); !strings.Contains(q, `"request"`) {
		t.Errorf("requests lens must include requests:\n%s", q)
	}
	if q := SessionTimelineQuery("SESS-0", DefaultTimeframe, 3); strings.Contains(q, "characteristics.classifier") {
		t.Errorf("all lens must not filter classifiers:\n%s", q)
	}
}

// TestBuildSessionTimeline covers the containment nesting: views at the
// root, actions inside their view, requests/errors inside the action (or
// view) whose window contains them.
func TestBuildSessionTimeline(t *testing.T) {
	at := func(sec int) string { return "2026-07-07T10:00:0" + string(rune('0'+sec)) + "Z" }
	records := []map[string]any{
		{"characteristics.classifier": "view_summary", "view.name": "/checkout",
			"start_time": at(0), "duration": "9000000000"}, // 0–9s
		{"characteristics.classifier": "user_action", "user_action.name": "click pay",
			"start_time": at(1), "duration": "2000000000"}, // 1–3s
		{"characteristics.classifier": "request", "url.path": "/v1/pay",
			"http.request.method": "POST", "http.response.status_code": "502",
			"start_time": at(2), "duration": "500000000",
			"trace.id": "3c6d1553ae49e57bda57f9455899d471"}, // inside the action
		{"characteristics.classifier": "error", "error.display_name": "PayFailed",
			"start_time": at(4)}, // inside the view, outside the action
	}
	rows := BuildSessionTimeline(records)
	if len(rows) != 4 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0].Depth != 0 || rows[0].Kind != "view" || rows[0].Label != "/checkout" {
		t.Errorf("view row = %+v", rows[0])
	}
	if rows[1].Depth != 1 || rows[1].Kind != "action" || rows[1].Label != "click pay" {
		t.Errorf("action row = %+v", rows[1])
	}
	if rows[2].Depth != 2 || rows[2].Kind != "request" {
		t.Errorf("request row = %+v", rows[2])
	}
	if rows[2].Label != "POST /v1/pay → 502" {
		t.Errorf("request label = %q", rows[2].Label)
	}
	if !rows[2].Failed {
		t.Error("5xx request must be marked failed")
	}
	if rows[2].Trace == "" {
		t.Error("request row must carry its backend trace id")
	}
	if rows[3].Depth != 1 || rows[3].Kind != "error" || !rows[3].Failed {
		t.Errorf("error row = %+v", rows[3])
	}
	// Bars need the time axis: the view spans 9s.
	if rows[0].End-rows[0].Start != 9_000_000_000 {
		t.Errorf("view window = %d ns", rows[0].End-rows[0].Start)
	}
}
