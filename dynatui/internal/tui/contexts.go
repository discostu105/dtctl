package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dynatui/internal/tui/theme"
)

// The context picker is the single-select overlay behind `:ctx` without a name
// (mirrors the timeframe picker's shape): it lists the configured dtctl
// contexts, marks the one the session is on, and switches to the chosen one
// through the same session-local switchContext path as `:ctx <name>`. Typing a
// name still switches directly — the picker is for when you don't remember it.

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
	a.ctxPickActive = true
	a.ctxPickSel = 0
	for i, name := range a.opts.Contexts {
		if name == a.opts.ContextName {
			a.ctxPickSel = i
			break
		}
	}
	return nil
}

func (a *app) updateCtxPicker(msg tea.KeyMsg) tea.Cmd {
	n := len(a.opts.Contexts)
	switch msg.String() {
	case "esc":
		a.ctxPickActive = false
		return nil
	case "up", "k":
		if a.ctxPickSel > 0 {
			a.ctxPickSel--
		}
		return nil
	case "down", "j", "tab":
		if a.ctxPickSel < n-1 {
			a.ctxPickSel++
		}
		return nil
	case "enter":
		a.ctxPickActive = false
		if a.ctxPickSel >= n {
			return nil
		}
		return a.switchContext(a.opts.Contexts[a.ctxPickSel])
	}
	// Digit quick-select mirrors the timeframe picker's 1-9 shortcuts.
	if s := msg.String(); len(s) == 1 {
		if idx := strings.IndexByte("123456789", s[0]); idx >= 0 && idx < n {
			a.ctxPickActive = false
			a.ctxPickSel = idx
			return a.switchContext(a.opts.Contexts[idx])
		}
	}
	return nil
}

func (a *app) renderCtxPicker() string {
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render("switch context") + "  " +
		theme.Dim.Render("session-local · resets pin, segments, and view stacks") + "\n\n")
	nameW := 32
	limit := max(a.bodyHeight()-7, 4)
	start := 0
	if a.ctxPickSel >= limit {
		start = a.ctxPickSel - limit + 1
	}
	for i := start; i < len(a.opts.Contexts) && i < start+limit; i++ {
		name := a.opts.Contexts[i]
		row := fmt.Sprintf("%d · %s", i+1, ansi.Truncate(name, nameW, "…"))
		badge := ""
		if name == a.opts.ContextName {
			badge = " " + theme.Badge.Render("current")
		}
		if i == a.ctxPickSel {
			b.WriteString(theme.Selected.Render(" "+pad(row, nameW+4)) + badge)
		} else {
			b.WriteString(" " + theme.HeaderVal.Render(pad(row, nameW+4)) + badge)
		}
		b.WriteString("\n")
	}
	if rest := len(a.opts.Contexts) - start - limit; rest > 0 {
		b.WriteString(theme.Dim.Render(fmt.Sprintf(" … %d more", rest)) + "\n")
	}
	b.WriteString("\n" + theme.Dim.Render("enter switch · j/k move · esc cancel"))
	return b.String()
}
