package catalog

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Fact is one curated line on the entity detail page's key-facts panel and
// the entity preview panes: the handful of properties someone triaging wants
// without reading the full record. Class optionally maps the rendered value
// to a severity render class (a Failed pod phase colors like an ERROR log).
type Fact struct {
	Label string
	Value func(rec map[string]any) string
	Class func(val string) string
}

// DetailQuery fetches the full Smartscape node behind an entity. Validated
// live: id must be compared via toSmartscapeId() — a plain string comparison
// silently matches nothing. Unlike the list queries, nothing is stripped:
// this is one record, and k8s.object (the full manifest — spec, status,
// images, resources) and references (containment edges as navigable entity
// ids) are exactly the depth a detail page is for. references is NOT in the
// default projection and must be fieldsAdd-ed explicitly (validated live).
// The inspector keeps huge JSON docs collapsed by default, so the manifest
// cannot swamp the page.
func DetailQuery(e Entity) string {
	return fmt.Sprintf("smartscapeNodes %q\n| filter id == toSmartscapeId(%q)\n| fieldsAdd references\n| limit 1",
		e.Type, e.ID)
}

// PriorityFields returns the highlight fields for a record, most important
// first — the block the inspector hoists above the namespace groups. The set
// is per record kind (a problem's essentials are not a log record's); fields
// the record does not carry are skipped at render time.
func PriorityFields(rec map[string]any) []string {
	switch {
	case Str(rec, "event.kind") == "DAVIS_PROBLEM":
		return []string{"display_id", "event.name", "event.status", "event.severity",
			"event.category", "event.description", "event.start", "event.end",
			"dt.davis.impact_level", "affected_entity_names"}
	case Str(rec, "event.kind") == "DAVIS_EVENT":
		return []string{"event.name", "event.type", "event.status", "event.severity",
			"event.description", "event.start", "event.end",
			"dt.davis.is_rootcause_relevant", "dt_source_entity_name"}
	case isAttackRecord(rec):
		// Before the vulnerability arm: attack records carry
		// vulnerability.code_location.name but are detections, not vulns.
		return []string{"finding.title", "finding.type", "finding.severity",
			"finding.action", "timestamp", "entry_point.payload",
			"entry_point.url.path", "entry_point.function.name", "actor.ips",
			"dt.security.rap.target.name", "vulnerability.code_location.name",
			"trace.id"}
	case rec["vulnerability.id"] != nil || Str(rec, "vulnerability.display_id") != "":
		// The vulns view summarizes into short aliases (title, level, score…);
		// raw security.events records keep the vulnerability.* names.
		return []string{"title", "display_id", "level", "score", "status", "tech",
			"cve", "url", "affected", "exposure", "exploit", "fix", "stack",
			"component", "vulnerability.title", "vulnerability.risk.level",
			"vulnerability.risk.score", "vulnerability.resolution.status",
			"vulnerability.davis_assessment.exposure_status",
			"vulnerability.davis_assessment.exploit_status",
			"vulnerability.description", "vulnerability.remediation.description"}
	case rec["span.kind"] != nil || Str(rec, "span.name") != "":
		// Whichever service-name attribute this span carries (see SpanService);
		// listing both would render the same value twice on OneAgent spans.
		svcField := "service.name"
		if Str(rec, svcField) == "" {
			svcField = "dt.service.name"
		}
		return []string{"span.name", "endpoint.name", "span.kind", svcField,
			"start_time", "duration", "span.status_code", "gen_ai.operation.name"}
	case rec["user_action_count"] != nil || Str(rec, "end_reason") != "":
		// RUM session (user.sessions).
		return []string{"start_time", "duration", "frontend.name", "view_summary_count",
			"user_action_count", "request_count", "error.count", "browser.name",
			"os.name", "geo.country.iso_code", "end_reason"}
	case rec["characteristics.classifier"] != nil:
		// RUM event (user.events).
		return []string{"start_time", "characteristics.classifier", "view.name",
			"user_action.name", "error.display_name", "frontend.name"}
	case Str(rec, "monitor.name") != "" || rec["result.state"] != nil:
		// Synthetic execution (dt.synthetic.events).
		return []string{"timestamp", "monitor.name", "event.type", "result.state",
			"result.status.message", "step.name"}
	case Str(rec, "event.kind") == "BIZ_EVENT",
		Str(rec, "event.provider") != "" && Str(rec, "event.kind") == "":
		// Business event (event.kind BIZ_EVENT, validated live; the second
		// arm catches producers that omit the kind). Payload fields vary per
		// producer, so the stable envelope leads and the producer's payload
		// reads below in its namespace groups.
		return []string{"event.type", "event.provider", "event.category",
			"event.name", "timestamp", "event.id"}
	}
	return []string{"content", "event.name", "event.description", "display_id",
		"event.status", "event.category", "timestamp", "loglevel", "status", "host.name"}
}

// KeyFacts returns the curated most-relevant properties for an entity type.
// Facts whose value is empty are skipped at render time, so a fact may probe
// fields that only some records carry — in particular both the FULL node
// record (navigator preview, detail page, census sampler) and the summarized
// LIST-row aliases some views project (the pods list renames k8s.pod.phase to
// phase, the nodes list precomputes version/os/cpus — validated against the
// specs' own queries). Where the substance lives only in the k8s.object
// manifest (replica readiness, cron schedules, PVC capacity), the fact digs
// into the parsed manifest. Curating a new type? Walk the package doc's
// "Curating an entity type" checklist — this switch is one of several
// per-type dispatch points.
func KeyFacts(entityType string) []Fact {
	common := []Fact{{Label: "id", Value: factField("id")}}
	switch entityType {
	case "HOST":
		return append(common,
			Fact{Label: "os", Value: hostOS},
			Fact{Label: "cpu", Value: hostCPU},
			Fact{Label: "memory", Value: func(rec map[string]any) string { return FormatBytesStr(Str(rec, "memory")) }},
			Fact{Label: "ip", Value: factField("ip")},
			Fact{Label: "public ip", Value: factField("public_ip")},
			Fact{Label: "cloud", Value: hostCloud},
			Fact{Label: "instance", Value: hostInstance},
			Fact{Label: "hypervisor", Value: trimmedField("hypervisor.type", "HYPERVISOR_TYPE_")},
			Fact{Label: "host group", Value: factField("dt.host_group.id")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case "SERVICE":
		return append(common,
			Fact{Label: "detection", Value: factField("dt.service_detection.version")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case "PROCESS":
		return append(common,
			Fact{Label: "group", Value: factField("dt.process_group.detected_name")},
			Fact{Label: "tech", Value: processTech},
			Fact{Label: "command", Value: processCommand},
			Fact{Label: "host", Value: factField("host.name")},
			Fact{Label: "pod", Value: processPod},
			Fact{Label: "containerized", Value: flagField("process.containerized")},
			Fact{Label: "ports", Value: factField("port")},
			Fact{Label: "cost center", Value: factField("dt.cost.costcenter")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case "CONTAINER":
		return append(common,
			Fact{Label: "image", Value: containerImage},
			Fact{Label: "pod", Value: factField("k8s.pod.name")},
			Fact{Label: "namespace", Value: factField("k8s.namespace.name")},
			Fact{Label: "node", Value: factField("k8s.node.name")},
			Fact{Label: "cluster", Value: factField("k8s.cluster.name")},
			Fact{Label: "runtime", Value: factField("container.runtime.name")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case "K8S_POD":
		return append(common,
			Fact{Label: "phase", Value: fieldsFirst("k8s.pod.phase", "phase"), Class: classPodPhase},
			Fact{Label: "ready", Value: podReadyFact, Class: classPodReady},
			Fact{Label: "restarts", Value: podRestartsFact, Class: classNonzeroWarn},
			Fact{Label: "workload", Value: podWorkload},
			Fact{Label: "namespace", Value: fieldsFirst("k8s.namespace.name", "namespace")},
			Fact{Label: "node", Value: fieldsFirst("k8s.node.name", "node")},
			Fact{Label: "cluster", Value: factField("k8s.cluster.name")},
			Fact{Label: "cost center", Value: factField("dt.cost.costcenter")},
			Fact{Label: "created", Value: k8sCreated},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case "K8S_DEPLOYMENT", "K8S_STATEFULSET", "K8S_DAEMONSET", "K8S_REPLICASET":
		return append(common,
			Fact{Label: "ready", Value: workloadReady, Class: classPodReady},
			Fact{Label: "kind", Value: fieldsFirst("k8s.workload.kind", "kind")},
			Fact{Label: "owner", Value: replicasetOwner},
			Fact{Label: "image", Value: workloadImages},
			Fact{Label: "strategy", Value: manifestValue("spec", "strategy", "type")},
			Fact{Label: "namespace", Value: fieldsFirst("k8s.namespace.name", "namespace")},
			Fact{Label: "cluster", Value: factField("k8s.cluster.name")},
			Fact{Label: "created", Value: k8sCreated},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case "K8S_JOB":
		return append(common,
			Fact{Label: "status", Value: jobStatus, Class: classJobStatus},
			Fact{Label: "ran", Value: jobRun},
			Fact{Label: "owner", Value: podWorkload},
			Fact{Label: "namespace", Value: factField("k8s.namespace.name")},
			Fact{Label: "cluster", Value: factField("k8s.cluster.name")},
			Fact{Label: "created", Value: k8sCreated},
		)
	case "K8S_CRONJOB":
		return append(common,
			Fact{Label: "schedule", Value: manifestValue("spec", "schedule")},
			Fact{Label: "suspended", Value: cronSuspended, Class: func(string) string { return "warn" }},
			Fact{Label: "last run", Value: manifestTime("status", "lastScheduleTime")},
			Fact{Label: "last success", Value: manifestTime("status", "lastSuccessfulTime")},
			Fact{Label: "namespace", Value: factField("k8s.namespace.name")},
			Fact{Label: "cluster", Value: factField("k8s.cluster.name")},
			Fact{Label: "created", Value: k8sCreated},
		)
	case "K8S_SERVICE":
		return append(common,
			Fact{Label: "type", Value: manifestValue("spec", "type")},
			Fact{Label: "cluster ip", Value: manifestValue("spec", "clusterIP")},
			Fact{Label: "ports", Value: servicePorts},
			Fact{Label: "external ip", Value: loadBalancerIP},
			Fact{Label: "namespace", Value: factField("k8s.namespace.name")},
			Fact{Label: "cluster", Value: factField("k8s.cluster.name")},
			Fact{Label: "created", Value: k8sCreated},
		)
	case "K8S_INGRESS":
		return append(common,
			Fact{Label: "hosts", Value: ingressHosts},
			Fact{Label: "external ip", Value: loadBalancerIP},
			Fact{Label: "namespace", Value: factField("k8s.namespace.name")},
			Fact{Label: "cluster", Value: factField("k8s.cluster.name")},
			Fact{Label: "created", Value: k8sCreated},
		)
	case "K8S_PERSISTENTVOLUMECLAIM", "K8S_PERSISTENTVOLUME":
		return append(common,
			Fact{Label: "phase", Value: manifestValue("status", "phase"), Class: classVolumePhase},
			Fact{Label: "capacity", Value: volumeCapacity},
			Fact{Label: "class", Value: manifestValue("spec", "storageClassName")},
			Fact{Label: "access", Value: manifestValue("spec", "accessModes")},
			Fact{Label: "volume", Value: manifestValue("spec", "volumeName")},
			Fact{Label: "claim", Value: volumeClaim},
			Fact{Label: "namespace", Value: factField("k8s.namespace.name")},
			Fact{Label: "cluster", Value: factField("k8s.cluster.name")},
			Fact{Label: "created", Value: k8sCreated},
		)
	case "K8S_NODE":
		return append(common,
			Fact{Label: "kubelet", Value: nodeKubelet},
			Fact{Label: "os", Value: nodeOS},
			Fact{Label: "capacity", Value: nodeCapacity},
			Fact{Label: "runtime", Value: manifestValue("status", "nodeInfo", "containerRuntimeVersion")},
			Fact{Label: "ip", Value: nodeAddresses},
			Fact{Label: "instance", Value: nodeLabelOrAlias("instance", "node.kubernetes.io/instance-type")},
			Fact{Label: "zone", Value: nodeLabelOrAlias("zone", "topology.kubernetes.io/zone")},
			Fact{Label: "cluster", Value: fieldsFirst("k8s.cluster.name", "cluster")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case "K8S_CLUSTER":
		return append(common,
			Fact{Label: "distribution", Value: factField("k8s.cluster.distribution")},
			Fact{Label: "version", Value: factField("k8s.cluster.version")},
			Fact{Label: "operator", Value: metadataField("operator_version")},
			Fact{Label: "activegate", Value: metadataField("activegate_version")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case "K8S_NAMESPACE":
		return append(common,
			Fact{Label: "cluster", Value: fieldsFirst("k8s.cluster.name", "cluster")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case "FRONTEND":
		return append(common,
			Fact{Label: "type", Value: factField("frontend.type")},
			Fact{Label: "instrumentation", Value: factField("dt.rum.instrumentation.id")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case "ONEAGENT":
		return append(common,
			Fact{Label: "version", Value: factField("dt.agent.module.version")},
			Fact{Label: "mode", Value: factField("dt.agent.monitoring_mode")},
			Fact{Label: "network zone", Value: factField("dt.network_zone.id")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case "DISK":
		return append(common,
			Fact{Label: "host", Value: factField("host.name")},
			Fact{Label: "cluster", Value: factField("k8s.cluster.name")},
			Fact{Label: "cloud", Value: cloudLocation},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case "NETWORK_INTERFACE":
		return append(common,
			Fact{Label: "host", Value: factField("host.name")},
			Fact{Label: "ip", Value: factField("ip")},
			Fact{Label: "mac", Value: factField("mac")},
			Fact{Label: "host group", Value: factField("dt.host_group.id")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case "EXT_NETWORK_DEVICE":
		return append(common,
			Fact{Label: "ip", Value: fieldsFirst("snmp.ip", "ip")},
			Fact{Label: "location", Value: factField("location")},
			Fact{Label: "device", Value: factField("device_type")},
			Fact{Label: "interfaces", Value: factField("interface_count")},
			Fact{Label: "model", Value: factField("description")},
			Fact{Label: "contact", Value: factField("contact")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case "EXT_NETWORK_INTERFACE":
		return append(common,
			Fact{Label: "status", Value: factField("operational_status"), Class: classIfStatus},
			Fact{Label: "admin", Value: factField("admin_status"), Class: classIfStatus},
			Fact{Label: "speed", Value: factField("speed")},
			Fact{Label: "mtu", Value: factField("mtu")},
			Fact{Label: "mac", Value: factField("mac")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case "BIZ_FLOW":
		return append(common,
			Fact{Label: "priority", Value: factField("bizflow.priority")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	}
	switch {
	case strings.HasPrefix(entityType, "DB_"):
		return append(common,
			Fact{Label: "system", Value: factField("db.system")},
			Fact{Label: "database", Value: factField("db.database.name")},
			Fact{Label: "instance", Value: factField("db.instance.name")},
			Fact{Label: "version", Value: factField("db.instance.version")},
			Fact{Label: "state", Value: factField("state"), Class: classDBState},
			Fact{Label: "host", Value: factField("db.connection_details.hostname")},
			Fact{Label: "port", Value: factField("db.connection_details.port")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case strings.HasPrefix(entityType, "AWS_"):
		return append(common,
			Fact{Label: "type", Value: factField("aws.resource.type")},
			Fact{Label: "state", Value: factField("aws.state"), Class: classCloudState},
			Fact{Label: "arn", Value: factField("aws.arn")},
			Fact{Label: "region", Value: fieldsFirst("aws.availability_zone", "aws.region")},
			Fact{Label: "account", Value: factField("aws.account.id")},
			Fact{Label: "acquisition", Value: nonOKStatus("cloud.acquisition.status"), Class: func(string) string { return "warn" }},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case strings.HasPrefix(entityType, "AZURE_"):
		return append(common,
			Fact{Label: "type", Value: factField("azure.resource.type")},
			Fact{Label: "resource group", Value: factField("azure.resource.group")},
			Fact{Label: "location", Value: factField("azure.location")},
			Fact{Label: "sku", Value: factField("azure.resource.sku.name")},
			Fact{Label: "subscription", Value: factField("azure.subscription")},
			Fact{Label: "acquisition", Value: nonOKStatus("cloud.acquisition.status"), Class: func(string) string { return "warn" }},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case strings.HasPrefix(entityType, "GCP_"):
		return append(common,
			Fact{Label: "type", Value: fieldsFirst("gcp.resource.type", "gcp.asset.type")},
			Fact{Label: "project", Value: factField("gcp.project.id")},
			Fact{Label: "zone", Value: fieldsFirst("gcp.zone", "gcp.region", "gcp.location")},
			Fact{Label: "ip", Value: gcpAddresses},
			Fact{Label: "acquisition", Value: nonOKStatus("cloud.acquisition.status"), Class: func(string) string { return "warn" }},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case strings.HasPrefix(entityType, "GENAI_"):
		// GenAI nodes are sparse (validated live: provider + name + lifetime);
		// their substance lives on the traces tab's genai lens.
		return append(common,
			Fact{Label: "provider", Value: factField("gen_ai.provider.name")},
			Fact{Label: "type", Value: factField("type")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case strings.HasPrefix(entityType, "K8S_"):
		// The K8s long tail (CRDs, DynaKubes, …): placement plus lifecycle.
		return append(common,
			Fact{Label: "workload", Value: podWorkload},
			Fact{Label: "namespace", Value: factField("k8s.namespace.name")},
			Fact{Label: "cluster", Value: factField("k8s.cluster.name")},
			Fact{Label: "created", Value: k8sCreated},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	}
	// Unknown types: identity plus whatever placement context the record
	// carries — most node kinds stamp at least one of these.
	return append(common,
		Fact{Label: "type", Value: factField("type")},
		Fact{Label: "host", Value: factField("host.name")},
		Fact{Label: "ip", Value: factField("ip")},
		Fact{Label: "cloud", Value: cloudLocation},
		Fact{Label: "cluster", Value: factField("k8s.cluster.name")},
		Fact{Label: "first seen", Value: lifetimeBound("start")},
		Fact{Label: "last seen", Value: lifetimeBound("end")},
	)
}

// --- k8s.object manifest access -------------------------------------------------

// k8sManifestKey memoizes the parsed manifest on the record — Value funcs run
// on every render frame and the manifest is kilobytes of JSON. The __ prefix
// keeps the synthetic key out of the inspector and the facet picker.
const k8sManifestKey = "__k8s.object.parsed"

// k8sManifest returns the record's parsed k8s.object manifest (empty map when
// absent or unparseable). The field arrives as a JSON string (validated live).
func k8sManifest(rec map[string]any) map[string]any {
	if cached, ok := rec[k8sManifestKey].(map[string]any); ok {
		return cached
	}
	m := map[string]any{}
	switch obj := rec["k8s.object"].(type) {
	case map[string]any:
		m = obj
	case string:
		_ = json.Unmarshal([]byte(obj), &m)
	}
	rec[k8sManifestKey] = m
	return m
}

// dig walks nested maps by key path, nil when any hop is missing.
func dig(m map[string]any, path ...string) any {
	var cur any = m
	for _, key := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[key]
	}
	return cur
}

// manifestValue renders the manifest value at a key path.
func manifestValue(path ...string) func(map[string]any) string {
	return func(rec map[string]any) string { return FormatValue(dig(k8sManifest(rec), path...)) }
}

// manifestTime renders a manifest timestamp at a key path as clock time.
func manifestTime(path ...string) func(map[string]any) string {
	return func(rec map[string]any) string {
		if iso, ok := dig(k8sManifest(rec), path...).(string); ok {
			return FormatTime(iso)
		}
		return ""
	}
}

// k8sCreated reads the creation time: the list-row alias first (the pods and
// workloads lists precompute created), then the manifest.
func k8sCreated(rec map[string]any) string {
	iso := Str(rec, "created")
	if iso == "" {
		iso, _ = dig(k8sManifest(rec), "metadata", "creationTimestamp").(string)
	}
	if iso == "" {
		return ""
	}
	return FormatTime(iso)
}

// podReadyFact renders container readiness: the pods list's ready/total
// aliases, else counted from the manifest's containerStatuses.
func podReadyFact(rec map[string]any) string {
	if v := podReady(rec); v != "" {
		return v
	}
	statuses, _ := dig(k8sManifest(rec), "status", "containerStatuses").([]any)
	if len(statuses) == 0 {
		return ""
	}
	ready := 0
	for _, s := range statuses {
		m, _ := s.(map[string]any)
		if b, _ := m["ready"].(bool); b {
			ready++
		}
	}
	return fmt.Sprintf("%d/%d", ready, len(statuses))
}

// podRestartsFact sums container restarts: list alias first, else manifest.
func podRestartsFact(rec map[string]any) string {
	if v := FormatValue(rec["restarts"]); v != "" {
		return v
	}
	statuses, _ := dig(k8sManifest(rec), "status", "containerStatuses").([]any)
	if len(statuses) == 0 {
		return ""
	}
	total := 0.0
	for _, s := range statuses {
		m, _ := s.(map[string]any)
		if f, ok := FloatValue(m["restartCount"]); ok {
			total += f
		}
	}
	return fmt.Sprintf("%.0f", total)
}

// workloadReady renders replica readiness ("2/3"): the workloads list's
// ready/desired aliases, else the manifest (deployments/statefulsets carry
// readyReplicas+replicas, daemonsets numberReady+desiredNumberScheduled).
func workloadReady(rec map[string]any) string {
	m := k8sManifest(rec)
	ready := firstNonEmpty(FormatValue(rec["ready"]),
		FormatValue(dig(m, "status", "readyReplicas")), FormatValue(dig(m, "status", "numberReady")))
	desired := firstNonEmpty(FormatValue(rec["desired"]),
		FormatValue(dig(m, "spec", "replicas")), FormatValue(dig(m, "status", "desiredNumberScheduled")))
	if desired == "" {
		return ""
	}
	if ready == "" {
		ready = "0"
	}
	return ready + "/" + desired
}

// replicasetOwner names the workload a replicaset belongs to ("" on the
// workload kinds themselves, where kind+name are the row's own identity).
func replicasetOwner(rec map[string]any) string {
	if Str(rec, "k8s.replicaset.name") == "" {
		return ""
	}
	return podWorkload(rec)
}

// workloadImages summarizes the pod template's container images.
func workloadImages(rec map[string]any) string {
	containers, _ := dig(k8sManifest(rec), "spec", "template", "spec", "containers").([]any)
	if len(containers) == 0 {
		return ""
	}
	first, _ := containers[0].(map[string]any)
	image := Str(first, "image")
	if rest := len(containers) - 1; rest > 0 {
		return fmt.Sprintf("%s +%d", image, rest)
	}
	return image
}

// jobStatus reads a K8s job's outcome from its manifest status counters.
func jobStatus(rec map[string]any) string {
	st, _ := dig(k8sManifest(rec), "status").(map[string]any)
	if st == nil {
		return ""
	}
	for _, probe := range []struct{ key, verdict string }{
		{"failed", "failed"}, {"active", "running"}, {"succeeded", "succeeded"},
	} {
		if f, ok := FloatValue(st[probe.key]); ok && f > 0 {
			return probe.verdict
		}
	}
	return ""
}

func classJobStatus(val string) string {
	switch val {
	case "failed":
		return "error"
	case "succeeded":
		return "ok"
	}
	return ""
}

// jobRun renders when a job ran and how long it took.
func jobRun(rec map[string]any) string {
	st, _ := dig(k8sManifest(rec), "status").(map[string]any)
	if st == nil {
		return ""
	}
	start, err := time.Parse(time.RFC3339Nano, Str(st, "startTime"))
	if err != nil {
		return ""
	}
	out := FormatTime(Str(st, "startTime"))
	if end, err := time.Parse(time.RFC3339Nano, Str(st, "completionTime")); err == nil {
		out += " · took " + FormatDuration(end.Sub(start))
	}
	return out
}

// cronSuspended surfaces a paused cron ("" while it runs normally).
func cronSuspended(rec map[string]any) string {
	if b, _ := dig(k8sManifest(rec), "spec", "suspend").(bool); b {
		return "yes"
	}
	return ""
}

// servicePorts summarizes a K8s service's port list ("https:8443, dns:53").
func servicePorts(rec map[string]any) string {
	ports, _ := dig(k8sManifest(rec), "spec", "ports").([]any)
	var parts []string
	for i, p := range ports {
		if i == 4 {
			parts = append(parts, fmt.Sprintf("+%d", len(ports)-4))
			break
		}
		m, _ := p.(map[string]any)
		port := FormatValue(m["port"])
		if name := Str(m, "name"); name != "" {
			port = name + ":" + port
		}
		parts = append(parts, port)
	}
	return strings.Join(parts, ", ")
}

// loadBalancerIP reads the assigned external address of a service/ingress.
func loadBalancerIP(rec map[string]any) string {
	ingress, _ := dig(k8sManifest(rec), "status", "loadBalancer", "ingress").([]any)
	var parts []string
	for _, e := range ingress {
		m, _ := e.(map[string]any)
		if v := firstNonEmpty(Str(m, "ip"), Str(m, "hostname")); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, ", ")
}

// ingressHosts lists the host rules of an ingress.
func ingressHosts(rec map[string]any) string {
	rules, _ := dig(k8sManifest(rec), "spec", "rules").([]any)
	var parts []string
	for _, r := range rules {
		m, _ := r.(map[string]any)
		if h := Str(m, "host"); h != "" {
			parts = append(parts, h)
		}
	}
	return strings.Join(parts, ", ")
}

func classVolumePhase(val string) string {
	switch val {
	case "Bound", "Available":
		return "ok"
	case "Pending":
		return "warn"
	case "Lost", "Failed":
		return "error"
	case "Released":
		return "dim"
	}
	return ""
}

// volumeCapacity reads a PVC/PV size: the bound capacity, the PV's declared
// capacity, or the claim's request.
func volumeCapacity(rec map[string]any) string {
	m := k8sManifest(rec)
	for _, path := range [][]string{
		{"status", "capacity", "storage"},
		{"spec", "capacity", "storage"},
		{"spec", "resources", "requests", "storage"},
	} {
		if s, ok := dig(m, path...).(string); ok && s != "" {
			return formatK8sQuantity(s)
		}
	}
	return ""
}

// volumeClaim names the PVC a persistent volume is bound to.
func volumeClaim(rec map[string]any) string {
	ref, _ := dig(k8sManifest(rec), "spec", "claimRef").(map[string]any)
	if ref == nil {
		return ""
	}
	return joinNonEmpty("/", Str(ref, "namespace"), Str(ref, "name"))
}

// nodeKubelet reads the kubelet version: the nodes list's alias, else nodeInfo.
func nodeKubelet(rec map[string]any) string {
	return firstNonEmpty(Str(rec, "version"),
		FormatValue(dig(k8sManifest(rec), "status", "nodeInfo", "kubeletVersion")))
}

// nodeOS names the node's OS image: the nodes list's alias, else nodeInfo.
func nodeOS(rec map[string]any) string {
	return firstNonEmpty(Str(rec, "os"),
		FormatValue(dig(k8sManifest(rec), "status", "nodeInfo", "osImage")))
}

// nodeCapacity summarizes a node's size ("4 cpu · 15.7 GiB").
func nodeCapacity(rec map[string]any) string {
	capacity, _ := dig(k8sManifest(rec), "status", "capacity").(map[string]any)
	if capacity == nil {
		if c := Str(rec, "cpus"); c != "" { // nodes-list alias
			return c + " cpu"
		}
		return ""
	}
	var parts []string
	if cpu := FormatValue(capacity["cpu"]); cpu != "" {
		parts = append(parts, cpu+" cpu")
	}
	if mem := Str(capacity, "memory"); mem != "" {
		parts = append(parts, formatK8sQuantity(mem))
	}
	return strings.Join(parts, " · ")
}

// nodeAddresses renders the node's internal (and external) addresses.
func nodeAddresses(rec map[string]any) string {
	addrs, _ := dig(k8sManifest(rec), "status", "addresses").([]any)
	var parts []string
	for _, a := range addrs {
		m, _ := a.(map[string]any)
		switch Str(m, "type") {
		case "InternalIP", "ExternalIP":
			parts = append(parts, Str(m, "address"))
		}
	}
	return strings.Join(parts, " · ")
}

// nodeLabelOrAlias probes the nodes list's alias, then the k8s label.
func nodeLabelOrAlias(alias, label string) func(map[string]any) string {
	fromLabel := labelTag(label)
	return func(rec map[string]any) string {
		return firstNonEmpty(Str(rec, alias), fromLabel(rec))
	}
}

// formatK8sQuantity renders a Kubernetes resource quantity ("16374532Ki",
// "10Mi") in IEC units; unknown shapes pass through.
func formatK8sQuantity(s string) string {
	for suffix, mult := range map[string]int64{
		"Ki": 1 << 10, "Mi": 1 << 20, "Gi": 1 << 30, "Ti": 1 << 40,
	} {
		if strings.HasSuffix(s, suffix) {
			if n, err := strconv.ParseInt(strings.TrimSuffix(s, suffix), 10, 64); err == nil {
				return FormatBytes(n * mult)
			}
		}
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 1024 {
		return FormatBytes(n)
	}
	return s
}

// metadataField reads a key from the dt.metadata map (K8s clusters carry
// operator/activegate versions there — a real map, validated live).
func metadataField(key string) func(map[string]any) string {
	return func(rec map[string]any) string {
		meta, _ := rec["dt.metadata"].(map[string]any)
		if meta == nil {
			return ""
		}
		return Str(meta, key)
	}
}

// --- shared fact helpers ---------------------------------------------------------

// podWorkload joins the workload kind and name ("deployment checkout"),
// reading the pods list's kind/workload aliases where the raw names are gone.
func podWorkload(rec map[string]any) string {
	return joinNonEmpty(" ",
		firstNonEmpty(Str(rec, "k8s.workload.kind"), Str(rec, "kind")),
		firstNonEmpty(Str(rec, "k8s.workload.name"), Str(rec, "workload")))
}

// processCommand reads the detected command line from process.metadata.
func processCommand(rec map[string]any) string {
	meta, _ := rec["process.metadata"].(map[string]any)
	if meta == nil {
		return ""
	}
	return Str(meta, "COMMAND_LINE_ARGS")
}

// processPod names the pod a containerized process runs in.
func processPod(rec map[string]any) string {
	meta, _ := rec["process.metadata"].(map[string]any)
	if meta == nil {
		return ""
	}
	return Str(meta, "KUBERNETES_FULL_POD_NAME")
}

// labelTag reads a key from the tags:k8s.labels map field.
func labelTag(key string) func(map[string]any) string {
	return func(rec map[string]any) string {
		labels, _ := rec["tags:k8s.labels"].(map[string]any)
		if labels == nil {
			return ""
		}
		return Str(labels, key)
	}
}

func factField(key string) func(map[string]any) string {
	return func(rec map[string]any) string { return FormatValue(rec[key]) }
}

// fieldsFirst probes several field names, first non-empty wins — the bridge
// between full node records and the summarized list-row aliases.
func fieldsFirst(keys ...string) func(map[string]any) string {
	return func(rec map[string]any) string {
		for _, k := range keys {
			if v := FormatValue(rec[k]); v != "" {
				return v
			}
		}
		return ""
	}
}

// trimmedField renders a field with a noisy enum prefix removed.
func trimmedField(key, prefix string) func(map[string]any) string {
	return func(rec map[string]any) string {
		return strings.TrimPrefix(Str(rec, key), prefix)
	}
}

// flagField surfaces a boolean field only when it is true.
func flagField(key string) func(map[string]any) string {
	return func(rec map[string]any) string {
		if b, _ := rec[key].(bool); b {
			return "yes"
		}
		return ""
	}
}

// nonOKStatus surfaces a status field only when it deviates from OK.
func nonOKStatus(key string) func(map[string]any) string {
	return func(rec map[string]any) string {
		if v := Str(rec, key); v != "" && v != "OK" {
			return v
		}
		return ""
	}
}

// cloudLocation composes provider and the most specific region field.
func cloudLocation(rec map[string]any) string {
	return joinNonEmpty(" ",
		Str(rec, "cloud.provider"),
		firstNonEmpty(
			Str(rec, "aws.availability_zone"),
			Str(rec, "aws.region"),
			Str(rec, "azure.location"),
			Str(rec, "gcp.zone"),
			Str(rec, "gcp.region")))
}

// gcpAddresses joins a GCP resource's private and public addresses.
func gcpAddresses(rec map[string]any) string {
	return joinNonEmpty(" · ",
		FormatValue(rec["private_ip_address"]),
		FormatValue(rec["public_ip_address"]))
}

// classDBState colors a database availability state.
func classDBState(val string) string {
	switch val {
	case "ONLINE":
		return "ok"
	case "":
		return ""
	}
	return "warn"
}

// classCloudState colors an AWS resource state.
func classCloudState(val string) string {
	switch val {
	case "running", "available", "active":
		return "ok"
	case "terminated", "deleted":
		return "dim"
	case "stopped", "stopping", "pending":
		return "warn"
	}
	return ""
}

// classIfStatus colors an SNMP interface status ("up(1)", "down(2)").
func classIfStatus(val string) string {
	switch {
	case strings.HasPrefix(val, "up"):
		return "ok"
	case strings.HasPrefix(val, "down"):
		return "error"
	}
	return ""
}

func lifetimeBound(bound string) func(map[string]any) string {
	return func(rec map[string]any) string {
		lifetime, _ := rec["lifetime"].(map[string]any)
		if lifetime == nil {
			return ""
		}
		return FormatTime(Str(lifetime, bound))
	}
}

func hostOS(rec map[string]any) string {
	return joinNonEmpty(" · ",
		strings.TrimPrefix(Str(rec, "os.type"), "OS_TYPE_"),
		Str(rec, "os.version"))
}

func hostCPU(rec map[string]any) string {
	logical, physical := Str(rec, "logical_cores"), Str(rec, "cores")
	if logical == "" {
		return physical
	}
	out := logical + " logical"
	if physical != "" {
		out += " / " + physical + " physical"
	}
	return out
}

func hostCloud(rec map[string]any) string {
	return cloudLocation(rec)
}

func hostInstance(rec map[string]any) string {
	sku := Str(rec, "sku")
	res := firstNonEmpty(Str(rec, "aws.resource.id"), Str(rec, "azure.resource.id"))
	if sku != "" && res != "" {
		return fmt.Sprintf("%s (%s)", sku, res)
	}
	return firstNonEmpty(sku, res)
}

func joinNonEmpty(sep string, parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}

func firstNonEmpty(parts ...string) string {
	for _, p := range parts {
		if p != "" {
			return p
		}
	}
	return ""
}
