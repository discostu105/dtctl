package catalog

import (
	"fmt"
	"strings"
)

// Distributed-tracing views. Validated live: trace.id is a UID type — string
// literals silently match nothing, toUid() is mandatory. Durations are
// nanoseconds serialized as strings; request.is_failed exists only on
// server/entry spans (null elsewhere, countIf handles it).

var tracesSpec = &Spec{
	Name:    "traces",
	Aliases: []string{"tr", "trace", "spans"},
	Kind:    KindSignal,
	Desc:    "Distributed traces (spans grouped by trace)",
	Query: func(s Scope) string {
		var b strings.Builder
		fmt.Fprintf(&b, "fetch spans, from:%s", s.Timeframe.DQL())
		if s.Entity != nil {
			fmt.Fprintf(&b, "\n| filter %s", SpanFilter(*s.Entity))
		}
		b.WriteString(`
| summarize spans = count(), failed = countIf(request.is_failed == true), start = min(start_time), dur = max(end_time) - min(start_time), root = takeFirst(if(isNull(span.parent_id), coalesce(endpoint.name, span.name))), svc = takeFirst(if(isNull(span.parent_id), service.name)), by:{trace.id}
| sort start desc
| limit 100`)
		return b.String()
	},
	Columns: []Column{
		{Title: "START", Width: 12, Value: func(rec map[string]any) string { return FormatTime(Str(rec, "start")) },
			Sort: func(rec map[string]any) any { return Str(rec, "start") }},
		{Title: "TRACE", Value: traceLabel},
		{Title: "SERVICE", Field: "svc", Width: 20},
		{Title: "SPANS", Field: "spans", Width: 5, Right: true},
		{Title: "FAIL", Field: "failed", Width: 4, Right: true, Class: classNonzeroError},
		{Title: "DURATION", Width: 8, Right: true, Value: func(rec map[string]any) string { return FormatNs(rec["dur"]) },
			Sort: func(rec map[string]any) any { return rec["dur"] }},
	},
	EnterTarget: "waterfall", // bespoke: enter opens the trace waterfall
	Trace:       func(rec map[string]any) string { return Str(rec, "trace.id") },
	Drills:      map[string]string{"l": "trace-logs"},
}

// traceLabel prefers the root span's endpoint; traces whose root fell outside
// the window fall back to the trace id.
func traceLabel(rec map[string]any) string {
	if root := Str(rec, "root"); root != "" {
		return root
	}
	return Str(rec, "trace.id")
}

func classNonzeroError(val string) string {
	if val == "" || val == "0" {
		return "dim"
	}
	return "error"
}

// WaterfallQuery fetches every span of one trace, in start order, with the
// full record intact (the span inspector shows all attributes). toUid() is
// mandatory — a plain string comparison silently returns nothing.
func WaterfallQuery(traceID string, tf Timeframe) string {
	return fmt.Sprintf("fetch spans, from:%s\n| filter trace.id == toUid(%q)\n| sort start_time asc\n| limit 500",
		tf.DQL(), traceID)
}
