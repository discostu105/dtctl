package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
)

// viewModel is the contract every screen on the breadcrumb stack implements.
// Views mutate in place (pointer receivers) and are kept alive when covered,
// so esc restores them with data and cursor intact.
type viewModel interface {
	Init() tea.Cmd
	Update(msg tea.Msg) tea.Cmd
	// View renders the body at the given size (chrome excluded).
	View(width, height int) string
	// Crumb is the breadcrumb label, including scope where relevant.
	Crumb() string
	// Hints lists the key bindings shown in the footer for this view.
	Hints() []keyHint
	// Echo is the CLI equivalent of what the view shows ("" = none).
	Echo() string
	// InputActive reports whether a text input owns the keyboard, which
	// suppresses global single-letter keys like q.
	InputActive() bool
	Refresh() tea.Cmd
	SetTimeframe(tf catalog.Timeframe) tea.Cmd
}

type keyHint struct {
	Key  string
	Desc string
}

// --- navigation messages ------------------------------------------------------

// pushViewMsg opens a catalog view. replace resets the stack (command-bar
// jumps); otherwise the view is pushed as a drill-down.
type pushViewMsg struct {
	spec    *catalog.Spec
	scope   catalog.Scope
	replace bool
}

// inspectMsg opens the record inspector for a selected row.
type inspectMsg struct {
	title string
	rec   map[string]any
}

// metricsMsg opens the canned metrics charts for an entity.
type metricsMsg struct {
	entity catalog.Entity
}

// statusMsg shows a transient message in the footer.
type statusMsg struct {
	text  string
	isErr bool
}

func status(text string) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: text} }
}

func statusErr(text string) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: text, isErr: true} }
}

// bodySizeMsg tells views how much room the body has (chrome excluded).
type bodySizeMsg struct {
	width, height int
}
