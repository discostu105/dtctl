package catalog

import (
	"strings"
	"testing"
)

// exceptionSpan mirrors the live shapes: a OneAgent/Java exception event with
// the Grail stack_trace spelling and file/line, next to a feature_flag event.
func exceptionSpan() map[string]any {
	return map[string]any{
		"span.name":        "GET /v1/orders",
		"span.kind":        "server",
		"span.status_code": nil, // most exception-bearing spans are NOT failed
		"span.events": []any{
			map[string]any{
				"span_event.name":       "exception",
				"exception.type":        "javax.servlet.http.HttpServletResponse.setStatus",
				"exception.message":     "HTTP 404 setStatus called",
				"exception.stack_trace": "a.b.C.one (C.java:1)\na.b.C.two (C.java:2)",
				"exception.file.full":   "C.java",
				"exception.line_number": "243",
			},
			map[string]any{
				"span_event.name":  "feature_flag",
				"feature_flag.key": "recommendationCacheFailure",
			},
		},
	}
}

func TestSpanEventsParsesNamesAndFields(t *testing.T) {
	events := SpanEvents(exceptionSpan())
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Name != "exception" || events[1].Name != "feature_flag" {
		t.Errorf("names = %q, %q", events[0].Name, events[1].Name)
	}
	if SpanEvents(map[string]any{"span.name": "no events"}) != nil {
		t.Error("a span without span.events must yield nil")
	}
}

func TestSpanExceptionsCoalescesBothSpellings(t *testing.T) {
	excs := SpanExceptions(exceptionSpan())
	if len(excs) != 1 {
		t.Fatalf("exceptions = %d, want 1 (the feature_flag event is not one)", len(excs))
	}
	ex := excs[0]
	if ex.Type != "javax.servlet.http.HttpServletResponse.setStatus" ||
		ex.Message != "HTTP 404 setStatus called" {
		t.Errorf("type/message = %q / %q", ex.Type, ex.Message)
	}
	if !strings.Contains(ex.StackTrace, "C.java:1") {
		t.Errorf("stack trace = %q", ex.StackTrace)
	}
	if ex.Location != "C.java:243" {
		t.Errorf("location = %q, want C.java:243", ex.Location)
	}

	// The pure-OTel spelling (exception.stacktrace) coalesces too.
	otel := map[string]any{"span.events": []any{map[string]any{
		"span_event.name": "exception", "exception.type": "*fmt.wrapError",
		"exception.stacktrace": "main.run\n\tmain.go:42",
	}}}
	if got := SpanExceptions(otel); len(got) != 1 || !strings.Contains(got[0].StackTrace, "main.go:42") {
		t.Errorf("otel spelling exceptions = %+v", got)
	}

	if SpanExceptions(nil) != nil {
		t.Error("nil record must yield nil, not panic")
	}
}

// TestSpanExceptionsMemoizes: column Value funcs run per render frame — the
// parse must happen once per record.
func TestSpanExceptionsMemoizes(t *testing.T) {
	rec := exceptionSpan()
	first := SpanExceptions(rec)
	rec["span.events"] = []any{} // would parse to nothing…
	if again := SpanExceptions(rec); len(again) != len(first) {
		t.Error("second call must return the memoized parse")
	}
}

func TestExceptionTypeShort(t *testing.T) {
	cases := map[string]string{
		"javax.servlet.http.HttpServletResponse.setStatus": "HttpServletResponse.setStatus",
		"*fmt.wrapError":  "*fmt.wrapError",
		"CLOSE_Exception": "CLOSE_Exception",
		"":                "",
	}
	for in, want := range cases {
		if got := ExceptionTypeShort(in); got != want {
			t.Errorf("ExceptionTypeShort(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSpanErrored: the OTel status code alone must mark a span errored — the
// request verdict exists only on entry spans (the waterfall's old gap).
func TestSpanErrored(t *testing.T) {
	if !SpanErrored(map[string]any{"span.status_code": "error"}) {
		t.Error("status_code error must count as errored")
	}
	if !SpanErrored(map[string]any{"request.is_failed": true}) {
		t.Error("request verdict must count as errored")
	}
	if SpanErrored(map[string]any{"span.status_code": "ok"}) {
		t.Error("ok span is not errored")
	}
}

// TestSpanErrorBrief: HTTP error status wins, then the gRPC status name, then
// the exception type, then the status message.
func TestSpanErrorBrief(t *testing.T) {
	rec := exceptionSpan()
	rec["http.response.status_code"] = float64(503)
	rec["rpc.grpc.status_code"] = float64(4)
	if got := SpanErrorBrief(rec); got != "HTTP 503" {
		t.Errorf("brief = %q, want HTTP 503", got)
	}
	delete(rec, "http.response.status_code")
	if got := SpanErrorBrief(rec); got != "DEADLINE_EXCEEDED" {
		t.Errorf("brief = %q, want DEADLINE_EXCEEDED", got)
	}
	delete(rec, "rpc.grpc.status_code")
	if got := SpanErrorBrief(rec); got != "HttpServletResponse.setStatus" {
		t.Errorf("brief = %q, want the short exception type", got)
	}
	msgOnly := map[string]any{"span.status_message": "upstream connect error"}
	if got := SpanErrorBrief(msgOnly); got != "upstream connect error" {
		t.Errorf("brief = %q, want the status message", got)
	}
	// A 200 on a failed span is not an error story — fall through.
	ok200 := map[string]any{"http.status_code": float64(200), "span.status_message": "canceled"}
	if got := SpanErrorBrief(ok200); got != "canceled" {
		t.Errorf("brief = %q, want canceled (200 is not an error)", got)
	}
}

// TestSpanPreviewSurfacesExceptions: exceptions render as error-classed facts
// with the message wrapped — regardless of the status verdict.
func TestSpanPreviewSurfacesExceptions(t *testing.T) {
	rec := exceptionSpan()
	rec["span.events"] = append(rec["span.events"].([]any), map[string]any{
		"span_event.name": "exception", "exception.type": "CLOSE_Exception", "exception.message": "-501",
	})
	facts := PreviewFacts(rec)
	f := factByLabel(facts, "exceptions ×2")
	if f.Value != "javax.servlet.http.HttpServletResponse.setStatus" || f.Class != "error" {
		t.Errorf("exceptions fact = %+v", f)
	}
	if m := factByLabel(facts, "thrown"); !m.Wrap || m.Value != "HTTP 404 setStatus called" {
		t.Errorf("thrown fact = %+v", m)
	}
}

// TestSpanPreviewGRPCStatus: the rpc arm names the gRPC verdict.
func TestSpanPreviewGRPCStatus(t *testing.T) {
	facts := PreviewFacts(map[string]any{
		"span.name": "oteldemo.CartService/GetCart", "span.kind": "client",
		"rpc.service": "oteldemo.CartService", "rpc.method": "GetCart",
		"rpc.system": "grpc", "rpc.grpc.status_code": float64(14),
	})
	if f := factByLabel(facts, "grpc"); f.Value != "UNAVAILABLE" || f.Class != "error" {
		t.Errorf("grpc fact = %+v, want UNAVAILABLE/error", f)
	}
	okFacts := PreviewFacts(map[string]any{
		"span.name": "x", "span.kind": "client",
		"rpc.system": "grpc", "rpc.grpc.status_code": float64(0),
	})
	if f := factByLabel(okFacts, "grpc"); f.Value != "OK" || f.Class != "dim" {
		t.Errorf("grpc ok fact = %+v, want OK/dim", f)
	}
}

// TestSpanPreviewHTTPStatusWithoutMethod: a failed span's 503 must not vanish
// because the instrumentation skipped the method attribute.
func TestSpanPreviewHTTPStatusWithoutMethod(t *testing.T) {
	facts := PreviewFacts(map[string]any{
		"span.name": "handler", "span.kind": "server",
		"http.response.status_code": float64(503),
	})
	if f := factByLabel(facts, "http"); f.Value != "503" || f.Class != "error" {
		t.Errorf("http fact = %+v, want 503/error", f)
	}
}
