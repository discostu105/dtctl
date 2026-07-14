package catalog

import (
	"encoding/json"
	"fmt"
	"regexp"
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
	// The query is the analyzer's input: the scoped logs pipeline, mirroring
	// logsSpec (entity, trace, pattern) so 'a' analyzes exactly the list the
	// user was looking at. Server searches and facets inject into it like any
	// other view; ctrl+q reveals it. The timestamp/content projection the
	// analyzer schema demands is appended by the log-patterns source AFTER
	// composition — a projection here would sit before the injected facet
	// stages and silently null every faceted field.
	Query: func(s Scope) string {
		var b strings.Builder
		fmt.Fprintf(&b, "fetch logs, from:%s", s.Timeframe.DQL())
		if s.Entity != nil {
			fmt.Fprintf(&b, "\n| filter %s", SignalFilter(*s.Entity))
		}
		if s.TraceID != "" {
			fmt.Fprintf(&b, "\n| filter trace_id == %q", s.TraceID)
		}
		if s.Pattern != "" {
			fmt.Fprintf(&b, "\n| filter matchesPattern(content, %q)", s.Pattern)
		}
		fmt.Fprintf(&b, "\n| limit %d", LogPatternSampleSize)
		return b.String()
	},
	Echo: func(s Scope, dql string) string {
		// The logQuery lands inside a JSON string — marshal it, or any quote
		// in the composed DQL breaks the copied command.
		input, err := json.Marshal(map[string]any{
			"logQuery":         LogPatternInput(dql),
			"numberOfExamples": LogPatternExamples,
		})
		if err != nil {
			return ""
		}
		return fmt.Sprintf("dtctl exec analyzer %s --input '%s'", LogPatternAnalyzer, input)
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

// LogPatternInput turns the composed patterns query into the analyzer's
// logQuery: flattened to one line with the timestamp/content projection the
// input schema requires appended last (after any injected facet stages, so
// they still see the full record).
func LogPatternInput(dql string) string {
	return strings.Join(strings.Fields(dql), " ") + " | fields timestamp, content"
}

// exportRe matches a DPL matcher export: MATCHER:name (the extractor names
// its variable tokens f_1, f_2, … — validated live: "'LISTEN ' DQS:f_1").
var exportRe = regexp.MustCompile(`\b[A-Z][A-Z0-9]*:([A-Za-z_][A-Za-z0-9_]*)`)

// PatternExports lists the field names a DPL pattern extracts, in pattern
// order. Quoted literals are stripped first so a literal "FOO:bar" inside
// '…' never counts as an export.
func PatternExports(pattern string) []string {
	var bare strings.Builder
	inLiteral := false
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case inLiteral && c == '\\' && i+1 < len(pattern):
			i++ // escaped char inside a literal
		case c == '\'':
			inLiteral = !inLiteral
			bare.WriteByte(' ')
		case !inLiteral:
			bare.WriteByte(c)
		}
	}
	var names []string
	seen := map[string]bool{}
	for _, m := range exportRe.FindAllStringSubmatch(bare.String(), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			names = append(names, m[1])
		}
	}
	return names
}

// patternColumnMax caps how many extracted fields become table columns.
const patternColumnMax = 5

// PatternColumns derives the logs view's column set for a pattern drill:
// time and level, one column per extracted field, and the raw content last.
// nil when the pattern extracts nothing (pure literals) — the standard log
// columns apply.
func PatternColumns(pattern string) []Column {
	exports := PatternExports(pattern)
	if len(exports) == 0 {
		return nil
	}
	if len(exports) > patternColumnMax {
		exports = exports[:patternColumnMax]
	}
	cols := []Column{logTimeColumn, logLevelColumn}
	for _, name := range exports {
		cols = append(cols, Column{Title: strings.ToUpper(name), Field: name})
	}
	return append(cols, Column{Title: "CONTENT", Field: "content"})
}
