package catalog

import (
	"strings"
	"testing"
)

func TestProcessesSpecScoping(t *testing.T) {
	spec := Lookup("processes")
	if spec == nil {
		t.Fatal("processes spec not registered")
	}
	tf := Timeframe{Label: "2h"}

	host := spec.Query(Scope{Timeframe: tf,
		Entity: &Entity{ID: "HOST-1", Name: "web-01", Type: "HOST"}})
	if !strings.Contains(host, `| filter host.name == "web-01"`) {
		t.Errorf("host scope missing host.name filter:\n%s", host)
	}

	node := spec.Query(Scope{Timeframe: tf,
		Entity: &Entity{ID: "K8S_NODE-1", Name: "node-a", Type: "K8S_NODE"}})
	if !strings.Contains(node, `| filter host.name == "node-a"`) {
		t.Errorf("node scope missing host.name filter:\n%s", node)
	}

	pod := spec.Query(Scope{Timeframe: tf,
		Entity: &Entity{ID: "K8S_POD-1", Name: "checkout-abc", Type: "K8S_POD"}})
	if !strings.Contains(pod, "| filter process.metadata[`KUBERNETES_FULL_POD_NAME`] == \"checkout-abc\"") {
		t.Errorf("pod scope missing metadata filter:\n%s", pod)
	}

	// A service scope joins through its runs_on edges — process nodes carry
	// no service field to filter by (ServiceRunsOnStage).
	svc := spec.Query(Scope{Timeframe: tf,
		Entity: &Entity{ID: "SERVICE-1", Name: "checkout", Type: "SERVICE"}})
	for _, want := range []string{
		`| join [smartscapeEdges "*" | filter source_id == toSmartscapeId("SERVICE-1") and type == "runs_on"`,
		"on:{left[id] == right[target_id]}, kind:inner",
		"| fieldsRemove right.target_id",
	} {
		if !strings.Contains(svc, want) {
			t.Errorf("service scope missing %q:\n%s", want, svc)
		}
	}

	// An incompatible or unnamed entity must not narrow the list at all —
	// CanScope's honesty rule.
	unscoped := spec.Query(Scope{Timeframe: tf})
	fe := spec.Query(Scope{Timeframe: tf,
		Entity: &Entity{ID: "FRONTEND-1", Name: "shop-ui", Type: "FRONTEND"}})
	if fe != unscoped {
		t.Errorf("frontend scope should not compose:\n%s", fe)
	}
	if !strings.Contains(unscoped, `smartscapeNodes "PROCESS"`) || !strings.Contains(unscoped, "| limit 500") {
		t.Errorf("unscoped query = %s", unscoped)
	}

	enrich := spec.Enrich.Query(tf, []string{"PROCESS-1", "PROCESS-2"})
	for _, want := range []string{
		"avg(dt.process.cpu.usage)", "avg(dt.process.memory.usage)",
		"by:{dt.smartscape.process}", `toSmartscapeId("PROCESS-1")`,
	} {
		if !strings.Contains(enrich, want) {
			t.Errorf("process enrich query missing %q:\n%s", want, enrich)
		}
	}
}

func TestContainersSpecScoping(t *testing.T) {
	spec := Lookup("containers")
	if spec == nil {
		t.Fatal("containers spec not registered")
	}
	tf := Timeframe{Label: "2h"}

	pod := spec.Query(Scope{Timeframe: tf,
		Entity: &Entity{ID: "K8S_POD-1", Name: "checkout-abc", Type: "K8S_POD"}})
	if !strings.Contains(pod, `| filter k8s.pod.name == "checkout-abc"`) {
		t.Errorf("pod scope missing k8s.pod.name filter:\n%s", pod)
	}

	host := spec.Query(Scope{Timeframe: tf,
		Entity: &Entity{ID: "HOST-1", Name: "web-01", Type: "HOST"}})
	if !strings.Contains(host, `| filter host.name == "web-01"`) {
		t.Errorf("host scope missing host.name filter:\n%s", host)
	}

	wl := spec.Query(Scope{Timeframe: tf,
		Entity: &Entity{ID: "K8S_DEPLOYMENT-1", Name: "checkout", Type: "K8S_DEPLOYMENT"}})
	if !strings.Contains(wl, `k8s.workload.kind == "deployment" and k8s.workload.name == "checkout"`) {
		t.Errorf("workload scope missing kind+name filter:\n%s", wl)
	}

	// A service scope joins through its runs_on edges, like processes.
	svc := spec.Query(Scope{Timeframe: tf,
		Entity: &Entity{ID: "SERVICE-1", Name: "checkout", Type: "SERVICE"}})
	if !strings.Contains(svc, `source_id == toSmartscapeId("SERVICE-1") and type == "runs_on"`) {
		t.Errorf("service scope missing runs_on join:\n%s", svc)
	}

	enrich := spec.Enrich.Query(tf, []string{"CONTAINER-1"})
	for _, want := range []string{
		"avg(dt.kubernetes.container.cpu_usage)", "avg(dt.kubernetes.container.memory_working_set)",
		"by:{dt.smartscape.container}", `toSmartscapeId("CONTAINER-1")`,
	} {
		if !strings.Contains(enrich, want) {
			t.Errorf("container enrich query missing %q:\n%s", want, enrich)
		}
	}
}

func TestContainerImage(t *testing.T) {
	rec := map[string]any{
		"container.image.name":    "docker.io/otel/opentelemetry-collector-contrib",
		"container.image.version": "0.119.0",
	}
	if got := containerImage(rec); got != "opentelemetry-collector-contrib:0.119.0" {
		t.Errorf("containerImage = %q", got)
	}
	if got := containerImage(map[string]any{}); got != "" {
		t.Errorf("empty record image = %q", got)
	}
}

func TestProcessTech(t *testing.T) {
	rec := map[string]any{
		"process.software_technologies.os": []any{
			map[string]any{"type": "GO", "version": ""},
		},
	}
	if got := processTech(rec); got != "go" {
		t.Errorf("processTech = %q", got)
	}
	// Runtime technologies win over OS-level ones.
	rec["process.software_technologies"] = []any{
		map[string]any{"type": "JVM", "version": "21"},
	}
	if got := processTech(rec); got != "jvm" {
		t.Errorf("processTech with runtime list = %q", got)
	}
	// Container runtimes only win when nothing else is detected — the CONT
	// column already tells the containerized story.
	mixed := map[string]any{
		"process.software_technologies": []any{
			map[string]any{"type": "CONTAINERD"},
			map[string]any{"type": "GO"},
		},
	}
	if got := processTech(mixed); got != "go" {
		t.Errorf("processTech should skip container runtimes, got %q", got)
	}
	only := map[string]any{
		"process.software_technologies": []any{map[string]any{"type": "CONTAINERD"}},
	}
	if got := processTech(only); got != "containerd" {
		t.Errorf("processTech fallback = %q", got)
	}
}

func TestVitalsFor(t *testing.T) {
	// Host vitals are the full utilization picture; every series is vital.
	host := VitalsFor("HOST")
	if host == nil || len(host.Series) != 5 {
		t.Fatalf("VitalsFor(HOST) = %+v", host)
	}
	q := host.Query(Entity{ID: "HOST-9", Type: "HOST"}, Timeframe{Label: "2h"}, nil)
	if !strings.Contains(q, `dt.smartscape.host == toSmartscapeId("HOST-9")`) {
		t.Errorf("host vitals query not scoped:\n%s", q)
	}
	if !strings.Contains(q, "disk = max(dt.host.disk.used.percent)") {
		t.Errorf("host disk vital should chart the fullest disk (max):\n%s", q)
	}

	// Pod vitals are the universal keys only — the limits story stays on the
	// metrics tab (limit series don't exist on every pod).
	pod := VitalsFor("K8S_POD")
	if pod == nil || len(pod.Series) != 4 {
		t.Fatalf("VitalsFor(K8S_POD) = %+v", pod)
	}
	for _, s := range pod.Series {
		if strings.Contains(s.Alias, "limit") || s.Alias == "throttled" {
			t.Errorf("pod vitals must not include %s", s.Alias)
		}
	}

	// The node's vitals ride on its host's metrics: the filter must carry
	// the host.name arm so dt.host.* series match.
	node := VitalsFor("K8S_NODE")
	nq := node.Query(Entity{ID: "K8S_NODE-9", Name: "node-a", Type: "K8S_NODE"}, Timeframe{Label: "2h"}, nil)
	if !strings.Contains(nq, `host.name == "node-a"`) ||
		!strings.Contains(nq, `dt.smartscape.k8s_node == toSmartscapeId("K8S_NODE-9")`) {
		t.Errorf("node vitals filter must or-chain smartscape id and host name:\n%s", nq)
	}

	// New curated types exist; uncurated types stay nil.
	for _, typ := range []string{"PROCESS", "CONTAINER", "K8S_NAMESPACE", "K8S_CLUSTER", "K8S_DEPLOYMENT"} {
		if VitalsFor(typ) == nil {
			t.Errorf("VitalsFor(%s) = nil", typ)
		}
	}
	if VitalsFor("AWS_LAMBDA_FUNCTION") != nil {
		t.Error("uncurated types should have no vitals")
	}

	// Vitals reuse the parent spec's filter: a vitals query and a metrics
	// query for the same entity scope identically.
	proc := VitalsFor("PROCESS")
	pq := proc.Query(Entity{ID: "PROCESS-9", Type: "PROCESS"}, Timeframe{Label: "2h"}, nil)
	if !strings.Contains(pq, `dt.smartscape.process == toSmartscapeId("PROCESS-9")`) {
		t.Errorf("process vitals not scoped via dt.smartscape.process:\n%s", pq)
	}
}
