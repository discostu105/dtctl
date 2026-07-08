package catalog

import (
	"fmt"
	"regexp"
	"strings"
)

// Grail data explorer: dt.system.data_objects (tables & views),
// dt.system.buckets, and dt.system.files (lookup data), each drilling into
// the generic record sampler. Facts validated live: tables vs views are told
// apart by the type column (there is no views column); the metrics data
// object is fieldsSnapshot-only and cannot be fetched; dt.system.buckets uses
// snake_case columns and holds no status (that lives in the REST bucket API);
// lookup files load with load "<absolute-path>" — double quotes, leading
// slash, no scheme; dt.system.bucket is a queryable field on event tables so
// a bucket row drills into its records. All three metadata tables are
// timeframe-free.

var tablesSpec = &Spec{
	Name:    "tables",
	Aliases: []string{"tbl", "data", "dataobjects"},
	Kind:    KindEntity,
	Desc:    "Grail data objects — enter samples a table's records",
	Query: func(s Scope) string {
		var b strings.Builder
		b.WriteString("fetch dt.system.data_objects")
		if l := lensAt(tableLenses, s.Lens); l.Filter != "" {
			fmt.Fprintf(&b, "\n| filter %s", l.Filter)
		}
		b.WriteString("\n| sort name asc\n| limit 500")
		return b.String()
	},
	Lenses: tableLenses,
	Columns: []Column{
		{Title: "NAME", Width: 40, Field: "name"},
		{Title: "KIND", Width: 5, Field: "type", Class: func(val string) string {
			if val == "view" {
				return "dim"
			}
			return ""
		}},
		{Title: "DISPLAY NAME", Width: 24, Field: "display_name"},
		{Title: "DESCRIPTION", Field: "description"},
	},
	EnterTarget: "records",
	EnterArg: func(rec map[string]any) string {
		// The metrics data object is fieldsSnapshot-only (validated live) —
		// refusing here beats a guaranteed query error.
		if usable, ok := rec["usable_with"].([]any); ok {
			for _, u := range usable {
				if u == "fetch" {
					return Str(rec, "name")
				}
			}
			return ""
		}
		return Str(rec, "name")
	},
	Drills: map[string]string{},
}

var tableLenses = []Lens{
	{Name: "all", Desc: "tables and views"},
	{Name: "tables", Desc: "physical tables only", Filter: `type == "table"`},
	{Name: "views", Desc: "views only", Filter: `type == "view"`},
}

var bucketsSpec = &Spec{
	Name:    "buckets",
	Aliases: []string{"bucket", "bkt"},
	Kind:    KindEntity,
	Desc:    "Grail buckets — retention, size, records; enter samples a bucket",
	Query: func(s Scope) string {
		return `fetch dt.system.buckets
| sort name asc
| limit 200`
	},
	Columns: []Column{
		{Title: "NAME", Width: 34, Field: "name"},
		{Title: "TABLE", Width: 18, Field: "dt.system.table"},
		{Title: "CLASS", Width: 6, Field: "dt.bucket.class"},
		{Title: "RECORDS", Width: 15, Right: true, Value: func(rec map[string]any) string {
			return FormatCount(rec["records"])
		}, Sort: func(rec map[string]any) any { return rec["records"] }},
		{Title: "SIZE", Width: 9, Right: true, Value: func(rec map[string]any) string {
			return FormatBytesStr(Str(rec, "estimated_uncompressed_bytes"))
		}, Sort: func(rec map[string]any) any { return rec["estimated_uncompressed_bytes"] }},
		{Title: "RETENTION", Width: 9, Right: true, Value: func(rec map[string]any) string {
			if d := Str(rec, "retention_days"); d != "" {
				return d + "d"
			}
			return ""
		}, Sort: func(rec map[string]any) any { return rec["retention_days"] }},
		{Title: "DISPLAY NAME", Field: "display_name"},
	},
	EnterTarget: "records",
	EnterArg: func(rec map[string]any) string {
		table, bucket := Str(rec, "dt.system.table"), Str(rec, "name")
		if table == "" || bucket == "" || table == "metrics" {
			// The metrics data object is fieldsSnapshot-only — fetch would
			// hard-fail (same refusal the tables view gives).
			return ""
		}
		return table + "@" + bucket
	},
	Drills: map[string]string{},
}

var filesSpec = &Spec{
	Name:    "files",
	Aliases: []string{"file", "lookups", "lookup"},
	Kind:    KindEntity,
	Desc:    "Grail files (lookup data) — enter loads a file's rows",
	Query: func(s Scope) string {
		return `fetch dt.system.files
| sort name asc
| limit 200`
	},
	Columns: []Column{
		{Title: "PATH", Width: 36, Field: "name"},
		{Title: "DISPLAY NAME", Width: 20, Field: "display_name"},
		{Title: "TYPE", Width: 14, Field: "type"},
		{Title: "KEY", Width: 12, Field: "lookup_field"},
		{Title: "RECORDS", Width: 8, Right: true, Value: func(rec map[string]any) string {
			return FormatCount(rec["records"])
		}, Sort: func(rec map[string]any) any { return rec["records"] }},
		{Title: "SIZE", Width: 9, Right: true, Value: func(rec map[string]any) string {
			return FormatBytesStr(Str(rec, "size"))
		}, Sort: func(rec map[string]any) any { return rec["size"] }},
		{Title: "MODIFIED", Width: 8, Right: true, Value: func(rec map[string]any) string {
			return Age(Str(rec, "modified.timestamp"))
		}, Sort: func(rec map[string]any) any { return Str(rec, "modified.timestamp") }},
		{Title: "DESCRIPTION", Field: "description"},
	},
	EnterTarget: "records",
	EnterArg: func(rec map[string]any) string {
		if name := Str(rec, "name"); name != "" {
			return "load:" + name
		}
		return ""
	},
	Drills: map[string]string{},
}

// samplerArg matches the record sampler's safe argument alphabet — table
// names, bucket names, and file paths come from server records and are
// composed into DQL, so anything else is refused.
var samplerArg = regexp.MustCompile(`^[A-Za-z0-9._@/:-]+$`)

// recordsSpec is the generic record sampler behind the explorer drills: it
// fetches a page of any table (optionally one bucket's slice) or loads a
// lookup file, and derives its columns from whatever comes back.
var recordsSpec = &Spec{
	Name:    "records",
	Aliases: []string{"rec", "sample"},
	Kind:    KindSignal,
	Desc:    "Record sampler — rows of one table, bucket, or file",
	Dynamic: true,
	Query: func(s Scope) string {
		arg := s.Arg
		if !samplerArg.MatchString(arg) {
			arg = ""
		}
		switch {
		case strings.HasPrefix(arg, "load:"):
			return fmt.Sprintf("load %q\n| limit 200", strings.TrimPrefix(arg, "load:"))
		case strings.Contains(arg, "@"):
			parts := strings.SplitN(arg, "@", 2)
			return fmt.Sprintf("fetch %s\n| filter dt.system.bucket == %q\n| limit 200", parts[0], parts[1])
		case arg != "":
			return fmt.Sprintf("fetch %s\n| limit 200", arg)
		default:
			// Opened without an argument (command bar): browse the catalog of
			// tables instead of failing — enter there samples one.
			return "fetch dt.system.data_objects\n| sort name asc\n| limit 200"
		}
	},
	Columns: []Column{{Title: "RECORD", Field: ""}},
	Trace: func(rec map[string]any) string {
		if id := Str(rec, "trace.id"); id != "" {
			return id
		}
		return Str(rec, "trace_id")
	},
	Drills: map[string]string{"s": "trace"},
}

// FormatCount renders a count field (string or number) with thousands
// separators.
func FormatCount(v any) string {
	f, ok := FloatValue(v)
	if !ok {
		return FormatValue(v)
	}
	n := int64(f)
	s := fmt.Sprintf("%d", n)
	if n < 1000 {
		// Also keeps the sign out of the grouping loop below (counts are
		// never negative, but a bad value must not render "-,123").
		return s
	}
	if n < 0 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	return s + "," + strings.Join(parts, ",")
}
