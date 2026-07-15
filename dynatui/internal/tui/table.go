package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
	"github.com/dynatrace-oss/dynatui/internal/tui/theme"
)

// enrichCap bounds how many rows one batched enrichment query covers.
const enrichCap = 120

// tableView is the generic engine behind every catalog view: a DQL-backed
// table with incremental filtering and the universal drill-down vocabulary.
type tableView struct {
	ds    *dataSource
	spec  *catalog.Spec
	scope catalog.Scope

	all  []map[string]any // as fetched
	rows []map[string]any // after /-filter and sort
	scroller

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

	// hopDone marks the spec's scope-widening pre-query as resolved (or
	// skipped); refreshes then reuse the widened scope.Entities.
	hopDone bool

	// onData observes fetched records before filtering (the query escape
	// hatch derives result columns from it).
	onData func(records []map[string]any)

	// dynCols is the column set derived from the fetched records when the
	// spec is Dynamic (the record sampler browses arbitrary tables).
	dynCols []catalog.Column

	// lensDigits marks a table nested in an entered page (set by tabSet):
	// its lens strip is then the innermost numbered strip — it renders digit
	// labels and the page routes 1-9 to it. Top-level tables stay
	// un-numbered; their digits are the global bookmarks.
	lensDigits bool

	width, height int
}

// enrichOwner tags enrichment queries so their results are told apart from
// the view's list query (both share the view's seq generation).
type enrichOwner struct{ v *tableView }

// hopOwner tags the scope-widening pre-query (Spec.Hop) that runs before
// the first list fetch.
type hopOwner struct{ v *tableView }

func newTableView(ds *dataSource, spec *catalog.Spec, scope catalog.Scope) *tableView {
	ti := newTextInput()
	ti.Prompt = "/"
	ti.CharLimit = 64
	fi := newTextInput()
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
// searches and bucket facets injected after the source (DQL rejects search
// later in the pipeline; the bucket filter prunes physical reads at the
// source), the record's bucket projected in on bucket-backed views (skipped
// for API views — their query feeds an analyzer, not the table), and the
// remaining facet filters before the sort/limit tail. API-backed views
// without a query render "".
func (v *tableView) composeDQL() string {
	if v.spec.Query == nil {
		return ""
	}
	return catalog.ComposeQuery(v.spec.Query(v.scope), v.searches, v.facets, v.spec.API == "")
}

func (v *tableView) Refresh() tea.Cmd {
	v.seq++
	v.loading = true
	v.err = nil
	if cmd := v.hopCmd(); cmd != nil {
		return cmd
	}
	return v.fetch()
}

// fetch composes and issues the list query for the current scope.
func (v *tableView) fetch() tea.Cmd {
	v.dql = v.composeDQL()
	if v.spec.API != "" {
		return v.ds.call(v, v.seq, v.spec.API, v.scope, v.dql)
	}
	return v.ds.query(v, v.seq, v.dql)
}

// hopCmd issues the spec's scope-widening pre-query once, before the first
// fetch (nil = no hop needed, fetch directly).
func (v *tableView) hopCmd() tea.Cmd {
	if v.spec.Hop == nil || v.hopDone {
		return nil
	}
	q := v.spec.Hop.Query(v.scope)
	if q == "" {
		v.hopDone = true
		return nil
	}
	v.dql = "" // the list query composes once the widened scope is known
	return v.ds.query(hopOwner{v}, v.seq, q)
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
	return v.ds.echoQuery(v.dql)
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
		if v.lensDigits {
			hints = append(hints, keyHint{"1-9", "lens"})
		} else {
			hints = append(hints, keyHint{"tab", "lens"})
		}
	}
	if !v.ds.previewOn() {
		hints = append(hints, keyHint{"P", "preview"})
	}
	if h, sentinel := enterHandlers[v.spec.EnterTarget]; sentinel {
		if label := h.hint(v); label != "" {
			hints = append(hints, keyHint{"enter", label})
		}
	} else {
		switch {
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
		if ho, ok := msg.owner.(hopOwner); ok && ho.v == v {
			if msg.seq != v.seq {
				return nil
			}
			v.hopDone = true
			// A hop failure degrades to the unwidened scope — the entity's
			// own filter arms still match directly-stamped records.
			if msg.err == nil && v.spec.Hop != nil {
				v.scope.Entities = v.spec.Hop.Apply(v.scope, msg.records)
				if n := len(v.scope.Entities) - 1; n > 0 {
					return tea.Batch(v.fetch(),
						status(fmt.Sprintf("scope widened to %d runtime entities — logs rarely carry service IDs", n)))
				}
			}
			return v.fetch()
		}
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

	if v.scroller.handleKey(msg.String(), len(v.rows), v.pageSize()) {
		return nil
	}

	switch msg.String() {
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
		return status("no lens strip on this view")
	case "[":
		if len(v.spec.Lenses) > 0 {
			return v.setLens(v.scope.Lens-1, true)
		}
		return status("no lens strip on this view")
	case "tab":
		// tab cycles the view's primary strip; on a plain table that is the
		// lens strip. (A table nested in a detail tab never sees tab — the
		// page's tab bar claims it first.)
		if len(v.spec.Lenses) > 0 {
			return v.setLens(v.scope.Lens+1, true)
		}
	case "shift+tab":
		if len(v.spec.Lenses) > 0 {
			return v.setLens(v.scope.Lens-1, true)
		}
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
		// bespoke sentinels resolve through the enterHandlers registry
		// (waterfall, chart, pattern-logs, …); anything else is a catalog
		// view name (workload → its pods, census row → typed list).
		if h, ok := enterHandlers[v.spec.EnterTarget]; ok {
			return h.open(v, rec)
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
		if catalog.IsVulnerability(rec) {
			return func() tea.Msg { return vulnMsg{rec: rec} }
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
	// A parked cursor may now point past the narrowed rows.
	v.scroller.move(0, len(v.rows), v.pageSize())
}

func (v *tableView) View(width, height int) string {
	v.width, v.height = width, height

	if v.facetMode != facetOff {
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Position(0.2),
			theme.OverlayBox.Render(v.renderFacetPicker()))
	}
	return previewLayout(v.ds.previewOn(), width, height, v.renderTable, v.previewLines)
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

	// Lens strip: the view's quick subsets (tab and the brackets cycle).
	// Nested in an entered page it is the innermost numbered strip, so it
	// carries digit labels and the digits address it; at the top level it
	// stays un-numbered — digits are the global bookmarks there.
	chrome := 2
	if n := len(v.spec.Lenses); n > 0 {
		chrome = 3
		labels := make([]string, n)
		for i, l := range v.spec.Lenses {
			name := l.Name
			if v.lensDigits {
				name = fmt.Sprintf("%d %s", i+1, name)
			}
			if i == v.scope.Lens {
				labels[i] = theme.TabActive.Render(name)
			} else {
				labels[i] = theme.TabInactive.Render(name)
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
