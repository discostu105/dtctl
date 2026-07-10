package catalog

import (
	"fmt"
	"strings"
)

// Process and container inventory views — the opinionated "next entity" a
// host or pod page drills into. Scoping is by the plain join fields the
// Smartscape nodes carry (validated live): processes and containers carry
// host.name; containers carry the k8s.* names; a process' pod name lives in
// process.metadata[KUBERNETES_FULL_POD_NAME].

// processScopeFilter narrows the process list to the entity drilled in from.
// Name-based matching mirrors k8sScopeFilter — server-side narrowing matters
// because the lists are limit-capped.
func processScopeFilter(e Entity) string {
	if e.Name == "" {
		return ""
	}
	switch e.Type {
	case "HOST", "K8S_NODE":
		// K8s node names equal the OneAgent host name (validated live on
		// EKS), so a node page scopes the same way a host page does.
		return fmt.Sprintf("host.name == %q", e.Name)
	case "K8S_POD":
		return fmt.Sprintf("process.metadata[`KUBERNETES_FULL_POD_NAME`] == %q", e.Name)
	}
	return ""
}

var processesSpec = &Spec{
	Name:         "processes",
	Aliases:      []string{"ps", "proc", "process"},
	Kind:         KindEntity,
	EntityScoped: true,
	Desc:         "Processes (Smartscape) — a host page's next hop",
	Query: func(s Scope) string {
		// No from: window — like the hosts list, the query reads the current
		// Smartscape state. A window makes smartscapeNodes serve stale field
		// values (validated live: container image names degrade to image ids
		// under from:).
		var b strings.Builder
		b.WriteString(`smartscapeNodes "PROCESS"`)
		if s.Entity != nil {
			if f := processScopeFilter(*s.Entity); f != "" {
				fmt.Fprintf(&b, "\n| filter %s", f)
			}
		}
		b.WriteString("\n| fieldsRemove references\n| sort name asc\n| limit 500")
		return b.String()
	},
	Columns: []Column{
		{Title: "NAME", Field: "name"},
		SparkColumn("CPU", "cpu", 8),
		SparkColumn("MEM", "mem", 8),
		{Title: "TECH", Width: 10, Value: processTech},
		{Title: "CONT", Width: 4, Value: processContainerized},
		{Title: "HOST", Field: "host.name", Width: 28},
		{Title: "SEEN", Width: 5, Right: true, Value: lifetimeAge},
	},
	Entity: nodeEntity("PROCESS"),
	// No 's': spans carry no process scope field (SpanScopable).
	Drills: map[string]string{"l": "logs", "m": "metrics", "p": "problems", "v": "events"},
	Enrich: &EnrichSpec{
		Key:    func(rec map[string]any) string { return Str(rec, "id") },
		By:     "dt.smartscape.process",
		Series: []string{"cpu", "mem"},
		Query: func(tf Timeframe, keys []string) string {
			return fmt.Sprintf(
				"timeseries { cpu = avg(dt.process.cpu.usage), mem = avg(dt.process.memory.usage) }, by:{dt.smartscape.process}, from:%s, interval: %s, filter: { in(dt.smartscape.process, {%s}) }",
				tf.DQL(), sparkInterval(tf), idList(keys))
		},
	},
}

// processTech names the process' first detected technology, lowercased
// ("go", "jvm", "containerd"). Runtime technologies win over OS-level ones.
func processTech(rec map[string]any) string {
	for _, key := range []string{"process.software_technologies", "process.software_technologies.os"} {
		arr, _ := rec[key].([]any)
		for _, e := range arr {
			m, _ := e.(map[string]any)
			if t := Str(m, "type"); t != "" {
				return strings.ToLower(t)
			}
		}
	}
	return ""
}

// processContainerized marks containerized processes with a dot.
func processContainerized(rec map[string]any) string {
	if b, _ := rec["process.containerized"].(bool); b {
		return "●"
	}
	return ""
}

// containerScopeFilter narrows the container list to the entity drilled in
// from: k8s.* names for K8s entities (containers carry them all), host.name
// for hosts.
func containerScopeFilter(e Entity) string {
	if e.Type == "HOST" {
		if e.Name == "" {
			return ""
		}
		return fmt.Sprintf("host.name == %q", e.Name)
	}
	return k8sScopeFilter(e)
}

var containersSpec = &Spec{
	Name:         "containers",
	Aliases:      []string{"ct", "cont", "container"},
	Kind:         KindEntity,
	EntityScoped: true,
	Desc:         "Containers (Smartscape) — a pod page's next hop",
	Query: func(s Scope) string {
		// No from: window — see processesSpec: a window degrades field
		// values (image names become image ids).
		var b strings.Builder
		b.WriteString(`smartscapeNodes "CONTAINER"`)
		if s.Entity != nil {
			if f := containerScopeFilter(*s.Entity); f != "" {
				fmt.Fprintf(&b, "\n| filter %s", f)
			}
		}
		b.WriteString("\n| fieldsRemove references\n| sort name asc\n| limit 500")
		return b.String()
	},
	Columns: []Column{
		{Title: "NAME", Field: "name"},
		SparkColumn("CPU", "cpu", 8),
		SparkColumn("MEM", "mem", 8),
		{Title: "IMAGE", Width: 30, Value: containerImage},
		{Title: "POD", Field: "k8s.pod.name", Width: 28},
		{Title: "SEEN", Width: 5, Right: true, Value: lifetimeAge},
	},
	Entity: nodeEntity("CONTAINER"),
	Drills: map[string]string{"l": "logs", "s": "traces", "m": "metrics", "p": "problems", "v": "events"},
	Enrich: &EnrichSpec{
		Key:    func(rec map[string]any) string { return Str(rec, "id") },
		By:     "dt.smartscape.container",
		Series: []string{"cpu", "mem"},
		Query: func(tf Timeframe, keys []string) string {
			return fmt.Sprintf(
				"timeseries { cpu = avg(dt.kubernetes.container.cpu_usage), mem = avg(dt.kubernetes.container.memory_working_set) }, by:{dt.smartscape.container}, from:%s, interval: %s, filter: { in(dt.smartscape.container, {%s}) }",
				tf.DQL(), sparkInterval(tf), idList(keys))
		},
	},
}

// containerImage renders the image compactly: the last path segment plus the
// version tag ("opentelemetry-collector-contrib:0.119.0") — registries
// dominate the raw name and say nothing.
func containerImage(rec map[string]any) string {
	img := Str(rec, "container.image.name")
	if i := strings.LastIndex(img, "/"); i >= 0 {
		img = img[i+1:]
	}
	if v := Str(rec, "container.image.version"); v != "" && img != "" {
		img += ":" + v
	}
	return img
}
