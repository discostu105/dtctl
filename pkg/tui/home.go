package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

	width, height int
}

type homePanel struct {
	title   string
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
				return pushViewMsg{spec: catalog.Lookup("problems"),
					scope: catalog.Scope{Timeframe: tf}, filter: catalog.Str(rec, "display_id")}
			},
		},
		{
			title: "failing services",
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
	var cmds []tea.Cmd
	for i, p := range v.panels {
		p.loading = true
		p.err = nil
		p.dql = p.query(v.tf)
		cmds = append(cmds, v.ds.query(panelOwner{v: v, idx: i}, i, p.dql))
	}
	return tea.Batch(cmds...)
}

func (v *homeView) SetTimeframe(tf catalog.Timeframe) tea.Cmd {
	v.tf = tf
	return v.Refresh()
}

func (v *homeView) InputActive() bool { return false }
func (v *homeView) Crumb() string     { return "home" }

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
		if !ok || po.v != v || po.idx >= len(v.panels) {
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
	panelH := height/rows - 1
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

// renderPanel draws one panel at a fixed size.
func (v *homeView) renderPanel(idx int, p *homePanel, width, height int) string {
	title := p.title
	if idx == v.focus {
		title = theme.OverlayTitle.Render("▸ " + title)
	} else {
		title = theme.GroupTitle.Render("  " + title)
	}
	lines := []string{ansi.Truncate(title, width, "…")}

	switch {
	case p.loading:
		lines = append(lines, theme.Spinner.Render("  ⟳ loading…"))
	case p.err != nil:
		lines = append(lines, theme.Error.Render(ansi.Truncate("  "+p.err.Error(), width, "…")))
	case len(p.records) == 0:
		lines = append(lines, theme.StatusOK.Render("  ✓ nothing to report"))
	default:
		for li, rec := range p.records {
			if len(lines) >= height {
				break
			}
			text, class := p.line(rec)
			text = ansi.Truncate("  "+text, width, "…")
			if idx == v.focus && li == v.cursor {
				text = theme.Selected.Render(pad(text, width))
			} else if class != "" {
				text = theme.Class(class, text)
			}
			lines = append(lines, text)
		}
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines[:height], "\n")
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
