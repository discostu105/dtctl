// Package theme centralizes lipgloss styles for the dtctl TUI. It sticks to
// the 16-color ANSI palette so it degrades gracefully on limited terminals,
// consistent with pkg/output's raw-ANSI styling of CLI output.
package theme

import "github.com/charmbracelet/lipgloss"

var (
	// Chrome
	AppName   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	HeaderKey = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	HeaderVal = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	Crumb     = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	CrumbDim  = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))

	// Table
	TableHeader = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	Selected    = lipgloss.NewStyle().Reverse(true).Bold(true)

	// Footer
	KeyHint  = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	KeyDesc  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	Echo     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	StatusOK = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	Error    = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)

	// Overlays
	OverlayBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("12")).
			Padding(0, 2)
	OverlayTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))

	// Content
	Dim        = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	Label      = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	FactLabel  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	Hit        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	GroupTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	Chart      = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	Spinner    = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	Pin        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
	Badge      = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
)

// classStyles maps the catalog's semantic cell classes to styles.
var classStyles = map[string]lipgloss.Style{
	"error": lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true),
	"warn":  lipgloss.NewStyle().Foreground(lipgloss.Color("11")),
	"ok":    lipgloss.NewStyle().Foreground(lipgloss.Color("10")),
	"dim":   lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
	"spark": lipgloss.NewStyle().Foreground(lipgloss.Color("14")),
}

// Class styles text according to a semantic class name; unknown classes and
// "" return the text unchanged.
func Class(class, text string) string {
	if s, ok := classStyles[class]; ok {
		return s.Render(text)
	}
	return text
}

// Safety returns the style for a context safety level: the more a context can
// mutate, the louder the color (readonly green → unrestricted red).
func Safety(level string) lipgloss.Style {
	switch level {
	case "readonly":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	case "readwrite-mine":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	}
}
