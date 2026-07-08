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

// DictFieldsLens is the index of the fields lens — the model drill (enter on
// a models row) lands there with Scope.Arg set to the model name.
const DictFieldsLens = 1

// dictLenses slice the one dictionary view: the models catalog first, then
// the field definitions with stability cuts — the same tab strip every other
// lensed view uses.
var dictLenses = []Lens{
	{Name: "models", Desc: "semantic models — enter lists a model's fields", Columns: modelColumns},
	{Name: "fields", Desc: "every field definition — enter opens the full record"},
	{Name: "stable", Desc: "stable fields only", Filter: `stability == "stable"`},
	{Name: "experimental", Desc: "experimental fields only", Filter: `stability == "experimental"`},
	{Name: "deprecated", Desc: "deprecated fields only", Filter: `stability == "deprecated"`},
}

var modelColumns = []Column{
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
}

// dictionarySpec is the whole semantic dictionary as ONE view: models and
// fields are lenses of the same table, so the tab strip works exactly like
// traces/sessions. Enter on a model drills into its fields (fields lens,
// Arg = model); enter on a field opens the full definition in the inspector.
var dictionarySpec = &Spec{
	Name:    "dictionary",
	Aliases: []string{"dict", "semdict", "models", "fields", "model", "field"},
	Kind:    KindEntity,
	Desc:    "Semantic dictionary — models and field definitions",
	Query: func(s Scope) string {
		lens := lensAt(dictLenses, s.Lens)
		if lens.Name == "models" {
			var b strings.Builder
			b.WriteString("fetch dt.semantic_dictionary.models")
			if s.Arg != "" {
				// Lens-switching back from a model's fields keeps the Arg —
				// applying it keeps the crumb honest (the list shows exactly
				// the model it claims; esc pops to the full catalog).
				fmt.Fprintf(&b, "\n| filter name == %q", s.Arg)
			}
			b.WriteString("\n| sort name asc\n| limit 1000")
			return b.String()
		}
		if s.Arg != "" {
			// One model's fields: expand the model's declared names and
			// left-join their definitions (enter on a models row).
			var b strings.Builder
			fmt.Fprintf(&b, "fetch dt.semantic_dictionary.models\n| filter name == %q", s.Arg)
			b.WriteString("\n| expand fields\n| fields field_name = fields")
			b.WriteString("\n| join [fetch dt.semantic_dictionary.fields], kind: leftOuter, on: { left[field_name] == right[name] }, fields: {name, type, stability, unit, description, examples, supported_values, tags}")
			if lens.Filter != "" {
				fmt.Fprintf(&b, "\n| filter %s", lens.Filter)
			}
			b.WriteString("\n| sort field_name asc\n| limit 500")
			return b.String()
		}
		var b strings.Builder
		b.WriteString("fetch dt.semantic_dictionary.fields\n| fieldsAdd field_name = name")
		if lens.Filter != "" {
			fmt.Fprintf(&b, "\n| filter %s", lens.Filter)
		}
		// The table holds ~1400 records and the executor caps at 1000 — an
		// honest limit; '/' search narrows server-side.
		b.WriteString("\n| sort name asc\n| limit 1000")
		return b.String()
	},
	Lenses: dictLenses,
	Columns: []Column{
		{Title: "FIELD", Width: 40, Field: "field_name"},
		{Title: "TYPE", Width: 10, Field: "type"},
		{Title: "STABILITY", Width: 12, Field: "stability", Class: classStability},
		{Title: "UNIT", Width: 6, Field: "unit"},
		{Title: "DESCRIPTION", Field: "description"},
	},
	EnterTarget: "model-fields", // sentinel: model rows → fields lens; field rows → inspector
	Drills:      map[string]string{},
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

// --- field documentation (the inspector's on-focus descriptions) ------------

// FieldDoc is one dictionary field definition, cached per session so every
// inspector can explain the field under the cursor.
type FieldDoc struct {
	Description string
	Unit        string
	Stability   string
}

// FieldDocLimit bounds the one-shot dictionary fetch. The table holds ~1400
// definitions (validated live) — comfortably under this.
const FieldDocLimit = 4000

// FieldDocsQuery fetches every field definition, projected small. Timeframe-
// free metadata: one query serves the whole session.
func FieldDocsQuery() string {
	return fmt.Sprintf("fetch dt.semantic_dictionary.fields\n| fields name, description, unit, stability\n| limit %d", FieldDocLimit)
}

// ParseFieldDocs indexes a FieldDocsQuery result by field name.
func ParseFieldDocs(records []map[string]any) map[string]FieldDoc {
	docs := make(map[string]FieldDoc, len(records))
	for _, rec := range records {
		name := Str(rec, "name")
		if name == "" {
			continue
		}
		docs[name] = FieldDoc{
			Description: Str(rec, "description"),
			Unit:        Str(rec, "unit"),
			Stability:   Str(rec, "stability"),
		}
	}
	return docs
}
