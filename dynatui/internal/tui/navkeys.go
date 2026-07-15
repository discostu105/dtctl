// The navigator's key handling: activation (enter walks, browses, or
// re-roots), folding, backtracking, and the drill vocabulary.
package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
)

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

	// Space pages like every other list; z folds (space-as-fold made the
	// navigator the one view where paging collapsed things instead). Every
	// move re-arms the debounced preview fetch.
	if v.scroller.handleKey(msg.String(), len(v.rows), maxInt(v.listHeight(), 1)) {
		return v.schedulePreview()
	}

	switch msg.String() {
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
	case "z":
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
