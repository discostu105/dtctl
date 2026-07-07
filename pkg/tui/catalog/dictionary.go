package catalog

import (
	"fmt"
	"strings"
)

// Semantic dictionary explorer: dt.semantic_dictionary.models and .fields.
// Facts validated live: fields.model_id is null on EVERY record — the real
// join is the reverse direction (models.fields is a string array of field
// names; expand it and join on fields.name, kind:leftOuter so the ~13% of
// declared names without a fields row survive). No model is literally named
// "spans"/"logs": model names are span/http/log.general, the Grail table
// lives in data_object. Both tables are timeframe-free metadata.

var modelsSpec = &Spec{
	Name:    "models",
	Aliases: []string{"model", "dict", "dictionary"},
	Kind:    KindEntity,
	Desc:    "Semantic dictionary models — enter lists a model's fields",
	Query: func(s Scope) string {
		return `fetch dt.semantic_dictionary.models
| sort name asc
| limit 1000`
	},
	Columns: []Column{
		{Title: "MODEL", Field: "name", Width: 34},
		{Title: "DATA OBJECT", Width: 26, Value: func(rec map[string]any) string {
			// Five link-models carry the literal string "null" (validated
			// live) — render those as empty, not as a fake table name.
			if v := Str(rec, "data_object"); v != "null" {
				return v
			}
			return ""
		}},
		{Title: "FIELDS", Width: 6, Right: true, Value: func(rec map[string]any) string {
			if arr, ok := rec["fields"].([]any); ok {
				return fmt.Sprintf("%d", len(arr))
			}
			return ""
		}, Sort: func(rec map[string]any) any {
			arr, _ := rec["fields"].([]any)
			return float64(len(arr))
		}},
		{Title: "TITLE", Width: 24, Field: "title"},
		{Title: "DESCRIPTION", Field: "description"},
	},
	EnterTarget: "fields",
	EnterArg:    func(rec map[string]any) string { return Str(rec, "name") },
	Drills:      map[string]string{},
}

var fieldsSpec = &Spec{
	Name:    "fields",
	Aliases: []string{"field", "fld"},
	Kind:    KindEntity,
	Desc:    "Semantic dictionary fields — descriptions, types, stability",
	Query: func(s Scope) string {
		if s.Arg != "" {
			// One model's fields: expand the model's declared names and
			// left-join their definitions (enter on a models row).
			var b strings.Builder
			fmt.Fprintf(&b, "fetch dt.semantic_dictionary.models\n| filter name == %q", s.Arg)
			b.WriteString("\n| expand fields\n| fields field_name = fields")
			b.WriteString("\n| join [fetch dt.semantic_dictionary.fields], kind: leftOuter, on: { left[field_name] == right[name] }, fields: {name, type, stability, unit, description, examples, supported_values, tags}")
			if l := fieldLensAt(s.Lens); l.Filter != "" {
				fmt.Fprintf(&b, "\n| filter %s", l.Filter)
			}
			b.WriteString("\n| sort field_name asc\n| limit 500")
			return b.String()
		}
		var b strings.Builder
		b.WriteString("fetch dt.semantic_dictionary.fields\n| fieldsAdd field_name = name")
		if l := fieldLensAt(s.Lens); l.Filter != "" {
			fmt.Fprintf(&b, "\n| filter %s", l.Filter)
		}
		// The table holds ~1400 records and the executor caps at 1000 — an
		// honest limit; '/' search narrows server-side.
		b.WriteString("\n| sort name asc\n| limit 1000")
		return b.String()
	},
	Lenses: fieldLenses,
	Columns: []Column{
		{Title: "FIELD", Width: 40, Field: "field_name"},
		{Title: "TYPE", Width: 10, Field: "type"},
		{Title: "STABILITY", Width: 12, Field: "stability", Class: classStability},
		{Title: "UNIT", Width: 6, Field: "unit"},
		{Title: "DESCRIPTION", Field: "description"},
	},
	EnterArg: nil, // enter opens the record inspector (examples, enums, tags)
	Drills:   map[string]string{},
}

var fieldLenses = []Lens{
	{Name: "all", Desc: "every field definition"},
	{Name: "stable", Desc: "stable fields only", Filter: `stability == "stable"`},
	{Name: "experimental", Desc: "experimental fields only", Filter: `stability == "experimental"`},
	{Name: "deprecated", Desc: "deprecated fields only", Filter: `stability == "deprecated"`},
}

func fieldLensAt(i int) Lens {
	if i < 0 || i >= len(fieldLenses) {
		i = 0
	}
	return fieldLenses[i]
}

func classStability(val string) string {
	switch val {
	case "stable":
		return "ok"
	case "deprecated":
		return "warn"
	case "experimental":
		return "dim"
	}
	return ""
}
