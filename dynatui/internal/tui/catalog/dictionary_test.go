package catalog

import (
	"strings"
	"testing"
)

func TestDictionaryLenses(t *testing.T) {
	// One view, lensed: models first, field definitions behind the other
	// lenses — the same tab strip as traces/sessions.
	models := dictionarySpec.Query(Scope{Timeframe: DefaultTimeframe})
	if !strings.Contains(models, "dt.semantic_dictionary.models") || strings.Contains(models, "| expand") {
		t.Errorf("default lens must list models:\n%s", models)
	}
	dql := dictionarySpec.Query(Scope{Timeframe: DefaultTimeframe, Arg: "span", Lens: DictFieldsLens})
	for _, want := range []string{
		`| filter name == "span"`,
		"| expand fields",
		"kind: leftOuter", // 9 of span's 70 declared fields have no fields row
	} {
		if !strings.Contains(dql, want) {
			t.Errorf("fields(model) query must contain %q:\n%s", want, dql)
		}
	}
	flat := dictionarySpec.Query(Scope{Timeframe: DefaultTimeframe, Lens: 4})
	if !strings.Contains(flat, `stability == "deprecated"`) {
		t.Errorf("stability lens must filter:\n%s", flat)
	}
	// A stale Arg on the models lens narrows to that model — the crumb must
	// never claim a scope the query dropped.
	one := dictionarySpec.Query(Scope{Timeframe: DefaultTimeframe, Arg: "span"})
	if !strings.Contains(one, `| filter name == "span"`) {
		t.Errorf("models lens must apply a lingering Arg:\n%s", one)
	}
	// The old view names keep resolving (aliases; stale history entries).
	for _, name := range []string{"models", "fields", "dict", "semdict"} {
		if Lookup(name) != dictionarySpec {
			t.Errorf("Lookup(%q) must resolve to the dictionary", name)
		}
	}
}
