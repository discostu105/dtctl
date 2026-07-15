package catalog

import (
	"strings"
	"testing"
)

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
