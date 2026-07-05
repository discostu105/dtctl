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
	Drills: map[string]string{"l": "logs", "v": "events", "m": "metrics"},
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
		{Title: "ID", Field: "id", Width: 25},
		{Title: "DETECTION", Width: 9, Value: func(rec map[string]any) string {
			return Str(rec, "dt.service_detection.version")
		}},
		{Title: "SEEN", Width: 5, Value: lifetimeAge},
	},
	Entity: nodeEntity("SERVICE"),
	Drills: map[string]string{"l": "logs", "p": "problems", "v": "events", "m": "metrics"},
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
		{Title: "CPUS", Width: 4, Field: "logical_cores"},
		{Title: "MEMORY", Width: 9, Value: func(rec map[string]any) string {
			return FormatBytesStr(Str(rec, "memory"))
		}},
		{Title: "IP", Width: 15, Value: func(rec map[string]any) string { return StrFirst(rec, "ip") }},
		{Title: "CLOUD", Width: 5, Field: "cloud.provider"},
		{Title: "SKU", Width: 12, Field: "sku"},
		{Title: "SEEN", Width: 5, Value: lifetimeAge},
	},
	Entity: nodeEntity("HOST"),
	Drills: map[string]string{"l": "logs", "p": "problems", "v": "events", "m": "metrics"},
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
		b.WriteString("\n| sort timestamp desc\n| limit 300")
		return b.String()
	},
	Columns: []Column{
		{Title: "TIME", Width: 12, Value: func(rec map[string]any) string { return FormatTime(Str(rec, "timestamp")) }},
		{Title: "LEVEL", Width: 5, Value: logLevel, Class: classLogLevel},
		{Title: "SOURCE", Width: 24, Value: logSource},
		{Title: "CONTENT", Field: "content"},
	},
	Entity: signalSourceEntity,
	Drills: map[string]string{"p": "problems"},
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
	}
	return nil
}
