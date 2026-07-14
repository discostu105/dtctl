package catalog

import (
	"fmt"
	"strconv"
	"strings"
)

// Distributed-tracing views. Validated live: trace.id is a UID type — string
// literals silently match nothing, toUid() is mandatory. Durations are
// nanoseconds serialized as strings; request.is_failed exists only on
// server/entry spans (null elsewhere), span.status_code is null on the vast
// majority of spans and "error"/"ok" where instrumentation set it.
//
// Span attributes arrive in two semantic-convention eras, and a tenant holds
// either or both: OneAgent emits the Dynatrace semantic dictionary's mix
// (old db.system next to new db.query.text/db.namespace), pure-OTLP ingest
// emits stable OTel semconv (db.system.name), and the populations are
// disjoint — validated live on two tenants, one of which had zero
// db.system.name spans. Every category filter and display column therefore
// coalesces both names. Renames that matter here: db.system →
// db.system.name (semconv 1.30, stable 1.33), db.statement → db.query.text,
// db.name → db.namespace, db.operation → db.operation.name (1.26),
// http.method → http.request.method, messaging.operation →
// messaging.operation.type, and request.is_failed → transaction.is_failed
// (Dynatrace dictionary deprecation; both dup-emitted today).
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
	// errors and exceptions deliberately coexist: errors selects spans that
	// FAILED (status verdict), exceptions spans that THREW — ~98% of
	// exception-bearing spans are not failed (caught/handled), and the
	// populations barely overlap (validated live).
	{Name: "errors", Desc: "failed spans of any kind, with the minimal why",
		Filter:  `span.status_code == "error" or request.is_failed == true or transaction.is_failed == true`,
		Columns: errorSpanColumns},
	// Exceptions hide from every status filter: ~98% of exception-bearing
	// spans have span.status_code null or "ok" (validated live). Iterative
	// expressions (span.events[][span_event.name]) are rejected inside filter
	// (ITERATIVE_EXPRESSION_FOR_FILTER), so this string-matches the exact
	// serialized discriminator — validated against toString(span.events).
	{Name: "exceptions", Desc: "spans that recorded exception events (mostly non-failed spans)",
		Filter: `contains(toString(span.events), "\"span_event.name\":\"exception\"")`, Columns: exceptionSpanColumns},
	{Name: "server", Desc: "incoming requests handled by a service",
		Filter: `span.kind == "server"`},
	{Name: "client", Desc: "outgoing calls (HTTP, RPC, DB drivers)",
		Filter: `span.kind == "client"`},
	{Name: "db", Desc: "database statements",
		Filter: "isNotNull(db.system.name) or isNotNull(db.system)", Columns: dbSpanColumns},
	{Name: "rpc", Desc: "remote procedure calls (gRPC, SOAP, cloud APIs)",
		Filter: "isNotNull(rpc.system)", Columns: rpcSpanColumns},
	{Name: "messaging", Desc: "queue/topic publishes and consumes",
		Filter: "isNotNull(messaging.system)", Columns: messagingSpanColumns},
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
		if f := ScopeSpanFilter(s); f != "" {
			fmt.Fprintf(&b, "\n| filter %s", f)
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
		spanServiceColumn,
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

var spanServiceColumn = Column{Title: "SERVICE", Width: 20, Value: SpanService}

// SpanService names the service a span belongs to. Two attributes carry it
// and neither is universal: Dynatrace stamps its resolved dt.service.name on
// OneAgent/extension spans (validated live — 12% of one tenant's spans had
// ONLY that, e.g. extension and background-thread spans), while pure-OTLP
// ingest carries only the resource attribute service.name. Where both exist
// the values agree, so the coalesce order is cosmetic.
func SpanService(rec map[string]any) string {
	return firstNonEmpty(Str(rec, "dt.service.name"), Str(rec, "service.name"))
}

// dbSpanColumns put the statement front and center (db lens). Every db.*
// display field coalesces its two semconv-era names.
var dbSpanColumns = []Column{
	spanStartColumn,
	{Title: "STATEMENT", Value: func(rec map[string]any) string {
		if q := firstNonEmpty(Str(rec, "db.query.text"), Str(rec, "db.statement")); q != "" {
			return q
		}
		return Str(rec, "span.name")
	}},
	{Title: "SYSTEM", Width: 10, Value: func(rec map[string]any) string {
		return firstNonEmpty(Str(rec, "db.system.name"), Str(rec, "db.system"))
	}},
	{Title: "DATABASE", Width: 14, Value: func(rec map[string]any) string {
		return firstNonEmpty(Str(rec, "db.namespace"), Str(rec, "db.name"))
	}},
	spanServiceColumn,
	spanDurationColumn,
}

// errorSpanColumns (errors lens): the failure verdict plus the minimal "why"
// — the HTTP status, gRPC status name, exception type, or status message —
// on the row itself instead of a drill-down away.
var errorSpanColumns = []Column{
	spanStartColumn,
	{Title: "NAME", Value: spanLabel},
	{Title: "ERROR", Width: 22, Value: SpanErrorBrief, Class: func(string) string { return "error" }},
	{Title: "STATUS", Width: 6, Value: spanStatus, Class: classSpanStatus},
	spanServiceColumn,
	spanDurationColumn,
}

// exceptionSpanColumns lead with what was thrown (exceptions lens): the first
// exception event's type and message. STATUS stays — watching "ok" and null
// spans throw is the lens's whole point.
var exceptionSpanColumns = []Column{
	spanStartColumn,
	{Title: "EXCEPTION", Width: 28, Value: func(rec map[string]any) string {
		excs := SpanExceptions(rec)
		if len(excs) == 0 {
			return ""
		}
		t := ExceptionTypeShort(firstNonEmpty(excs[0].Type, "exception"))
		if len(excs) > 1 {
			t += fmt.Sprintf(" +%d", len(excs)-1)
		}
		return t
	}, Class: func(string) string { return "error" }},
	{Title: "MESSAGE", Value: func(rec map[string]any) string {
		if excs := SpanExceptions(rec); len(excs) > 0 && excs[0].Message != "" {
			return excs[0].Message
		}
		return spanLabel(rec)
	}},
	{Title: "STATUS", Width: 6, Value: spanStatus, Class: classSpanStatus},
	spanServiceColumn,
	spanDurationColumn,
}

// rpcSpanColumns read like stack frames (rpc lens): the remote
// service.method being called, and which RPC flavor carried it. OneAgent
// emits meaningful rpc.service/rpc.method even where rpc.system leaks
// numeric enum values ("1", "2") — validated live.
var rpcSpanColumns = []Column{
	spanStartColumn,
	{Title: "CALL", Value: func(rec map[string]any) string {
		if call := joinNonEmpty(".", Str(rec, "rpc.service"), Str(rec, "rpc.method")); call != "" {
			return call
		}
		return spanLabel(rec)
	}},
	{Title: "SYSTEM", Width: 11, Field: "rpc.system"},
	{Title: "KIND", Width: 8, Field: "span.kind"},
	spanServiceColumn,
	spanDurationColumn,
}

// messagingSpanColumns name the broker interaction (messaging lens): which
// queue/topic, what was done to it (publish/receive/process), which broker.
var messagingSpanColumns = []Column{
	spanStartColumn,
	{Title: "DESTINATION", Value: func(rec map[string]any) string {
		if d := Str(rec, "messaging.destination.name"); d != "" {
			return d
		}
		return spanLabel(rec)
	}},
	{Title: "OP", Width: 8, Value: func(rec map[string]any) string {
		return firstNonEmpty(Str(rec, "messaging.operation.type"), Str(rec, "messaging.operation"))
	}},
	{Title: "SYSTEM", Width: 10, Field: "messaging.system"},
	spanServiceColumn,
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

// SpanFailed reports Dynatrace's request verdict for a span:
// transaction.is_failed or its deprecated alias request.is_failed (dictionary
// rename; tenants dup-emit both today but either may stand alone).
func SpanFailed(rec map[string]any) bool {
	if failed, _ := rec["transaction.is_failed"].(bool); failed {
		return true
	}
	failed, _ := rec["request.is_failed"].(bool)
	return failed
}

// SpanCategory classifies a span by its semconv discriminator attribute so
// views can badge it — "db" or "messaging", "" otherwise. Precedence: db
// wins over messaging (a span carrying both is a driver-level DB call).
// GenAI has its own richer treatment (GenAIOp) and RPC/HTTP spans keep
// span.kind — for those the call direction is the story, for db and
// messaging the fact that a database/broker is involved is.
func SpanCategory(rec map[string]any) string {
	if firstNonEmpty(Str(rec, "db.system.name"), Str(rec, "db.system")) != "" {
		return "db"
	}
	if Str(rec, "messaging.system") != "" {
		return "messaging"
	}
	return ""
}

// SpanErrored reports whether a span failed by ANY verdict: the Dynatrace
// request verdict (entry spans only) or the OTel status code. SpanFailed
// alone misses every non-entry error span — a span with
// span.status_code == "error" and no request verdict.
func SpanErrored(rec map[string]any) bool {
	return SpanFailed(rec) || Str(rec, "span.status_code") == "error"
}

// SpanErrorBrief is a failed/throwing span's minimal error info for
// badge-sized slots (waterfall rows): the HTTP error status, the gRPC status
// name, the exception type, or the status message — first present wins, ""
// when none is stamped (~2% of failed spans live).
func SpanErrorBrief(rec map[string]any) string {
	status := firstNonEmpty(FormatValue(rec["http.response.status_code"]), FormatValue(rec["http.status_code"]))
	if n, err := strconv.Atoi(status); err == nil && n >= 400 {
		return "HTTP " + status
	}
	if name := GRPCStatusName(FormatValue(rec["rpc.grpc.status_code"])); name != "" && name != "OK" {
		return name
	}
	if excs := SpanExceptions(rec); len(excs) > 0 {
		return ExceptionTypeShort(firstNonEmpty(excs[0].Type, "exception"))
	}
	return compactText(Str(rec, "span.status_message"), 40)
}

// grpcStatusNames maps numeric gRPC status codes to their canonical names —
// "DEADLINE_EXCEEDED" places blame, "4" doesn't (demo tenant emits the
// numeric rpc.grpc.status_code, validated live).
var grpcStatusNames = map[string]string{
	"0": "OK", "1": "CANCELLED", "2": "UNKNOWN", "3": "INVALID_ARGUMENT",
	"4": "DEADLINE_EXCEEDED", "5": "NOT_FOUND", "6": "ALREADY_EXISTS",
	"7": "PERMISSION_DENIED", "8": "RESOURCE_EXHAUSTED", "9": "FAILED_PRECONDITION",
	"10": "ABORTED", "11": "OUT_OF_RANGE", "12": "UNIMPLEMENTED", "13": "INTERNAL",
	"14": "UNAVAILABLE", "15": "DATA_LOSS", "16": "UNAUTHENTICATED",
}

// GRPCStatusName names a numeric gRPC status code ("" when absent; unknown
// codes pass through as-is).
func GRPCStatusName(code string) string {
	if name, ok := grpcStatusNames[code]; ok {
		return name
	}
	return code
}

// spanStatus renders the span's failure state: Dynatrace's request verdict
// where present (entry spans), else the OTel status code.
func spanStatus(rec map[string]any) string {
	if SpanFailed(rec) {
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
// open on. GenAI entities land on the genai lens: the genai columns
// (prompts, tool calls, tokens) are what the drill is for. Everything else
// lands on "all", NOT the unscoped default "roots": root spans belong only
// to the trace's entry service, so roots ANDed with an entity scope is
// silently empty for most entities (validated live — 11 of the top-15
// services on one tenant had zero root spans; "server" is no safer, busy
// internal-only services carry neither).
func DefaultSpanLens(entityType string) int {
	name := "all"
	if strings.HasPrefix(entityType, "GENAI_") {
		name = "genai"
	}
	for i, l := range spanLenses {
		if l.Name == name {
			return i
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
