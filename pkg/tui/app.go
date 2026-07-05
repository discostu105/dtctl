// Package tui implements the interactive terminal UI behind `dtctl tui` — a
// k9s-style navigator over observability primitives (see docs/dev/TUI_DESIGN.md).
// It is a presentation layer over pkg/exec and the catalog's DQL templates;
// nothing outside cmd/tui.go imports it.
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtctl/pkg/exec"
	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
	"github.com/dynatrace-oss/dtctl/pkg/tui/theme"
)

// Options wires the TUI to the current dtctl context.
type Options struct {
	ContextName string
	Environment string
	SafetyLevel string
	Executor    *exec.DQLExecutor
	InitialView string // catalog view name or alias; "" = problems
}

// Run launches the TUI and blocks until the user quits.
func Run(opts Options) error {
	app, err := newApp(opts)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(app, tea.WithAltScreen()).Run()
	return err
}

// refreshIntervals are the R-key auto-refresh cycle (0 = off).
var refreshIntervals = []time.Duration{0, 10 * time.Second, 30 * time.Second, 60 * time.Second}

type app struct {
	opts Options
	ds   *dataSource
	tf   catalog.Timeframe

	width, height int

	stack []viewModel
	prev  []viewModel // '-' toggles between the two most recent stacks

	// Overlays.
	cmdActive  bool
	cmdInput   textinput.Model
	cmdMatches []*catalog.Spec
	cmdSel     int
	helpActive bool
	tfActive   bool
	tfSel      int

	status    string
	statusErr bool

	refreshIdx int
	refreshGen int
}

type refreshTickMsg struct{ gen int }

func newApp(opts Options) (*app, error) {
	spec := catalog.Lookup(opts.InitialView)
	if spec == nil {
		return nil, fmt.Errorf("unknown view %q (available: %s)", opts.InitialView, strings.Join(catalog.Names(), ", "))
	}
	ci := textinput.New()
	ci.Prompt = ":"
	ci.CharLimit = 64
	a := &app{
		opts: opts,
		ds:   &dataSource{exec: opts.Executor},
		tf:   catalog.DefaultTimeframe,
	}
	a.cmdInput = ci
	for i, tf := range catalog.Timeframes {
		if tf.Label == a.tf.Label {
			a.tfSel = i
		}
	}
	a.stack = []viewModel{newTableView(a.ds, spec, catalog.Scope{Timeframe: a.tf})}
	return a, nil
}

func (a *app) top() viewModel { return a.stack[len(a.stack)-1] }

func (a *app) Init() tea.Cmd { return a.top().Init() }

func (a *app) bodyHeight() int { return max(a.height-4, 1) }

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		return a, a.broadcast(bodySizeMsg{width: a.width, height: a.bodyHeight()})

	case tea.KeyMsg:
		// Any key clears the transient status line.
		a.status = ""
		return a, a.handleKey(msg)

	case dataMsg:
		// Deliver to the issuing view wherever it sits on either stack, so a
		// parent still loading below a drill-down completes too.
		for _, v := range append(append([]viewModel{}, a.stack...), a.prev...) {
			if any(v) == msg.owner {
				return a, v.Update(msg)
			}
		}
		return a, nil

	case pushViewMsg:
		view := newTableView(a.ds, msg.spec, msg.scope)
		return a, a.navigate(view, msg.replace)

	case inspectMsg:
		return a, a.navigate(newInspectorView(msg.title, msg.rec), false)

	case metricsMsg:
		return a, a.navigate(newMetricsView(a.ds, msg.entity, a.tf), false)

	case statusMsg:
		a.status, a.statusErr = msg.text, msg.isErr
		return a, nil

	case refreshTickMsg:
		if msg.gen != a.refreshGen || refreshIntervals[a.refreshIdx] == 0 {
			return a, nil
		}
		return a, tea.Batch(a.top().Refresh(), a.scheduleRefresh())
	}

	return a, a.top().Update(msg)
}

// navigate pushes a view (or replaces the stack for command-bar jumps),
// remembering the previous stack for the '-' toggle.
func (a *app) navigate(view viewModel, replace bool) tea.Cmd {
	a.prev = a.stack
	if replace {
		a.stack = []viewModel{view}
	} else {
		a.stack = append(append([]viewModel{}, a.stack...), view)
	}
	return tea.Batch(
		view.Update(bodySizeMsg{width: a.width, height: a.bodyHeight()}),
		view.Init(),
	)
}

func (a *app) handleKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()

	if key == "ctrl+c" {
		return tea.Quit
	}

	if a.cmdActive {
		return a.updateCmdbar(msg)
	}
	if a.tfActive {
		return a.updateTfPicker(msg)
	}
	if a.helpActive {
		a.helpActive = false
		return nil
	}

	top := a.top()
	if !top.InputActive() {
		switch key {
		case "q":
			return tea.Quit
		case ":":
			a.cmdActive = true
			a.cmdInput.SetValue("")
			a.cmdInput.Focus()
			a.updateCmdMatches()
			return textinput.Blink
		case "?":
			a.helpActive = true
			return nil
		case "t":
			a.tfActive = true
			return nil
		case "r":
			return top.Refresh()
		case "R":
			a.refreshIdx = (a.refreshIdx + 1) % len(refreshIntervals)
			a.refreshGen++
			if refreshIntervals[a.refreshIdx] == 0 {
				return status("auto-refresh off")
			}
			return tea.Batch(
				status(fmt.Sprintf("auto-refresh every %s", refreshIntervals[a.refreshIdx])),
				a.scheduleRefresh(),
			)
		case "-":
			if a.prev != nil {
				a.stack, a.prev = a.prev, a.stack
				return a.top().Update(bodySizeMsg{width: a.width, height: a.bodyHeight()})
			}
			return nil
		case "esc":
			// Give the view first claim on esc (clearing an active filter);
			// tableView returns nil when it has nothing to clear.
			if cmd := top.Update(msg); cmd != nil {
				return cmd
			}
			if len(a.stack) > 1 {
				a.prev = a.stack
				a.stack = a.stack[:len(a.stack)-1]
			}
			return nil
		}
	}
	return top.Update(msg)
}

func (a *app) scheduleRefresh() tea.Cmd {
	gen := a.refreshGen
	return tea.Tick(refreshIntervals[a.refreshIdx], func(time.Time) tea.Msg {
		return refreshTickMsg{gen: gen}
	})
}

func (a *app) broadcast(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for _, v := range a.stack {
		if cmd := v.Update(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// --- command bar --------------------------------------------------------------

func (a *app) updateCmdbar(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		a.cmdActive = false
		a.cmdInput.Blur()
		return nil
	case "tab", "down":
		if len(a.cmdMatches) > 0 {
			a.cmdSel = (a.cmdSel + 1) % len(a.cmdMatches)
		}
		return nil
	case "shift+tab", "up":
		if len(a.cmdMatches) > 0 {
			a.cmdSel = (a.cmdSel + len(a.cmdMatches) - 1) % len(a.cmdMatches)
		}
		return nil
	case "enter":
		a.cmdActive = false
		a.cmdInput.Blur()
		input := strings.Fields(strings.TrimSpace(a.cmdInput.Value()))
		if len(input) == 0 {
			return nil
		}
		switch input[0] {
		case "q", "quit":
			return tea.Quit
		case "help":
			a.helpActive = true
			return nil
		}
		spec := catalog.Lookup(input[0])
		if spec == nil && a.cmdSel < len(a.cmdMatches) {
			spec = a.cmdMatches[a.cmdSel]
		}
		if spec == nil {
			return statusErr(fmt.Sprintf("unknown view %q", input[0]))
		}
		scope := catalog.Scope{Timeframe: a.tf}
		return func() tea.Msg { return pushViewMsg{spec: spec, scope: scope, replace: true} }
	}
	var cmd tea.Cmd
	a.cmdInput, cmd = a.cmdInput.Update(msg)
	a.updateCmdMatches()
	return cmd
}

func (a *app) updateCmdMatches() {
	first := ""
	if fields := strings.Fields(a.cmdInput.Value()); len(fields) > 0 {
		first = fields[0]
	}
	a.cmdMatches = catalog.Match(first)
	a.cmdSel = 0
}

// --- timeframe picker ---------------------------------------------------------

func (a *app) updateTfPicker(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "t":
		a.tfActive = false
		return nil
	case "left", "h", "up", "k":
		a.tfSel = (a.tfSel + len(catalog.Timeframes) - 1) % len(catalog.Timeframes)
		return nil
	case "right", "l", "down", "j", "tab":
		a.tfSel = (a.tfSel + 1) % len(catalog.Timeframes)
		return nil
	case "enter":
		a.tfActive = false
		a.tf = catalog.Timeframes[a.tfSel]
		return a.top().SetTimeframe(a.tf)
	}
	if idx := strings.IndexByte("1234", msg.String()[0]); idx >= 0 && idx < len(catalog.Timeframes) && len(msg.String()) == 1 {
		a.tfActive = false
		a.tfSel = idx
		a.tf = catalog.Timeframes[idx]
		return a.top().SetTimeframe(a.tf)
	}
	return nil
}

// --- rendering ------------------------------------------------------------------

func (a *app) View() string {
	if a.width == 0 {
		return "loading…"
	}

	header := a.renderHeader()
	body := a.renderBody()
	footer := a.renderFooter()

	// Pin the footer to the bottom row.
	bodyH := a.bodyHeight()
	body = lipgloss.NewStyle().MaxHeight(bodyH).Render(body)
	if pad := bodyH - lipgloss.Height(body); pad > 0 {
		body += strings.Repeat("\n", pad)
	}
	return header + "\n" + body + "\n" + footer
}

func (a *app) renderHeader() string {
	sep := theme.HeaderKey.Render(" ─ ")
	parts := []string{
		theme.AppName.Render(" dtctl"),
		theme.HeaderKey.Render("ctx:") + theme.HeaderVal.Render(a.opts.ContextName),
		theme.HeaderKey.Render("safety:") + theme.Safety(a.opts.SafetyLevel).Render(a.opts.SafetyLevel),
		theme.HeaderKey.Render("last ") + theme.HeaderVal.Render(a.tf.Label),
	}
	if iv := refreshIntervals[a.refreshIdx]; iv > 0 {
		parts = append(parts, theme.HeaderKey.Render("⟳ ")+theme.HeaderVal.Render(iv.String()))
	}
	line1 := ansi.Truncate(strings.Join(parts, sep), a.width, "…")

	var line2 string
	if a.cmdActive {
		line2 = " " + a.cmdInput.View() + "  " + a.renderCmdMatches()
	} else {
		crumbs := make([]string, len(a.stack))
		for i, v := range a.stack {
			label := v.Crumb()
			if i == len(a.stack)-1 {
				crumbs[i] = theme.Crumb.Render(label)
			} else {
				crumbs[i] = theme.CrumbDim.Render(label)
			}
		}
		line2 = " ▸ " + strings.Join(crumbs, theme.CrumbDim.Render(" ▸ "))
	}
	return line1 + "\n" + ansi.Truncate(line2, a.width, "…")
}

func (a *app) renderCmdMatches() string {
	var parts []string
	for i, s := range a.cmdMatches {
		label := s.Name
		if i == a.cmdSel {
			parts = append(parts, theme.Selected.Render(label))
		} else {
			parts = append(parts, theme.Dim.Render(label))
		}
	}
	return strings.Join(parts, " ")
}

func (a *app) renderBody() string {
	bodyH := a.bodyHeight()
	if a.helpActive {
		return overlay(a.width, bodyH, a.renderHelp())
	}
	if a.tfActive {
		return overlay(a.width, bodyH, a.renderTfPicker())
	}
	return a.top().View(a.width, bodyH)
}

func (a *app) renderFooter() string {
	hints := a.top().Hints()
	hints = append(hints,
		keyHint{":", "views"}, keyHint{"t", "timeframe"}, keyHint{"r", "refresh"},
		keyHint{"esc", "back"}, keyHint{"?", "help"}, keyHint{"q", "quit"},
	)
	var parts []string
	for _, h := range hints {
		parts = append(parts, theme.KeyHint.Render("<"+h.Key+">")+theme.KeyDesc.Render(h.Desc))
	}
	line1 := ansi.Truncate(" "+strings.Join(parts, " "), a.width, "…")

	var line2 string
	switch {
	case a.status != "" && a.statusErr:
		line2 = " " + theme.Error.Render(a.status)
	case a.status != "":
		line2 = " " + theme.StatusOK.Render(a.status)
	default:
		if echo := a.top().Echo(); echo != "" {
			line2 = " " + theme.Echo.Render("≡ "+echo)
		}
	}
	return line1 + "\n" + ansi.Truncate(line2, a.width, "…")
}

func (a *app) renderHelp() string {
	sections := []struct {
		title string
		keys  []keyHint
	}{
		{"Navigation", []keyHint{
			{":", "command bar (view aliases, fuzzy)"},
			{"enter", "inspect selection"},
			{"esc", "back (breadcrumb stack)"},
			{"-", "toggle last two views"},
			{"/", "filter current table"},
			{"j/k ↑/↓ g/G", "move"},
		}},
		{"Drill-down (pre-scoped to selection)", []keyHint{
			{"l", "logs"},
			{"m", "metrics"},
			{"p", "problems"},
			{"v", "events"},
			{"d", "describe record"},
		}},
		{"Global", []keyHint{
			{"t", "timeframe picker"},
			{"r / R", "refresh / cycle auto-refresh"},
			{"?", "help"},
			{"q", "quit"},
		}},
	}
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render("dtctl tui — keys") + "\n")
	for _, s := range sections {
		b.WriteString("\n" + theme.GroupTitle.Render(s.title) + "\n")
		for _, h := range s.keys {
			b.WriteString(fmt.Sprintf("  %s %s\n",
				theme.KeyHint.Render(fmt.Sprintf("%-12s", h.Key)), h.Desc))
		}
	}
	b.WriteString("\n" + theme.Dim.Render("views: "+strings.Join(catalog.Names(), " · ")))
	return b.String()
}

func (a *app) renderTfPicker() string {
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render("timeframe") + "\n\n")
	for i, tf := range catalog.Timeframes {
		label := fmt.Sprintf(" %d  last %-4s ", i+1, tf.Label)
		if i == a.tfSel {
			label = theme.Selected.Render(label)
		}
		b.WriteString(label + "\n")
	}
	b.WriteString("\n" + theme.Dim.Render("enter/1-4 apply · esc cancel"))
	return b.String()
}

func overlay(width, height int, content string) string {
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center,
		theme.OverlayBox.Render(content))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
