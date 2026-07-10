package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
	"github.com/dynatrace-oss/dtui/internal/tui/theme"
)

// enrichCap bounds how many rows one batched enrichment query covers.
const enrichCap = 120

// tableView is the generic engine behind every catalog view: a DQL-backed
// table with incremental filtering and the universal drill-down vocabulary.
type tableView struct {
	ds    *dataSource
	spec  *catalog.Spec
	scope catalog.Scope

	all    []map[string]any // as fetched
	rows   []map[string]any // after /-filter and sort
	cursor int
	offset int

	loading bool
	err     error
	seq     int
	dql     string
	elapsed string

	sortCol  int // -1 = fetch order
	sortDesc bool

	filterInput textinput.Model
	filtering   bool // input focused
	filter      string

	searches []string        // server-side | search terms ('/' + enter stacks them)
	facets   []catalog.Facet // server-side attribute filters ('f')

	// Facet manager overlay ('f'): the first stage lists the active filters
	// (edit/remove) above the attribute candidates from the fetched records'
	// keys; the value stage picks from the server's fieldsSummary.
	facetMode    facetStage
	facetInput   textinput.Model
	facetFields  []string             // attribute candidates (manager stage)
	facetField   string               // explored attribute ("" = editing a search term)
	facetOptions []catalog.FacetValue // fieldsSummary suggestions
	facetLoading bool
	facetErr     error
	facetSel     int
	facetEdit    int // index of the search/facet being edited; -1 = adding

	// onData observes fetched records before filtering (the query escape
	// hatch derives result columns from it).
	onData func(records []map[string]any)

	// dynCols is the column set derived from the fetched records when the
	// spec is Dynamic (the record sampler browses arbitrary tables).
	dynCols []catalog.Column

	// previewOn shows the selected row's highlights in a side pane (bottom
	// panel on narrow screens) — the navigator's peek pattern, toggled with
	// tab. Entirely client-side: the row's record is already fetched.
	previewOn bool

	width, height int
}

// previewPaneMinWidth is the narrowest screen that fits a side preview; below
// it the preview renders as a bottom panel instead.
const previewPaneMinWidth = 110

// previewBottomH is the bottom preview panel's line budget on narrow screens.
const previewBottomH = 9

func (v *tableView) previewSide() bool   { return v.previewOn && v.width >= previewPaneMinWidth }
func (v *tableView) previewBottom() bool { return v.previewOn && v.width < previewPaneMinWidth }

// facetStage is the facet picker's overlay state.
type facetStage int

const (
	facetOff facetStage = iota
	facetFieldStage
	facetValueStage
)

// facetEntry is one selectable row of the manager stage: an active search
// term or facet (edit/remove), or an attribute to add a new facet by.
type facetEntry struct {
	kind facetEntryKind
	idx  int    // index into searches/facets (active kinds)
	attr string // attribute name (entryAttr)
}

type facetEntryKind int

const (
	entrySearch facetEntryKind = iota
	entryFacet
	entryAttr
)

// enrichOwner tags enrichment queries so their results are told apart from
// the view's list query (both share the view's seq generation).
type enrichOwner struct{ v *tableView }

// facetOwner tags the facet picker's fieldsSummary query; field pins the
// result to the attribute it was requested for (two in-flight explorations
// must not cross).
type facetOwner struct {
	v     *tableView
	field string
}

func newTableView(ds *dataSource, spec *catalog.Spec, scope catalog.Scope) *tableView {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.CharLimit = 64
	fi := textinput.New()
	fi.Prompt = "⌕ "
	fi.CharLimit = 64
	return &tableView{ds: ds, spec: spec, scope: scope, filterInput: ti, facetInput: fi, sortCol: -1, facetEdit: -1}
}

func (v *tableView) Init() tea.Cmd { return v.Refresh() }

// columns returns the active column set: the scope-derived override (the
// logs pattern drill shows extracted fields), the lens' override when the
// active lens curates its own (db → statement, genai → tokens), the derived
// set for dynamic specs, else the spec's.
func (v *tableView) columns() []catalog.Column {
	if v.spec.ScopeColumns != nil {
		if cols := v.spec.ScopeColumns(v.scope); cols != nil {
			return cols
		}
	}
	if len(v.spec.Lenses) > 0 {
		if cols := v.spec.LensAt(v.scope.Lens).Columns; cols != nil {
			return cols
		}
	}
	if v.spec.Dynamic && v.dynCols != nil {
		return v.dynCols
	}
	return v.spec.Columns
}

// setLens activates a lens by index (wrapping when asked — the brackets
// cycle) and refetches. Sort resets: lenses may carry different columns.
func (v *tableView) setLens(i int, wrap bool) tea.Cmd {
	n := len(v.spec.Lenses)
	if n == 0 {
		return nil
	}
	if wrap {
		i = ((i % n) + n) % n
	}
	if i < 0 || i >= n || i == v.scope.Lens {
		return nil
	}
	v.scope.Lens = i
	v.sortCol = -1
	v.cursor, v.offset = 0, 0
	l := v.spec.Lenses[i]
	return tea.Batch(v.Refresh(), markHistory, status(fmt.Sprintf("lens: %s — %s", l.Name, l.Desc)))
}

// composeDQL renders the list query: the spec's scope query with the server
// searches injected after the source (DQL rejects them later in the
// pipeline) and the facet filters before the sort/limit tail. API-backed
// views without a query render "".
func (v *tableView) composeDQL() string {
	if v.spec.Query == nil {
		return ""
	}
	dql := catalog.InjectSearches(v.spec.Query(v.scope), v.searches)
	var stages []string
	for _, f := range v.facets {
		stages = append(stages, f.Stage())
	}
	return catalog.InjectStages(dql, stages)
}

func (v *tableView) Refresh() tea.Cmd {
	v.seq++
	v.loading = true
	v.err = nil
	v.dql = v.composeDQL()
	if v.spec.API != "" {
		return v.ds.call(v, v.seq, v.spec.API, v.scope, v.dql)
	}
	return v.ds.query(v, v.seq, v.dql)
}

func (v *tableView) SetTimeframe(tf catalog.Timeframe) tea.Cmd {
	v.scope.Timeframe = tf
	return v.Refresh()
}

func (v *tableView) InputActive() bool { return v.filtering || v.facetMode != facetOff }

// Busy reports whether the list query is in flight (animates the spinner).
func (v *tableView) Busy() bool { return v.loading || v.facetLoading }

// RowCount reports the fetched row count for tab badges — valid only once a
// fetch has completed cleanly.
func (v *tableView) RowCount() (int, bool) {
	return len(v.all), v.seq > 0 && !v.loading && v.err == nil
}

func (v *tableView) Crumb() string {
	label := v.spec.Name
	// The default lens is the view's understood state; only deviations show.
	if len(v.spec.Lenses) > 0 && v.scope.Lens > 0 {
		label += "·" + v.spec.LensAt(v.scope.Lens).Name
	}
	if v.scope.Arg != "" {
		label += fmt.Sprintf(" (%s)", strings.ToLower(v.scope.Arg))
	}
	if v.scope.Entity != nil && v.spec.UsesScope() {
		name := v.scope.Entity.Name
		if name == "" {
			name = v.scope.Entity.ID
		}
		label += fmt.Sprintf(" (%s)", name)
	}
	if v.scope.Pattern != "" {
		label += " [pattern]"
	}
	// Server-side narrowing is scope the query kept — the breadcrumb (and the
	// history trail snapshotted from it) must say so.
	var narrow []string
	for _, s := range v.searches {
		narrow = append(narrow, "⌕"+s)
	}
	for _, f := range v.facets {
		narrow = append(narrow, f.Label())
	}
	if len(narrow) > 0 {
		label += " [" + strings.Join(narrow, " ") + "]"
	}
	return label
}

// setFilter pre-fills the incremental filter (command-bar args, home jumps).
func (v *tableView) setFilter(f string) {
	v.filterInput.SetValue(f)
	v.filter = f
	v.applyFilter()
}

func (v *tableView) Echo() string {
	if v.spec.API != "" && v.spec.Echo != nil {
		return v.spec.Echo(v.scope, v.dql)
	}
	if v.dql == "" {
		return ""
	}
	oneline := strings.Join(strings.Fields(strings.ReplaceAll(v.dql, "\n", " ")), " ")
	return fmt.Sprintf("dtctl query '%s'", oneline)
}

// DQL reveals the view's generated query (ctrl+q).
func (v *tableView) DQL() string { return v.dql }

func (v *tableView) Hints() []keyHint {
	switch v.facetMode {
	case facetFieldStage:
		hints := []keyHint{{"type", "find attribute"}, {"↑/↓", "move"}, {"enter", "add / edit"}}
		if len(v.searches)+len(v.facets) > 0 {
			hints = append(hints, keyHint{"ctrl+x", "remove"})
		}
		return append(hints, keyHint{"esc", "close"})
	case facetValueStage:
		if v.facetField == "" {
			return []keyHint{{"type", "edit term"}, {"enter", "apply"}, {"esc", "back"}}
		}
		return []keyHint{{"type", "narrow · * = pattern"}, {"↑/↓", "move"}, {"enter", "apply filter"}, {"esc", "back"}}
	}
	if v.filtering {
		return []keyHint{{"type", "filter rows"}, {"enter", "add server search"}, {"alt+enter", "replace"}, {"esc", "clear"}}
	}
	var hints []keyHint
	if len(v.spec.Lenses) > 0 {
		hints = append(hints, keyHint{"[/]", "lens"})
	}
	hints = append(hints, keyHint{"tab", "preview"})
	switch {
	case v.spec.EnterTarget == "pattern-logs":
		hints = append(hints, keyHint{"enter", "matching logs"})
	case v.spec.EnterTarget == "session-timeline":
		hints = append(hints, keyHint{"enter", "session timeline"})
	case v.spec.EnterTarget == "model-fields":
		if v.spec.LensAt(v.scope.Lens).Name == "models" {
			hints = append(hints, keyHint{"enter", "model fields"})
		} else {
			hints = append(hints, keyHint{"enter", "definition"})
		}
	case v.spec.EnterTarget != "":
		hints = append(hints, keyHint{"enter", v.spec.EnterTarget})
		if v.spec.EnterArg == nil && v.spec.Kind == catalog.KindEntity {
			hints = append(hints, keyHint{"d", "details"})
		}
	case v.spec.Kind == catalog.KindEntity && v.spec.Entity != nil:
		hints = append(hints, keyHint{"enter", "details"}, keyHint{"d", "record"})
	default:
		hints = append(hints, keyHint{"enter", "inspect"})
	}
	// Stable order for the drill keys.
	for _, k := range []string{"l", "s", "m", "p", "v", "u", "e", "a"} {
		if target, ok := v.spec.Drills[k]; ok {
			hints = append(hints, keyHint{k, target})
		}
	}
	hints = append(hints, keyHint{"/", "filter"})
	if v.spec.API == "" {
		hints = append(hints, keyHint{"f", "facets"})
	}
	if len(v.searches) > 0 || len(v.facets) > 0 {
		hints = append(hints, keyHint{"F", "clear facets"})
	}
	hints = append(hints, keyHint{"J/K", "sort"})
	return hints
}

func (v *tableView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bodySizeMsg:
		v.width, v.height = msg.width, msg.height
		return nil

	case dataMsg:
		if eo, ok := msg.owner.(enrichOwner); ok && eo.v == v {
			if msg.seq == v.seq && msg.err == nil {
				v.applyEnrichment(msg.records)
			}
			return nil // enrichment failures degrade to blank cells
		}
		if fo, ok := msg.owner.(facetOwner); ok && fo.v == v {
			// Only the exploration the picker is currently standing on counts;
			// a result for a superseded field or a closed picker is stale.
			if msg.seq != v.seq || v.facetMode != facetValueStage || fo.field != v.facetField {
				return nil
			}
			v.facetLoading = false
			v.facetErr = msg.err
			if msg.err == nil {
				v.facetOptions = catalog.ParseFieldsSummary(msg.records)
			}
			return nil
		}
		if msg.owner != any(v) || msg.seq != v.seq {
			return nil
		}
		v.loading = false
		v.err = msg.err
		if msg.err == nil {
			v.all = msg.records
			v.elapsed = catalog.FormatDuration(msg.elapsed)
			if v.spec.Dynamic {
				// nil on empty keeps the spec's declared fallback columns —
				// deriveColumns would pin a "(no rows)" placeholder.
				v.dynCols = nil
				if len(msg.records) > 0 {
					v.dynCols = deriveColumns(msg.records)
					// Timestamped tables sample newest-first client-side. The
					// server query cannot sort blindly — sorting on a field
					// the table lacks is a hard FIELD_DOES_NOT_EXIST
					// (validated live) — so the fetched page orders here.
					if v.sortCol < 0 {
						for i, c := range v.dynCols {
							if c.Field == "timestamp" {
								v.sortCol, v.sortDesc = i, true
								break
							}
						}
					}
				}
			}
			if v.onData != nil {
				v.onData(msg.records)
			}
			v.applyFilter()
			return v.enrich()
		}
		return nil

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

// enrich issues the batched sparkline query for the fetched page (capped).
func (v *tableView) enrich() tea.Cmd {
	es := v.spec.Enrich
	if es == nil || len(v.all) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var keys []string
	for _, rec := range v.all {
		if len(keys) >= enrichCap {
			break
		}
		if k := es.Key(rec); k != "" && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	return v.ds.query(enrichOwner{v}, v.seq, es.Query(v.scope.Timeframe, keys))
}

// applyEnrichment joins batched timeseries results onto the fetched rows.
func (v *tableView) applyEnrichment(results []map[string]any) {
	es := v.spec.Enrich
	if es == nil {
		return
	}
	byKey := map[string]map[string]any{}
	for _, res := range results {
		if k := catalog.Str(res, es.By); k != "" {
			byKey[k] = res
		}
	}
	for _, rec := range v.all {
		res := byKey[es.Key(rec)]
		if res == nil {
			continue
		}
		for _, alias := range es.Series {
			if series, ok := res[alias]; ok {
				rec[catalog.EnrichKey(alias)] = series
			}
		}
	}
	v.applyFilter() // re-render (and re-sort: sparkline columns sort by last value)
}

func (v *tableView) handleKey(msg tea.KeyMsg) tea.Cmd {
	if v.facetMode != facetOff {
		return v.handleFacetKey(msg)
	}
	if v.filtering {
		switch msg.String() {
		case "enter", "alt+enter":
			v.filtering = false
			v.filterInput.Blur()
			// Enter promotes the typed text to a server-side search over the
			// whole dataset (any field, case-insensitive) — the client filter
			// only ever narrowed the fetched page's visible columns. The
			// client filter clears: server-matched rows may match on fields
			// no column shows, and hiding them again would lie. Terms stack
			// (each is a chained | search stage); alt+enter replaces the
			// active terms with this one instead.
			if term := strings.TrimSpace(v.filter); term != "" {
				// A view without a query has nothing to inject a server
				// search into — the client filter stays as the narrowing.
				if v.spec.Query == nil {
					return status(fmt.Sprintf("client filter %q (no server search on %s)", term, v.spec.Name))
				}
				v.filterInput.SetValue("")
				v.filter = ""
				v.applyFilter()
				return v.addSearch(term, msg.String() == "alt+enter")
			}
		case "esc":
			v.filtering = false
			v.filterInput.Blur()
			v.filterInput.SetValue("")
			v.filter = ""
			v.applyFilter()
		default:
			var cmd tea.Cmd
			v.filterInput, cmd = v.filterInput.Update(msg)
			v.filter = v.filterInput.Value()
			v.applyFilter()
			return cmd
		}
		return nil
	}

	switch msg.String() {
	case "up", "k":
		v.move(-1)
	case "down", "j":
		v.move(1)
	case "pgup", "ctrl+b":
		v.move(-v.pageSize())
	case "pgdown", "ctrl+f", " ":
		v.move(v.pageSize())
	case "home", "g":
		v.cursor = 0
		v.offset = 0
	case "end", "G":
		v.move(len(v.rows))
	case "J": // next sort column (wraps through "no sort")
		v.sortCol++
		if v.sortCol >= len(v.columns()) {
			v.sortCol = -1
		}
		v.sortDesc = v.defaultDesc()
		v.applyFilter()
		if v.sortCol < 0 {
			return status("sort: fetch order")
		}
		return status(fmt.Sprintf("sort: %s %s", v.columns()[v.sortCol].Title, sortArrow(v.sortDesc)))
	case "K": // toggle direction of the current sort column
		if v.sortCol < 0 {
			return status("no sort column (J selects one)")
		}
		v.sortDesc = !v.sortDesc
		v.applyFilter()
		return status(fmt.Sprintf("sort: %s %s", v.columns()[v.sortCol].Title, sortArrow(v.sortDesc)))
	case "]":
		if len(v.spec.Lenses) > 0 {
			return v.setLens(v.scope.Lens+1, true)
		}
	case "[":
		if len(v.spec.Lenses) > 0 {
			return v.setLens(v.scope.Lens-1, true)
		}
	case "tab":
		// Peek without committing: tab toggles the preview pane (the same
		// key the navigator uses), rendering the selected row's highlights
		// from data already fetched — no extra query.
		v.previewOn = !v.previewOn
		if v.previewOn {
			return status("preview on — enter opens the full record")
		}
		return status("preview off")
	case "/":
		v.filtering = true
		v.filterInput.Focus()
		return textinput.Blink
	case "f":
		return v.openFacets()
	case "F":
		return v.clearFacets()
	case "esc":
		if v.filter != "" {
			v.filterInput.SetValue("")
			v.filter = ""
			v.applyFilter()
			return claimKey
		}
		if len(v.searches) > 0 || len(v.facets) > 0 {
			return v.clearFacets()
		}
		return nil // app pops the stack
	case "enter":
		rec := v.selected()
		if rec == nil {
			return nil
		}
		// Containment navigation (k9s-style) when the spec declares it:
		// workload → its pods, census row → typed list, trace → waterfall,
		// metric-explorer row → chart (carrying the explorer's own scope).
		if v.spec.EnterTarget == "waterfall" {
			return v.openTrace(rec)
		}
		if v.spec.EnterTarget == "chart" {
			key := catalog.Str(rec, "metric.key")
			if key == "" {
				return statusErr("row carries no metric key")
			}
			var entity *catalog.Entity
			if v.scope.Entity != nil {
				e := *v.scope.Entity
				entity = &e
			}
			return func() tea.Msg { return metricChartMsg{key: key, entity: entity} }
		}
		if v.spec.EnterTarget == "pattern-logs" {
			// Logs matching the selected pattern, keeping the patterns view's
			// own scope (entity + timeframe) so the drill stays honest.
			pattern := catalog.PatternOf(rec)
			if pattern == "" {
				return statusErr("row carries no pattern")
			}
			spec := catalog.Lookup("logs")
			scope := catalog.Scope{Timeframe: v.scope.Timeframe, Entity: v.scope.Entity, Pattern: pattern}
			return func() tea.Msg { return pushViewMsg{spec: spec, scope: scope} }
		}
		if v.spec.EnterTarget == "session-timeline" {
			return v.openSession(rec)
		}
		if v.spec.EnterTarget == "model-fields" {
			// The dictionary's models lens drills into the model's fields —
			// the same view, fields lens, scoped by Arg; on the field lenses
			// enter opens the full definition (examples, enums).
			if v.spec.LensAt(v.scope.Lens).Name == "models" {
				name := catalog.Str(rec, "name")
				if name == "" {
					return statusErr("row carries no model name")
				}
				spec, scope := v.spec, v.scope
				scope.Arg = name
				scope.Lens = catalog.DictFieldsLens
				return func() tea.Msg { return pushViewMsg{spec: spec, scope: scope} }
			}
			return v.inspect(rec)
		}
		if v.spec.EnterTarget != "" {
			if target := catalog.Lookup(v.spec.EnterTarget); target != nil {
				scope := catalog.Scope{Timeframe: v.scope.Timeframe}
				if v.spec.EnterArg != nil {
					if scope.Arg = v.spec.EnterArg(rec); scope.Arg == "" {
						return statusErr("row is not enterable — this data object cannot be fetched")
					}
				} else if e := v.entityOf(rec); e != nil {
					scope.Entity = e
				}
				return func() tea.Msg { return pushViewMsg{spec: target, scope: scope} }
			}
		}
		// Entity rows open the tabbed detail page; signal rows (logs,
		// events) open the record inspector — except Davis problems, which
		// get the bespoke problem page ('d' keeps the raw record).
		if v.spec.Kind == catalog.KindEntity {
			if e := v.entityOf(rec); e != nil {
				entity := *e
				return func() tea.Msg { return detailMsg{entity: entity, rec: rec} }
			}
		}
		if catalog.IsProblem(rec) {
			return func() tea.Msg { return problemMsg{rec: rec} }
		}
		return v.inspect(rec)
	case "d":
		rec := v.selected()
		if rec == nil {
			return nil
		}
		// Views whose enter follows containment keep the detail page on d.
		if v.spec.EnterTarget != "" && v.spec.EnterArg == nil && v.spec.Kind == catalog.KindEntity {
			if e := v.entityOf(rec); e != nil {
				entity := *e
				return func() tea.Msg { return detailMsg{entity: entity, rec: rec} }
			}
		}
		return v.inspect(rec)
	default:
		if target, ok := v.spec.Drills[msg.String()]; ok {
			return v.drill(target)
		}
	}
	return nil
}

// --- facets ---------------------------------------------------------------

// openFacets opens the manager: active filters (edit/remove) above the
// attribute candidates from the fetched records' keys.
func (v *tableView) openFacets() tea.Cmd {
	if v.spec.API != "" {
		// Facet exploration pairs the fetched records' attributes with
		// fieldsSummary over the view's query — on an API view the records
		// are NOT the query's rows (patterns are analyzer output over a
		// logs pipeline), so interactive faceting would filter on fields
		// the pipeline never carries. Facets inherited from the source
		// list (the 'a' drill) still apply; '/' filters client-side.
		return statusErr("facets need a DQL-backed view — / filters " + v.spec.Name + " client-side")
	}
	v.facetFields = v.facetCandidates()
	if len(v.facetFields) == 0 && len(v.searches)+len(v.facets) == 0 {
		return statusErr("no attributes to facet on (no rows fetched)")
	}
	v.facetMode = facetFieldStage
	// Adding is the common case: start on the first attribute; the active
	// filters sit above, one ↑ away.
	v.facetSel = len(v.searches) + len(v.facets)
	v.facetEdit = -1
	v.facetErr = nil
	v.facetInput.SetValue("")
	v.facetInput.Focus()
	return textinput.Blink
}

// clearFacets drops every server search and facet, refetching unnarrowed.
func (v *tableView) clearFacets() tea.Cmd {
	if len(v.searches) == 0 && len(v.facets) == 0 {
		return status("no active facets")
	}
	v.searches = nil
	v.facets = nil
	return tea.Batch(v.Refresh(), markHistory, status("facets cleared"))
}

// addSearch stacks (or, replacing, swaps in) a server-side search term.
func (v *tableView) addSearch(term string, replace bool) tea.Cmd {
	if replace {
		v.searches = []string{term}
		return tea.Batch(v.Refresh(), markHistory, status(fmt.Sprintf("server search %q — f manages · F clears", term)))
	}
	for _, s := range v.searches {
		if s == term {
			return status(fmt.Sprintf("search %q already active", term))
		}
	}
	v.searches = append(v.searches, term)
	return tea.Batch(v.Refresh(), markHistory, status(fmt.Sprintf("server search %q added — f manages · F clears", term)))
}

// addFacet appends a facet (deduplicated) and refetches — shared by the
// picker's value stage and the inspector's cross-view 'f'.
func (v *tableView) addFacet(f catalog.Facet) tea.Cmd {
	for _, existing := range v.facets {
		if existing == f {
			return status("facet " + f.Label() + " already active")
		}
	}
	v.facets = append(v.facets, f)
	return tea.Batch(v.Refresh(), markHistory, status("facet "+f.Label()+" — f manages · F clears"))
}

// hasField reports whether any fetched record carries the key — the guard
// that keeps a cross-view facet from silently emptying the list.
func (v *tableView) hasField(field string) bool {
	for _, rec := range v.all {
		if _, ok := rec[field]; ok {
			return true
		}
	}
	return false
}

// facetCandidates returns the scalar attribute keys present in the fetched
// records, sorted — maps and arrays can't anchor a value filter, and
// enrichment keys are synthetic.
func (v *tableView) facetCandidates() []string {
	seen := map[string]bool{}
	var fields []string
	for _, rec := range v.all {
		for k, val := range rec {
			if seen[k] || strings.HasPrefix(k, "__enrich.") {
				continue
			}
			switch val.(type) {
			case map[string]any, []any:
				continue
			}
			seen[k] = true
			fields = append(fields, k)
		}
	}
	sort.Strings(fields)
	return fields
}

func (v *tableView) handleFacetKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		if v.facetMode == facetValueStage {
			// Back to the manager, not out of the picker.
			v.facetMode = facetFieldStage
			v.facetSel = len(v.searches) + len(v.facets)
			v.facetEdit = -1
			v.facetErr = nil
			v.facetInput.SetValue("")
			return claimKey
		}
		v.closeFacets()
		return claimKey
	case "up", "ctrl+k":
		v.facetSel = clampSel(v.facetSel-1, v.facetListLen())
	case "down", "ctrl+j", "tab":
		v.facetSel = clampSel(v.facetSel+1, v.facetListLen())
	case "enter":
		if v.facetMode == facetFieldStage {
			entries := v.facetEntries()
			if len(entries) == 0 {
				return nil
			}
			return v.enterFacetEntry(entries[clampSel(v.facetSel, len(entries))])
		}
		return v.applyFacet()
	case "ctrl+x", "delete":
		if v.facetMode == facetFieldStage {
			return v.removeFacetEntry()
		}
		fallthrough
	default:
		var cmd tea.Cmd
		v.facetInput, cmd = v.facetInput.Update(msg)
		if v.facetMode == facetFieldStage {
			// Typing hunts for an attribute — jump the selection past the
			// active filters so enter adds rather than edits.
			v.facetSel = len(v.searches) + len(v.facets)
		} else {
			v.facetSel = 0
		}
		return cmd
	}
	return nil
}

// enterFacetEntry acts on a manager row: attributes explore their values,
// active filters open prefilled for editing.
func (v *tableView) enterFacetEntry(e facetEntry) tea.Cmd {
	switch e.kind {
	case entryAttr:
		v.facetEdit = -1
		return v.openFacetValues(e.attr)
	case entryFacet:
		v.facetEdit = e.idx
		cmd := v.openFacetValues(v.facets[e.idx].Field)
		v.facetInput.SetValue(v.facets[e.idx].Value)
		return cmd
	default: // entrySearch — edit the term in place, no suggestions to fetch
		v.facetMode = facetValueStage
		v.facetField = ""
		v.facetEdit = e.idx
		v.facetOptions = nil
		v.facetErr = nil
		v.facetLoading = false
		v.facetSel = 0
		v.facetInput.SetValue(v.searches[e.idx])
		return nil
	}
}

// removeFacetEntry drops the selected active filter (manager stage).
func (v *tableView) removeFacetEntry() tea.Cmd {
	entries := v.facetEntries()
	if len(entries) == 0 {
		return nil
	}
	e := entries[clampSel(v.facetSel, len(entries))]
	var label string
	switch e.kind {
	case entrySearch:
		label = "⌕" + v.searches[e.idx]
		v.searches = append(v.searches[:e.idx], v.searches[e.idx+1:]...)
	case entryFacet:
		label = v.facets[e.idx].Label()
		v.facets = append(v.facets[:e.idx], v.facets[e.idx+1:]...)
	default:
		return status("nothing to remove — select an active filter above")
	}
	v.facetSel = clampSel(v.facetSel, len(v.facetEntries()))
	return tea.Batch(v.Refresh(), markHistory, status("removed "+label))
}

// facetListLen is the length of whichever list the picker currently shows.
func (v *tableView) facetListLen() int {
	if v.facetMode == facetFieldStage {
		return len(v.facetEntries())
	}
	return len(v.facetValueMatches())
}

// openFacetValues advances to the value stage: the server's top values for
// field (v.facetEdit >= 0 means the chosen value replaces that facet).
func (v *tableView) openFacetValues(field string) tea.Cmd {
	v.facetMode = facetValueStage
	v.facetField = field
	v.facetOptions = nil
	v.facetErr = nil
	v.facetLoading = true
	v.facetSel = 0
	v.facetInput.SetValue("")
	dql := catalog.FieldsSummaryQuery(v.spec.Query(v.scope), field, v.searches, v.facets)
	return v.ds.query(facetOwner{v: v, field: field}, v.seq, dql)
}

// applyFacet commits the value-stage choice: input containing '*' applies as
// a wildcard pattern, a highlighted suggestion applies exactly, and
// free-typed text without suggestions applies as an exact value. In edit
// mode the result replaces the entry it was opened from.
func (v *tableView) applyFacet() tea.Cmd {
	input := strings.TrimSpace(v.facetInput.Value())
	if v.facetField == "" { // editing a search term
		if input == "" {
			return statusErr("empty term — esc goes back, ctrl+x in the manager removes")
		}
		v.searches[v.facetEdit] = input
		v.closeFacets()
		return tea.Batch(v.Refresh(), markHistory, status(fmt.Sprintf("search %q updated", input)))
	}
	matches := v.facetValueMatches()
	var value string
	switch {
	case strings.Contains(input, "*"):
		value = input
	case len(matches) > 0:
		value = matches[clampSel(v.facetSel, len(matches))].Value
	case input != "":
		value = input
	default:
		return nil
	}
	f := catalog.Facet{Field: v.facetField, Value: value}
	if v.facetEdit >= 0 {
		v.facets[v.facetEdit] = f
		v.closeFacets()
		return tea.Batch(v.Refresh(), markHistory, status("facet "+f.Label()+" updated"))
	}
	v.closeFacets()
	return v.addFacet(f)
}

func (v *tableView) closeFacets() {
	v.facetMode = facetOff
	v.facetLoading = false
	v.facetEdit = -1
	v.facetInput.Blur()
	v.facetInput.SetValue("")
}

// facetEntries lists the manager rows: active searches, active facets, then
// the attribute candidates narrowed by the picker input (active filters stay
// pinned — they are few, and removal must not require clearing the input).
func (v *tableView) facetEntries() []facetEntry {
	entries := make([]facetEntry, 0, len(v.searches)+len(v.facets)+len(v.facetFields))
	for i := range v.searches {
		entries = append(entries, facetEntry{kind: entrySearch, idx: i})
	}
	for i := range v.facets {
		entries = append(entries, facetEntry{kind: entryFacet, idx: i})
	}
	for _, f := range v.facetFieldMatches() {
		entries = append(entries, facetEntry{kind: entryAttr, attr: f})
	}
	return entries
}

// facetFieldMatches filters attribute candidates by the picker input.
func (v *tableView) facetFieldMatches() []string {
	needle := strings.ToLower(strings.TrimSpace(v.facetInput.Value()))
	if needle == "" {
		return v.facetFields
	}
	var out []string
	for _, f := range v.facetFields {
		if strings.Contains(strings.ToLower(f), needle) {
			out = append(out, f)
		}
	}
	return out
}

// facetValueMatches filters suggestions by the picker input. Input holding a
// '*' is pattern syntax headed for applyFacet, not a suggestion filter.
func (v *tableView) facetValueMatches() []catalog.FacetValue {
	needle := strings.ToLower(strings.TrimSpace(v.facetInput.Value()))
	if needle == "" || strings.Contains(needle, "*") {
		return v.facetOptions
	}
	var out []catalog.FacetValue
	for _, fv := range v.facetOptions {
		if strings.Contains(strings.ToLower(fv.Value), needle) {
			out = append(out, fv)
		}
	}
	return out
}

// clampSel bounds a selection index against a (possibly shrunken) list.
func clampSel(sel, n int) int {
	if sel >= n {
		sel = n - 1
	}
	if sel < 0 {
		return 0
	}
	return sel
}

// renderFacetPicker renders the overlay: the manager (active filters +
// attribute list), or the value stage for the explored attribute.
func (v *tableView) renderFacetPicker() string {
	const valueW, countW = 44, 10
	var b strings.Builder
	if v.facetMode == facetFieldStage {
		b.WriteString(theme.OverlayTitle.Render("facets — "+v.spec.Name) + "\n\n")
		b.WriteString(" " + v.facetInput.View() + "\n\n")
		entries := v.facetEntries()
		sel := clampSel(v.facetSel, len(entries))
		active := len(v.searches) + len(v.facets)
		if active > 0 {
			b.WriteString(theme.Section("active — enter edits · ctrl+x removes") + "\n")
		}
		v.renderFacetList(&b, len(entries), sel, func(i int) string {
			e := entries[i]
			switch e.kind {
			case entrySearch:
				return pad("⌕ "+v.searches[e.idx], valueW) + " " + cell("search", countW, true)
			case entryFacet:
				return pad(v.facets[e.idx].Label(), valueW) + " " + cell("facet", countW, true)
			default:
				return pad(e.attr, valueW+countW+1)
			}
		}, func(i int) string {
			// The attribute block gets its own header once the actives end.
			if i == active && active > 0 {
				return theme.Section("add by attribute")
			}
			return ""
		})
		b.WriteString("\n" + theme.Dim.Render("type to narrow · enter add/edit · esc close"))
		return b.String()
	}

	title := "facet — " + v.facetField
	if v.facetField == "" {
		title = "search — full text (any field)"
	}
	b.WriteString(theme.OverlayTitle.Render(title) + "\n\n")
	b.WriteString(" " + v.facetInput.View() + "\n\n")
	switch {
	case v.facetField == "":
		b.WriteString(theme.Dim.Render(" matches records containing the term in any field") + "\n")
	case v.facetLoading:
		b.WriteString(" " + theme.Spinner.Render(theme.Spin()) + theme.Dim.Render(" exploring top values…") + "\n")
	case v.facetErr != nil:
		b.WriteString(" " + theme.Error.Render("✗ "+wrap(v.facetErr.Error(), valueW+countW)) + "\n")
	default:
		matches := v.facetValueMatches()
		sel := clampSel(v.facetSel, len(matches))
		if len(matches) == 0 {
			b.WriteString(theme.Dim.Render(" no values (type one — enter applies it)") + "\n")
		}
		v.renderFacetList(&b, len(matches), sel, func(i int) string {
			return pad(matches[i].Value, valueW) + " " + cell(matches[i].Count, countW, true)
		}, nil)
	}
	b.WriteString("\n" + theme.Dim.Render("enter apply · payment* / *ayment* patterns · esc back"))
	return b.String()
}

// renderFacetList writes a windowed, selection-highlighted list of n rows;
// header (optional) injects a section line before row i.
func (v *tableView) renderFacetList(b *strings.Builder, n, sel int, row func(i int) string, header func(i int) string) {
	limit := max(v.height-12, 4)
	offset := 0
	if sel >= limit {
		offset = sel - limit + 1
	}
	end := offset + limit
	if end > n {
		end = n
	}
	for i := offset; i < end; i++ {
		if header != nil {
			if h := header(i); h != "" {
				b.WriteString(h + "\n")
			}
		}
		if i == sel {
			b.WriteString(theme.Selected.Render(" " + row(i) + " "))
		} else {
			b.WriteString(" " + row(i) + " ")
		}
		b.WriteString("\n")
	}
	if rest := n - end; rest > 0 {
		b.WriteString(theme.Dim.Render(fmt.Sprintf(" … %d more", rest)) + "\n")
	}
}

// inspect opens the raw record inspector for a row, titled by the most
// specific identity the record offers (a "detectors › detectors" crumb says
// nothing).
func (v *tableView) inspect(rec map[string]any) tea.Cmd {
	title := v.spec.Name
	if e := v.entityOf(rec); e != nil && e.Name != "" {
		title = e.Name
	} else {
		for _, key := range []string{"title", "name", "display_id"} {
			if t := catalog.Str(rec, key); t != "" {
				title = t
				break
			}
		}
	}
	return func() tea.Msg { return inspectMsg{title: title, rec: rec} }
}

// drill opens the target view scoped to the selected row's entity. The
// special targets: "metrics" (canned charts), "trace" (waterfall jump via
// the record's trace id), "patterns" (analyze the whole current list).
func (v *tableView) drill(target string) tea.Cmd {
	if target == "patterns" {
		// Pattern extraction describes the list being looked at, not one
		// row: it inherits the view's FULL scope (entity, trace, pattern)
		// and its server-side narrowing, and needs no selection.
		spec := catalog.Lookup("patterns")
		scope := catalog.Scope{Entity: v.scope.Entity, Timeframe: v.scope.Timeframe,
			TraceID: v.scope.TraceID, Pattern: v.scope.Pattern}
		return func() tea.Msg {
			return pushViewMsg{spec: spec, scope: scope, searches: v.searches, facets: v.facets}
		}
	}
	rec := v.selected()
	if rec == nil {
		return nil
	}
	if target == "trace" {
		return v.openTrace(rec)
	}
	if target == "session" {
		return v.openSession(rec)
	}
	if target == "trace-logs" {
		if v.spec.Trace == nil {
			return nil
		}
		id := v.spec.Trace(rec)
		if id == "" {
			return statusErr("record carries no trace id")
		}
		spec := catalog.Lookup("logs")
		scope := catalog.Scope{Timeframe: v.scope.Timeframe, TraceID: id}
		return func() tea.Msg { return pushViewMsg{spec: spec, scope: scope} }
	}
	entity := v.entityOf(rec)
	if entity == nil {
		return statusErr("selection carries no entity to scope by")
	}
	if target == "metrics" {
		// Types without canned charts get the metric explorer scoped to the
		// entity — every type has discoverable metrics, curated or not.
		if catalog.MetricsFor(entity.Type) == nil {
			spec := catalog.Lookup("metrics")
			scope := catalog.Scope{Entity: entity, Timeframe: v.scope.Timeframe}
			return func() tea.Msg { return pushViewMsg{spec: spec, scope: scope} }
		}
		return func() tea.Msg { return metricsMsg{entity: *entity} }
	}
	if target == "traces" && !catalog.SpanScopable(entity.Type) {
		return statusErr(fmt.Sprintf("spans carry no %s scope field", entity.Type))
	}
	spec := catalog.Lookup(target)
	if spec == nil {
		return statusErr(fmt.Sprintf("unknown view %q", target))
	}
	scope := catalog.Scope{Entity: entity, Timeframe: v.scope.Timeframe}
	if target == "traces" {
		// GenAI entities land on the genai lens — their spans rarely
		// include roots, and prompts/tool calls are what the drill is for.
		scope.Lens = catalog.DefaultSpanLens(entity.Type)
	}
	return func() tea.Msg { return pushViewMsg{spec: spec, scope: scope} }
}

// openTrace jumps to the waterfall of the selected row's trace, anchored on
// the span the jump came from (a 500-span trace must not open at the root
// and leave the user hunting for the row they were standing on).
func (v *tableView) openTrace(rec map[string]any) tea.Cmd {
	if v.spec.Trace == nil {
		return nil
	}
	id := v.spec.Trace(rec)
	if id == "" {
		return statusErr("record carries no trace id")
	}
	span := catalog.Str(rec, "span.id")
	return func() tea.Msg { return waterfallMsg{traceID: id, focusSpanID: span} }
}

// openSession jumps to the session timeline of the selected row (a sessions
// row via enter, or any RUM event row via the 'u' drill).
func (v *tableView) openSession(rec map[string]any) tea.Cmd {
	id := catalog.Str(rec, "dt.rum.session.id")
	if id == "" {
		return statusErr("record carries no session id")
	}
	// Only a sessions-list row IS the session's record; a RUM event row (the
	// 'u' drill — it carries a classifier) merely names the session, and the
	// timeline fetches the record itself.
	session := rec
	if catalog.Str(rec, "characteristics.classifier") != "" {
		session = nil
	}
	return func() tea.Msg { return timelineMsg{sessionID: id, rec: session} }
}

func (v *tableView) entityOf(rec map[string]any) *catalog.Entity {
	if v.spec.Entity == nil {
		return nil
	}
	return v.spec.Entity(rec)
}

// Selection exposes the highlighted row and its entity for app-level actions
// (pin, yank, open in browser, relations).
func (v *tableView) Selection() (map[string]any, *catalog.Entity) {
	rec := v.selected()
	if rec == nil {
		return nil, nil
	}
	return rec, v.entityOf(rec)
}

func (v *tableView) selected() map[string]any {
	if v.cursor >= 0 && v.cursor < len(v.rows) {
		return v.rows[v.cursor]
	}
	return nil
}

func (v *tableView) move(delta int) {
	v.cursor += delta
	if v.cursor < 0 {
		v.cursor = 0
	}
	if v.cursor >= len(v.rows) {
		v.cursor = len(v.rows) - 1
	}
	if v.cursor < 0 {
		v.cursor = 0
	}
	v.clampOffset()
}

func (v *tableView) pageSize() int {
	chrome := 3
	if len(v.spec.Lenses) > 0 {
		chrome++ // the lens strip line
	}
	if v.previewBottom() {
		chrome += previewBottomH + 1 // panel plus its rule
	}
	if v.height > chrome {
		return v.height - chrome
	}
	return 10
}

func (v *tableView) clampOffset() {
	visible := v.pageSize()
	if v.cursor < v.offset {
		v.offset = v.cursor
	}
	if v.cursor >= v.offset+visible {
		v.offset = v.cursor - visible + 1
	}
	if v.offset < 0 {
		v.offset = 0
	}
}

// applyFilter recomputes rows from the fetched page (client-side, consistent
// with the design: server-side narrowing is what scope is for).
func (v *tableView) applyFilter() {
	if v.filter == "" {
		v.rows = v.all
	} else {
		needle := strings.ToLower(v.filter)
		v.rows = nil
		for _, rec := range v.all {
			var cells []string
			for _, c := range v.columns() {
				cells = append(cells, c.Text(rec))
			}
			if strings.Contains(strings.ToLower(strings.Join(cells, " ")), needle) {
				v.rows = append(v.rows, rec)
			}
		}
	}
	v.sortRows()
	if v.cursor >= len(v.rows) {
		v.cursor = len(v.rows) - 1
	}
	if v.cursor < 0 {
		v.cursor = 0
	}
	v.clampOffset()
}

// sortKey extracts a column's sort key for a record.
func (v *tableView) sortKey(col catalog.Column, rec map[string]any) any {
	switch {
	case col.Sort != nil:
		return col.Sort(rec)
	case col.Field != "":
		return rec[col.Field]
	default:
		return col.Text(rec)
	}
}

// sortRows orders rows by the active sort column (stable; empties last).
func (v *tableView) sortRows() {
	if v.sortCol < 0 || v.sortCol >= len(v.columns()) {
		return
	}
	col := v.columns()[v.sortCol]
	sort.SliceStable(v.rows, func(i, j int) bool {
		a, aEmpty := sortable(v.sortKey(col, v.rows[i]))
		b, bEmpty := sortable(v.sortKey(col, v.rows[j]))
		if aEmpty != bEmpty {
			return bEmpty // empties sink regardless of direction
		}
		if aEmpty {
			return false
		}
		less := cmpSortable(a, b) < 0
		if v.sortDesc {
			less = cmpSortable(a, b) > 0
		}
		return less
	})
}

// defaultDesc picks the natural direction for a freshly selected sort column:
// numbers biggest-first (CPU, restarts), text A-to-Z.
func (v *tableView) defaultDesc() bool {
	if v.sortCol < 0 || v.sortCol >= len(v.columns()) {
		return false
	}
	col := v.columns()[v.sortCol]
	for _, rec := range v.rows {
		key, empty := sortable(v.sortKey(col, rec))
		if empty {
			continue
		}
		_, numeric := key.(float64)
		return numeric
	}
	return false
}

// sortable normalizes a sort key: numbers (including Grail's stringified
// longs and durations) become float64, everything else lowercase text.
func sortable(key any) (norm any, empty bool) {
	switch val := key.(type) {
	case nil:
		return nil, true
	case float64:
		return val, false
	case int:
		return float64(val), false
	case int64:
		return float64(val), false
	case bool:
		if val {
			return 1.0, false
		}
		return 0.0, false
	case string:
		if val == "" {
			return nil, true
		}
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f, false
		}
		return strings.ToLower(val), false
	default:
		s := catalog.FormatValue(val)
		if s == "" {
			return nil, true
		}
		return strings.ToLower(s), false
	}
}

// cmpSortable compares two normalized sort keys; numbers order before text.
func cmpSortable(a, b any) int {
	af, aNum := a.(float64)
	bf, bNum := b.(float64)
	switch {
	case aNum && bNum:
		switch {
		case af < bf:
			return -1
		case af > bf:
			return 1
		}
		return 0
	case aNum:
		return -1
	case bNum:
		return 1
	}
	return strings.Compare(a.(string), b.(string))
}

func sortArrow(desc bool) string {
	if desc {
		return "↓"
	}
	return "↑"
}

func (v *tableView) View(width, height int) string {
	v.width, v.height = width, height

	if v.facetMode != facetOff {
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Position(0.2),
			theme.OverlayBox.Render(v.renderFacetPicker()))
	}

	if v.previewSide() {
		paneW := width * 2 / 5
		if paneW > 48 {
			paneW = 48
		}
		leftW := width - paneW - 3
		left := strings.Split(v.renderTable(leftW, height), "\n")
		right := v.previewLines(paneW)
		if len(right) > height {
			right = append(right[:height-1], theme.Dim.Render(fmt.Sprintf("… +%d more (enter opens)", len(right)-height+1)))
		}
		sep := theme.Rule.Render("│")
		var b strings.Builder
		rows := max(len(left), len(right))
		if rows > height {
			rows = height
		}
		for i := 0; i < rows; i++ {
			l, r := "", ""
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				r = right[i]
			}
			b.WriteString(pad(l, leftW) + " " + sep + " " + ansi.Truncate(r, paneW, "…"))
			if i < rows-1 {
				b.WriteString("\n")
			}
		}
		return b.String()
	}
	if v.previewBottom() {
		tableH := max(height-previewBottomH-1, 1)
		out := v.renderTable(width, tableH)
		if gap := tableH - lipgloss.Height(out); gap > 0 {
			out += strings.Repeat("\n", gap)
		}
		lines := v.previewLines(width - 2)
		if len(lines) > previewBottomH {
			lines = append(lines[:previewBottomH-1], theme.Dim.Render("… (enter opens the full record)"))
		}
		out += "\n" + theme.Rule.Render(strings.Repeat("─", max(width, 0)))
		for _, l := range lines {
			out += "\n " + ansi.Truncate(l, width-2, "…")
		}
		return out
	}
	return v.renderTable(width, height)
}

// previewLines renders the selected row's peek pane: identity, then the
// curated key facts for entity rows or the priority-field highlights for
// signal records — all from the record already in hand, zero queries.
func (v *tableView) previewLines(w int) []string {
	rec := v.selected()
	if rec == nil {
		return []string{theme.Dim.Render("no selection")}
	}
	entity := v.entityOf(rec)
	title := v.spec.Name
	if entity != nil && entity.Name != "" {
		title = entity.Name
	} else {
		for _, key := range []string{"title", "name", "event.name", "span.name", "endpoint.name", "display_id", "content"} {
			if t := catalog.Str(rec, key); t != "" {
				title = t
				break
			}
		}
	}
	lines := []string{theme.OverlayTitle.Render(ansi.Truncate(flatten(title), w, "…"))}
	if entity != nil {
		id := ansi.Truncate(" "+entity.ID, max(w-lipgloss.Width(entity.Type), 8), "…")
		lines = append(lines, theme.Badge.Render(entity.Type)+theme.Dim.Render(id), "")
		shown := 0
		for _, fact := range catalog.KeyFacts(entity.Type) {
			val := fact.Value(rec)
			if val == "" {
				continue
			}
			lines = append(lines, " "+theme.FactLabel.Render(fact.Label+":")+" "+
				ansi.Truncate(flatten(val), max(w-len(fact.Label)-4, 8), "…"))
			shown++
		}
		if shown <= 1 {
			lines = append(lines, theme.Dim.Render(" (sparse list row — enter opens details)"))
		}
		return lines
	}
	lines = append(lines, "")
	for _, key := range catalog.PriorityFields(rec) {
		val, ok := rec[key]
		if !ok {
			continue
		}
		text := flatten(catalog.FormatValue(val))
		if text == "" {
			continue
		}
		if key == "event.severity" {
			text = catalog.SeverityBadge(catalog.Str(rec, key))
		}
		// Long prose fields (log content, event descriptions) wrap over a few
		// lines; everything else stays a one-line fact.
		if key == "content" || key == "event.description" {
			lines = append(lines, " "+theme.FactLabel.Render(key))
			wrapped := wrapLines(text, max(w-2, 8))
			if len(wrapped) > 4 {
				wrapped = append(wrapped[:4], theme.Dim.Render("…"))
			}
			for _, l := range wrapped {
				lines = append(lines, "  "+l)
			}
			continue
		}
		if class := catalog.SeverityFieldClass(key, text); class != "" {
			text = theme.Class(class, text)
		}
		lines = append(lines, " "+theme.FactLabel.Render(key+":")+" "+
			ansi.Truncate(text, max(w-len(key)-4, 8), "…"))
	}
	if v.spec.Trace != nil {
		if id := v.spec.Trace(rec); id != "" {
			lines = append(lines, "", " "+theme.FactLabel.Render("trace:")+" "+theme.UID.Render(shortID(id))+theme.Dim.Render(" (s opens)"))
		}
	}
	return lines
}

func (v *tableView) renderTable(width, height int) string {
	var b strings.Builder

	// Status/filter line.
	head := " " + theme.Count.Render(fmt.Sprintf("%d", len(v.rows)))
	if len(v.rows) != len(v.all) {
		head += theme.Dim.Render(fmt.Sprintf("/%d", len(v.all)))
	}
	head += theme.Dim.Render(" rows")
	if v.elapsed != "" {
		head += theme.Dim.Render(" · " + v.elapsed)
	}
	if v.sortCol >= 0 && v.sortCol < len(v.columns()) {
		head += theme.Dim.Render(" · ") +
			theme.SortMark.Render(sortArrow(v.sortDesc)+" "+v.columns()[v.sortCol].Title)
	}
	for _, s := range v.searches {
		head += "  " + theme.Badge.Render("⌕ "+s)
	}
	for _, f := range v.facets {
		head += " " + theme.Badge.Render(f.Label())
	}
	if v.filtering || v.filter != "" {
		head += "  " + v.filterInput.View()
	}
	if v.loading {
		head = " " + theme.Spinner.Render(theme.Spin()) + head
	}
	b.WriteString(ansi.Truncate(head, width, "…"))
	b.WriteString("\n")

	// Lens strip: the view's quick subsets, detail-tab style ([ and ] cycle;
	// no digit labels — digits are global hotkeys everywhere).
	chrome := 2
	if n := len(v.spec.Lenses); n > 0 {
		chrome = 3
		labels := make([]string, n)
		for i, l := range v.spec.Lenses {
			if i == v.scope.Lens {
				labels[i] = theme.TabActive.Render(l.Name)
			} else {
				labels[i] = theme.TabInactive.Render(l.Name)
			}
		}
		b.WriteString(ansi.Truncate(" "+strings.Join(labels, "  "), width, "…"))
		b.WriteString("\n")
	}

	if v.err != nil {
		b.WriteString("\n" + theme.Error.Render("✗ "+wrap(v.err.Error(), width-2)))
		return b.String()
	}

	// One gutter cell marks the cursor row; columns share the rest.
	widths := v.columnWidths(width - 1)

	// Header row.
	var hdr []string
	for i, c := range v.columns() {
		title := c.Title
		if i == v.sortCol {
			title += sortArrow(v.sortDesc)
		}
		hdr = append(hdr, cell(title, widths[i], c.Right))
	}
	b.WriteString(" " + theme.TableHeader.Render(ansi.Truncate(strings.Join(hdr, "  "), width-1, "")))
	b.WriteString("\n")

	visible := height - chrome
	if visible < 1 {
		visible = 1
	}
	end := v.offset + visible
	if end > len(v.rows) {
		end = len(v.rows)
	}
	for i := v.offset; i < end; i++ {
		b.WriteString(v.renderRow(i, widths, width))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	if len(v.rows) == 0 && !v.loading {
		if v.filter != "" && len(v.all) > 0 {
			b.WriteString(theme.Dim.Render(fmt.Sprintf("  no rows match %q (%d fetched — esc clears the filter)", v.filter, len(v.all))))
		} else {
			empty := "∅ no data in timeframe (last " + v.scope.Timeframe.Label + ")"
			if len(v.spec.Lenses) > 0 {
				what := v.spec.LensAt(v.scope.Lens).Name
				if what == "all" {
					what = "data" // "no all in timeframe" reads broken
				}
				empty = fmt.Sprintf("∅ no %s in timeframe (last %s) — [/] switches lens",
					what, v.scope.Timeframe.Label)
			}
			b.WriteString("\n" + lipgloss.PlaceHorizontal(width, lipgloss.Center, theme.Dim.Render(empty)))
		}
	}
	return b.String()
}

func (v *tableView) renderRow(i int, widths []int, width int) string {
	rec := v.rows[i]
	selected := i == v.cursor

	var cells []string
	for ci, c := range v.columns() {
		text := cell(c.Text(rec), widths[ci], c.Right)
		if !selected && c.Class != nil {
			if class := c.Class(strings.TrimSpace(text)); class != "" {
				text = theme.Class(class, text)
			}
		}
		cells = append(cells, text)
	}
	row := ansi.Truncate(strings.Join(cells, "  "), width-1, "…")
	if selected {
		// Selected rows drop per-cell colors so the highlight reads as one
		// bar: an accent gutter mark plus a background wash.
		return theme.Gutter.Render("▌") + theme.Selected.Render(pad(row, width-1))
	}
	return " " + row
}

// columnWidths assigns fixed widths and shares the remainder among flex columns.
func (v *tableView) columnWidths(total int) []int {
	cols := v.columns()
	widths := make([]int, len(cols))
	fixed, flexCount := 0, 0
	for i, c := range cols {
		if c.Width > 0 {
			widths[i] = c.Width
			fixed += c.Width
		} else {
			flexCount++
		}
	}
	gaps := 2 * (len(cols) - 1)
	remaining := total - fixed - gaps
	if flexCount > 0 {
		per := remaining / flexCount
		if per < 8 {
			per = 8
		}
		for i, c := range cols {
			if c.Width == 0 {
				widths[i] = per
			}
		}
	}
	return widths
}

// flatten collapses line breaks and tabs to single spaces — a multi-line
// value (SQL statements, log content) must not shear a table row apart.
func flatten(s string) string {
	if !strings.ContainsAny(s, "\n\r\t") {
		return s
	}
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\t' || r == ' '
	}), " ")
}

// pad truncates or pads s to exactly w display cells (ANSI-aware).
func pad(s string, w int) string {
	s = ansi.Truncate(flatten(s), w, "…")
	if gap := w - lipgloss.Width(s); gap > 0 {
		s += strings.Repeat(" ", gap)
	}
	return s
}

// cell renders a table cell at exactly w cells, right-aligning when asked.
func cell(s string, w int, right bool) string {
	if !right {
		return pad(s, w)
	}
	s = ansi.Truncate(flatten(s), w, "…")
	if gap := w - lipgloss.Width(s); gap > 0 {
		s = strings.Repeat(" ", gap) + s
	}
	return s
}

// wrap soft-wraps text to a width using lipgloss.
func wrap(s string, w int) string {
	if w <= 0 {
		return s
	}
	return lipgloss.NewStyle().Width(w).Render(s)
}
