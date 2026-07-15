package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// tickDelay scales every timer command the TUI arms. Tests zero it: the
// harness executes commands synchronously, so a real tea.Tick would sleep
// its full interval on the test goroutine.
var tickDelay = func(d time.Duration) time.Duration { return d }

// tick is how the TUI arms timers — do not call tea.Tick directly, or the
// test time scale won't apply.
func tick(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
	return tea.Tick(tickDelay(d), fn)
}
