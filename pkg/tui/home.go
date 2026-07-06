package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
	"github.com/dynatrace-oss/dtctl/pkg/tui/theme"
)

// homeView is the triage landing page: active problems, failing services,
// Kubernetes warnings, and open vulnerabilities, each loading independently.
// enter on a row jumps into the corresponding full view, pre-filtered.
type homeView struct {
	ds *dataSource
	tf catalog.Timeframe

	panels []*homePanel
	focus  int // panel index owning the cursor
	cursor int // line index within the focused panel
	seq    int // refresh generation — drops stale in-flight results

	width, height int
}

type homePanel struct {
	title   string
	dot     string // pre-styled severity dot shown in the panel title
	query   func(tf catalog.Timeframe) string
	line    func(rec map[string]any) (text, class string)
	action  func(rec map[string]any, tf catalog.Timeframe) tea.Msg
	records []map[string]any
	loading bool
	err     error
	dql     string
}

// panelOwner routes dataMsg results to one panel.
type panelOwner struct {
	v   *homeView
	idx int
}

func newHomeView(ds *dataSource, tf catalog.Timeframe) *homeView {
	v := &homeView{ds: ds, tf: tf}
	v.panels = []*homePanel{
		{
			title: "active problems (24h)",
			dot:   theme.Class("error", "●"),
			query: func(catalog.Timeframe) string {
				return `fetch dt.davis.problems, from:now() - 24h
| filter not(dt.davis.is_duplicate)
| sort timestamp asc
| summarize { status = takeLast(event.status), name = takeLast(event.name), start = takeLast(event.start), affected = takeLast(affected_entity_names) }, by:{display_id}
| filter status == "ACTIVE"
| sort start desc
| limit 8`
			},
			line: func(rec map[string]any) (string, string) {
				affected := ""
				if names, _ := rec["affected"].([]any); len(names) > 0 {
					affected = " · " + catalog.FormatValue(names[0])
					if len(names) > 1 {
						affected += fmt.Sprintf(" +%d", len(names)-1)
					}
				}
				return fmt.Sprintf("%-8s %4s  %s%s",
					catalog.Str(rec, "display_id"), catalog.Age(catalog.Str(rec, "start")),
					catalog.Str(rec, "name"), affected), "error"
			},
			action: func(rec map[string]any, tf catalog.Timeframe) tea.Msg {
				// The panel looks back 24h — the jump must too, or an ACTIVE
				// problem last touched over the shorter window ago filters to
				// an empty "no rows match" state.
				day := catalog.Timeframe{Label: "24h", Dur: 24 * time.Hour}
				if tf.Dur > day.Dur {
					day = tf
				}
				return pushViewMsg{spec: catalog.Lookup("problems"),
					scope: catalog.Scope{Timeframe: day}, filter: catalog.Str(rec, "display_id")}
			},
		},
		{
			title: "failing services",
			dot:   theme.SeriesAt(3).Render("●"),
			query: func(tf catalog.Timeframe) string {
				return fmt.Sprintf(`fetch spans, from:%s
| filter request.is_failed == true and isNotNull(dt.smartscape.service)
| summarize failed = count(), svc = takeFirst(service.name), ns = takeFirst(k8s.namespace.name), by:{dt.smartscape.service}
| sort failed desc
| limit 8`, tf.DQL())
			},
			line: func(rec map[string]any) (string, string) {
				svc := catalog.Str(rec, "svc")
				if ns := catalog.Str(rec, "ns"); ns != "" {
					svc += " (" + ns + ")"
				}
				return fmt.Sprintf("%-42s %6s failed spans", svc, catalog.Str(rec, "failed")), "warn"
			},
			action: func(rec map[string]any, tf catalog.Timeframe) tea.Msg {
				id := catalog.Str(rec, "dt.smartscape.service")
				if id == "" {
					return pushViewMsg{spec: catalog.Lookup("services"), scope: catalog.Scope{Timeframe: tf}}
				}
				entity := catalog.Entity{ID: id, Name: catalog.Str(rec, "svc"), Type: "SERVICE"}
				return detailMsg{entity: entity}
			},
		},
		{
			title: "kubernetes warnings (24h)",
			dot:   theme.Class("warn", "●"),
			query: func(catalog.Timeframe) string {
				return `timeseries events = sum(dt.kubernetes.events, default: 0), by:{k8s.pod.name, k8s.event.reason}, from:now() - 24h, interval: 1h, filter: { k8s.event.type == "Warning" }
| limit 100`
			},
			line: func(rec map[string]any) (string, string) {
				total := 0.0
				for _, p := range catalog.FloatSeries(rec["events"]) {
					total += p
				}
				return fmt.Sprintf("%-24s %5.0f×  %s",
					catalog.Str(rec, "k8s.event.reason"), total, catalog.Str(rec, "k8s.pod.name")), "warn"
			},
			action: func(rec map[string]any, tf catalog.Timeframe) tea.Msg {
				// The panel looks back 24h — the jump must too, and the pod
				// name goes into the query itself: a client-side filter
				// could miss a pod cut from the limit-capped page.
				day := catalog.Timeframe{Label: "24h", Dur: 24 * time.Hour}
				if tf.Dur > day.Dur {
					day = tf
				}
				pod := &catalog.Entity{Name: catalog.Str(rec, "k8s.pod.name"), Type: "K8S_POD"}
				return pushViewMsg{spec: catalog.Lookup("pods"),
					scope: catalog.Scope{Timeframe: day, Entity: pod}}
			},
		},
		{
			title: "open vulnerabilities (24h)",
			dot:   theme.SeriesAt(2).Render("●"),
			query: func(catalog.Timeframe) string {
				return `fetch security.events, from:now() - 24h
| filter event.type == "VULNERABILITY_STATE_REPORT_EVENT"
| sort timestamp asc
| summarize { title = takeLast(vulnerability.title), display_id = takeLast(vulnerability.display_id), level = takeLast(vulnerability.risk.level), score = takeLast(vulnerability.risk.score), status = takeLast(vulnerability.resolution.status) }, by:{vulnerability.id}
| filter status == "OPEN"
| sort score desc
| limit 8`
			},
			line: func(rec map[string]any) (string, string) {
				level := catalog.Str(rec, "level")
				return fmt.Sprintf("%-5s %4s %-8s %s",
					catalog.Str(rec, "display_id"), catalog.FormatValue(rec["score"]), level,
					catalog.Str(rec, "title")), classRisk(level)
			},
			action: func(rec map[string]any, tf catalog.Timeframe) tea.Msg {
				return pushViewMsg{spec: catalog.Lookup("vulnerabilities"),
					scope: catalog.Scope{Timeframe: tf}, filter: catalog.Str(rec, "display_id")}
			},
		},
	}
	return v
}

func classRisk(level string) string {
	switch level {
	case "CRITICAL":
		return "error"
	case "HIGH":
		return "warn"
	}
	return "dim"
}

func (v *homeView) Init() tea.Cmd { return v.Refresh() }

func (v *homeView) Refresh() tea.Cmd {
	v.seq++
	var cmds []tea.Cmd
	for i, p := range v.panels {
		p.loading = true
		p.err = nil
		p.dql = p.query(v.tf)
		// panelOwner.idx routes to the panel; the query seq is the refresh
		// generation, so a slow result from a superseded refresh is dropped.
		cmds = append(cmds, v.ds.query(panelOwner{v: v, idx: i}, v.seq, p.dql))
	}
	return tea.Batch(cmds...)
}

func (v *homeView) SetTimeframe(tf catalog.Timeframe) tea.Cmd {
	v.tf = tf
	return v.Refresh()
}

func (v *homeView) InputActive() bool { return false }
func (v *homeView) Crumb() string     { return "home" }

// Busy reports whether any panel is still loading (animates the spinner).
func (v *homeView) Busy() bool {
	for _, p := range v.panels {
		if p.loading {
			return true
		}
	}
	return false
}

func (v *homeView) DQL() string {
	if v.focus < len(v.panels) {
		return v.panels[v.focus].dql
	}
	return ""
}

func (v *homeView) Echo() string {
	if dql := v.DQL(); dql != "" {
		return fmt.Sprintf("dtctl query '%s'", strings.Join(strings.Fields(strings.ReplaceAll(dql, "\n", " ")), " "))
	}
	return ""
}

func (v *homeView) Hints() []keyHint {
	return []keyHint{{"enter", "open"}, {"tab", "next panel"}, {"j/k", "move"}}
}

func (v *homeView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bodySizeMsg:
		v.width, v.height = msg.width, msg.height
		return nil

	case dataMsg:
		po, ok := msg.owner.(panelOwner)
		if !ok || po.v != v || po.idx >= len(v.panels) || msg.seq != v.seq {
			return nil
		}
		p := v.panels[po.idx]
		p.loading = false
		p.err = msg.err
		if msg.err == nil {
			p.records = msg.records
			if po.idx == 2 { // k8s warnings: order by total occurrences
				sort.SliceStable(p.records, func(i, j int) bool {
					return seriesTotal(p.records[i], "events") > seriesTotal(p.records[j], "events")
				})
				if len(p.records) > 8 {
					p.records = p.records[:8]
				}
			}
		}
		return nil

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

func seriesTotal(rec map[string]any, key string) float64 {
	total := 0.0
	for _, p := range catalog.FloatSeries(rec[key]) {
		total += p
	}
	return total
}

func (v *homeView) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "tab":
		v.focus = (v.focus + 1) % len(v.panels)
		v.cursor = 0
	case "shift+tab":
		v.focus = (v.focus + len(v.panels) - 1) % len(v.panels)
		v.cursor = 0
	case "down", "j":
		v.moveCursor(1)
	case "up", "k":
		v.moveCursor(-1)
	case "enter":
		p := v.panels[v.focus]
		if v.cursor < len(p.records) {
			rec := p.records[v.cursor]
			action, tf := p.action, v.tf
			return func() tea.Msg { return action(rec, tf) }
		}
	}
	return nil
}

// moveCursor walks lines within the focused panel and rolls over to the
// neighboring panel at the edges, so j/k tours the whole page.
func (v *homeView) moveCursor(delta int) {
	v.cursor += delta
	if v.cursor < 0 {
		v.focus = (v.focus + len(v.panels) - 1) % len(v.panels)
		v.cursor = maxInt(len(v.panels[v.focus].records)-1, 0)
		return
	}
	if v.cursor >= len(v.panels[v.focus].records) && v.cursor > 0 {
		v.focus = (v.focus + 1) % len(v.panels)
		v.cursor = 0
	}
}

func (v *homeView) View(width, height int) string {
	v.width, v.height = width, height

	cols := 1
	if width >= 110 {
		cols = 2
	}
	rows := (len(v.panels) + cols - 1) / cols
	panelW := width/cols - 1
	panelH := height / rows
	if panelH < 4 {
		panelH = 4
	}

	rendered := make([]string, len(v.panels))
	for i, p := range v.panels {
		rendered[i] = v.renderPanel(i, p, panelW, panelH)
	}

	var out []string
	for r := 0; r < rows; r++ {
		var line []string
		for c := 0; c < cols; c++ {
			idx := r*cols + c
			if idx < len(rendered) {
				line = append(line, rendered[idx])
			}
		}
		out = append(out, joinHorizontal(line, panelW))
	}
	return strings.Join(out, "\n")
}

// renderPanel draws one panel as a rounded box with the title embedded in the
// top border; the focused panel's border lights up in the accent color.
func (v *homeView) renderPanel(idx int, p *homePanel, width, height int) string {
	border := theme.PanelBlur
	title := theme.PanelTitle2.Render(p.title)
	if idx == v.focus {
		border = theme.PanelFocus
		title = theme.PanelTitle.Render(p.title)
	}
	head := p.dot + " " + title
	if !p.loading && p.err == nil && len(p.records) > 0 {
		head += theme.Dim.Render(fmt.Sprintf(" · %d", len(p.records)))
	}
	head = ansi.Truncate(head, max(width-7, 1), "…")
	fill := width - lipgloss.Width(head) - 5
	if fill < 0 {
		fill = 0
	}
	top := border.Render("╭─") + " " + head + " " + border.Render(strings.Repeat("─", fill)+"╮")

	innerW := width - 4
	var body []string
	switch {
	case p.loading:
		body = append(body, theme.Spinner.Render(theme.Spin()+" loading…"))
	case p.err != nil:
		body = append(body, theme.Error.Render(ansi.Truncate("✗ "+p.err.Error(), innerW, "…")))
	case len(p.records) == 0:
		body = append(body, theme.StatusOK.Render("✓ nothing to report"))
	default:
		for li, rec := range p.records {
			if len(body) >= height-2 {
				break
			}
			text, class := p.line(rec)
			text = ansi.Truncate(text, innerW, "…")
			if idx == v.focus && li == v.cursor {
				text = theme.Selected.Render(pad(text, innerW))
			} else if class != "" {
				text = theme.Class(class, text)
			}
			body = append(body, text)
		}
	}
	for len(body) < height-2 {
		body = append(body, "")
	}

	side := border.Render("│")
	lines := []string{top}
	for _, l := range body[:height-2] {
		lines = append(lines, side+" "+pad(l, innerW)+" "+side)
	}
	lines = append(lines, border.Render("╰"+strings.Repeat("─", max(width-2, 0))+"╯"))
	return strings.Join(lines, "\n")
}

// joinHorizontal places panel blocks side by side.
func joinHorizontal(blocks []string, width int) string {
	if len(blocks) == 1 {
		return blocks[0]
	}
	split := make([][]string, len(blocks))
	maxLines := 0
	for i, b := range blocks {
		split[i] = strings.Split(b, "\n")
		if len(split[i]) > maxLines {
			maxLines = len(split[i])
		}
	}
	var out []string
	for l := 0; l < maxLines; l++ {
		var parts []string
		for i := range split {
			line := ""
			if l < len(split[i]) {
				line = split[i][l]
			}
			parts = append(parts, pad(line, width))
		}
		out = append(out, strings.Join(parts, " "))
	}
	return strings.Join(out, "\n")
}
