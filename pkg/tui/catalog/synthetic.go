package catalog

import (
	"fmt"
	"strings"
)

// Synthetic monitoring. Facts validated live (demo tenant — the box tenant
// has no synthetic at all): monitors are NOT smartscape nodes — SYNTHETIC_*
// and HTTP_CHECK return empty from smartscapeNodes even where classic
// entities are plentiful — so the views run on classic dt.entity.* tables.
// Browser and HTTP monitors split across two entity tables and two metric
// families (dt.synthetic.browser.* by dt.entity.synthetic_test,
// dt.synthetic.http.* by dt.entity.http_check); `append` unions them in one
// query. Execution results live in dt.synthetic.events (exact name — the
// variants error with UNKNOWN_DATA_OBJECT); durations are ns strings.

var syntheticSpec = &Spec{
	Name:    "synthetic",
	Aliases: []string{"syn", "monitors", "monitor"},
	Kind:    KindEntity,
	Desc:    "Synthetic monitors (browser + HTTP) — enter shows executions",
	Query: func(s Scope) string {
		switch lensAt(syntheticLenses, s.Lens).Name {
		case "browser":
			return `fetch dt.entity.synthetic_test
| fieldsAdd lifetime, tags
| fieldsAdd monitor.type = "browser"
| sort entity.name asc
| limit 500`
		case "http":
			return `fetch dt.entity.http_check
| fieldsAdd lifetime, tags
| fieldsAdd monitor.type = "http"
| sort entity.name asc
| limit 500`
		}
		return `fetch dt.entity.synthetic_test
| fieldsAdd lifetime, tags
| fieldsAdd monitor.type = "browser"
| append [fetch dt.entity.http_check | fieldsAdd lifetime, tags | fieldsAdd monitor.type = "http"]
| sort entity.name asc
| limit 500`
	},
	Lenses: syntheticLenses,
	Columns: []Column{
		{Title: "NAME", Field: "entity.name"},
		{Title: "TYPE", Width: 7, Field: "monitor.type"},
		SparkColumn("AVAILABILITY", "avail", 12),
		{Title: "TAGS", Width: 20, Value: func(rec map[string]any) string { return FormatValue(rec["tags"]) }},
		{Title: "ID", Width: 28, Field: "id"},
		{Title: "SEEN", Width: 5, Right: true, Value: lifetimeAge},
	},
	EnterTarget: "executions",
	EnterArg:    func(rec map[string]any) string { return Str(rec, "id") },
	Drills:      map[string]string{},
	Enrich: &EnrichSpec{
		Key:    func(rec map[string]any) string { return Str(rec, "id") },
		By:     "key",
		Series: []string{"avail"},
		Query: func(tf Timeframe, keys []string) string {
			// One query for both monitor families: each timeseries filters by
			// its own entity dimension (ids of the other family simply do not
			// match) and aliases it to a shared join key (validated live).
			return fmt.Sprintf(
				"timeseries avail = avg(dt.synthetic.browser.availability), by:{dt.entity.synthetic_test}, from:%s, interval: %s, filter: { in(dt.entity.synthetic_test, {%s}) }\n"+
					"| fieldsAdd key = dt.entity.synthetic_test\n"+
					"| append [timeseries avail = avg(dt.synthetic.http.availability), by:{dt.entity.http_check}, from:%s, interval: %s, filter: { in(dt.entity.http_check, {%s}) } | fieldsAdd key = dt.entity.http_check]\n"+
					"| fields key, avail",
				tf.DQL(), sparkInterval(tf), quoteList(keys),
				tf.DQL(), sparkInterval(tf), quoteList(keys))
		},
	},
}

var syntheticLenses = []Lens{
	{Name: "all", Desc: "browser and HTTP monitors"},
	{Name: "browser", Desc: "browser monitors only"},
	{Name: "http", Desc: "HTTP monitors only"},
}

var executionsSpec = &Spec{
	Name:    "executions",
	Aliases: []string{"exec", "runs"},
	Kind:    KindSignal,
	Desc:    "Synthetic executions and steps (dt.synthetic.events)",
	Query: func(s Scope) string {
		var b strings.Builder
		fmt.Fprintf(&b, "fetch dt.synthetic.events, from:%s", s.Timeframe.DQL())
		if s.Arg != "" {
			fmt.Fprintf(&b, "\n| filter dt.synthetic.monitor.id == %q", s.Arg)
		}
		if l := lensAt(executionLenses, s.Lens); l.Filter != "" {
			fmt.Fprintf(&b, "\n| filter %s", l.Filter)
		}
		b.WriteString("\n| sort timestamp desc\n| limit 300")
		return b.String()
	},
	Lenses: executionLenses,
	Columns: []Column{
		{Title: "TIME", Width: 12, Value: func(rec map[string]any) string { return FormatTime(Str(rec, "timestamp")) },
			Sort: func(rec map[string]any) any { return Str(rec, "timestamp") }},
		{Title: "MONITOR", Width: 30, Field: "monitor.name"},
		{Title: "EVENT", Width: 30, Value: func(rec map[string]any) string {
			return strings.ReplaceAll(Str(rec, "event.type"), "_", " ")
		}},
		{Title: "STATE", Width: 7, Field: "result.state", Class: classExecutionState},
		{Title: "STATUS", Width: 14, Field: "result.status.message"},
		{Title: "STEP", Width: 24, Field: "step.name"},
		{Title: "DURATION", Width: 8, Right: true,
			Value: func(rec map[string]any) string { return FormatNs(rec["result.statistics.duration"]) },
			Sort:  func(rec map[string]any) any { return rec["result.statistics.duration"] }},
	},
	Drills: map[string]string{},
}

var executionLenses = []Lens{
	{Name: "all", Desc: "every synthetic event"},
	{Name: "runs", Desc: "monitor executions only",
		Filter: `event.type == "browser_monitor_execution" or event.type == "http_monitor_execution"`},
	{Name: "steps", Desc: "per-step executions",
		// HTTP step events are "http_step_execution", NOT
		// "http_monitor_step_execution" (validated live).
		Filter: `event.type == "browser_monitor_step_execution" or event.type == "http_step_execution"`},
	{Name: "failed", Desc: "executions that did not succeed",
		Filter: `isNotNull(result.state) and result.state != "SUCCESS"`},
}

func classExecutionState(val string) string {
	switch val {
	case "SUCCESS":
		return "ok"
	case "":
		return ""
	default:
		return "error"
	}
}
