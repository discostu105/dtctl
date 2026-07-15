package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
)

// navView is the smartscape navigator (:nav) — a dedicated app for exploring
// the topology graph (docs/design/smartscape-navigator.md). Three levels,
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

	// browser state. search puts the browser in name-resolution flavor
	// (:nav <name>): instances are the cross-type name matches and a unique
	// match re-shapes the view into a walk rooted there. searchFallback
	// carries an ambiguous lowercase argument (":nav payments" is
	// type-shaped too): a type browse that lands empty retries it as the
	// name search instead of dead-ending.
	typ            string
	search         string
	searchFallback string
	instances      []map[string]any

	// walk state.
	root       catalog.Entity
	trail      []catalog.Entity // hops that led here, oldest first
	edges      []catalog.Edge   // current root's edges (sorted by BuildEdges)
	collapsed  map[navGroupKey]bool
	expanded   map[navGroupKey]bool // groups past the render cap shown fully
	dirFilter  navDir
	structOnly bool // hide the communication mesh (calls, routes_to)

	// session caches, shared across hops within this navigator instance.
	names     map[string]string         // id → display name
	nodeCache map[string][]catalog.Edge // id → its edges (0-query backtracks)
	detail    map[string]map[string]any // id → full node record (preview)

	// health overlay: one tenant-wide problem query per refresh, intersected
	// client-side — never a query per node.
	probSeq    int
	probLoaded bool
	probByID   map[string][]map[string]any // entity id (both eras) → problems
	probByType map[string]int              // node type → #problems touching it

	// preview debounce: cursor movement bumps the generation; the fetch fires
	// only when the tick comes back with the current one. Whether the pane
	// shows at all follows the app-wide preference (ds.previewOn, P toggles).
	previewGen int

	rows []navRow // flattened cursor rows for the current mode

	filterActive bool
	filter       string
	filterInput  textinput.Model

	scroller
	loading       bool
	err           error
	seq           int
	dql           string
	width, height int
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
	fi := newTextInput()
	fi.Prompt = "/"
	fi.CharLimit = 64
	return &navView{
		ds: ds, tf: tf, mode: navOverview,
		names:       map[string]string{},
		nodeCache:   map[string][]catalog.Edge{},
		detail:      map[string]map[string]any{},
		collapsed:   map[navGroupKey]bool{},
		expanded:    map[navGroupKey]bool{},
		filterInput: fi,
	}
}

func newNavBrowserView(ds *dataSource, typ string, tf catalog.Timeframe) *navView {
	v := newNavView(ds, tf)
	v.mode = navBrowser
	v.typ = typ
	return v
}

// newNavSearchView resolves a name argument (:nav payments): a browser-mode
// view whose list query matches names across every type. The disambiguation
// list IS the browser — enter walks a match, health dots and the preview
// pane apply unchanged.
func newNavSearchView(ds *dataSource, term string, tf catalog.Timeframe) *navView {
	v := newNavView(ds, tf)
	v.mode = navBrowser
	v.search = strings.TrimSpace(term)
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
		if v.search != "" {
			return fmt.Sprintf("nav %q", v.search)
		}
		return v.typ
	case navWalk:
		return "walk (" + entityName(v.root) + ")"
	}
	return "smartscape"
}

func (v *navView) DQL() string { return v.dql }

func (v *navView) Echo() string { return v.ds.echoQuery(v.dql) }

func (v *navView) Hints() []keyHint {
	if v.filterActive {
		return []keyHint{{"enter", "apply"}, {"esc", "clear"}}
	}
	switch v.mode {
	case navBrowser:
		return []keyHint{{"enter", "walk"}, {"d", "details"}, {"/", "filter"}, {".", "pin"}, {"o", "open"}}
	case navWalk:
		return []keyHint{{"enter", "walk to"}, {"←", "back"}, {"d", "details"}, {"z", "fold"},
			{"i", "direction"}, {"M", "mesh"}, {"/", "filter"}}
	}
	hints := []keyHint{{"enter", "browse type"}, {"/", "filter"}}
	if !v.ds.previewOn() {
		hints = append(hints, keyHint{"P", "pane"})
	}
	return hints
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
