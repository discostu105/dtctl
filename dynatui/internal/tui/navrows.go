// Row building: the flattened cursor rows for the current mode — walk
// groups, fold state, direction/mesh filters — plus the preview debounce.
package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
)

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

// move keeps the navigator's movement-with-preview call sites one-argument;
// the clamp math lives on the shared scroller.
func (v *navView) move(delta int) {
	v.scroller.move(delta, len(v.rows), maxInt(v.listHeight(), 1))
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
	return tick(250*time.Millisecond, func(time.Time) tea.Msg { return navPreviewTickMsg{gen: gen} })
}

func (v *navView) rightPaneVisible() bool {
	return v.ds.previewOn() && v.width >= 100
}
