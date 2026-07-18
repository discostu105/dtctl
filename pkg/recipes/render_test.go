package recipes

import (
	"strings"
	"testing"

	"github.com/dynatrace-oss/dtctl/pkg/util/template"
)

func strPtr(s string) *string { return &s }

func testRecipe() *Recipe {
	return &Recipe{
		Params: map[string]*Param{
			"namespace": {Default: strPtr("")},
			"timeframe": {Type: TypeDuration, Default: strPtr("now()-2h")},
			"limit":     {Type: TypeInt, Default: strPtr("50")},
		},
		DQL: `fetch logs, from:{{.timeframe}}
{{- if .namespace }}
| filter k8s.namespace.name == {{.namespace | dqlString}}
{{- end }}
| limit {{.limit}}`,
	}
}

func TestRenderDefaults(t *testing.T) {
	rendered, err := Render(testRecipe(), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(rendered.DQL, "namespace") {
		t.Errorf("empty default should skip the branch:\n%s", rendered.DQL)
	}
	if !strings.Contains(rendered.DQL, "from:now()-2h") || !strings.Contains(rendered.DQL, "limit 50") {
		t.Errorf("defaults not applied:\n%s", rendered.DQL)
	}
	if rendered.Provenance != ParamsProvenanceDefault {
		t.Errorf("provenance = %q", rendered.Provenance)
	}
}

func TestRenderEscapesStrings(t *testing.T) {
	rendered, err := Render(testRecipe(), map[string]string{"namespace": `pay"ments | fieldsRemove content`})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(rendered.DQL, `== "pay\"ments | fieldsRemove content"`) {
		t.Errorf("string param not escaped into a quoted literal:\n%s", rendered.DQL)
	}
	if rendered.Provenance == ParamsProvenanceDefault || len(rendered.Provenance) != 8 {
		t.Errorf("non-default provenance = %q", rendered.Provenance)
	}
}

func TestRenderRejectsBadValues(t *testing.T) {
	cases := map[string]map[string]string{
		"int injection":      {"limit": "100 | fieldsRemove content"},
		"duration injection": {"timeframe": `now()-2h" | limit 1 | filter a == "`},
		"unknown key":        {"namepace": "x"},
	}
	for name, set := range cases {
		if _, err := Render(testRecipe(), set); err == nil {
			t.Errorf("%s: expected error for %v", name, set)
		}
	}
}

func TestRenderRequiredParams(t *testing.T) {
	r := &Recipe{
		Params: map[string]*Param{"id": {Description: "entity id"}},
		DQL:    "fetch logs | filter x == {{.id | dqlString}}",
	}
	if _, err := Render(r, nil); err == nil || !strings.Contains(err.Error(), "requires params: id") {
		t.Errorf("expected required-param error, got %v", err)
	}
	if got := RequiredParams(r); len(got) != 1 || got[0] != "id" {
		t.Errorf("RequiredParams = %v", got)
	}
	if _, err := Render(r, map[string]string{"id": "SERVICE-1"}); err != nil {
		t.Errorf("Render with required param: %v", err)
	}
}

func TestDQLStringFunc(t *testing.T) {
	got := template.DQLString(`a"b\c
d`)
	want := `"a\"b\\c\nd"`
	if got != want {
		t.Errorf("DQLString = %s, want %s", got, want)
	}
}

func TestProvenanceStable(t *testing.T) {
	a := provenance(map[string]string{"a": "1", "b": "2"})
	b := provenance(map[string]string{"b": "2", "a": "1"})
	if a != b {
		t.Errorf("provenance not order-independent: %s vs %s", a, b)
	}
}
