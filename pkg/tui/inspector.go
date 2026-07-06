package tui

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
	"github.com/dynatrace-oss/dtctl/pkg/tui/theme"
)

// inspectorView shows a full record grouped by field namespace — records
// routinely carry 50+ dotted fields, so grouping beats flat YAML
// (TUI_DESIGN.md, "Log record inspector"). The most relevant fields render
// first as a highlighted block, and '/' narrows the property list by key or
// value substring.
//
// Values render typed (render.go): timestamps with age, durations humanized,
// JSON as highlighted blocks, and entity ids as traversable links — j/k moves
// a cursor over fields, enter follows the selected link (entity → its detail
// page, trace id → the waterfall) or expands a collapsed long value.
//
// With facts and a data source set (newEntityInfoView) it doubles as the
// details tab of the entity page: a curated key-facts panel on top of the
// full property list, refreshable via the Smartscape detail query.
type inspectorView struct {
	title string
	rec   map[string]any

	// Entity mode (details tab).
	facts  []catalog.Fact
	ds     *dataSource
	dql    string
	entity *catalog.Entity

	seq     int
	loading bool
	err     error

	searchInput textinput.Model
	searching   bool
	search      string

	rows     []fieldRow // selectable field rows, in content order
	cursor   int
	expanded map[string]bool // per-field expand state for long values

	lines []string // assembled content lines (selection applied at render)

	vp    viewport.Model
	ready bool
}

// fieldRow is one selectable property in the assembled content: its first
// line carries the label (and inline value); enter acts on its target.
type fieldRow struct {
	key   string // record key (search & expand identity)
	label string // display label — key, or key[i] for exploded id arrays
	val   valueView
	line  int // first line index in lines
	span  int
	// values too big for one line collapse to a truncated preview by
	// default; enter toggles the full block.
	expandable bool
	expanded   bool
}

// priorityFields render first as the "highlights" block, in this order,
// before the namespace groups.
var priorityFields = []string{
	"content", "event.name", "event.description", "display_id", "event.status",
	"event.category", "timestamp", "loglevel", "status", "host.name",
}

func newInspectorView(title string, rec map[string]any) *inspectorView {
	si := textinput.New()
	si.Prompt = "/"
	si.CharLimit = 64
	return &inspectorView{title: title, rec: rec, searchInput: si, expanded: map[string]bool{}}
}

// newEntityInfoView builds the details tab of an entity page. rec is the
// already-fetched list row (shown instantly); nil triggers a fetch on Init.
func newEntityInfoView(ds *dataSource, entity catalog.Entity, rec map[string]any) *inspectorView {
	v := newInspectorView(entityName(entity), rec)
	v.facts = catalog.KeyFacts(entity.Type)
	v.ds = ds
	v.dql = catalog.DetailQuery(entity)
	v.entity = &entity
	return v
}

// DQL reveals the detail query in entity mode (ctrl+q).
func (v *inspectorView) DQL() string { return v.dql }

// Selection exposes the record and the most specific entity under the
// cursor: a selected entity-id field wins over the page's own entity, so
// pin/relations/open act on the highlighted link.
func (v *inspectorView) Selection() (map[string]any, *catalog.Entity) {
	if row := v.selectedRow(); row != nil && row.val.entity != nil {
		e := *row.val.entity
		return v.rec, &e
	}
	return v.rec, v.entity
}

// YankText supplies the selected field's raw value to the global 'y'.
func (v *inspectorView) YankText() (text, label string, ok bool) {
	row := v.selectedRow()
	if row == nil || row.val.raw == "" {
		return "", "", false
	}
	return row.val.raw, row.label, true
}

func (v *inspectorView) selectedRow() *fieldRow {
	if v.cursor < 0 || v.cursor >= len(v.rows) {
		return nil
	}
	return &v.rows[v.cursor]
}

func (v *inspectorView) Init() tea.Cmd {
	// Entity mode always fetches the full Smartscape node: the list row
	// renders instantly, but summarized rows (pods, workloads) carry only
	// their table fields — the fetch upgrades them in place.
	if v.rec == nil || v.ds != nil {
		return v.Refresh()
	}
	return nil
}

func (v *inspectorView) Refresh() tea.Cmd {
	if v.ds == nil || v.dql == "" {
		return nil
	}
	v.seq++
	v.loading = true
	v.err = nil
	return v.ds.query(v, v.seq, v.dql)
}

func (v *inspectorView) SetTimeframe(tf catalog.Timeframe) tea.Cmd { return nil }
func (v *inspectorView) InputActive() bool                         { return v.searching }
func (v *inspectorView) Crumb() string                             { return v.title }

// Busy reports whether the detail fetch is in flight.
func (v *inspectorView) Busy() bool { return v.loading }

func (v *inspectorView) Echo() string {
	if v.dql == "" {
		return ""
	}
	oneline := strings.Join(strings.Fields(strings.ReplaceAll(v.dql, "\n", " ")), " ")
	return fmt.Sprintf("dtctl query '%s'", oneline)
}

func (v *inspectorView) Hints() []keyHint {
	if v.searching {
		return []keyHint{{"type", "search fields"}, {"enter", "apply"}, {"esc", "clear"}}
	}
	hints := []keyHint{{"j/k", "fields"}}
	if row := v.selectedRow(); row != nil {
		switch {
		case row.val.entity != nil:
			hints = append(hints, keyHint{"enter", "open " + strings.ToLower(row.val.entity.Type)})
		case row.val.trace != "":
			hints = append(hints, keyHint{"enter", "open trace"})
		case row.expandable && !row.expanded:
			hints = append(hints, keyHint{"enter", "expand"})
		case row.expandable:
			hints = append(hints, keyHint{"enter", "collapse"})
		}
	}
	return append(hints, keyHint{"y", "yank value"}, keyHint{"/", "search"})
}

func (v *inspectorView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bodySizeMsg:
		v.resize(msg.width, msg.height)
		return nil

	case dataMsg:
		if msg.owner != any(v) || msg.seq != v.seq {
			return nil
		}
		v.loading = false
		if msg.err != nil {
			if v.rec != nil {
				// Keep showing the record we have; surface the failure.
				return statusErr("refresh failed: " + msg.err.Error())
			}
			v.err = msg.err
			return nil
		}
		if len(msg.records) > 0 {
			v.rec = msg.records[0]
			// An id-only jump learns the entity's name from the fetch.
			if v.entity != nil && v.entity.Name == "" {
				if name := catalog.Str(v.rec, "name"); name != "" {
					v.entity.Name = name
					v.title = name
				}
			}
		} else if v.rec == nil {
			v.err = errors.New("entity not found (no longer known to Smartscape?)")
		}
		v.rebuild()
		return nil

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

func (v *inspectorView) handleKey(msg tea.KeyMsg) tea.Cmd {
	if v.searching {
		switch msg.String() {
		case "enter":
			v.searching = false
			v.searchInput.Blur()
		case "esc":
			v.clearSearch()
		default:
			var cmd tea.Cmd
			v.searchInput, cmd = v.searchInput.Update(msg)
			if v.searchInput.Value() != v.search {
				v.search = v.searchInput.Value()
				v.rebuild()
				v.vp.GotoTop()
			}
			return cmd
		}
		return nil
	}

	switch msg.String() {
	case "/":
		v.searching = true
		v.searchInput.Focus()
		return textinput.Blink
	case "esc":
		if v.search != "" {
			v.clearSearch()
			return claimKey
		}
		return nil // app pops the stack
	case "j", "down":
		v.moveCursor(1)
		return nil
	case "k", "up":
		v.moveCursor(-1)
		return nil
	case "g", "home":
		v.cursor = 0
		v.refreshVP()
		v.vp.GotoTop()
		return nil
	case "G", "end":
		v.cursor = max(len(v.rows)-1, 0)
		v.refreshVP()
		v.vp.GotoBottom()
		return nil
	case "enter":
		return v.enterRow()
	}
	var cmd tea.Cmd
	v.vp, cmd = v.vp.Update(msg)
	return cmd
}

// enterRow acts on the selected field: follow an entity/trace link, or
// toggle a collapsed long value.
func (v *inspectorView) enterRow() tea.Cmd {
	row := v.selectedRow()
	if row == nil {
		return nil
	}
	switch {
	case row.val.entity != nil:
		entity := *row.val.entity
		return func() tea.Msg { return detailMsg{entity: entity} }
	case row.val.trace != "":
		trace := row.val.trace
		return func() tea.Msg { return waterfallMsg{traceID: trace} }
	case row.expandable:
		v.expanded[row.label] = !row.expanded
		v.rebuild()
		v.ensureVisible()
		return claimKey
	}
	return nil
}

func (v *inspectorView) moveCursor(delta int) {
	if len(v.rows) == 0 {
		return
	}
	v.cursor += delta
	if v.cursor < 0 {
		v.cursor = 0
	}
	if v.cursor >= len(v.rows) {
		v.cursor = len(v.rows) - 1
	}
	v.refreshVP()
	v.ensureVisible()
}

// ensureVisible scrolls the viewport so the selected row is on screen.
func (v *inspectorView) ensureVisible() {
	row := v.selectedRow()
	if row == nil || !v.ready {
		return
	}
	first, last := row.line, row.line+row.span-1
	switch {
	case first < v.vp.YOffset:
		v.vp.SetYOffset(first)
	case last >= v.vp.YOffset+v.vp.Height:
		off := last - v.vp.Height + 1
		if off > first {
			off = first
		}
		v.vp.SetYOffset(off)
	}
}

func (v *inspectorView) clearSearch() {
	v.searching = false
	v.searchInput.Blur()
	v.searchInput.SetValue("")
	v.search = ""
	v.rebuild()
	v.vp.GotoTop()
}

func (v *inspectorView) searchActive() bool { return v.searching || v.search != "" }

func (v *inspectorView) resize(width, height int) {
	bodyH := height
	if v.searchActive() {
		bodyH--
	}
	if bodyH < 1 {
		bodyH = 1
	}
	if !v.ready {
		v.vp = viewport.New(width, bodyH)
		v.ready = true
		v.rebuild()
		return
	}
	widthChanged := v.vp.Width != width
	v.vp.Width, v.vp.Height = width, bodyH
	if widthChanged {
		v.rebuild()
	}
}

// refreshVP re-renders the assembled content into the viewport (cheap: the
// lines are prebuilt; only the selected row's wash is applied here).
func (v *inspectorView) refreshVP() {
	if !v.ready {
		return
	}
	v.vp.SetContent(v.assemble())
}

// assemble joins the content lines, washing the selected row's label line
// with the selection bar (per-value colors drop on that line — one calm bar,
// same pattern as the tables).
func (v *inspectorView) assemble() string {
	row := v.selectedRow()
	if row == nil {
		return strings.Join(v.lines, "\n")
	}
	var b strings.Builder
	for i, line := range v.lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i == row.line {
			b.WriteString(theme.Gutter.Render("▌") + theme.Selected.Render(pad(ansi.Strip(line), v.vp.Width-1)))
		} else {
			b.WriteString(line)
		}
	}
	return b.String()
}

func (v *inspectorView) View(width, height int) string {
	v.resize(width, height)
	if v.rec == nil {
		switch {
		case v.loading:
			return " " + theme.Spinner.Render(theme.Spin()+" loading…")
		case v.err != nil:
			return theme.Error.Render("✗ " + wrap(v.err.Error(), width-2))
		default:
			return theme.Dim.Render("no record")
		}
	}
	out := v.vp.View()
	if v.searchActive() {
		out = " " + v.searchInput.View() + "\n" + out
	}
	return out
}

// rebuild renders the record into content lines and selectable rows: the
// curated facts panel (entity mode), the priority-field highlights, then
// namespace groups (k8s.*, event.*, …) sorted by name. A search needle
// narrows fields by key or value.
func (v *inspectorView) rebuild() {
	if !v.ready || v.rec == nil {
		return
	}
	v.lines = nil
	v.rows = nil

	if len(v.facts) > 0 {
		for _, f := range v.facts {
			if text := f.Value(v.rec); text != "" {
				v.addLine(" " + theme.FactLabel.Render(fmt.Sprintf("%-14s", f.Label)) + "  " + text)
			}
		}
		v.addLine("")
		v.addLine(theme.Section("properties"))
	}

	needle := strings.ToLower(strings.TrimSpace(v.search))
	rendered := map[string]bool{}

	var prio []string
	for _, key := range priorityFields {
		val, ok := v.rec[key]
		if !ok {
			continue
		}
		rendered[key] = true
		if fieldMatches(needle, key, val) {
			prio = append(prio, key)
		}
	}
	if len(prio) > 0 {
		if len(v.facts) == 0 {
			v.addLine(theme.Section("highlights"))
		}
		for _, key := range prio {
			v.addField(key, needle, theme.FactLabel)
		}
	}

	groups := map[string][]string{}
	for key, val := range v.rec {
		if rendered[key] || !fieldMatches(needle, key, val) {
			continue
		}
		group := ""
		if i := strings.Index(key, "."); i > 0 {
			group = key[:i]
		}
		groups[group] = append(groups[group], key)
	}

	names := make([]string, 0, len(groups))
	for g := range groups {
		names = append(names, g)
	}
	sort.Strings(names)
	// Top-level (ungrouped) fields come right after the highlights block.
	sort.SliceStable(names, func(i, j int) bool { return names[i] == "" && names[j] != "" })

	for _, g := range names {
		keys := groups[g]
		sort.Strings(keys)
		if g != "" {
			v.addLine("")
			v.addLine(theme.Section(g))
		} else if len(v.lines) > 0 && !strings.HasPrefix(ansi.Strip(v.lines[len(v.lines)-1]), "▍") {
			// Separate ungrouped fields from the highlights — unless a
			// section header directly precedes them (double gap otherwise).
			v.addLine("")
		}
		for _, key := range keys {
			v.addField(key, needle, theme.Label)
		}
	}

	if needle != "" && len(v.rows) == 0 {
		v.addLine(theme.Dim.Render("  no matching properties"))
	}

	if v.cursor >= len(v.rows) {
		v.cursor = max(len(v.rows)-1, 0)
	}
	v.refreshVP()
}

func (v *inspectorView) addLine(line string) { v.lines = append(v.lines, line) }

// addField renders one record field as selectable row(s). Arrays of entity
// ids explode into one navigable row per id; everything else is one row.
func (v *inspectorView) addField(key, needle string, style lipgloss.Style) {
	if ids := entityIDList(v.rec[key]); ids != nil {
		for i, id := range ids {
			label := fmt.Sprintf("%s[%d]", key, i)
			v.addRow(key, label, v.labelStyle(needle, key, style), renderString(key, id, v.vp.Width-6))
		}
		return
	}
	val := renderValue(key, v.rec[key], v.vp.Width-6)
	// The record's own id is not a jump target — style it opaque instead.
	if val.entity != nil && v.entity != nil && val.entity.ID == v.entity.ID {
		val.entity = nil
		val.lines = []string{theme.UID.Render(val.raw)}
	}
	v.addRow(key, key, v.labelStyle(needle, key, style), val)
}

// addRow lays out one field. Scalars and arrays are one line by default — a
// value too big for its line collapses to a truncated preview marked with ▸
// (density first: a record is scannable without paging, detail is one
// keypress away). JSON objects read as structure, so they default to the
// full indented block; enter toggles either way.
func (v *inspectorView) addRow(key, label string, style lipgloss.Style, val valueView) {
	row := fieldRow{key: key, label: label, val: val, line: len(v.lines)}
	labelText := " " + style.Render(fmt.Sprintf("%-32s", label))
	avail := v.vp.Width - lipgloss.Width(labelText) - 2

	oneLine := len(val.lines) == 1 && lipgloss.Width(val.lines[0]) <= avail
	row.expanded = val.block
	if exp, overridden := v.expanded[label]; overridden {
		row.expanded = exp
	}
	row.expanded = row.expanded && !oneLine
	row.expandable = !oneLine

	switch {
	case oneLine:
		v.addLine(labelText + "  " + val.lines[0])
	case !row.expanded:
		preview := val.compact
		if preview == "" {
			preview = val.lines[0]
		}
		marker := theme.Dim.Render(" ▸")
		preview = ansi.Truncate(preview, max(avail-2, 3), "…")
		v.addLine(labelText + "  " + preview + marker)
	default:
		lines := val.lines
		if len(lines) == 1 {
			lines = wrapLines(lines[0], v.vp.Width-6)
		}
		v.addLine(labelText + "  " + theme.Dim.Render("▾"))
		for _, l := range lines {
			v.addLine("    " + l)
		}
	}
	row.span = len(v.lines) - row.line
	v.rows = append(v.rows, row)
}

// labelStyle highlights keys that themselves match the search needle (a field
// can also be shown because its value matched).
func (v *inspectorView) labelStyle(needle, key string, base lipgloss.Style) lipgloss.Style {
	if needle != "" && strings.Contains(strings.ToLower(key), needle) {
		return theme.Hit
	}
	return base
}

func fieldMatches(needle, key string, val any) bool {
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(key), needle) ||
		strings.Contains(strings.ToLower(catalog.FormatValue(val)), needle)
}

func indent(s string, n int) string {
	pad := strings.Repeat(" ", n)
	return pad + strings.ReplaceAll(s, "\n", "\n"+pad)
}
