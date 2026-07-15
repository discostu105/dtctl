package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
	"github.com/dynatrace-oss/dynatui/internal/tui/theme"
)

// helpOverlay is the '?' key reference; any key dismisses it.
type helpOverlay struct {
	app *app
}

func (a *app) openHelp() tea.Cmd {
	a.overlay = &helpOverlay{app: a}
	return nil
}

func (h *helpOverlay) Hints() []keyHint { return nil }

func (h *helpOverlay) HandleKey(tea.KeyMsg) tea.Cmd {
	h.app.overlay = nil
	return nil
}

func (h *helpOverlay) View(width, height int) string {
	sections := []struct {
		title string
		keys  []keyHint
	}{
		{"Navigation", []keyHint{
			{":", "command bar — fuzzy view names, args filter (:pods checkout, :trace <id>, :nav <type|id|name>, :ctx [name] — bare :ctx opens a picker)"},
			{"enter", "detail / drill into children / follow entity link / expand value / waterfall / session timeline"},
			{"0-9", "global bookmarks: 0 home · 1 problems · 2 services · 3 hosts · 4 pods · 5 logs · 6 traces · 7 workloads · 8 events · 9 aws — on an entered page 1-9 address the innermost numbered strip: its tabs, or the active tab's lens strip when it shows one (0 still jumps home, esc restores all bookmarks)"},
			{"esc / -", "back / toggle last two views"},
			{"tab", "cycle the view's primary strip: lens strip on tables · tabs on detail pages · panels on home"},
			{"[ / ]", "cycle the lens strip explicitly (the only way for a strip nested inside a detail tab)"},
			{"P", "preview pane on/off — the peek at the selected row is on by default and auto-hides on cramped terminals"},
			{"H", "history — restore a previous page (survives restarts)"},
			{"/", "filter table (live) — enter adds it as a server-side search, alt+enter replaces"},
			{"f / F", "facet manager: add attribute=value filters (fieldsSummary top values, * patterns), edit/remove each / clear all"},
			{"J/K", "sort column/direction"},
			{"j/k ↑/↓ g/G", "move"},
			{"pgup/pgdn", "page jump (ctrl+d/u half page in inspectors)"},
		}},
		{"Drill-down (pre-scoped to selection; on detail pages the letters jump to the matching tab)", []keyHint{
			{"l", "logs"},
			{"s", "traces (spans) / jump to a log's or RUM event's trace"},
			{"m", "metrics — canned charts, or the metric explorer for other types"},
			{"p", "problems"},
			{"v", "events"},
			{"a", "log patterns — Davis clustering of the current logs (enter: records with the pattern's fields parsed out)"},
			{"u", "sessions of a frontend / session timeline of a RUM event"},
			{"e", "user events of a frontend / of a session"},
			{"x / X", "smartscape navigator — walk the topology from the selection (:nav); one-hop relations live on the detail page's related tab"},
			{"d", "describe / details"},
		}},
		{"Scope & actions", []keyHint{
			{".", "pin selection as global scope (ctrl+x unpins)"},
			{"t", "timeframe picker — presets or a custom relative window (45m, 12h, 3d)"},
			{"S", "segments — up to 10 filter segments applied to every DQL view (:segments); v picks variable values; a .dynatrace.yaml in the project pre-selects them"},
			{"alt+s", "segments on/off — suspend the applied set for the unfiltered picture, restore it with bindings intact"},
			{"ctrl+q", "reveal query — this view's DQL in the editor"},
			{"o", "open in the Dynatrace UI — a picker appears when several targets apply"},
			{"y / c", "yank id / copy CLI command"},
		}},
		{"Global", []keyHint{
			{"r / R", "refresh / cycle auto-refresh"},
			{"?", "help"},
			{"q", "quit"},
		}},
	}
	var b strings.Builder
	b.WriteString(theme.OverlayTitle.Render("dtctl tui — keys") + "\n")
	for _, s := range sections {
		b.WriteString("\n" + theme.Section(s.title) + "\n")
		for _, h := range s.keys {
			b.WriteString(fmt.Sprintf("  %s %s\n",
				theme.KeyHint.Render(fmt.Sprintf("%-12s", h.Key)), h.Desc))
		}
	}
	b.WriteString("\n" + theme.Dim.Render(wrap("views: home · query · nav · "+strings.Join(catalog.Names(), " · "), 76)))
	return centerOverlay(width, height, b.String())
}
