package catalog

import (
	"fmt"
	"regexp"
	"strings"
)

// Server-side narrowing for table views: the '/'-promoted full-text search
// and the 'f' facet picker. Validated live (see TUI_LEARNINGS.md §1.10):
//   - `| search "text"` matches case-insensitively across all fields on
//     fetch and smartscapeNodes pipelines alike, but is rejected after
//     transforming commands — parse/expand/summarize —
//     (SEARCH_COMMAND_NOT_ALLOWED_AFTER), while filter and fieldsRemove
//     before it are fine. It therefore injects directly after the source.
//   - fieldsSummary stringifies values ("2" for a numeric field), and
//     `numeric_field == "2"` is silently empty — toString(field) == "2" is
//     the type-safe exact encoding.
//   - matchesValue supports leading/trailing '*' wildcards, case-insensitive.

// Facet is one attribute filter: an exact value or a '*' wildcard pattern.
type Facet struct {
	Field string
	Value string
}

// Pattern reports whether the facet value is a wildcard pattern.
func (f Facet) Pattern() bool { return strings.Contains(f.Value, "*") }

// Stage renders the facet as a DQL filter stage.
func (f Facet) Stage() string {
	field := escapeField(f.Field)
	if f.Pattern() {
		return fmt.Sprintf("| filter matchesValue(toString(%s), %q)", field, f.Value)
	}
	return fmt.Sprintf("| filter toString(%s) == %q", field, f.Value)
}

// Label is the compact pill text shown in the table header.
func (f Facet) Label() string {
	if f.Pattern() {
		return f.Field + "~" + f.Value
	}
	return f.Field + "=" + f.Value
}

// SearchStage renders the server-side full-text stage ('/' + enter). The
// search command matches whole tokens — "fss" misses pod "…-fssb5"
// (validated live) — so a term without wildcards of its own wraps in *…*
// for the contains semantics the '/' filter promises.
func SearchStage(term string) string {
	if !strings.Contains(term, "*") {
		term = "*" + term + "*"
	}
	return fmt.Sprintf("| search %q", term)
}

// bareField matches field names DQL accepts unquoted (dotted paths).
var bareField = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)

// escapeField backtick-quotes field names DQL rejects bare (`tags:aws`).
func escapeField(name string) string {
	if bareField.MatchString(name) {
		return name
	}
	return "`" + name + "`"
}

// tailStart returns the index of the first line of a query's trailing
// `| sort` / `| limit` suffix (len(lines) when there is none). Every catalog
// query ends with that suffix by convention, and narrowing stages must go
// before it so the filter applies before the cap truncates.
func tailStart(lines []string) int {
	cut := len(lines)
	for cut > 0 {
		t := strings.TrimSpace(lines[cut-1])
		if !strings.HasPrefix(t, "| sort") && !strings.HasPrefix(t, "| limit") {
			break
		}
		cut--
	}
	return cut
}

// InjectStages inserts pipeline stages before a query's sort/limit tail.
// Filter stages are legal anywhere, and the tail position filters the exact
// fields the columns (and the facet picker) present — post-summarize, in
// views that aggregate.
func InjectStages(dql string, stages []string) string {
	if len(stages) == 0 {
		return dql
	}
	lines := strings.Split(dql, "\n")
	cut := tailStart(lines)
	out := make([]string, 0, len(lines)+len(stages))
	out = append(out, lines[:cut]...)
	out = append(out, stages...)
	out = append(out, lines[cut:]...)
	return strings.Join(out, "\n")
}

// InjectSearches inserts one full-text stage per term directly after the
// source command (always the query's first line by catalog convention) —
// DQL rejects `search` after transforming commands like parse, expand, or
// summarize (SEARCH_COMMAND_NOT_ALLOWED_AFTER), but chains of search stages
// compose as AND (both validated live).
func InjectSearches(dql string, terms []string) string {
	if len(terms) == 0 {
		return dql
	}
	lines := strings.Split(dql, "\n")
	out := make([]string, 0, len(lines)+len(terms))
	out = append(out, lines[0])
	for _, t := range terms {
		out = append(out, SearchStage(t))
	}
	out = append(out, lines[1:]...)
	return strings.Join(out, "\n")
}

// --- buckets ----------------------------------------------------------------

// BucketField is Grail's physical-partition metadata field. Every record in a
// bucket-backed table carries it and it is queryable at any point before an
// aggregation, but responses only include it once a `fieldsAdd
// dt.system.bucket` projects it (validated live). Buckets are physical data
// separation — the primary Grail narrowing axis — so eligible views project
// the field into every record and the facet manager pins it first.
const BucketField = "dt.system.bucket"

// bucketFieldStage projects the record's bucket into the response.
const bucketFieldStage = "| fieldsAdd " + BucketField

// bucketTables are the Grail tables whose records live in buckets — the
// dt.system.table values of `fetch dt.system.buckets` (validated live).
// Referencing dt.system.bucket on any other table fails the whole query with
// FIELD_DOES_NOT_EXIST (validated live on dt.entity.*, dt.system.buckets and
// dt.semantic_dictionary.*) — not null — so eligibility is a whitelist.
var bucketTables = map[string]bool{
	"logs": true, "events": true, "spans": true, "bizevents": true,
	"metrics": true, "security.events": true, "application.snapshots": true,
	"user.events": true, "user.sessions": true, "user.replays": true,
	"dt.system.events": true,
}

// BucketEligible reports whether a query's records carry dt.system.bucket:
// the source fetches a bucket-backed table — including the dt.davis.* and
// dt.synthetic.* views over events (validated live) — and no later stage
// drops the field (summarize aggregates it away, a fields projection
// excludes it).
func BucketEligible(dql string) bool {
	lines := strings.Split(dql, "\n")
	table, ok := strings.CutPrefix(strings.TrimSpace(lines[0]), "fetch ")
	if !ok {
		return false
	}
	table = strings.TrimSpace(strings.SplitN(table, ",", 2)[0])
	if !bucketTables[table] && !strings.HasPrefix(table, "dt.davis.") && !strings.HasPrefix(table, "dt.synthetic.") {
		return false
	}
	for _, l := range lines[1:] {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "| summarize") || strings.HasPrefix(t, "| fields ") {
			return false
		}
	}
	return true
}

// ComposeQuery renders a view's final list query from its narrowing state:
// search terms directly after the source (InjectSearches), then — on
// bucket-eligible queries — the bucket facets, whose filter prunes physical
// reads at the source and survives transforming stages the tail sits after,
// then the bucket projection (projectBucket; off for API views, whose query
// is analyzer input rather than the records the table shows), and the
// remaining facets before the sort/limit tail as usual. The stage order
// source → search → bucket filter → fieldsAdd is validated live.
func ComposeQuery(dql string, searches []string, facets []Facet, projectBucket bool) string {
	var head, tail []string
	eligible := BucketEligible(dql)
	for _, f := range facets {
		if eligible && f.Field == BucketField {
			head = append(head, f.Stage())
		} else {
			tail = append(tail, f.Stage())
		}
	}
	if eligible && projectBucket {
		head = append(head, bucketFieldStage)
	}
	out := InjectSearches(dql, searches)
	if len(head) > 0 {
		lines := strings.Split(out, "\n")
		at := 1 + len(searches)
		spliced := make([]string, 0, len(lines)+len(head))
		spliced = append(spliced, lines[:at]...)
		spliced = append(spliced, head...)
		spliced = append(spliced, lines[at:]...)
		out = strings.Join(spliced, "\n")
	}
	return InjectStages(out, tail)
}

// FacetTopValues is how many suggestions the value picker requests.
const FacetTopValues = 25

// FieldsSummaryQuery renders the top-values exploration query for one field:
// the view's query minus its sort/limit tail, narrowed by the active
// searches and every facet except the explored field's own (so re-faceting
// a field offers all its values), summarized server-side.
func FieldsSummaryQuery(dql, field string, searches []string, facets []Facet) string {
	lines := strings.Split(dql, "\n")
	head := InjectSearches(strings.Join(lines[:tailStart(lines)], "\n"), searches)
	var b strings.Builder
	b.WriteString(head)
	for _, f := range facets {
		if f.Field != field {
			b.WriteString("\n" + f.Stage())
		}
	}
	fmt.Fprintf(&b, "\n| fieldsSummary %s, topValues: %d", escapeField(field), FacetTopValues)
	return b.String()
}

// FacetValue is one fieldsSummary suggestion.
type FacetValue struct {
	Value string // stringified — drops straight into Facet.Value
	Count string
}

// ParseFieldsSummary extracts the top values from a fieldsSummary result
// (shape validated live: one record per field, values: [{value, count}]).
func ParseFieldsSummary(records []map[string]any) []FacetValue {
	if len(records) == 0 {
		return nil
	}
	values, _ := records[0]["values"].([]any)
	out := make([]FacetValue, 0, len(values))
	for _, v := range values {
		m, _ := v.(map[string]any)
		if m == nil {
			continue
		}
		out = append(out, FacetValue{Value: FormatValue(m["value"]), Count: Str(m, "count")})
	}
	return out
}
