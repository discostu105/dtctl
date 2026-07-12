package catalog

import (
	"strings"
	"testing"
)

func TestDetailQuery(t *testing.T) {
	got := DetailQuery(Entity{ID: "HOST-AAAABBBBCCCCDDDD", Type: "HOST"})
	want := "smartscapeNodes \"HOST\"\n| filter id == toSmartscapeId(\"HOST-AAAABBBBCCCCDDDD\")\n| fieldsAdd references\n| limit 1"
	if got != want {
		t.Errorf("DetailQuery:\ngot  %q\nwant %q", got, want)
	}
}

func factValues(t *testing.T, entityType string, rec map[string]any) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, f := range KeyFacts(entityType) {
		out[f.Label] = f.Value(rec)
	}
	return out
}

func TestKeyFactsHost(t *testing.T) {
	rec := map[string]any{
		"id":                    "HOST-AAAABBBBCCCCDDDD",
		"name":                  "web-01.example.invalid",
		"os.type":               "OS_TYPE_LINUX",
		"os.version":            "Test Linux 1.0",
		"logical_cores":         "2",
		"cores":                 "1",
		"memory":                "8198213632",
		"ip":                    []any{"10.0.0.1"},
		"cloud.provider":        "aws",
		"aws.availability_zone": "us-east-1b",
		"sku":                   "t3.large",
		"aws.resource.id":       "i-00000000000000000",
		"dt.host_group.id":      "group-a",
		"lifetime":              map[string]any{"start": "2026-06-18T15:00:00.000000000Z", "end": "2026-07-05T20:51:00.000000000Z"},
	}
	facts := factValues(t, "HOST", rec)
	for label, want := range map[string]string{
		"id":         "HOST-AAAABBBBCCCCDDDD",
		"os":         "LINUX · Test Linux 1.0",
		"cpu":        "2 logical / 1 physical",
		"memory":     "7.6 GiB",
		"ip":         "10.0.0.1",
		"cloud":      "aws us-east-1b",
		"instance":   "t3.large (i-00000000000000000)",
		"host group": "group-a",
	} {
		if facts[label] != want {
			t.Errorf("fact %q = %q, want %q", label, facts[label], want)
		}
	}
	if facts["first seen"] == "" || facts["last seen"] == "" {
		t.Errorf("lifetime facts empty: %+v", facts)
	}
}

func TestKeyFactsSparseRecordSkipsEmpty(t *testing.T) {
	facts := factValues(t, "HOST", map[string]any{"id": "HOST-1"})
	if facts["cloud"] != "" || facts["instance"] != "" || facts["first seen"] != "" {
		t.Errorf("facts on sparse record should be empty: %+v", facts)
	}
}

func TestKeyFactsFallbackType(t *testing.T) {
	facts := factValues(t, "SOME_NEW_TYPE", map[string]any{
		"id": "SOME_NEW_TYPE-1", "type": "SOME_NEW_TYPE",
		"host.name": "web-01.example.invalid", "k8s.cluster.name": "prod",
	})
	if facts["id"] != "SOME_NEW_TYPE-1" || facts["type"] != "SOME_NEW_TYPE" {
		t.Errorf("fallback facts = %+v", facts)
	}
	// Unknown types still surface whatever placement context the record has.
	if facts["host"] != "web-01.example.invalid" || facts["cluster"] != "prod" {
		t.Errorf("fallback placement facts = %+v", facts)
	}
}

func TestKeyFactsPod(t *testing.T) {
	facts := factValues(t, "K8S_POD", map[string]any{
		"id":                "K8S_POD-1",
		"k8s.pod.phase":     "Running",
		"k8s.workload.kind": "deployment",
		"k8s.workload.name": "checkout",
	})
	if facts["phase"] != "Running" || facts["workload"] != "deployment checkout" {
		t.Errorf("pod facts = %+v", facts)
	}
}

func TestKeyFactsService(t *testing.T) {
	labels := strings.Builder{}
	for _, f := range KeyFacts("SERVICE") {
		labels.WriteString(f.Label + ",")
	}
	for _, want := range []string{"id", "detection", "first seen", "last seen"} {
		if !strings.Contains(labels.String(), want) {
			t.Errorf("SERVICE facts missing %q (have %s)", want, labels.String())
		}
	}
}

// TestKeyFactsPodListAliases: the pods LIST summarizes into alias fields
// (phase/ready/total/restarts/node/workload) — the facts must read those so
// the list preview is not sparse.
func TestKeyFactsPodListAliases(t *testing.T) {
	facts := factValues(t, "K8S_POD", map[string]any{
		"id": "K8S_POD-1", "phase": "Pending", "ready": "1", "total": "2",
		"restarts": "7", "node": "node-1", "workload": "checkout",
		"kind": "deployment", "namespace": "shop",
		"created": "2026-07-02T16:30:32Z",
	})
	for label, want := range map[string]string{
		"phase": "Pending", "ready": "1/2", "restarts": "7",
		"node": "node-1", "namespace": "shop", "workload": "deployment checkout",
	} {
		if facts[label] != want {
			t.Errorf("fact %q = %q, want %q", label, facts[label], want)
		}
	}
	if facts["created"] == "" {
		t.Error("created fact should format the list alias")
	}
}

// TestKeyFactsPodManifest: full node records carry the k8s.object manifest —
// readiness and restarts count from containerStatuses.
func TestKeyFactsPodManifest(t *testing.T) {
	facts := factValues(t, "K8S_POD", map[string]any{
		"id":            "K8S_POD-1",
		"k8s.pod.phase": "Running",
		"k8s.object": `{"metadata":{"creationTimestamp":"2026-07-02T16:30:32Z"},` +
			`"status":{"containerStatuses":[{"ready":true,"restartCount":2},{"ready":false,"restartCount":1}]}}`,
	})
	if facts["ready"] != "1/2" || facts["restarts"] != "3" {
		t.Errorf("pod manifest facts = ready %q restarts %q", facts["ready"], facts["restarts"])
	}
}

// TestKeyFactsWorkloadManifest: deployments read replica readiness, images,
// and strategy from the manifest.
func TestKeyFactsWorkloadManifest(t *testing.T) {
	facts := factValues(t, "K8S_DEPLOYMENT", map[string]any{
		"id": "K8S_DEPLOYMENT-1", "k8s.workload.kind": "deployment",
		"k8s.namespace.name": "shop",
		"k8s.object": `{"metadata":{"creationTimestamp":"2026-07-02T16:30:32Z"},` +
			`"spec":{"replicas":3,"strategy":{"type":"RollingUpdate"},` +
			`"template":{"spec":{"containers":[{"image":"shop/checkout:1.2.3"},{"image":"envoy:1"}]}}},` +
			`"status":{"readyReplicas":2}}`,
	})
	for label, want := range map[string]string{
		"ready": "2/3", "image": "shop/checkout:1.2.3 +1", "strategy": "RollingUpdate",
		"namespace": "shop",
	} {
		if facts[label] != want {
			t.Errorf("fact %q = %q, want %q", label, facts[label], want)
		}
	}
	// Class: partial readiness warns, full readiness reads ok.
	if classPodReady("2/3") != "warn" || classPodReady("3/3") != "ok" {
		t.Error("workload ready classes wrong")
	}
}

// TestKeyFactsJobAndCron: jobs report their outcome and run duration; crons
// their schedule and suspension.
func TestKeyFactsJobAndCron(t *testing.T) {
	job := factValues(t, "K8S_JOB", map[string]any{
		"id": "K8S_JOB-1", "k8s.workload.kind": "cronjob", "k8s.workload.name": "cleanup",
		"k8s.object": `{"status":{"failed":1,"startTime":"2026-07-12T15:10:00Z","completionTime":"2026-07-12T15:10:34Z"}}`,
	})
	if job["status"] != "failed" || classJobStatus("failed") != "error" {
		t.Errorf("job status = %q", job["status"])
	}
	if !strings.Contains(job["ran"], "took 34s") {
		t.Errorf("job ran = %q, want the duration", job["ran"])
	}
	if job["owner"] != "cronjob cleanup" {
		t.Errorf("job owner = %q", job["owner"])
	}

	cron := factValues(t, "K8S_CRONJOB", map[string]any{
		"id":         "K8S_CRONJOB-1",
		"k8s.object": `{"spec":{"schedule":"*/5 * * * *","suspend":true},"status":{"lastScheduleTime":"2026-07-12T15:10:00Z"}}`,
	})
	if cron["schedule"] != "*/5 * * * *" || cron["suspended"] != "yes" {
		t.Errorf("cron facts = %+v", cron)
	}
	if cron["last run"] == "" {
		t.Error("cron last run should format lastScheduleTime")
	}
}

// TestKeyFactsServiceAndVolume: K8s services summarize ports and external
// addresses; PVCs their phase and capacity in IEC units.
func TestKeyFactsServiceAndVolume(t *testing.T) {
	svc := factValues(t, "K8S_SERVICE", map[string]any{
		"id": "K8S_SERVICE-1",
		"k8s.object": `{"spec":{"type":"LoadBalancer","clusterIP":"10.0.0.7",` +
			`"ports":[{"name":"https","port":443},{"port":8080}]},` +
			`"status":{"loadBalancer":{"ingress":[{"ip":"20.1.2.3"}]}}}`,
	})
	for label, want := range map[string]string{
		"type": "LoadBalancer", "cluster ip": "10.0.0.7",
		"ports": "https:443, 8080", "external ip": "20.1.2.3",
	} {
		if svc[label] != want {
			t.Errorf("service fact %q = %q, want %q", label, svc[label], want)
		}
	}

	pvc := factValues(t, "K8S_PERSISTENTVOLUMECLAIM", map[string]any{
		"id":         "K8S_PVC-1",
		"k8s.object": `{"spec":{"storageClassName":"default","accessModes":["ReadWriteOnce"]},"status":{"phase":"Bound","capacity":{"storage":"10Gi"}}}`,
	})
	if pvc["phase"] != "Bound" || classVolumePhase("Bound") != "ok" {
		t.Errorf("pvc phase = %q", pvc["phase"])
	}
	if pvc["capacity"] != "10.0 GiB" || pvc["class"] != "default" {
		t.Errorf("pvc facts = %+v", pvc)
	}
}

// TestKeyFactsNode: kubelet/os come from the nodes list's aliases or the
// manifest's nodeInfo; capacity summarizes cpu+memory.
func TestKeyFactsNode(t *testing.T) {
	aliasRow := factValues(t, "K8S_NODE", map[string]any{
		"id": "K8S_NODE-1", "version": "v1.33.6", "os": "Ubuntu 22.04.5 LTS",
		"cpus": "4", "instance": "e2-custom-4", "zone": "us-central1-c",
	})
	if aliasRow["kubelet"] != "v1.33.6" || aliasRow["capacity"] != "4 cpu" || aliasRow["instance"] != "e2-custom-4" {
		t.Errorf("node alias facts = %+v", aliasRow)
	}
	full := factValues(t, "K8S_NODE", map[string]any{
		"id": "K8S_NODE-1",
		"k8s.object": `{"status":{"capacity":{"cpu":"4","memory":"16374532Ki"},` +
			`"addresses":[{"type":"InternalIP","address":"10.1.0.7"},{"type":"Hostname","address":"x"}],` +
			`"nodeInfo":{"kubeletVersion":"v1.33.6","osImage":"Ubuntu 22.04.5 LTS","containerRuntimeVersion":"containerd://2.0.5"}}}`,
	})
	if full["kubelet"] != "v1.33.6" || full["os"] != "Ubuntu 22.04.5 LTS" {
		t.Errorf("node manifest facts = %+v", full)
	}
	if full["capacity"] != "4 cpu · 15.6 GiB" {
		t.Errorf("node capacity = %q", full["capacity"])
	}
	if full["ip"] != "10.1.0.7" || full["runtime"] != "containerd://2.0.5" {
		t.Errorf("node addresses/runtime = %+v", full)
	}
}

// TestKeyFactsCloudArms: AWS state colors, Azure sku/resource group, GCP
// project/addresses — and the acquisition fact stays silent on OK.
func TestKeyFactsCloudArms(t *testing.T) {
	aws := factValues(t, "AWS_EC2_INSTANCE", map[string]any{
		"id": "AWS_EC2_INSTANCE-1", "aws.resource.type": "AWS::EC2::Instance",
		"aws.state": "terminated", "aws.region": "us-east-1",
		"aws.account.id": "123", "cloud.acquisition.status": "OK",
	})
	if aws["state"] != "terminated" || classCloudState("terminated") != "dim" {
		t.Errorf("aws state = %q", aws["state"])
	}
	if aws["acquisition"] != "" {
		t.Error("acquisition fact must stay silent on OK")
	}
	azure := factValues(t, "AZURE_MICROSOFT_COMPUTE_DISKS", map[string]any{
		"id": "A-1", "azure.resource.group": "rg-prod", "azure.location": "eastus",
		"azure.resource.sku.name": "Standard_D4as_v4",
	})
	if azure["resource group"] != "rg-prod" || azure["sku"] != "Standard_D4as_v4" {
		t.Errorf("azure facts = %+v", azure)
	}
	gcp := factValues(t, "GCP_COMPUTE_GOOGLEAPIS_COM_INSTANCE", map[string]any{
		"id": "G-1", "gcp.project.id": "demo", "gcp.zone": "us-central1-c",
		"private_ip_address": []any{"10.1.0.6"}, "public_ip_address": []any{"34.59.226.42"},
	})
	if gcp["project"] != "demo" || gcp["ip"] != "10.1.0.6 · 34.59.226.42" {
		t.Errorf("gcp facts = %+v", gcp)
	}
}

// TestKeyFactsInfraTypes: the ONEAGENT/DB/interface arms read their live
// field shapes (validated on the box tenant).
func TestKeyFactsInfraTypes(t *testing.T) {
	agent := factValues(t, "ONEAGENT", map[string]any{
		"id": "ONEAGENT-1", "dt.agent.module.version": "1.341.48",
		"dt.agent.monitoring_mode": "FULL_STACK", "dt.network_zone.id": "default",
	})
	if agent["version"] != "1.341.48" || agent["mode"] != "FULL_STACK" {
		t.Errorf("oneagent facts = %+v", agent)
	}
	db := factValues(t, "DB_DATABASE_MSSQL", map[string]any{
		"id": "DB-1", "db.system": "mssql", "db.database.name": "shop",
		"state": "ONLINE", "db.connection_details.hostname": "db.example.invalid",
	})
	if db["system"] != "mssql" || db["state"] != "ONLINE" || classDBState("ONLINE") != "ok" {
		t.Errorf("db facts = %+v", db)
	}
	iface := factValues(t, "EXT_NETWORK_INTERFACE", map[string]any{
		"id": "IF-1", "operational_status": "down(2)", "speed": "1000", "mtu": "1518",
	})
	if iface["status"] != "down(2)" || classIfStatus("down(2)") != "error" {
		t.Errorf("interface facts = %+v", iface)
	}
}
