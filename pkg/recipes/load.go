package recipes

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// BookPath returns the recipe book path for a context:
// <configDir>/recipes/<context>.yaml. Recipe books in the config dir are
// trusted (explicitly provisioned); repo-local books are never loaded.
func BookPath(configDir, contextName string) string {
	return filepath.Join(configDir, "recipes", contextName+".yaml")
}

// PackDir returns the immutable store path of one pack version:
// <configDir>/packs/<name>/<version>/pack.yaml.
func PackDir(configDir, name, version string) string {
	return filepath.Join(configDir, "packs", name, version)
}

// LoadBook reads and validates a recipe book. A missing file returns
// (nil, nil) — no book is a normal state, not an error.
func LoadBook(path string) (*Book, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read recipe book %s: %w", path, err)
	}
	var book Book
	if err := yaml.Unmarshal(data, &book); err != nil {
		return nil, fmt.Errorf("failed to parse recipe book %s: %w", path, err)
	}
	if book.Kind != "" && book.Kind != KindBook {
		return nil, fmt.Errorf("%s: kind is %q, expected %q", path, book.Kind, KindBook)
	}
	return &book, nil
}

// LoadPack reads and validates a recipe pack file.
func LoadPack(path string) (*Pack, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read recipe pack %s: %w", path, err)
	}
	var pack Pack
	if err := yaml.Unmarshal(data, &pack); err != nil {
		return nil, fmt.Errorf("failed to parse recipe pack %s: %w", path, err)
	}
	if pack.Kind != KindPack {
		return nil, fmt.Errorf("%s: kind is %q, expected %q", path, pack.Kind, KindPack)
	}
	if pack.Metadata.Name == "" || pack.Metadata.Version == "" {
		return nil, fmt.Errorf("%s: pack metadata must carry name and version", path)
	}
	return &pack, nil
}

// InstallPack copies a pack into the immutable store
// (<configDir>/packs/<name>/<version>/pack.yaml). Retained pack versions are
// immutable: an existing version is left untouched (and it is not an error —
// re-installing the same version is a no-op).
func InstallPack(configDir, srcPath string) (*Pack, error) {
	pack, err := LoadPack(srcPath)
	if err != nil {
		return nil, err
	}
	dir := PackDir(configDir, pack.Metadata.Name, pack.Metadata.Version)
	dst := filepath.Join(dir, "pack.yaml")
	if _, err := os.Stat(dst); err == nil {
		return pack, nil // immutable: keep the retained version
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create pack dir: %w", err)
	}
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return nil, fmt.Errorf("failed to install pack: %w", err)
	}
	return pack, nil
}

// InstalledPacks lists all packs in the store, sorted by name then version.
func InstalledPacks(configDir string) ([]*Pack, error) {
	root := filepath.Join(configDir, "packs")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var packs []*Pack
	for _, nameDir := range entries {
		if !nameDir.IsDir() {
			continue
		}
		versions, err := os.ReadDir(filepath.Join(root, nameDir.Name()))
		if err != nil {
			continue
		}
		for _, v := range versions {
			if !v.IsDir() {
				continue
			}
			p, err := LoadPack(filepath.Join(root, nameDir.Name(), v.Name(), "pack.yaml"))
			if err != nil {
				continue // a broken retained version never blocks the rest
			}
			packs = append(packs, p)
		}
	}
	sort.Slice(packs, func(i, j int) bool {
		if packs[i].Metadata.Name != packs[j].Metadata.Name {
			return packs[i].Metadata.Name < packs[j].Metadata.Name
		}
		return packs[i].Metadata.Version < packs[j].Metadata.Version
	})
	return packs, nil
}

// Resolved is one recipe with its book/pack fields merged (book override >
// later pack > earlier pack) and its classification attached.
type Resolved struct {
	Name   string
	Recipe *Recipe   // effective merged recipe (never nil)
	Source string    // "pack:<recipe>@<version>" or "" for local recipes
	Local  bool      // defined in the book, no pack pointer
	InBook bool      // has an entry in the book (stamped or classified)
	Disabled *Disabled // non-nil when the book disabled it (with evidence)
}

// Library is the loaded knowledge for one context: the book (may be nil) plus
// the packs it references (or all installed packs when no book exists yet).
type Library struct {
	ContextName string
	BookPath    string
	Book        *Book   // nil when no book has been generated yet
	Packs       []*Pack // precedence order, lowest first
	Warnings    []string
}

// LoadLibrary loads the recipe book for a context and the packs it pins.
// Missing pinned pack versions degrade to a warning (the book's referencing
// entries for that pack become unresolvable, not fatal).
func LoadLibrary(configDir, contextName string) (*Library, error) {
	if contextName == "" {
		return nil, fmt.Errorf("no current context — recipes are per-context; use --context or `dtctl ctx use`")
	}
	lib := &Library{ContextName: contextName, BookPath: BookPath(configDir, contextName)}
	book, err := LoadBook(lib.BookPath)
	if err != nil {
		return nil, err
	}
	lib.Book = book

	if book != nil && len(book.Metadata.Packs) > 0 {
		for _, ref := range book.Metadata.Packs {
			p, err := LoadPack(filepath.Join(PackDir(configDir, ref.Name, ref.Version), "pack.yaml"))
			if err != nil {
				lib.Warnings = append(lib.Warnings,
					fmt.Sprintf("pack %s@%s pinned by the recipe book is not installed — its recipes are unavailable", ref.Name, ref.Version))
				continue
			}
			lib.Packs = append(lib.Packs, p)
		}
	} else {
		packs, err := InstalledPacks(configDir)
		if err != nil {
			return nil, err
		}
		lib.Packs = packs
	}
	return lib, nil
}

// ParseSource splits a "pack:<recipe>@<version>" pointer.
func ParseSource(source string) (recipeName, version string, ok bool) {
	rest, found := strings.CutPrefix(source, "pack:")
	if !found {
		return "", "", false
	}
	name, ver, found := strings.Cut(rest, "@")
	if !found || name == "" {
		return "", "", false
	}
	return name, ver, true
}

// packRecipe finds a recipe by name across the packs, highest precedence
// first. Version, when non-empty, must match the pack's version.
func (l *Library) packRecipe(name, version string) (*Recipe, *Pack) {
	for i := len(l.Packs) - 1; i >= 0; i-- {
		p := l.Packs[i]
		if version != "" && p.Metadata.Version != version {
			continue
		}
		if r, ok := p.Recipes[name]; ok {
			return r, p
		}
	}
	if version != "" {
		// Version drift: fall back to any version rather than dangling.
		return l.packRecipe(name, "")
	}
	return nil, nil
}

// mergeRecipe overlays book fields on a pack recipe: non-empty book fields
// win field-wise; stamps/overrides/notes always come from the book.
func mergeRecipe(pack *Recipe, book *Recipe) *Recipe {
	if pack == nil {
		return book
	}
	merged := *pack
	if book == nil {
		return &merged
	}
	if book.Description != "" {
		merged.Description = book.Description
	}
	if book.DQL != "" {
		merged.DQL = book.DQL
	}
	if book.Params != nil {
		if merged.Params == nil {
			merged.Params = book.Params
		} else {
			params := make(map[string]*Param, len(merged.Params)+len(book.Params))
			for k, v := range merged.Params {
				params[k] = v
			}
			for k, v := range book.Params {
				params[k] = v
			}
			merged.Params = params
		}
	}
	if book.Followups != nil {
		merged.Followups = book.Followups
	}
	if book.Segments != nil {
		merged.Segments = book.Segments
	}
	merged.Source = book.Source
	merged.Override = book.Override
	merged.LastRun = book.LastRun
	merged.Note = book.Note
	return &merged
}

// Lookup resolves one recipe by name: book entry (resolving its pack pointer)
// first, then disabled entries, then pack-only recipes not yet in the book.
func (l *Library) Lookup(name string) (*Resolved, error) {
	if l.Book != nil {
		if entry, ok := l.Book.Recipes[name]; ok {
			return l.resolveBookEntry(name, entry, nil), nil
		}
		if d, ok := l.Book.Disabled[name]; ok {
			// A disabled recipe is still resolvable (and executable with a
			// warning): the body comes from the packs.
			if packR, _ := l.packRecipe(name, ""); packR != nil {
				res := l.resolveBookEntry(name, nil, packR)
				res.Disabled = d
				return res, nil
			}
			return nil, fmt.Errorf("recipe %q is disabled (%s) and its pack is not installed", name, d.Reason)
		}
	}
	if packR, pack := l.packRecipe(name, ""); packR != nil {
		merged := mergeRecipe(packR, nil)
		return &Resolved{
			Name:   name,
			Recipe: merged,
			Source: fmt.Sprintf("pack:%s@%s", name, pack.Metadata.Version),
		}, nil
	}
	return nil, fmt.Errorf("recipe %q not found — run `dtctl recipes` for the index", name)
}

func (l *Library) resolveBookEntry(name string, entry *Recipe, packR *Recipe) *Resolved {
	res := &Resolved{Name: name, InBook: entry != nil}
	source := ""
	if entry != nil {
		source = entry.Source
	}
	if packR == nil && source != "" {
		if rName, ver, ok := ParseSource(source); ok {
			packR, _ = l.packRecipe(rName, ver)
		}
	}
	res.Recipe = mergeRecipe(packR, entry)
	res.Source = source
	res.Local = entry != nil && source == ""
	if res.Recipe == nil {
		res.Recipe = &Recipe{}
	}
	return res
}

// All returns the merged recipe namespace, sorted by name: every usable book
// entry, local recipe, and pack recipe not yet classified by the book.
// Disabled recipes are NOT included — list them via the book's Disabled map.
func (l *Library) All() []*Resolved {
	seen := map[string]*Resolved{}
	if l.Book != nil {
		for name, entry := range l.Book.Recipes {
			seen[name] = l.resolveBookEntry(name, entry, nil)
		}
	}
	for _, p := range l.Packs {
		for name := range p.Recipes {
			if _, ok := seen[name]; ok {
				continue
			}
			if l.Book != nil {
				if _, disabled := l.Book.Disabled[name]; disabled {
					continue
				}
			}
			r, _ := l.Lookup(name)
			if r != nil {
				seen[name] = r
			}
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*Resolved, 0, len(names))
	for _, n := range names {
		out = append(out, seen[n])
	}
	return out
}

// Capabilities merges the capability definitions of all packs (later pack
// wins on a name collision).
func (l *Library) Capabilities() map[string]*CapabilityDef {
	defs := map[string]*CapabilityDef{}
	for _, p := range l.Packs {
		for name, def := range p.Capabilities {
			defs[name] = def
		}
	}
	return defs
}
