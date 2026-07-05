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
	scope := catalog.Scope{Entity: &entity, Timeframe: tf}
	tabs := []detailTab{{name: "details", view: newEntityInfoView(ds, entity, rec)}}
	if catalog.MetricsFor(entity.Type) != nil {
		tabs = append(tabs, detailTab{name: "metrics", view: newMetricsView(ds, entity, tf)})
	}
	for _, name := range []string{"logs", "events", "problems"} {
		if spec := catalog.Lookup(name); spec != nil {
			tabs = append(tabs, detailTab{name: name, view: newTableView(ds, spec, scope)})
		}
	}
	return &detailView{entity: entity, tabs: tabs}
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
func (v *detailView) Crumb() string     { return entityName(v.entity) }
func (v *detailView) Echo() string      { return v.tabs[v.active].view.Echo() }

func (v *detailView) Hints() []keyHint {
	hints := []keyHint{{fmt.Sprintf("tab/1-%d", len(v.tabs)), "tabs"}}
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

// tabKey handles tab-switching keys; drill keys and everything else fall
// through to the active tab's view.
func (v *detailView) tabKey(key string) (tea.Cmd, bool) {
	switch key {
	case "tab", "]":
		return v.setActive((v.active + 1) % len(v.tabs)), true
	case "shift+tab", "[":
		return v.setActive((v.active + len(v.tabs) - 1) % len(v.tabs)), true
	}
	if len(key) == 1 && key[0] >= '1' && key[0] < byte('1'+len(v.tabs)) {
		return v.setActive(int(key[0] - '1')), true
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
	return bodySizeMsg{width: v.width, height: max(v.height-1, 1)}
}

func (v *detailView) View(width, height int) string {
	v.width, v.height = width, height
	labels := make([]string, len(v.tabs))
	for i, t := range v.tabs {
		label := fmt.Sprintf(" %d %s ", i+1, t.name)
		if i == v.active {
			labels[i] = theme.Selected.Render(label)
		} else {
			labels[i] = theme.Dim.Render(label)
		}
	}
	bar := ansi.Truncate(strings.Join(labels, " "), width, "…")
	return bar + "\n" + v.tabs[v.active].view.View(width, max(height-1, 1))
}
