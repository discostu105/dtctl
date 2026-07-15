package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
	"github.com/dynatrace-oss/dynatui/internal/tui/theme"
)

// cmdPalette is the ':' command bar: a fuzzy view finder whose input renders
// in the header line and whose matches render as a picker near the top.
type cmdPalette struct {
	app     *app
	input   textinput.Model
	matches []*catalog.Spec
	sel     int
}

// bespokeSpecs are the command palette's entries for the non-catalog screens,
// so :nav and friends are discoverable by scanning or typing. Display-only
// stubs — deliberately NOT in the catalog registry (Lookup must never hand
// them to newTableView; the enter path routes them by name).
var bespokeSpecs = []*catalog.Spec{
	{Name: "home", Desc: "Triage landing page"},
	{Name: "query", Aliases: []string{"dql"}, Desc: "DQL escape hatch"},
	{Name: "nav", Aliases: []string{"smartscape", "navigator"}, Desc: "Smartscape topology navigator"},
	{Name: "segments", Aliases: []string{"seg"}, Desc: "Filter segments — global DQL scope"},
	{Name: "ctx", Aliases: []string{"context"}, Desc: "Switch dtctl context (session-local)"},
}

// openCmdPalette shows the command bar (':').
func (a *app) openCmdPalette() tea.Cmd {
	ci := newTextInput()
	ci.Prompt = ":"
	ci.PromptStyle = theme.Crumb
	ci.CharLimit = 64
	ci.Focus()
	p := &cmdPalette{app: a, input: ci}
	p.updateMatches()
	a.overlay = p
	return textinput.Blink
}

func (p *cmdPalette) Hints() []keyHint {
	return []keyHint{{"enter", "open"}, {"tab", "next match"}, {"esc", "cancel"}}
}

func (p *cmdPalette) HandleKey(msg tea.KeyMsg) tea.Cmd {
	a := p.app
	switch msg.String() {
	case "esc":
		a.overlay = nil
		return nil
	case "tab", "down":
		if len(p.matches) > 0 {
			p.sel = (p.sel + 1) % len(p.matches)
		}
		return nil
	case "shift+tab", "up":
		if len(p.matches) > 0 {
			p.sel = (p.sel + len(p.matches) - 1) % len(p.matches)
		}
		return nil
	case "enter":
		a.overlay = nil
		input := strings.Fields(strings.TrimSpace(p.input.Value()))
		if len(input) == 0 {
			// Empty input still has a highlighted suggestion (tab/down cycles
			// it) — enter opens it rather than silently dropping the choice.
			if p.sel < len(p.matches) {
				return a.jumpTo(p.matches[p.sel].Name, "")
			}
			return nil
		}
		arg := strings.Join(input[1:], " ")
		switch input[0] {
		case "q", "quit":
			return a.quit()
		case "help":
			return a.openHelp()
		case "history", "hist":
			return a.openHistory()
		case "home", "query", "dql":
			return a.jumpTo(input[0], "")
		case "nav", "smartscape", "navigator":
			return a.openNav(arg)
		case "trace":
			if arg == "" {
				return statusErr("usage: trace <trace-id>")
			}
			return func() tea.Msg { return waterfallMsg{traceID: arg} }
		case "segments", "seg":
			return a.openSegmentPicker()
		case "ctx", "context":
			return a.switchContext(arg)
		}
		spec := catalog.Lookup(input[0])
		if spec == nil && p.sel < len(p.matches) {
			spec = p.matches[p.sel]
		}
		if spec == nil {
			return statusErr(fmt.Sprintf("unknown view %q", input[0]))
		}
		// A highlighted bespoke entry (partial input like ":na") routes like
		// its exact-name special above — never into newTableView.
		switch spec.Name {
		case "home", "query":
			return a.jumpTo(spec.Name, "")
		case "nav":
			return a.openNav(arg)
		case "segments":
			return a.openSegmentPicker()
		case "ctx":
			return a.switchContext(arg)
		}
		// Arguments narrow the jump (":pods checkout" pre-fills the filter).
		return a.jumpTo(spec.Name, arg)
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	p.updateMatches()
	return cmd
}

func (p *cmdPalette) updateMatches() {
	first := ""
	if fields := strings.Fields(p.input.Value()); len(fields) > 0 {
		first = fields[0]
	}
	candidates := append(append([]*catalog.Spec{}, bespokeSpecs...), catalog.All()...)
	p.matches = catalog.MatchSpecs(candidates, first)
	p.sel = 0
}

// View renders the matches as a picker near the top (the input itself lives
// in the header line): view name, aliases, and description, with the current
// suggestion highlighted.
func (p *cmdPalette) View(width, height int) string {
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render("views") + "\n\n")
	nameW, aliasW, descW := 14, 12, 36
	rowW := nameW + aliasW + descW + 3
	limit := len(p.matches)
	if m := max(height-8, 4); limit > m {
		limit = m
	}
	for i := 0; i < limit; i++ {
		s := p.matches[i]
		row := pad(s.Name, nameW) + " " + pad(strings.Join(s.Aliases, " "), aliasW) + " " + pad(s.Desc, descW)
		if i == p.sel {
			b.WriteString(theme.Selected.Render(pad(" "+row, rowW)))
		} else {
			b.WriteString(" " + theme.HeaderVal.Render(pad(s.Name, nameW)) + " " +
				theme.Dim.Render(pad(strings.Join(s.Aliases, " "), aliasW)) + " " +
				theme.CrumbDim.Render(pad(s.Desc, descW)))
		}
		b.WriteString("\n")
	}
	if len(p.matches) == 0 {
		b.WriteString(theme.Dim.Render(" no matching view") + "\n")
	}
	if rest := len(p.matches) - limit; rest > 0 {
		b.WriteString(theme.Dim.Render(fmt.Sprintf(" … %d more", rest)) + "\n")
	}
	b.WriteString("\n" + theme.Dim.Render("tab next · enter open · also :history :trace <id>"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Position(0.2),
		theme.OverlayBox.Render(b.String()))
}
