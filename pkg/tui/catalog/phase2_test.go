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

	unscoped := spec.Query(fixtureScope(nil))
	for _, want := range []string{
		"fetch spans, from:now() - 2h",
		"failed = countIf(request.is_failed == true)",
		"root = takeFirst(if(isNull(span.parent_id), coalesce(endpoint.name, span.name)))",
		"by:{trace.id}",
	} {
		if !strings.Contains(unscoped, want) {
			t.Errorf("traces query missing %q:\n%s", want, unscoped)
		}
	}

	scoped := spec.Query(fixtureScope(&Entity{ID: "K8S_POD-42", Name: "checkout-1", Type: "K8S_POD"}))
	if !strings.Contains(scoped, `dt.smartscape.k8s_pod == toSmartscapeId("K8S_POD-42")`) {
		t.Errorf("pod scope not composed into spans:\n%s", scoped)
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
	col := SparkColumn("CPU", "cpu", 8)
	rec := map[string]any{EnrichKey("cpu"): []any{1.0, nil, 3.5}}
	if got := col.Sort(rec); got != 3.5 {
		t.Errorf("Sort = %v, want 3.5 (latest non-null)", got)
	}
	if col.Sort(map[string]any{}) != nil {
		t.Error("unenriched rows must sort as empty")
	}
	if col.Value(rec) == "" {
		t.Error("sparkline cell should render for a non-empty series")
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
