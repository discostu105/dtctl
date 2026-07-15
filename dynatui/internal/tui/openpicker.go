package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dynatui/internal/tui/theme"
)

// openPicker is the "open with" overlay ('o' with several browser targets).
type openPicker struct {
	app  *app
	list []linkOption
	sel  int
}

// openSelection deep-links the selection into the Dynatrace UI. One target
// opens directly; several open the "open with" picker (record app, carried
// URLs, trace, entity app, topology, query as notebook).
func (a *app) openSelection() tea.Cmd {
	rec, entity := a.selection()
	traceID := ""
	if tp, ok := a.top().(traceProvider); ok {
		traceID = tp.TraceID()
	}
	dql := ""
	if p, ok := a.top().(dqlProvider); ok {
		dql = p.DQL()
	}
	opts := linkOptionsFor(a.opts.Environment, rec, entity, traceID, dql)
	if len(opts) == 0 {
		return statusErr("nothing to open here")
	}
	if len(opts) == 1 {
		return a.openLink(opts[0])
	}
	a.overlay = &openPicker{app: a, list: opts}
	return nil
}

// openLink launches the browser on one picker target.
func (a *app) openLink(o linkOption) tea.Cmd {
	if err := openBrowser(o.URL); err != nil {
		return statusErr("browser: " + err.Error())
	}
	return status("opened " + o.Label)
}

func (p *openPicker) Hints() []keyHint {
	return []keyHint{{"enter", "open"}, {"y", "yank url"}, {"esc", "cancel"}}
}

func (p *openPicker) HandleKey(msg tea.KeyMsg) tea.Cmd {
	a := p.app
	key := msg.String()
	switch key {
	case "esc", "o":
		a.overlay = nil
		return nil
	case "y":
		if p.sel < len(p.list) {
			yank(p.list[p.sel].URL)
			a.overlay = nil
			return status("copied url — " + p.list[p.sel].Label)
		}
		return nil
	case "enter":
		a.overlay = nil
		if p.sel < len(p.list) {
			return a.openLink(p.list[p.sel])
		}
		return nil
	}
	if moveSel(key, &p.sel, len(p.list)) {
		return nil
	}
	if idx, ok := digitIndex(key, len(p.list)); ok {
		a.overlay = nil
		return a.openLink(p.list[idx])
	}
	return nil
}

func (p *openPicker) View(width, height int) string {
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render("open with") + "\n\n")
	for i, o := range p.list {
		label := fmt.Sprintf("%d · %s", i+1, o.Label)
		if i == p.sel {
			b.WriteString(theme.Selected.Render(pad(" "+label, 44)))
		} else {
			b.WriteString(" " + theme.HeaderVal.Render(pad(label, 43)))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n" + theme.Dim.Render("enter/1-9 open · y yank url · esc cancel"))
	return centerOverlay(width, height, b.String())
}
