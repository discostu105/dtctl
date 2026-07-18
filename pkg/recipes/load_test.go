package recipes

import (
	"os"
	"path/filepath"
	"testing"
)

// The committed concept examples are the schema spec — loading them directly
// keeps one source of truth (a schema change that breaks them must fail here).
const examplesDir = "../../docs/dev/examples/recipes"

func TestLoadPackExamples(t *testing.T) {
	pack, err := LoadPack(filepath.Join(examplesDir, "base-pack.example.yaml"))
	if err != nil {
		t.Fatalf("LoadPack: %v", err)
	}
	if pack.Metadata.Name != "dynatrace-recipes" || pack.Metadata.Version != "2026.07" {
		t.Errorf("unexpected metadata: %+v", pack.Metadata)
	}
	if len(pack.Recipes) != 3 {
		t.Errorf("expected 3 recipes, got %d", len(pack.Recipes))
	}
	se := pack.Recipes["service-errors"]
	if se == nil || se.Verify == nil || se.Verify.Expect != "records" {
		t.Fatalf("service-errors verify spec not parsed: %+v", se)
	}
	if se.Params["sampling"].Type != TypeInt {
		t.Errorf("sampling param type = %q", se.Params["sampling"].Type)
	}
	if def := pack.Capabilities["security-rap"]; def == nil || def.Probe == "" || def.Window != "24h" {
		t.Errorf("probe capability definition not parsed: %+v", def)
	}
}

func TestLoadFullPack(t *testing.T) {
	pack, err := LoadPack(filepath.Join(examplesDir, "base-pack.full.example.yaml"))
	if err != nil {
		t.Fatalf("LoadPack full: %v", err)
	}
	if len(pack.Recipes) < 50 {
		t.Errorf("expected >=50 recipes in the full pack, got %d", len(pack.Recipes))
	}
	if len(pack.Capabilities) < 15 {
		t.Errorf("expected >=15 capability definitions, got %d", len(pack.Capabilities))
	}
	// The drill graph is serialized as per-recipe followups.
	edges := 0
	for _, r := range pack.Recipes {
		edges += len(r.Followups)
	}
	if edges < 50 {
		t.Errorf("expected the followup drill graph (>=50 edges), got %d", edges)
	}
}

func TestLoadBookExample(t *testing.T) {
	book, err := LoadBook(filepath.Join(examplesDir, "recipebook.example.yaml"))
	if err != nil {
		t.Fatalf("LoadBook: %v", err)
	}
	if book.Kind != KindBook {
		t.Errorf("kind = %q", book.Kind)
	}
	if len(book.Recipes) == 0 || len(book.Disabled) != 3 {
		t.Fatalf("recipes=%d disabled=%d", len(book.Recipes), len(book.Disabled))
	}
	if book.Disabled["frontend-service-logs"].Class != DisabledClassProbeEmpty {
		t.Errorf("disabled class = %q", book.Disabled["frontend-service-logs"].Class)
	}
	if book.Facts.FieldCarriage["logs"]["dt.smartscape.service"] != 0.20 {
		t.Errorf("carriage not parsed: %v", book.Facts.FieldCarriage["logs"])
	}
	if book.Scoping["SERVICE"]["logs"].Hop != "runs_on" {
		t.Errorf("scoping hop not parsed")
	}
	local := book.Recipes["audit-log-search"]
	if local == nil || local.Source != "" || len(local.Segments) != 1 {
		t.Errorf("local recipe not parsed: %+v", local)
	}
	// Referencing form entries carry a stamp with params provenance.
	if lr := book.Recipes["pods-restarting"].LastRun; lr == nil || lr.Params != ParamsProvenanceDefault {
		t.Errorf("stamp not parsed: %+v", lr)
	}
}

func TestLoadGeneratedBooks(t *testing.T) {
	for _, f := range []string{
		"recipebook.demo.generated.yaml", "recipebook.dev.generated.yaml",
		"recipebook.large.generated.yaml", "recipebook.netobs.generated.yaml",
		"recipebook.playground.generated.yaml",
	} {
		if _, err := LoadBook(filepath.Join(examplesDir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}

func TestLibraryResolution(t *testing.T) {
	configDir := t.TempDir()
	if _, err := InstallPack(configDir, filepath.Join(examplesDir, "base-pack.example.yaml")); err != nil {
		t.Fatalf("InstallPack: %v", err)
	}
	// Immutability: re-install is a no-op, not an error.
	if _, err := InstallPack(configDir, filepath.Join(examplesDir, "base-pack.example.yaml")); err != nil {
		t.Fatalf("re-InstallPack: %v", err)
	}

	book := &Book{
		APIVersion: APIVersion,
		Kind:       KindBook,
		Metadata: BookMetadata{
			Name:  "test",
			Packs: []PackRef{{Name: "dynatrace-recipes", Version: "2026.07"}},
		},
		Recipes: map[string]*Recipe{
			// referencing entry with a book-key different from the pack recipe name
			"my-logs": {Source: "pack:entity-logs@2026.07", Description: "override desc"},
			"local-one": {Description: "local", DQL: "fetch logs | limit 1"},
		},
		Disabled: map[string]*Disabled{
			"rum-errors": {Class: DisabledClassGuard, Reason: "requires: [rum] — capability absent"},
		},
	}
	if err := os.MkdirAll(filepath.Join(configDir, "recipes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteBook(BookPath(configDir, "test"), book); err != nil {
		t.Fatalf("WriteBook: %v", err)
	}

	lib, err := LoadLibrary(configDir, "test")
	if err != nil {
		t.Fatalf("LoadLibrary: %v", err)
	}
	if len(lib.Packs) != 1 {
		t.Fatalf("packs = %d", len(lib.Packs))
	}

	// Book entry resolves its body from the pack, book fields win.
	res, err := lib.Lookup("my-logs")
	if err != nil {
		t.Fatalf("Lookup my-logs: %v", err)
	}
	if res.Recipe.DQL == "" || res.Recipe.Description != "override desc" {
		t.Errorf("merge failed: dql=%q desc=%q", res.Recipe.DQL, res.Recipe.Description)
	}
	if res.Local || !res.InBook {
		t.Errorf("classification: local=%v inBook=%v", res.Local, res.InBook)
	}

	// Local recipe.
	res, err = lib.Lookup("local-one")
	if err != nil || !res.Local {
		t.Errorf("local lookup: %v local=%v", err, res != nil && res.Local)
	}

	// Disabled recipe still resolvable, with evidence attached.
	res, err = lib.Lookup("rum-errors")
	if err != nil {
		t.Fatalf("Lookup disabled: %v", err)
	}
	if res.Disabled == nil || res.Recipe.DQL == "" {
		t.Errorf("disabled resolution: %+v", res)
	}

	// Pack-only recipe (not classified by the book) resolves unstamped.
	res, err = lib.Lookup("service-errors")
	if err != nil || res.InBook {
		t.Errorf("pack-only lookup: err=%v inBook=%v", err, res != nil && res.InBook)
	}

	if _, err := lib.Lookup("does-not-exist"); err == nil {
		t.Error("expected error for unknown recipe")
	}

	// All(): book entries + local + pack-only, disabled excluded.
	names := map[string]bool{}
	for _, r := range lib.All() {
		names[r.Name] = true
	}
	if !names["my-logs"] || !names["local-one"] || !names["service-errors"] || names["rum-errors"] {
		t.Errorf("All() = %v", names)
	}
}
