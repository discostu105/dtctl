// Package recipes implements the Recipes concept (docs/dev/RECIPES_CONCEPT.md):
// per-environment, verified DQL query knowledge. The persisted artifact is the
// recipe book (kind: RecipeBook), generated per context from installed recipe
// packs (kind: RecipePack) plus environment discovery. Recipes are read-only
// by design — DQL only, no mutation verbs.
package recipes

// API envelope constants.
const (
	APIVersion = "dtctl.dev/v1alpha1"
	KindBook   = "RecipeBook"
	KindPack   = "RecipePack"
)

// PackRef pins a pack by name and version (ordered in a book: lowest
// precedence first).
type PackRef struct {
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version" yaml:"version"`
}

// BookMetadata identifies a recipe book and the packs it was generated from.
type BookMetadata struct {
	Name        string    `yaml:"name,omitempty"`
	Context     string    `yaml:"context,omitempty"`
	GeneratedAt string    `yaml:"generatedAt,omitempty"`
	Generator   string    `yaml:"generator,omitempty"`
	Packs       []PackRef `yaml:"packs,omitempty"`
}

// SegmentFact is a Grail segment discovered on the environment (apply with -S).
type SegmentFact struct {
	UID         string   `json:"uid" yaml:"uid"`
	Name        string   `json:"name,omitempty" yaml:"name,omitempty"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	Variables   []string `json:"variables,omitempty" yaml:"variables,omitempty"`
}

// Facts is everything discovered by probes — uniform provenance. Human/org
// claims live in Book.Declared, never here.
type Facts struct {
	Character     string                        `json:"character,omitempty" yaml:"character,omitempty"`
	Capabilities  []string                      `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
	Absent        []string                      `json:"absent,omitempty" yaml:"absent,omitempty"`
	EntityTypes   map[string]int64              `json:"entityTypes,omitempty" yaml:"entityTypes,omitempty"`
	DataObjects   []string                      `json:"dataObjects,omitempty" yaml:"dataObjects,omitempty"`
	// Unfetchable lists catalog objects that do not support `fetch` (the
	// catalog's usable_with says so): they are queried through other commands
	// (metrics → timeseries, smartscape.* → smartscapeNodes/Edges). Kept apart
	// from DataObjects so the briefing never baits a fetch that cannot work.
	Unfetchable   []string                      `json:"unfetchable,omitempty" yaml:"unfetchable,omitempty"`
	Buckets       []string                      `json:"buckets,omitempty" yaml:"buckets,omitempty"`
	FieldCarriage map[string]map[string]float64 `json:"fieldCarriage,omitempty" yaml:"fieldCarriage,omitempty"`
	Segments      []SegmentFact                 `json:"segments,omitempty" yaml:"segments,omitempty"`
	Notes         []string                      `json:"notes,omitempty" yaml:"notes,omitempty"`
}

// ScopeRule says how to reference an entity of one type in one signal:
// either a filter template (rendered with .id/.name) or a named hop strategy
// resolved in code. Coverage is derived from facts.fieldCarriage.
type ScopeRule struct {
	Filter   string   `json:"filter,omitempty" yaml:"filter,omitempty"`
	Hop      string   `json:"hop,omitempty" yaml:"hop,omitempty"`
	Coverage *float64 `json:"coverage,omitempty" yaml:"coverage,omitempty"`
}

// Param is a typed recipe parameter. Types: string (the default; rendered
// only through dqlString), int, duration, enum, identifier, dql-filter (raw
// by declared contract). A nil Default means the param is required.
type Param struct {
	Type        string  `yaml:"type,omitempty"`
	Default     *string `yaml:"default,omitempty"`
	Description string  `yaml:"description,omitempty"`
	Values      string  `yaml:"values,omitempty"`
}

// MinCarriage is the third guard shape: a minimum field-carriage ratio.
type MinCarriage struct {
	Table string  `yaml:"table"`
	Field string  `yaml:"field"`
	Ratio float64 `yaml:"ratio"`
}

// Verify is the two-field verify spec: a threshold-free probe plus how to
// interpret it (records: >=1 record verifies; values: a single-row aggregate
// verifies iff at least one aggregated value is non-zero). Entity-scoped
// recipes declare Coverage instead.
type Verify struct {
	Probe    string         `yaml:"probe,omitempty"`
	Expect   string         `yaml:"expect,omitempty"`
	Coverage *CoverageProbe `yaml:"coverage,omitempty"`
}

// CoverageProbe declares a structured coverage measurement (a fraction, not a
// boolean): does signal S exist per entity of type T within the window.
type CoverageProbe struct {
	Per    string `yaml:"per"`
	Signal string `yaml:"signal"`
	Window string `yaml:"window,omitempty"`
}

// LastRun is the stamp — a living cache refreshed on every execution, not a
// verification claim. Params records the conditions ("default" or a short
// hash of the rendered non-default param set); drift reasoning uses only
// default-param stamps.
type LastRun struct {
	At            string  `yaml:"at,omitempty"`
	Records       *int    `yaml:"records,omitempty"`
	Seconds       float64 `yaml:"seconds,omitempty"`
	Params        string  `yaml:"params,omitempty"`
	LimitHit      bool    `yaml:"limitHit,omitempty"`
	Empty         string  `yaml:"empty,omitempty"`
	Partial       string  `yaml:"partial,omitempty"`
	Warning       string  `yaml:"warning,omitempty"`
	Coverage      string  `yaml:"coverage,omitempty"`
	ValuesNonZero *bool   `yaml:"valuesNonZero,omitempty"`
}

// Recipe is one named, parameterized DQL query. In a pack it carries guards
// and a verify spec; in a book it either references a pack recipe (Source set,
// body resolved from the pack) or is local (no Source — hand-written, exempt
// from regeneration).
type Recipe struct {
	Description string            `yaml:"description,omitempty"`
	Source      string            `yaml:"source,omitempty"`
	Requires    []string          `yaml:"requires,omitempty"`
	DataObjects []string          `yaml:"dataObjects,omitempty"`
	MinCarriage *MinCarriage      `yaml:"minCarriage,omitempty"`
	Segments    []string          `yaml:"segments,omitempty"`
	Params      map[string]*Param `yaml:"params,omitempty"`
	DQL         string            `yaml:"dql,omitempty"`
	Verify      *Verify           `yaml:"verify,omitempty"`
	Followups   []string          `yaml:"followups,omitempty"`
	Override    string            `yaml:"override,omitempty"`
	LastRun     *LastRun          `yaml:"lastRun,omitempty"`
	Note        string            `yaml:"note,omitempty"`
}

// Disabled is a recipe moved out of service with classified evidence.
// class: guard — structural, durable, re-evaluated by refresh/discover.
// class: probe-empty — absence of events in a window; weak evidence,
// re-probed on every refresh, and `query --recipe` still executes the recipe
// (a non-empty result resurrects it — never a one-way door).
type Disabled struct {
	Class  string `yaml:"class,omitempty"`
	At     string `yaml:"at,omitempty"`
	Reason string `yaml:"reason,omitempty"`
}

// DisabledClassGuard / DisabledClassProbeEmpty are the two evidence classes.
const (
	DisabledClassGuard      = "guard"
	DisabledClassProbeEmpty = "probe-empty"
)

// Book is the per-environment recipe book (~/.config/dtctl/recipes/<context>.yaml).
type Book struct {
	APIVersion string                          `yaml:"apiVersion"`
	Kind       string                          `yaml:"kind"`
	Metadata   BookMetadata                    `yaml:"metadata"`
	Facts      Facts                           `yaml:"facts,omitempty"`
	Declared   map[string]string               `yaml:"declared,omitempty"`
	Scoping    map[string]map[string]ScopeRule `yaml:"scoping,omitempty"`
	Recipes    map[string]*Recipe              `yaml:"recipes,omitempty"`
	Disabled   map[string]*Disabled            `yaml:"disabled,omitempty"`
}

// CapabilityDef defines a capability by how it is discovered — one of four
// fixed shapes. Structural shapes (DataObject/EntityTypes/MetricKey) are
// preferred; Probe (+ mandatory Window) is weak evidence and re-evaluated on
// every refresh.
type CapabilityDef struct {
	DataObject  string   `yaml:"dataObject,omitempty"`
	EntityTypes []string `yaml:"entityTypes,omitempty"`
	MetricKey   string   `yaml:"metricKey,omitempty"`
	Probe       string   `yaml:"probe,omitempty"`
	Window      string   `yaml:"window,omitempty"`
}

// PackMetadata identifies a pack. Retained pack versions are immutable.
type PackMetadata struct {
	Name    string   `yaml:"name"`
	Version string   `yaml:"version"`
	Sources []string `yaml:"sources,omitempty"`
}

// Pack is a curated, versioned, cross-environment recipe pack.
type Pack struct {
	APIVersion   string                    `yaml:"apiVersion"`
	Kind         string                    `yaml:"kind"`
	Metadata     PackMetadata              `yaml:"metadata"`
	Capabilities map[string]*CapabilityDef `yaml:"capabilities,omitempty"`
	Recipes      map[string]*Recipe        `yaml:"recipes,omitempty"`
}
