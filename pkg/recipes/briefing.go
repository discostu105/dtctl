package recipes

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// IndexEntry is one line of the recipe index: enough to pick a recipe by
// description and price its trust, without loading the full body. A present
// Records implies the recipe is stamped; Stamped is set only for the rare
// stamped-without-record-count case so the common rows stay two fields
// lighter (the index is the bulk of every agent bootstrap call).
type IndexEntry struct {
	Name           string   `json:"name" yaml:"name"`
	Description    string   `json:"description,omitempty" yaml:"description,omitempty"`
	Records        *int     `json:"records,omitempty" yaml:"records,omitempty"`
	At             string   `json:"at,omitempty" yaml:"at,omitempty"`
	Stamped        bool     `json:"stamped,omitempty" yaml:"stamped,omitempty"`
	Empty          string   `json:"empty,omitempty" yaml:"empty,omitempty"`
	RequiredParams []string `json:"requiredParams,omitempty" yaml:"requiredParams,omitempty"`
	Override       string   `json:"override,omitempty" yaml:"override,omitempty"`
	Note           string   `json:"note,omitempty" yaml:"note,omitempty"`
	Local          bool     `json:"local,omitempty" yaml:"local,omitempty"`
}

// DisabledIndexEntry is one line of negative knowledge — often the most
// token-valuable content in the book ("don't try RUM here").
type DisabledIndexEntry struct {
	Name   string `json:"name" yaml:"name"`
	Class  string `json:"class,omitempty" yaml:"class,omitempty"`
	At     string `json:"at,omitempty" yaml:"at,omitempty"`
	Reason string `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// Briefing is the one bootstrap call: environment facts plus the recipe
// index. `dtctl commands` answers "what can I run?"; this answers "what is
// true here?".
type Briefing struct {
	Context     string    `json:"context" yaml:"context"`
	GeneratedAt string    `json:"generatedAt,omitempty" yaml:"generatedAt,omitempty"`
	FactsAge    string    `json:"factsAge,omitempty" yaml:"factsAge,omitempty"`
	Packs       []PackRef `json:"packs,omitempty" yaml:"packs,omitempty"`
	Facts       *Facts    `json:"facts,omitempty" yaml:"facts,omitempty"`
	// Facts above is a curated view (see curatedFacts); these totals say how
	// much the full book on disk holds beyond it.
	EntityTypesTotal int                             `json:"entityTypesTotal,omitempty" yaml:"entityTypesTotal,omitempty"`
	DataObjectsTotal int                             `json:"dataObjectsTotal,omitempty" yaml:"dataObjectsTotal,omitempty"`
	Declared         map[string]string               `json:"declared,omitempty" yaml:"declared,omitempty"`
	Scoping          map[string]map[string]ScopeRule `json:"scoping,omitempty" yaml:"scoping,omitempty"`
	Recipes          []IndexEntry                    `json:"recipes" yaml:"recipes"`
	Disabled         []DisabledIndexEntry            `json:"disabled,omitempty" yaml:"disabled,omitempty"`
	Warnings         []string                        `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}

// BuildBriefing assembles the briefing from a loaded library.
func BuildBriefing(lib *Library, now func() time.Time) *Briefing {
	if now == nil {
		now = time.Now
	}
	b := &Briefing{Context: lib.ContextName, Warnings: lib.Warnings}

	if lib.Book == nil {
		if len(lib.Packs) == 0 {
			b.Warnings = append(b.Warnings,
				"no recipe book and no installed packs for this context — run `dtctl recipes discover --pack <pack.yaml>` to generate one")
		} else {
			b.Warnings = append(b.Warnings,
				"no recipe book for this context — the index below is UNVERIFIED pack content; run `dtctl recipes discover` to probe it against this environment")
		}
	} else {
		b.GeneratedAt = lib.Book.Metadata.GeneratedAt
		b.Packs = lib.Book.Metadata.Packs
		b.Facts = curatedFacts(&lib.Book.Facts)
		b.EntityTypesTotal = len(lib.Book.Facts.EntityTypes)
		b.DataObjectsTotal = len(lib.Book.Facts.DataObjects)
		b.Declared = lib.Book.Declared
		b.Scoping = lib.Book.Scoping
		if t, err := time.Parse(time.RFC3339, lib.Book.Metadata.GeneratedAt); err == nil {
			b.FactsAge = humanAge(now().Sub(t))
		}
		for name, d := range lib.Book.Disabled {
			b.Disabled = append(b.Disabled, DisabledIndexEntry{Name: name, Class: d.Class, At: d.At, Reason: d.Reason})
		}
		sortDisabled(b.Disabled)
	}

	for _, res := range lib.All() {
		entry := IndexEntry{
			Name:        res.Name,
			Description: res.Recipe.Description,
			Local:       res.Local,
			Override:    res.Recipe.Override,
			Note:        res.Recipe.Note,
		}
		entry.RequiredParams = RequiredParams(res.Recipe)
		if lr := res.Recipe.LastRun; lr != nil {
			entry.Records = lr.Records
			entry.At = stampDate(lr.At)
			entry.Stamped = lr.Records == nil
			entry.Empty = lr.Empty
		}
		b.Recipes = append(b.Recipes, entry)
	}
	return b
}

// briefingEntityTypes caps the entity census in the bootstrap call.
const briefingEntityTypes = 12

// curatedFacts trims the raw fact battery to briefing weight: the full book
// stays on disk; the bootstrap call ships what an agent can act on. The
// dt.entity.* catalog dominates DataObjects (hundreds of entries) and is
// derivable from the entity census, so it is dropped here; the census itself
// keeps the dominant types. Eval forensics showed the uncurated facts
// tripling the briefing's context weight.
func curatedFacts(f *Facts) *Facts {
	c := *f
	if len(f.EntityTypes) > briefingEntityTypes {
		c.EntityTypes = topNEntityTypes(f.EntityTypes, briefingEntityTypes)
	}
	if len(f.DataObjects) > 0 {
		objs := make([]string, 0, len(f.DataObjects))
		for _, o := range f.DataObjects {
			if !strings.HasPrefix(o, "dt.entity.") {
				objs = append(objs, o)
			}
		}
		c.DataObjects = objs
	}
	return &c
}

func topNEntityTypes(census map[string]int64, n int) map[string]int64 {
	type kv struct {
		k string
		v int64
	}
	entries := make([]kv, 0, len(census))
	for k, v := range census {
		entries = append(entries, kv{k, v})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].v != entries[j].v {
			return entries[i].v > entries[j].v
		}
		return entries[i].k < entries[j].k
	})
	if n > len(entries) {
		n = len(entries)
	}
	out := make(map[string]int64, n)
	for _, e := range entries[:n] {
		out[e.k] = e.v
	}
	return out
}

func sortDisabled(entries []DisabledIndexEntry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].Name < entries[j-1].Name; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}

// stampDate compresses a stamp timestamp to its date for the index — the
// book's generatedAt/factsAge already carry freshness at full precision.
func stampDate(at string) string {
	if t, err := time.Parse(time.RFC3339, at); err == nil {
		return t.Format("2006-01-02")
	}
	return at
}

// humanAge renders a duration as a compact age ("3h", "2d").
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
