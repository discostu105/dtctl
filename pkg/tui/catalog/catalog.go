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

// Timeframe is the global query window applied to every view. From/To pin
// the window absolutely (a problem's lifespan); zero From means the relative
// Label window.
type Timeframe struct {
	Label string // DQL relative duration, e.g. "2h"
	Dur   time.Duration
	// From/To bound an absolute window. To may be zero (open-ended: an
	// ACTIVE problem's window runs to now). Dur should be set to To-From so
	// interval heuristics keep working.
	From, To time.Time
}

// Absolute reports whether the window is pinned to fixed instants.
func (t Timeframe) Absolute() bool { return !t.From.IsZero() }

// DQL returns the timeframe as a DQL from: value. An absolute window renders
// BOTH bounds — `toTimestamp("…"), to:toTimestamp("…")` — through the same
// `from:%s` slot every query template uses; from:/to: accept toTimestamp
// literals in every command position the catalog composes (validated live).
func (t Timeframe) DQL() string {
	if !t.Absolute() {
		return "now() - " + t.Label
	}
	out := dqlTimestamp(t.From)
	if !t.To.IsZero() {
		out += ", to:" + dqlTimestamp(t.To)
	}
	return out
}

func dqlTimestamp(t time.Time) string {
	return fmt.Sprintf("toTimestamp(%q)", t.UTC().Format("2006-01-02T15:04:05.000Z"))
}

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
	// Entities scopes a signal view to several entities at once (a problem's
	// affected set). When set it wins over Entity; specs compose it via
	// ScopeSignalFilter / ScopeSpanFilter.
	Entities []Entity
	// Arg is a view-specific argument: the node type for the generic entity
	// browser ("AWS_EC2_INSTANCE"), the table/bucket/file for the record
	// sampler, the model for the dictionary fields view, or the monitor id
	// for synthetic executions — set by EnterArg drill-downs.
	Arg string
	// TraceID scopes logs/traces to one distributed trace (log↔trace jumps).
	TraceID string
	// Lens indexes into the spec's Lenses (0 = default). Views without
	// lenses ignore it.
	Lens int
	// Pattern scopes the logs view to records matching a DPL pattern —
	// the log-patterns drill (enter on a pattern row) fills it.
	Pattern string
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
	Right bool   // right-align (numeric columns)
	// Value computes the cell text from the whole record (optional).
	Value func(rec map[string]any) string
	// Class returns a semantic style class ("error", "warn", "ok", "dim", "")
	// for the formatted cell value (optional). The view layer maps classes to
	// concrete styles.
	Class func(val string) string
	// Sort extracts the sort key for a record (optional). Defaults to the raw
	// field value (Field set) or the rendered cell text. Numeric strings —
	// Grail serializes longs and durations as strings — compare numerically.
	Sort func(rec map[string]any) any
}

// Text returns the formatted cell value for a record.
func (c Column) Text(rec map[string]any) string {
	if c.Value != nil {
		return c.Value(rec)
	}
	return FormatValue(rec[c.Field])
}

// Lens is one quick server-side slice of a view's dataset — the spans view
// offers roots/errors/server/…. Lenses render as a tab strip above the table
// (digits and tab switch), and the active lens' filter is part of the spec's
// Query output, so facet exploration and the DQL echo see it too.
type Lens struct {
	Name   string // strip label ("roots")
	Desc   string // one-liner shown in the status bar on switch
	Filter string // DQL condition the query composes ("" = unfiltered)
	// Columns overrides the spec's columns while this lens is active
	// (db → statement columns, genai → token columns). nil = spec columns.
	Columns []Column
}

// Spec is a declarative view definition.
type Spec struct {
	Name    string
	Aliases []string
	Kind    ViewKind
	Desc    string
	// Query renders the list query for a scope. Signal views compose
	// s.Entity into a filter; entity views honor it when EntityScoped.
	// nil for API-backed views without an underlying query (see API).
	Query   func(s Scope) string
	Columns []Column
	// API names a non-DQL data source (wired in tui.Options.Sources) that
	// fetches this view's records: REST-backed lists (SLOs, anomaly
	// detectors) and analyzer executions (log patterns). When Query is also
	// set, its output is handed to the source as input — the log-patterns
	// source analyzes the composed logs query. Server facets need
	// fieldsSummary and stay disabled for API views; server search applies
	// only when Query is set.
	API string
	// Echo renders the CLI equivalent for API-backed views (dql is the
	// composed Query output, "" when Query is nil). DQL views echo
	// `dtctl query` automatically and leave this nil.
	Echo func(s Scope, dql string) string
	// Dynamic derives the column set from the fetched records (the record
	// sampler browses arbitrary tables); Columns is the empty-result
	// fallback.
	Dynamic bool
	// ScopeColumns derives a scope-specific column set (the logs pattern
	// drill shows the pattern's extracted fields); nil result falls back to
	// the lens/static columns.
	ScopeColumns func(s Scope) []Column
	// Lenses are the view's quick subset selections (nil = none). The
	// spec's Query is responsible for composing the scoped lens' Filter.
	Lenses []Lens
	// Entity extracts the Smartscape entity a selected row stands for
	// (nil when the row carries none). It supplies the scope for drills.
	Entity func(rec map[string]any) *Entity
	// Drills maps a key press to a target view name. The special target
	// "metrics" opens the canned metrics charts for the selected entity.
	Drills map[string]string
	// EntityScoped marks entity views whose Query composes s.Entity (e.g.
	// pods of a namespace). Signal views are always entity-scoped. The pin
	// (⌖) is only handed to views that can use it.
	EntityScoped bool
	// EnterTarget makes enter follow containment k9s-style (workload → its
	// pods) instead of opening the detail page; the detail page moves to
	// 'd'. The target view is scoped to the row's entity — unless EnterArg
	// is set, which scopes it by Arg instead (census row → typed list). The
	// sentinel "waterfall" opens the trace waterfall for Trace(rec).
	EnterTarget string
	EnterArg    func(rec map[string]any) string
	// Trace extracts a row's trace id ("" = none): the 's' jump on log
	// records and enter on trace rows.
	Trace func(rec map[string]any) string
	// Enrich adds async per-row metric columns (sparklines) fetched in one
	// batched timeseries query after the list loads (nil = none).
	Enrich *EnrichSpec
	// Scopable refines CanScope for views whose query composes a filter that
	// is present but useless for some entity types — traces filter by a
	// dt.smartscape.* field spans only carry for some types. nil = rely on
	// the query-diff check alone.
	Scopable func(e Entity) bool
}

// UsesScope reports whether a view's query can compose an entity scope.
func (s *Spec) UsesScope() bool { return s.Kind == KindSignal || s.EntityScoped }

// LensAt returns the lens at index i, falling back to the default (first)
// lens when i is out of range — stale history entries must survive catalog
// reorderings. Zero value for views without lenses.
func (s *Spec) LensAt(i int) Lens {
	return lensAt(s.Lenses, i)
}

// lensAt is the clamp behind LensAt, callable from Query closures — a spec's
// Query cannot reference its own Spec var (initialization cycle), but it can
// reference the lens slice.
func lensAt(lenses []Lens, i int) Lens {
	if len(lenses) == 0 {
		return Lens{}
	}
	if i < 0 || i >= len(lenses) {
		i = 0
	}
	return lenses[i]
}

// floorTimeframe widens a query window to at least min — sparse tables
// (sessions, bizevents, vulnerability state reports) look deceptively empty
// over the default 2h. Absolute windows are deliberate and never widened.
func floorTimeframe(tf Timeframe, min time.Duration, label string) string {
	if !tf.Absolute() && tf.Dur < min {
		return "now() - " + label
	}
	return tf.DQL()
}

// CanScope reports whether pinning entity e meaningfully narrows this view.
// It is honest by construction: if composing the entity does not change the
// generated query (an incompatible type — a SERVICE pin on :pods, or any pin
// on a view whose query ignores scope), the pin must be neither applied nor
// claimed in the breadcrumb. Scopable catches the subtler case where the
// filter differs but would match nothing (traces on a non-span-scopable
// type).
func (s *Spec) CanScope(tf Timeframe, e Entity) bool {
	if s.Query == nil {
		return false
	}
	if s.Query(Scope{Timeframe: tf, Entity: &e}) == s.Query(Scope{Timeframe: tf}) {
		return false
	}
	if s.Scopable != nil {
		return s.Scopable(e)
	}
	return true
}

// EnrichSpec describes the batched metric enrichment of an entity table.
// One query per refresh fetches a small timeseries per visible row; results
// land in the records under Into keys and render as sparkline columns.
type EnrichSpec struct {
	// Key returns the record's join value (e.g. its Smartscape id or pod
	// name); rows with "" are skipped.
	Key func(rec map[string]any) string
	// By is the result field carrying the join value in the batched query.
	By string
	// Series lists the timeseries aliases to copy into the record, stored
	// under "__enrich." + alias.
	Series []string
	// Query renders the batched timeseries for a set of join values.
	Query func(tf Timeframe, keys []string) string
}

// EnrichKey is the record key enrichment series are stored under.
func EnrichKey(alias string) string { return "__enrich." + alias }

// specs is the ordered registry; order drives command-bar suggestions.
var specs = []*Spec{
	problemsSpec, servicesSpec, hostsSpec, logsSpec, tracesSpec, eventsSpec,
	podsSpec, workloadsSpec, namespacesSpec, nodesSpec, clustersSpec,
	awsSpec, entitiesSpec, resourcesSpec,
	frontendsSpec, databasesSpec, genaiSpec, vulnsSpec, metricsSpec,
	sessionsSpec, userEventsSpec, bizeventsSpec,
	patternsSpec, slosSpec, detectorsSpec,
	syntheticSpec, executionsSpec,
	dictionarySpec,
	tablesSpec, bucketsSpec, filesSpec, recordsSpec,
}

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
func Match(input string) []*Spec { return MatchSpecs(specs, input) }

// MatchSpecs is Match over an explicit candidate list — the command bar
// matches its bespoke screens (home, query, nav) together with the registry.
func MatchSpecs(candidates []*Spec, input string) []*Spec {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return candidates
	}
	var prefix, sub []*Spec
	for _, s := range candidates {
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
// an entity type. Validated live for HOST/SERVICE/PROCESS/K8S_*/CONTAINER/
// FRONTEND/DB_*_POSTGRES/AWS_*: the field name is always the lowercased type.
// GenAI is the exception: GENAI_MODEL nodes stamp spans as
// dt.smartscape.gen_ai.model (dot namespace, not genai_model — validated
// live on chat/invoke_agent spans). A nonexistent field in an or-chain
// compares as null (false) — harmless.
func smartscapeField(entityType string) string {
	if entityType == "" {
		return ""
	}
	if kind, ok := strings.CutPrefix(entityType, "GENAI_"); ok {
		return "dt.smartscape.gen_ai." + strings.ToLower(kind)
	}
	return "dt.smartscape." + strings.ToLower(entityType)
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
// an entity, matching both ID eras plus the record's source entity. For K8s
// entities it also matches the plain k8s.* name attributes — log records on
// real tenants carry those but no dt.smartscape.k8s_* fields (validated
// live), while Davis events carry both.
func SignalFilter(e Entity) string {
	var parts []string
	if f := smartscapeField(e.Type); f != "" {
		parts = append(parts, fmt.Sprintf("%s == toSmartscapeId(%q)", f, e.ID))
	}
	if f := legacyField(e.Type); f != "" {
		parts = append(parts, fmt.Sprintf("%s == %q", f, e.ID))
	}
	if f := k8sNameFilter(e); f != "" {
		parts = append(parts, f)
	}
	parts = append(parts, fmt.Sprintf("dt.smartscape_source.id == toSmartscapeId(%q)", e.ID))
	return strings.Join(parts, " or ")
}

// entityList returns the scope's entity set: Entities when set, else the
// singleton Entity, else none.
func (s Scope) entityList() []Entity {
	if len(s.Entities) > 0 {
		return s.Entities
	}
	if s.Entity != nil {
		return []Entity{*s.Entity}
	}
	return nil
}

// ScopeSignalFilter renders the logs/events filter for the scope's entity or
// entity set ("" when unscoped). A single entity composes exactly like
// SignalFilter; several or-join, each parenthesized.
func ScopeSignalFilter(s Scope) string {
	ents := s.entityList()
	switch len(ents) {
	case 0:
		return ""
	case 1:
		return SignalFilter(ents[0])
	}
	parts := make([]string, len(ents))
	for i, e := range ents {
		parts[i] = "(" + SignalFilter(e) + ")"
	}
	return strings.Join(parts, " or ")
}

// ScopeSpanFilter renders the spans filter for the scope's entity set,
// keeping only span-scopable types — a filter on a type spans never carry
// would silently match nothing. "" when unscoped or nothing is scopable.
func ScopeSpanFilter(s Scope) string {
	var parts []string
	for _, e := range s.entityList() {
		if SpanScopable(e.Type) {
			parts = append(parts, SpanFilter(e))
		}
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	for i, p := range parts {
		parts[i] = "(" + p + ")"
	}
	return strings.Join(parts, " or ")
}

// k8sNameFilter matches a K8s entity by its k8s.* name attributes ("" for
// non-K8s types). Signal records carry these as plain strings.
func k8sNameFilter(e Entity) string {
	if e.Name == "" {
		return ""
	}
	switch e.Type {
	case "K8S_POD":
		return fmt.Sprintf("k8s.pod.name == %q", e.Name)
	case "K8S_NAMESPACE":
		return fmt.Sprintf("k8s.namespace.name == %q", e.Name)
	case "K8S_NODE":
		return fmt.Sprintf("k8s.node.name == %q", e.Name)
	case "K8S_CLUSTER":
		return fmt.Sprintf("k8s.cluster.name == %q", e.Name)
	case "K8S_DEPLOYMENT", "K8S_STATEFULSET", "K8S_DAEMONSET":
		kind := strings.ToLower(strings.TrimPrefix(e.Type, "K8S_"))
		return fmt.Sprintf("(k8s.workload.kind == %q and k8s.workload.name == %q)", kind, e.Name)
	}
	return ""
}

// ProblemFilter renders the DQL condition that scopes dt.davis.problems to
// problems affecting an entity (either ID era).
func ProblemFilter(e Entity) string {
	return fmt.Sprintf(
		`matchesPhrase(arrayToString(smartscape.affected_entity.ids, delimiter:","), %q) or matchesPhrase(arrayToString(affected_entity_ids, delimiter:","), %q)`,
		e.ID, e.ID)
}

// SpanFilter renders the DQL condition that scopes a spans query to an
// entity. Spans carry dt.smartscape.* fields for services, K8s objects and
// containers (validated live); there is no legacy fallback worth matching.
func SpanFilter(e Entity) string {
	return fmt.Sprintf("%s == toSmartscapeId(%q)", smartscapeField(e.Type), e.ID)
}

// SpanScopable reports whether spans can be filtered to this entity type —
// the 's' drill is only offered where the scope field actually exists on
// span records (validated live; HOST notably carries none). GenAI spans
// carry dt.smartscape.gen_ai.* ids for agent/model/provider/service.
func SpanScopable(entityType string) bool {
	switch entityType {
	case "SERVICE", "CONTAINER":
		return true
	}
	return strings.HasPrefix(entityType, "K8S_") || strings.HasPrefix(entityType, "GENAI_")
}
