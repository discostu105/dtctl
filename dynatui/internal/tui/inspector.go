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

	"github.com/dynatrace-oss/dtctl/pkg/output"
	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
	"github.com/dynatrace-oss/dynatui/internal/tui/theme"
)

// inspectorView shows a full record grouped by field namespace — records
// routinely carry 50+ dotted fields, so grouping beats flat YAML
// (docs/design/tui.md, "Log record inspector"). The most relevant fields render
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

	names   map[string]string // entity id → display name (batched lookup)
	nameReq map[string]bool   // ids already sent to a name query

	// Signals block (entity mode): the page's active problems (injected by
	// the detail view's pulse query) and the entity's latest change event
	// (own one-shot query) render as navigable rows between the facts and
	// the properties.
	sigProblems []map[string]any
	sigLoaded   bool
	change      map[string]any
	changeLoad  bool

	// Vitals block (entity mode): the curated utilization series injected
	// by the detail view's vitals query, rendered between the facts and the
	// signals.
	vitals       []vitalStat
	vitalsWindow string // timeframe label the series cover

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
	// open is the navigation a synthetic row (signals block) fires on enter;
	// openHint labels it in the footer.
	open     tea.Msg
	openHint string
}

func newInspectorView(ds *dataSource, title string, rec map[string]any) *inspectorView {
	si := newTextInput()
	si.Prompt = "/"
	si.CharLimit = 64
	return &inspectorView{title: title, rec: rec, ds: ds, searchInput: si,
		expanded: map[string]bool{}, names: map[string]string{}, nameReq: map[string]bool{}}
}

// newEntityInfoView builds the details tab of an entity page. rec is the
// already-fetched list row (shown instantly); nil triggers a fetch on Init.
func newEntityInfoView(ds *dataSource, entity catalog.Entity, rec map[string]any) *inspectorView {
	v := newInspectorView(ds, entityName(entity), rec)
	v.facts = catalog.KeyFacts(entity.Type)
	v.dql = catalog.DetailQuery(entity)
	v.entity = &entity
	return v
}

// inspNameOwner tags the batched id→name resolution query.
type inspNameOwner struct{ v *inspectorView }

// changeOwner tags the entity's latest-change-event query (fired once, on
// the details tab's Init).
type changeOwner struct{ v *inspectorView }

// setProblems injects the page's active problems (the detail view's pulse
// result) into the signals block.
func (v *inspectorView) setProblems(problems []map[string]any) {
	v.sigProblems = problems
	v.sigLoaded = true
	v.rebuild()
}

// vitalStat is one row of the details tab's vitals block: a curated
// utilization series (the Vital-marked subset of the entity's canned
// metrics) rendered as sparkline + latest value + avg/max.
type vitalStat struct {
	title  string
	unit   string
	key    string // metric key — enter opens its explorer chart
	series []float64
}

// setVitals injects the page's utilization block (the detail view's vitals
// query result), with the window label the series cover.
func (v *inspectorView) setVitals(stats []vitalStat, window string) {
	v.vitals = stats
	v.vitalsWindow = window
	v.rebuild()
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
	// their table fields — the fetch upgrades them in place. Entity ids in
	// the record resolve to display names in a second batched query, and the
	// session's one-shot dictionary fetch powers the field-doc footer.
	cmds := []tea.Cmd{v.Refresh(), v.resolveNames()}
	if v.ds != nil {
		cmds = append(cmds, v.ds.ensureDict())
	}
	// Entity mode also asks for the latest change-ish event (deploys,
	// config changes, restarts) — the signals block's "what changed here".
	if v.ds != nil && v.entity != nil && v.entity.ID != "" {
		cmds = append(cmds, v.ds.query(changeOwner{v}, 0, catalog.ChangeEventQuery(*v.entity)))
	}
	return tea.Batch(cmds...)
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

// resolveNames issues one batched Smartscape lookup for every entity id in
// the record (top-level, arrays, nested JSON) not yet requested, so ids
// render with their display names next to them.
func (v *inspectorView) resolveNames() tea.Cmd {
	if v.ds == nil || v.rec == nil {
		return nil
	}
	found := map[string]bool{}
	for _, val := range v.rec {
		collectEntityIDs(val, found)
	}
	var ids []string
	for id := range found {
		if !v.nameReq[id] {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	if len(ids) > 100 {
		ids = ids[:100]
	}
	for _, id := range ids {
		v.nameReq[id] = true
	}
	return v.ds.query(inspNameOwner{v}, v.seq, catalog.NamesQuery(ids))
}

// withName appends the resolved Smartscape name next to an entity-id value
// and carries it on the link target (detail pages open pre-titled).
func (v *inspectorView) withName(val valueView) valueView {
	if val.entity == nil || len(val.lines) == 0 {
		return val
	}
	name := v.names[val.entity.ID]
	if name == "" {
		return val
	}
	e := *val.entity
	e.Name = name
	val.entity = &e
	lines := append([]string(nil), val.lines...)
	lines[0] += theme.Dim.Render(" · ") + name
	val.lines = lines
	return val
}

func (v *inspectorView) SetTimeframe(tf catalog.Timeframe) tea.Cmd { return nil }
func (v *inspectorView) InputActive() bool                         { return v.searching }
func (v *inspectorView) Crumb() string                             { return v.title }

// Busy reports whether the detail fetch is in flight.
func (v *inspectorView) Busy() bool { return v.loading }

func (v *inspectorView) Echo() string { return v.ds.echoQuery(v.dql) }

func (v *inspectorView) Hints() []keyHint {
	if v.searching {
		return []keyHint{{"type", "search fields"}, {"enter", "apply"}, {"esc", "clear"}}
	}
	hints := []keyHint{{"j/k", "fields"}}
	if row := v.selectedRow(); row != nil {
		switch {
		case row.open != nil:
			hints = append(hints, keyHint{"enter", row.openHint})
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
	return append(hints, keyHint{"f", "facet list by field"}, keyHint{"y", "yank value"}, keyHint{"/", "search"})
}

func (v *inspectorView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bodySizeMsg:
		v.resize(msg.width, msg.height)
		return nil

	case dataMsg:
		if no, ok := msg.owner.(inspNameOwner); ok && no.v == v {
			// Merge whatever resolved; failures fall back to raw ids.
			if msg.err == nil && len(msg.records) > 0 {
				for _, rec := range msg.records {
					if id := catalog.Str(rec, "id"); id != "" {
						v.names[id] = catalog.Str(rec, "name")
					}
				}
				v.rebuild()
			}
			return nil
		}
		if co, ok := msg.owner.(changeOwner); ok && co.v == v {
			// Fired once per page; errors degrade to an absent row.
			if msg.err == nil {
				if len(msg.records) > 0 {
					v.change = msg.records[0]
				}
				v.changeLoad = true
				v.rebuild()
			}
			return nil
		}
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
		return v.resolveNames()

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
	case "ctrl+d":
		v.pageCursor(max(v.vp.Height/2, 1))
		return nil
	case "ctrl+u":
		v.pageCursor(-max(v.vp.Height/2, 1))
		return nil
	case "pgdown", "ctrl+f":
		v.pageCursor(max(v.vp.Height-1, 1))
		return nil
	case "pgup", "ctrl+b":
		v.pageCursor(-max(v.vp.Height-1, 1))
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
	case "f":
		return v.facetRow()
	}
	var cmd tea.Cmd
	v.vp, cmd = v.vp.Update(msg)
	return cmd
}

// facetRow asks the app to facet the nearest list beneath this page by the
// selected field's value: scalars apply exactly, a string-array element
// applies as a contains pattern (matchesValue is element-wise on string
// arrays), and elements of record or numeric arrays apply as a `~` token
// search — the only operator that matches inside those server-side
// (validated live).
func (v *inspectorView) facetRow() tea.Cmd {
	row := v.selectedRow()
	if row == nil {
		return nil
	}
	field := row.key
	if strings.HasPrefix(field, "__enrich.") {
		return statusErr("enrichment columns are synthetic — no such field server-side")
	}
	switch val := v.rec[field].(type) {
	case map[string]any:
		return statusErr("can't facet on a structured value — pick one of its fields")
	case []any:
		if row.label == row.key || row.val.raw == "" {
			return statusErr("can't facet on a whole array — select one element")
		}
		for _, elem := range val {
			if elem == nil {
				continue
			}
			if _, isString := elem.(string); !isString {
				return func() tea.Msg { return applyFacetMsg{field: field, value: row.val.raw, tokens: true} }
			}
			break
		}
		value := "*" + row.val.raw + "*"
		return func() tea.Msg { return applyFacetMsg{field: field, value: value} }
	default:
		value := catalog.FormatValue(val)
		if value == "" {
			return statusErr("empty value — nothing to facet by")
		}
		if len(value) > 200 {
			return statusErr("value too long to facet by")
		}
		return func() tea.Msg { return applyFacetMsg{field: field, value: value} }
	}
}

// enterRow acts on the selected field: fire a synthetic row's navigation,
// follow an entity/trace link, or toggle a collapsed long value.
func (v *inspectorView) enterRow() tea.Cmd {
	row := v.selectedRow()
	if row == nil {
		return nil
	}
	switch {
	case row.open != nil:
		open := row.open
		return func() tea.Msg { return open }
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
	if v.scrollTallRow(delta) {
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

// pageCursor moves the field cursor by roughly deltaLines of content — page
// jumps over long property lists (ctrl+d/u half page, pgup/pgdn full page)
// instead of one row at a time.
func (v *inspectorView) pageCursor(deltaLines int) {
	if len(v.rows) == 0 {
		return
	}
	if v.scrollTallRow(deltaLines) {
		return
	}
	target := v.rows[v.cursor].line + deltaLines
	i := v.cursor
	if deltaLines > 0 {
		for i < len(v.rows)-1 && v.rows[i+1].line <= target {
			i++
		}
	} else {
		for i > 0 && v.rows[i-1].line >= target {
			i--
		}
	}
	if i == v.cursor { // always make progress, even over a tall block
		if deltaLines > 0 && i < len(v.rows)-1 {
			i++
		} else if deltaLines < 0 && i > 0 {
			i--
		}
	}
	v.cursor = i
	v.refreshVP()
	v.ensureVisible()
}

// scrollTallRow scrolls the viewport within the selected row when the row is
// taller than the screen and movement in that direction still has unseen
// lines — otherwise the middle of an expanded stack trace or GenAI prompt
// would be unreachable (the cursor would leap over the whole block). Reports
// whether it consumed the movement; g/G and enter (collapse) skip the block.
func (v *inspectorView) scrollTallRow(delta int) bool {
	row := v.selectedRow()
	if row == nil || !v.ready || delta == 0 || row.span <= v.vp.Height {
		return false
	}
	if delta > 0 {
		last := row.line + row.span - 1
		if v.vp.YOffset+v.vp.Height > last {
			return false // block bottom already on screen — move on
		}
		v.vp.SetYOffset(min(v.vp.YOffset+delta, last-v.vp.Height+1))
		return true
	}
	if v.vp.YOffset <= row.line {
		return false // block top already on screen
	}
	v.vp.SetYOffset(max(v.vp.YOffset+delta, row.line))
	return true
}

// ensureVisible scrolls the viewport so the selected row is on screen.
func (v *inspectorView) ensureVisible() {
	row := v.selectedRow()
	if row == nil || !v.ready {
		return
	}
	first, last := row.line, row.line+row.span-1
	// The first row drags the un-selectable lines above it (facts, the
	// signals block's header, section titles) into view when they fit —
	// scrolling up at the top row must reach the actual top of the page,
	// not stall at the row's own line.
	if v.cursor == 0 && last < v.vp.Height {
		first = 0
	}
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
	// One line stays reserved for the field-doc footer (the semantic
	// dictionary's description of the field under the cursor) — reserving it
	// unconditionally keeps the viewport height stable as the cursor moves.
	bodyH := height - 1
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
	return out + "\n" + v.docFooter()
}

// docFooter explains the field under the cursor from the semantic dictionary
// — the data model teaching itself ("" while unknown; the line stays
// reserved so the layout never jumps).
func (v *inspectorView) docFooter() string {
	row := v.selectedRow()
	if row == nil || v.ds == nil {
		return ""
	}
	doc, ok := v.ds.fieldDoc(row.key)
	if !ok || doc.Description == "" {
		return ""
	}
	text := doc.Description
	if doc.Unit != "" {
		text += " · unit: " + doc.Unit
	}
	if doc.Stability != "" && doc.Stability != "stable" {
		text += " · " + doc.Stability
	}
	return ansi.Truncate(" "+theme.Label.Render("ⓘ ")+theme.Dim.Render(text), v.vp.Width, "…")
}

// rebuild renders the record into content lines and selectable rows: the
// curated facts panel (entity mode), the signals block (entity mode), the
// per-kind priority-field highlights, the links block (record mode), then
// namespace groups (k8s.*, event.*, …) sorted by name. A search needle
// narrows fields by key or value.
func (v *inspectorView) rebuild() {
	if !v.ready || v.rec == nil {
		return
	}
	v.lines = nil
	v.rows = nil

	needle := strings.ToLower(strings.TrimSpace(v.search))

	if len(v.facts) > 0 {
		for _, f := range v.facts {
			if text := f.Value(v.rec); text != "" {
				if f.Class != nil {
					if class := f.Class(text); class != "" {
						text = theme.Class(class, text)
					}
				}
				v.addLine(" " + theme.FactLabel.Render(fmt.Sprintf("%-14s", f.Label)) + "  " + text)
			}
		}
		if needle == "" {
			v.addVitalRows()
			v.addSignalRows()
		}
		v.addLine("")
		v.addLine(theme.Section("properties"))
	}

	rendered := map[string]bool{}

	// A span's recorded events — exceptions above all — render as a
	// first-class section before everything else: the verdict comes first.
	// The raw span.events array (a collapsed JSON blob otherwise) is
	// consumed by it.
	if v.addSpanEvents(needle) {
		markSpanEventsConsumed(rendered)
	}

	// A GenAI span's exchange renders as a first-class conversation section;
	// the raw message fields (JSON blobs or flat numbered attributes) are
	// consumed by it.
	if v.addConversation(needle) {
		markConversationConsumed(v.rec, rendered)
	}

	var prio []string
	for _, key := range catalog.PriorityFields(v.rec) {
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

	// Record mode: hoist the record's exits — entity ids, trace ids, URLs —
	// into a links block where the eye lands ("ok, what now?" answered with
	// a jump list). Entity pages have the related tab and facts instead.
	if len(v.facts) == 0 {
		v.addLinkRows(needle, rendered)
	}

	groups := map[string][]string{}
	for key, val := range v.rec {
		if rendered[key] || !fieldMatches(needle, key, val) {
			continue
		}
		// Synthetic internals (enrichment series, memoized derivations) are
		// not record data.
		if strings.HasPrefix(key, "__") {
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
	// Top-level (ungrouped) fields come right after the highlights block;
	// dt.* sinks to the bottom — it mostly carries pipeline metadata and
	// entity-id plumbing, not what someone triaging reads first.
	sort.Slice(names, func(i, j int) bool {
		if ri, rj := groupRank(names[i]), groupRank(names[j]); ri != rj {
			return ri < rj
		}
		return names[i] < names[j]
	})

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

// addVitalRows renders the entity page's vitals block btop-style: one row
// per curated utilization series — label, sparkline (the trend), a
// load-colored gauge for percent metrics (the absolute story a normalized
// sparkline can't tell), the latest value, and avg/max over the window.
// Enter on a row opens the metric's explorer chart scoped to the entity
// (aggregation cycling, dimension splits). Absent while loading or
// unavailable: the block must never noise up the page.
func (v *inspectorView) addVitalRows() {
	if len(v.vitals) == 0 {
		return
	}
	v.addLine("")
	v.addLine(theme.Section("vitals (last " + v.vitalsWindow + ")"))
	labelW := 14
	hasPct := false
	for _, s := range v.vitals {
		if n := len(s.title); n > labelW {
			labelW = n
		}
		if s.unit == "%" {
			hasPct = true
		}
	}
	const meterW = 12
	for _, s := range v.vitals {
		_, maxV, avg, last := seriesStats(s.series)
		label := strings.ToLower(s.title)
		valText := fmt.Sprintf("%11s", fmtUnit(last, s.unit))
		gauge, value := "", theme.HeaderVal.Render(valText)
		switch {
		case s.unit == "%":
			gauge = "  " + meter(last, meterW)
			value = theme.Class(pctClass(last), valText)
		case hasPct:
			// Blank gauge slot so the value column stays aligned when the
			// block mixes percent and absolute rows.
			gauge = "  " + strings.Repeat(" ", meterW)
		}
		line := " " + theme.FactLabel.Render(fmt.Sprintf("%-*s", labelW, label)) + "  " +
			theme.Chart.Render(output.MiniGraph(s.series, 16)) + gauge + "  " + value +
			theme.Dim.Render(fmt.Sprintf("   avg %s · max %s", fmtUnit(avg, s.unit), fmtUnit(maxV, s.unit)))
		var entity *catalog.Entity
		if v.entity != nil {
			e := *v.entity
			entity = &e
		}
		v.addActionRow("__vital."+s.key, label, line,
			metricChartMsg{key: s.key, entity: entity}, "chart "+label, fmtUnit(last, s.unit))
	}
}

// meter renders a btop-style 0-100 gauge: the filled span is colored by how
// loaded the value is (calm green, warn from 75%, loud from 90%).
func meter(pct float64, width int) string {
	filled := int(pct/100*float64(width) + 0.5)
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	return theme.Class(pctClass(pct), strings.Repeat("█", filled)) +
		theme.Track.Render(strings.Repeat("░", width-filled))
}

// pctClass grades a percent value into the semantic cell classes.
func pctClass(p float64) string {
	switch {
	case p >= 90:
		return "error"
	case p >= 75:
		return "warn"
	default:
		return "ok"
	}
}

// addSignalRows renders the entity page's signals block: the active problems
// (injected by the page's pulse query) and the latest change-ish event, each
// a navigable row — "what now?" answered with a jump list. Nothing renders
// while both are empty; the header pulse already tells the quiet story.
func (v *inspectorView) addSignalRows() {
	if len(v.sigProblems) == 0 && v.change == nil {
		return
	}
	v.addLine("")
	v.addLine(theme.Section("signals"))
	const maxProblems = 3
	for i, p := range v.sigProblems {
		if i == maxProblems {
			v.addLine("   " + theme.Dim.Render(fmt.Sprintf("… %d more on the problems tab", len(v.sigProblems)-maxProblems)))
			break
		}
		id := catalog.Str(p, "display_id")
		line := " " + theme.Error.Render("⚠ "+id) + "  " + catalog.Str(p, "event.name") +
			theme.Dim.Render("  "+catalog.Str(p, "event.status")+" · "+catalog.Age(catalog.Str(p, "event.start")))
		v.addActionRow("__signal.problem", id, line, problemMsg{rec: p}, "open problem", id)
	}
	if v.change != nil {
		name := catalog.Str(v.change, "event.name")
		if name == "" {
			name = catalog.Str(v.change, "event.type")
		}
		line := " " + theme.Hit.Render("↯ last change") + "  " + name +
			theme.Dim.Render("  "+catalog.Age(catalog.Str(v.change, "timestamp"))+" ago")
		v.addActionRow("__signal.change", "last change", line, inspectMsg{title: name, rec: v.change}, "inspect event", name)
	}
}

// addActionRow appends one synthetic navigable row (signals block): enter
// fires its message, y yanks its raw text.
func (v *inspectorView) addActionRow(key, label, line string, open tea.Msg, hint, raw string) {
	v.rows = append(v.rows, fieldRow{key: key, label: label,
		val:  valueView{lines: []string{line}, raw: raw},
		line: len(v.lines), span: 1, open: open, openHint: hint})
	v.addLine(line)
}

// linkMax caps the links block — a record with thirty entity references must
// not push the highlights off screen (the rest stay in their groups below).
const linkMax = 8

// addLinkRows hoists the record's traversable references — trace ids, entity
// ids, URLs — into one links block between the highlights and the namespace
// groups. Hoisted keys are marked rendered; a key whose rows would overflow
// the cap stays whole in its namespace group instead.
func (v *inspectorView) addLinkRows(needle string, rendered map[string]bool) {
	type linkRow struct {
		label string
		val   valueView
	}
	type linkGroup struct {
		key  string
		rank int // traces first, then entities, then urls
		rows []linkRow
	}
	var groups []linkGroup
	for key, val := range v.rec {
		if rendered[key] || strings.HasPrefix(key, "__") || !fieldMatches(needle, key, val) {
			continue
		}
		if ids := entityIDList(val); ids != nil {
			g := linkGroup{key: key, rank: 1}
			for i, id := range ids {
				g.rows = append(g.rows, linkRow{label: fmt.Sprintf("%s[%d]", key, i),
					val: renderString(key, id, v.vp.Width-6)})
			}
			groups = append(groups, g)
			continue
		}
		s, ok := val.(string)
		if !ok || s == "" {
			continue
		}
		rv := renderString(key, s, v.vp.Width-6)
		var rank int
		switch {
		case rv.trace != "":
			rank = 0
		case rv.entity != nil:
			rank = 1
		case strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://"):
			rank = 2
		default:
			continue
		}
		groups = append(groups, linkGroup{key: key, rank: rank, rows: []linkRow{{label: key, val: rv}}})
	}
	if len(groups) == 0 {
		return
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].rank != groups[j].rank {
			return groups[i].rank < groups[j].rank
		}
		return groups[i].key < groups[j].key
	})
	total := 0
	var kept []linkGroup
	for _, g := range groups {
		if total+len(g.rows) > linkMax {
			continue
		}
		total += len(g.rows)
		rendered[g.key] = true
		kept = append(kept, g)
	}
	if len(kept) == 0 {
		return
	}
	v.addLine("")
	v.addLine(theme.Section("links"))
	for _, g := range kept {
		for _, l := range g.rows {
			v.addRow(g.key, l.label, v.labelStyle(needle, g.key, theme.Label), v.withName(l.val))
		}
	}
}

// addField renders one record field as selectable row(s). Arrays of entity
// ids explode into one navigable row per id; everything else is one row.
func (v *inspectorView) addField(key, needle string, style lipgloss.Style) {
	if ids := entityIDList(v.rec[key]); ids != nil {
		for i, id := range ids {
			label := fmt.Sprintf("%s[%d]", key, i)
			v.addRow(key, label, v.labelStyle(needle, key, style), v.withName(renderString(key, id, v.vp.Width-6)))
		}
		return
	}
	val := renderValue(key, v.rec[key], v.vp.Width-6)
	// The record's own id is not a jump target — style it opaque instead.
	if val.entity != nil && v.entity != nil && val.entity.ID == v.entity.ID {
		val.entity = nil
		val.lines = []string{theme.UID.Render(val.raw)}
	}
	v.addRow(key, key, v.labelStyle(needle, key, style), v.withName(val))
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
		v.addLine(labelText + "  " + theme.Dim.Render("▾"))
		if val.doc != nil {
			// Expanded JSON explodes into one row per key/element, so every
			// nested value is individually selectable and yankable.
			row.span = len(v.lines) - row.line
			v.rows = append(v.rows, row)
			v.addJSONRows(key, label, val.doc)
			return
		}
		lines := val.lines
		if len(lines) == 1 {
			lines = wrapLines(lines[0], v.vp.Width-6)
		}
		for _, l := range lines {
			v.addLine("    " + l)
		}
	}
	row.span = len(v.lines) - row.line
	v.rows = append(v.rows, row)
}

// addJSONRows appends the per-node rows of an expanded JSON block: y yanks
// the leaf (or subtree) under the cursor and enter follows entity ids nested
// inside the document.
func (v *inspectorView) addJSONRows(key, parentLabel string, doc any) {
	for _, node := range jsonRows(doc, "", 0, v.vp.Width-8) {
		label := parentLabel
		if node.path != "" {
			if strings.HasPrefix(node.path, "[") {
				label = parentLabel + node.path
			} else {
				label = parentLabel + "." + node.path
			}
		}
		val := v.withName(valueView{lines: node.lines, raw: node.raw, entity: node.entity})
		row := fieldRow{key: key, label: label, val: val, line: len(v.lines), span: len(val.lines)}
		for _, l := range val.lines {
			v.addLine("    " + l)
		}
		v.rows = append(v.rows, row)
	}
}

// groupRank orders the namespace groups: ungrouped fields first, domain
// groups alphabetically, dt.* last.
func groupRank(g string) int {
	switch g {
	case "":
		return 0
	case "dt":
		return 2
	default:
		return 1
	}
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
