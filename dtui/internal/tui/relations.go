package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
	"github.com/dynatrace-oss/dtui/internal/tui/theme"
)

// relationsView is the topology hop behind 'x': every Smartscape edge of an
// entity, both directions, with names resolved in a second batched query.
// enter navigates to the neighbor's detail page; x again keeps walking.
// It also serves as the detail page's "related" tab (embedded), where a
// host's processes, containers, and K8s node are one tab away instead of
// one keypress.
type relationsView struct {
	ds     *dataSource
	entity catalog.Entity
	tf     catalog.Timeframe

	rows   []catalog.Edge
	names  map[string]string // id → display name (second query)
	cursor int
	offset int

	embedded bool // detail-page tab: the page header already names the entity

	loading bool
	err     error
	seq     int
	dql     string

	width, height int
}

func newRelationsView(ds *dataSource, entity catalog.Entity, tf catalog.Timeframe) *relationsView {
	return &relationsView{ds: ds, entity: entity, tf: tf, names: map[string]string{}}
}

// newEmbeddedRelationsView builds the detail page's "related" tab.
func newEmbeddedRelationsView(ds *dataSource, entity catalog.Entity, tf catalog.Timeframe) *relationsView {
	v := newRelationsView(ds, entity, tf)
	v.embedded = true
	return v
}

// nameOwner tags the second (name-resolution) query.
type nameOwner struct{ v *relationsView }

func (v *relationsView) Init() tea.Cmd { return v.Refresh() }

func (v *relationsView) Refresh() tea.Cmd {
	v.seq++
	v.loading = true
	v.err = nil
	v.dql = catalog.EdgesQuery(v.entity.ID)
	return v.ds.query(v, v.seq, v.dql)
}

func (v *relationsView) SetTimeframe(catalog.Timeframe) tea.Cmd { return nil }
func (v *relationsView) InputActive() bool                      { return false }

// Busy reports whether the edge query is in flight (animates the spinner).
func (v *relationsView) Busy() bool { return v.loading }

// RowCount reports the loaded edge count for the detail page's tab badge.
func (v *relationsView) RowCount() (int, bool) {
	return len(v.rows), v.seq > 0 && !v.loading && v.err == nil
}
func (v *relationsView) Crumb() string { return "relations (" + entityName(v.entity) + ")" }
func (v *relationsView) DQL() string   { return v.dql }

func (v *relationsView) Echo() string {
	if v.dql == "" {
		return ""
	}
	return fmt.Sprintf("dtctl query '%s'", strings.Join(strings.Fields(strings.ReplaceAll(v.dql, "\n", " ")), " "))
}

func (v *relationsView) Hints() []keyHint {
	return []keyHint{{"enter", "go to entity"}, {"x", "its relations"}, {".", "pin"}, {"y", "yank id"}}
}

// Selection exposes the highlighted neighbor for app-level actions.
func (v *relationsView) Selection() (map[string]any, *catalog.Entity) {
	if v.cursor < 0 || v.cursor >= len(v.rows) {
		return nil, nil
	}
	row := v.rows[v.cursor]
	return nil, &catalog.Entity{ID: row.OtherID, Name: v.names[row.OtherID], Type: row.OtherType}
}

func (v *relationsView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bodySizeMsg:
		v.width, v.height = msg.width, msg.height
		return nil

	case dataMsg:
		if no, ok := msg.owner.(nameOwner); ok && no.v == v {
			if msg.seq == v.seq && msg.err == nil {
				for _, rec := range msg.records {
					if id := catalog.Str(rec, "id"); id != "" {
						v.names[id] = catalog.Str(rec, "name")
					}
				}
			}
			return nil // resolution failures fall back to raw ids
		}
		if msg.owner != any(v) || msg.seq != v.seq {
			return nil
		}
		v.loading = false
		v.err = msg.err
		if msg.err != nil {
			return nil
		}
		v.rows = catalog.BuildEdges(v.entity.ID, msg.records)
		if v.cursor >= len(v.rows) {
			v.cursor, v.offset = 0, 0
		}
		return v.resolveNames()

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

// resolveNames issues the batched name lookup for every neighbor id.
func (v *relationsView) resolveNames() tea.Cmd {
	seen := map[string]bool{}
	var ids []string
	for _, row := range v.rows {
		if !seen[row.OtherID] {
			seen[row.OtherID] = true
			ids = append(ids, row.OtherID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return v.ds.query(nameOwner{v}, v.seq, catalog.NamesQuery(ids))
}

func (v *relationsView) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		v.move(-1)
	case "down", "j":
		v.move(1)
	case "home", "g":
		v.cursor, v.offset = 0, 0
	case "end", "G":
		v.move(len(v.rows))
	case "enter":
		if _, e := v.Selection(); e != nil {
			entity := *e
			return func() tea.Msg { return detailMsg{entity: entity} }
		}
	}
	return nil
}

func (v *relationsView) move(delta int) {
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
	if vis := max(v.height-2, 1); v.cursor >= v.offset+vis {
		v.offset = v.cursor - vis + 1
	}
}

// relationLabel renders the edge together with the neighbor's type so each
// row reads as a sentence around the arrow: "calls → HOST" (we do it to the
// neighbor) vs "PROCESS ← runs on" (the neighbor does it to us). The styled
// form colors the verb by direction and dims the type; the plain form feeds
// width math and the selected row, whose row style paints the whole line.
func relationLabel(r catalog.Edge, styled bool) string {
	verb := strings.ReplaceAll(r.Verb, "_", " ")
	if !styled {
		if r.Outgoing {
			return verb + " → " + r.OtherType
		}
		return r.OtherType + " ← " + verb
	}
	if r.Outgoing {
		return theme.ArrowOut.Render(verb+" →") + " " + theme.Dim.Render(r.OtherType)
	}
	return theme.Dim.Render(r.OtherType) + " " + theme.ArrowIn.Render("← "+verb)
}

func (v *relationsView) View(width, height int) string {
	v.width, v.height = width, height
	var b strings.Builder

	title := ""
	if !v.embedded {
		title = " " + theme.OverlayTitle.Render(entityName(v.entity)) + "  " + theme.Badge.Render(v.entity.Type) + " "
	}
	if !v.loading && v.err == nil {
		title += theme.Dim.Render(fmt.Sprintf(" %d relations", len(v.rows)))
	}
	b.WriteString(ansi.Truncate(title, width, "…") + "\n")

	switch {
	case v.loading:
		b.WriteString(" " + theme.Spinner.Render(theme.Spin()+" walking topology…"))
		return b.String()
	case v.err != nil:
		b.WriteString(theme.Error.Render("✗ " + wrap(v.err.Error(), width-2)))
		return b.String()
	case len(v.rows) == 0:
		b.WriteString("\n" + lipgloss.PlaceHorizontal(width, lipgloss.Center,
			theme.Dim.Render("∅ no Smartscape edges for this entity")))
		return b.String()
	}

	relW := 18
	for _, row := range v.rows {
		if w := lipgloss.Width(relationLabel(row, false)); w > relW {
			relW = w
		}
	}
	nameW := width - relW - 3
	if nameW < 16 {
		nameW = 16
	}

	end := v.offset + max(height-2, 1)
	if end > len(v.rows) {
		end = len(v.rows)
	}
	for i := v.offset; i < end; i++ {
		row := v.rows[i]
		name := v.names[row.OtherID]
		if name == "" {
			name = row.OtherID
		}
		if i == v.cursor {
			line := pad(relationLabel(row, false), relW) + " " + pad(name, nameW)
			b.WriteString(theme.Gutter.Render("▌") + theme.Selected.Render(pad(line, width-1)))
		} else {
			line := " " + pad(relationLabel(row, true), relW) + " " + pad(name, nameW)
			b.WriteString(ansi.Truncate(line, width, "…"))
		}
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
