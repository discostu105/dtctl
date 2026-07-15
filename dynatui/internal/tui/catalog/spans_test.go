package catalog

import (
	"strings"
	"testing"
	"time"
)

func TestTracesQueryComposition(t *testing.T) {
	spec := Lookup("traces")

	// Direct span fetch — no summarize: every attribute survives into the
	// rows (inspector, facet suggestions), and the default lens approximates
	// the classic trace list via the root-span heuristic.
	unscoped := spec.Query(fixtureScope(nil))
	want := "fetch spans, from:now() - 2h\n" +
		"| filter isNull(span.parent_id)\n" +
		"| sort start_time desc\n" +
		"| limit 200"
	if unscoped != want {
		t.Errorf("traces query =\n%s\nwant\n%s", unscoped, want)
	}

	scoped := spec.Query(fixtureScope(&Entity{ID: "K8S_POD-42", Name: "checkout-1", Type: "K8S_POD"}))
	if !strings.Contains(scoped, `dt.smartscape.k8s_pod == toSmartscapeId("K8S_POD-42")`) {
		t.Errorf("pod scope not composed into spans:\n%s", scoped)
	}
	if !strings.Contains(scoped, "| filter isNull(span.parent_id)") {
		t.Errorf("entity scope must not displace the lens filter:\n%s", scoped)
	}
}
func TestTracesLenses(t *testing.T) {
	spec := Lookup("traces")
	if len(spec.Lenses) == 0 {
		t.Fatal("traces spec should offer lenses")
	}
	if spec.Lenses[0].Name != "roots" {
		t.Errorf("default lens should be roots, got %q", spec.Lenses[0].Name)
	}

	// Each lens composes its filter (the last, "all", none at all). Category
	// lenses OR both semconv eras of their discriminator — a tenant holds
	// either (validated live: OneAgent emits db.system, OTLP db.system.name).
	filters := map[string]string{
		"roots":     "| filter isNull(span.parent_id)",
		"errors":    `| filter span.status_code == "error" or request.is_failed == true or transaction.is_failed == true`,
		"server":    `| filter span.kind == "server"`,
		"client":    `| filter span.kind == "client"`,
		"db":        "| filter isNotNull(db.system.name) or isNotNull(db.system)",
		"rpc":       "| filter isNotNull(rpc.system)",
		"messaging": "| filter isNotNull(messaging.system)",
		"genai":     "| filter isNotNull(gen_ai.operation.name)",
	}
	for i, l := range spec.Lenses {
		s := fixtureScope(nil)
		s.Lens = i
		q := spec.Query(s)
		if l.Name == "all" {
			if strings.Contains(q, "| filter") {
				t.Errorf("lens all should not filter:\n%s", q)
			}
			continue
		}
		if !strings.Contains(q, filters[l.Name]) {
			t.Errorf("lens %s query missing %q:\n%s", l.Name, filters[l.Name], q)
		}
	}

	// A stale history index falls back to the default lens, not a panic.
	s := fixtureScope(nil)
	s.Lens = 99
	if q := spec.Query(s); !strings.Contains(q, "isNull(span.parent_id)") {
		t.Errorf("out-of-range lens should clamp to roots:\n%s", q)
	}
	if got := spec.LensAt(-1).Name; got != "roots" {
		t.Errorf("LensAt(-1) = %q, want roots", got)
	}

	// Curated lens columns exist where the default table would be mute.
	for _, name := range []string{"db", "rpc", "messaging", "genai"} {
		for _, l := range spec.Lenses {
			if l.Name == name && l.Columns == nil {
				t.Errorf("lens %s should curate its own columns", name)
			}
		}
	}
}

// TestSpanDualConventions pins the two semconv eras onto the display
// helpers: a OneAgent-era record (db.system, db.statement, db.name,
// request.is_failed) and a stable-semconv record (db.system.name,
// db.query.text, db.namespace, transaction.is_failed) must render the same.
func TestSpanDualConventions(t *testing.T) {
	oneagent := map[string]any{
		"db.system": "postgresql", "db.statement": "SELECT 1", "db.name": "otel",
		"request.is_failed": true,
	}
	otlp := map[string]any{
		"db.system.name": "postgresql", "db.query.text": "SELECT 1", "db.namespace": "otel",
		"transaction.is_failed": true,
	}
	for _, rec := range []map[string]any{oneagent, otlp} {
		if got := dbSpanColumns[1].Text(rec); got != "SELECT 1" {
			t.Errorf("STATEMENT = %q, want SELECT 1 (rec %v)", got, rec)
		}
		if got := dbSpanColumns[2].Text(rec); got != "postgresql" {
			t.Errorf("SYSTEM = %q, want postgresql (rec %v)", got, rec)
		}
		if got := dbSpanColumns[3].Text(rec); got != "otel" {
			t.Errorf("DATABASE = %q, want otel (rec %v)", got, rec)
		}
		if !SpanFailed(rec) {
			t.Errorf("SpanFailed = false for %v", rec)
		}
		if got := SpanCategory(rec); got != "db" {
			t.Errorf("SpanCategory = %q, want db (rec %v)", got, rec)
		}
	}

	// Precedence: a DynamoDB call carries db.system AND rpc.system=aws_api
	// (validated live) — the db category wins; plain broker spans classify
	// as messaging; unadorned spans stay uncategorized.
	dynamo := map[string]any{"db.system": "dynamodb", "rpc.system": "aws_api"}
	if got := SpanCategory(dynamo); got != "db" {
		t.Errorf("SpanCategory(dynamodb) = %q, want db", got)
	}
	kafka := map[string]any{"messaging.system": "kafka", "messaging.operation.type": "process"}
	if got := SpanCategory(kafka); got != "messaging" {
		t.Errorf("SpanCategory(kafka) = %q, want messaging", got)
	}
	if got := SpanCategory(map[string]any{"span.kind": "server"}); got != "" {
		t.Errorf("SpanCategory(plain) = %q, want empty", got)
	}

	// Messaging columns coalesce operation eras too.
	if got := messagingSpanColumns[2].Text(kafka); got != "process" {
		t.Errorf("messaging OP = %q, want process", got)
	}
	old := map[string]any{"messaging.system": "kafka", "messaging.operation": "receive"}
	if got := messagingSpanColumns[2].Text(old); got != "receive" {
		t.Errorf("messaging OP (old era) = %q, want receive", got)
	}

	// RPC columns compose service.method with a span-name fallback.
	rpc := map[string]any{"rpc.service": "OrderController", "rpc.method": "getLatestStatus"}
	if got := rpcSpanColumns[1].Text(rpc); got != "OrderController.getLatestStatus" {
		t.Errorf("rpc CALL = %q", got)
	}
	if got := rpcSpanColumns[1].Text(map[string]any{"span.name": "POST /x"}); got != "POST /x" {
		t.Errorf("rpc CALL fallback = %q, want POST /x", got)
	}

	// The service name has two carriers too: extension/background spans have
	// only dt.service.name, pure-OTLP spans only service.name.
	for _, rec := range []map[string]any{
		{"dt.service.name": "checkout"},
		{"service.name": "checkout"},
		{"dt.service.name": "checkout", "service.name": "checkout"},
	} {
		if got := SpanService(rec); got != "checkout" {
			t.Errorf("SpanService = %q, want checkout (rec %v)", got, rec)
		}
		if got := spanServiceColumn.Text(rec); got != "checkout" {
			t.Errorf("SERVICE column = %q, want checkout (rec %v)", got, rec)
		}
	}
}
func TestWaterfallQueryRequiresToUid(t *testing.T) {
	q := WaterfallQuery("140ea4cf0d16aa99aadde231773bd127", Timeframe{Label: "2h", Dur: 2 * time.Hour})
	if !strings.Contains(q, `filter trace.id == toUid("140ea4cf0d16aa99aadde231773bd127")`) {
		t.Errorf("waterfall query must cast via toUid:\n%s", q)
	}
	if !strings.Contains(q, "| sort start_time asc") {
		t.Errorf("waterfall spans must be start-ordered:\n%s", q)
	}
}
func TestSpanScopable(t *testing.T) {
	for typ, want := range map[string]bool{
		"SERVICE": true, "K8S_POD": true, "K8S_NAMESPACE": true, "CONTAINER": true,
		"HOST": false, "AWS_EC2_INSTANCE": false, "FRONTEND": false,
	} {
		if got := SpanScopable(typ); got != want {
			t.Errorf("SpanScopable(%s) = %v, want %v", typ, got, want)
		}
	}
}
