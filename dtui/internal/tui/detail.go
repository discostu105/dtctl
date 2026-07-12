package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtctl/pkg/output"
	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
	"github.com/dynatrace-oss/dtui/internal/tui/theme"
)

// rowCounter lets a tabbed page badge a tab with the row count it has loaded
// — "where is the signal?" answered without visiting every tab. Implemented
// by views whose fetched row count is meaningful (tables, relations).
type rowCounter interface {
	// RowCount returns the fetched row count and whether it is valid yet
	// (false while loading, after an error, or before the first fetch).
	RowCount() (int, bool)
}

// detailTab is one tab of a tabbed page (entity detail, problem).
type detailTab struct {
	name    string
	view    viewModel
	started bool
}

// tabSet is the machinery shared by the tabbed pages: the held tabs, the
// active index, and the tab/digit/bracket key protocol. Pages embed it and
// keep their own chrome (two header lines) and message handling.
type tabSet struct {
	tabs   []detailTab
	active int

	width, height int
}

func (ts *tabSet) activeView() viewModel { return ts.tabs[ts.active].view }

// startFirst marks and initializes the first tab (the page's Init).
func (ts *tabSet) startFirst() tea.Cmd {
	ts.tabs[0].started = true
	return ts.tabs[0].view.Init()
}

func (ts *tabSet) Refresh() tea.Cmd  { return ts.activeView().Refresh() }
func (ts *tabSet) InputActive() bool { return ts.activeView().InputActive() }
func (ts *tabSet) Echo() string      { return ts.activeView().Echo() }

// Busy delegates to the active tab (animates the spinner while it loads).
func (ts *tabSet) Busy() bool {
	if br, ok := ts.activeView().(busyReporter); ok {
		return br.Busy()
	}
	return false
}

// DQL reveals the active tab's query (ctrl+q).
func (ts *tabSet) DQL() string {
	if p, ok := ts.activeView().(dqlProvider); ok {
		return p.DQL()
	}
	return ""
}

// YankText forwards the active tab's field-level yank.
func (ts *tabSet) YankText() (string, string, bool) {
	if yp, ok := ts.activeView().(yankProvider); ok {
		return yp.YankText()
	}
	return "", "", false
}

func (ts *tabSet) Hints() []keyHint {
	// The digit hint travels with the innermost numbered strip: the active
	// tab's own hints lead with "1-9 lens" when it shows a strip.
	hints := []keyHint{{"1-9/tab", "tabs"}}
	if ts.lensedInner() != nil {
		hints = []keyHint{{"tab", "tabs"}}
	}
	return append(hints, ts.activeView().Hints()...)
}

// tabJumps maps the drill vocabulary onto same-named page tabs: pressing l on
// an entity page lands on its logs tab directly — the letters mean the same
// signals everywhere.
var tabJumps = map[string]string{
	"l": "logs", "s": "traces", "v": "events", "p": "problems",
	"m": "metrics", "u": "sessions", "e": "userevents", "a": "attacks",
}

// ClaimsDigits marks the page as entered: its innermost numbered strip owns
// the digit keys while it is on top (the numbering on screen is the mode
// indicator); everywhere without visible numbers the digits stay the global
// bookmarks, and esc restores them.
func (ts *tabSet) ClaimsDigits() bool { return len(ts.tabs) > 1 }

// lensedInner returns the active tab's table when it carries a lens strip —
// then the strip is the innermost numbered strip and the digits address it.
func (ts *tabSet) lensedInner() *tableView {
	if tv, ok := ts.activeView().(*tableView); ok && len(tv.spec.Lenses) > 0 {
		return tv
	}
	return nil
}

// adoptTabs marks every table child as nested in an entered page, so a
// lensed tab renders digit labels on its lens strip (the innermost numbered
// strip — the tab bar drops its own numbers while one is visible).
func (ts *tabSet) adoptTabs() {
	for _, t := range ts.tabs {
		if tv, ok := t.view.(*tableView); ok {
			tv.lensDigits = true
		}
	}
}

// tabKey handles tab-switching keys; everything else falls through to the
// active tab's view. Entering the page rescoped the keyboard to it: the
// digits address the numbered tab bar directly, tab/shift+tab cycle it, the
// brackets always fall through to the active tab's own lens strip (never the
// tabs — the same key must not change meaning between tabs), and the drill
// letters jump straight to their tab.
func (ts *tabSet) tabKey(key string) (tea.Cmd, bool) {
	switch key {
	case "tab":
		return ts.setActive((ts.active + 1) % len(ts.tabs)), true
	case "shift+tab":
		return ts.setActive((ts.active + len(ts.tabs) - 1) % len(ts.tabs)), true
	}
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		i := int(key[0] - '1')
		// The digits address the innermost numbered strip: the active tab's
		// lens strip when it shows one, else the tab bar (0 stays the global
		// jump home). A digit the strip doesn't show teaches instead of
		// jumping somewhere invisible.
		if inner := ts.lensedInner(); inner != nil {
			if i < len(inner.spec.Lenses) {
				return inner.setLens(i, false), true
			}
			return status("digits pick lenses here — tab or letters switch tabs"), true
		}
		if i < len(ts.tabs) {
			return ts.setActive(i), true
		}
		return status("digits pick tabs here — esc first for the global bookmarks"), true
	}
	// A letter the active tab's rows drill by (l on an evidence row scopes to
	// THAT row's source entity) keeps its per-row meaning — the tab jump only
	// catches letters the active view would otherwise drop.
	if name, ok := tabJumps[key]; ok && !ts.activeDrills(key) {
		for i, t := range ts.tabs {
			if t.name == name {
				return ts.setActive(i), true
			}
		}
	}
	return nil, false
}

// activeDrills reports whether the active tab's view drills by this key.
func (ts *tabSet) activeDrills(key string) bool {
	if tv, ok := ts.activeView().(*tableView); ok {
		_, has := tv.spec.Drills[key]
		return has
	}
	return false
}

func (ts *tabSet) setActive(i int) tea.Cmd {
	if i == ts.active {
		return nil
	}
	ts.active = i
	tab := &ts.tabs[i]
	cmds := []tea.Cmd{tab.view.Update(ts.childSize())}
	if !tab.started {
		tab.started = true
		cmds = append(cmds, tab.view.Init())
	}
	return tea.Batch(cmds...)
}

func (ts *tabSet) childSize() bodySizeMsg {
	// Four chrome lines: identity header, pulse/summary line, tab bar,
	// separator rule.
	return bodySizeMsg{width: ts.width, height: max(ts.height-4, 1)}
}

// resizeTabs records the page size and re-sizes every tab.
func (ts *tabSet) resizeTabs(width, height int) tea.Cmd {
	ts.width, ts.height = width, height
	var cmds []tea.Cmd
	for i := range ts.tabs {
		if cmd := ts.tabs[i].view.Update(ts.childSize()); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// forwardData hands a data result to every tab; each view drops results it
// doesn't own.
func (ts *tabSet) forwardData(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for i := range ts.tabs {
		if cmd := ts.tabs[i].view.Update(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// setTimeframeTabs applies a new window to every tab. Unstarted tabs only
// record the new scope; their query runs on first activation anyway.
func (ts *tabSet) setTimeframeTabs(tf catalog.Timeframe) tea.Cmd {
	var cmds []tea.Cmd
	for i := range ts.tabs {
		cmd := ts.tabs[i].view.SetTimeframe(tf)
		if ts.tabs[i].started && cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// tabBar renders the tab strip with a row-count badge on every tab that has
// loaded one. Exactly one strip on screen is numbered — the innermost — so
// the tabs carry digit labels only while the active tab shows no lens strip
// of its own (the numbers on screen say what the digits do).
func (ts *tabSet) tabBar() string {
	numbered := ts.lensedInner() == nil
	labels := make([]string, len(ts.tabs))
	for i, t := range ts.tabs {
		label := t.name
		if numbered {
			label = fmt.Sprintf("%d %s", i+1, t.name)
		}
		if rc, ok := t.view.(rowCounter); ok && t.started {
			if n, valid := rc.RowCount(); valid {
				label = fmt.Sprintf("%s (%d)", label, n)
			}
		}
		if i == ts.active {
			labels[i] = theme.TabActive.Render(label)
		} else {
			labels[i] = theme.TabInactive.Render(label)
		}
	}
	return " " + strings.Join(labels, " ")
}

// renderPage assembles the page: two header lines, the tab bar, a rule, and
// the active tab's body.
func (ts *tabSet) renderPage(width, height int, line1, line2 string) string {
	ts.width, ts.height = width, height
	rule := theme.Rule.Render(strings.Repeat("─", max(width, 0)))
	return ansi.Truncate(line1, width, "…") + "\n" +
		ansi.Truncate(line2, width, "…") + "\n" +
		ansi.Truncate(ts.tabBar(), width, "…") + "\n" + rule + "\n" +
		ts.activeView().View(width, max(height-4, 1))
}

// detailView is the tabbed entity page behind enter on entity views: the key
// facts and full properties up front, with the entity's metrics, logs,
// events, and problems one tab away. Tabs are real views (pre-scoped
// tableViews / metricsView) loaded lazily on first activation, so switching
// back and forth keeps data and cursor intact.
//
// The header carries a pulse line — active-problem count (one cheap
// dt.davis.problems query per page open), the list row's enrichment
// sparklines (free — the data already rode in on the row), and the entity's
// age — so "is this thing on fire?" is answered on every tab.
type detailView struct {
	tabSet
	entity catalog.Entity
	rec    map[string]any // the list row (enrichment sparklines); nil on id-jumps
	ds     *dataSource
	tf     catalog.Timeframe

	pulseSeq    int
	pulse       []map[string]any // active problems, latest record each
	pulseLoaded bool

	// Vitals: the details tab's utilization block (the Vital-marked subset
	// of the type's canned metrics), fetched page-level like the pulse.
	vitalsSeq  int
	vitalsSpec *catalog.MetricsSpec // nil = type has no vitals
}

// pulseOwner tags the page's active-problem query.
type pulseOwner struct{ v *detailView }

// vitalsProbeOwner and vitalsOwner tag the vitals block's two query phases
// (availability probe, then the timeseries over the available subset).
type vitalsProbeOwner struct{ v *detailView }
type vitalsOwner struct{ v *detailView }

// containmentTabs names each type's most relevant next entities — the
// opinionated first hops (a host's processes, a pod's containers) served as
// pre-scoped tables on the first tabs after the details. A service gets its
// deployment surface — the pods and processes it runs on, joined through its
// runs_on edges. The related tab keeps the full unranked edge list for
// everything else.
var containmentTabs = map[string][]string{
	"HOST":            {"processes"},
	"SERVICE":         {"pods", "processes"},
	"K8S_POD":         {"containers"},
	"K8S_NODE":        {"pods"},
	"K8S_DEPLOYMENT":  {"pods"},
	"K8S_STATEFULSET": {"pods"},
	"K8S_DAEMONSET":   {"pods"},
	"K8S_NAMESPACE":   {"workloads"},
	"K8S_CLUSTER":     {"nodes"},
}

func newDetailView(ds *dataSource, entity catalog.Entity, rec map[string]any, tf catalog.Timeframe) *detailView {
	v := &detailView{entity: entity, rec: rec, ds: ds, tf: tf,
		vitalsSpec: catalog.VitalsFor(entity.Type)}
	// Signal tabs share a pointer to the page entity: when an id-only jump
	// (an inspector entity link) learns the name from the detail fetch, the
	// still-unstarted tabs compose it into their scope filters — K8s log
	// scoping matches by plain k8s.* names, so the name is load-bearing.
	scope := catalog.Scope{Entity: &v.entity, Timeframe: tf}
	v.tabs = []detailTab{
		{name: "details", view: newEntityInfoView(ds, entity, rec)},
	}
	// The type's most relevant next entities, first tabs after the details:
	// a host's processes, a pod's containers, a service's pods and processes
	// — pre-scoped real tables (sparklines, sorting, drills), not raw edges.
	// Note a host's pods still live one hop out (host → related → its
	// K8S_NODE → pods): pods edge to the node in Smartscape, not to the host.
	for _, name := range containmentTabs[entity.Type] {
		if spec := catalog.Lookup(name); spec != nil {
			v.tabs = append(v.tabs, detailTab{name: name, view: newTableView(ds, spec, scope)})
		}
	}
	// Every entity gets a metrics tab: the canned charts where a type has
	// them (enter opens the explorer from there), the scoped metric explorer
	// where it doesn't.
	if catalog.MetricsFor(entity.Type) != nil {
		v.tabs = append(v.tabs, detailTab{name: "metrics", view: newMetricsView(ds, entity, tf)})
	} else if spec := catalog.Lookup("metrics"); spec != nil {
		v.tabs = append(v.tabs, detailTab{name: "metrics", view: newTableView(ds, spec, scope)})
	}
	signalTabs := []string{"logs", "events", "problems"}
	switch {
	case entity.Type == "FRONTEND":
		// A frontend's terrain is RUM: its user sessions and events replace
		// logs/traces (frontends emit no log records, spans carry no
		// frontend scope field) — the frontend page connects straight into
		// the session story.
		signalTabs = []string{"sessions", "userevents", "events", "problems"}
	case catalog.SpanScopable(entity.Type):
		signalTabs = []string{"logs", "traces", "events", "problems"}
	}
	for _, name := range signalTabs {
		if spec := catalog.Lookup(name); spec != nil {
			tabScope := scope
			if name == "traces" {
				// Scoped tabs skip the roots lens — see DefaultSpanLens.
				tabScope.Lens = catalog.DefaultSpanLens(entity.Type)
			}
			v.tabs = append(v.tabs, detailTab{name: name, view: newTableView(ds, spec, tabScope)})
		}
	}
	// Every entity's topology neighbors, on the last tab: a host's containers
	// and K8s node, a service's callers. enter navigates to the neighbor, x
	// keeps walking. Last because the curated tabs answer the common
	// questions; the raw edge list is the fallback for everything else.
	v.tabs = append(v.tabs, detailTab{name: "related", view: newEmbeddedRelationsView(ds, entity, tf)})
	v.adoptTabs()
	return v
}

// Selection exposes the most specific entity for app-level actions (pin,
// relations, open in browser): an entity link highlighted on the details tab
// or a neighbor highlighted on the related tab wins over the page's entity.
func (v *detailView) Selection() (map[string]any, *catalog.Entity) {
	switch t := v.activeView().(type) {
	case *inspectorView:
		if _, e := t.Selection(); e != nil {
			return nil, e
		}
	case *relationsView:
		if _, e := t.Selection(); e != nil {
			return nil, e
		}
	}
	entity := v.entity
	return nil, &entity
}

func (v *detailView) Init() tea.Cmd {
	return tea.Batch(v.startFirst(), v.pulseCmd(), v.vitalsCmd())
}

// pulseCmd fires the header's active-problem query.
func (v *detailView) pulseCmd() tea.Cmd {
	if v.ds == nil {
		return nil
	}
	v.pulseSeq++
	v.pulseLoaded = false
	return v.ds.query(pulseOwner{v}, v.pulseSeq, catalog.ProblemPulseQuery(v.entity, v.tf))
}

// vitalsCmd fires the details tab's utilization block, availability probe
// first — a timeseries returns zero records when ANY requested key is
// entirely absent, so charting the fixed vitals list blindly would blank the
// block for entities missing one metric.
func (v *detailView) vitalsCmd() tea.Cmd {
	if v.ds == nil || v.vitalsSpec == nil {
		return nil
	}
	v.vitalsSeq++
	return v.ds.query(vitalsProbeOwner{v}, v.vitalsSeq, v.vitalsSpec.AvailabilityQuery(v.entity, v.tf))
}

// vitalStats extracts the vitals rows from the timeseries record: one row
// per spec series that actually reported (absent keys were never queried,
// all-null series carry nothing worth a row).
func (v *detailView) vitalStats(rec map[string]any) []vitalStat {
	var stats []vitalStat
	for _, s := range v.vitalsSpec.Series {
		series := floatSeries(rec[s.Alias])
		if len(series) == 0 {
			continue
		}
		stats = append(stats, vitalStat{title: s.Title, unit: s.Unit, key: s.Key, series: series})
	}
	return stats
}

func (v *detailView) SetTimeframe(tf catalog.Timeframe) tea.Cmd {
	v.tf = tf
	return tea.Batch(v.setTimeframeTabs(tf), v.pulseCmd(), v.vitalsCmd())
}

// Refresh refetches the visible tab and the page-level queries (pulse,
// vitals) together.
func (v *detailView) Refresh() tea.Cmd {
	return tea.Batch(v.tabSet.Refresh(), v.pulseCmd(), v.vitalsCmd())
}

func (v *detailView) Crumb() string { return entityName(v.entity) }

func (v *detailView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bodySizeMsg:
		return v.resizeTabs(msg.width, msg.height)

	case dataMsg:
		if vo, ok := msg.owner.(vitalsProbeOwner); ok {
			if vo.v != v || msg.seq != v.vitalsSeq {
				return nil
			}
			if msg.err != nil {
				// Probe failed — chart the full vitals list; a fully absent
				// key then blanks the block, which is the honest fallback.
				return v.ds.query(vitalsOwner{v}, v.vitalsSeq, v.vitalsSpec.Query(v.entity, v.tf, nil))
			}
			available := map[string]bool{}
			for _, rec := range msg.records {
				if k := catalog.Str(rec, "metric.key"); k != "" {
					available[k] = true
				}
			}
			dql := v.vitalsSpec.Query(v.entity, v.tf, available)
			if dql == "" {
				return nil // nothing reports; the block stays absent
			}
			return v.ds.query(vitalsOwner{v}, v.vitalsSeq, dql)
		}
		if vo, ok := msg.owner.(vitalsOwner); ok {
			// Errors and empties degrade to an absent block — the vitals
			// must never noise up the page.
			if vo.v != v || msg.seq != v.vitalsSeq || msg.err != nil || len(msg.records) == 0 {
				return nil
			}
			if iv, ok := v.tabs[0].view.(*inspectorView); ok {
				iv.setVitals(v.vitalStats(msg.records[0]), v.tf.Label)
			}
			return nil
		}
		if po, ok := msg.owner.(pulseOwner); ok {
			if po.v != v || msg.seq != v.pulseSeq {
				return nil
			}
			// Errors degrade to a blank slot — the pulse must never block
			// or noise up the page.
			if msg.err == nil {
				v.pulse = catalog.ActiveProblems(msg.records)
				v.pulseLoaded = true
				// The details tab renders the same problems as its signals
				// block, one enter away from the problem page.
				if iv, ok := v.tabs[0].view.(*inspectorView); ok {
					iv.setProblems(v.pulse)
				}
			}
			return nil
		}
		// Forward to every tab; each view drops results it doesn't own.
		cmd := v.forwardData(msg)
		// An id-only jump learns the entity's name from the details-tab
		// fetch; unstarted signal tabs pick it up via the shared scope
		// pointer when they compose their queries.
		if v.entity.Name == "" {
			if iv, ok := v.tabs[0].view.(*inspectorView); ok && iv.entity != nil {
				v.entity.Name = iv.entity.Name
			}
		}
		return cmd

	case tea.KeyMsg:
		if !v.activeView().InputActive() {
			if cmd, handled := v.tabKey(msg.String()); handled {
				return cmd
			}
		}
		return v.activeView().Update(msg)
	}
	return v.activeView().Update(msg)
}

// pulseLine renders the header's second line: problem count, the list row's
// enrichment sparklines, and the entity's age.
func (v *detailView) pulseLine() string {
	var parts []string
	switch {
	case !v.pulseLoaded:
		// Loading or failed: hold the slot without claiming anything.
		parts = append(parts, theme.Dim.Render("… problems"))
	case len(v.pulse) > 0:
		label := fmt.Sprintf("⚠ %d active problem", len(v.pulse))
		if len(v.pulse) > 1 {
			label += "s"
		}
		parts = append(parts, theme.Error.Render(label))
	default:
		parts = append(parts, theme.Dim.Render("no active problems ("+v.pulseWindowLabel()+")"))
	}
	for _, s := range enrichSparks(v.rec) {
		parts = append(parts, s)
	}
	if age := v.seenAge(); age != "" {
		parts = append(parts, theme.Dim.Render("seen "+age))
	}
	return " " + strings.Join(parts, theme.HeaderSep.Render("  ·  "))
}

// pulseWindowLabel names the window the pulse actually queried (floored at
// 24h — see ProblemPulseQuery).
func (v *detailView) pulseWindowLabel() string {
	if v.tf.Dur > 24*time.Hour {
		return v.tf.Label
	}
	return "24h"
}

// seenAge is how long the entity has been known, from the freshest record at
// hand (the details tab's fetch upgrades the list row).
func (v *detailView) seenAge() string {
	rec := v.rec
	if iv, ok := v.tabs[0].view.(*inspectorView); ok && iv.rec != nil {
		rec = iv.rec
	}
	if rec == nil {
		return ""
	}
	lifetime, _ := rec["lifetime"].(map[string]any)
	if lifetime == nil {
		return ""
	}
	return catalog.Age(catalog.Str(lifetime, "start"))
}

// enrichSparks renders the record's enrichment series ("__enrich.req" …) as
// labeled mini-sparklines — free signal, the list already fetched it.
func enrichSparks(rec map[string]any) []string {
	if rec == nil {
		return nil
	}
	var keys []string
	for k := range rec {
		if strings.HasPrefix(k, "__enrich.") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		series := catalog.FloatSeries(rec[k])
		if len(series) == 0 {
			continue
		}
		alias := strings.TrimPrefix(k, "__enrich.")
		out = append(out, theme.Dim.Render(alias+" ")+theme.Chart.Render(output.MiniGraph(series, 8)))
	}
	return out
}

func (v *detailView) View(width, height int) string {
	identity := " " + theme.OverlayTitle.Render(entityName(v.entity)) +
		"  " + theme.Badge.Render(v.entity.Type) +
		theme.Dim.Render("  "+v.entity.ID)
	return v.renderPage(width, height, identity, v.pulseLine())
}
