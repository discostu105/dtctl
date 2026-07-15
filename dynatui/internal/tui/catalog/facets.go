package catalog

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

// Server-side narrowing for table views: the '/'-promoted full-text search
// and the 'f' facet picker.
//
// The injectors below rewrite generated DQL by LINE CONVENTION rather than a
// structured pipeline type: the source command owns the first line and every
// query ends in a `| sort`/`| limit` tail. convention_test.go enforces both
// on every spec, so a Query closure that drifts fails the suite instead of
// silently mis-splicing here.
//
// Validated live (see docs/dev/learnings.md §1.10):
//   - `| search "text"` matches case-insensitively across all fields on
//     fetch and smartscapeNodes pipelines alike, but is rejected after
//     transforming commands — parse/expand/summarize —
//     (SEARCH_COMMAND_NOT_ALLOWED_AFTER), while filter and fieldsRemove
//     before it are fine. It therefore injects directly after the source.
//   - Facet stages never wrap the field in toString() — a function call on
//     the field mutes Grail's index features. Stage() instead branches on
//     the shape of the (always stringified — fieldsSummary) value.
//   - matchesValue supports leading/trailing '*' wildcards, case-insensitive,
//     applies element-wise to string arrays, and never matches non-string
//     fields (warning, not error).

// Facet is one attribute filter: an exact value, a '*' wildcard pattern, or
// — with Tokens — a `~` token search, the only operator that matches inside
// arrays of records or numbers server-side.
type Facet struct {
	Field  string
	Value  string
	Tokens bool `json:",omitempty"`
}

// Pattern reports whether the facet value is a wildcard pattern.
func (f Facet) Pattern() bool { return !f.Tokens && strings.Contains(f.Value, "*") }

// Value shapes that pick the exact-facet encoding in Stage. fieldsSummary
// hands every value over stringified, so the field's server-side type is
// unknowable here; the shape of the value decides which typed comparisons
// can match it (all validated live).
var (
	smartscapeIDShape = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-[0-9A-F]{16}$`)
	hexShape          = regexp.MustCompile(`^[0-9a-fA-F]{16,64}$`) // span.id (16), trace.id (32)
	uuidShape         = regexp.MustCompile(`^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$`)
	integerShape      = regexp.MustCompile(`^-?[0-9]+$`)
	decimalShape      = regexp.MustCompile(`^-?[0-9]+\.[0-9]+$`)
)

// idShaped reports whether the value reads as a typed scalar's string
// representation — smartscape ID, UID hex, UUID, or IP address. Those field
// types never equal a string literal (`==` is silently empty), but `~`
// auto-converts and matches their exact representation.
func idShaped(v string) bool {
	return smartscapeIDShape.MatchString(v) || hexShape.MatchString(v) ||
		uuidShape.MatchString(v) || net.ParseIP(v) != nil
}

// Stage renders the facet as a DQL filter stage. Every encoding compares the
// bare field — wrapping it in toString() mutes Grail's indexes — and covers
// each field type the stringified value could have come from:
//   - Tokens          → field ~ "v": token search, the only operator that
//     matches inside arrays of records or numbers.
//   - '*' patterns    → matchesValue(field, "v"): whole-value wildcards,
//     element-wise on string arrays; never matches non-string fields.
//   - true/false      → == true or == "true" (`~` never matches booleans).
//   - ID-shaped       → == "v" or ~ "v" (typed scalars need the `~` leg).
//   - integers        → == 2 or == "2" or == duration(2, "ns") — the
//     duration leg matches duration fields, whose fieldsSummary values are
//     nanosecond counts that toString() never matched.
//   - decimals        → == 2.5 or == "2.5"
//   - other strings   → == "v" alone: exact and case-sensitive. No `~` leg —
//     it is case-insensitive and token-based, so "Running" would also match
//     "Running fast" (validated live).
func (f Facet) Stage() string {
	field := escapeField(f.Field)
	v := f.Value
	switch {
	case f.Tokens:
		return fmt.Sprintf("| filter %s ~ %q", field, v)
	case f.Pattern():
		return fmt.Sprintf("| filter matchesValue(%s, %q)", field, v)
	case v == "true" || v == "false":
		return fmt.Sprintf("| filter %s == %s or %s == %q", field, v, field, v)
	case idShaped(v):
		return fmt.Sprintf("| filter %s == %q or %s ~ %q", field, v, field, v)
	case integerShape.MatchString(v):
		return fmt.Sprintf(`| filter %s == %s or %s == %q or %s == duration(%s, "ns")`, field, v, field, v, field, v)
	case decimalShape.MatchString(v):
		return fmt.Sprintf("| filter %s == %s or %s == %q", field, v, field, v)
	default:
		return fmt.Sprintf("| filter %s == %q", field, v)
	}
}

// Label is the compact pill text shown in the table header.
func (f Facet) Label() string {
	if f.Tokens || f.Pattern() {
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

// sourceTable returns the table a query's first line fetches ("" when the
// source is not a fetch command).
func sourceTable(dql string) string {
	first := strings.TrimSpace(strings.SplitN(dql, "\n", 2)[0])
	table, ok := strings.CutPrefix(first, "fetch ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(table, ",", 2)[0])
}

// BucketEligible reports whether a query's records carry dt.system.bucket:
// the source fetches a bucket-backed table — including the dt.davis.* and
// dt.synthetic.* views over events (validated live) — and no later stage
// drops the field (summarize aggregates it away, a fields projection
// excludes it).
func BucketEligible(dql string) bool {
	table := sourceTable(dql)
	if table == "" {
		return false
	}
	if !bucketTables[table] && !strings.HasPrefix(table, "dt.davis.") && !strings.HasPrefix(table, "dt.synthetic.") {
		return false
	}
	for _, l := range strings.Split(dql, "\n")[1:] {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "| summarize") || strings.HasPrefix(t, "| fields ") {
			return false
		}
	}
	return true
}

// ComposeQuery renders a view's final list query from its narrowing state:
// search terms directly after the source (InjectSearches), then — on
// bucket-eligible queries — the bucket facet. A single exact bucket facet on
// a real table becomes the fetch command's bucket: parameter, Grail's
// native physical-read pruning; the dt.davis.*/dt.synthetic.* views reject
// that parameter (PARAMETER_NOT_ALLOWED_FOR_FETCH_VIEW, validated live) and
// keep a head filter stage instead, as do multiple bucket facets (whose AND
// semantics a multi-value parameter would flip to OR). Then the bucket
// projection (projectBucket; off for API views, whose query is analyzer
// input rather than the records the table shows), and the remaining facets
// before the sort/limit tail as usual. The stage order
// source → search → bucket filter → fieldsAdd is validated live.
func ComposeQuery(dql string, searches []string, facets []Facet, projectBucket bool) string {
	var bucketFacets []Facet
	var tail []string
	eligible := BucketEligible(dql)
	for _, f := range facets {
		if eligible && f.Field == BucketField {
			bucketFacets = append(bucketFacets, f)
		} else {
			tail = append(tail, f.Stage())
		}
	}
	var head []string
	if len(bucketFacets) == 1 && !bucketFacets[0].Pattern() && !bucketFacets[0].Tokens && bucketTables[sourceTable(dql)] {
		lines := strings.SplitN(dql, "\n", 2)
		lines[0] += fmt.Sprintf(", bucket:{%q}", bucketFacets[0].Value)
		dql = strings.Join(lines, "\n")
	} else {
		for _, f := range bucketFacets {
			head = append(head, f.Stage())
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
