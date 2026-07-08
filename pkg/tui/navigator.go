package tui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
	"github.com/dynatrace-oss/dtctl/pkg/tui/theme"
)

// navView is the smartscape navigator (:nav) — a dedicated app for exploring
// the topology graph (docs/dev/TUI_SMARTSCAPE_NAVIGATOR.md). Three levels,
// one layout language (list left, preview right):
//
//   - overview: entity-type census plus the type-level relationship schema
//   - browser:  the instances of one type, with health dots
//   - walk:     an ego-centric, re-rootable neighbor tree around one entity
//
// Levels are separate stacked pages (overview → browser → walk), so the
// app-wide esc convention pops levels; hops inside walk mode are TRAIL
// entries (← backtracks), so a 15-hop walk stays one stack entry. It
// deliberately does not draw the graph — grouped lists, a trail, and a
// preview are the terminal-native answer to the hairball.
type navView struct {
	ds   *dataSource
	tf   catalog.Timeframe
	mode navMode

	// overview state.
	census        []map[string]any // census rows: type, count
	schema        []catalog.SchemaEdge
	schemaLoading bool
	schemaErr     error

	// browser state.
	typ       string
	instances []map[string]any

	// walk state.
	root      catalog.Entity
	trail     []catalog.Entity // hops that led here, oldest first
	edges     []catalog.Edge   // current root's edges (sorted by BuildEdges)
	collapsed map[navGroupKey]bool
	expanded  map[navGroupKey]bool // groups past the render cap shown fully
	dirFilter navDir
	structOnly bool // hide the communication mesh (calls, routes_to)

	// session caches, shared across hops within this navigator instance.
	names     map[string]string           // id → display name
	nodeCache map[string][]catalog.Edge   // id → its edges (0-query backtracks)
	detail    map[string]map[string]any   // id → full node record (preview)

	// health overlay: one tenant-wide problem query per refresh, intersected
	// client-side — never a query per node.
	probSeq    int
	probLoaded bool
	probByID   map[string][]map[string]any // entity id (both eras) → problems
	probByType map[string]int              // node type → #problems touching it

	// preview debounce: cursor movement bumps the generation; the fetch fires
	// only when the tick comes back with the current one.
	previewOn  bool
	previewGen int

	rows []navRow // flattened cursor rows for the current mode

	filterActive bool
	filter       string
	filterInput  textinput.Model

	cursor, offset int
	loading        bool
	err            error
	seq            int
	dql            string
	width, height  int
}

type navMode int

const (
	navOverview navMode = iota
	navBrowser
	navWalk
)

// navModeName is the mode's history-file identity (pageRef.View).
func navModeName(m navMode) string {
	switch m {
	case navBrowser:
		return "types"
	case navWalk:
		return "walk"
	}
	return "overview"
}

type navDir int

const (
	navDirBoth navDir = iota
	navDirOut
	navDirIn
)

// navGroupKey identifies one walk-mode neighbor group: direction + verb.
type navGroupKey struct {
	out  bool
	verb string
}

type navRowKind int

const (
	navRowRoot navRowKind = iota
	navRowGroup
	navRowNeighbor
	navRowMore
	navRowType     // overview census line
	navRowInstance // browser line
)

// navRow is one cursor line. count carries the group's member total on group
// rows and the hidden remainder on "+N more" rows.
type navRow struct {
	kind  navRowKind
	key   navGroupKey
	edge  catalog.Edge
	rec   map[string]any
	count int
}

// navGroupCap bounds neighbors rendered per group before "+N more" — a busy
// host's hundred-row call mesh must not bury the structure (never silent:
// the remainder is always shown as a count).
const navGroupCap = 12

// navTypeRe matches a Smartscape type name for the :nav argument.
var navTypeRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// Owners tagging the navigator's secondary queries.
type navNameOwner struct{ v *navView }
type navSchemaOwner struct{ v *navView }
type navProblemsOwner struct{ v *navView }
type navPreviewOwner struct {
	v  *navView
	id string
}

// navPreviewTickMsg is the preview debounce timer.
type navPreviewTickMsg struct{ gen int }

func newNavView(ds *dataSource, tf catalog.Timeframe) *navView {
	fi := textinput.New()
	fi.Prompt = "/"
	fi.CharLimit = 64
	return &navView{
		ds: ds, tf: tf, mode: navOverview,
		names:     map[string]string{},
		nodeCache: map[string][]catalog.Edge{},
		detail:    map[string]map[string]any{},
		collapsed: map[navGroupKey]bool{},
		expanded:  map[navGroupKey]bool{},
		previewOn: true,
		filterInput: fi,
	}
}

func newNavBrowserView(ds *dataSource, typ string, tf catalog.Timeframe) *navView {
	v := newNavView(ds, tf)
	v.mode = navBrowser
	v.typ = typ
	return v
}

func newNavWalkView(ds *dataSource, root catalog.Entity, tf catalog.Timeframe) *navView {
	v := newNavView(ds, tf)
	v.mode = navWalk
	v.root = root
	if root.Name != "" {
		v.names[root.ID] = root.Name
	}
	return v
}

func (v *navView) Init() tea.Cmd {
	cmds := []tea.Cmd{v.fetchMain(), v.problemsCmd()}
	if v.mode == navOverview {
		cmds = append(cmds, v.schemaCmd())
	}
	return tea.Batch(cmds...)
}

// Refresh refetches everything; the walk caches clear so a stale topology
// doesn't survive an explicit refresh.
func (v *navView) Refresh() tea.Cmd {
	v.nodeCache = map[string][]catalog.Edge{}
	v.detail = map[string]map[string]any{}
	cmds := []tea.Cmd{v.fetchMain(), v.problemsCmd()}
	if v.mode == navOverview {
		cmds = append(cmds, v.schemaCmd())
	}
	return tea.Batch(cmds...)
}

// SetTimeframe is a deliberate no-op: smartscape nodes and edges are not
// windowed, and the health overlay keeps its fixed 24h floor (same rationale
// as the detail-page pulse).
func (v *navView) SetTimeframe(tf catalog.Timeframe) tea.Cmd {
	v.tf = tf
	return nil
}

func (v *navView) InputActive() bool { return v.filterActive }

// Busy animates the spinner while the main list (or the overview's schema
// aggregation) is in flight.
func (v *navView) Busy() bool { return v.loading || v.schemaLoading }

func (v *navView) Crumb() string {
	switch v.mode {
	case navBrowser:
		return v.typ
	case navWalk:
		return "walk (" + entityName(v.root) + ")"
	}
	return "smartscape"
}

func (v *navView) DQL() string { return v.dql }

func (v *navView) Echo() string {
	if v.dql == "" {
		return ""
	}
	return fmt.Sprintf("dtctl query '%s'", strings.Join(strings.Fields(strings.ReplaceAll(v.dql, "\n", " ")), " "))
}

func (v *navView) Hints() []keyHint {
	if v.filterActive {
		return []keyHint{{"enter", "apply"}, {"esc", "clear"}}
	}
	switch v.mode {
	case navBrowser:
		return []keyHint{{"enter", "walk"}, {"d", "details"}, {"/", "filter"}, {".", "pin"}, {"o", "open"}}
	case navWalk:
		return []keyHint{{"enter", "walk to"}, {"←", "back"}, {"d", "details"}, {"space", "fold"},
			{"i", "direction"}, {"M", "mesh"}, {"/", "filter"}}
	}
	return []keyHint{{"enter", "browse type"}, {"/", "filter"}, {"tab", "pane"}}
}

// Selection exposes the highlighted node for app-level actions (pin, x,
// yank, open, drills). Type and group rows carry no entity.
func (v *navView) Selection() (map[string]any, *catalog.Entity) {
	row := v.selectedRow()
	if row == nil {
		return nil, nil
	}
	switch row.kind {
	case navRowRoot:
		e := v.root
		return nil, &e
	case navRowNeighbor:
		return nil, &catalog.Entity{ID: row.edge.OtherID, Name: v.names[row.edge.OtherID], Type: row.edge.OtherType}
	case navRowInstance:
		return row.rec, navInstanceEntity(row.rec)
	}
	return nil, nil
}

func navInstanceEntity(rec map[string]any) *catalog.Entity {
	id := catalog.Str(rec, "id")
	if id == "" {
		return nil
	}
	name := catalog.Str(rec, "display")
	if name == "" {
		name = catalog.Str(rec, "name")
	}
	return &catalog.Entity{ID: id, Name: name, Type: catalog.Str(rec, "type")}
}

func (v *navView) selectedRow() *navRow {
	if v.cursor < 0 || v.cursor >= len(v.rows) {
		return nil
	}
	return &v.rows[v.cursor]
}

// --- data flow ------------------------------------------------------------------

// fetchMain fires the mode's list query. A cached walk root skips the fetch —
// backtracking and re-visits are zero-query.
func (v *navView) fetchMain() tea.Cmd {
	v.seq++
	v.err = nil
	switch v.mode {
	case navOverview:
		v.dql = catalog.CensusQuery()
	case navBrowser:
		v.dql = catalog.TypeInstancesQuery(v.typ)
	case navWalk:
		v.dql = catalog.EdgesQuery(v.root.ID)
		if edges, ok := v.nodeCache[v.root.ID]; ok {
			v.loading = false
			v.edges = edges
			v.rebuildRows()
			return v.resolveNames()
		}
	}
	v.loading = true
	return v.ds.query(v, v.seq, v.dql)
}

func (v *navView) schemaCmd() tea.Cmd {
	v.schemaLoading = true
	v.schemaErr = nil
	return v.ds.queryCapped(navSchemaOwner{v}, v.seq, catalog.SchemaQuery(), 2000)
}

func (v *navView) problemsCmd() tea.Cmd {
	v.probSeq++
	v.probLoaded = false
	return v.ds.query(navProblemsOwner{v}, v.probSeq, catalog.ProblemOverlayQuery())
}

// resolveNames issues the batched name lookup for neighbors not yet known.
func (v *navView) resolveNames() tea.Cmd {
	seen := map[string]bool{}
	var ids []string
	for _, e := range v.edges {
		if _, known := v.names[e.OtherID]; known || seen[e.OtherID] {
			continue
		}
		seen[e.OtherID] = true
		ids = append(ids, e.OtherID)
	}
	if len(ids) == 0 {
		return nil
	}
	return v.ds.query(navNameOwner{v}, v.seq, catalog.NamesQuery(ids))
}

func (v *navView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bodySizeMsg:
		v.width, v.height = msg.width, msg.height
		return nil

	case navPreviewTickMsg:
		if msg.gen != v.previewGen {
			return nil
		}
		_, e := v.Selection()
		if e == nil || e.ID == "" || e.Type == "" {
			return nil
		}
		if _, cached := v.detail[e.ID]; cached {
			return nil
		}
		return v.ds.query(navPreviewOwner{v: v, id: e.ID}, msg.gen, catalog.DetailQuery(*e))

	case dataMsg:
		return v.handleData(msg)

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

func (v *navView) handleData(msg dataMsg) tea.Cmd {
	switch owner := msg.owner.(type) {
	case navNameOwner:
		if owner.v != v || msg.err != nil {
			return nil // resolution failures fall back to raw ids
		}
		for _, rec := range msg.records {
			if id := catalog.Str(rec, "id"); id != "" {
				v.names[id] = catalog.Str(rec, "name")
			}
		}
		if v.root.Name == "" {
			v.root.Name = v.names[v.root.ID]
		}
		// Names don't reorder rows (BuildEdges sorts by type+id on purpose),
		// but an active filter matches on them.
		if v.filter != "" {
			v.rebuildRows()
		}
		return nil

	case navSchemaOwner:
		if owner.v != v || msg.seq != v.seq {
			return nil
		}
		v.schemaLoading = false
		v.schemaErr = msg.err
		if msg.err == nil {
			v.schema = catalog.BuildSchema(msg.records)
		}
		return nil

	case navProblemsOwner:
		if owner.v != v || msg.seq != v.probSeq {
			return nil
		}
		// Errors degrade to a blank overlay — health must never block the map.
		if msg.err != nil {
			return nil
		}
		v.probByID = map[string][]map[string]any{}
		v.probByType = map[string]int{}
		for _, rec := range catalog.ActiveProblems(msg.records) {
			typesSeen := map[string]bool{}
			for _, id := range catalog.ProblemAffectedIDs(rec) {
				v.probByID[id] = append(v.probByID[id], rec)
				if typ := entityTypeOf(id); typ != "" && !typesSeen[typ] {
					typesSeen[typ] = true
					v.probByType[typ]++
				}
			}
		}
		v.probLoaded = true
		return nil

	case navPreviewOwner:
		if owner.v != v || msg.err != nil {
			return nil
		}
		// Cache even a stale-generation result — the fetch already happened.
		if len(msg.records) > 0 {
			v.detail[owner.id] = msg.records[0]
		} else {
			// Fetched-but-empty: an edge-only endpoint with no node record.
			v.detail[owner.id] = map[string]any{}
		}
		return nil
	}

	if msg.owner != any(v) || msg.seq != v.seq {
		return nil
	}
	v.loading = false
	v.err = msg.err
	if msg.err != nil {
		return nil
	}
	switch v.mode {
	case navOverview:
		v.census = msg.records
	case navBrowser:
		v.instances = msg.records
	case navWalk:
		v.edges = catalog.BuildEdges(v.root.ID, msg.records)
		v.nodeCache[v.root.ID] = v.edges
	}
	v.rebuildRows()
	if v.mode == navWalk {
		return tea.Batch(v.resolveNames(), v.schedulePreview())
	}
	return v.schedulePreview()
}

// entityTypeOf derives the node type from an id's prefix (both eras encode it
// there: "K8S_POD-16HEX").
func entityTypeOf(id string) string {
	if i := strings.LastIndex(id, "-"); i > 0 {
		return id[:i]
	}
	return ""
}

// --- keys -----------------------------------------------------------------------

func (v *navView) handleKey(msg tea.KeyMsg) tea.Cmd {
	if v.filterActive {
		switch msg.String() {
		case "enter":
			v.filterActive = false
			v.filterInput.Blur()
		case "esc":
			v.filterActive = false
			v.filterInput.Blur()
			v.filterInput.SetValue("")
			v.filter = ""
			v.rebuildRows()
		default:
			var cmd tea.Cmd
			v.filterInput, cmd = v.filterInput.Update(msg)
			v.filter = v.filterInput.Value()
			v.rebuildRows()
			return cmd
		}
		return nil
	}

	switch msg.String() {
	case "up", "k":
		v.move(-1)
		return v.schedulePreview()
	case "down", "j":
		v.move(1)
		return v.schedulePreview()
	case "pgup":
		v.move(-v.listHeight())
		return v.schedulePreview()
	case "pgdown":
		v.move(v.listHeight())
		return v.schedulePreview()
	case "home", "g":
		v.cursor, v.offset = 0, 0
		return v.schedulePreview()
	case "end", "G":
		v.move(len(v.rows))
		return v.schedulePreview()
	case "/":
		v.filterActive = true
		v.filterInput.Focus()
		return textinput.Blink
	case "enter":
		return v.activate(false)
	case "right":
		return v.activate(true)
	case "left", "backspace":
		if v.mode == navWalk {
			return v.backtrack()
		}
		return nil
	case " ":
		return v.toggleFold()
	case "d":
		if _, e := v.Selection(); e != nil {
			entity := *e
			return func() tea.Msg { return detailMsg{entity: entity} }
		}
		return statusErr("selection carries no entity to describe")
	case "i":
		if v.mode != navWalk {
			return nil
		}
		v.dirFilter = (v.dirFilter + 1) % 3
		v.rebuildRows()
		return status("direction: " + [...]string{"both", "outgoing only", "incoming only"}[v.dirFilter])
	case "M":
		if v.mode != navWalk {
			return nil
		}
		v.structOnly = !v.structOnly
		v.rebuildRows()
		if v.structOnly {
			return status("structure only — mesh edges (calls, routes to) hidden")
		}
		return status("mesh edges shown")
	case "tab":
		v.previewOn = !v.previewOn
		return nil
	case "l", "s", "v", "p", "m":
		return v.drill(map[string]string{
			"l": "logs", "s": "traces", "v": "events", "p": "problems", "m": "metrics",
		}[msg.String()])
	case "esc":
		if v.filter != "" {
			v.filterInput.SetValue("")
			v.filter = ""
			v.rebuildRows()
			return claimKey
		}
		return nil // app pops the stack
	}
	return nil
}

// activate is enter (or →, which only navigates — it never opens pages).
func (v *navView) activate(rightArrow bool) tea.Cmd {
	row := v.selectedRow()
	if row == nil {
		return nil
	}
	switch row.kind {
	case navRowType:
		typ := catalog.Str(row.rec, "type")
		if typ == "" {
			return nil
		}
		return func() tea.Msg { return navMsg{typ: typ} }
	case navRowInstance:
		e := navInstanceEntity(row.rec)
		if e == nil {
			return nil
		}
		root := *e
		return func() tea.Msg { return navMsg{root: &root} }
	case navRowRoot:
		if rightArrow {
			return nil
		}
		entity := v.root
		return func() tea.Msg { return detailMsg{entity: entity} }
	case navRowGroup:
		if rightArrow {
			v.collapsed[row.key] = false
			v.rebuildRows()
			return nil
		}
		return v.toggleFold()
	case navRowNeighbor:
		return v.reRoot(catalog.Entity{ID: row.edge.OtherID, Name: v.names[row.edge.OtherID], Type: row.edge.OtherType})
	case navRowMore:
		v.expanded[row.key] = true
		v.rebuildRows()
	}
	return nil
}

func (v *navView) toggleFold() tea.Cmd {
	row := v.selectedRow()
	if row == nil {
		return nil
	}
	switch row.kind {
	case navRowGroup, navRowNeighbor:
		v.collapsed[row.key] = !v.collapsed[row.key]
		v.rebuildRows()
	case navRowMore:
		v.expanded[row.key] = true
		v.rebuildRows()
	}
	return nil
}

// reRoot commits a hop: the old root joins the trail and the neighbor becomes
// the center. Cached nodes re-root with zero queries.
func (v *navView) reRoot(e catalog.Entity) tea.Cmd {
	if e.ID == "" || e.ID == v.root.ID {
		return nil
	}
	v.trail = append(v.trail, v.root)
	return v.setRoot(e)
}

// backtrack pops one hop off the trail (the graph-semantic back; esc stays
// the page-semantic back).
func (v *navView) backtrack() tea.Cmd {
	if len(v.trail) == 0 {
		return status("start of the walk — esc leaves the navigator")
	}
	last := v.trail[len(v.trail)-1]
	v.trail = v.trail[:len(v.trail)-1]
	return v.setRoot(last)
}

func (v *navView) setRoot(e catalog.Entity) tea.Cmd {
	if e.Name == "" {
		e.Name = v.names[e.ID]
	} else {
		v.names[e.ID] = e.Name
	}
	v.root = e
	v.cursor, v.offset = 0, 0
	v.collapsed = map[navGroupKey]bool{}
	v.expanded = map[navGroupKey]bool{}
	v.filterInput.SetValue("")
	v.filter = ""
	v.edges = nil
	// markHistory: a hop changes the page's identity without a navigation.
	return tea.Batch(v.fetchMain(), v.schedulePreview(), markHistory)
}

// drill opens a signal view scoped to the highlighted node — the same
// vocabulary as everywhere else in the TUI (l logs, s traces, v events,
// p problems, m metrics).
func (v *navView) drill(target string) tea.Cmd {
	_, e := v.Selection()
	if e == nil {
		return statusErr("selection carries no entity to scope by")
	}
	entity := *e
	if target == "metrics" {
		if catalog.MetricsFor(entity.Type) == nil {
			spec := catalog.Lookup("metrics")
			scope := catalog.Scope{Entity: &entity, Timeframe: v.tf}
			return func() tea.Msg { return pushViewMsg{spec: spec, scope: scope} }
		}
		return func() tea.Msg { return metricsMsg{entity: entity} }
	}
	if target == "traces" && !catalog.SpanScopable(entity.Type) {
		return statusErr(fmt.Sprintf("spans carry no %s scope field", entity.Type))
	}
	spec := catalog.Lookup(target)
	if spec == nil {
		return statusErr(fmt.Sprintf("unknown view %q", target))
	}
	scope := catalog.Scope{Entity: &entity, Timeframe: v.tf}
	if target == "traces" {
		scope.Lens = catalog.DefaultSpanLens(entity.Type)
	}
	return func() tea.Msg { return pushViewMsg{spec: spec, scope: scope} }
}

// --- rows -----------------------------------------------------------------------

func (v *navView) rebuildRows() {
	f := strings.ToLower(strings.TrimSpace(v.filter))
	var rows []navRow
	switch v.mode {
	case navOverview:
		for _, rec := range v.census {
			if f != "" && !strings.Contains(strings.ToLower(catalog.Str(rec, "type")), f) {
				continue
			}
			rows = append(rows, navRow{kind: navRowType, rec: rec})
		}
	case navBrowser:
		for _, rec := range v.instances {
			if f != "" && !navInstanceMatches(rec, f) {
				continue
			}
			rows = append(rows, navRow{kind: navRowInstance, rec: rec})
		}
	case navWalk:
		rows = append(rows, navRow{kind: navRowRoot})
		rows = append(rows, v.groupRows(f)...)
	}
	v.rows = rows
	if v.cursor >= len(v.rows) {
		v.cursor = maxInt(len(v.rows)-1, 0)
	}
	if v.offset > v.cursor {
		v.offset = v.cursor
	}
}

func navInstanceMatches(rec map[string]any, f string) bool {
	for _, key := range []string{"display", "name", "id"} {
		if strings.Contains(strings.ToLower(catalog.Str(rec, key)), f) {
			return true
		}
	}
	return false
}

// groupRows flattens the filtered edges into group headers and members,
// applying the fold state and the per-group render cap.
func (v *navView) groupRows(f string) []navRow {
	edges := v.visibleEdges(f)
	var out []navRow
	for i := 0; i < len(edges); {
		key := navGroupKey{out: edges[i].Outgoing, verb: edges[i].Verb}
		j := i
		for j < len(edges) && edges[j].Outgoing == key.out && edges[j].Verb == key.verb {
			j++
		}
		members := edges[i:j]
		out = append(out, navRow{kind: navRowGroup, key: key, count: len(members)})
		if !v.collapsed[key] {
			limit := navGroupCap
			// A cap that would hide a single row is pedantry — show it.
			if v.expanded[key] || len(members) <= limit+1 {
				limit = len(members)
			}
			for _, e := range members[:limit] {
				out = append(out, navRow{kind: navRowNeighbor, key: key, edge: e})
			}
			if rest := len(members) - limit; rest > 0 {
				out = append(out, navRow{kind: navRowMore, key: key, count: rest})
			}
		}
		i = j
	}
	return out
}

func (v *navView) visibleEdges(f string) []catalog.Edge {
	var out []catalog.Edge
	for _, e := range v.edges {
		if v.dirFilter == navDirOut && !e.Outgoing {
			continue
		}
		if v.dirFilter == navDirIn && e.Outgoing {
			continue
		}
		if v.structOnly && catalog.MeshVerb(e.Verb) {
			continue
		}
		if f != "" &&
			!strings.Contains(strings.ToLower(v.names[e.OtherID]), f) &&
			!strings.Contains(strings.ToLower(e.OtherID), f) &&
			!strings.Contains(strings.ToLower(e.OtherType), f) &&
			!strings.Contains(strings.ToLower(e.Verb), f) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func (v *navView) move(delta int) {
	v.cursor += delta
	if v.cursor >= len(v.rows) {
		v.cursor = len(v.rows) - 1
	}
	if v.cursor < 0 {
		v.cursor = 0
	}
	if v.cursor < v.offset {
		v.offset = v.cursor
	}
	if vis := maxInt(v.listHeight(), 1); v.cursor >= v.offset+vis {
		v.offset = v.cursor - vis + 1
	}
}

// --- preview --------------------------------------------------------------------

// schedulePreview arms the debounce timer for the highlighted node's detail
// fetch; rapid cursor movement keeps bumping the generation so only the rest
// position fires a query.
func (v *navView) schedulePreview() tea.Cmd {
	if !v.rightPaneVisible() || v.mode == navOverview {
		return nil
	}
	_, e := v.Selection()
	if e == nil || e.ID == "" || e.Type == "" {
		return nil
	}
	if _, cached := v.detail[e.ID]; cached {
		return nil
	}
	v.previewGen++
	gen := v.previewGen
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return navPreviewTickMsg{gen: gen} })
}

func (v *navView) rightPaneVisible() bool {
	return v.previewOn && v.width >= 100
}

// --- rendering ------------------------------------------------------------------

func (v *navView) listHeight() int {
	h := v.height - 1 // context/trail line
	if v.filterShown() {
		h--
	}
	return maxInt(h, 1)
}

func (v *navView) filterShown() bool { return v.filterActive || v.filter != "" }

func (v *navView) View(width, height int) string {
	v.width, v.height = width, height
	var b strings.Builder
	b.WriteString(ansi.Truncate(v.contextLine(width), width, "…") + "\n")
	if v.filterShown() {
		b.WriteString(ansi.Truncate(" "+v.filterInput.View(), width, "…") + "\n")
	}
	listH := v.listHeight()

	leftW := width
	var right []string
	rightW := 0
	if v.rightPaneVisible() {
		rightW = width * 2 / 5
		if rightW > 46 {
			rightW = 46
		}
		leftW = width - rightW - 3
		right = v.renderRight(rightW, listH)
	}
	left := v.renderList(leftW, listH)

	if right == nil {
		b.WriteString(strings.Join(left, "\n"))
		return b.String()
	}
	sep := theme.Rule.Render("│")
	for i := 0; i < listH; i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		b.WriteString(pad(l, leftW) + " " + sep + " " + ansi.Truncate(r, rightW, "…"))
		if i < listH-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// contextLine is the top line: a summary on the overview and browser, the
// walk trail in walk mode.
func (v *navView) contextLine(width int) string {
	switch v.mode {
	case navWalk:
		return v.trailLine(width)
	case navBrowser:
		line := " " + theme.OverlayTitle.Render(v.typ)
		if !v.loading && v.err == nil {
			line += theme.Dim.Render(fmt.Sprintf("  %d entities", len(v.instances)))
			if v.probLoaded {
				if n := v.probByType[v.typ]; n > 0 {
					line += theme.Error.Render(fmt.Sprintf("  ● %d with problems", n))
				}
			}
		}
		return line
	}
	line := " " + theme.OverlayTitle.Render("smartscape")
	if !v.loading && v.err == nil {
		total := 0
		for _, rec := range v.census {
			total += catalog.IntValue(rec["count"])
		}
		line += theme.Dim.Render(fmt.Sprintf("  %d types · %d entities", len(v.census), total))
		if v.probLoaded {
			active := map[string]bool{}
			for _, probs := range v.probByID {
				for _, p := range probs {
					active[catalog.Str(p, "display_id")] = true
				}
			}
			if len(active) > 0 {
				line += theme.Error.Render("  ⚠ " + plural(len(active), "active problem"))
			}
		}
	}
	return line
}

// trailLine renders the walk path, eliding the oldest hops when the line
// overflows — the current root and its immediate history matter most.
func (v *navView) trailLine(width int) string {
	sep := theme.CrumbDim.Render(" ▸ ")
	render := func(skip int) string {
		parts := []string{}
		if skip > 0 {
			parts = append(parts, theme.CrumbDim.Render(fmt.Sprintf("… +%d", skip)))
		}
		for _, e := range v.trail[skip:] {
			parts = append(parts, theme.CrumbDim.Render(entityName(e)))
		}
		parts = append(parts, theme.Crumb.Render(entityName(v.root)))
		return " " + strings.Join(parts, sep)
	}
	for skip := 0; skip < len(v.trail); skip++ {
		if line := render(skip); lipgloss.Width(line) <= width {
			return line
		}
	}
	return render(len(v.trail))
}

func (v *navView) renderList(w, h int) []string {
	switch {
	case v.loading:
		return []string{" " + theme.Spinner.Render(theme.Spin()+" "+v.loadingLabel())}
	case v.err != nil:
		return strings.Split(theme.Error.Render("✗ "+wrap(v.err.Error(), maxInt(w-2, 8))), "\n")
	}
	if v.emptyMessage() != "" && len(v.rows) <= v.emptyThreshold() {
		return []string{"", lipgloss.PlaceHorizontal(w, lipgloss.Center, theme.Dim.Render(v.emptyMessage()))}
	}
	end := v.offset + h
	if end > len(v.rows) {
		end = len(v.rows)
	}
	lines := make([]string, 0, end-v.offset)
	for i := v.offset; i < end; i++ {
		lines = append(lines, v.renderRow(v.rows[i], i == v.cursor, w))
	}
	return lines
}

func (v *navView) loadingLabel() string {
	switch v.mode {
	case navBrowser:
		return "listing " + v.typ + "…"
	case navWalk:
		return "walking topology…"
	}
	return "surveying topology…"
}

// emptyMessage / emptyThreshold: a walk view always carries the root row, so
// "empty" means one row there.
func (v *navView) emptyThreshold() int {
	if v.mode == navWalk {
		return 1
	}
	return 0
}

func (v *navView) emptyMessage() string {
	if v.filter != "" {
		return "∅ nothing matches the filter"
	}
	switch v.mode {
	case navOverview:
		return "∅ no smartscape entities"
	case navBrowser:
		return "∅ no " + v.typ + " entities"
	}
	if v.dirFilter != navDirBoth || v.structOnly {
		return "∅ no edges pass the direction/mesh toggles (i / M reset)"
	}
	return "∅ no Smartscape edges for this entity"
}

func (v *navView) renderRow(row navRow, selected bool, w int) string {
	plain, styled := v.rowText(row, w)
	if selected {
		return theme.Gutter.Render("▌") + theme.Selected.Render(pad(plain, w-1))
	}
	return ansi.Truncate(" "+styled, w, "…")
}

// rowText renders a row twice: plain for the selected line (its row style
// paints the whole line) and styled for everything else.
func (v *navView) rowText(row navRow, w int) (plain, styled string) {
	switch row.kind {
	case navRowType:
		typ := catalog.Str(row.rec, "type")
		count := catalog.IntValue(row.rec["count"])
		probs := ""
		if v.probLoaded && v.probByType[typ] > 0 {
			probs = fmt.Sprintf("● %d", v.probByType[typ])
		}
		typW := maxInt(w-16, 12)
		plain = pad(typ, typW) + cell(fmt.Sprintf("%d", count), 7, true) + "  " + probs
		styled = pad(typ, typW) + theme.Number.Render(cell(fmt.Sprintf("%d", count), 7, true)) + "  " + theme.Error.Render(probs)
		return plain, styled

	case navRowInstance:
		e := navInstanceEntity(row.rec)
		if e == nil {
			return "", ""
		}
		dot, dotStyled := v.probDot(e.ID)
		name := entityName(*e)
		plain = dot + " " + name
		styled = dotStyled + " " + name
		if e.Name != "" {
			plain += "  " + e.ID
			styled += theme.Dim.Render("  " + e.ID)
		}
		return plain, styled

	case navRowRoot:
		dot, dotStyled := v.probDot(v.root.ID)
		rels := ""
		if !v.loading && v.err == nil {
			rels = fmt.Sprintf(" · %d relations", len(v.edges))
			// The edge query is capped; a full page means the count is a floor,
			// not a total — never let truncation read as completeness.
			if len(v.edges) >= catalog.EdgeQueryLimit {
				rels = fmt.Sprintf(" · %d+ relations (edge limit)", len(v.edges))
			}
		}
		plain = dot + " " + entityName(v.root) + "  " + v.root.Type + rels
		styled = dotStyled + " " + theme.OverlayTitle.Render(entityName(v.root)) +
			"  " + theme.Badge.Render(v.root.Type) + theme.Dim.Render(rels)
		return plain, styled

	case navRowGroup:
		fold := "▾"
		if v.collapsed[row.key] {
			fold = "▸"
		}
		verb := strings.ReplaceAll(row.key.verb, "_", " ")
		if row.key.out {
			label := fmt.Sprintf("%s → (%d)", verb, row.count)
			return fold + " " + label, fold + " " + theme.ArrowOut.Render(label)
		}
		label := fmt.Sprintf("← %s (%d)", verb, row.count)
		return fold + " " + label, fold + " " + theme.ArrowIn.Render(label)

	case navRowNeighbor:
		dot, dotStyled := v.probDot(row.edge.OtherID)
		name := v.names[row.edge.OtherID]
		nameStyled := name
		if name == "" {
			name = row.edge.OtherID
			nameStyled = theme.Dim.Render(name) // nodeless or unresolved: raw id
		}
		typW := 22
		plain = "   " + dot + " " + pad(row.edge.OtherType, typW) + " " + name
		styled = "   " + dotStyled + " " + theme.Dim.Render(pad(row.edge.OtherType, typW)) + " " + nameStyled
		return plain, styled

	case navRowMore:
		label := fmt.Sprintf("     … +%d more (enter shows all)", row.count)
		return label, theme.Dim.Render(label)
	}
	return "", ""
}

// plural renders "1 active problem" / "3 active problems".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// probDot renders a node's health dot: red with active problems, quiet dot
// otherwise, blank while the overlay hasn't loaded.
func (v *navView) probDot(id string) (plain, styled string) {
	if !v.probLoaded {
		return " ", " "
	}
	if len(v.probByID[id]) > 0 {
		return "●", theme.Error.Render("●")
	}
	return "·", theme.Dim.Render("·")
}

// renderRight renders the right pane: the schema neighborhood on the
// overview, the node preview elsewhere.
func (v *navView) renderRight(w, h int) []string {
	var lines []string
	if v.mode == navOverview {
		lines = v.schemaPane(w)
	} else {
		lines = v.previewPane(w)
	}
	if len(lines) > h {
		rest := len(lines) - h + 1
		lines = append(lines[:h-1], theme.Dim.Render(fmt.Sprintf("… +%d more", rest)))
	}
	return lines
}

// schemaPane shows the highlighted type's relationship schema: which verbs
// connect it to which peer types, with edge counts.
func (v *navView) schemaPane(w int) []string {
	row := v.selectedRow()
	if row == nil || row.kind != navRowType {
		return []string{theme.Dim.Render("select a type")}
	}
	typ := catalog.Str(row.rec, "type")
	lines := []string{
		theme.OverlayTitle.Render(typ),
		theme.Dim.Render(fmt.Sprintf("%d entities", catalog.IntValue(row.rec["count"]))) + v.typeProblemSuffix(typ),
	}
	switch {
	case v.schemaLoading:
		return append(lines, "", theme.Spinner.Render(theme.Spin()+" aggregating schema…"))
	case v.schemaErr != nil:
		return append(lines, "", theme.Dim.Render("schema unavailable: "+v.schemaErr.Error()))
	}
	var out, in []string
	for _, e := range v.schema {
		verb := strings.ReplaceAll(e.Verb, "_", " ")
		if e.SourceType == typ {
			out = append(out, "  "+theme.ArrowOut.Render(verb+" ▸")+" "+pad(e.TargetType, maxInt(w-len(verb)-14, 8))+theme.Number.Render(fmt.Sprintf("%6d", e.Count)))
		}
		if e.TargetType == typ {
			in = append(in, "  "+theme.ArrowIn.Render(verb+" ◂")+" "+pad(e.SourceType, maxInt(w-len(verb)-14, 8))+theme.Number.Render(fmt.Sprintf("%6d", e.Count)))
		}
	}
	if len(out) == 0 && len(in) == 0 {
		return append(lines, "", theme.Dim.Render("no edges touch this type"))
	}
	if len(out) > 0 {
		lines = append(lines, "", theme.Section("outgoing"))
		lines = append(lines, out...)
	}
	if len(in) > 0 {
		lines = append(lines, "", theme.Section("incoming"))
		lines = append(lines, in...)
	}
	return lines
}

func (v *navView) typeProblemSuffix(typ string) string {
	if v.probLoaded && v.probByType[typ] > 0 {
		return theme.Error.Render(fmt.Sprintf(" · ● %d problems", v.probByType[typ]))
	}
	return ""
}

// previewPane shows the highlighted node without committing a hop: identity,
// health from the overlay, and the curated key facts from the (debounced,
// cached) detail fetch.
func (v *navView) previewPane(w int) []string {
	_, e := v.Selection()
	if e == nil && v.mode == navWalk {
		root := v.root
		e = &root // group/more rows: preview the center
	}
	if e == nil {
		return []string{theme.Dim.Render("select a node")}
	}
	lines := []string{
		theme.OverlayTitle.Render(entityName(*e)),
		theme.Badge.Render(e.Type) + theme.Dim.Render(" "+e.ID),
		"",
	}
	// Health, from the shared overlay — no extra query.
	switch {
	case !v.probLoaded:
		lines = append(lines, theme.Dim.Render("… problems"))
	case len(v.probByID[e.ID]) > 0:
		probs := v.probByID[e.ID]
		lines = append(lines, theme.Error.Render("⚠ "+plural(len(probs), "active problem")))
		for i, p := range probs {
			if i == 3 {
				lines = append(lines, theme.Dim.Render(fmt.Sprintf("  … +%d more", len(probs)-3)))
				break
			}
			lines = append(lines, "  "+theme.Error.Render(catalog.Str(p, "display_id"))+" "+
				ansi.Truncate(catalog.Str(p, "event.name"), maxInt(w-12, 8), "…"))
		}
	default:
		lines = append(lines, theme.Dim.Render("no active problems (24h)"))
	}
	lines = append(lines, "", theme.Section("key facts"))
	rec, fetched := v.detail[e.ID]
	switch {
	case !fetched:
		lines = append(lines, theme.Dim.Render("  "+theme.Spin()+" fetching…"))
	case len(rec) == 0:
		lines = append(lines, theme.Dim.Render("  ∅ no node record (edge-only endpoint)"))
	default:
		shown := 0
		for _, fact := range catalog.KeyFacts(e.Type) {
			val := fact.Value(rec)
			if val == "" {
				continue
			}
			lines = append(lines, "  "+theme.FactLabel.Render(fact.Label+":")+" "+
				ansi.Truncate(val, maxInt(w-len(fact.Label)-5, 8), "…"))
			shown++
		}
		if shown == 0 {
			lines = append(lines, theme.Dim.Render("  (no curated facts — d for the full record)"))
		}
	}
	return lines
}
