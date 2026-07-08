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
		if s.Pattern != "" {
			// The DPL predicate keeps only records matching the extracted
			// pattern (validated live; an invalid pattern is a hard error,
			// not a silent empty). The parse stage then extracts the
			// pattern's named tokens (f_1, f_2, …) as real fields — the
			// extractor names every variable matcher (validated live) — so
			// the drill shows the pattern's variables as columns.
			fmt.Fprintf(&b, "\n| filter matchesPattern(content, %q)", s.Pattern)
			if len(PatternExports(s.Pattern)) > 0 {
				fmt.Fprintf(&b, "\n| parse content, %q", s.Pattern)
			}
		}
		b.WriteString("\n| sort timestamp desc\n| limit 300")
		return b.String()
	},
	Columns: []Column{
		logTimeColumn,
		logLevelColumn,
		{Title: "SOURCE", Width: 24, Value: logSource},
		{Title: "CONTENT", Field: "content"},
	},
	// A pattern drill swaps in columns for the pattern's extracted fields.
	ScopeColumns: func(s Scope) []Column { return PatternColumns(s.Pattern) },
	Entity:       signalSourceEntity,
	Trace:        func(rec map[string]any) string { return Str(rec, "trace_id") },
	// 'a' analyzes the current logs into Davis patterns ('g' is go-to-top).
	Drills: map[string]string{"p": "problems", "s": "trace", "a": "patterns"},
}

var logTimeColumn = Column{
	Title: "TIME", Width: 12,
	Value: func(rec map[string]any) string { return FormatTime(Str(rec, "timestamp")) },
	Sort:  func(rec map[string]any) any { return Str(rec, "timestamp") },
}

var logLevelColumn = Column{Title: "LEVEL", Width: 5, Value: logLevel, Class: classLogLevel}

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
	// Events carry their source entity — logs and metrics of the thing that
	// emitted the event are one keystroke away, mirroring the logs view.
	Drills: map[string]string{"l": "logs", "m": "metrics", "p": "problems"},
}

// --- canned metrics (the 'm' drill) ------------------------------------------

// MetricSeries describes one chart on the metrics view.
type MetricSeries struct {
	Alias string // field name in the timeseries result
	Title string
	Unit  string
	Key   string // metric key charted
	Agg   string // aggregation: avg, sum, min, max
	// Default fills empty buckets (the `default:` parameter) — sparse
	// counters like deadlocks read better as zero lines than gaps.
	Default string
}

// Expr renders the series' aggregation expression for a timeseries query.
func (s MetricSeries) Expr() string {
	if s.Default != "" {
		return fmt.Sprintf("%s = %s(%s, default: %s)", s.Alias, s.Agg, escapeField(s.Key), s.Default)
	}
	return fmt.Sprintf("%s = %s(%s)", s.Alias, s.Agg, escapeField(s.Key))
}

// MetricsSpec is the canned per-entity-type metrics page. Types without one
// fall back to the metric explorer (metricsSpec), which the canned view also
// links to via enter.
type MetricsSpec struct {
	Series []MetricSeries
	// Filter renders the scope condition composed into both the availability
	// probe and the timeseries query ("" = unscoped).
	Filter func(e Entity) string
}

// AvailabilityQuery probes which metric keys report for the scope. It exists
// because a timeseries query returns ZERO records when any requested metric
// has no series at all — `default:` does not rescue an entirely-absent key
// (validated live) — so charting a fixed series list silently blanks the
// whole page for entities missing one metric (pods without limits set were
// the visible casualty). The view intersects this probe with its series
// before composing the chart query.
func (m *MetricsSpec) AvailabilityQuery(e Entity, tf Timeframe) string {
	var b strings.Builder
	fmt.Fprintf(&b, "metrics from:%s", tf.DQL())
	if f := m.Filter(e); f != "" {
		fmt.Fprintf(&b, "\n| filter %s", f)
	}
	b.WriteString("\n| summarize count(), by:{metric.key}")
	return b.String()
}

// Query renders the timeseries for the series whose keys are available
// (nil available = chart everything). "" when nothing would be charted.
func (m *MetricsSpec) Query(e Entity, tf Timeframe, available map[string]bool) string {
	var exprs []string
	for _, s := range m.Series {
		if available != nil && !available[s.Key] {
			continue
		}
		exprs = append(exprs, s.Expr())
	}
	if len(exprs) == 0 {
		return ""
	}
	q := fmt.Sprintf("timeseries { %s }, from:%s", strings.Join(exprs, ", "), tf.DQL())
	if f := m.Filter(e); f != "" {
		q += fmt.Sprintf(", filter: { %s }", f)
	}
	return q
}

// smartscapeEq scopes a metrics filter to one entity via its
// dt.smartscape.* dimension.
func smartscapeEq(entityType string) func(e Entity) string {
	field := smartscapeField(entityType)
	return func(e Entity) string {
		return fmt.Sprintf("%s == toSmartscapeId(%q)", field, e.ID)
	}
}

// MetricsFor returns the canned metrics spec for an entity type, or nil when
// the type has no curated charts yet.
func MetricsFor(entityType string) *MetricsSpec {
	switch entityType {
	case "HOST":
		return &MetricsSpec{
			Series: []MetricSeries{
				{Alias: "cpu", Title: "CPU usage", Unit: "%", Key: "dt.host.cpu.usage", Agg: "avg"},
				{Alias: "mem", Title: "Memory usage", Unit: "%", Key: "dt.host.memory.usage", Agg: "avg"},
				{Alias: "disk", Title: "Disk used", Unit: "%", Key: "dt.host.disk.used.percent", Agg: "avg"},
			},
			Filter: smartscapeEq("HOST"),
		}
	case "SERVICE":
		// Scope via MetricScopeFilter: OTel-instrumented services report
		// http.server.* under service.name / dt.entity.service, not
		// dt.smartscape.service (validated live). dt.service.request
		// .response_time is microseconds — the µs unit renders adaptively.
		return &MetricsSpec{
			Series: []MetricSeries{
				{Alias: "req", Title: "Requests", Unit: "", Key: "dt.service.request.count", Agg: "sum"},
				{Alias: "fail", Title: "Failed requests", Unit: "", Key: "dt.service.request.failure_count", Agg: "sum"},
				{Alias: "rt", Title: "Response time", Unit: "µs", Key: "dt.service.request.response_time", Agg: "avg"},
				{Alias: "odur", Title: "HTTP server duration (OTel)", Unit: "s", Key: "http.server.request.duration", Agg: "avg"},
			},
			Filter: func(e Entity) string { return MetricScopeFilter(e) },
		}
	case "K8S_POD":
		// Universal keys plus the limits story — limit/throttling series only
		// exist on pods with limits set and drop out via the availability probe.
		return &MetricsSpec{
			Series: []MetricSeries{
				{Alias: "cpu", Title: "CPU usage", Unit: "mCores", Key: "dt.kubernetes.container.cpu_usage", Agg: "avg"},
				{Alias: "cpu_limit", Title: "CPU limit", Unit: "mCores", Key: "dt.kubernetes.container.limits_cpu", Agg: "avg"},
				{Alias: "throttled", Title: "CPU throttled", Unit: "mCores", Key: "dt.kubernetes.container.cpu_throttled", Agg: "avg"},
				{Alias: "mem", Title: "Memory working set", Unit: "B", Key: "dt.kubernetes.container.memory_working_set", Agg: "avg"},
				{Alias: "mem_limit", Title: "Memory limit", Unit: "B", Key: "dt.kubernetes.container.limits_memory", Agg: "avg"},
				{Alias: "net_rx", Title: "Network received", Unit: "B/s", Key: "dt.kubernetes.pod.network_received_data", Agg: "avg"},
				{Alias: "net_tx", Title: "Network transmitted", Unit: "B/s", Key: "dt.kubernetes.pod.network_transmitted_data", Agg: "avg"},
			},
			Filter: smartscapeEq("K8S_POD"),
		}
	case "K8S_DEPLOYMENT", "K8S_STATEFULSET", "K8S_DAEMONSET":
		return &MetricsSpec{
			Series: []MetricSeries{
				{Alias: "desired", Title: "Desired pods", Unit: "", Key: "dt.kubernetes.workload.pods_desired", Agg: "max"},
			},
			Filter: smartscapeEq(entityType),
		}
	case "K8S_NODE":
		return &MetricsSpec{
			Series: []MetricSeries{
				{Alias: "cpu_alloc", Title: "CPU allocatable", Unit: "mCores", Key: "dt.kubernetes.node.cpu_allocatable", Agg: "max"},
				{Alias: "mem_alloc", Title: "Memory allocatable", Unit: "B", Key: "dt.kubernetes.node.memory_allocatable", Agg: "max"},
				{Alias: "pods_alloc", Title: "Pods allocatable", Unit: "", Key: "dt.kubernetes.node.pods_allocatable", Agg: "max"},
			},
			Filter: smartscapeEq("K8S_NODE"),
		}
	case "FRONTEND":
		return &MetricsSpec{
			Series: []MetricSeries{
				{Alias: "req", Title: "Requests", Unit: "", Key: "dt.frontend.request.count", Agg: "sum"},
				{Alias: "err", Title: "Errors", Unit: "", Key: "dt.frontend.error.count", Agg: "sum"},
				{Alias: "lcp", Title: "Largest contentful paint", Unit: "ms", Key: "dt.frontend.web.page.largest_contentful_paint", Agg: "avg"},
				{Alias: "inp", Title: "Interaction to next paint", Unit: "ms", Key: "dt.frontend.web.page.interaction_to_next_paint", Agg: "avg"},
			},
			Filter: smartscapeEq("FRONTEND"),
		}
	case "DB_INSTANCE_POSTGRES":
		return &MetricsSpec{
			Series: []MetricSeries{
				{Alias: "active", Title: "Active connections", Unit: "", Key: "postgres.activity.active", Agg: "avg"},
				{Alias: "idle", Title: "Idle connections", Unit: "", Key: "postgres.activity.idle", Agg: "avg"},
				{Alias: "deadlocks", Title: "Deadlocks", Unit: "", Key: "postgres.deadlocks.count", Agg: "sum", Default: "0"},
			},
			Filter: smartscapeEq("DB_INSTANCE_POSTGRES"),
		}
	case "AWS_RDS_DBINSTANCE":
		return &MetricsSpec{
			Series: []MetricSeries{
				{Alias: "cpu", Title: "CPU utilization", Unit: "%", Key: "cloud.aws.rds.CPUUtilization.By.DBInstanceIdentifier", Agg: "avg"},
				{Alias: "conn", Title: "Database connections", Unit: "", Key: "cloud.aws.rds.DatabaseConnections.By.DBInstanceIdentifier", Agg: "avg"},
				{Alias: "mem", Title: "Freeable memory", Unit: "B", Key: "cloud.aws.rds.FreeableMemory.By.DBInstanceIdentifier", Agg: "avg"},
			},
			Filter: func(e Entity) string {
				return fmt.Sprintf("dt.smartscape_source.id == toSmartscapeId(%q)", e.ID)
			},
		}
	}
	return nil
}
