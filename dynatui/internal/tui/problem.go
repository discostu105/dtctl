package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
	"github.com/dynatrace-oss/dynatui/internal/tui/theme"
)

// problemView is the tabbed page behind enter on a Davis problem — the front
// door of an investigation (docs/design/tui.md, "Problem detail"). The overview
// leads with Davis's own explanation and the affected entities as navigable
// rows; the evidence tab lists the constituent Davis events; and the signal
// tabs open logs/traces/events pre-scoped to the affected entities AND the
// problem's time window, not the global one — the page owns its window, so
// the global timeframe picker deliberately does not reach into it. The full
// record stays one tab away (details).
type problemView struct {
	tabSet
	rec      map[string]any
	window   catalog.Timeframe
	affected []catalog.Entity
}

func newProblemView(ds *dataSource, rec map[string]any, now time.Time) *problemView {
	v := &problemView{
		rec:      rec,
		window:   catalog.ProblemWindow(rec, now),
		affected: catalog.ProblemAffectedEntities(rec),
	}
	v.tabs = []detailTab{{name: "overview", view: newProblemOverview(rec, v.affected)}}
	if ids := catalog.ProblemEventIDs(rec); len(ids) > 0 {
		// Evidence records trail the problem window on both sides (analysis
		// offsets, late CLOSED transitions) — query a generously padded one.
		scope := catalog.Scope{Arg: strings.Join(ids, ","), Timeframe: padWindow(v.window, time.Hour)}
		v.tabs = append(v.tabs, detailTab{name: "evidence", view: newTableView(ds, catalog.DavisEventsSpec, scope)})
	}
	if len(v.affected) > 0 {
		scope := catalog.Scope{Entities: v.affected, Timeframe: v.window}
		if spec := catalog.Lookup("logs"); spec != nil {
			v.tabs = append(v.tabs, detailTab{name: "logs", view: newTableView(ds, spec, scope)})
		}
		if spec := catalog.Lookup("traces"); spec != nil && catalog.ScopeSpanFilter(scope) != "" {
			v.tabs = append(v.tabs, detailTab{name: "traces", view: newTableView(ds, spec, scope)})
		}
		if spec := catalog.Lookup("events"); spec != nil {
			v.tabs = append(v.tabs, detailTab{name: "events", view: newTableView(ds, spec, scope)})
		}
	}
	v.tabs = append(v.tabs, detailTab{name: "details",
		view: newInspectorView(ds, catalog.Str(rec, "display_id"), rec)})
	v.adoptTabs()
	return v
}

// padWindow widens an absolute window by margin on both bounds (an open end
// stays open). Relative windows pass through.
func padWindow(w catalog.Timeframe, margin time.Duration) catalog.Timeframe {
	if !w.Absolute() {
		return w
	}
	out := catalog.Timeframe{Label: w.Label, From: w.From.Add(-margin), Dur: w.Dur + margin}
	if !w.To.IsZero() {
		out.To = w.To.Add(margin)
		out.Dur = out.To.Sub(out.From)
	}
	return out
}

func (v *problemView) Init() tea.Cmd { return v.startFirst() }

// SetTimeframe is a no-op: the page's tabs are scoped to the problem's own
// window — that is the point of the page — so the global picker passes by.
func (v *problemView) SetTimeframe(catalog.Timeframe) tea.Cmd { return nil }

func (v *problemView) Crumb() string { return catalog.Str(v.rec, "display_id") }

// Selection exposes the problem record plus the most specific entity: an
// affected entity highlighted on the overview, a row's entity on a signal
// tab. The record keeps 'o' pointed at the Davis Problems app everywhere.
func (v *problemView) Selection() (map[string]any, *catalog.Entity) {
	switch t := v.activeView().(type) {
	case *problemOverview:
		return v.rec, t.selectedEntity()
	case *inspectorView:
		return t.Selection()
	case *tableView:
		return t.Selection()
	}
	return v.rec, nil
}

func (v *problemView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bodySizeMsg:
		return v.resizeTabs(msg.width, msg.height)
	case dataMsg:
		return v.forwardData(msg)
	case tea.KeyMsg:
		if !v.activeView().InputActive() {
			if cmd, handled := v.tabKey(msg.String()); handled {
				return cmd
			}
		}
		return v.activeView().Update(msg)
	}
	return v.activeView().Update(msg)
}

func (v *problemView) View(width, height int) string {
	identity := " " + theme.OverlayTitle.Render(catalog.Str(v.rec, "display_id")) +
		"  " + catalog.Str(v.rec, "event.name")
	return v.renderPage(width, height, identity, " "+v.statusLine())
}

// statusLine is the header's second line: status, severity, age, blast
// radius, and the investigation window the signal tabs are scoped to.
func (v *problemView) statusLine() string {
	var parts []string
	status := catalog.Str(v.rec, "event.status")
	if status == "ACTIVE" {
		parts = append(parts, theme.Error.Render("● "+status))
	} else if status != "" {
		parts = append(parts, theme.Dim.Render("● "+status))
	}
	if sev := catalog.ProblemSeverity(v.rec); sev != "" {
		if class := catalog.ClassSeverityBadge(sev); class != "" {
			sev = theme.Class(class, sev)
		} else {
			sev = theme.Badge.Render(sev)
		}
		parts = append(parts, sev)
	}
	if age := catalog.Age(catalog.Str(v.rec, "event.start")); age != "" {
		parts = append(parts, theme.Dim.Render("started "+age+" ago"))
	}
	if n := len(v.affected); n == 1 {
		parts = append(parts, theme.Dim.Render("1 affected entity"))
	} else if n > 1 {
		parts = append(parts, theme.Dim.Render(fmt.Sprintf("%d affected entities", n)))
	}
	parts = append(parts, theme.Dim.Render("⧖ "+windowLabel(v.window)))
	return strings.Join(parts, theme.HeaderSep.Render("  ·  "))
}

// windowLabel renders the investigation window compactly ("07:45 → now").
func windowLabel(w catalog.Timeframe) string {
	if !w.Absolute() {
		return "last " + w.Label
	}
	from := w.From.Local().Format("15:04")
	to := "now"
	if !w.To.IsZero() {
		to = w.To.Local().Format("15:04")
		if !w.To.Local().Truncate(24 * time.Hour).Equal(w.From.Local().Truncate(24 * time.Hour)) {
			to = w.To.Local().Format("Jan 02 15:04")
		}
	}
	return from + " → " + to
}

// problemOverview is the problem page's first tab: curated facts, the
// affected entities as navigable rows (the next hop of every triage), and
// Davis's own explanation. Everything renders from the record in hand — no
// query.
type problemOverview struct {
	rec      map[string]any
	affected []catalog.Entity

	lines  []string
	rows   []int // line index of each affected-entity row
	cursor int
	offset int

	width, height int
}

func newProblemOverview(rec map[string]any, affected []catalog.Entity) *problemOverview {
	return &problemOverview{rec: rec, affected: affected}
}

func (v *problemOverview) Init() tea.Cmd                          { return nil }
func (v *problemOverview) Refresh() tea.Cmd                       { return nil }
func (v *problemOverview) SetTimeframe(catalog.Timeframe) tea.Cmd { return nil }
func (v *problemOverview) InputActive() bool                      { return false }
func (v *problemOverview) Crumb() string                          { return "overview" }
func (v *problemOverview) Echo() string                           { return "" }

func (v *problemOverview) Hints() []keyHint {
	hints := []keyHint{{"j/k", "move"}}
	if v.selectedEntity() != nil {
		hints = append(hints, keyHint{"enter", "open entity"}, keyHint{"x", "relations"}, keyHint{".", "pin"})
	}
	return append(hints, keyHint{"y", "yank id"})
}

// selectedEntity is the affected entity under the cursor (nil when none).
func (v *problemOverview) selectedEntity() *catalog.Entity {
	if v.cursor < 0 || v.cursor >= len(v.affected) {
		return nil
	}
	e := v.affected[v.cursor]
	return &e
}

// YankText supplies the selected entity's id to the global 'y'.
func (v *problemOverview) YankText() (string, string, bool) {
	if e := v.selectedEntity(); e != nil {
		return e.ID, e.ID, true
	}
	return "", "", false
}

func (v *problemOverview) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bodySizeMsg:
		v.width, v.height = msg.width, msg.height
		v.rebuild()
		return nil
	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

func (v *problemOverview) handleKey(msg tea.KeyMsg) tea.Cmd {
	page := max(v.height-1, 1)
	switch msg.String() {
	case "j", "down":
		v.moveCursor(1)
	case "k", "up":
		v.moveCursor(-1)
	case "g", "home":
		v.cursor, v.offset = 0, 0
	case "G", "end":
		v.cursor = max(len(v.affected)-1, 0)
		v.offset = max(len(v.lines)-v.height, 0)
	case "ctrl+d":
		v.scroll(page / 2)
	case "ctrl+u":
		v.scroll(-page / 2)
	case "pgdown", "ctrl+f":
		v.scroll(page)
	case "pgup", "ctrl+b":
		v.scroll(-page)
	case "enter":
		if e := v.selectedEntity(); e != nil {
			entity := *e
			return func() tea.Msg { return detailMsg{entity: entity} }
		}
	}
	return nil
}

func (v *problemOverview) moveCursor(delta int) {
	if len(v.affected) == 0 {
		v.scroll(delta)
		return
	}
	v.cursor += delta
	if v.cursor < 0 {
		v.cursor = 0
	}
	if v.cursor >= len(v.affected) {
		v.cursor = len(v.affected) - 1
	}
	v.ensureVisible()
}

func (v *problemOverview) scroll(delta int) {
	v.offset += delta
	v.clampOffset()
}

func (v *problemOverview) clampOffset() {
	if v.offset > len(v.lines)-v.height {
		v.offset = len(v.lines) - v.height
	}
	if v.offset < 0 {
		v.offset = 0
	}
}

// ensureVisible scrolls so the selected entity row is on screen.
func (v *problemOverview) ensureVisible() {
	if v.cursor < 0 || v.cursor >= len(v.rows) {
		return
	}
	line := v.rows[v.cursor]
	// The first impact row drags the facts above it into view when they
	// fit — scrolling up at the top row reaches the actual top.
	top := line
	if v.cursor == 0 && line < v.height {
		top = 0
	}
	if top < v.offset {
		v.offset = top
	}
	if line >= v.offset+v.height {
		v.offset = line - v.height + 1
	}
	v.clampOffset()
}

// rebuild assembles the overview's lines: facts, the impact list, then the
// Davis description (the jumps stay above the fold; the prose reads below).
func (v *problemOverview) rebuild() {
	v.lines = nil
	v.rows = nil
	fact := func(label, text string) {
		if text != "" {
			v.lines = append(v.lines, " "+theme.FactLabel.Render(fmt.Sprintf("%-14s", label))+"  "+text)
		}
	}

	status := catalog.Str(v.rec, "event.status")
	if transition := catalog.Str(v.rec, "event.status_transition"); transition != "" && transition != status {
		status += theme.Dim.Render(" (" + strings.ToLower(transition) + ")")
	}
	fact("status", status)
	if sev := catalog.ProblemSeverity(v.rec); sev != "" {
		if class := catalog.ClassSeverityBadge(sev); class != "" {
			sev = theme.Class(class, sev)
		}
		fact("severity", sev)
	}
	fact("category", catalog.Str(v.rec, "event.category"))
	fact("impact", catalog.StrFirst(v.rec, "dt.davis.impact_level"))
	if start := catalog.Str(v.rec, "event.start"); start != "" {
		fact("started", catalog.FormatTime(start)+theme.Dim.Render(" · "+catalog.Age(start)+" ago"))
	}
	if end := catalog.Str(v.rec, "event.end"); end != "" {
		fact("ended", catalog.FormatTime(end))
	}
	fact("flags", catalog.ProblemFlags(v.rec))
	fact("k8s", joinK8s(v.rec))

	if len(v.affected) > 0 {
		v.lines = append(v.lines, "", theme.Section(fmt.Sprintf("impact — %d affected", len(v.affected))))
		for _, e := range v.affected {
			v.rows = append(v.rows, len(v.lines))
			line := "  " + entityName(e) + "  " + theme.Badge.Render(e.Type)
			if e.Name != "" { // unnamed entities already show the id as name
				line += theme.Dim.Render("  " + e.ID)
			}
			v.lines = append(v.lines, line)
		}
	}

	if desc := catalog.Str(v.rec, "event.description"); desc != "" {
		v.lines = append(v.lines, "", theme.Section("davis says"))
		for _, l := range strings.Split(wrap(desc, max(v.width-2, 20)), "\n") {
			v.lines = append(v.lines, " "+l)
		}
	}
	v.clampOffset()
}

// joinK8s summarizes the problem's Kubernetes context in one line.
func joinK8s(rec map[string]any) string {
	var parts []string
	if c := catalog.StrFirst(rec, "k8s.cluster.name"); c != "" {
		parts = append(parts, c)
	}
	if ns := catalog.StrFirst(rec, "k8s.namespace.name"); ns != "" {
		parts = append(parts, ns)
	}
	if w := catalog.StrFirst(rec, "k8s.workload.name"); w != "" {
		kind := strings.ToLower(catalog.StrFirst(rec, "k8s.workload.kind"))
		if kind != "" {
			w = kind + " " + w
		}
		parts = append(parts, w)
	}
	return strings.Join(parts, " / ")
}

func (v *problemOverview) View(width, height int) string {
	if width != v.width || height != v.height {
		v.width, v.height = width, height
		v.rebuild()
		v.ensureVisible()
	}
	end := min(v.offset+height, len(v.lines))
	out := make([]string, 0, height)
	selLine := -1
	if v.cursor >= 0 && v.cursor < len(v.rows) {
		selLine = v.rows[v.cursor]
	}
	for i := v.offset; i < end; i++ {
		line := v.lines[i]
		if i == selLine {
			line = theme.Gutter.Render("▌") + theme.Selected.Render(pad(ansi.Strip(line), width-1))
		}
		out = append(out, ansi.Truncate(line, width, "…"))
	}
	return strings.Join(out, "\n")
}
