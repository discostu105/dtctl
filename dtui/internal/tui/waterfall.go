package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
	"github.com/dynatrace-oss/dtui/internal/tui/theme"
)

// waterfallView renders one distributed trace as a span tree with
// proportional timing bars — the classic trace waterfall. Spans arrive
// start-ordered; the tree is rebuilt from span.parent_id (roots are spans
// whose parent is null or outside the fetched window).
type waterfallView struct {
	ds      *dataSource
	traceID string
	// focusSpan anchors the cursor on the span the jump came from once the
	// trace loads ("" = root) — consumed on first use so refreshes and manual
	// movement keep the user's own position.
	focusSpan string
	tf        catalog.Timeframe

	rows    []wfRow
	cursor  int
	offset  int
	loading bool
	widened bool // auto-retried with a 24h window after an empty result
	err     error
	seq     int
	dql     string

	width, height int
}

type wfRow struct {
	rec    map[string]any
	guide  string // tree guides (│ ├─ └─) preceding the label
	label  string
	kind   string
	svc    string
	start  int64 // ns since epoch
	end    int64
	failed bool
	// GenAI annotations: the operation badge replaces the span kind and the
	// token usage rides on the label — an agent trace reads as its
	// prompts and tool calls, not as anonymous client/internal spans.
	genaiOp string
	tokens  string
	// category badges db/messaging spans in the kind column ("client" says
	// less than "the span talked to a database").
	category string
}

func newWaterfallView(ds *dataSource, traceID, focusSpan string, tf catalog.Timeframe) *waterfallView {
	return &waterfallView{ds: ds, traceID: traceID, focusSpan: focusSpan, tf: tf}
}

func (v *waterfallView) Init() tea.Cmd { return v.Refresh() }

func (v *waterfallView) Refresh() tea.Cmd {
	v.seq++
	v.loading = true
	v.err = nil
	v.dql = catalog.WaterfallQuery(v.traceID, v.tf)
	return v.ds.query(v, v.seq, v.dql)
}

func (v *waterfallView) SetTimeframe(tf catalog.Timeframe) tea.Cmd {
	v.tf = tf
	v.widened = false
	return v.Refresh()
}

func (v *waterfallView) InputActive() bool { return false }

// Busy reports whether the trace query is in flight (animates the spinner).
func (v *waterfallView) Busy() bool { return v.loading }

func (v *waterfallView) Crumb() string {
	return "trace " + shortID(v.traceID)
}

func (v *waterfallView) Echo() string { return v.ds.echoQuery(v.dql) }

func (v *waterfallView) DQL() string { return v.dql }

func (v *waterfallView) Hints() []keyHint {
	return []keyHint{
		{"enter", "span attributes"}, {"l", "trace logs"}, {"x", "topology"},
		{"y", "yank trace id"}, {"o", "open"},
	}
}

// Selection exposes the highlighted span and its service entity (for pin,
// relations, open-in-browser).
func (v *waterfallView) Selection() (map[string]any, *catalog.Entity) {
	if v.cursor < 0 || v.cursor >= len(v.rows) {
		return nil, nil
	}
	row := v.rows[v.cursor]
	var entity *catalog.Entity
	if id := catalog.Str(row.rec, "dt.smartscape.service"); id != "" {
		entity = &catalog.Entity{ID: id, Name: row.svc, Type: "SERVICE"}
	}
	return row.rec, entity
}

// TraceID lets app-level actions (o, y) target the trace itself.
func (v *waterfallView) TraceID() string { return v.traceID }

func (v *waterfallView) Update(msg tea.Msg) tea.Cmd {
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
		if msg.err != nil {
			return nil
		}
		if len(msg.records) == 0 && !v.widened && v.tf.Dur < 24*time.Hour {
			// The trace may predate the active window — retry once at 24h.
			v.widened = true
			v.tf = catalog.Timeframe{Label: "24h", Dur: 24 * time.Hour}
			return v.Refresh()
		}
		v.rows = buildWaterfall(msg.records)
		if v.cursor >= len(v.rows) {
			v.cursor = 0
			v.offset = 0
		}
		// Anchor on the originating span, then let the user own the cursor.
		if v.focusSpan != "" {
			for i, r := range v.rows {
				if catalog.Str(r.rec, "span.id") == v.focusSpan {
					v.cursor = i
					v.offset = 0
					v.move(0) // clamp the window around the anchored row
					break
				}
			}
			v.focusSpan = ""
		}
		return nil

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

func (v *waterfallView) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		v.move(-1)
	case "down", "j":
		v.move(1)
	case "pgup", "ctrl+b":
		v.move(-v.visible())
	case "pgdown", "ctrl+f", " ":
		v.move(v.visible())
	case "home", "g":
		v.cursor, v.offset = 0, 0
	case "end", "G":
		v.move(len(v.rows))
	case "enter", "d":
		if rec, _ := v.Selection(); rec != nil {
			// GenAI labels carry whole prompts — keep the crumb short.
			label := ansi.Truncate(v.rows[v.cursor].label, 40, "…")
			return func() tea.Msg { return inspectMsg{title: label, rec: rec} }
		}
	case "l":
		spec := catalog.Lookup("logs")
		if spec == nil {
			return nil
		}
		scope := catalog.Scope{Timeframe: v.tf, TraceID: v.traceID}
		return func() tea.Msg { return pushViewMsg{spec: spec, scope: scope} }
	}
	return nil
}

func (v *waterfallView) move(delta int) {
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
	if vis := v.visible(); v.cursor >= v.offset+vis {
		v.offset = v.cursor - vis + 1
	}
	if v.offset < 0 {
		v.offset = 0
	}
}

func (v *waterfallView) visible() int { return max(v.height-1, 1) }

// buildWaterfall assembles the depth-ordered rows from start-sorted spans.
func buildWaterfall(records []map[string]any) []wfRow {
	ids := map[string]bool{}
	for _, rec := range records {
		ids[catalog.Str(rec, "span.id")] = true
	}
	children := map[string][]map[string]any{}
	var roots []map[string]any
	for _, rec := range records {
		parent := catalog.Str(rec, "span.parent_id")
		if parent == "" || !ids[parent] {
			roots = append(roots, rec) // true root or parent outside window
			continue
		}
		children[parent] = append(children[parent], rec)
	}

	var rows []wfRow
	var walk func(rec map[string]any, prefix string, last, root bool)
	walk = func(rec map[string]any, prefix string, last, root bool) {
		label := catalog.Str(rec, "span.name")
		if label == "" {
			label = catalog.Str(rec, "endpoint.name")
		}
		guide, childPrefix := prefix, prefix
		if !root {
			if last {
				guide += "└─ "
				childPrefix += "   "
			} else {
				guide += "├─ "
				childPrefix += "│  "
			}
		}
		row := wfRow{
			rec:      rec,
			guide:    guide,
			label:    label,
			kind:     catalog.Str(rec, "span.kind"),
			svc:      catalog.Str(rec, "service.name"),
			start:    parseTimeNs(catalog.Str(rec, "start_time")),
			end:      parseTimeNs(catalog.Str(rec, "end_time")),
			failed:   catalog.SpanFailed(rec),
			category: catalog.SpanCategory(rec),
		}
		if op := catalog.GenAIOp(rec); op != "" {
			row.genaiOp = catalog.GenAIOpShort(op)
			row.tokens = catalog.GenAITokens(rec)
			// The prompt or tool call is the span's story — the raw span
			// name ("anthropic.chat") says nothing an op badge doesn't.
			if detail := catalog.GenAIDetail(rec); detail != "" {
				row.label = detail
			}
		}
		rows = append(rows, row)
		kids := children[catalog.Str(rec, "span.id")]
		for i, child := range kids {
			walk(child, childPrefix, i == len(kids)-1, false)
		}
	}
	for _, root := range roots {
		walk(root, "", false, true)
	}
	return rows
}

func parseTimeNs(iso string) int64 {
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		return 0
	}
	return t.UnixNano()
}

func (v *waterfallView) View(width, height int) string {
	v.width, v.height = width, height
	var b strings.Builder

	switch {
	case v.loading:
		return " " + theme.Spinner.Render(theme.Spin()+" loading trace…")
	case v.err != nil:
		return theme.Error.Render("✗ " + wrap(v.err.Error(), width-2))
	case len(v.rows) == 0:
		return "\n" + lipgloss.PlaceHorizontal(width, lipgloss.Center,
			theme.Dim.Render("∅ trace not found in the last "+v.tf.Label))
	}

	// Trace-wide time axis.
	t0, t1 := v.rows[0].start, v.rows[0].end
	failed := 0
	for _, r := range v.rows {
		if r.start != 0 && r.start < t0 {
			t0 = r.start
		}
		if r.end > t1 {
			t1 = r.end
		}
		if r.failed {
			failed++
		}
	}
	total := max64(t1-t0, 1)

	head := " " + theme.Count.Render(catalog.FormatNs(float64(total))) +
		theme.Dim.Render(fmt.Sprintf(" · %d spans", len(v.rows)))
	if failed > 0 {
		head += theme.Dim.Render(" · ") + theme.Error.Render(fmt.Sprintf("✗ %d failed", failed))
	}
	b.WriteString(head + "\n")

	// Column layout: tree | kind | service | bar+duration.
	barW := width * 30 / 100
	if barW < 16 {
		barW = 16
	}
	durW := 9
	kindW, svcW := 8, 22
	// Five separator spaces plus the one-cell cursor gutter.
	treeW := width - barW - durW - kindW - svcW - 6
	if treeW < 16 {
		treeW = 16
	}

	end := v.offset + v.visible()
	if end > len(v.rows) {
		end = len(v.rows)
	}
	for i := v.offset; i < end; i++ {
		b.WriteString(v.renderRow(v.rows[i], i == v.cursor, t0, total, treeW, kindW, svcW, barW, durW))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (v *waterfallView) renderRow(r wfRow, selected bool, t0, total int64, treeW, kindW, svcW, barW, durW int) string {
	label := r.label
	if r.failed {
		label = "✗ " + label
	}
	if r.tokens != "" {
		label += " ⟨" + r.tokens + "⟩"
	}
	kindText := r.kind
	switch {
	case r.genaiOp != "":
		kindText = genaiGlyph(r.genaiOp) + " " + r.genaiOp
	case r.category == "db":
		kindText = "⛁ db"
	case r.category == "messaging":
		kindText = "✉ msg"
	}
	tree := pad(r.guide+label, treeW)
	kind := pad(kindText, kindW)
	svc := pad(r.svc, svcW)

	// Proportional bar on the trace's time axis. Clamp both ends — a span
	// with an unparseable timestamp lands at 0 and must not underflow.
	dur := r.end - r.start
	startCell := int((r.start - t0) * int64(barW) / total)
	lenCells := int(dur * int64(barW) / total)
	if startCell < 0 {
		startCell = 0
	}
	if startCell >= barW {
		startCell = barW - 1
	}
	if lenCells < 1 {
		lenCells = 1
	}
	if startCell+lenCells > barW {
		lenCells = barW - startCell
	}
	durTxt := cell(catalog.FormatNs(float64(dur)), durW, true)

	if selected {
		bar := strings.Repeat("┄", startCell) + strings.Repeat("█", lenCells) +
			strings.Repeat("┄", barW-startCell-lenCells)
		return theme.Gutter.Render("▌") +
			theme.Selected.Render(pad(tree+" "+kind+" "+svc+" "+bar+" "+durTxt, v.width-1))
	}

	// Bars are colored by service, so one service's spans group visually;
	// the dim track keeps offsets readable across rows.
	barStyle := theme.ForKey(r.svc)
	if r.failed {
		barStyle = theme.Error
		tree = theme.Error.Render(tree)
	} else {
		tree = theme.Rule.Render(r.guide) + pad(label, max(treeW-lipgloss.Width(r.guide), 0))
	}
	bar := theme.Track.Render(strings.Repeat("┄", startCell)) +
		barStyle.Render(strings.Repeat("█", lenCells)) +
		theme.Track.Render(strings.Repeat("┄", barW-startCell-lenCells))
	if r.svc != "" {
		svc = barStyle.Render("●") + " " + pad(r.svc, max(svcW-2, 0))
	}
	kindStyle := theme.Dim
	switch {
	case r.genaiOp != "":
		kindStyle = theme.GenAI
	case r.category != "":
		kindStyle = theme.Label
	}
	return ansi.Truncate(" "+tree+" "+kindStyle.Render(kind)+" "+svc+" "+bar+" "+theme.Dim.Render(durTxt), v.width, "…")
}

// genaiGlyph marks a GenAI operation in the waterfall's kind column.
func genaiGlyph(op string) string {
	switch op {
	case "tool":
		return "⚙"
	case "agent":
		return "◈"
	}
	return "✦" // chat, embeddings, …
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8] + "…"
	}
	return id
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
