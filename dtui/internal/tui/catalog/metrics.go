package catalog

import (
	"fmt"
	"sort"
	"strings"
)

// The `metrics` DQL command enumerates metric series — one record per metric
// key + dimension set — within a timeframe. Summarizing by metric.key turns
// it into a per-environment metric catalog (custom metrics included), and
// filtering by an entity's dimension fields scopes it to "metrics related to
// this entity". Validated live: a K8S_POD on a busy tenant carries ~120 keys
// (dt.kubernetes.*, dt.containers.*, dt.process.*, plus custom OTel app
// metrics) — far beyond any canned chart set, which is why the explorer
// exists.

// metricsSpec is the metric explorer: every metric key reporting in the
// window, optionally scoped to an entity (pin, 'm' drill on types without
// canned charts, detail-page tab). enter charts the selected key.
var metricsSpec = &Spec{
	Name:    "metrics",
	Aliases: []string{"mx", "metric"},
	Kind:    KindSignal,
	Desc:    "Metric explorer — keys reporting in the window",
	Query: func(s Scope) string {
		var b strings.Builder
		fmt.Fprintf(&b, "metrics from:%s", s.Timeframe.DQL())
		if s.Entity != nil {
			fmt.Fprintf(&b, "\n| filter %s", MetricScopeFilter(*s.Entity))
		}
		b.WriteString("\n| summarize series = count(), types = collectDistinct(dt.smartscape_source.type), by:{metric.key}")
		b.WriteString("\n| sort metric.key asc\n| limit 1000")
		return b.String()
	},
	Columns: []Column{
		{Title: "METRIC KEY", Field: "metric.key"},
		{Title: "SERIES", Field: "series", Width: 6, Right: true},
		{Title: "RELATES TO", Width: 28, Value: metricRelatesTo},
	},
	EnterTarget: "chart",
}

// metricRelatesTo summarizes which entity types a metric's series point at
// (the collectDistinct(dt.smartscape_source.type) column; null = series
// without a source entity).
func metricRelatesTo(rec map[string]any) string {
	arr, _ := rec["types"].([]any)
	var kept []string
	for _, t := range arr {
		if s, ok := t.(string); ok && s != "" {
			kept = append(kept, s)
		}
	}
	sort.Strings(kept)
	return strings.Join(kept, " ")
}

// MetricScopeFilter renders the DQL condition that scopes a metric-series
// query to an entity. Mirrors SignalFilter's dual-era matching, plus
// service.name for services: OTel-exported metrics carry service.name and the
// legacy dt.entity.service but no dt.smartscape.service (validated live — the
// name clause grows a service's discovered keys from 3 to ~35 on the box
// tenant). K8s metrics need no name fallback: they carry dt.smartscape.k8s_*
// dimensions directly.
func MetricScopeFilter(e Entity) string {
	var parts []string
	if f := smartscapeField(e.Type); f != "" {
		parts = append(parts, fmt.Sprintf("%s == toSmartscapeId(%q)", f, e.ID))
	}
	if f := legacyField(e.Type); f != "" {
		parts = append(parts, fmt.Sprintf("%s == %q", f, e.ID))
	}
	if e.Type == "SERVICE" && e.Name != "" {
		parts = append(parts, fmt.Sprintf("service.name == %q", e.Name))
	}
	if e.Type == "K8S_NODE" && e.Name != "" {
		// A node's own utilization lives in dt.host.* series, which carry
		// host.name but no dt.smartscape.k8s_node (validated live; OneAgent
		// names the host after the node) — the name arm lets a node scope
		// find its host's metrics too.
		parts = append(parts, fmt.Sprintf("host.name == %q", e.Name))
	}
	parts = append(parts, fmt.Sprintf("dt.smartscape_source.id == toSmartscapeId(%q)", e.ID))
	return strings.Join(parts, " or ")
}

// ChartAggs are the aggregations the explorer chart cycles through ('a').
var ChartAggs = []string{"avg", "sum", "min", "max"}

// ExploreMetricsSpec builds the single-chart spec behind enter on an explorer
// row. The series carries no unit here — the chart query runs with
// metric-metadata enrichment and the view attaches the catalogue unit
// (metadata.metrics[].unit, via NormalizeUnit) from the response. The entity
// filter composes only when the chart is scoped (e.ID != "": the explorer was
// itself entity-scoped).
func ExploreMetricsSpec(key, agg string) *MetricsSpec {
	return &MetricsSpec{
		Series: []MetricSeries{{Alias: "value", Title: agg + "(" + key + ")", Key: key, Agg: agg}},
		Filter: func(e Entity) string {
			if e.ID == "" {
				return ""
			}
			return MetricScopeFilter(e)
		},
	}
}

// MetricDimsQuery samples a metric's series records so the chart's split
// picker can discover the dimensions (and their cardinality) client-side.
func MetricDimsQuery(key string, e Entity, tf Timeframe) string {
	var b strings.Builder
	fmt.Fprintf(&b, "metrics from:%s\n| filter metric.key == %q", tf.DQL(), key)
	if e.ID != "" {
		fmt.Fprintf(&b, "\n| filter %s", MetricScopeFilter(e))
	}
	b.WriteString("\n| limit 500")
	return b.String()
}

// MetricSplitQuery charts one metric per value of a dimension. The result is
// one record per dimension value; the view ranks and caps them.
func MetricSplitQuery(key, agg, dim string, e Entity, tf Timeframe) string {
	var b strings.Builder
	fmt.Fprintf(&b, "timeseries value = %s(%s), by:{%s}, from:%s",
		agg, escapeField(key), escapeField(dim), tf.DQL())
	if e.ID != "" {
		fmt.Fprintf(&b, ", filter: { %s }", MetricScopeFilter(e))
	}
	b.WriteString("\n| limit 100")
	return b.String()
}
