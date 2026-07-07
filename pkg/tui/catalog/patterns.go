package catalog

import (
	"fmt"
	"strings"
)

// Log pattern analysis via the Davis analyzer
// dt.statistics.clustering.LogPatternExtractor ('g' on the logs view).
// Facts validated live: the analyzer takes a logQuery that MUST project
// timestamp and content (schema-enforced), returns synchronously in ~1s for a
// few hundred records, and its output items are {patternExpression (a DPL
// pattern), sampleMatches, numberOfMatches} — unsorted, so the source sorts
// by match count. The patternExpression drops straight into DQL's
// matchesPattern(content, …) predicate (validated live), which is how enter
// on a pattern row drills into the matching log records.

// LogPatternAnalyzer is the Davis analyzer the patterns view executes.
const LogPatternAnalyzer = "dt.statistics.clustering.LogPatternExtractor"

// LogPatternSampleSize caps how many log records feed one pattern extraction.
const LogPatternSampleSize = 1000

// LogPatternExamples is how many sample lines the analyzer keeps per pattern.
const LogPatternExamples = 3

var patternsSpec = &Spec{
	Name:    "patterns",
	Aliases: []string{"pat", "logpatterns", "grouped"},
	Kind:    KindSignal,
	Desc:    "Log patterns (Davis clustering) — enter shows matching records",
	API:     "log-patterns",
	// The query is the analyzer's input: the scoped logs pipeline projected
	// to what the extractor needs. Server searches inject into it like any
	// other view, so '/'-narrowed patterns work; ctrl+q reveals it.
	Query: func(s Scope) string {
		var b strings.Builder
		fmt.Fprintf(&b, "fetch logs, from:%s", s.Timeframe.DQL())
		if s.Entity != nil {
			fmt.Fprintf(&b, "\n| filter %s", SignalFilter(*s.Entity))
		}
		fmt.Fprintf(&b, "\n| fields timestamp, content\n| limit %d", LogPatternSampleSize)
		return b.String()
	},
	Echo: func(s Scope, dql string) string {
		oneline := strings.Join(strings.Fields(strings.ReplaceAll(dql, "\n", " ")), " ")
		return fmt.Sprintf("dtctl exec analyzer %s --input '{\"logQuery\":\"%s\",\"numberOfExamples\":%d}'",
			LogPatternAnalyzer, oneline, LogPatternExamples)
	},
	Columns: []Column{
		{Title: "MATCHES", Width: 8, Right: true, Field: "numberOfMatches"},
		{Title: "PATTERN", Value: func(rec map[string]any) string { return Str(rec, "patternExpression") }},
		{Title: "SAMPLE", Value: func(rec map[string]any) string {
			if arr, ok := rec["sampleMatches"].([]any); ok && len(arr) > 0 {
				return FormatValue(arr[0])
			}
			return ""
		}},
	},
	EnterTarget: "pattern-logs", // sentinel: logs filtered by the row's pattern
	Drills:      map[string]string{},
}

// PatternOf extracts the DPL pattern a patterns-view row stands for.
func PatternOf(rec map[string]any) string { return Str(rec, "patternExpression") }
