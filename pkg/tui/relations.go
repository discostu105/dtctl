package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
	"github.com/dynatrace-oss/dtctl/pkg/tui/theme"
)

// relationsView is the topology hop behind 'x': every Smartscape edge of an
// entity, both directions, with names resolved in a second batched query.
// enter navigates to the neighbor's detail page; x again keeps walking.
type relationsView struct {
	ds     *dataSource
	entity catalog.Entity
	tf     catalog.Timeframe

	rows   []relRow
	names  map[string]string // id → display name (second query)
	cursor int
	offset int

	loading bool
	err     error
	seq     int
	dql     string

	width, height int
}

type relRow struct {
	outgoing  bool
	edgeType  string
	otherID   string
	otherType string
}

func newRelationsView(ds *dataSource, entity catalog.Entity, tf catalog.Timeframe) *relationsView {
	return &relationsView{ds: ds, entity: entity, tf: tf, names: map[string]string{}}
}

// nameOwner tags the second (name-resolution) query.
type nameOwner struct{ v *relationsView }

// edgesQuery fetches both edge directions in one query (validated live —
// source-only misses incoming routes_to / is_part_of edges).
func edgesQuery(id string) string {
	return fmt.Sprintf(`smartscapeEdges "*"
| filter source_id == toSmartscapeId(%[1]q) or target_id == toSmartscapeId(%[1]q)
| fields source_id, source_type, type, target_id, target_type
| limit 200`, id)
}

func namesQuery(ids []string) string {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = fmt.Sprintf("toSmartscapeId(%q)", id)
	}
	return fmt.Sprintf("smartscapeNodes \"*\"\n| filter in(id, {%s})\n| fields id, name, type\n| limit 200",
		strings.Join(quoted, ", "))
}

func (v *relationsView) Init() tea.Cmd { return v.Refresh() }

func (v *relationsView) Refresh() tea.Cmd {
	v.seq++
	v.loading = true
	v.err = nil
	v.dql = edgesQuery(v.entity.ID)
	return v.ds.query(v, v.seq, v.dql)
}

func (v *relationsView) SetTimeframe(catalog.Timeframe) tea.Cmd { return nil }
func (v *relationsView) InputActive() bool                      { return false }
func (v *relationsView) Crumb() string                          { return "relations (" + entityName(v.entity) + ")" }
func (v *relationsView) DQL() string                            { return v.dql }

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
	return nil, &catalog.Entity{ID: row.otherID, Name: v.names[row.otherID], Type: row.otherType}
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
		v.rows = buildRelations(v.entity.ID, msg.records)
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
		if !seen[row.otherID] {
			seen[row.otherID] = true
			ids = append(ids, row.otherID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return v.ds.query(nameOwner{v}, v.seq, namesQuery(ids))
}

// buildRelations turns edge records into direction-aware rows, outgoing
// first, grouped by edge type.
func buildRelations(selfID string, records []map[string]any) []relRow {
	var rows []relRow
	for _, rec := range records {
		src, dst := catalog.Str(rec, "source_id"), catalog.Str(rec, "target_id")
		row := relRow{edgeType: catalog.Str(rec, "type")}
		if src == selfID {
			row.outgoing = true
			row.otherID, row.otherType = dst, catalog.Str(rec, "target_type")
		} else {
			row.otherID, row.otherType = src, catalog.Str(rec, "source_type")
		}
		if row.otherID == "" || row.otherID == selfID {
			continue
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].outgoing != rows[j].outgoing {
			return rows[i].outgoing
		}
		if rows[i].edgeType != rows[j].edgeType {
			return rows[i].edgeType < rows[j].edgeType
		}
		return rows[i].otherType < rows[j].otherType
	})
	return rows
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

// relationVerb renders the edge with its direction read naturally:
// "runs on →" vs "← runs on" (the neighbor does it to us).
func relationVerb(r relRow) string {
	verb := strings.ReplaceAll(r.edgeType, "_", " ")
	if r.outgoing {
		return verb + " →"
	}
	return "← " + verb
}

func (v *relationsView) View(width, height int) string {
	v.width, v.height = width, height
	var b strings.Builder

	title := fmt.Sprintf("%s · %s", entityName(v.entity), v.entity.Type)
	b.WriteString(theme.GroupTitle.Render(title) + "\n")

	switch {
	case v.loading:
		b.WriteString(theme.Spinner.Render("⟳ walking topology…"))
		return b.String()
	case v.err != nil:
		b.WriteString(theme.Error.Render(wrap(v.err.Error(), width)))
		return b.String()
	case len(v.rows) == 0:
		b.WriteString(theme.Dim.Render("no Smartscape edges for this entity"))
		return b.String()
	}

	verbW, typeW := 18, 26
	nameW := width - verbW - typeW - 2
	if nameW < 16 {
		nameW = 16
	}

	end := v.offset + max(height-2, 1)
	if end > len(v.rows) {
		end = len(v.rows)
	}
	for i := v.offset; i < end; i++ {
		row := v.rows[i]
		name := v.names[row.otherID]
		nameStyled := name
		if name == "" {
			name = row.otherID
			nameStyled = name
		}
		line := pad(relationVerb(row), verbW) + " " + pad(nameStyled, nameW) + " " + pad(row.otherType, typeW)
		if i == v.cursor {
			line = theme.Selected.Render(pad(line, width))
		} else {
			line = pad(relationVerb(row), verbW) + " " + pad(name, nameW) + " " + theme.Dim.Render(pad(row.otherType, typeW))
		}
		b.WriteString(ansi.Truncate(line, width, "…"))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
