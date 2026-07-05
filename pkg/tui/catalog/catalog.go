// Package catalog defines the declarative view catalog for the dtctl TUI.
//
// Each view is a Spec: a scope-aware DQL query template plus curated columns
// and drill-down targets. Adding a view is configuration, not plumbing (see
// docs/dev/TUI_DESIGN.md, "The ViewSpec catalog"). The package is free of any
// TUI framework dependency so query composition can be tested as plain data.
package catalog

import (
	"fmt"
	"strings"
	"time"
)

// Entity identifies a Smartscape entity used for scoping signal queries.
type Entity struct {
	ID   string // Smartscape ID string, e.g. "HOST-0D8DA6F3E704257C"
	Name string
	Type string // Smartscape node type, e.g. "HOST", "SERVICE", "K8S_POD"
}

// Timeframe is the global query window applied to every view.
type Timeframe struct {
	Label string // DQL relative duration, e.g. "2h"
	Dur   time.Duration
}

// DQL returns the timeframe as a DQL from: expression.
func (t Timeframe) DQL() string { return "now() - " + t.Label }

// Timeframes are the picker choices, ordered short to long.
var Timeframes = []Timeframe{
	{Label: "30m", Dur: 30 * time.Minute},
	{Label: "2h", Dur: 2 * time.Hour},
	{Label: "24h", Dur: 24 * time.Hour},
	{Label: "7d", Dur: 7 * 24 * time.Hour},
}

// DefaultTimeframe is the window used at startup.
var DefaultTimeframe = Timeframes[1]

// Scope carries the context every query composes: the entity the user is
// standing on (nil = unscoped) and the global timeframe. Drill-down
// navigation fills Entity so signal views are never opened unbounded.
type Scope struct {
	Entity    *Entity
	Timeframe Timeframe
}

// ViewKind distinguishes entity views (the map) from signal views (the terrain).
type ViewKind int

const (
	KindEntity ViewKind = iota
	KindSignal
)

// Column describes one curated table column.
type Column struct {
	Title string
	Field string // record key; ignored when Value is set
	Width int    // display width; 0 = flex (shares remaining space)
	// Value computes the cell text from the whole record (optional).
	Value func(rec map[string]any) string
	// Class returns a semantic style class ("error", "warn", "ok", "dim", "")
	// for the formatted cell value (optional). The view layer maps classes to
	// concrete styles.
	Class func(val string) string
}

// Text returns the formatted cell value for a record.
func (c Column) Text(rec map[string]any) string {
	if c.Value != nil {
		return c.Value(rec)
	}
	return FormatValue(rec[c.Field])
}

// Spec is a declarative view definition.
type Spec struct {
	Name    string
	Aliases []string
	Kind    ViewKind
	Desc    string
	// Query renders the list query for a scope. Signal views compose
	// s.Entity into a filter; entity views ignore it (Phase 1).
	Query   func(s Scope) string
	Columns []Column
	// Entity extracts the Smartscape entity a selected row stands for
	// (nil when the row carries none). It supplies the scope for drills.
	Entity func(rec map[string]any) *Entity
	// Drills maps a key press to a target view name. The special target
	// "metrics" opens the canned metrics charts for the selected entity.
	Drills map[string]string
}

// specs is the ordered registry; order drives command-bar suggestions.
var specs = []*Spec{problemsSpec, servicesSpec, hostsSpec, logsSpec, eventsSpec}

// All returns every registered view spec.
func All() []*Spec { return specs }

// Names returns the canonical view names.
func Names() []string {
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.Name
	}
	return names
}

// Lookup resolves a view by exact name or alias.
func Lookup(name string) *Spec {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, s := range specs {
		if s.Name == name {
			return s
		}
		for _, a := range s.Aliases {
			if a == name {
				return s
			}
		}
	}
	return nil
}

// Match returns specs whose name or alias fuzzy-matches the input, for the
// command bar. Prefix matches rank before subsequence matches.
func Match(input string) []*Spec {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return specs
	}
	var prefix, sub []*Spec
	for _, s := range specs {
		names := append([]string{s.Name}, s.Aliases...)
		matched := 0 // 0 = no, 1 = subsequence, 2 = prefix
		for _, n := range names {
			if strings.HasPrefix(n, input) {
				matched = 2
				break
			}
			if isSubsequence(input, n) {
				matched = 1
			}
		}
		switch matched {
		case 2:
			prefix = append(prefix, s)
		case 1:
			sub = append(sub, s)
		}
	}
	return append(prefix, sub...)
}

func isSubsequence(needle, hay string) bool {
	i := 0
	for _, r := range hay {
		if i < len(needle) && rune(needle[i]) == r {
			i++
		}
	}
	return i == len(needle)
}

// --- scope filter fragments -------------------------------------------------

// smartscapeField returns the dt.smartscape.* field signal records carry for
// an entity type, or "" when there is no per-type field.
func smartscapeField(entityType string) string {
	switch entityType {
	case "HOST":
		return "dt.smartscape.host"
	case "SERVICE":
		return "dt.smartscape.service"
	case "PROCESS":
		return "dt.smartscape.process"
	}
	if strings.HasPrefix(entityType, "K8S_") {
		return "dt.smartscape." + strings.ToLower(entityType)
	}
	return ""
}

// legacyField returns the deprecated dt.entity.* field for an entity type.
// Records carry both ID eras during the Smartscape migration, so scope
// filters must match either (TUI_DESIGN.md, "Dual entity-ID eras").
func legacyField(entityType string) string {
	switch entityType {
	case "HOST":
		return "dt.entity.host"
	case "SERVICE":
		return "dt.entity.service"
	case "PROCESS":
		return "dt.entity.process_group_instance"
	}
	return ""
}

// SignalFilter renders the DQL condition that scopes a logs/events query to
// an entity, matching both ID eras plus the record's source entity.
func SignalFilter(e Entity) string {
	var parts []string
	if f := smartscapeField(e.Type); f != "" {
		parts = append(parts, fmt.Sprintf("%s == toSmartscapeId(%q)", f, e.ID))
	}
	if f := legacyField(e.Type); f != "" {
		parts = append(parts, fmt.Sprintf("%s == %q", f, e.ID))
	}
	parts = append(parts, fmt.Sprintf("dt.smartscape_source.id == toSmartscapeId(%q)", e.ID))
	return strings.Join(parts, " or ")
}

// ProblemFilter renders the DQL condition that scopes dt.davis.problems to
// problems affecting an entity (either ID era).
func ProblemFilter(e Entity) string {
	return fmt.Sprintf(
		`matchesPhrase(arrayToString(smartscape.affected_entity.ids, delimiter:","), %q) or matchesPhrase(arrayToString(affected_entity_ids, delimiter:","), %q)`,
		e.ID, e.ID)
}
