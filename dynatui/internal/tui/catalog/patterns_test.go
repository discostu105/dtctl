package catalog

import (
	"reflect"
	"strings"
	"testing"
)

func TestPatternExports(t *testing.T) {
	tests := []struct {
		pattern string
		want    []string
	}{
		// Extractor output shapes validated live on the box tenant.
		{`'LISTEN ' DQS:f_1`, []string{"f_1"}},
		{`'SELECT ' DATA:f_1`, []string{"f_1"}},
		{`TIMESTAMP:f_1 ' ' LD:f_2 ' status=' INT:f_3`, []string{"f_1", "f_2", "f_3"}},
		// Pure literals extract nothing.
		{`'BEGIN READ ONLY'`, nil},
		// A literal containing something colon-shaped is not an export.
		{`'-- name: FindStuck :many' LD:f_1`, []string{"f_1"}},
		{`'FOO:bar baz'`, nil},
		// Escaped quote inside a literal does not end it.
		{`'it\'s INT:not_an_export'`, nil},
		// Duplicate export names collapse.
		{`INT:f_1 ' ' INT:f_1`, []string{"f_1"}},
	}
	for _, tt := range tests {
		if got := PatternExports(tt.pattern); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("PatternExports(%q) = %v, want %v", tt.pattern, got, tt.want)
		}
	}
}

func TestPatternColumns(t *testing.T) {
	cols := PatternColumns(`'LISTEN ' DQS:f_1`)
	if len(cols) != 4 { // time, level, f_1, content
		t.Fatalf("columns = %d, want 4", len(cols))
	}
	if cols[2].Title != "F_1" || cols[2].Field != "f_1" {
		t.Errorf("extracted column = %+v", cols[2])
	}
	if cols[len(cols)-1].Field != "content" {
		t.Errorf("last column = %+v, want content", cols[len(cols)-1])
	}
	if PatternColumns(`'BEGIN READ ONLY'`) != nil {
		t.Error("literal-only pattern should keep the standard columns")
	}
	if PatternColumns("") != nil {
		t.Error("no pattern should keep the standard columns")
	}
}

// TestLogsPatternQuery ensures the pattern drill both narrows to matching
// records and parses out the pattern's named tokens as fields.
func TestLogsPatternQuery(t *testing.T) {
	spec := Lookup("logs")
	q := spec.Query(Scope{Timeframe: DefaultTimeframe, Pattern: `'LISTEN ' DQS:f_1`})
	if !strings.Contains(q, `| filter matchesPattern(content, "'LISTEN ' DQS:f_1")`) {
		t.Errorf("query misses matchesPattern:\n%s", q)
	}
	if !strings.Contains(q, `| parse content, "'LISTEN ' DQS:f_1"`) {
		t.Errorf("query misses parse stage:\n%s", q)
	}
	// A literal-only pattern filters but has nothing to parse.
	q = spec.Query(Scope{Timeframe: DefaultTimeframe, Pattern: `'BEGIN READ ONLY'`})
	if strings.Contains(q, "| parse") {
		t.Errorf("literal-only pattern must not add a parse stage:\n%s", q)
	}
	if !strings.Contains(q, "| filter matchesPattern") {
		t.Errorf("literal-only pattern still filters:\n%s", q)
	}
	// ScopeColumns follows the same rule.
	if cols := spec.ScopeColumns(Scope{Pattern: `'LISTEN ' DQS:f_1`}); len(cols) == 0 {
		t.Error("pattern scope should derive columns")
	}
	if cols := spec.ScopeColumns(Scope{}); cols != nil {
		t.Error("unpatterned scope must keep standard columns")
	}
}

func TestPatternsQueryShape(t *testing.T) {
	pod := Entity{ID: "K8S_POD-1", Name: "checkout-1", Type: "K8S_POD"}
	dql := patternsSpec.Query(Scope{Timeframe: DefaultTimeframe, Entity: &pod,
		TraceID: "abc123", Pattern: `'x' DQS:f_1`})
	if !strings.Contains(dql, `k8s.pod.name == "checkout-1"`) {
		t.Errorf("patterns must compose the entity scope:\n%s", dql)
	}
	// The full list scope carries over — trace- and pattern-narrowed logs
	// views must analyze their own subset, not the whole tenant.
	if !strings.Contains(dql, `trace_id == "abc123"`) || !strings.Contains(dql, "matchesPattern(content,") {
		t.Errorf("patterns must compose trace and pattern scope:\n%s", dql)
	}
	// The projection the analyzer schema demands is appended by the source
	// AFTER facet injection — a projection inside Query would null every
	// faceted field (found in review). LogPatternInput adds it last.
	if strings.Contains(dql, "| fields timestamp, content") {
		t.Errorf("patterns Query must not project (facets inject before the tail):\n%s", dql)
	}
	input := LogPatternInput(injectFacet(dql))
	if !strings.HasSuffix(input, "| fields timestamp, content") {
		t.Errorf("LogPatternInput must append the schema projection last:\n%s", input)
	}
	if strings.Contains(input, "\n") {
		t.Errorf("LogPatternInput must flatten to one line:\n%s", input)
	}
	if patternsSpec.API != "log-patterns" {
		t.Errorf("patterns view must run through the log-patterns source, got %q", patternsSpec.API)
	}
}

// injectFacet simulates the table view's facet injection so the test
// proves faceted fields survive until the source's appended projection.
func injectFacet(dql string) string {
	return InjectStages(dql, []string{`| filter loglevel == "ERROR"`})
}

func TestLogsPatternScope(t *testing.T) {
	dql := logsSpec.Query(Scope{Timeframe: DefaultTimeframe, Pattern: `IPADDR:ip ' - - '`})
	if !strings.Contains(dql, `| filter matchesPattern(content, "IPADDR:ip ' - - '")`) {
		t.Errorf("logs must compose the pattern scope:\n%s", dql)
	}
}
