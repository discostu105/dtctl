package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
	"github.com/dynatrace-oss/dtctl/pkg/tui/theme"
)

// enrichCap bounds how many rows one batched enrichment query covers.
const enrichCap = 120

// tableView is the generic engine behind every catalog view: a DQL-backed
// table with incremental filtering and the universal drill-down vocabulary.
type tableView struct {
	ds    *dataSource
	spec  *catalog.Spec
	scope catalog.Scope

	all    []map[string]any // as fetched
	rows   []map[string]any // after /-filter and sort
	cursor int
	offset int

	loading bool
	err     error
	seq     int
	dql     string
	elapsed string

	sortCol  int // -1 = fetch order
	sortDesc bool

	filterInput textinput.Model
	filtering   bool // input focused
	filter      string

	// onData observes fetched records before filtering (the query escape
	// hatch derives result columns from it).
	onData func(records []map[string]any)

	width, height int
}

// enrichOwner tags enrichment queries so their results are told apart from
// the view's list query (both share the view's seq generation).
type enrichOwner struct{ v *tableView }

func newTableView(ds *dataSource, spec *catalog.Spec, scope catalog.Scope) *tableView {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.CharLimit = 64
	return &tableView{ds: ds, spec: spec, scope: scope, filterInput: ti, sortCol: -1}
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

// Busy reports whether the list query is in flight (animates the spinner).
func (v *tableView) Busy() bool { return v.loading }

func (v *tableView) Crumb() string {
	label := v.spec.Name
	if v.scope.Arg != "" {
		label += fmt.Sprintf(" (%s)", strings.ToLower(v.scope.Arg))
	}
	if v.scope.Entity != nil && v.spec.UsesScope() {
		name := v.scope.Entity.Name
		if name == "" {
			name = v.scope.Entity.ID
		}
		label += fmt.Sprintf(" (%s)", name)
	}
	return label
}

// setFilter pre-fills the incremental filter (command-bar args, home jumps).
func (v *tableView) setFilter(f string) {
	v.filterInput.SetValue(f)
	v.filter = f
	v.applyFilter()
}

func (v *tableView) Echo() string {
	if v.dql == "" {
		return ""
	}
	oneline := strings.Join(strings.Fields(strings.ReplaceAll(v.dql, "\n", " ")), " ")
	return fmt.Sprintf("dtctl query '%s'", oneline)
}

// DQL reveals the view's generated query (ctrl+q).
func (v *tableView) DQL() string { return v.dql }

func (v *tableView) Hints() []keyHint {
	if v.filtering {
		return []keyHint{{"type", "filter rows"}, {"enter", "apply"}, {"esc", "clear"}}
	}
	var hints []keyHint
	switch {
	case v.spec.EnterTarget != "":
		hints = append(hints, keyHint{"enter", v.spec.EnterTarget})
		if v.spec.EnterArg == nil && v.spec.Kind == catalog.KindEntity {
			hints = append(hints, keyHint{"d", "details"})
		}
	case v.spec.Kind == catalog.KindEntity:
		hints = append(hints, keyHint{"enter", "details"}, keyHint{"d", "record"})
	default:
		hints = append(hints, keyHint{"enter", "inspect"})
	}
	// Stable order for the drill keys.
	for _, k := range []string{"l", "s", "m", "p", "v"} {
		if target, ok := v.spec.Drills[k]; ok {
			hints = append(hints, keyHint{k, target})
		}
	}
	hints = append(hints, keyHint{"/", "filter"}, keyHint{"J/K", "sort"})
	return hints
}

func (v *tableView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bodySizeMsg:
		v.width, v.height = msg.width, msg.height
		return nil

	case dataMsg:
		if eo, ok := msg.owner.(enrichOwner); ok && eo.v == v {
			if msg.seq == v.seq && msg.err == nil {
				v.applyEnrichment(msg.records)
			}
			return nil // enrichment failures degrade to blank cells
		}
		if msg.owner != any(v) || msg.seq != v.seq {
			return nil
		}
		v.loading = false
		v.err = msg.err
		if msg.err == nil {
			v.all = msg.records
			v.elapsed = catalog.FormatDuration(msg.elapsed)
			if v.onData != nil {
				v.onData(msg.records)
			}
			v.applyFilter()
			return v.enrich()
		}
		return nil

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

// enrich issues the batched sparkline query for the fetched page (capped).
func (v *tableView) enrich() tea.Cmd {
	es := v.spec.Enrich
	if es == nil || len(v.all) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var keys []string
	for _, rec := range v.all {
		if len(keys) >= enrichCap {
			break
		}
		if k := es.Key(rec); k != "" && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	return v.ds.query(enrichOwner{v}, v.seq, es.Query(v.scope.Timeframe, keys))
}

// applyEnrichment joins batched timeseries results onto the fetched rows.
func (v *tableView) applyEnrichment(results []map[string]any) {
	es := v.spec.Enrich
	if es == nil {
		return
	}
	byKey := map[string]map[string]any{}
	for _, res := range results {
		if k := catalog.Str(res, es.By); k != "" {
			byKey[k] = res
		}
	}
	for _, rec := range v.all {
		res := byKey[es.Key(rec)]
		if res == nil {
			continue
		}
		for _, alias := range es.Series {
			if series, ok := res[alias]; ok {
				rec[catalog.EnrichKey(alias)] = series
			}
		}
	}
	v.applyFilter() // re-render (and re-sort: sparkline columns sort by last value)
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
	case "J": // next sort column (wraps through "no sort")
		v.sortCol++
		if v.sortCol >= len(v.spec.Columns) {
			v.sortCol = -1
		}
		v.sortDesc = v.defaultDesc()
		v.applyFilter()
		if v.sortCol < 0 {
			return status("sort: fetch order")
		}
		return status(fmt.Sprintf("sort: %s %s", v.spec.Columns[v.sortCol].Title, sortArrow(v.sortDesc)))
	case "K": // toggle direction of the current sort column
		if v.sortCol < 0 {
			return status("no sort column (J selects one)")
		}
		v.sortDesc = !v.sortDesc
		v.applyFilter()
		return status(fmt.Sprintf("sort: %s %s", v.spec.Columns[v.sortCol].Title, sortArrow(v.sortDesc)))
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
		// Containment navigation (k9s-style) when the spec declares it:
		// workload → its pods, census row → typed list, trace → waterfall.
		if v.spec.EnterTarget == "waterfall" {
			return v.openTrace(rec)
		}
		if v.spec.EnterTarget != "" {
			if target := catalog.Lookup(v.spec.EnterTarget); target != nil {
				scope := catalog.Scope{Timeframe: v.scope.Timeframe}
				if v.spec.EnterArg != nil {
					scope.Arg = v.spec.EnterArg(rec)
				} else if e := v.entityOf(rec); e != nil {
					scope.Entity = e
				}
				return func() tea.Msg { return pushViewMsg{spec: target, scope: scope} }
			}
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
		rec := v.selected()
		if rec == nil {
			return nil
		}
		// Views whose enter follows containment keep the detail page on d.
		if v.spec.EnterTarget != "" && v.spec.EnterArg == nil && v.spec.Kind == catalog.KindEntity {
			if e := v.entityOf(rec); e != nil {
				entity := *e
				return func() tea.Msg { return detailMsg{entity: entity, rec: rec} }
			}
		}
		return v.inspect(rec)
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

// drill opens the target view scoped to the selected row's entity. The
// special targets: "metrics" (canned charts), "trace" (waterfall jump via
// the record's trace id).
func (v *tableView) drill(target string) tea.Cmd {
	rec := v.selected()
	if rec == nil {
		return nil
	}
	if target == "trace" {
		return v.openTrace(rec)
	}
	if target == "trace-logs" {
		if v.spec.Trace == nil {
			return nil
		}
		id := v.spec.Trace(rec)
		if id == "" {
			return statusErr("record carries no trace id")
		}
		spec := catalog.Lookup("logs")
		scope := catalog.Scope{Timeframe: v.scope.Timeframe, TraceID: id}
		return func() tea.Msg { return pushViewMsg{spec: spec, scope: scope} }
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
	if target == "traces" && !catalog.SpanScopable(entity.Type) {
		return statusErr(fmt.Sprintf("spans carry no %s scope field", entity.Type))
	}
	spec := catalog.Lookup(target)
	if spec == nil {
		return statusErr(fmt.Sprintf("unknown view %q", target))
	}
	scope := catalog.Scope{Entity: entity, Timeframe: v.scope.Timeframe}
	return func() tea.Msg { return pushViewMsg{spec: spec, scope: scope} }
}

// openTrace jumps to the waterfall of the selected row's trace.
func (v *tableView) openTrace(rec map[string]any) tea.Cmd {
	if v.spec.Trace == nil {
		return nil
	}
	id := v.spec.Trace(rec)
	if id == "" {
		return statusErr("record carries no trace id")
	}
	return func() tea.Msg { return waterfallMsg{traceID: id} }
}

func (v *tableView) entityOf(rec map[string]any) *catalog.Entity {
	if v.spec.Entity == nil {
		return nil
	}
	return v.spec.Entity(rec)
}

// Selection exposes the highlighted row and its entity for app-level actions
// (pin, yank, open in browser, relations).
func (v *tableView) Selection() (map[string]any, *catalog.Entity) {
	rec := v.selected()
	if rec == nil {
		return nil, nil
	}
	return rec, v.entityOf(rec)
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
	v.sortRows()
	if v.cursor >= len(v.rows) {
		v.cursor = len(v.rows) - 1
	}
	if v.cursor < 0 {
		v.cursor = 0
	}
	v.clampOffset()
}

// sortKey extracts a column's sort key for a record.
func (v *tableView) sortKey(col catalog.Column, rec map[string]any) any {
	switch {
	case col.Sort != nil:
		return col.Sort(rec)
	case col.Field != "":
		return rec[col.Field]
	default:
		return col.Text(rec)
	}
}

// sortRows orders rows by the active sort column (stable; empties last).
func (v *tableView) sortRows() {
	if v.sortCol < 0 || v.sortCol >= len(v.spec.Columns) {
		return
	}
	col := v.spec.Columns[v.sortCol]
	sort.SliceStable(v.rows, func(i, j int) bool {
		a, aEmpty := sortable(v.sortKey(col, v.rows[i]))
		b, bEmpty := sortable(v.sortKey(col, v.rows[j]))
		if aEmpty != bEmpty {
			return bEmpty // empties sink regardless of direction
		}
		if aEmpty {
			return false
		}
		less := cmpSortable(a, b) < 0
		if v.sortDesc {
			less = cmpSortable(a, b) > 0
		}
		return less
	})
}

// defaultDesc picks the natural direction for a freshly selected sort column:
// numbers biggest-first (CPU, restarts), text A-to-Z.
func (v *tableView) defaultDesc() bool {
	if v.sortCol < 0 || v.sortCol >= len(v.spec.Columns) {
		return false
	}
	col := v.spec.Columns[v.sortCol]
	for _, rec := range v.rows {
		key, empty := sortable(v.sortKey(col, rec))
		if empty {
			continue
		}
		_, numeric := key.(float64)
		return numeric
	}
	return false
}

// sortable normalizes a sort key: numbers (including Grail's stringified
// longs and durations) become float64, everything else lowercase text.
func sortable(key any) (norm any, empty bool) {
	switch val := key.(type) {
	case nil:
		return nil, true
	case float64:
		return val, false
	case int:
		return float64(val), false
	case int64:
		return float64(val), false
	case bool:
		if val {
			return 1.0, false
		}
		return 0.0, false
	case string:
		if val == "" {
			return nil, true
		}
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f, false
		}
		return strings.ToLower(val), false
	default:
		s := catalog.FormatValue(val)
		if s == "" {
			return nil, true
		}
		return strings.ToLower(s), false
	}
}

// cmpSortable compares two normalized sort keys; numbers order before text.
func cmpSortable(a, b any) int {
	af, aNum := a.(float64)
	bf, bNum := b.(float64)
	switch {
	case aNum && bNum:
		switch {
		case af < bf:
			return -1
		case af > bf:
			return 1
		}
		return 0
	case aNum:
		return -1
	case bNum:
		return 1
	}
	return strings.Compare(a.(string), b.(string))
}

func sortArrow(desc bool) string {
	if desc {
		return "↓"
	}
	return "↑"
}

func (v *tableView) View(width, height int) string {
	v.width, v.height = width, height

	var b strings.Builder

	// Status/filter line.
	head := " " + theme.Count.Render(fmt.Sprintf("%d", len(v.rows)))
	if len(v.rows) != len(v.all) {
		head += theme.Dim.Render(fmt.Sprintf("/%d", len(v.all)))
	}
	head += theme.Dim.Render(" rows")
	if v.elapsed != "" {
		head += theme.Dim.Render(" · " + v.elapsed)
	}
	if v.sortCol >= 0 && v.sortCol < len(v.spec.Columns) {
		head += theme.Dim.Render(" · ") +
			theme.SortMark.Render(sortArrow(v.sortDesc)+" "+v.spec.Columns[v.sortCol].Title)
	}
	if v.filtering || v.filter != "" {
		head += "  " + v.filterInput.View()
	}
	if v.loading {
		head = " " + theme.Spinner.Render(theme.Spin()) + head
	}
	b.WriteString(ansi.Truncate(head, width, "…"))
	b.WriteString("\n")

	if v.err != nil {
		b.WriteString("\n" + theme.Error.Render("✗ "+wrap(v.err.Error(), width-2)))
		return b.String()
	}

	// One gutter cell marks the cursor row; columns share the rest.
	widths := v.columnWidths(width - 1)

	// Header row.
	var hdr []string
	for i, c := range v.spec.Columns {
		title := c.Title
		if i == v.sortCol {
			title += sortArrow(v.sortDesc)
		}
		hdr = append(hdr, cell(title, widths[i], c.Right))
	}
	b.WriteString(" " + theme.TableHeader.Render(ansi.Truncate(strings.Join(hdr, "  "), width-1, "")))
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
		if v.filter != "" && len(v.all) > 0 {
			b.WriteString(theme.Dim.Render(fmt.Sprintf("  no rows match %q (%d fetched — esc clears the filter)", v.filter, len(v.all))))
		} else {
			b.WriteString("\n" + lipgloss.PlaceHorizontal(width, lipgloss.Center,
				theme.Dim.Render("∅ no data in timeframe (last "+v.scope.Timeframe.Label+")")))
		}
	}
	return b.String()
}

func (v *tableView) renderRow(i int, widths []int, width int) string {
	rec := v.rows[i]
	selected := i == v.cursor

	var cells []string
	for ci, c := range v.spec.Columns {
		text := cell(c.Text(rec), widths[ci], c.Right)
		if !selected && c.Class != nil {
			if class := c.Class(strings.TrimSpace(text)); class != "" {
				text = theme.Class(class, text)
			}
		}
		cells = append(cells, text)
	}
	row := ansi.Truncate(strings.Join(cells, "  "), width-1, "…")
	if selected {
		// Selected rows drop per-cell colors so the highlight reads as one
		// bar: an accent gutter mark plus a background wash.
		return theme.Gutter.Render("▌") + theme.Selected.Render(pad(row, width-1))
	}
	return " " + row
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
	gaps := 2 * (len(v.spec.Columns) - 1)
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

// cell renders a table cell at exactly w cells, right-aligning when asked.
func cell(s string, w int, right bool) string {
	if !right {
		return pad(s, w)
	}
	s = ansi.Truncate(s, w, "…")
	if gap := w - lipgloss.Width(s); gap > 0 {
		s = strings.Repeat(" ", gap) + s
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
