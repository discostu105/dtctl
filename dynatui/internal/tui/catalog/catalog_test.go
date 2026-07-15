package catalog

import (
	"strings"
	"testing"
	"time"
)

func fixtureScope(entity *Entity) Scope {
	return Scope{Entity: entity, Timeframe: Timeframe{Label: "2h", Dur: 2 * time.Hour}}
}

func TestLookupResolvesNamesAndAliases(t *testing.T) {
	cases := map[string]string{
		"problems": "problems",
		"pb":       "problems",
		"svc":      "services",
		"hosts":    "hosts",
		"ho":       "hosts",
		"logs":     "logs",
		"ev":       "events",
		"HOSTS":    "hosts", // case-insensitive
	}
	for input, want := range cases {
		spec := Lookup(input)
		if spec == nil || spec.Name != want {
			t.Errorf("Lookup(%q) = %v, want %q", input, spec, want)
		}
	}
	if Lookup("nope") != nil {
		t.Error("Lookup(nope) should be nil")
	}
}

func TestMatchRanksPrefixFirst(t *testing.T) {
	m := Match("ho")
	if len(m) == 0 || m[0].Name != "hosts" {
		t.Fatalf("Match(ho) first = %v, want hosts", m)
	}
	if len(Match("")) != len(All()) {
		t.Error("empty input should match all views")
	}
	// Subsequence: "lg" matches "logs".
	found := false
	for _, s := range Match("lg") {
		if s.Name == "logs" {
			found = true
		}
	}
	if !found {
		t.Error("Match(lg) should include logs via subsequence")
	}
}

// The query templates are the contract between navigation and Grail: these
// tests pin the exact DQL that scope composition produces.
func TestProblemsQueryComposition(t *testing.T) {
	spec := Lookup("problems")

	unscoped := spec.Query(fixtureScope(nil))
	want := "fetch dt.davis.problems, from:now() - 2h\n| filter not(dt.davis.is_duplicate)\n| sort event.start desc\n| limit 200"
	if unscoped != want {
		t.Errorf("unscoped problems query:\n%s\nwant:\n%s", unscoped, want)
	}

	scoped := spec.Query(fixtureScope(&Entity{ID: "HOST-1234", Name: "web-1", Type: "HOST"}))
	if !strings.Contains(scoped, `matchesPhrase(arrayToString(smartscape.affected_entity.ids, delimiter:","), "HOST-1234")`) {
		t.Errorf("scoped problems query missing smartscape affected filter:\n%s", scoped)
	}
	if !strings.Contains(scoped, `matchesPhrase(arrayToString(affected_entity_ids, delimiter:","), "HOST-1234")`) {
		t.Errorf("scoped problems query missing legacy affected filter:\n%s", scoped)
	}
}

func TestLogsQueryComposesDualEraFilter(t *testing.T) {
	spec := Lookup("logs")
	scoped := spec.Query(fixtureScope(&Entity{ID: "SERVICE-42", Name: "checkout", Type: "SERVICE"}))
	for _, want := range []string{
		"fetch logs, from:now() - 2h",
		`dt.smartscape.service == toSmartscapeId("SERVICE-42")`,
		`dt.entity.service == "SERVICE-42"`,
		`dt.smartscape_source.id == toSmartscapeId("SERVICE-42")`,
		"| sort timestamp desc",
	} {
		if !strings.Contains(scoped, want) {
			t.Errorf("logs query missing %q:\n%s", want, scoped)
		}
	}
}

func TestSignalFilterKubernetesTypes(t *testing.T) {
	// K8s types have no legacy dt.entity.* mapping; they match the derived
	// dt.smartscape.* field plus the record source.
	f := SignalFilter(Entity{ID: "K8S_POD-77", Type: "K8S_POD"})
	if !strings.Contains(f, `dt.smartscape.k8s_pod == toSmartscapeId("K8S_POD-77")`) {
		t.Errorf("missing k8s_pod field match: %s", f)
	}
	if strings.Contains(f, "dt.entity.") {
		t.Errorf("unexpected legacy field for K8s type: %s", f)
	}
	if !strings.Contains(f, `dt.smartscape_source.id == toSmartscapeId("K8S_POD-77")`) {
		t.Errorf("missing source fallback: %s", f)
	}
}

func TestEntityViewsIgnoreScope(t *testing.T) {
	for _, name := range []string{"hosts", "services"} {
		spec := Lookup(name)
		q := spec.Query(fixtureScope(&Entity{ID: "HOST-1", Type: "HOST"}))
		if strings.Contains(q, "HOST-1") {
			t.Errorf("%s query should ignore entity scope in Phase 1:\n%s", name, q)
		}
		if !strings.HasPrefix(q, "smartscapeNodes") {
			t.Errorf("%s query should be Smartscape-backed:\n%s", name, q)
		}
	}
}

func TestMetricsFor(t *testing.T) {
	host := MetricsFor("HOST")
	if host == nil || len(host.Series) != 5 {
		t.Fatalf("MetricsFor(HOST) = %+v", host)
	}
	q := host.Query(Entity{ID: "HOST-9", Type: "HOST"}, Timeframe{Label: "30m"}, nil)
	for _, want := range []string{
		"timeseries {",
		"cpu = avg(dt.host.cpu.usage)",
		"from:now() - 30m",
		`dt.smartscape.host == toSmartscapeId("HOST-9")`,
	} {
		if !strings.Contains(q, want) {
			t.Errorf("host metrics query missing %q:\n%s", want, q)
		}
	}
	if MetricsFor("AWS_LAMBDA_FUNCTION") != nil {
		t.Error("uncurated types should return nil (view falls back to the explorer)")
	}
}

func TestMetricsSpecAvailabilitySubset(t *testing.T) {
	pod := MetricsFor("K8S_POD")
	e := Entity{ID: "K8S_POD-9", Type: "K8S_POD"}
	tf := Timeframe{Label: "2h"}

	probe := pod.AvailabilityQuery(e, tf)
	for _, want := range []string{
		"metrics from:now() - 2h",
		`| filter dt.smartscape.k8s_pod == toSmartscapeId("K8S_POD-9")`,
		"| summarize count(), by:{metric.key}",
	} {
		if !strings.Contains(probe, want) {
			t.Errorf("availability probe missing %q:\n%s", want, probe)
		}
	}

	// A limit-less pod reports only the universal keys: the query must chart
	// exactly those — one absent metric in a timeseries query zeroes the
	// whole result (validated live).
	available := map[string]bool{
		"dt.kubernetes.container.cpu_usage":          true,
		"dt.kubernetes.container.memory_working_set": true,
		"dt.kubernetes.pod.network_received_data":    true,
		"dt.kubernetes.pod.network_transmitted_data": true,
	}
	q := pod.Query(e, tf, available)
	for _, want := range []string{"cpu = sum(", "mem = sum(", "net_rx = avg(", "net_tx = avg("} {
		if !strings.Contains(q, want) {
			t.Errorf("subset query missing %q:\n%s", want, q)
		}
	}
	for _, reject := range []string{"cpu_limit", "mem_limit", "throttled"} {
		if strings.Contains(q, reject) {
			t.Errorf("subset query must drop unavailable %q:\n%s", reject, q)
		}
	}
	if pod.Query(e, tf, map[string]bool{}) != "" {
		t.Error("nothing available should render an empty query")
	}
}

func TestProblemEntityExtraction(t *testing.T) {
	rec := map[string]any{
		"smartscape.affected_entities": []any{
			map[string]any{"id": "K8S_DAEMONSET-1", "name": "agent", "type": "K8S_DAEMONSET"},
		},
	}
	e := problemEntity(rec)
	if e == nil || e.ID != "K8S_DAEMONSET-1" || e.Type != "K8S_DAEMONSET" || e.Name != "agent" {
		t.Errorf("problemEntity = %+v", e)
	}
	if problemEntity(map[string]any{}) != nil {
		t.Error("no affected entities should yield nil")
	}
}

func TestFormatHelpers(t *testing.T) {
	if got := FormatBytes(8198213632); got != "7.6 GiB" {
		t.Errorf("FormatBytes = %q", got)
	}
	if got := FormatDuration(34 * time.Minute); got != "34m" {
		t.Errorf("FormatDuration = %q", got)
	}
	if got := FormatDuration(72 * time.Hour); got != "3d" {
		t.Errorf("FormatDuration = %q", got)
	}
	if got := FormatValue([]any{"a", "b", "c", "d", "e"}); got != "a,b,c,+2" {
		t.Errorf("FormatValue(array) = %q", got)
	}
	if Age("garbage") != "" {
		t.Error("Age should be empty for unparseable input")
	}
}

// TestLookupResolvesShortAliases pins the command-bar shorthand for every
// registered view family.
func TestLookupResolvesShortAliases(t *testing.T) {
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
		"se":    "sessions", "ue": "userevents", "biz": "bizevents",
		"dict": "dictionary", "models": "dictionary", "fields": "dictionary",
		"tbl": "tables",
		"bkt": "buckets", "lookups": "files", "syn": "synthetic",
		"slo": "slos", "ad": "detectors", "pat": "patterns", "runs": "executions",
	}
	for input, want := range cases {
		spec := Lookup(input)
		if spec == nil || spec.Name != want {
			t.Errorf("Lookup(%q) = %v, want %q", input, spec, want)
		}
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
