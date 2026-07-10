package catalog

import (
	"fmt"
	"strings"
)

// Fact is one curated line on the entity detail page's key-facts panel: the
// handful of properties someone triaging wants without reading the full
// record.
type Fact struct {
	Label string
	Value func(rec map[string]any) string
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
	case rec["vulnerability.id"] != nil || Str(rec, "vulnerability.display_id") != "":
		// The vulns view summarizes into short aliases (title, level, score…);
		// raw security.events records keep the vulnerability.* names.
		return []string{"title", "display_id", "level", "score", "status", "tech",
			"cve", "url", "affected", "vulnerability.title", "vulnerability.risk.level",
			"vulnerability.risk.score", "vulnerability.resolution.status"}
	case rec["span.kind"] != nil || Str(rec, "span.name") != "":
		return []string{"span.name", "endpoint.name", "span.kind", "service.name",
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
// fields that only some records carry.
func KeyFacts(entityType string) []Fact {
	common := []Fact{{Label: "id", Value: factField("id")}}
	switch entityType {
	case "HOST":
		return append(common,
			Fact{Label: "os", Value: hostOS},
			Fact{Label: "cpu", Value: hostCPU},
			Fact{Label: "memory", Value: func(rec map[string]any) string { return FormatBytesStr(Str(rec, "memory")) }},
			Fact{Label: "ip", Value: factField("ip")},
			Fact{Label: "cloud", Value: hostCloud},
			Fact{Label: "instance", Value: hostInstance},
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
	case "K8S_POD":
		return append(common,
			Fact{Label: "phase", Value: factField("k8s.pod.phase")},
			Fact{Label: "namespace", Value: factField("k8s.namespace.name")},
			Fact{Label: "node", Value: factField("k8s.node.name")},
			Fact{Label: "workload", Value: podWorkload},
			Fact{Label: "cluster", Value: factField("k8s.cluster.name")},
			Fact{Label: "cost center", Value: factField("dt.cost.costcenter")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case "K8S_DEPLOYMENT", "K8S_STATEFULSET", "K8S_DAEMONSET":
		return append(common,
			Fact{Label: "kind", Value: factField("k8s.workload.kind")},
			Fact{Label: "namespace", Value: factField("k8s.namespace.name")},
			Fact{Label: "cluster", Value: factField("k8s.cluster.name")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case "K8S_NODE":
		return append(common,
			Fact{Label: "cluster", Value: factField("k8s.cluster.name")},
			Fact{Label: "instance", Value: labelTag("node.kubernetes.io/instance-type")},
			Fact{Label: "zone", Value: labelTag("topology.kubernetes.io/zone")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case "K8S_CLUSTER":
		return append(common,
			Fact{Label: "distribution", Value: factField("k8s.cluster.distribution")},
			Fact{Label: "version", Value: factField("k8s.cluster.version")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	case "K8S_NAMESPACE":
		return append(common,
			Fact{Label: "cluster", Value: factField("k8s.cluster.name")},
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
	case "DB_INSTANCE_POSTGRES", "DB_DATABASE_POSTGRES":
		return append(common,
			Fact{Label: "system", Value: factField("db.system")},
			Fact{Label: "database", Value: factField("db.database.name")},
			Fact{Label: "version", Value: factField("db.instance.version")},
			Fact{Label: "host", Value: factField("db.connection_details.hostname")},
			Fact{Label: "port", Value: factField("db.connection_details.port")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	}
	if strings.HasPrefix(entityType, "AWS_") {
		return append(common,
			Fact{Label: "type", Value: factField("type")},
			Fact{Label: "arn", Value: factField("aws.arn")},
			Fact{Label: "region", Value: factField("aws.region")},
			Fact{Label: "account", Value: factField("aws.account.id")},
			Fact{Label: "resource", Value: factField("aws.resource.type")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	}
	if strings.HasPrefix(entityType, "GENAI_") {
		// GenAI nodes are sparse (validated live: provider + name + lifetime);
		// their substance lives on the traces tab's genai lens.
		return append(common,
			Fact{Label: "provider", Value: factField("gen_ai.provider.name")},
			Fact{Label: "type", Value: factField("type")},
			Fact{Label: "first seen", Value: lifetimeBound("start")},
			Fact{Label: "last seen", Value: lifetimeBound("end")},
		)
	}
	return append(common,
		Fact{Label: "type", Value: factField("type")},
		Fact{Label: "first seen", Value: lifetimeBound("start")},
		Fact{Label: "last seen", Value: lifetimeBound("end")},
	)
}

// podWorkload joins the workload kind and name ("deployment checkout").
func podWorkload(rec map[string]any) string {
	return joinNonEmpty(" ", Str(rec, "k8s.workload.kind"), Str(rec, "k8s.workload.name"))
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
	return joinNonEmpty(" ",
		Str(rec, "cloud.provider"),
		firstNonEmpty(
			Str(rec, "aws.availability_zone"),
			Str(rec, "aws.region"),
			Str(rec, "azure.location"),
			Str(rec, "gcp.zone")))
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
