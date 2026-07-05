package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
	"github.com/dynatrace-oss/dtctl/pkg/tui/theme"
)

// tableView is the generic engine behind every catalog view: a DQL-backed
// table with incremental filtering and the universal drill-down vocabulary.
type tableView struct {
	ds    *dataSource
	spec  *catalog.Spec
	scope catalog.Scope

	all    []map[string]any // as fetched
	rows   []map[string]any // after /-filter
	cursor int
	offset int

	loading bool
	err     error
	seq     int
	dql     string
	elapsed string

	filterInput textinput.Model
	filtering   bool // input focused
	filter      string

	width, height int
}

func newTableView(ds *dataSource, spec *catalog.Spec, scope catalog.Scope) *tableView {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.CharLimit = 64
	return &tableView{ds: ds, spec: spec, scope: scope, filterInput: ti}
}

func (v *tableView) Init() tea.Cmd { return v.Refresh() }

func (v *tableView) Refresh() tea.Cmd {
	v.seq++
	v.loading = true
	v.err = nil
	v.dql = v.spec.Query(v.scope)
	return v.ds.query(v, v.seq, v.dql)
}

func (v *tableView) SetTimeframe(tf catalog.Timeframe) tea.Cmd {
	v.scope.Timeframe = tf
	return v.Refresh()
}

func (v *tableView) InputActive() bool { return v.filtering }

func (v *tableView) Crumb() string {
	label := v.spec.Name
	if v.scope.Entity != nil {
		name := v.scope.Entity.Name
		if name == "" {
			name = v.scope.Entity.ID
		}
		label += fmt.Sprintf(" (%s)", name)
	}
	return label
}

func (v *tableView) Echo() string {
	if v.dql == "" {
		return ""
	}
	oneline := strings.Join(strings.Fields(strings.ReplaceAll(v.dql, "\n", " ")), " ")
	return fmt.Sprintf("dtctl query '%s'", oneline)
}

func (v *tableView) Hints() []keyHint {
	var hints []keyHint
	if v.spec.Kind == catalog.KindEntity {
		hints = append(hints, keyHint{"enter", "details"}, keyHint{"d", "record"})
	} else {
		hints = append(hints, keyHint{"enter", "inspect"})
	}
	// Stable order for the drill keys.
	for _, k := range []string{"l", "s", "m", "p", "v"} {
		if target, ok := v.spec.Drills[k]; ok {
			hints = append(hints, keyHint{k, target})
		}
	}
	hints = append(hints, keyHint{"/", "filter"})
	return hints
}

func (v *tableView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bodySizeMsg:
		v.width, v.height = msg.width, msg.height
		return nil

	case dataMsg:
		if msg.owner != any(v) || msg.seq != v.seq {
			return nil
		}
		v.loading = false
		v.err = msg.err
		if msg.err == nil {
			v.all = msg.records
			v.elapsed = catalog.FormatDuration(msg.elapsed)
			v.applyFilter()
		}
		return nil

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

func (v *tableView) handleKey(msg tea.KeyMsg) tea.Cmd {
	if v.filtering {
		switch msg.String() {
		case "enter":
			v.filtering = false
			v.filterInput.Blur()
		case "esc":
			v.filtering = false
			v.filterInput.Blur()
			v.filterInput.SetValue("")
			v.filter = ""
			v.applyFilter()
		default:
			var cmd tea.Cmd
			v.filterInput, cmd = v.filterInput.Update(msg)
			v.filter = v.filterInput.Value()
			v.applyFilter()
			return cmd
		}
		return nil
	}

	switch msg.String() {
	case "up", "k":
		v.move(-1)
	case "down", "j":
		v.move(1)
	case "pgup", "ctrl+b":
		v.move(-v.pageSize())
	case "pgdown", "ctrl+f", " ":
		v.move(v.pageSize())
	case "home", "g":
		v.cursor = 0
		v.offset = 0
	case "end", "G":
		v.move(len(v.rows))
	case "/":
		v.filtering = true
		v.filterInput.Focus()
		return textinput.Blink
	case "esc":
		if v.filter != "" {
			v.filterInput.SetValue("")
			v.filter = ""
			v.applyFilter()
			return claimKey
		}
		return nil // app pops the stack
	case "enter":
		rec := v.selected()
		if rec == nil {
			return nil
		}
		// Entity rows open the tabbed detail page; signal rows (logs,
		// events, problems) open the record inspector.
		if v.spec.Kind == catalog.KindEntity {
			if e := v.entityOf(rec); e != nil {
				entity := *e
				return func() tea.Msg { return detailMsg{entity: entity, rec: rec} }
			}
		}
		return v.inspect(rec)
	case "d":
		if rec := v.selected(); rec != nil {
			return v.inspect(rec)
		}
	default:
		if target, ok := v.spec.Drills[msg.String()]; ok {
			return v.drill(target)
		}
	}
	return nil
}

// inspect opens the raw record inspector for a row.
func (v *tableView) inspect(rec map[string]any) tea.Cmd {
	title := v.spec.Name
	if e := v.entityOf(rec); e != nil && e.Name != "" {
		title = e.Name
	}
	return func() tea.Msg { return inspectMsg{title: title, rec: rec} }
}

// drill opens the target view scoped to the selected row's entity.
func (v *tableView) drill(target string) tea.Cmd {
	rec := v.selected()
	if rec == nil {
		return nil
	}
	entity := v.entityOf(rec)
	if entity == nil {
		return statusErr("selection carries no entity to scope by")
	}
	if target == "metrics" {
		if catalog.MetricsFor(entity.Type) == nil {
			return statusErr(fmt.Sprintf("no curated metrics for %s yet", entity.Type))
		}
		return func() tea.Msg { return metricsMsg{entity: *entity} }
	}
	spec := catalog.Lookup(target)
	if spec == nil {
		return statusErr(fmt.Sprintf("unknown view %q", target))
	}
	scope := catalog.Scope{Entity: entity, Timeframe: v.scope.Timeframe}
	return func() tea.Msg { return pushViewMsg{spec: spec, scope: scope} }
}

func (v *tableView) entityOf(rec map[string]any) *catalog.Entity {
	if v.spec.Entity == nil {
		return nil
	}
	return v.spec.Entity(rec)
}

func (v *tableView) selected() map[string]any {
	if v.cursor >= 0 && v.cursor < len(v.rows) {
		return v.rows[v.cursor]
	}
	return nil
}

func (v *tableView) move(delta int) {
	v.cursor += delta
	if v.cursor < 0 {
		v.cursor = 0
	}
	if v.cursor >= len(v.rows) {
		v.cursor = len(v.rows) - 1
	}
	if v.cursor < 0 {
		v.cursor = 0
	}
	v.clampOffset()
}

func (v *tableView) pageSize() int {
	if v.height > 3 {
		return v.height - 3
	}
	return 10
}

func (v *tableView) clampOffset() {
	visible := v.pageSize()
	if v.cursor < v.offset {
		v.offset = v.cursor
	}
	if v.cursor >= v.offset+visible {
		v.offset = v.cursor - visible + 1
	}
	if v.offset < 0 {
		v.offset = 0
	}
}

// applyFilter recomputes rows from the fetched page (client-side, consistent
// with the design: server-side narrowing is what scope is for).
func (v *tableView) applyFilter() {
	if v.filter == "" {
		v.rows = v.all
	} else {
		needle := strings.ToLower(v.filter)
		v.rows = nil
		for _, rec := range v.all {
			var cells []string
			for _, c := range v.spec.Columns {
				cells = append(cells, c.Text(rec))
			}
			if strings.Contains(strings.ToLower(strings.Join(cells, " ")), needle) {
				v.rows = append(v.rows, rec)
			}
		}
	}
	if v.cursor >= len(v.rows) {
		v.cursor = len(v.rows) - 1
	}
	if v.cursor < 0 {
		v.cursor = 0
	}
	v.clampOffset()
}

func (v *tableView) View(width, height int) string {
	v.width, v.height = width, height

	var b strings.Builder

	// Status/filter line.
	head := fmt.Sprintf("%d/%d rows", len(v.rows), len(v.all))
	if v.elapsed != "" {
		head += theme.Dim.Render(fmt.Sprintf("  (%s)", v.elapsed))
	}
	if v.filtering || v.filter != "" {
		head += "  " + v.filterInput.View()
	}
	if v.loading {
		head = theme.Spinner.Render("⟳ loading…") + "  " + head
	}
	b.WriteString(ansi.Truncate(head, width, "…"))
	b.WriteString("\n")

	if v.err != nil {
		b.WriteString("\n" + theme.Error.Render(wrap(v.err.Error(), width)))
		return b.String()
	}

	widths := v.columnWidths(width)

	// Header row.
	var hdr []string
	for i, c := range v.spec.Columns {
		hdr = append(hdr, pad(c.Title, widths[i]))
	}
	b.WriteString(theme.TableHeader.Render(ansi.Truncate(strings.Join(hdr, " "), width, "")))
	b.WriteString("\n")

	visible := height - 2
	if visible < 1 {
		visible = 1
	}
	end := v.offset + visible
	if end > len(v.rows) {
		end = len(v.rows)
	}
	for i := v.offset; i < end; i++ {
		b.WriteString(v.renderRow(i, widths, width))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	if len(v.rows) == 0 && !v.loading {
		b.WriteString(theme.Dim.Render("  no data in timeframe"))
	}
	return b.String()
}

func (v *tableView) renderRow(i int, widths []int, width int) string {
	rec := v.rows[i]
	selected := i == v.cursor

	var cells []string
	for ci, c := range v.spec.Columns {
		text := pad(c.Text(rec), widths[ci])
		if !selected && c.Class != nil {
			if class := c.Class(strings.TrimSpace(text)); class != "" {
				text = theme.Class(class, text)
			}
		}
		cells = append(cells, text)
	}
	row := ansi.Truncate(strings.Join(cells, " "), width, "…")
	if selected {
		// Selected rows drop per-cell colors so the highlight reads as one bar.
		row = theme.Selected.Render(pad(row, width))
	}
	return row
}

// columnWidths assigns fixed widths and shares the remainder among flex columns.
func (v *tableView) columnWidths(total int) []int {
	widths := make([]int, len(v.spec.Columns))
	fixed, flexCount := 0, 0
	for i, c := range v.spec.Columns {
		if c.Width > 0 {
			widths[i] = c.Width
			fixed += c.Width
		} else {
			flexCount++
		}
	}
	gaps := len(v.spec.Columns) - 1
	remaining := total - fixed - gaps
	if flexCount > 0 {
		per := remaining / flexCount
		if per < 8 {
			per = 8
		}
		for i, c := range v.spec.Columns {
			if c.Width == 0 {
				widths[i] = per
			}
		}
	}
	return widths
}

// pad truncates or pads s to exactly w display cells (ANSI-aware).
func pad(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	if gap := w - lipgloss.Width(s); gap > 0 {
		s += strings.Repeat(" ", gap)
	}
	return s
}

// wrap soft-wraps text to a width using lipgloss.
func wrap(s string, w int) string {
	if w <= 0 {
		return s
	}
	return lipgloss.NewStyle().Width(w).Render(s)
}
