package recipes

import (
	"fmt"
	"time"
)

// IndexEntry is one line of the recipe index: enough to pick a recipe by
// description and price its trust, without loading the full body.
type IndexEntry struct {
	Name           string   `json:"name" yaml:"name"`
	Description    string   `json:"description,omitempty" yaml:"description,omitempty"`
	Records        *int     `json:"records,omitempty" yaml:"records,omitempty"`
	At             string   `json:"at,omitempty" yaml:"at,omitempty"`
	Stamped        bool     `json:"stamped" yaml:"stamped"`
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
	Context     string                          `json:"context" yaml:"context"`
	GeneratedAt string                          `json:"generatedAt,omitempty" yaml:"generatedAt,omitempty"`
	FactsAge    string                          `json:"factsAge,omitempty" yaml:"factsAge,omitempty"`
	Packs       []PackRef                       `json:"packs,omitempty" yaml:"packs,omitempty"`
	Facts       *Facts                          `json:"facts,omitempty" yaml:"facts,omitempty"`
	Declared    map[string]string               `json:"declared,omitempty" yaml:"declared,omitempty"`
	Scoping     map[string]map[string]ScopeRule `json:"scoping,omitempty" yaml:"scoping,omitempty"`
	Recipes     []IndexEntry                    `json:"recipes" yaml:"recipes"`
	Disabled    []DisabledIndexEntry            `json:"disabled,omitempty" yaml:"disabled,omitempty"`
	Warnings    []string                        `json:"warnings,omitempty" yaml:"warnings,omitempty"`
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
		b.Facts = &lib.Book.Facts
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
			entry.At = lr.At
			entry.Stamped = true
			entry.Empty = lr.Empty
		}
		b.Recipes = append(b.Recipes, entry)
	}
	return b
}

func sortDisabled(entries []DisabledIndexEntry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].Name < entries[j-1].Name; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
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
