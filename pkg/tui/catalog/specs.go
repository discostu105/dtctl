package catalog

import (
	"fmt"
	"strings"
)

// All queries below were validated live against a tenant (field names, filter
// syntax, and toSmartscapeId usage); see docs/dev/TUI_DESIGN.md "Detail Pages"
// for the exploration notes.

var problemsSpec = &Spec{
	Name:    "problems",
	Aliases: []string{"pb", "problem"},
	Kind:    KindSignal,
	Desc:    "Davis problems — the investigation entry point",
	Query: func(s Scope) string {
		var b strings.Builder
		fmt.Fprintf(&b, "fetch dt.davis.problems, from:%s\n| filter not(dt.davis.is_duplicate)", s.Timeframe.DQL())
		if s.Entity != nil {
			fmt.Fprintf(&b, "\n| filter %s", ProblemFilter(*s.Entity))
		}
		b.WriteString("\n| sort event.start desc\n| limit 200")
		return b.String()
	},
	Columns: []Column{
		{Title: "ID", Field: "display_id", Width: 9},
		{Title: "STATUS", Field: "event.status", Width: 6, Class: classProblemStatus},
		{Title: "CATEGORY", Field: "event.category", Width: 20},
		{Title: "TITLE", Field: "event.name"},
		{Title: "AFFECTED", Width: 26, Value: problemAffected},
		{Title: "AGE", Width: 5, Value: func(rec map[string]any) string { return Age(Str(rec, "event.start")) }},
	},
	Entity: problemEntity,
	Drills: map[string]string{"l": "logs", "s": "traces", "v": "events", "m": "metrics"},
}

func classProblemStatus(val string) string {
	switch val {
	case "ACTIVE":
		return "error"
	case "CLOSED":
		return "dim"
	}
	return ""
}

// problemAffected summarizes the affected entities column.
func problemAffected(rec map[string]any) string {
	names, _ := rec["affected_entity_names"].([]any)
	if len(names) == 0 {
		return ""
	}
	first := FormatValue(names[0])
	if len(names) > 1 {
		return fmt.Sprintf("%s +%d", first, len(names)-1)
	}
	return first
}

// problemEntity returns the first affected Smartscape entity, which supplies
// the scope for l/v/m drills from a problem row.
func problemEntity(rec map[string]any) *Entity {
	affected, _ := rec["smartscape.affected_entities"].([]any)
	if len(affected) == 0 {
		return nil
	}
	first, _ := affected[0].(map[string]any)
	if first == nil {
		return nil
	}
	e := &Entity{ID: Str(first, "id"), Name: Str(first, "name"), Type: Str(first, "type")}
	if e.ID == "" {
		return nil
	}
	return e
}

var servicesSpec = &Spec{
	Name:    "services",
	Aliases: []string{"svc", "service"},
	Kind:    KindEntity,
	Desc:    "Services (Smartscape)",
	Query: func(s Scope) string {
		return "smartscapeNodes \"SERVICE\"\n| fieldsRemove references\n| sort name asc\n| limit 500"
	},
	Columns: []Column{
		{Title: "NAME", Field: "name"},
		SparkColumn("REQUESTS", "req", 10),
		SparkColumn("FAILED", "fail", 10),
		{Title: "ID", Field: "id", Width: 25},
		{Title: "SEEN", Width: 5, Right: true, Value: lifetimeAge},
	},
	Entity: nodeEntity("SERVICE"),
	Drills: map[string]string{"l": "logs", "s": "traces", "p": "problems", "v": "events", "m": "metrics"},
	Enrich: &EnrichSpec{
		Key:    func(rec map[string]any) string { return Str(rec, "id") },
		By:     "dt.smartscape.service",
		Series: []string{"req", "fail"},
		Query: func(tf Timeframe, keys []string) string {
			return fmt.Sprintf(
				"timeseries { req = sum(dt.service.request.count), fail = sum(dt.service.request.failure_count) }, by:{dt.smartscape.service}, from:%s, interval: %s, filter: { in(dt.smartscape.service, {%s}) }",
				tf.DQL(), sparkInterval(tf), idList(keys))
		},
	},
}

var hostsSpec = &Spec{
	Name:    "hosts",
	Aliases: []string{"ho", "host"},
	Kind:    KindEntity,
	Desc:    "Hosts (Smartscape)",
	Query: func(s Scope) string {
		return "smartscapeNodes \"HOST\"\n| fieldsRemove k8s.object, aws.object, azure.object, references\n| sort name asc\n| limit 500"
	},
	Columns: []Column{
		{Title: "NAME", Field: "name"},
		{Title: "OS", Width: 7, Value: func(rec map[string]any) string {
			return strings.TrimPrefix(Str(rec, "os.type"), "OS_TYPE_")
		}},
		{Title: "CPUS", Width: 4, Right: true, Field: "logical_cores"},
		SparkColumn("CPU", "cpu", 8),
		SparkColumn("MEM", "mem", 8),
		{Title: "MEMORY", Width: 9, Right: true, Value: func(rec map[string]any) string {
			return FormatBytesStr(Str(rec, "memory"))
		}, Sort: func(rec map[string]any) any { return Str(rec, "memory") }},
		{Title: "IP", Width: 15, Value: func(rec map[string]any) string { return StrFirst(rec, "ip") }},
		{Title: "SKU", Width: 12, Field: "sku"},
		{Title: "SEEN", Width: 5, Right: true, Value: lifetimeAge},
	},
	Entity: nodeEntity("HOST"),
	Drills: map[string]string{"l": "logs", "p": "problems", "v": "events", "m": "metrics"},
	Enrich: &EnrichSpec{
		Key:    func(rec map[string]any) string { return Str(rec, "id") },
		By:     "dt.smartscape.host",
		Series: []string{"cpu", "mem"},
		Query: func(tf Timeframe, keys []string) string {
			return fmt.Sprintf(
				"timeseries { cpu = avg(dt.host.cpu.usage), mem = avg(dt.host.memory.usage) }, by:{dt.smartscape.host}, from:%s, interval: %s, filter: { in(dt.smartscape.host, {%s}) }",
				tf.DQL(), sparkInterval(tf), idList(keys))
		},
	},
}

// nodeEntity extracts the entity from a smartscapeNodes record.
func nodeEntity(fallbackType string) func(rec map[string]any) *Entity {
	return func(rec map[string]any) *Entity {
		id := Str(rec, "id")
		if id == "" {
			return nil
		}
		typ := Str(rec, "type")
		if typ == "" {
			typ = fallbackType
		}
		return &Entity{ID: id, Name: Str(rec, "name"), Type: typ}
	}
}

// lifetimeAge renders how long a Smartscape node has been known.
func lifetimeAge(rec map[string]any) string {
	lifetime, _ := rec["lifetime"].(map[string]any)
	if lifetime == nil {
		return ""
	}
	return Age(Str(lifetime, "start"))
}

var logsSpec = &Spec{
	Name:    "logs",
	Aliases: []string{"log"},
	Kind:    KindSignal,
	Desc:    "Log records",
	Query: func(s Scope) string {
		var b strings.Builder
		fmt.Fprintf(&b, "fetch logs, from:%s", s.Timeframe.DQL())
		if s.Entity != nil {
			fmt.Fprintf(&b, "\n| filter %s", SignalFilter(*s.Entity))
		}
		if s.TraceID != "" {
			// Log trace_id is a plain string field (validated live) — no
			// toUid() needed, unlike span trace.id.
			fmt.Fprintf(&b, "\n| filter trace_id == %q", s.TraceID)
		}
		b.WriteString("\n| sort timestamp desc\n| limit 300")
		return b.String()
	},
	Columns: []Column{
		{Title: "TIME", Width: 12, Value: func(rec map[string]any) string { return FormatTime(Str(rec, "timestamp")) },
			Sort: func(rec map[string]any) any { return Str(rec, "timestamp") }},
		{Title: "LEVEL", Width: 5, Value: logLevel, Class: classLogLevel},
		{Title: "SOURCE", Width: 24, Value: logSource},
		{Title: "CONTENT", Field: "content"},
	},
	Entity: signalSourceEntity,
	Trace:  func(rec map[string]any) string { return Str(rec, "trace_id") },
	Drills: map[string]string{"p": "problems", "s": "trace"},
}

// logLevel prefers loglevel over the coarser status field.
func logLevel(rec map[string]any) string {
	if l := Str(rec, "loglevel"); l != "" && l != "NONE" {
		return l
	}
	return Str(rec, "status")
}

func classLogLevel(val string) string {
	switch strings.ToUpper(val) {
	case "ERROR", "SEVERE", "CRITICAL", "FATAL", "EMERGENCY", "ALERT":
		return "error"
	case "WARN", "WARNING":
		return "warn"
	case "INFO", "NOTICE":
		return "ok"
	case "NONE", "DEBUG", "TRACE":
		return "dim"
	}
	return ""
}

// logSource labels the row with the most specific origin available.
func logSource(rec map[string]any) string {
	for _, key := range []string{"k8s.pod.name", "dt.process_group.detected_name", "log.source", "host.name"} {
		if v := Str(rec, key); v != "" {
			return v
		}
	}
	return ""
}

// signalSourceEntity extracts the record's source entity (for future drills).
func signalSourceEntity(rec map[string]any) *Entity {
	id := Str(rec, "dt.smartscape_source.id")
	if id == "" {
		return nil
	}
	return &Entity{ID: id, Name: logSource(rec), Type: Str(rec, "dt.smartscape_source.type")}
}

var eventsSpec = &Spec{
	Name:    "events",
	Aliases: []string{"ev", "event"},
	Kind:    KindSignal,
	Desc:    "Events (Davis, deployments, K8s)",
	Query: func(s Scope) string {
		var b strings.Builder
		fmt.Fprintf(&b, "fetch events, from:%s", s.Timeframe.DQL())
		if s.Entity != nil {
			fmt.Fprintf(&b, "\n| filter %s", SignalFilter(*s.Entity))
		}
		b.WriteString("\n| sort timestamp desc\n| limit 300")
		return b.String()
	},
	Columns: []Column{
		{Title: "TIME", Width: 12, Value: func(rec map[string]any) string { return FormatTime(Str(rec, "timestamp")) }},
		{Title: "KIND", Field: "event.kind", Width: 12},
		{Title: "TYPE", Field: "event.type", Width: 28},
		{Title: "NAME", Field: "event.name"},
	},
	Entity: signalSourceEntity,
	Drills: map[string]string{"p": "problems"},
}

// --- canned metrics (the 'm' drill) ------------------------------------------

// MetricSeries describes one chart on the metrics view.
type MetricSeries struct {
	Alias string // field name in the timeseries result
	Title string
	Unit  string
}

// MetricsSpec is the canned per-entity-type metrics page (Phase 1: charts for
// hosts and services; the full metric browser is a later phase).
type MetricsSpec struct {
	Series []MetricSeries
	Query  func(e Entity, tf Timeframe) string
}

// MetricsFor returns the canned metrics spec for an entity type, or nil when
// the type has no curated charts yet.
func MetricsFor(entityType string) *MetricsSpec {
	switch entityType {
	case "HOST":
		return &MetricsSpec{
			Series: []MetricSeries{
				{Alias: "cpu", Title: "CPU usage", Unit: "%"},
				{Alias: "mem", Title: "Memory usage", Unit: "%"},
				{Alias: "disk", Title: "Disk used", Unit: "%"},
			},
			Query: func(e Entity, tf Timeframe) string {
				return fmt.Sprintf(
					"timeseries { cpu = avg(dt.host.cpu.usage), mem = avg(dt.host.memory.usage), disk = avg(dt.host.disk.used.percent) }, from:%s, filter: { dt.smartscape.host == toSmartscapeId(%q) }",
					tf.DQL(), e.ID)
			},
		}
	case "SERVICE":
		return &MetricsSpec{
			Series: []MetricSeries{
				{Alias: "req", Title: "Requests", Unit: ""},
				{Alias: "fail", Title: "Failed requests", Unit: ""},
				{Alias: "rt", Title: "Response time", Unit: "ms"},
			},
			Query: func(e Entity, tf Timeframe) string {
				return fmt.Sprintf(
					"timeseries { req = sum(dt.service.request.count), fail = sum(dt.service.request.failure_count), rt = avg(dt.service.request.response_time) }, from:%s, filter: { dt.smartscape.service == toSmartscapeId(%q) }",
					tf.DQL(), e.ID)
			},
		}
	case "K8S_POD":
		return &MetricsSpec{
			Series: []MetricSeries{
				{Alias: "cpu", Title: "CPU usage (vs limit)", Unit: "mCores"},
				{Alias: "cpu_limit", Title: "CPU limit", Unit: "mCores"},
				{Alias: "mem", Title: "Memory working set", Unit: "B"},
				{Alias: "mem_limit", Title: "Memory limit", Unit: "B"},
				{Alias: "throttled", Title: "CPU throttled", Unit: "mCores"},
			},
			Query: func(e Entity, tf Timeframe) string {
				return fmt.Sprintf(
					"timeseries { cpu = avg(dt.kubernetes.container.cpu_usage), cpu_limit = avg(dt.kubernetes.container.limits_cpu), mem = avg(dt.kubernetes.container.memory_working_set), mem_limit = avg(dt.kubernetes.container.limits_memory), throttled = avg(dt.kubernetes.container.cpu_throttled) }, from:%s, filter: { dt.smartscape.k8s_pod == toSmartscapeId(%q) }",
					tf.DQL(), e.ID)
			},
		}
	case "K8S_DEPLOYMENT", "K8S_STATEFULSET", "K8S_DAEMONSET":
		field := "dt.smartscape." + strings.ToLower(entityType)
		return &MetricsSpec{
			Series: []MetricSeries{
				{Alias: "desired", Title: "Desired pods", Unit: ""},
			},
			Query: func(e Entity, tf Timeframe) string {
				return fmt.Sprintf(
					"timeseries desired = max(dt.kubernetes.workload.pods_desired), from:%s, filter: { %s == toSmartscapeId(%q) }",
					tf.DQL(), field, e.ID)
			},
		}
	case "K8S_NODE":
		return &MetricsSpec{
			Series: []MetricSeries{
				{Alias: "cpu_alloc", Title: "CPU allocatable", Unit: "mCores"},
				{Alias: "mem_alloc", Title: "Memory allocatable", Unit: "B"},
				{Alias: "pods_alloc", Title: "Pods allocatable", Unit: ""},
			},
			Query: func(e Entity, tf Timeframe) string {
				return fmt.Sprintf(
					"timeseries { cpu_alloc = max(dt.kubernetes.node.cpu_allocatable), mem_alloc = max(dt.kubernetes.node.memory_allocatable), pods_alloc = max(dt.kubernetes.node.pods_allocatable) }, from:%s, filter: { dt.smartscape.k8s_node == toSmartscapeId(%q) }",
					tf.DQL(), e.ID)
			},
		}
	case "FRONTEND":
		return &MetricsSpec{
			Series: []MetricSeries{
				{Alias: "req", Title: "Requests", Unit: ""},
				{Alias: "err", Title: "Errors", Unit: ""},
				{Alias: "lcp", Title: "Largest contentful paint", Unit: "ms"},
				{Alias: "inp", Title: "Interaction to next paint", Unit: "ms"},
			},
			Query: func(e Entity, tf Timeframe) string {
				return fmt.Sprintf(
					"timeseries { req = sum(dt.frontend.request.count), err = sum(dt.frontend.error.count), lcp = avg(dt.frontend.web.page.largest_contentful_paint), inp = avg(dt.frontend.web.page.interaction_to_next_paint) }, from:%s, filter: { dt.smartscape.frontend == toSmartscapeId(%q) }",
					tf.DQL(), e.ID)
			},
		}
	case "DB_INSTANCE_POSTGRES":
		return &MetricsSpec{
			Series: []MetricSeries{
				{Alias: "active", Title: "Active connections", Unit: ""},
				{Alias: "idle", Title: "Idle connections", Unit: ""},
				{Alias: "deadlocks", Title: "Deadlocks", Unit: ""},
			},
			Query: func(e Entity, tf Timeframe) string {
				return fmt.Sprintf(
					"timeseries { active = avg(postgres.activity.active), idle = avg(postgres.activity.idle), deadlocks = sum(postgres.deadlocks.count, default: 0) }, from:%s, filter: { dt.smartscape.db_instance_postgres == toSmartscapeId(%q) }",
					tf.DQL(), e.ID)
			},
		}
	case "AWS_RDS_DBINSTANCE":
		return &MetricsSpec{
			Series: []MetricSeries{
				{Alias: "cpu", Title: "CPU utilization", Unit: "%"},
				{Alias: "conn", Title: "Database connections", Unit: ""},
				{Alias: "mem", Title: "Freeable memory", Unit: "B"},
			},
			Query: func(e Entity, tf Timeframe) string {
				return fmt.Sprintf(
					"timeseries { cpu = avg(cloud.aws.rds.CPUUtilization.By.DBInstanceIdentifier), conn = avg(cloud.aws.rds.DatabaseConnections.By.DBInstanceIdentifier), mem = avg(cloud.aws.rds.FreeableMemory.By.DBInstanceIdentifier) }, from:%s, filter: { dt.smartscape_source.id == toSmartscapeId(%q) }",
					tf.DQL(), e.ID)
			},
		}
	}
	return nil
}
