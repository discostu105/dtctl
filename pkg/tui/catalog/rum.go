package catalog

import (
	"fmt"
	"strings"
	"time"
)

// Real user monitoring: user.sessions and user.events (New RUM Experience
// tables). Facts validated live: there is NO event.type on user.events — the
// discriminator is characteristics.classifier (request, error, user_action,
// view_summary, …) and the field set varies per classifier. Durations and web
// vitals are nanoseconds serialized as strings (CLS is a raw float). The
// session↔events join key is dt.rum.session.id on both tables, including the
// "-0" split suffix. Frontend linkage differs by table: dt.smartscape.frontend
// is a SCALAR on events and an ARRAY on sessions.

// rumEventFilter scopes user.events to a frontend (scalar field).
func rumEventFilter(e Entity) string {
	if e.Type != "FRONTEND" {
		return ""
	}
	return fmt.Sprintf("dt.smartscape.frontend == toSmartscapeId(%q)", e.ID)
}

// rumSessionFilter scopes user.sessions to a frontend. The field is an array
// there — matchesPhrase over its rendered form matches element-wise
// (validated live: 88 sessions on the probe tenant).
func rumSessionFilter(e Entity) string {
	if e.Type != "FRONTEND" {
		return ""
	}
	return fmt.Sprintf(`matchesPhrase(arrayToString(dt.smartscape.frontend, delimiter:","), %q)`, e.ID)
}

var sessionsSpec = &Spec{
	Name:    "sessions",
	Aliases: []string{"se", "session", "usersessions"},
	Kind:    KindSignal,
	Desc:    "RUM user sessions — enter opens a session's event timeline",
	Query: func(s Scope) string {
		var b strings.Builder
		// Sessions are sparse (single digits over 2h on the probe tenant) —
		// floor the window at 24h so the list is never misleadingly empty.
		fmt.Fprintf(&b, "fetch user.sessions, from:%s", floorTimeframe(s.Timeframe, 24*time.Hour, "24h"))
		if s.Entity != nil {
			if f := rumSessionFilter(*s.Entity); f != "" {
				fmt.Fprintf(&b, "\n| filter %s", f)
			}
		}
		if l := lensAt(sessionLenses, s.Lens); l.Filter != "" {
			fmt.Fprintf(&b, "\n| filter %s", l.Filter)
		}
		b.WriteString("\n| sort start_time desc\n| limit 300")
		return b.String()
	},
	Lenses: sessionLenses,
	Columns: []Column{
		{Title: "START", Width: 12, Value: func(rec map[string]any) string { return FormatTime(Str(rec, "start_time")) },
			Sort: func(rec map[string]any) any { return Str(rec, "start_time") }},
		{Title: "APP", Width: 20, Value: func(rec map[string]any) string { return StrFirst(rec, "frontend.name") }},
		{Title: "DURATION", Width: 8, Right: true, Value: func(rec map[string]any) string { return FormatNs(rec["duration"]) },
			Sort: func(rec map[string]any) any { return rec["duration"] }},
		{Title: "VIEWS", Field: "view_summary_count", Width: 5, Right: true},
		{Title: "ACTIONS", Field: "user_action_count", Width: 7, Right: true},
		{Title: "REQS", Field: "request_count", Width: 5, Right: true},
		{Title: "ERRORS", Field: "error.count", Width: 6, Right: true, Class: classNonzeroError},
		{Title: "BROWSER", Width: 14, Value: func(rec map[string]any) string {
			return strings.TrimSpace(Str(rec, "browser.name") + " " + Str(rec, "browser.version"))
		}},
		{Title: "OS", Field: "os.name", Width: 8},
		{Title: "GEO", Field: "geo.country.iso_code", Width: 3},
		{Title: "END", Field: "end_reason", Width: 10},
	},
	Entity: sessionFrontendEntity,
	// Enter opens the session timeline — the RUM waterfall (views > actions >
	// requests/errors on the session's time axis, 's' there jumps into a
	// request's backend trace); 'e' keeps the frontend's flat events table.
	EnterTarget: "session-timeline",
	Drills:      map[string]string{"m": "metrics", "p": "problems", "e": "userevents"},
}

var sessionLenses = []Lens{
	{Name: "all", Desc: "every session in the window"},
	{Name: "errors", Desc: "sessions with at least one error", Filter: "error.count > 0"},
	{Name: "bounced", Desc: "single-view sessions", Filter: "characteristics.is_bounce == true"},
}

var classNonzeroError = classNonzero("error")

// sessionFrontendEntity extracts the session's frontend (array fields — the
// first element stands for the session's app).
func sessionFrontendEntity(rec map[string]any) *Entity {
	id := StrFirst(rec, "dt.smartscape.frontend")
	if id == "" {
		return nil
	}
	return &Entity{ID: id, Name: StrFirst(rec, "frontend.name"), Type: "FRONTEND"}
}

var userEventsSpec = &Spec{
	Name:    "userevents",
	Aliases: []string{"ue", "rumevents", "actions"},
	Kind:    KindSignal,
	Desc:    "RUM user events by lens: errors, actions, page views, requests",
	Query: func(s Scope) string {
		var b strings.Builder
		if s.Arg != "" {
			// A session timeline (enter on a session row): its events, oldest
			// first, every classifier included. The window floors at 24h to
			// match the sessions list — a session picked from that list may
			// have started before the global window, and a 2h timeline would
			// silently clip its earliest events (found on a live drive).
			fmt.Fprintf(&b, "fetch user.events, from:%s", floorTimeframe(s.Timeframe, 24*time.Hour, "24h"))
			fmt.Fprintf(&b, "\n| filter dt.rum.session.id == %q", s.Arg)
			if l := lensAt(userEventLenses, s.Lens); l.Filter != "" {
				fmt.Fprintf(&b, "\n| filter %s", l.Filter)
			}
			b.WriteString("\n| sort start_time asc\n| limit 500")
			return b.String()
		}
		fmt.Fprintf(&b, "fetch user.events, from:%s", s.Timeframe.DQL())
		if s.Entity != nil {
			if f := rumEventFilter(*s.Entity); f != "" {
				fmt.Fprintf(&b, "\n| filter %s", f)
			}
		}
		if l := lensAt(userEventLenses, s.Lens); l.Filter != "" {
			fmt.Fprintf(&b, "\n| filter %s", l.Filter)
		}
		b.WriteString("\n| sort start_time desc\n| limit 300")
		return b.String()
	},
	Lenses: userEventLenses,
	Columns: []Column{
		rumTimeColumn,
		{Title: "KIND", Field: "characteristics.classifier", Width: 12},
		{Title: "DETAIL", Value: userEventDetail},
		{Title: "VIEW", Width: 24, Value: func(rec map[string]any) string {
			if v := Str(rec, "view.name"); v != "" {
				return v
			}
			return Str(rec, "view.detected_name")
		}},
		rumDurationColumn,
	},
	Entity: func(rec map[string]any) *Entity {
		id := Str(rec, "dt.smartscape.frontend")
		if id == "" {
			return nil
		}
		return &Entity{ID: id, Name: Str(rec, "frontend.name"), Type: "FRONTEND"}
	},
	Trace: func(rec map[string]any) string { return Str(rec, "trace.id") },
	// 's' follows a request event into its backend trace; 'u' opens the
	// timeline of the session the event belongs to — events and sessions
	// stay two keystrokes apart in both directions.
	Drills: map[string]string{"s": "trace", "u": "session", "p": "problems"},
}

var rumTimeColumn = Column{
	Title: "TIME", Width: 12,
	Value: func(rec map[string]any) string { return FormatTime(Str(rec, "start_time")) },
	Sort:  func(rec map[string]any) any { return Str(rec, "start_time") },
}

var rumDurationColumn = Column{
	Title: "DURATION", Width: 8, Right: true,
	Value: func(rec map[string]any) string { return FormatNs(rec["duration"]) },
	Sort:  func(rec map[string]any) any { return rec["duration"] },
}

var userEventLenses = []Lens{
	{Name: "all", Desc: "every RUM event, newest first"},
	{Name: "errors", Desc: "JS exceptions, failed requests, console errors",
		Filter: `characteristics.classifier == "error"`, Columns: rumErrorColumns},
	{Name: "actions", Desc: "user actions (clicks, navigations)",
		Filter: `characteristics.classifier == "user_action"`, Columns: rumActionColumns},
	{Name: "views", Desc: "page/view summaries with Core Web Vitals",
		Filter: `characteristics.classifier == "view_summary"`, Columns: rumViewColumns},
	{Name: "requests", Desc: "XHR/fetch requests with status and timing",
		Filter: `characteristics.classifier == "request"`, Columns: rumRequestColumns},
}

// userEventDetail labels a mixed-classifier row with its most telling field.
func userEventDetail(rec map[string]any) string {
	for _, key := range []string{"error.display_name", "user_action.name", "url.path", "page.name"} {
		if v := Str(rec, key); v != "" {
			return v
		}
	}
	return Str(rec, "characteristics.classifier")
}

var rumErrorColumns = []Column{
	rumTimeColumn,
	{Title: "ERROR", Value: func(rec map[string]any) string {
		if v := Str(rec, "error.display_name"); v != "" {
			return v
		}
		return Str(rec, "error.name")
	}},
	{Title: "SOURCE", Field: "error.source", Width: 8},
	{Title: "TYPE", Field: "error.type", Width: 9},
	{Title: "VIEW", Width: 22, Value: func(rec map[string]any) string { return Str(rec, "view.name") }},
	{Title: "BROWSER", Field: "browser.name", Width: 10},
}

var rumActionColumns = []Column{
	rumTimeColumn,
	{Title: "ACTION", Field: "user_action.name"},
	{Title: "TYPE", Field: "user_action.type", Width: 15},
	{Title: "INTERACTION", Field: "interaction.type", Width: 11},
	{Title: "REQS", Field: "user_action.requests.count", Width: 5, Right: true},
	rumDurationColumn,
}

// vitalsMs renders a nanosecond web-vital as milliseconds.
func vitalsMs(rec map[string]any, key string) string {
	ms, ok := nsToMs(rec[key])
	if !ok {
		return ""
	}
	return fmt.Sprintf("%.0fms", ms)
}

var rumViewColumns = []Column{
	rumTimeColumn,
	{Title: "VIEW", Value: func(rec map[string]any) string { return Str(rec, "view.name") }},
	{Title: "LCP", Width: 7, Right: true, Class: classLCP,
		Value: func(rec map[string]any) string { return vitalsMs(rec, "web_vitals.largest_contentful_paint") },
		Sort:  func(rec map[string]any) any { return rec["web_vitals.largest_contentful_paint"] }},
	{Title: "INP", Width: 7, Right: true,
		Value: func(rec map[string]any) string { return vitalsMs(rec, "web_vitals.interaction_to_next_paint") },
		Sort:  func(rec map[string]any) any { return rec["web_vitals.interaction_to_next_paint"] }},
	{Title: "CLS", Width: 6, Right: true, Value: func(rec map[string]any) string {
		if f, ok := rec["web_vitals.cumulative_layout_shift"].(float64); ok {
			return fmt.Sprintf("%.3f", f)
		}
		return ""
	}},
	{Title: "TTFB", Width: 7, Right: true,
		Value: func(rec map[string]any) string { return vitalsMs(rec, "web_vitals.time_to_first_byte") }},
	{Title: "ERRORS", Width: 6, Right: true, Class: classNonzeroError, Value: rumViewErrors},
	rumDurationColumn,
}

// classLCP colors LCP against the Core Web Vitals thresholds (ms).
func classLCP(val string) string {
	ms, err := parseMsSuffix(val)
	if err != nil {
		return ""
	}
	switch {
	case ms > 4000:
		return "error"
	case ms > 2500:
		return "warn"
	}
	return "ok"
}

// rumViewErrors totals the view's error counters (fields serialize as
// strings or numbers depending on type — FloatValue coerces both).
func rumViewErrors(rec map[string]any) string {
	total := 0.0
	for _, key := range []string{"error.exception_count", "error.http_4xx_count", "error.http_5xx_count"} {
		if f, ok := FloatValue(rec[key]); ok {
			total += f
		}
	}
	if total == 0 {
		return "0"
	}
	return fmt.Sprintf("%.0f", total)
}

var rumRequestColumns = []Column{
	rumTimeColumn,
	{Title: "URL", Value: func(rec map[string]any) string { return Str(rec, "url.path") }},
	{Title: "METHOD", Field: "http.request.method", Width: 6},
	{Title: "STATUS", Field: "http.response.status_code", Width: 6, Right: true, Class: classHTTPStatus},
	{Title: "DOMAIN", Field: "url.domain", Width: 22},
	rumDurationColumn,
}

func classHTTPStatus(val string) string {
	switch {
	case strings.HasPrefix(val, "5"):
		return "error"
	case strings.HasPrefix(val, "4"):
		return "warn"
	}
	return ""
}
