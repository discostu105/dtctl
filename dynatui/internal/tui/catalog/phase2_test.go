package catalog

import (
	"strings"
	"testing"
	"time"
)

// The Phase 2/3 query templates are contracts with Grail — these tests pin
// the scope composition (namespace → pods, entity → spans, trace → logs)
// that live-tenant exploration validated.

func TestLookupResolvesPhase2Aliases(t *testing.T) {
	cases := map[string]string{
		"po":    "pods",
		"pods":  "pods",
		"wl":    "workloads",
		"ns":    "namespaces",
		"no":    "nodes",
		"cl":    "clusters",
		"tr":    "traces",
		"spans": "traces",
		"cloud": "aws",
		"topo":  "entities",
		"fe":    "frontends",
		"db":    "databases",
		"ai":    "genai",
		"vulns": "vulnerabilities",
		"res":   "resources",
	}
	for input, want := range cases {
		spec := Lookup(input)
		if spec == nil || spec.Name != want {
			t.Errorf("Lookup(%q) = %v, want %q", input, spec, want)
		}
	}
}

func TestPodsQueryComposition(t *testing.T) {
	spec := Lookup("pods")

	unscoped := spec.Query(fixtureScope(nil))
	for _, want := range []string{
		`smartscapeNodes "K8S_POD", from:now() - 2h`,
		`parse k8s.object, "JSON:obj"`,
		"expand cs = obj[status][containerStatuses]",
		"restarts = sum(toLong(cs[restartCount]))",
		"| sort phase asc, namespace asc, name asc",
	} {
		if !strings.Contains(unscoped, want) {
			t.Errorf("pods query missing %q:\n%s", want, unscoped)
		}
	}

	byNS := spec.Query(fixtureScope(&Entity{ID: "K8S_NAMESPACE-1", Name: "shop", Type: "K8S_NAMESPACE"}))
	if !strings.Contains(byNS, `| filter k8s.namespace.name == "shop"`) {
		t.Errorf("namespace scope not composed:\n%s", byNS)
	}

	byNode := spec.Query(fixtureScope(&Entity{ID: "K8S_NODE-1", Name: "node-1", Type: "K8S_NODE"}))
	if !strings.Contains(byNode, `| filter k8s.node.name == "node-1"`) {
		t.Errorf("node scope not composed:\n%s", byNode)
	}

	byWL := spec.Query(fixtureScope(&Entity{ID: "K8S_STATEFULSET-1", Name: "kafka", Type: "K8S_STATEFULSET"}))
	if !strings.Contains(byWL, `k8s.workload.kind == "statefulset" and k8s.workload.name == "kafka"`) {
		t.Errorf("workload scope not composed:\n%s", byWL)
	}

	// A service scope has no pod-side field to filter by; it joins through
	// the service's runs_on edges instead (validated live on two tenants).
	bySvc := spec.Query(fixtureScope(&Entity{ID: "SERVICE-1", Name: "checkout", Type: "SERVICE"}))
	for _, want := range []string{
		`| join [smartscapeEdges "*" | filter source_id == toSmartscapeId("SERVICE-1") and type == "runs_on"`,
		"on:{left[id] == right[target_id]}, kind:inner",
		"| fieldsRemove right.target_id",
	} {
		if !strings.Contains(bySvc, want) {
			t.Errorf("service scope missing %q:\n%s", want, bySvc)
		}
	}
}

func TestWorkloadsQueryCoalescesDaemonSetStatus(t *testing.T) {
	q := Lookup("workloads").Query(fixtureScope(nil))
	for _, want := range []string{
		`smartscapeNodes "K8S_DEPLOYMENT", "K8S_STATEFULSET", "K8S_DAEMONSET"`,
		"coalesce(obj[status][readyReplicas], obj[status][numberReady])",
		"coalesce(obj[spec][replicas], obj[status][desiredNumberScheduled])",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("workloads query missing %q:\n%s", want, q)
		}
	}
}

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

func TestLogsQueryScopesByTraceAndK8sNames(t *testing.T) {
	spec := Lookup("logs")

	byTrace := spec.Query(Scope{Timeframe: Timeframe{Label: "2h"}, TraceID: "abc123"})
	if !strings.Contains(byTrace, `| filter trace_id == "abc123"`) {
		t.Errorf("trace scope not composed (plain string, no toUid on logs):\n%s", byTrace)
	}

	// Log records carry k8s.* names but no dt.smartscape.k8s_* fields — the
	// filter must match by name too, or pod-scoped logs are silently empty.
	byPod := spec.Query(fixtureScope(&Entity{ID: "K8S_POD-42", Name: "checkout-1", Type: "K8S_POD"}))
	if !strings.Contains(byPod, `k8s.pod.name == "checkout-1"`) {
		t.Errorf("pod name filter missing:\n%s", byPod)
	}
	byWL := spec.Query(fixtureScope(&Entity{ID: "K8S_DEPLOYMENT-1", Name: "checkout", Type: "K8S_DEPLOYMENT"}))
	if !strings.Contains(byWL, `(k8s.workload.kind == "deployment" and k8s.workload.name == "checkout")`) {
		t.Errorf("workload name filter missing:\n%s", byWL)
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

func TestResourcesQueryUsesArg(t *testing.T) {
	spec := Lookup("resources")
	q := spec.Query(Scope{Timeframe: Timeframe{Label: "2h"}, Arg: "AWS_EC2_INSTANCE"})
	if !strings.Contains(q, `smartscapeNodes "AWS_EC2_INSTANCE"`) {
		t.Errorf("resources query ignores Arg:\n%s", q)
	}
	if !strings.Contains(q, "`tags:aws`[`Name`]") {
		t.Errorf("resources query missing Name-tag display fallback:\n%s", q)
	}
	if got := spec.Query(Scope{Timeframe: Timeframe{Label: "2h"}}); !strings.Contains(got, `smartscapeNodes "*"`) {
		t.Errorf("resources query without Arg should browse all types:\n%s", got)
	}
}

func TestVulnsQueryFloorsLookbackAt24h(t *testing.T) {
	spec := Lookup("vulnerabilities")
	short := spec.Query(Scope{Timeframe: Timeframe{Label: "2h", Dur: 2 * time.Hour}})
	if !strings.Contains(short, "from:now() - 24h") {
		t.Errorf("short window should floor to 24h:\n%s", short)
	}
	long := spec.Query(Scope{Timeframe: Timeframe{Label: "7d", Dur: 7 * 24 * time.Hour}})
	if !strings.Contains(long, "from:now() - 7d") {
		t.Errorf("long window should stay:\n%s", long)
	}
}

func TestEnrichSpecs(t *testing.T) {
	tf := Timeframe{Label: "2h", Dur: 2 * time.Hour}

	hosts := Lookup("hosts").Enrich
	q := hosts.Query(tf, []string{"HOST-1", "HOST-2"})
	if !strings.Contains(q, `in(dt.smartscape.host, {toSmartscapeId("HOST-1"), toSmartscapeId("HOST-2")})`) {
		t.Errorf("host enrichment must wrap ids in toSmartscapeId:\n%s", q)
	}
	if !strings.Contains(q, "interval: 5m") {
		t.Errorf("2h window should use 5m buckets:\n%s", q)
	}

	pods := Lookup("pods").Enrich
	q = pods.Query(tf, []string{"pod-a"})
	if !strings.Contains(q, `in(k8s.pod.name, {"pod-a"})`) {
		t.Errorf("pod enrichment joins by plain pod name:\n%s", q)
	}
	if pods.Key(map[string]any{"name": "pod-a"}) != "pod-a" || pods.By != "k8s.pod.name" {
		t.Error("pod enrichment join key mismatch")
	}
}

func TestSparkColumnSortsByLatestValue(t *testing.T) {
	col := SparkColumn("CPU", "cpu", 14, "%")
	rec := map[string]any{EnrichKey("cpu"): []any{1.0, nil, 3.5}}
	if got := col.Sort(rec); got != 3.5 {
		t.Errorf("Sort = %v, want 3.5 (latest non-null)", got)
	}
	if col.Sort(map[string]any{}) != nil {
		t.Error("unenriched rows must sort as empty")
	}
	// The cell carries the sparkline AND the latest value — a normalized
	// mini-graph alone can't distinguish a flat 3% from a flat 90%.
	if cell := col.Value(rec); !strings.HasSuffix(cell, "3.5%") {
		t.Errorf("sparkline cell should end with the latest value, got %q", cell)
	}
	if col.Value(map[string]any{}) != "" {
		t.Error("unenriched rows must render an empty cell")
	}
}

func TestFormatUnitShort(t *testing.T) {
	cases := []struct {
		f    float64
		unit string
		want string
	}{
		{78.2, "%", "78.2%"},
		{3.5, "mCores", "3.50m"},
		{73315123, "B", "69.9MiB"},
		{44134, "B/s", "43.1KiB/s"},
		{1234, "", "1234"},
	}
	for _, c := range cases {
		if got := FormatUnitShort(c.f, c.unit); got != c.want {
			t.Errorf("FormatUnitShort(%v, %q) = %q, want %q", c.f, c.unit, got, c.want)
		}
	}
	// The long form keeps the space (chart headers, vitals rows).
	if got := FormatUnit(73315123, "B"); got != "69.9 MiB" {
		t.Errorf("FormatUnit bytes = %q", got)
	}
}

func TestFormatNs(t *testing.T) {
	cases := map[string]string{
		"4845165":      "4.8ms",
		"163000":       "163µs",
		"20253838":     "20.3ms",
		"5800000000":   "5.80s",
		"126000000000": "2m06s",
	}
	for in, want := range cases {
		if got := FormatNs(in); got != want {
			t.Errorf("FormatNs(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestPrettyType(t *testing.T) {
	if got := prettyType("AWS_EC2_INSTANCE"); got != "ec2 instance" {
		t.Errorf("prettyType = %q", got)
	}
	if got := prettyType("K8S_POD"); got != "pod" {
		t.Errorf("prettyType = %q", got)
	}
}

func TestPodReadyAndClasses(t *testing.T) {
	if got := podReady(map[string]any{"ready": "1", "total": "2"}); got != "1/2" {
		t.Errorf("podReady = %q", got)
	}
	// Kubernetes omits readyReplicas when zero are ready.
	if got := workloadReady(map[string]any{"desired": "3"}); got != "0/3" {
		t.Errorf("workloadReady = %q", got)
	}
	if classPodReady("1/2") != "warn" || classPodReady("2/2") != "ok" {
		t.Error("classPodReady thresholds wrong")
	}
	if classPodPhase("Failed") != "error" || classPodPhase("Running") != "ok" {
		t.Error("classPodPhase mapping wrong")
	}
}
