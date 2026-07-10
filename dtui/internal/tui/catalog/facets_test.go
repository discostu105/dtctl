package catalog

import (
	"strings"
	"testing"
)

func TestFacetStageEncodings(t *testing.T) {
	tests := []struct {
		facet Facet
		want  string
	}{
		// Exact values compare via toString: fieldsSummary stringifies every
		// value, and numeric_field == "2" is silently empty (validated live).
		{Facet{Field: "phase", Value: "Running"}, `| filter toString(phase) == "Running"`},
		{Facet{Field: "logical_cores", Value: "2"}, `| filter toString(logical_cores) == "2"`},
		// '*' switches to case-insensitive wildcard matching.
		{Facet{Field: "name", Value: "payment*"}, `| filter matchesValue(toString(name), "payment*")`},
		{Facet{Field: "name", Value: "*ayment*"}, `| filter matchesValue(toString(name), "*ayment*")`},
		// Field names DQL rejects bare get backticks.
		{Facet{Field: "tags:aws", Value: "x"}, "| filter toString(`tags:aws`) == \"x\""},
	}
	for _, tt := range tests {
		if got := tt.facet.Stage(); got != tt.want {
			t.Errorf("Stage(%+v) = %q, want %q", tt.facet, got, tt.want)
		}
	}
}

func TestFacetLabel(t *testing.T) {
	if got := (Facet{Field: "phase", Value: "Running"}).Label(); got != "phase=Running" {
		t.Errorf("exact label = %q", got)
	}
	if got := (Facet{Field: "name", Value: "pay*"}).Label(); got != "name~pay*" {
		t.Errorf("pattern label = %q", got)
	}
}

func TestInjectStagesBeforeSortLimitTail(t *testing.T) {
	dql := "fetch logs, from:now() - 2h\n| filter x == 1\n| sort timestamp desc\n| limit 300"
	got := InjectStages(dql, []string{`| filter toString(status) == "ERROR"`})
	want := "fetch logs, from:now() - 2h\n| filter x == 1\n| filter toString(status) == \"ERROR\"\n| sort timestamp desc\n| limit 300"
	if got != want {
		t.Errorf("InjectStages =\n%s\nwant\n%s", got, want)
	}
}

func TestInjectStagesNoTailAppends(t *testing.T) {
	dql := `smartscapeNodes "SERVICE"` + "\n| fieldsRemove references"
	got := InjectStages(dql, []string{`| filter toString(name) == "x"`})
	if !strings.HasSuffix(got, `| filter toString(name) == "x"`) {
		t.Errorf("stages should append when there is no sort/limit tail:\n%s", got)
	}
}

// Search stages must land directly after the source command — DQL rejects
// them after transforming commands like parse/expand/summarize (validated
// live: SEARCH_COMMAND_NOT_ALLOWED_AFTER on the pods pipeline). Multiple
// terms chain as consecutive stages (AND, validated live).
func TestInjectSearchesAfterSource(t *testing.T) {
	dql := "smartscapeNodes \"K8S_POD\", from:now() - 2h\n| parse k8s.object, \"JSON:obj\"\n| summarize x = count(), by:{id}\n| sort x desc\n| limit 800"
	got := strings.Split(InjectSearches(dql, []string{"payment", "fss*"}), "\n")
	if got[1] != `| search "*payment*"` || got[2] != `| search "fss*"` {
		t.Errorf("search stages must follow the source line:\n%s", strings.Join(got, "\n"))
	}
	if len(got) != 7 || got[0] != "smartscapeNodes \"K8S_POD\", from:now() - 2h" {
		t.Errorf("InjectSearches reshaped the query:\n%s", strings.Join(got, "\n"))
	}
	if InjectSearches(dql, nil) != dql {
		t.Error("no terms must return the query unchanged")
	}
}

func TestInjectStagesEmpty(t *testing.T) {
	dql := "fetch logs\n| limit 10"
	if got := InjectStages(dql, nil); got != dql {
		t.Errorf("no stages must return the query unchanged, got:\n%s", got)
	}
}

// Every registered view's query must honor the two injection conventions:
// a sort/limit tail for facet filters, and a single source line the search
// stage can follow.
func TestInjectionOnEveryCatalogQuery(t *testing.T) {
	scope := Scope{Timeframe: DefaultTimeframe}
	for _, spec := range All() {
		if spec.Query == nil {
			// API-backed views without a query have no DQL surface — the
			// table view disables server search and facets for them.
			if spec.API == "" {
				t.Errorf("%s: neither Query nor API set", spec.Name)
			}
			continue
		}
		dql := spec.Query(scope)
		lines := strings.Split(dql, "\n")
		if strings.HasPrefix(strings.TrimSpace(lines[0]), "|") {
			t.Errorf("%s: first line is not a source command:\n%s", spec.Name, dql)
		}
		got := InjectStages(dql, []string{`| filter toString(x) == "y"`})
		gl := strings.Split(got, "\n")
		last := strings.TrimSpace(gl[len(gl)-1])
		if !strings.HasPrefix(last, "| limit") && !strings.HasPrefix(last, "| sort") {
			t.Errorf("%s: query does not end with a sort/limit tail:\n%s", spec.Name, dql)
		}
		idx := strings.Index(got, `| filter toString(x)`)
		tail := strings.LastIndex(got, "| limit")
		if idx == -1 || (tail != -1 && idx > tail) {
			t.Errorf("%s: facet stage not injected before the limit:\n%s", spec.Name, got)
		}
		if sl := strings.Split(InjectSearches(dql, []string{"probe"}), "\n"); sl[1] != `| search "*probe*"` {
			t.Errorf("%s: search stage must follow the source line:\n%s", spec.Name, strings.Join(sl, "\n"))
		}
	}
}

func TestFieldsSummaryQuery(t *testing.T) {
	dql := "fetch logs, from:now() - 2h\n| sort timestamp desc\n| limit 300"
	got := FieldsSummaryQuery(dql, "loglevel", []string{"payment"}, []Facet{
		{Field: "loglevel", Value: "INFO"}, // same field — excluded so all values show
		{Field: "k8s.namespace.name", Value: "prod"},
	})
	want := "fetch logs, from:now() - 2h\n" +
		"| search \"*payment*\"\n" +
		"| filter toString(k8s.namespace.name) == \"prod\"\n" +
		"| fieldsSummary loglevel, topValues: 25"
	if got != want {
		t.Errorf("FieldsSummaryQuery =\n%s\nwant\n%s", got, want)
	}
}

// On an aggregating pipeline the search must precede the summarize while the
// facet filters stay after it (they filter the summarized fields).
func TestFieldsSummaryQuerySearchPrecedesSummarize(t *testing.T) {
	dql := "fetch spans, from:now() - 2h\n| summarize spans = count(), by:{trace.id}\n| sort spans desc\n| limit 100"
	got := FieldsSummaryQuery(dql, "svc", []string{"checkout"}, []Facet{{Field: "spans", Value: "3"}})
	want := "fetch spans, from:now() - 2h\n" +
		"| search \"*checkout*\"\n" +
		"| summarize spans = count(), by:{trace.id}\n" +
		"| filter toString(spans) == \"3\"\n" +
		"| fieldsSummary svc, topValues: 25"
	if got != want {
		t.Errorf("FieldsSummaryQuery =\n%s\nwant\n%s", got, want)
	}
}

func TestParseFieldsSummary(t *testing.T) {
	records := []map[string]any{{
		"field": "loglevel",
		"count": "452238",
		"values": []any{
			map[string]any{"value": "INFO", "count": "433403"},
			map[string]any{"value": "2", "count": "54"},
		},
	}}
	got := ParseFieldsSummary(records)
	if len(got) != 2 || got[0].Value != "INFO" || got[0].Count != "433403" || got[1].Value != "2" {
		t.Errorf("ParseFieldsSummary = %+v", got)
	}
	if ParseFieldsSummary(nil) != nil {
		t.Error("empty result should parse to nil")
	}
}
