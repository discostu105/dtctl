package catalog

import (
	"fmt"
	"strings"
	"time"
)

// Session timeline: one RUM session's events laid out on the session's time
// axis, nested by containment — views at the root, user actions under the
// view they happened in, requests and errors under the action (or view)
// whose window contains them. The trace-waterfall idea applied to a user
// session; request events carry trace.id (validated live), so a row jumps
// straight into the backend trace.
//
// A busy session is dominated by request noise (16k requests vs 65 actions
// on the probe session), so the default lens shows the journey skeleton and
// requests are one lens away.

// SessionTimelineLenses slice the session's event stream.
var SessionTimelineLenses = []Lens{
	{Name: "journey", Desc: "views, user actions, navigations, and errors — the session's story",
		Filter: `in(characteristics.classifier, {"view_summary", "user_action", "navigation", "error"})`},
	{Name: "requests", Desc: "the journey plus XHR/fetch requests (busy sessions truncate at the cap)",
		Filter: `in(characteristics.classifier, {"view_summary", "user_action", "navigation", "error", "request"})`},
	{Name: "errors", Desc: "errors only",
		Filter: `characteristics.classifier == "error"`},
	{Name: "all", Desc: "every event of the session, whatever its kind"},
}

// SessionTimelineQuery fetches one session's events oldest-first. The window
// floors at 24h like the sessions list — a session picked there may have
// started before the global window.
func SessionTimelineQuery(sessionID string, tf Timeframe, lens int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "fetch user.events, from:%s", floorTimeframe(tf, 24*time.Hour, "24h"))
	fmt.Fprintf(&b, "\n| filter dt.rum.session.id == %q", sessionID)
	if l := lensAt(SessionTimelineLenses, lens); l.Filter != "" {
		fmt.Fprintf(&b, "\n| filter %s", l.Filter)
	}
	b.WriteString("\n| sort start_time asc\n| limit 1000")
	return b.String()
}

// SessionRecordQuery fetches the session's own record (browser, OS, geo,
// duration, end reason) — the timeline arrives with it from the sessions
// list but fetches it when entered from a user-event drill or a history
// restore. Same 24h floor as the sessions list.
func SessionRecordQuery(sessionID string, tf Timeframe) string {
	return fmt.Sprintf("fetch user.sessions, from:%s\n| filter dt.rum.session.id == %q\n| limit 1",
		floorTimeframe(tf, 24*time.Hour, "24h"), sessionID)
}

// TimelineRow is one event positioned on the session's time axis.
type TimelineRow struct {
	Rec    map[string]any
	Depth  int    // 0 = view, 1 = inside a view, 2 = inside an action
	Kind   string // short classifier: view · action · request · error · nav · …
	Label  string
	Start  int64 // ns since epoch
	End    int64
	Failed bool
	Trace  string // backend trace id (request events; "" = none)
}

// BuildSessionTimeline nests chronologically-ordered session events by time
// containment. view_summary windows span the whole view (their start_time is
// the view's start — validated live), so sorted-by-start emission puts every
// contained event right after its container.
func BuildSessionTimeline(records []map[string]any) []TimelineRow {
	type window struct{ start, end int64 }
	within := func(w *window, at int64) bool {
		return w != nil && at >= w.start && at <= w.end
	}

	rows := make([]TimelineRow, 0, len(records))
	var view, action *window
	for _, rec := range records {
		start := timeNs(Str(rec, "start_time"))
		end := start
		if dur, ok := FloatValue(rec["duration"]); ok && dur > 0 {
			end = start + int64(dur)
		}
		classifier := Str(rec, "characteristics.classifier")
		row := TimelineRow{
			Rec:   rec,
			Kind:  timelineKind(classifier),
			Label: timelineLabel(rec, classifier),
			Start: start,
			End:   end,
			Trace: Str(rec, "trace.id"),
		}
		switch classifier {
		case "view_summary", "page_summary":
			row.Depth = 0
			view = &window{start, end}
			action = nil
		case "user_action":
			if within(view, start) {
				row.Depth = 1
			}
			action = &window{start, end}
		default:
			switch {
			case within(action, start):
				row.Depth = 2
			case within(view, start):
				row.Depth = 1
			}
		}
		status := Str(rec, "http.response.status_code")
		row.Failed = classifier == "error" || strings.HasPrefix(status, "5") || strings.HasPrefix(status, "4")
		rows = append(rows, row)
	}
	return rows
}

// timelineKind shortens a classifier for the kind column.
func timelineKind(classifier string) string {
	switch classifier {
	case "view_summary":
		return "view"
	case "page_summary":
		return "page"
	case "user_action":
		return "action"
	case "user_interaction":
		return "interact"
	case "visibility_change":
		return "visib"
	case "navigation":
		return "nav"
	}
	return classifier // request, error, property, other, …
}

// timelineLabel picks the row's telling text per classifier.
func timelineLabel(rec map[string]any, classifier string) string {
	switch classifier {
	case "view_summary", "page_summary":
		for _, key := range []string{"view.name", "view.detected_name", "page.name", "url.path"} {
			if v := Str(rec, key); v != "" {
				return v
			}
		}
	case "user_action":
		if v := Str(rec, "user_action.name"); v != "" {
			return v
		}
	case "error":
		for _, key := range []string{"error.display_name", "error.name"} {
			if v := Str(rec, key); v != "" {
				return v
			}
		}
	case "request":
		label := strings.TrimSpace(Str(rec, "http.request.method") + " " + Str(rec, "url.path"))
		if status := Str(rec, "http.response.status_code"); status != "" {
			label += " → " + status
		}
		if label != "" {
			return label
		}
	case "navigation":
		if v := Str(rec, "url.path"); v != "" {
			return v
		}
	}
	return userEventDetail(rec)
}

// timeNs parses an ISO timestamp to ns since epoch (0 when unparseable).
func timeNs(iso string) int64 {
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		return 0
	}
	return t.UnixNano()
}
