package catalog

import "strings"

// Span-event / exception helpers. Validated live on two tenants: span.events
// is an array of records discriminated by span_event.name — "exception",
// "bizevent", "feature_flag", "message", or free instrumentation text.
// Exception events carry exception.type / exception.message plus, where the
// instrumentation stamps them, the stack trace (Grail spells it
// exception.stack_trace; pure-OTel SDK attributes use exception.stacktrace —
// coalesce both), exception.file.full and exception.line_number; OneAgent
// cause chains add exception.is_caused_by_root and multiple exception events
// per span (chains of 31 seen live). Crucially, exceptions mostly ride on
// spans that are NOT failed — ~98% of one tenant's exception-bearing spans
// had span.status_code null or "ok" (a caught-and-handled error, a 404
// recorded as an exception) — so exception visibility must never hang off
// the status verdict.

// SpanEvent is one span.events element: its discriminator name and the raw
// event record (name key included — Fields is the original map, not a copy).
type SpanEvent struct {
	Name   string
	Fields map[string]any
}

// SpanException is one exception event with both semconv spellings coalesced.
type SpanException struct {
	Type       string
	Message    string
	StackTrace string
	Location   string // "File.java:243" when the instrumentation stamped it
}

// SpanEvents parses a span record's span.events array; nil when absent.
func SpanEvents(rec map[string]any) []SpanEvent {
	arr, ok := rec["span.events"].([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	events := make([]SpanEvent, 0, len(arr))
	for _, e := range arr {
		fields, ok := e.(map[string]any)
		if !ok {
			continue
		}
		name := firstNonEmpty(Str(fields, "span_event.name"), Str(fields, "name"))
		if name == "" {
			name = "event"
		}
		events = append(events, SpanEvent{Name: name, Fields: fields})
	}
	return events
}

// Exception parses the event's exception fields (false for other event kinds).
func (e SpanEvent) Exception() (SpanException, bool) {
	if e.Name != "exception" {
		return SpanException{}, false
	}
	ex := SpanException{
		Type:    Str(e.Fields, "exception.type"),
		Message: Str(e.Fields, "exception.message"),
		StackTrace: firstNonEmpty(
			Str(e.Fields, "exception.stack_trace"), Str(e.Fields, "exception.stacktrace")),
	}
	if file := Str(e.Fields, "exception.file.full"); file != "" {
		ex.Location = file
		if line := FormatValue(e.Fields["exception.line_number"]); line != "" {
			ex.Location += ":" + line
		}
	}
	return ex, true
}

// spanExcKey memoizes the parsed exceptions — column Value funcs run on
// every render frame.
const spanExcKey = "__span.exceptions"

// SpanExceptions returns the span's exception events, parsed and coalesced.
func SpanExceptions(rec map[string]any) []SpanException {
	if rec == nil {
		return nil
	}
	if cached, ok := rec[spanExcKey].([]SpanException); ok {
		return cached
	}
	var excs []SpanException
	for _, ev := range SpanEvents(rec) {
		if ex, ok := ev.Exception(); ok {
			excs = append(excs, ex)
		}
	}
	rec[spanExcKey] = excs
	return excs
}

// ExceptionTypeShort compresses a fully-qualified exception type for a
// narrow cell: Java-style FQCNs keep their last two dot-segments
// ("javax.servlet.http.HttpServletResponse.setStatus" →
// "HttpServletResponse.setStatus"); already-short types ("*fmt.wrapError",
// "CLOSE_Exception") pass through.
func ExceptionTypeShort(t string) string {
	parts := strings.Split(t, ".")
	if len(parts) <= 2 {
		return t
	}
	return parts[len(parts)-2] + "." + parts[len(parts)-1]
}
