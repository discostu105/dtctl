package catalog

import (
	"fmt"
	"strconv"
	"strings"
)

// Management assets backed by REST APIs rather than DQL: SLOs (platform SLO
// API) and anomaly detectors (Settings API). Their records are flattened
// maps produced by the sources wired in tui.Options.Sources; columns name
// the flattened keys. Facts validated live: the SLO API returns definitions
// only — status, SLI value, and error budget come from the evaluation
// endpoint, which the slos source runs per SLO; ids/versions are opaque
// ~150-char base64 blobs (truncate, never sort by them); detector analyzer
// input values are all strings and can embed multi-KB DQL.

var slosSpec = &Spec{
	Name:    "slos",
	Aliases: []string{"slo", "objectives"},
	Kind:    KindEntity,
	Desc:    "Service-level objectives with live evaluation",
	API:     "slos",
	Echo:    func(Scope, string) string { return "dtctl get slos" },
	Columns: []Column{
		{Title: "NAME", Field: "name"},
		{Title: "STATUS", Width: 8, Field: "status", Class: classSLOStatus},
		{Title: "SLI", Width: 7, Right: true, Value: func(rec map[string]any) string {
			return formatPercent(rec["sli"])
		}, Sort: func(rec map[string]any) any { return rec["sli"] }},
		{Title: "TARGET", Width: 7, Right: true, Value: func(rec map[string]any) string {
			return formatPercent(rec["target"])
		}, Sort: func(rec map[string]any) any { return rec["target"] }},
		{Title: "BUDGET", Width: 7, Right: true, Value: func(rec map[string]any) string {
			return formatPercent(rec["errorBudget"])
		}, Sort: func(rec map[string]any) any { return rec["errorBudget"] }, Class: classErrorBudget},
		{Title: "WINDOW", Width: 8, Field: "window"},
		{Title: "TAGS", Width: 30, Value: func(rec map[string]any) string { return FormatValue(rec["tags"]) }},
		{Title: "MODIFIED", Width: 8, Right: true, Value: func(rec map[string]any) string {
			return Age(Str(rec, "modified"))
		}, Sort: func(rec map[string]any) any { return Str(rec, "modified") }},
	},
	Drills: map[string]string{},
}

func classSLOStatus(val string) string {
	switch val {
	case "SUCCESS", "MET", "OK":
		return "ok"
	case "":
		return "dim"
	default:
		return "error"
	}
}

func classErrorBudget(val string) string {
	f, err := parsePercent(val)
	if err != nil {
		return ""
	}
	switch {
	case f < 0:
		return "error"
	case f < 10:
		return "warn"
	}
	return ""
}

func formatPercent(v any) string {
	f, ok := FloatValue(v)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%.2f%%", f)
}

func parsePercent(val string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSuffix(val, "%"), 64)
}

var detectorsSpec = &Spec{
	Name:    "detectors",
	Aliases: []string{"detector", "ad", "anomaly"},
	Kind:    KindEntity,
	Desc:    "Davis anomaly detectors (Settings API)",
	API:     "anomaly-detectors",
	Echo:    func(Scope, string) string { return "dtctl get anomaly-detectors" },
	Columns: []Column{
		{Title: "TITLE", Field: "title"},
		{Title: "ENABLED", Width: 7, Field: "enabled", Class: func(val string) string {
			if val == "false" {
				return "dim"
			}
			return "ok"
		}},
		{Title: "ANALYZER", Width: 24, Field: "analyzer"},
		{Title: "EVENT TYPE", Width: 16, Field: "eventType"},
		{Title: "SOURCE", Width: 12, Field: "source"},
		{Title: "DESCRIPTION", Field: "description"},
	},
	Drills: map[string]string{},
}
