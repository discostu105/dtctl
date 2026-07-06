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
// jumps); otherwise the view is pushed as a drill-down. filter pre-fills the
// incremental table filter (command-bar arguments, home-panel jumps).
type pushViewMsg struct {
	spec    *catalog.Spec
	scope   catalog.Scope
	replace bool
	filter  string
}

// waterfallMsg opens the span waterfall for one trace.
type waterfallMsg struct {
	traceID string
}

// relationsMsg opens the Smartscape relations panel for an entity.
type relationsMsg struct {
	entity catalog.Entity
}

// queryMsg opens the DQL escape hatch, optionally pre-filled (reveal query).
type queryMsg struct {
	dql string
}

// inspectMsg opens the record inspector for a selected row.
type inspectMsg struct {
	title string
	rec   map[string]any
}

// detailMsg opens the tabbed entity detail page.
type detailMsg struct {
	entity catalog.Entity
	rec    map[string]any // selected row's record; nil = fetch on open
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

// claimKey is a no-op command a view returns to consume a key press the app
// would otherwise act on itself, e.g. esc that clears a filter instead of
// popping the stack (bubbletea discards nil messages).
var claimKey tea.Cmd = func() tea.Msg { return nil }

// entityName is the display label for an entity (ID when unnamed).
func entityName(e catalog.Entity) string {
	if e.Name != "" {
		return e.Name
	}
	return e.ID
}
