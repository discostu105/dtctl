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
		// Plain strings compare with bare == — exact and case-sensitive.
		// toString(field) == would mute Grail's indexes, and a `~` leg would
		// token-match ("Running" also matching "Running fast", validated live).
		{Facet{Field: "phase", Value: "Running"}, `| filter phase == "Running"`},
		// Numeric-looking values cover long, string, and duration fields:
		// fieldsSummary stringifies every value ("2" for a numeric field),
		// `field == "2"` is silently empty on numerics, and duration values
		// summarize as nanosecond counts only a duration() literal matches
		// (all validated live).
		{Facet{Field: "logical_cores", Value: "2"}, `| filter logical_cores == 2 or logical_cores == "2" or logical_cores == duration(2, "ns")`},
		{Facet{Field: "cpu.load", Value: "2.5"}, `| filter cpu.load == 2.5 or cpu.load == "2.5"`},
		// Booleans: `~` never matches a boolean field, == true does.
		{Facet{Field: "has_access", Value: "true"}, `| filter has_access == true or has_access == "true"`},
		// ID-shaped values add a `~` leg: smartscape-ID/UID/IP-typed fields
		// never equal a string literal (silently empty), while `~`
		// auto-converts and matches their exact representation (validated live).
		{Facet{Field: "dt.smartscape.host", Value: "HOST-0000000000000001"},
			`| filter dt.smartscape.host == "HOST-0000000000000001" or dt.smartscape.host ~ "HOST-0000000000000001"`},
		{Facet{Field: "trace.id", Value: "07d906132971314866a3305e9ebb453c"},
			`| filter trace.id == "07d906132971314866a3305e9ebb453c" or trace.id ~ "07d906132971314866a3305e9ebb453c"`},
		{Facet{Field: "host.ip", Value: "192.168.0.7"},
			`| filter host.ip == "192.168.0.7" or host.ip ~ "192.168.0.7"`},
		// '*' switches to case-insensitive wildcard matching on the bare
		// field (element-wise on string arrays, validated live).
		{Facet{Field: "name", Value: "payment*"}, `| filter matchesValue(name, "payment*")`},
		{Facet{Field: "name", Value: "*ayment*"}, `| filter matchesValue(name, "*ayment*")`},
		// Tokens facets (elements of record or numeric arrays) search via
		// `~` — the only operator that matches inside those (validated live).
		{Facet{Field: "span.events", Value: "java.lang.NullPointerException", Tokens: true},
			`| filter span.events ~ "java.lang.NullPointerException"`},
		// Field names DQL rejects bare get backticks.
		{Facet{Field: "tags:aws", Value: "x"}, "| filter `tags:aws` == \"x\""},
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
	if got := (Facet{Field: "span.events", Value: "x", Tokens: true}).Label(); got != "span.events~x" {
		t.Errorf("tokens label = %q", got)
	}
}

func TestInjectStagesBeforeSortLimitTail(t *testing.T) {
	dql := "fetch logs, from:now() - 2h\n| filter x == 1\n| sort timestamp desc\n| limit 300"
	got := InjectStages(dql, []string{`| filter status == "ERROR"`})
	want := "fetch logs, from:now() - 2h\n| filter x == 1\n| filter status == \"ERROR\"\n| sort timestamp desc\n| limit 300"
	if got != want {
		t.Errorf("InjectStages =\n%s\nwant\n%s", got, want)
	}
}

func TestInjectStagesNoTailAppends(t *testing.T) {
	dql := `smartscapeNodes "SERVICE"` + "\n| fieldsRemove references"
	got := InjectStages(dql, []string{`| filter name == "x"`})
	if !strings.HasSuffix(got, `| filter name == "x"`) {
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
		got := InjectStages(dql, []string{`| filter x == "y"`})
		gl := strings.Split(got, "\n")
		last := strings.TrimSpace(gl[len(gl)-1])
		if !strings.HasPrefix(last, "| limit") && !strings.HasPrefix(last, "| sort") {
			t.Errorf("%s: query does not end with a sort/limit tail:\n%s", spec.Name, dql)
		}
		idx := strings.Index(got, `| filter x == "y"`)
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
		"| filter k8s.namespace.name == \"prod\"\n" +
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
		"| filter spans == 3 or spans == \"3\" or spans == duration(3, \"ns\")\n" +
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

// Bucket eligibility is a whitelist: referencing dt.system.bucket on a table
// without buckets fails the whole query with FIELD_DOES_NOT_EXIST — not null
// (validated live on dt.entity.*, dt.system.buckets, dt.semantic_dictionary.*).
func TestBucketEligible(t *testing.T) {
	tests := []struct {
		dql  string
		want bool
	}{
		{"fetch logs, from:now() - 2h\n| sort timestamp desc\n| limit 300", true},
		{"fetch bizevents, from:now() - 24h\n| limit 100", true},
		// dt.davis.* and dt.synthetic.* are views over the events table and
		// carry buckets (validated live).
		{"fetch dt.davis.problems, from:now() - 2h\n| filter not(dt.davis.is_duplicate)\n| limit 200", true},
		{"fetch dt.synthetic.events, from:now() - 24h\n| limit 100", true},
		// The record sampler's bucket slice of a table.
		{"fetch logs\n| filter dt.system.bucket == \"default_logs\"\n| limit 200", true},
		// Tables without buckets hard-fail the field reference.
		{"fetch dt.system.buckets\n| limit 100", false},
		{"fetch dt.entity.http_check\n| limit 100", false},
		{"fetch dt.semantic_dictionary.fields\n| limit 100", false},
		// Non-fetch sources carry no buckets.
		{"smartscapeNodes \"HOST\", from:now() - 2h\n| limit 100", false},
		// summarize aggregates the field away; a fields projection drops it.
		{"fetch security.events, from:now() - 2h\n| summarize x = count(), by:{id}\n| limit 100", false},
		{"fetch logs, from:now() - 2h\n| fields timestamp, content\n| limit 100", false},
	}
	for _, tt := range tests {
		if got := BucketEligible(tt.dql); got != tt.want {
			t.Errorf("BucketEligible(%q) = %v, want %v", tt.dql, got, tt.want)
		}
	}
}

// The full composition: a single exact bucket facet on a real table becomes
// the fetch command's bucket: parameter — Grail's native physical pruning —
// then searches directly after the source, the bucket projection, and the
// remaining facets before the sort/limit tail. Stage order validated live.
func TestComposeQueryBucketPlacement(t *testing.T) {
	dql := "fetch logs, from:now() - 2h\n| filter x == 1\n| sort timestamp desc\n| limit 300"
	got := ComposeQuery(dql, []string{"payment"}, []Facet{
		{Field: BucketField, Value: "default_logs"},
		{Field: "loglevel", Value: "ERROR"},
	}, true)
	want := strings.Join([]string{
		`fetch logs, from:now() - 2h, bucket:{"default_logs"}`,
		`| search "*payment*"`,
		"| fieldsAdd dt.system.bucket",
		"| filter x == 1",
		`| filter loglevel == "ERROR"`,
		"| sort timestamp desc",
		"| limit 300",
	}, "\n")
	if got != want {
		t.Errorf("ComposeQuery =\n%s\nwant\n%s", got, want)
	}
}

// The dt.davis.*/dt.synthetic.* views reject the bucket: parameter
// (PARAMETER_NOT_ALLOWED_FOR_FETCH_VIEW, validated live) — their bucket
// facet stays a head filter stage that still prunes before the tail.
func TestComposeQueryBucketOnViewStaysFilter(t *testing.T) {
	dql := "fetch dt.davis.problems, from:now() - 2h\n| sort event.start desc\n| limit 200"
	got := ComposeQuery(dql, nil, []Facet{{Field: BucketField, Value: "default_davis_events"}}, true)
	want := "fetch dt.davis.problems, from:now() - 2h\n" +
		`| filter dt.system.bucket == "default_davis_events"` + "\n" +
		"| fieldsAdd dt.system.bucket\n" +
		"| sort event.start desc\n| limit 200"
	if got != want {
		t.Errorf("ComposeQuery =\n%s\nwant\n%s", got, want)
	}
}

// Two bucket facets AND together (matching nothing, as the pills promise) —
// a multi-value bucket: parameter would flip that to OR, so they stay
// filter stages.
func TestComposeQueryMultipleBucketFacetsStayFilters(t *testing.T) {
	dql := "fetch logs, from:now() - 2h\n| sort timestamp desc\n| limit 300"
	got := ComposeQuery(dql, nil, []Facet{
		{Field: BucketField, Value: "default_logs"},
		{Field: BucketField, Value: "custom_logs"},
	}, false)
	want := "fetch logs, from:now() - 2h\n" +
		`| filter dt.system.bucket == "default_logs"` + "\n" +
		`| filter dt.system.bucket == "custom_logs"` + "\n" +
		"| sort timestamp desc\n| limit 300"
	if got != want {
		t.Errorf("ComposeQuery =\n%s\nwant\n%s", got, want)
	}
}

// Ineligible queries change nothing: no projection, and a (stale) bucket
// facet stays a plain tail filter like any other.
func TestComposeQueryIneligibleUntouched(t *testing.T) {
	dql := "smartscapeNodes \"HOST\", from:now() - 2h\n| sort name asc\n| limit 300"
	got := ComposeQuery(dql, nil, []Facet{{Field: BucketField, Value: "x"}}, true)
	want := "smartscapeNodes \"HOST\", from:now() - 2h\n" +
		`| filter dt.system.bucket == "x"` + "\n" +
		"| sort name asc\n| limit 300"
	if got != want {
		t.Errorf("ComposeQuery =\n%s\nwant\n%s", got, want)
	}
	if ComposeQuery(dql, nil, nil, true) != dql {
		t.Error("no narrowing on an ineligible query must return it unchanged")
	}
}

// An API view's query is analyzer input, not the records the table shows —
// the bucket projection is skipped, but the bucket facet still prunes the
// source via the bucket: parameter.
func TestComposeQueryAnalyzerInputSkipsProjection(t *testing.T) {
	dql := "fetch logs, from:now() - 2h\n| sort timestamp desc\n| limit 300"
	got := ComposeQuery(dql, nil, []Facet{{Field: BucketField, Value: "default_logs"}}, false)
	want := `fetch logs, from:now() - 2h, bucket:{"default_logs"}` + "\n" +
		"| sort timestamp desc\n| limit 300"
	if got != want {
		t.Errorf("ComposeQuery =\n%s\nwant\n%s", got, want)
	}
}
