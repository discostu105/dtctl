package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
	"github.com/dynatrace-oss/dynatui/internal/tui/theme"
)

// timeframePicker is the 't' overlay: preset pills plus a custom entry whose
// text input takes any relative window ("45m", "12h", "3d").
type timeframePicker struct {
	app    *app
	sel    int // picker highlight; len(Timeframes) = the custom entry
	custom bool
	input  textinput.Model
}

func (a *app) openTimeframePicker() tea.Cmd {
	ti := newTextInput()
	ti.Prompt = "last "
	ti.PromptStyle = theme.Crumb
	ti.Placeholder = "45m · 12h · 3d"
	ti.CharLimit = 8
	p := &timeframePicker{app: a, input: ti}
	// A window typed via the custom entry is not a preset — land the
	// highlight on custom so the picker reflects what is applied.
	if i, preset := presetIndex(a.tf.Label); preset {
		p.sel = i
	} else {
		p.sel = len(catalog.Timeframes)
	}
	a.overlay = p
	return nil
}

func (p *timeframePicker) Hints() []keyHint { return nil }

func (p *timeframePicker) HandleKey(msg tea.KeyMsg) tea.Cmd {
	a := p.app
	// The custom entry: a focused text input for any relative window.
	if p.custom {
		switch msg.String() {
		case "esc":
			p.custom = false
			p.input.Blur()
			return nil
		case "enter":
			label := strings.TrimSpace(p.input.Value())
			tf, ok := catalog.ParseTimeframe(label)
			if !ok {
				return statusErr(fmt.Sprintf("%q is not a relative window (45m, 12h, 3d)", label))
			}
			a.overlay = nil
			return a.setTimeframe(tf)
		}
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(msg)
		return cmd
	}

	entries := len(catalog.Timeframes) + 1 // presets + the custom entry
	openCustom := func() tea.Cmd {
		p.sel = len(catalog.Timeframes)
		p.custom = true
		p.input.SetValue("")
		p.input.Focus()
		return textinput.Blink
	}
	switch msg.String() {
	case "esc", "t":
		a.overlay = nil
		return nil
	case "left", "h", "up", "k":
		p.sel = (p.sel + entries - 1) % entries
		return nil
	case "right", "l", "down", "j", "tab":
		p.sel = (p.sel + 1) % entries
		return nil
	case "enter":
		if p.sel >= len(catalog.Timeframes) {
			return openCustom()
		}
		a.overlay = nil
		return a.setTimeframe(catalog.Timeframes[p.sel])
	}
	if idx, ok := digitIndex(msg.String(), entries); ok {
		if idx == len(catalog.Timeframes) {
			return openCustom()
		}
		a.overlay = nil
		return a.setTimeframe(catalog.Timeframes[idx])
	}
	return nil
}

func (p *timeframePicker) View(width, height int) string {
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render("timeframe") + "\n\n")
	custom := "custom"
	if _, preset := presetIndex(p.app.tf.Label); !preset {
		custom = "custom (" + p.app.tf.Label + ")" // the applied window when it isn't a preset
	}
	labels := make([]string, 0, len(catalog.Timeframes)+1)
	for _, tf := range catalog.Timeframes {
		labels = append(labels, tf.Label)
	}
	labels = append(labels, custom)
	pills := make([]string, len(labels))
	for i, l := range labels {
		label := fmt.Sprintf("%d · %s", i+1, l)
		if i == p.sel {
			pills[i] = theme.TabActive.Render(label)
		} else {
			pills[i] = theme.TabInactive.Render(label)
		}
	}
	b.WriteString(strings.Join(pills, " ") + "\n")
	if p.custom {
		b.WriteString("\n" + p.input.View() + "\n")
		b.WriteString("\n" + theme.Dim.Render("a relative window: 45m · 12h · 3d — enter apply · esc back"))
		return centerOverlay(width, height, b.String())
	}
	b.WriteString("\n" + theme.Dim.Render(fmt.Sprintf("enter/1-%d apply · esc cancel", len(labels))))
	return centerOverlay(width, height, b.String())
}

// presetIndex finds a timeframe label among the picker presets.
func presetIndex(label string) (int, bool) {
	for i, tf := range catalog.Timeframes {
		if tf.Label == label {
			return i, true
		}
	}
	return 0, false
}
