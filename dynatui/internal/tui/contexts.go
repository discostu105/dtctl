package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dynatui/internal/tui/theme"
)

// contextPicker is the single-select overlay behind `:ctx` without a name
// (mirrors the timeframe picker's shape): it lists the configured dtctl
// contexts, marks the one the session is on, and switches to the chosen one
// through the same session-local switchContext path as `:ctx <name>`. Typing
// a name still switches directly — the picker is for when you don't remember
// it.
type contextPicker struct {
	app *app
	sel int
}

// openContextPicker opens the switcher, seeding the highlight on the current
// context so enter on it is a harmless no-op. Refuses (with a status) when
// switching isn't wired or nothing is configured — a picker over nothing.
func (a *app) openContextPicker() tea.Cmd {
	if a.opts.SwitchContext == nil {
		return statusErr("context switching is not wired up in this session")
	}
	if len(a.opts.Contexts) == 0 {
		return statusErr(fmt.Sprintf("context %s — no other contexts configured", a.opts.ContextName))
	}
	p := &contextPicker{app: a}
	for i, name := range a.opts.Contexts {
		if name == a.opts.ContextName {
			p.sel = i
			break
		}
	}
	a.overlay = p
	return nil
}

func (p *contextPicker) Hints() []keyHint {
	return []keyHint{{"enter", "switch"}, {"j/k", "move"}, {"esc", "cancel"}}
}

func (p *contextPicker) HandleKey(msg tea.KeyMsg) tea.Cmd {
	a := p.app
	n := len(a.opts.Contexts)
	key := msg.String()
	switch key {
	case "esc":
		a.overlay = nil
		return nil
	case "enter":
		a.overlay = nil
		if p.sel >= n {
			return nil
		}
		return a.switchContext(a.opts.Contexts[p.sel])
	}
	if moveSel(key, &p.sel, n) {
		return nil
	}
	// Digit quick-select mirrors the timeframe picker's 1-9 shortcuts.
	if idx, ok := digitIndex(key, n); ok {
		a.overlay = nil
		return a.switchContext(a.opts.Contexts[idx])
	}
	return nil
}

func (p *contextPicker) View(width, height int) string {
	a := p.app
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render("switch context") + "  " +
		theme.Dim.Render("session-local · resets pin, segments, and view stacks") + "\n\n")
	nameW := 32
	limit := max(height-7, 4)
	overlayRows(&b, len(a.opts.Contexts), p.sel, limit, func(i int) string {
		name := a.opts.Contexts[i]
		row := fmt.Sprintf("%d · %s", i+1, ansi.Truncate(name, nameW, "…"))
		badge := ""
		if name == a.opts.ContextName {
			badge = " " + theme.Badge.Render("current")
		}
		if i == p.sel {
			return theme.Selected.Render(" "+pad(row, nameW+4)) + badge
		}
		return " " + theme.HeaderVal.Render(pad(row, nameW+4)) + badge
	})
	b.WriteString("\n" + theme.Dim.Render("enter switch · j/k move · esc cancel"))
	return centerOverlay(width, height, b.String())
}
