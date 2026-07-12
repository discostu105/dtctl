package catalog

import (
	"strings"
	"testing"
)

// factByLabel finds a fact by label ("" when absent).
func factByLabel(facts []PreviewFact, label string) PreviewFact {
	for _, f := range facts {
		if f.Label == label {
			return f
		}
	}
	return PreviewFact{}
}

// TestPreviewFactsDBSpan: a db span previews its statement (wrapped), the
// system·database pair, the failure verdict colored, and a humanized duration
// — not raw nanoseconds.
func TestPreviewFactsDBSpan(t *testing.T) {
	facts := PreviewFacts(map[string]any{
		"span.name": "SELECT orders", "span.kind": "client",
		"db.system.name": "postgres", "db.namespace": "orders",
		"db.query.text":     "SELECT * FROM orders WHERE id = $1",
		"duration":          "4800000",
		"start_time":        "2026-07-06T16:18:52.000000000Z",
		"request.is_failed": true,
		"service.name":      "checkout",
	})
	if got := factByLabel(facts, "duration").Value; got != "4.8ms" {
		t.Errorf("duration = %q, want humanized 4.8ms", got)
	}
	if f := factByLabel(facts, "status"); f.Value != "failed" || f.Class != "error" {
		t.Errorf("status = %+v, want failed/error", f)
	}
	if f := factByLabel(facts, "statement"); !f.Wrap || !strings.Contains(f.Value, "SELECT * FROM") {
		t.Errorf("statement = %+v, want the wrapped query text", f)
	}
	if got := factByLabel(facts, "db").Value; got != "postgres · orders" {
		t.Errorf("db = %q", got)
	}
	// The verdict/latency lead so a cramped bottom panel keeps them.
	if facts[0].Label != "status" || facts[1].Label != "duration" {
		t.Errorf("facts must lead with status, duration — got %+v", facts[:2])
	}
}

// TestPreviewFactsSpanContext: the placement and origin OneAgent stamps on
// nearly every span — remote peer, code location, host, release version,
// exception exit — surface as facts (field names validated live).
func TestPreviewFactsSpanContext(t *testing.T) {
	facts := PreviewFacts(map[string]any{
		"span.name": "SELECT", "span.kind": "client",
		"db.system.name": "postgres",
		"server.address": "easytrade-db", "server.port": "1433",
		"code.namespace": "app.orders", "code.function": "load",
		"host.name":                        "node-3.example.invalid",
		"deployment.release_build_version": "1.2.3", "deployment.release_stage": "prod",
		"span.is_exit_by_exception": true,
	})
	if got := factByLabel(facts, "peer").Value; got != "easytrade-db:1433" {
		t.Errorf("peer = %q", got)
	}
	if got := factByLabel(facts, "code").Value; got != "app.orders.load" {
		t.Errorf("code = %q", got)
	}
	if got := factByLabel(facts, "host").Value; got != "node-3.example.invalid" {
		t.Errorf("host = %q", got)
	}
	if got := factByLabel(facts, "release").Value; got != "1.2.3 · prod" {
		t.Errorf("release = %q", got)
	}
	if f := factByLabel(facts, "exception"); f.Class != "error" {
		t.Errorf("exception fact = %+v, want an error-classed marker", f)
	}

	kafka := PreviewFacts(map[string]any{
		"span.kind": "consumer", "span.name": "process request_queue",
		"messaging.system": "kafka", "messaging.destination.name": "request_queue",
		"messaging.operation.type": "process", "messaging.kafka.offset": "1284",
		"messaging.destination.partition.id": "3", "messaging.consumer.group.name": "billing",
	})
	if got := factByLabel(kafka, "kafka").Value; got != "partition 3 · offset 1284 · group billing" {
		t.Errorf("kafka = %q", got)
	}

	faas := PreviewFacts(map[string]any{
		"span.kind": "server", "span.name": "my-fn",
		"faas.name": "my-fn", "faas.trigger": "http", "faas.coldstart": true,
	})
	if got := factByLabel(faas, "trigger").Value; got != "http · cold start" {
		t.Errorf("faas trigger = %q", got)
	}
}

// TestPreviewFactsGenAISpan: a tool-call span surfaces op, model, tokens and
// the tool invocation.
func TestPreviewFactsGenAISpan(t *testing.T) {
	facts := PreviewFacts(map[string]any{
		"span.name": "execute_tool search", "span.kind": "internal",
		"gen_ai.operation.name":      "execute_tool",
		"gen_ai.request.model":       "claude-sonnet-5",
		"gen_ai.usage.input_tokens":  "1200",
		"gen_ai.usage.output_tokens": float64(80),
		"gen_ai.tool.name":           "search",
		"gen_ai.tool.call.arguments": `{"q":"latency"}`,
		"duration":                   "125000000",
	})
	if got := factByLabel(facts, "op").Value; got != "tool" {
		t.Errorf("op = %q", got)
	}
	if got := factByLabel(facts, "model").Value; got != "claude-sonnet-5" {
		t.Errorf("model = %q", got)
	}
	if got := factByLabel(facts, "tokens").Value; got != "1.2k→80" {
		t.Errorf("tokens = %q", got)
	}
	if f := factByLabel(facts, "tool"); !f.Wrap || !strings.Contains(f.Value, "search") {
		t.Errorf("tool = %+v, want the wrapped tool call", f)
	}
}

// TestPreviewFactsHTTPSpan: a plain client span with http attributes shows
// the method+status verdict (colored by status class) and the URL.
func TestPreviewFactsHTTPSpan(t *testing.T) {
	facts := PreviewFacts(map[string]any{
		"span.kind": "client", "span.name": "GET",
		"http.request.method": "GET", "http.response.status_code": "503",
		"url.full": "https://api.example.invalid/orders",
	})
	if f := factByLabel(facts, "http"); f.Value != "GET 503" || f.Class != "error" {
		t.Errorf("http = %+v, want GET 503 colored error", f)
	}
	if got := factByLabel(facts, "url").Value; got != "https://api.example.invalid/orders" {
		t.Errorf("url = %q", got)
	}
}

// TestPreviewFactsSession: sessions summarize duration, activity counts,
// error count (colored), client and end reason — with bounce folded in.
func TestPreviewFactsSession(t *testing.T) {
	facts := PreviewFacts(map[string]any{
		"start_time":         "2026-07-06T16:18:52.000000000Z",
		"duration":           "185000000000", // 3m05s
		"view_summary_count": "3", "user_action_count": "12", "request_count": float64(87),
		"error.count":  "2",
		"browser.name": "Chrome", "browser.version": "126", "os.name": "Windows",
		"geo.country.iso_code":      "AT",
		"end_reason":                "timeout",
		"characteristics.is_bounce": true,
	})
	if got := factByLabel(facts, "duration").Value; got != "3m05s" {
		t.Errorf("duration = %q", got)
	}
	if got := factByLabel(facts, "activity").Value; got != "3 views · 12 actions · 87 requests" {
		t.Errorf("activity = %q", got)
	}
	if f := factByLabel(facts, "errors"); f.Value != "2" || f.Class != "error" {
		t.Errorf("errors = %+v", f)
	}
	if got := factByLabel(facts, "client").Value; got != "Chrome 126 · Windows · AT" {
		t.Errorf("client = %q", got)
	}
	if got := factByLabel(facts, "end").Value; got != "timeout · bounced" {
		t.Errorf("end = %q", got)
	}
	if got := PreviewTitle(map[string]any{"end_reason": "timeout", "frontend.name": []any{"www.example.invalid"}}); got != "www.example.invalid session" {
		t.Errorf("session title = %q", got)
	}
}

// TestPreviewFactsRUMEvents: the classifier decides the substance — a
// request's verdict, a view's web vitals (LCP colored by CWV thresholds),
// an error's origin.
func TestPreviewFactsRUMEvents(t *testing.T) {
	request := PreviewFacts(map[string]any{
		"characteristics.classifier": "request",
		"http.request.method":        "POST", "http.response.status_code": "500",
		"url.path": "/api/orders", "url.domain": "www.example.invalid",
		"duration": "230000000",
	})
	if f := factByLabel(request, "response"); f.Value != "POST 500" || f.Class != "error" {
		t.Errorf("request response = %+v", f)
	}
	if got := factByLabel(request, "duration").Value; got != "230.0ms" {
		t.Errorf("request duration = %q", got)
	}

	view := PreviewFacts(map[string]any{
		"characteristics.classifier":           "view_summary",
		"view.name":                            "/checkout",
		"web_vitals.largest_contentful_paint":  "3000000000", // 3000ms — needs-improvement
		"web_vitals.interaction_to_next_paint": "80000000",
		"web_vitals.cumulative_layout_shift":   0.021,
		"error.exception_count":                "1",
	})
	if f := factByLabel(view, "LCP"); f.Value != "3000ms" || f.Class != "warn" {
		t.Errorf("view LCP = %+v", f)
	}
	if got := factByLabel(view, "vitals").Value; got != "INP 80ms · CLS 0.021" {
		t.Errorf("view vitals = %q", got)
	}
	if f := factByLabel(view, "errors"); f.Value != "1" || f.Class != "error" {
		t.Errorf("view errors = %+v", f)
	}
	// A view summary titles as its view, not its url.path — and the view fact
	// dropping keeps the pane free of the duplicate line.
	if got := PreviewTitle(map[string]any{"characteristics.classifier": "view_summary",
		"view.name": "/checkout", "url.path": "/"}); got != "/checkout" {
		t.Errorf("view title = %q", got)
	}
	if f := factByLabel(view, "view"); f.Value != "" {
		t.Errorf("view fact should fold into the title, got %+v", f)
	}

	jsError := PreviewFacts(map[string]any{
		"characteristics.classifier": "error",
		"error.display_name":         "TypeError: x is undefined",
		"error.source":               "javascript", "error.type": "exception",
		"view.name": "/cart",
	})
	if f := factByLabel(jsError, "kind"); f.Class != "error" {
		t.Errorf("error kind fact = %+v, want colored", f)
	}
	if f := factByLabel(jsError, "error"); !f.Wrap || f.Value != "TypeError: x is undefined" {
		t.Errorf("error fact = %+v", f)
	}
	if got := factByLabel(jsError, "source").Value; got != "javascript · exception" {
		t.Errorf("error source = %q", got)
	}
}

// TestPreviewFactsProblem: problems lead with the colored status, SEV badge
// and lifespan; affected entities summarize with a +n tail.
func TestPreviewFactsProblem(t *testing.T) {
	facts := PreviewFacts(map[string]any{
		"event.kind": "DAVIS_PROBLEM", "display_id": "P-25071",
		"event.status": "ACTIVE", "event.severity": "2",
		"event.category":        "AVAILABILITY",
		"event.start":           "2026-07-06T16:18:52.000000000Z",
		"affected_entity_names": []any{"checkout", "payments", "cart"},
		"event.description":     "Response time degradation on checkout.",
	})
	if f := factByLabel(facts, "status"); f.Value != "ACTIVE" || f.Class != "error" {
		t.Errorf("status = %+v", f)
	}
	if f := factByLabel(facts, "severity"); f.Value != "SEV2" || f.Class != "error" {
		t.Errorf("severity = %+v", f)
	}
	if f := factByLabel(facts, "active"); !strings.Contains(f.Value, "since") {
		t.Errorf("active = %+v, want an age with its start time", f)
	}
	if got := factByLabel(facts, "affected").Value; got != "checkout, payments +1" {
		t.Errorf("affected = %q", got)
	}
	// A closed problem reports its lifespan instead of an age.
	closed := PreviewFacts(map[string]any{
		"event.kind": "DAVIS_PROBLEM", "event.status": "CLOSED",
		"event.start": "2026-07-06T16:00:00.000000000Z",
		"event.end":   "2026-07-06T17:44:00.000000000Z",
	})
	if f := factByLabel(closed, "lasted"); !strings.Contains(f.Value, "1h") {
		t.Errorf("lasted = %+v", f)
	}
	if got := PreviewTitle(map[string]any{"event.kind": "DAVIS_PROBLEM", "display_id": "P-25071", "event.name": "CPU saturation"}); got != "P-25071 CPU saturation" {
		t.Errorf("problem title = %q", got)
	}
}

// TestPreviewFactsLog: level colored, content wrapped, the K8s placement
// (namespace/pod + container), stderr marker, and origin facts surfaced.
func TestPreviewFactsLog(t *testing.T) {
	facts := PreviewFacts(map[string]any{
		"content": "connection refused to db", "loglevel": "ERROR",
		"timestamp":          "2026-07-06T16:18:52.000000000Z",
		"k8s.namespace.name": "shop", "k8s.pod.name": "checkout-7d9f",
		"k8s.container.name": "checkout", "host.name": "node-3.example.invalid",
		"log.iostream": "stderr", "log.source": "/var/log/app.log",
		"process.technology": "go",
	})
	if f := factByLabel(facts, "level"); f.Value != "ERROR" || f.Class != "error" {
		t.Errorf("level = %+v", f)
	}
	if f := factByLabel(facts, "stream"); f.Value != "stderr" || f.Class != "warn" {
		t.Errorf("stream = %+v", f)
	}
	if f := factByLabel(facts, "content"); !f.Wrap {
		t.Errorf("content = %+v, want wrapped", f)
	}
	if got := factByLabel(facts, "pod").Value; got != "shop/checkout-7d9f" {
		t.Errorf("pod = %q", got)
	}
	if got := factByLabel(facts, "container").Value; got != "checkout" {
		t.Errorf("container = %q", got)
	}
	if got := factByLabel(facts, "host").Value; got != "node-3.example.invalid" {
		t.Errorf("host = %q", got)
	}
	if got := factByLabel(facts, "source").Value; got != "/var/log/app.log" {
		t.Errorf("source = %q", got)
	}
	if got := factByLabel(facts, "tech").Value; got != "go" {
		t.Errorf("tech = %q", got)
	}
}

// TestPreviewFactsVuln: the vulns view's summarize aliases render as one risk
// line colored by level.
func TestPreviewFactsVuln(t *testing.T) {
	facts := PreviewFacts(map[string]any{
		"vulnerability.id": "V-1", "title": "RCE in libfoo",
		"level": "CRITICAL", "score": "9.8", "status": "OPEN",
		"affected": "12", "tech": "GO", "cve": []any{"CVE-2026-0001"},
	})
	if f := factByLabel(facts, "risk"); f.Value != "CRITICAL · 9.8" || f.Class != "error" {
		t.Errorf("risk = %+v", f)
	}
	if got := factByLabel(facts, "cve").Value; got != "CVE-2026-0001" {
		t.Errorf("cve = %q", got)
	}
}

// TestPreviewFactsGenericFallback: unknown records get no curated facts —
// the caller keeps the priority-field rendering; PreviewValue humanizes its
// known time/duration fields there.
func TestPreviewFactsGenericFallback(t *testing.T) {
	if facts := PreviewFacts(map[string]any{"name": "some row"}); facts != nil {
		t.Errorf("unknown record should fall back, got %+v", facts)
	}
	if got := PreviewValue("duration", "4800000"); got != "4.8ms" {
		t.Errorf("PreviewValue duration = %q", got)
	}
	if got := PreviewValue("timestamp", "not-a-time"); got != "not-a-time" {
		t.Errorf("PreviewValue must pass through unparseable times, got %q", got)
	}
}
