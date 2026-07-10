// Package theme centralizes the dtui TUI's visual language: an adaptive
// palette (Catppuccin-derived) declared as truecolor with explicit 256- and
// 16-color fallbacks per background flavor, so modern terminals get the
// designed look and limited ones degrade to the same sane ANSI colors the
// CLI output uses.
package theme

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// dual builds an adaptive color: truecolor plus explicit ANSI-256 and ANSI-16
// fallbacks, one triple per terminal background (dark first — the TUI's home
// turf; light values come from the palette's light flavor).
func dual(dTC, d256, d16, lTC, l256, l16 string) lipgloss.CompleteAdaptiveColor {
	return lipgloss.CompleteAdaptiveColor{
		Dark:  lipgloss.CompleteColor{TrueColor: dTC, ANSI256: d256, ANSI: d16},
		Light: lipgloss.CompleteColor{TrueColor: lTC, ANSI256: l256, ANSI: l16},
	}
}

// The palette. Accents are shared hues (Catppuccin Mocha / Latte); the grays
// run from Text (loudest) through Muted to Faint (quietest), plus surface
// tones for selection, chips, and rules.
var (
	Accent = dual("#89b4fa", "111", "12", "#1e66f5", "33", "4") // primary: blue
	Sky    = dual("#89dceb", "117", "14", "#04a5e5", "39", "6")
	Teal   = dual("#94e2d5", "116", "14", "#179299", "30", "6")
	Green  = dual("#a6e3a1", "151", "10", "#40a02b", "70", "2")
	Yellow = dual("#f9e2af", "223", "11", "#df8e1d", "172", "3")
	Peach  = dual("#fab387", "216", "11", "#fe640b", "202", "3")
	Red    = dual("#f38ba8", "211", "9", "#d20f39", "160", "1")
	Mauve  = dual("#cba6f7", "183", "13", "#8839ef", "93", "5")

	Text  = dual("#cdd6f4", "253", "15", "#4c4f69", "239", "0")
	Muted = dual("#a6adc8", "146", "7", "#6c6f85", "245", "8")
	Faint = dual("#6c7086", "243", "8", "#9ca0b0", "247", "8")

	Border = dual("#494d64", "239", "8", "#acb0be", "249", "7")
	SelBg  = dual("#3b4261", "237", "4", "#ccd0da", "252", "7")
	ChipBg = dual("#313244", "236", "8", "#dce0e8", "253", "7")
	Ink    = dual("#1e1e2e", "234", "0", "#eff1f5", "255", "15") // on-accent text
)

var (
	// Chrome
	HeaderKey = lipgloss.NewStyle().Foreground(Faint)
	HeaderVal = lipgloss.NewStyle().Bold(true).Foreground(Text)
	HeaderSep = lipgloss.NewStyle().Foreground(Border)
	Crumb     = lipgloss.NewStyle().Bold(true).Foreground(Accent)
	CrumbDim  = lipgloss.NewStyle().Foreground(Faint)
	Rule      = lipgloss.NewStyle().Foreground(Border)

	// Table
	TableHeader = lipgloss.NewStyle().Bold(true).Foreground(Accent)
	SortMark    = lipgloss.NewStyle().Bold(true).Foreground(Peach)
	Selected    = lipgloss.NewStyle().Bold(true).Background(SelBg).Foreground(Text)
	Gutter      = lipgloss.NewStyle().Bold(true).Foreground(Accent)
	Count       = lipgloss.NewStyle().Bold(true).Foreground(Text)

	// Footer
	KeyHint  = lipgloss.NewStyle().Bold(true).Foreground(Sky)
	KeyDesc  = lipgloss.NewStyle().Foreground(Faint)
	Echo     = lipgloss.NewStyle().Foreground(Faint)
	StatusOK = lipgloss.NewStyle().Foreground(Green)
	Error    = lipgloss.NewStyle().Bold(true).Foreground(Red)

	// Overlays
	OverlayBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(Accent).
			Padding(0, 2)
	OverlayTitle = lipgloss.NewStyle().Bold(true).Foreground(Text)

	// Tabs (detail pages)
	TabActive   = lipgloss.NewStyle().Bold(true).Background(Accent).Foreground(Ink).Padding(0, 1)
	TabInactive = lipgloss.NewStyle().Foreground(Faint).Padding(0, 1)

	// Panels (home)
	PanelFocus  = lipgloss.NewStyle().Foreground(Accent)
	PanelBlur   = lipgloss.NewStyle().Foreground(Border)
	PanelTitle  = lipgloss.NewStyle().Bold(true).Foreground(Text)
	PanelTitle2 = lipgloss.NewStyle().Foreground(Muted)

	// Content
	Dim        = lipgloss.NewStyle().Foreground(Faint)
	Label      = lipgloss.NewStyle().Foreground(Sky)
	FactLabel  = lipgloss.NewStyle().Bold(true).Foreground(Sky)
	Hit        = lipgloss.NewStyle().Bold(true).Foreground(Yellow)
	GroupTitle = lipgloss.NewStyle().Bold(true).Foreground(Accent)
	Chart      = lipgloss.NewStyle().Foreground(Sky)
	Track      = lipgloss.NewStyle().Foreground(Border)
	Spinner    = lipgloss.NewStyle().Foreground(Peach)
	Pin        = lipgloss.NewStyle().Bold(true).Foreground(Peach)
	Badge      = lipgloss.NewStyle().Foreground(Mauve).Background(ChipBg).Padding(0, 1)
	GenAI      = lipgloss.NewStyle().Foreground(Mauve)
	ArrowOut   = lipgloss.NewStyle().Foreground(Green)
	ArrowIn    = lipgloss.NewStyle().Foreground(Peach)

	// Typed values (inspector / detail pages). One color per value kind so a
	// record reads like syntax-highlighted data: numbers peach, JSON string
	// literals green, booleans/null mauve/faint, opaque uids mauve, entity-id
	// links accent+underline (they are traversable), punctuation quiet.
	Number    = lipgloss.NewStyle().Foreground(Peach)
	Boolean   = lipgloss.NewStyle().Foreground(Mauve)
	NullVal   = lipgloss.NewStyle().Foreground(Faint)
	UID       = lipgloss.NewStyle().Foreground(Mauve)
	Link      = lipgloss.NewStyle().Foreground(Accent).Underline(true)
	URL       = lipgloss.NewStyle().Foreground(Sky).Underline(true)
	JSONKey   = lipgloss.NewStyle().Foreground(Sky)
	JSONStr   = lipgloss.NewStyle().Foreground(Green)
	JSONPunct = lipgloss.NewStyle().Foreground(Faint)
)

// brandStops are the Dynatrace logo gradient colors, lime → green → teal →
// blue → purple — the header wordmark runs through them.
var brandStops = []string{"#B4DC00", "#73BE28", "#00B9B4", "#1496FF", "#6F2DA8"}

// brandAt interpolates the brand gradient at f ∈ [0,1] as an adaptive color;
// non-truecolor tiers keep the flat accent (downsampled blends pick mud).
func brandAt(f float64) lipgloss.CompleteAdaptiveColor {
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	pos := f * float64(len(brandStops)-1)
	i := int(pos)
	if i >= len(brandStops)-1 {
		i = len(brandStops) - 2
	}
	hex := blendHex(brandStops[i], brandStops[i+1], pos-float64(i))
	return lipgloss.CompleteAdaptiveColor{
		Dark:  lipgloss.CompleteColor{TrueColor: hex, ANSI256: "111", ANSI: "12"},
		Light: lipgloss.CompleteColor{TrueColor: hex, ANSI256: "33", ANSI: "4"},
	}
}

// BrandGradient renders s with a per-rune foreground sweep through the
// Dynatrace logo gradient.
func BrandGradient(s string, bold bool) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return ""
	}
	var b strings.Builder
	for i, r := range runes {
		f := 0.0
		if len(runes) > 1 {
			f = float64(i) / float64(len(runes)-1)
		}
		style := lipgloss.NewStyle().Foreground(brandAt(f))
		if bold {
			style = style.Bold(true)
		}
		b.WriteString(style.Render(string(r)))
	}
	return b.String()
}

// Wordmark is the header brand: the Dynatrace slant mark and the product
// name, both swept through the logo gradient.
func Wordmark() string {
	return BrandGradient("▛▞▟", true) + " " + BrandGradient("dtui", true)
}

// Series is the color cycle for multi-series charts and per-key coloring
// (waterfall bars by service, metric charts by series index).
var Series = []lipgloss.Style{
	lipgloss.NewStyle().Foreground(Accent),
	lipgloss.NewStyle().Foreground(Teal),
	lipgloss.NewStyle().Foreground(Mauve),
	lipgloss.NewStyle().Foreground(Peach),
	lipgloss.NewStyle().Foreground(Sky),
	lipgloss.NewStyle().Foreground(Green),
}

// SeriesColors are the raw palette colors behind Series, for code that
// derives shades (chart gradients) rather than rendering text directly.
var SeriesColors = []lipgloss.CompleteAdaptiveColor{Accent, Teal, Mauve, Peach, Sky, Green}

// SeriesColorAt returns the i-th series color, wrapping around the cycle.
func SeriesColorAt(i int) lipgloss.CompleteAdaptiveColor {
	return SeriesColors[i%len(SeriesColors)]
}

// Gradient returns one style per chart row for a filled braille chart, top
// row first: the top row renders in the full series color and lower rows fade
// toward the terminal background — the btop-like "glow under the line" look.
// Only the truecolor tier fades; 256/16-color terminals keep the flat base
// color on every row (automatic downsampling of blends picks ugly colors).
func Gradient(c lipgloss.CompleteAdaptiveColor, rows int) []lipgloss.Style {
	styles := make([]lipgloss.Style, rows)
	for i := range styles {
		f := 0.0
		if rows > 1 {
			f = 0.62 * float64(i) / float64(rows-1)
		}
		blended := lipgloss.CompleteAdaptiveColor{
			Dark:  lipgloss.CompleteColor{TrueColor: blendHex(c.Dark.TrueColor, Ink.Dark.TrueColor, f), ANSI256: c.Dark.ANSI256, ANSI: c.Dark.ANSI},
			Light: lipgloss.CompleteColor{TrueColor: blendHex(c.Light.TrueColor, Ink.Light.TrueColor, f), ANSI256: c.Light.ANSI256, ANSI: c.Light.ANSI},
		}
		styles[i] = lipgloss.NewStyle().Foreground(blended)
	}
	return styles
}

// blendHex lerps a #rrggbb color toward another by factor f (0 = pure fg).
func blendHex(fg, bg string, f float64) string {
	fr, fgr, fb, ok1 := parseHex(fg)
	br, bgr, bb, ok2 := parseHex(bg)
	if !ok1 || !ok2 {
		return fg
	}
	lerp := func(a, b int) int { return a + int(f*float64(b-a)) }
	return fmt.Sprintf("#%02x%02x%02x", lerp(fr, br), lerp(fgr, bgr), lerp(fb, bb))
}

func parseHex(s string) (r, g, b int, ok bool) {
	if len(s) != 7 || s[0] != '#' {
		return 0, 0, 0, false
	}
	n, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(n >> 16 & 0xff), int(n >> 8 & 0xff), int(n & 0xff), true
}

// ForKey returns a stable style from the Series cycle for a key, so the same
// service always renders in the same color within a session.
func ForKey(key string) lipgloss.Style {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return Series[h.Sum32()%uint32(len(Series))]
}

// SeriesAt returns the i-th series style, wrapping around the cycle.
func SeriesAt(i int) lipgloss.Style { return Series[i%len(Series)] }

// Section renders a group heading with a leading accent bar: "▍ title".
func Section(title string) string {
	return GroupTitle.Render("▍ " + title)
}

// classStyles maps the catalog's semantic cell classes to styles.
var classStyles = map[string]lipgloss.Style{
	"error": lipgloss.NewStyle().Bold(true).Foreground(Red),
	"warn":  lipgloss.NewStyle().Foreground(Yellow),
	"ok":    lipgloss.NewStyle().Foreground(Green),
	"dim":   lipgloss.NewStyle().Foreground(Faint),
	"spark": lipgloss.NewStyle().Foreground(Sky),
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
		return lipgloss.NewStyle().Foreground(Green)
	case "readwrite-mine":
		return lipgloss.NewStyle().Foreground(Yellow)
	default:
		return lipgloss.NewStyle().Foreground(Red)
	}
}

// --- animated spinner --------------------------------------------------------

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
var spinnerFrame int

// Tick advances the global spinner animation (driven by the app's ticker
// while any visible view reports itself busy).
func Tick() { spinnerFrame = (spinnerFrame + 1) % len(spinnerFrames) }

// Spin returns the current spinner frame glyph (style with Spinner).
func Spin() string { return spinnerFrames[spinnerFrame] }
