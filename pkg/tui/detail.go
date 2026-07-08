package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
	"github.com/dynatrace-oss/dtctl/pkg/tui/theme"
)

// detailView is the tabbed entity page behind enter on entity views: the key
// facts and full properties up front, with the entity's metrics, logs,
// events, and problems one tab away. Tabs are real views (pre-scoped
// tableViews / metricsView) loaded lazily on first activation, so switching
// back and forth keeps data and cursor intact.
type detailView struct {
	entity catalog.Entity
	tabs   []detailTab
	active int

	width, height int
}

type detailTab struct {
	name    string
	view    viewModel
	started bool
}

func newDetailView(ds *dataSource, entity catalog.Entity, rec map[string]any, tf catalog.Timeframe) *detailView {
	v := &detailView{entity: entity}
	// Signal tabs share a pointer to the page entity: when an id-only jump
	// (an inspector entity link) learns the name from the detail fetch, the
	// still-unstarted tabs compose it into their scope filters — K8s log
	// scoping matches by plain k8s.* names, so the name is load-bearing.
	scope := catalog.Scope{Entity: &v.entity, Timeframe: tf}
	v.tabs = []detailTab{
		{name: "details", view: newEntityInfoView(ds, entity, rec)},
		// Every entity's topology neighbors, one tab away: a host's
		// processes, containers, and K8s node; a service's callers. enter
		// navigates to the neighbor, x keeps walking.
		{name: "related", view: newEmbeddedRelationsView(ds, entity, tf)},
	}
	// Containment tab: a K8s node's pods as a pre-scoped pods table — the
	// same hop the nodes list offers via enter, kept on the node's page. A
	// host's pods live here too (host → related → its K8S_NODE → pods): pods
	// edge to the node in Smartscape, not to the host.
	if entity.Type == "K8S_NODE" {
		if spec := catalog.Lookup("pods"); spec != nil {
			v.tabs = append(v.tabs, detailTab{name: "pods", view: newTableView(ds, spec, scope)})
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
				// GenAI entities open on the genai lens: their chat/tool
				// spans nest deep, so the default roots lens is silently
				// empty (and the genai columns are the point).
				tabScope.Lens = catalog.DefaultSpanLens(entity.Type)
			}
			v.tabs = append(v.tabs, detailTab{name: name, view: newTableView(ds, spec, tabScope)})
		}
	}
	return v
}

// Selection exposes the most specific entity for app-level actions (pin,
// relations, open in browser): an entity link highlighted on the details tab
// or a neighbor highlighted on the related tab wins over the page's entity.
func (v *detailView) Selection() (map[string]any, *catalog.Entity) {
	switch t := v.tabs[v.active].view.(type) {
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

// YankText forwards the active tab's field-level yank (details tab).
func (v *detailView) YankText() (string, string, bool) {
	if yp, ok := v.tabs[v.active].view.(yankProvider); ok {
		return yp.YankText()
	}
	return "", "", false
}

// DQL reveals the active tab's query (ctrl+q).
func (v *detailView) DQL() string {
	if p, ok := v.tabs[v.active].view.(dqlProvider); ok {
		return p.DQL()
	}
	return ""
}

func (v *detailView) Init() tea.Cmd {
	v.tabs[0].started = true
	return v.tabs[0].view.Init()
}

func (v *detailView) Refresh() tea.Cmd { return v.tabs[v.active].view.Refresh() }

func (v *detailView) SetTimeframe(tf catalog.Timeframe) tea.Cmd {
	var cmds []tea.Cmd
	for i := range v.tabs {
		cmd := v.tabs[i].view.SetTimeframe(tf)
		// Unstarted tabs only record the new scope; their query runs on
		// first activation anyway.
		if v.tabs[i].started && cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

func (v *detailView) InputActive() bool { return v.tabs[v.active].view.InputActive() }

// Busy delegates to the active tab (animates the spinner while it loads).
func (v *detailView) Busy() bool {
	if br, ok := v.tabs[v.active].view.(busyReporter); ok {
		return br.Busy()
	}
	return false
}
func (v *detailView) Crumb() string { return entityName(v.entity) }
func (v *detailView) Echo() string  { return v.tabs[v.active].view.Echo() }

func (v *detailView) Hints() []keyHint {
	// While the active tab shows its own lens strip, digits belong to it —
	// the page tabs stay reachable via tab/shift+tab.
	label := fmt.Sprintf("tab/1-%d", len(v.tabs))
	if v.lensedInner() != nil {
		label = "tab"
	}
	hints := []keyHint{{label, "tabs"}}
	return append(hints, v.tabs[v.active].view.Hints()...)
}

func (v *detailView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bodySizeMsg:
		v.width, v.height = msg.width, msg.height
		var cmds []tea.Cmd
		for i := range v.tabs {
			if cmd := v.tabs[i].view.Update(v.childSize()); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return tea.Batch(cmds...)

	case dataMsg:
		// Forward to every tab; each view drops results it doesn't own.
		var cmds []tea.Cmd
		for i := range v.tabs {
			if cmd := v.tabs[i].view.Update(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		// An id-only jump learns the entity's name from the details-tab
		// fetch; unstarted signal tabs pick it up via the shared scope
		// pointer when they compose their queries.
		if v.entity.Name == "" {
			if iv, ok := v.tabs[0].view.(*inspectorView); ok && iv.entity != nil {
				v.entity.Name = iv.entity.Name
			}
		}
		return tea.Batch(cmds...)

	case tea.KeyMsg:
		if !v.tabs[v.active].view.InputActive() {
			if cmd, handled := v.tabKey(msg.String()); handled {
				return cmd
			}
		}
		return v.tabs[v.active].view.Update(msg)
	}
	return v.tabs[v.active].view.Update(msg)
}

// lensedInner returns the active tab's table when it carries a lens strip —
// nested "tabs" that digits and brackets should drive while it is visible.
func (v *detailView) lensedInner() *tableView {
	if tv, ok := v.tabs[v.active].view.(*tableView); ok && len(tv.spec.Lenses) > 0 {
		return tv
	}
	return nil
}

// claimsDigit reports whether the page consumes a digit key (the app's
// global hotkeys must stand back): the active tab's lens strip when it has
// one, else the page tabs.
func (v *detailView) claimsDigit(d byte) bool {
	if d < '1' {
		return false
	}
	if inner := v.lensedInner(); inner != nil {
		return d < byte('1'+len(inner.spec.Lenses))
	}
	return d < byte('1'+len(v.tabs))
}

// tabKey handles tab-switching keys; drill keys and everything else fall
// through to the active tab's view. When the active tab shows its own lens
// strip (traces, sessions), the digits and brackets drive THAT strip — it is
// the numbered thing on screen — and tab/shift+tab keep cycling the page
// tabs (the tab bar drops its digit labels then, see View).
func (v *detailView) tabKey(key string) (tea.Cmd, bool) {
	inner := v.lensedInner()
	switch key {
	case "tab":
		return v.setActive((v.active + 1) % len(v.tabs)), true
	case "shift+tab":
		return v.setActive((v.active + len(v.tabs) - 1) % len(v.tabs)), true
	case "]":
		if inner != nil {
			return nil, false // the inner view cycles its lens
		}
		return v.setActive((v.active + 1) % len(v.tabs)), true
	case "[":
		if inner != nil {
			return nil, false
		}
		return v.setActive((v.active + len(v.tabs) - 1) % len(v.tabs)), true
	}
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		if inner != nil {
			return nil, false // digits pick a lens on the visible strip
		}
		if key[0] < byte('1'+len(v.tabs)) {
			return v.setActive(int(key[0] - '1')), true
		}
	}
	return nil, false
}

func (v *detailView) setActive(i int) tea.Cmd {
	if i == v.active {
		return nil
	}
	v.active = i
	tab := &v.tabs[i]
	cmds := []tea.Cmd{tab.view.Update(v.childSize())}
	if !tab.started {
		tab.started = true
		cmds = append(cmds, tab.view.Init())
	}
	return tea.Batch(cmds...)
}

func (v *detailView) childSize() bodySizeMsg {
	// Three chrome lines: identity header, tab bar, separator rule.
	return bodySizeMsg{width: v.width, height: max(v.height-3, 1)}
}

func (v *detailView) View(width, height int) string {
	v.width, v.height = width, height
	identity := " " + theme.OverlayTitle.Render(entityName(v.entity)) +
		"  " + theme.Badge.Render(v.entity.Type) +
		theme.Dim.Render("  "+v.entity.ID)
	// When the active tab renders its own numbered lens strip, the page tab
	// bar drops its digit labels — two competing number rows would lie about
	// what the digits do.
	digits := v.lensedInner() == nil
	labels := make([]string, len(v.tabs))
	for i, t := range v.tabs {
		label := t.name
		if digits {
			label = fmt.Sprintf("%d · %s", i+1, t.name)
		}
		if i == v.active {
			labels[i] = theme.TabActive.Render(label)
		} else {
			labels[i] = theme.TabInactive.Render(label)
		}
	}
	bar := " " + strings.Join(labels, " ")
	rule := theme.Rule.Render(strings.Repeat("─", max(width, 0)))
	return ansi.Truncate(identity, width, "…") + "\n" +
		ansi.Truncate(bar, width, "…") + "\n" + rule + "\n" +
		v.tabs[v.active].view.View(width, max(height-3, 1))
}
