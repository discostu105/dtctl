package catalog

import (
	"fmt"
	"strings"
)

// Distributed-tracing views. Validated live: trace.id is a UID type — string
// literals silently match nothing, toUid() is mandatory. Durations are
// nanoseconds serialized as strings; request.is_failed exists only on
// server/entry spans (null elsewhere), span.status_code is null on the vast
// majority of spans and "error"/"ok" where instrumentation set it.
//
// The view fetches spans directly — no summarize — so every span attribute
// survives into the row (the inspector and the facet picker see the full
// record) and the query skips the aggregation cost. Lenses slice the firehose
// into the subsets people actually look for; "roots" approximates the classic
// trace list (one row per trace: its entry span, isNull(span.parent_id) —
// the OTel root definition; Dynatrace's request.is_root_span is also true on
// spans whose parent fell outside ingest, validated live).

var spanLenses = []Lens{
	{Name: "roots", Desc: "trace root spans (no parent) — one row per trace",
		Filter: "isNull(span.parent_id)"},
	{Name: "errors", Desc: "failed spans of any kind",
		Filter: `span.status_code == "error" or request.is_failed == true`},
	{Name: "server", Desc: "incoming requests handled by a service",
		Filter: `span.kind == "server"`},
	{Name: "client", Desc: "outgoing calls (HTTP, RPC, DB drivers)",
		Filter: `span.kind == "client"`},
	{Name: "db", Desc: "database statements",
		Filter: "isNotNull(db.system.name)", Columns: dbSpanColumns},
	// Both GenAI instrumentation eras: semconv spans carry
	// gen_ai.operation.name, traceloop/LangChain spans llm.request.type
	// (validated live — the demo tenant's agents emit only the latter).
	{Name: "genai", Desc: "LLM / GenAI operations",
		Filter: "isNotNull(gen_ai.operation.name) or isNotNull(llm.request.type)", Columns: genaiSpanColumns},
	{Name: "all", Desc: "every span, unfiltered"},
}

var tracesSpec = &Spec{
	Name:    "traces",
	Aliases: []string{"tr", "trace", "spans"},
	Kind:    KindSignal,
	Desc:    "Spans by lens: trace roots, errors, server/client, db, genai",
	Query: func(s Scope) string {
		var b strings.Builder
		fmt.Fprintf(&b, "fetch spans, from:%s", s.Timeframe.DQL())
		if s.Entity != nil {
			fmt.Fprintf(&b, "\n| filter %s", SpanFilter(*s.Entity))
		}
		if l := lensAt(spanLenses, s.Lens); l.Filter != "" {
			fmt.Fprintf(&b, "\n| filter %s", l.Filter)
		}
		b.WriteString("\n| sort start_time desc\n| limit 200")
		return b.String()
	},
	Lenses: spanLenses,
	Columns: []Column{
		spanStartColumn,
		{Title: "NAME", Value: spanLabel},
		{Title: "KIND", Width: 8, Field: "span.kind"},
		{Title: "SERVICE", Field: "service.name", Width: 20},
		{Title: "STATUS", Width: 6, Value: spanStatus, Class: classSpanStatus},
		spanDurationColumn,
	},
	EnterTarget: "waterfall", // bespoke: enter opens the span's trace waterfall
	Trace:       func(rec map[string]any) string { return Str(rec, "trace.id") },
	Drills:      map[string]string{"l": "trace-logs"},
	// Spans carry dt.smartscape.* scope fields only for some entity types
	// (not HOST/AWS/FRONTEND) — a pin of another type would compose a filter
	// that matches nothing. Gate the pin the same way the 's' drill does.
	Scopable: func(e Entity) bool { return SpanScopable(e.Type) },
}

var spanStartColumn = Column{
	Title: "START", Width: 12,
	Value: func(rec map[string]any) string { return FormatTime(Str(rec, "start_time")) },
	Sort:  func(rec map[string]any) any { return Str(rec, "start_time") },
}

var spanDurationColumn = Column{
	Title: "DURATION", Width: 8, Right: true,
	Value: func(rec map[string]any) string { return FormatNs(rec["duration"]) },
	Sort:  func(rec map[string]any) any { return rec["duration"] },
}

// dbSpanColumns put the statement front and center (db lens).
var dbSpanColumns = []Column{
	spanStartColumn,
	{Title: "STATEMENT", Value: func(rec map[string]any) string {
		if q := Str(rec, "db.query.text"); q != "" {
			return q
		}
		return Str(rec, "span.name")
	}},
	{Title: "SYSTEM", Width: 10, Field: "db.system.name"},
	{Title: "DATABASE", Width: 14, Field: "db.namespace"},
	{Title: "SERVICE", Field: "service.name", Width: 20},
	spanDurationColumn,
}

// genaiSpanColumns are about prompts and tool calls (genai lens): the
// operation, the telling text (tool name + arguments, or the last user
// prompt of a chat), the model, and token usage.
var genaiSpanColumns = []Column{
	spanStartColumn,
	{Title: "OP", Width: 6, Value: func(rec map[string]any) string { return GenAIOpShort(GenAIOp(rec)) },
		Class: classGenAIOp},
	{Title: "PROMPT / TOOL CALL", Value: func(rec map[string]any) string {
		if d := GenAIDetail(rec); d != "" {
			return d
		}
		return spanLabel(rec)
	}},
	{Title: "MODEL", Width: 22, Value: GenAIModel},
	{Title: "TOKENS", Width: 11, Right: true, Value: GenAITokens,
		Sort: func(rec map[string]any) any { return rec["gen_ai.usage.input_tokens"] }},
	{Title: "STATUS", Width: 6, Value: spanStatus, Class: classSpanStatus},
	spanDurationColumn,
}

// classGenAIOp colors the operation badge: chats stand out from tool calls.
func classGenAIOp(val string) string {
	switch val {
	case "chat", "text":
		return "ok"
	case "agent":
		return "warn"
	}
	return ""
}

// spanLabel prefers the detected endpoint over the raw span name.
func spanLabel(rec map[string]any) string {
	if ep := Str(rec, "endpoint.name"); ep != "" {
		return ep
	}
	return Str(rec, "span.name")
}

// spanStatus renders the span's failure state: Dynatrace's request verdict
// where present (entry spans), else the OTel status code.
func spanStatus(rec map[string]any) string {
	if failed, _ := rec["request.is_failed"].(bool); failed {
		return "failed"
	}
	return Str(rec, "span.status_code")
}

func classSpanStatus(val string) string {
	switch val {
	case "failed", "error":
		return "error"
	case "ok":
		return "dim"
	}
	return ""
}

// DefaultSpanLens picks the lens a traces view scoped to an entity should
// open on. GenAI entities land on the genai lens: their chat/tool spans
// nest deep inside agent traces, so the default roots lens
// (isNull(span.parent_id)) is silently empty for them — and the genai
// columns (prompts, tool calls, tokens) are what the drill is for.
func DefaultSpanLens(entityType string) int {
	if strings.HasPrefix(entityType, "GENAI_") {
		for i, l := range spanLenses {
			if l.Name == "genai" {
				return i
			}
		}
	}
	return 0
}

// WaterfallQuery fetches every span of one trace, in start order, with the
// full record intact (the span inspector shows all attributes). toUid() is
// mandatory — a plain string comparison silently returns nothing.
func WaterfallQuery(traceID string, tf Timeframe) string {
	return fmt.Sprintf("fetch spans, from:%s\n| filter trace.id == toUid(%q)\n| sort start_time asc\n| limit 500",
		tf.DQL(), traceID)
}
