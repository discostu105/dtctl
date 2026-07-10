package catalog

import (
	"fmt"
	"strings"
	"time"
)

// Kubernetes entity views. All queries validated live against a tenant:
// smartscapeNodes is a starting command (no fetch), k8s.object is a JSON
// string parsed with the DPL JSON matcher, and the numbers it yields are
// strings (toLong() where math is needed). See docs/dev/TUI_DESIGN.md.

// k8sScopeFilter narrows a K8s list to the entity the user drilled in from:
// pods of a namespace, pods on a node, pods of a workload, anything of a
// cluster. Matching is by name fields (plain strings on the node records).
func k8sScopeFilter(e Entity) string {
	name := e.Name
	switch e.Type {
	case "K8S_POD":
		// Server-side narrowing matters: the pod list is limit-capped, so a
		// client-side filter could miss pods cut from the fetched page.
		return fmt.Sprintf("k8s.pod.name == %q", name)
	case "K8S_NAMESPACE":
		return fmt.Sprintf("k8s.namespace.name == %q", name)
	case "K8S_NODE":
		return fmt.Sprintf("k8s.node.name == %q", name)
	case "K8S_CLUSTER":
		return fmt.Sprintf("k8s.cluster.name == %q", name)
	case "K8S_DEPLOYMENT", "K8S_STATEFULSET", "K8S_DAEMONSET":
		kind := strings.ToLower(strings.TrimPrefix(e.Type, "K8S_"))
		return fmt.Sprintf("k8s.workload.kind == %q and k8s.workload.name == %q", kind, name)
	}
	return ""
}

var podsSpec = &Spec{
	Name:         "pods",
	Aliases:      []string{"po", "pod"},
	Kind:         KindEntity,
	EntityScoped: true,
	Desc:         "Kubernetes pods (ready, restarts, phase)",
	Query: func(s Scope) string {
		var b strings.Builder
		fmt.Fprintf(&b, "smartscapeNodes \"K8S_POD\", from:%s", s.Timeframe.DQL())
		if s.Entity != nil {
			if f := k8sScopeFilter(*s.Entity); f != "" {
				fmt.Fprintf(&b, "\n| filter %s", f)
			}
		}
		b.WriteString(`
| parse k8s.object, "JSON:obj"
| expand cs = obj[status][containerStatuses]
| summarize { name = takeFirst(name), phase = takeFirst(k8s.pod.phase), namespace = takeFirst(k8s.namespace.name), node = takeFirst(k8s.node.name), workload = takeFirst(k8s.workload.name), kind = takeFirst(k8s.workload.kind), ready = countIf(cs[ready] == true), total = count(), restarts = sum(toLong(cs[restartCount])), created = takeFirst(toTimestamp(obj[metadata][creationTimestamp])) }, by:{id}
| sort phase asc, namespace asc, name asc
| limit 800`)
		return b.String()
	},
	Columns: []Column{
		{Title: "NAMESPACE", Field: "namespace", Width: 20},
		{Title: "NAME", Field: "name"},
		{Title: "READY", Width: 5, Right: true, Value: podReady, Class: classPodReady,
			Sort: func(rec map[string]any) any { return Str(rec, "ready") }},
		{Title: "RST", Field: "restarts", Width: 4, Right: true, Class: classNonzeroWarn},
		{Title: "PHASE", Field: "phase", Width: 9, Class: classPodPhase},
		SparkColumn("CPU", "cpu", 8),
		{Title: "NODE", Field: "node", Width: 24},
		{Title: "AGE", Width: 5, Right: true, Value: func(rec map[string]any) string { return Age(Str(rec, "created")) },
			Sort: func(rec map[string]any) any { return Str(rec, "created") }},
	},
	Entity: func(rec map[string]any) *Entity {
		id := Str(rec, "id")
		if id == "" {
			return nil
		}
		return &Entity{ID: id, Name: Str(rec, "name"), Type: "K8S_POD"}
	},
	Drills: map[string]string{"l": "logs", "s": "traces", "m": "metrics", "p": "problems", "v": "events"},
	Enrich: &EnrichSpec{
		Key:    func(rec map[string]any) string { return Str(rec, "name") },
		By:     "k8s.pod.name",
		Series: []string{"cpu"},
		Query: func(tf Timeframe, keys []string) string {
			return fmt.Sprintf(
				"timeseries cpu = avg(dt.kubernetes.container.cpu_usage), by:{k8s.pod.name}, from:%s, interval: %s, filter: { in(k8s.pod.name, {%s}) }",
				tf.DQL(), sparkInterval(tf), quoteList(keys))
		},
	},
}

func podReady(rec map[string]any) string {
	ready, total := Str(rec, "ready"), Str(rec, "total")
	if total == "" {
		return ""
	}
	if ready == "" {
		ready = "0"
	}
	return ready + "/" + total
}

func classPodReady(val string) string {
	parts := strings.SplitN(val, "/", 2)
	if len(parts) != 2 {
		return ""
	}
	if parts[0] != parts[1] {
		return "warn"
	}
	return "ok"
}

func classPodPhase(val string) string {
	switch val {
	case "Running":
		return "ok"
	case "Pending":
		return "warn"
	case "Failed":
		return "error"
	case "Succeeded":
		return "dim"
	}
	return ""
}

// classNonzero builds a Class func that highlights non-zero counts with the
// given severity (restarts warn, RUM session errors error) and dims zeros.
func classNonzero(severity string) func(string) string {
	return func(val string) string {
		if val == "" || val == "0" {
			return "dim"
		}
		return severity
	}
}

var classNonzeroWarn = classNonzero("warn")

var workloadsSpec = &Spec{
	Name:         "workloads",
	Aliases:      []string{"wl", "dep", "deployments"},
	Kind:         KindEntity,
	EntityScoped: true,
	Desc:         "Kubernetes workloads (deployments, statefulsets, daemonsets)",
	Query: func(s Scope) string {
		var b strings.Builder
		b.WriteString(`smartscapeNodes "K8S_DEPLOYMENT", "K8S_STATEFULSET", "K8S_DAEMONSET"`)
		if s.Entity != nil {
			if f := k8sScopeFilter(*s.Entity); f != "" {
				fmt.Fprintf(&b, "\n| filter %s", f)
			}
		}
		b.WriteString(`
| parse k8s.object, "JSON:obj"
| fieldsAdd ready = coalesce(obj[status][readyReplicas], obj[status][numberReady]), desired = coalesce(obj[spec][replicas], obj[status][desiredNumberScheduled]), created = toTimestamp(obj[metadata][creationTimestamp])
| fields id, name, type, namespace = k8s.namespace.name, kind = k8s.workload.kind, ready, desired, created
| sort namespace asc, name asc
| limit 500`)
		return b.String()
	},
	Columns: []Column{
		{Title: "NAMESPACE", Field: "namespace", Width: 20},
		{Title: "NAME", Field: "name"},
		{Title: "KIND", Field: "kind", Width: 11},
		{Title: "READY", Width: 6, Right: true, Value: workloadReady, Class: classPodReady,
			Sort: func(rec map[string]any) any { return Str(rec, "ready") }},
		SparkColumn("CPU", "cpu", 8),
		{Title: "AGE", Width: 5, Right: true, Value: func(rec map[string]any) string { return Age(Str(rec, "created")) },
			Sort: func(rec map[string]any) any { return Str(rec, "created") }},
	},
	Entity:      nodeEntity(""),
	EnterTarget: "pods",
	Drills:      map[string]string{"l": "logs", "s": "traces", "m": "metrics", "p": "problems", "v": "events"},
	Enrich: &EnrichSpec{
		Key:    func(rec map[string]any) string { return Str(rec, "name") },
		By:     "k8s.workload.name",
		Series: []string{"cpu"},
		Query: func(tf Timeframe, keys []string) string {
			return fmt.Sprintf(
				"timeseries cpu = sum(dt.kubernetes.container.cpu_usage), by:{k8s.workload.name}, from:%s, interval: %s, filter: { in(k8s.workload.name, {%s}) }",
				tf.DQL(), sparkInterval(tf), quoteList(keys))
		},
	},
}

func workloadReady(rec map[string]any) string {
	ready, desired := Str(rec, "ready"), Str(rec, "desired")
	if desired == "" {
		return ""
	}
	if ready == "" {
		ready = "0" // Kubernetes omits readyReplicas when zero are ready
	}
	return ready + "/" + desired
}

var namespacesSpec = &Spec{
	Name:         "namespaces",
	Aliases:      []string{"ns", "namespace"},
	Kind:         KindEntity,
	EntityScoped: true,
	Desc:         "Kubernetes namespaces",
	Query: func(s Scope) string {
		var b strings.Builder
		b.WriteString(`smartscapeNodes "K8S_NAMESPACE"`)
		if s.Entity != nil && s.Entity.Type == "K8S_CLUSTER" {
			fmt.Fprintf(&b, "\n| filter k8s.cluster.name == %q", s.Entity.Name)
		}
		b.WriteString("\n| fields id, name, type, cluster = k8s.cluster.name, lifetime\n| sort name asc\n| limit 300")
		return b.String()
	},
	Columns: []Column{
		{Title: "NAME", Field: "name"},
		{Title: "CLUSTER", Field: "cluster", Width: 16},
		{Title: "PODS", Width: 5, Right: true, Value: enrichNum("pods"), Sort: enrichSort("pods")},
		{Title: "RUNNING", Width: 7, Right: true, Value: enrichNum("running"), Sort: enrichSort("running")},
		{Title: "SEEN", Width: 5, Right: true, Value: lifetimeAge},
	},
	Entity:      nodeEntity("K8S_NAMESPACE"),
	EnterTarget: "workloads",
	Drills:      map[string]string{"l": "logs", "s": "traces", "p": "problems", "v": "events"},
	Enrich: &EnrichSpec{
		Key:    func(rec map[string]any) string { return Str(rec, "name") },
		By:     "k8s.namespace.name",
		Series: []string{"pods", "running"},
		Query: func(tf Timeframe, keys []string) string {
			return fmt.Sprintf(
				"smartscapeNodes \"K8S_POD\", from:%s\n| filter in(k8s.namespace.name, {%s})\n| summarize pods = count(), running = countIf(k8s.pod.phase == \"Running\"), by:{k8s.namespace.name}",
				tf.DQL(), quoteList(keys))
		},
	},
}

// enrichNum renders a scalar enrichment value (counts joined from a second
// query rather than a timeseries).
func enrichNum(alias string) func(rec map[string]any) string {
	key := EnrichKey(alias)
	return func(rec map[string]any) string { return FormatValue(rec[key]) }
}

func enrichSort(alias string) func(rec map[string]any) any {
	key := EnrichKey(alias)
	return func(rec map[string]any) any { return rec[key] }
}

var nodesSpec = &Spec{
	Name:         "nodes",
	Aliases:      []string{"no", "node"},
	Kind:         KindEntity,
	EntityScoped: true,
	Desc:         "Kubernetes nodes",
	Query: func(s Scope) string {
		var b strings.Builder
		b.WriteString(`smartscapeNodes "K8S_NODE"`)
		if s.Entity != nil && s.Entity.Type == "K8S_CLUSTER" {
			fmt.Fprintf(&b, "\n| filter k8s.cluster.name == %q", s.Entity.Name)
		}
		b.WriteString(`
| parse k8s.object, "JSON:obj"
| fields id, name, type, cluster = k8s.cluster.name, version = obj[status][nodeInfo][kubeletVersion], os = obj[status][nodeInfo][osImage], cpus = obj[status][capacity][cpu], instance = ` + "`tags:k8s.labels`[`node.kubernetes.io/instance-type`], zone = `tags:k8s.labels`[`topology.kubernetes.io/zone`]" + `, lifetime
| sort name asc
| limit 300`)
		return b.String()
	},
	Columns: []Column{
		{Title: "NAME", Field: "name"},
		{Title: "INSTANCE", Field: "instance", Width: 11},
		{Title: "ZONE", Field: "zone", Width: 10},
		{Title: "CPUS", Field: "cpus", Width: 4, Right: true},
		{Title: "PODS", Width: 5, Right: true, Value: enrichNum("pods"), Sort: enrichSort("pods")},
		{Title: "VERSION", Field: "version", Width: 18},
		{Title: "SEEN", Width: 5, Right: true, Value: lifetimeAge},
	},
	Entity:      nodeEntity("K8S_NODE"),
	EnterTarget: "pods",
	Drills:      map[string]string{"l": "logs", "s": "traces", "p": "problems", "v": "events"},
	Enrich: &EnrichSpec{
		Key:    func(rec map[string]any) string { return Str(rec, "name") },
		By:     "k8s.node.name",
		Series: []string{"pods"},
		Query: func(tf Timeframe, keys []string) string {
			return fmt.Sprintf(
				"smartscapeNodes \"K8S_POD\", from:%s\n| filter in(k8s.node.name, {%s}) and k8s.pod.phase == \"Running\"\n| summarize pods = count(), by:{k8s.node.name}",
				tf.DQL(), quoteList(keys))
		},
	},
}

var clustersSpec = &Spec{
	Name:    "clusters",
	Aliases: []string{"cl", "cluster"},
	Kind:    KindEntity,
	Desc:    "Kubernetes clusters",
	Query: func(s Scope) string {
		return `smartscapeNodes "K8S_CLUSTER"
| fields id, name, type, distribution = k8s.cluster.distribution, version = k8s.cluster.version, lifetime
| sort name asc
| limit 100`
	},
	Columns: []Column{
		{Title: "NAME", Field: "name"},
		{Title: "DISTRIBUTION", Field: "distribution", Width: 12},
		{Title: "VERSION", Field: "version", Width: 20},
		{Title: "SEEN", Width: 5, Right: true, Value: lifetimeAge},
	},
	Entity:      nodeEntity("K8S_CLUSTER"),
	EnterTarget: "nodes",
	Drills:      map[string]string{"l": "logs", "s": "traces", "p": "problems", "v": "events"},
}

// quoteList renders string values as a DQL {"a", "b"} set literal body.
func quoteList(keys []string) string {
	quoted := make([]string, len(keys))
	for i, k := range keys {
		quoted[i] = fmt.Sprintf("%q", k)
	}
	return strings.Join(quoted, ", ")
}

// sparkInterval picks a bucket size that yields ~25-45 points per window —
// enough shape for an 8-cell braille sparkline without heavy payloads.
func sparkInterval(tf Timeframe) string {
	switch {
	case tf.Dur <= 45*time.Minute:
		return "1m"
	case tf.Dur <= 3*time.Hour:
		return "5m"
	case tf.Dur <= 30*time.Hour:
		return "30m"
	default:
		return "4h"
	}
}
