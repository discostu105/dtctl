package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dynatrace-oss/dynatui/internal/tui/theme"
)

// overlay is a modal that owns the whole keyboard while it is open. The app
// shows at most one (app.overlay); opening another replaces it, and setting
// app.overlay to nil closes it. Overlays place themselves — most center via
// centerOverlay.
type overlay interface {
	// HandleKey consumes one key press (the overlay owns the keyboard).
	HandleKey(msg tea.KeyMsg) tea.Cmd
	// View renders the overlay over a body of the given size.
	View(width, height int) string
	// Hints lists the footer key bindings while the overlay is open; nil
	// keeps the covered view's default footer.
	Hints() []keyHint
}

// centerOverlay places boxed overlay content mid-screen.
func centerOverlay(width, height int, content string) string {
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center,
		theme.OverlayBox.Render(content))
}

// moveSel applies the shared vertical-picker cursor keys to sel over a list
// of n entries, reporting whether the key was one of them.
func moveSel(key string, sel *int, n int) bool {
	switch key {
	case "up", "k":
		if *sel > 0 {
			*sel--
		}
	case "down", "j", "tab":
		if *sel < n-1 {
			*sel++
		}
	default:
		return false
	}
	return true
}

// digitIndex maps a 1-9 quick-select key onto a list of n entries.
func digitIndex(key string, n int) (int, bool) {
	if len(key) != 1 {
		return 0, false
	}
	idx := strings.IndexByte("123456789", key[0])
	if idx < 0 || idx >= n {
		return 0, false
	}
	return idx, true
}

// overlayRows writes a sel-anchored window of limit rows (of n total)
// through row(i), appending an "… N more" line when the window clips.
func overlayRows(b *strings.Builder, n, sel, limit int, row func(i int) string) {
	start := 0
	if sel >= limit {
		start = sel - limit + 1
	}
	for i := start; i < n && i < start+limit; i++ {
		b.WriteString(row(i))
		b.WriteString("\n")
	}
	if rest := n - start - limit; rest > 0 {
		b.WriteString(theme.Dim.Render(fmt.Sprintf(" … %d more", rest)) + "\n")
	}
}
