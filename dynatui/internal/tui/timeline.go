package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
	"github.com/dynatrace-oss/dynatui/internal/tui/theme"
)

// timelineView renders one RUM session as a waterfall: events proportional
// on the session's time axis, nested by containment (view → action →
// request/error), colored by kind. The session-side twin of the trace
// waterfall — 's' on a request row jumps into its backend trace.
type timelineView struct {
	ds        *dataSource
	sessionID string
	session   map[string]any // the sessions-list row (nil on a history restore)
	tf        catalog.Timeframe
	lens      int

	rows    []catalog.TimelineRow
	cursor  int
	offset  int
	loading bool
	err     error
	seq     int
	dql     string

	width, height int
}

func newTimelineView(ds *dataSource, sessionID string, session map[string]any, tf catalog.Timeframe) *timelineView {
	return &timelineView{ds: ds, sessionID: sessionID, session: session, tf: tf}
}

// sessOwner tags the session-record fetch (the timeline entered from a
// user-event drill or a history restore has no sessions-list row yet).
type sessOwner struct{ v *timelineView }

func (v *timelineView) Init() tea.Cmd {
	cmds := []tea.Cmd{v.Refresh()}
	if v.session == nil {
		cmds = append(cmds, v.ds.query(sessOwner{v}, v.seq, catalog.SessionRecordQuery(v.sessionID, v.tf)))
	}
	return tea.Batch(cmds...)
}

func (v *timelineView) Refresh() tea.Cmd {
	v.seq++
	v.loading = true
	v.err = nil
	v.dql = catalog.SessionTimelineQuery(v.sessionID, v.tf, v.lens)
	return v.ds.query(v, v.seq, v.dql)
}

func (v *timelineView) SetTimeframe(tf catalog.Timeframe) tea.Cmd {
	v.tf = tf
	return v.Refresh()
}

func (v *timelineView) InputActive() bool { return false }

// Busy reports whether the events query is in flight.
func (v *timelineView) Busy() bool { return v.loading }

func (v *timelineView) Crumb() string {
	label := "session " + shortID(v.sessionID)
	if v.lens > 0 {
		label += "·" + catalog.SessionTimelineLenses[v.lens].Name
	}
	return label
}

func (v *timelineView) Echo() string { return v.ds.echoQuery(v.dql) }

func (v *timelineView) DQL() string { return v.dql }

func (v *timelineView) Hints() []keyHint {
	hints := []keyHint{
		{fmt.Sprintf("tab/1-%d", len(catalog.SessionTimelineLenses)), "lens"},
		{"enter", "event record"},
		{"d", "session record"},
	}
	if row := v.selectedRow(); row != nil && row.Trace != "" {
		hints = append(hints, keyHint{"s", "backend trace"})
	}
	hints = append(hints,
		keyHint{"e", "events table"}, keyHint{"x", "relations"}, keyHint{"o", "open"})
	if !previewEnabled {
		hints = append(hints, keyHint{"P", "preview"})
	}
	return hints
}

// Selection exposes the highlighted event and its frontend entity (pin,
// relations, open-in-browser act on it).
func (v *timelineView) Selection() (map[string]any, *catalog.Entity) {
	row := v.selectedRow()
	if row == nil {
		return v.session, nil
	}
	var entity *catalog.Entity
	if id := catalog.Str(row.Rec, "dt.smartscape.frontend"); id != "" {
		entity = &catalog.Entity{ID: id, Name: catalog.Str(row.Rec, "frontend.name"), Type: "FRONTEND"}
	}
	return row.Rec, entity
}

func (v *timelineView) selectedRow() *catalog.TimelineRow {
	if v.cursor < 0 || v.cursor >= len(v.rows) {
		return nil
	}
	return &v.rows[v.cursor]
}

func (v *timelineView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bodySizeMsg:
		v.width, v.height = msg.width, msg.height
		return nil

	case dataMsg:
		if so, ok := msg.owner.(sessOwner); ok && so.v == v {
			// The session's own record — header context and the 'd' jump.
			// Failures stay silent: the timeline is the primary content.
			if msg.err == nil && len(msg.records) > 0 && v.session == nil {
				v.session = msg.records[0]
			}
			return nil
		}
		if msg.owner != any(v) || msg.seq != v.seq {
			return nil
		}
		v.loading = false
		v.err = msg.err
		if msg.err != nil {
			return nil
		}
		v.rows = catalog.BuildSessionTimeline(msg.records)
		if v.cursor >= len(v.rows) {
			v.cursor = 0
			v.offset = 0
		}
		return nil

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

func (v *timelineView) setLens(i int, wrap bool) tea.Cmd {
	n := len(catalog.SessionTimelineLenses)
	if wrap {
		i = ((i % n) + n) % n
	}
	if i < 0 || i >= n || i == v.lens {
		return nil
	}
	v.lens = i
	v.cursor, v.offset = 0, 0
	l := catalog.SessionTimelineLenses[i]
	return tea.Batch(v.Refresh(), markHistory, status(fmt.Sprintf("lens: %s — %s", l.Name, l.Desc)))
}

func (v *timelineView) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch key := msg.String(); key {
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
	case "]", "tab":
		return v.setLens(v.lens+1, true)
	case "[", "shift+tab":
		return v.setLens(v.lens-1, true)
	case "enter":
		if row := v.selectedRow(); row != nil {
			rec, label := row.Rec, row.Label
			return func() tea.Msg { return inspectMsg{title: ansi.Truncate(label, 40, "…"), rec: rec} }
		}
	case "d":
		// The session's own record — user, browser, OS, geo, end reason —
		// the event→session navigation in one keystroke.
		if v.session == nil {
			return statusErr("session record not loaded yet")
		}
		rec := v.session
		title := "session " + shortID(v.sessionID)
		return func() tea.Msg { return inspectMsg{title: title, rec: rec} }
	case "s":
		row := v.selectedRow()
		if row == nil || row.Trace == "" {
			return statusErr("event carries no trace id (requests to traced backends do)")
		}
		trace := row.Trace
		return func() tea.Msg { return waterfallMsg{traceID: trace} }
	case "e":
		// The flat, sortable events table of the same session.
		spec := catalog.Lookup("userevents")
		if spec == nil {
			return nil
		}
		scope := catalog.Scope{Timeframe: v.tf, Arg: v.sessionID}
		return func() tea.Msg { return pushViewMsg{spec: spec, scope: scope} }
	}
	return nil
}

func (v *timelineView) move(delta int) {
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

// visible is the row budget under the header and lens strip, minus the
// bottom preview panel's bite when that layout is active.
func (v *timelineView) visible() int {
	h := v.height - 2
	if previewBottomOn(v.width, v.height) {
		h -= previewBottomH + 1
	}
	return max(h, 1)
}

func (v *timelineView) View(width, height int) string {
	v.width, v.height = width, height
	return previewLayout(width, height, v.renderBody, v.previewLines)
}

func (v *timelineView) renderBody(width, height int) string {
	var b strings.Builder

	switch {
	case v.loading && len(v.rows) == 0:
		return " " + theme.Spinner.Render(theme.Spin()+" loading session…")
	case v.err != nil:
		return theme.Error.Render("✗ " + wrap(v.err.Error(), width-2))
	}

	// Session-wide time axis and counts.
	var t0, t1 int64
	failed := 0
	for i, r := range v.rows {
		if i == 0 || (r.Start != 0 && r.Start < t0) {
			t0 = r.Start
		}
		if r.End > t1 {
			t1 = r.End
		}
		if r.Failed {
			failed++
		}
	}
	total := max64(t1-t0, 1)

	head := " " + theme.Count.Render(catalog.FormatNs(float64(total))) +
		theme.Dim.Render(fmt.Sprintf(" · %d events", len(v.rows)))
	if failed > 0 {
		head += theme.Dim.Render(" · ") + theme.Error.Render(fmt.Sprintf("✗ %d failed", failed))
	}
	if app := v.sessionApp(); app != "" {
		head += theme.Dim.Render(" · " + app)
	}
	b.WriteString(ansi.Truncate(head, width, "…") + "\n")

	// Lens strip, detail-tab style.
	labels := make([]string, len(catalog.SessionTimelineLenses))
	for i, l := range catalog.SessionTimelineLenses {
		label := fmt.Sprintf("%d · %s", i+1, l.Name)
		if i == v.lens {
			labels[i] = theme.TabActive.Render(label)
		} else {
			labels[i] = theme.TabInactive.Render(label)
		}
	}
	b.WriteString(ansi.Truncate(" "+strings.Join(labels, " "), width, "…") + "\n")

	if len(v.rows) == 0 {
		b.WriteString("\n" + lipgloss.PlaceHorizontal(width, lipgloss.Center,
			theme.Dim.Render("∅ no "+catalog.SessionTimelineLenses[v.lens].Name+" events in this session — tab switches lens")))
		return b.String()
	}

	// Column layout: label | kind | bar | duration (mirrors the waterfall).
	barW := width * 30 / 100
	if barW < 16 {
		barW = 16
	}
	durW, kindW := 9, 8
	labelW := width - barW - durW - kindW - 5
	if labelW < 16 {
		labelW = 16
	}

	end := v.offset + v.visible()
	if end > len(v.rows) {
		end = len(v.rows)
	}
	for i := v.offset; i < end; i++ {
		b.WriteString(v.renderRow(v.rows[i], i == v.cursor, t0, total, width, labelW, kindW, barW, durW))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// previewLines renders the highlighted event's peek pane: identity, the
// classifier-specific facts (web vitals, request verdict, error origin),
// where the event sits on the session's time axis, and its backend-trace
// jump when it has one.
func (v *timelineView) previewLines(w int) []string {
	row := v.selectedRow()
	if row == nil {
		return []string{theme.Dim.Render("no selection")}
	}
	title := catalog.PreviewTitle(row.Rec)
	if title == "" {
		title = row.Label
	}
	lines := []string{theme.OverlayTitle.Render(ansi.Truncate(flatten(title), w, "…")), ""}
	lines = append(lines, renderPreviewFacts(catalog.PreviewFacts(row.Rec), w)...)
	t0 := row.Start
	for _, r := range v.rows {
		if r.Start != 0 && r.Start < t0 {
			t0 = r.Start
		}
	}
	if row.Start > t0 {
		lines = append(lines, " "+theme.FactLabel.Render("offset:")+" "+
			catalog.FormatNs(float64(row.Start-t0))+theme.Dim.Render(" into the session"))
	}
	if row.Trace != "" {
		lines = append(lines, "", " "+theme.FactLabel.Render("trace:")+" "+
			theme.UID.Render(shortID(row.Trace))+theme.Dim.Render(" (s opens)"))
	}
	return lines
}

// sessionApp names the session's frontend for the header line.
func (v *timelineView) sessionApp() string {
	if v.session != nil {
		if app := catalog.StrFirst(v.session, "frontend.name"); app != "" {
			return app
		}
	}
	if len(v.rows) > 0 {
		return catalog.Str(v.rows[0].Rec, "frontend.name")
	}
	return ""
}

func (v *timelineView) renderRow(r catalog.TimelineRow, selected bool, t0, total int64, width, labelW, kindW, barW, durW int) string {
	indent := strings.Repeat("  ", r.Depth)
	label := r.Label
	if r.Failed {
		label = "✗ " + label
	}
	text := pad(indent+label, labelW)
	kind := pad(r.Kind, kindW)

	dur := r.End - r.Start
	startCell := int((r.Start - t0) * int64(barW) / total)
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
			theme.Selected.Render(pad(text+" "+kind+" "+bar+" "+durTxt, width-1))
	}

	style := timelineKindStyle(r.Kind)
	if r.Failed {
		style = theme.Error
		text = theme.Error.Render(text)
	} else if r.Depth > 0 {
		text = theme.Rule.Render(indent) + pad(label, max(labelW-len(indent), 0))
	}
	bar := theme.Track.Render(strings.Repeat("┄", startCell)) +
		style.Render(strings.Repeat("█", lenCells)) +
		theme.Track.Render(strings.Repeat("┄", barW-startCell-lenCells))
	return ansi.Truncate(" "+text+" "+style.Render(kind)+" "+bar+" "+theme.Dim.Render(durTxt), width, "…")
}

// timelineKindStyle colors an event kind consistently across rows.
func timelineKindStyle(kind string) lipgloss.Style {
	switch kind {
	case "view", "page":
		return theme.SeriesAt(0) // accent
	case "action":
		return theme.SeriesAt(1) // teal
	case "request":
		return theme.SeriesAt(4) // sky
	case "error":
		return theme.Error
	case "nav":
		return theme.SeriesAt(2) // mauve
	}
	return theme.Dim
}
