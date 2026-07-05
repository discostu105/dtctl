package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
	"github.com/dynatrace-oss/dtctl/pkg/tui/theme"
)

// inspectorView shows a full record grouped by field namespace — records
// routinely carry 50+ dotted fields, so grouping beats flat YAML
// (TUI_DESIGN.md, "Log record inspector").
type inspectorView struct {
	title string
	rec   map[string]any

	vp    viewport.Model
	ready bool
	width int
}

// priorityFields render first, in this order, before the namespace groups.
var priorityFields = []string{"content", "event.name", "event.description", "timestamp", "loglevel", "status"}

func newInspectorView(title string, rec map[string]any) *inspectorView {
	return &inspectorView{title: title, rec: rec}
}

func (v *inspectorView) Init() tea.Cmd                             { return nil }
func (v *inspectorView) Refresh() tea.Cmd                          { return nil }
func (v *inspectorView) SetTimeframe(tf catalog.Timeframe) tea.Cmd { return nil }
func (v *inspectorView) InputActive() bool                         { return false }
func (v *inspectorView) Crumb() string                             { return v.title }
func (v *inspectorView) Echo() string                              { return "" }

func (v *inspectorView) Hints() []keyHint {
	return []keyHint{{"j/k", "scroll"}, {"g/G", "top/bottom"}}
}

func (v *inspectorView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bodySizeMsg:
		v.resize(msg.width, msg.height)
		return nil
	case tea.KeyMsg:
		switch msg.String() {
		case "g", "home":
			v.vp.GotoTop()
			return nil
		case "G", "end":
			v.vp.GotoBottom()
			return nil
		}
		var cmd tea.Cmd
		v.vp, cmd = v.vp.Update(msg)
		return cmd
	}
	return nil
}

func (v *inspectorView) resize(width, height int) {
	if !v.ready {
		v.vp = viewport.New(width, height)
		v.ready = true
	} else {
		v.vp.Width = width
		v.vp.Height = height
	}
	if v.width != width {
		v.width = width
		v.vp.SetContent(v.content(width))
	}
}

func (v *inspectorView) View(width, height int) string {
	v.resize(width, height)
	return v.vp.View()
}

// content renders the record: priority fields, then top-level fields, then
// namespace groups (k8s.*, event.*, dt.smartscape.*, …) sorted by name.
func (v *inspectorView) content(width int) string {
	var b strings.Builder

	rendered := map[string]bool{}
	for _, key := range priorityFields {
		if val, ok := v.rec[key]; ok {
			writeField(&b, key, val, width)
			rendered[key] = true
		}
	}

	groups := map[string][]string{}
	for key := range v.rec {
		if rendered[key] {
			continue
		}
		group := ""
		if i := strings.Index(key, "."); i > 0 {
			group = key[:i]
		}
		groups[group] = append(groups[group], key)
	}

	names := make([]string, 0, len(groups))
	for g := range groups {
		names = append(names, g)
	}
	sort.Strings(names)
	// Top-level (ungrouped) fields come right after the priority block.
	sort.SliceStable(names, func(i, j int) bool { return names[i] == "" && names[j] != "" })

	for _, g := range names {
		keys := groups[g]
		sort.Strings(keys)
		if g != "" {
			b.WriteString("\n" + theme.GroupTitle.Render("── "+g+" ") + "\n")
		} else if b.Len() > 0 {
			b.WriteString("\n")
		}
		for _, key := range keys {
			writeField(&b, key, v.rec[key], width)
		}
	}
	return b.String()
}

func writeField(b *strings.Builder, key string, val any, width int) {
	text := catalog.FormatValue(val)
	label := theme.Label.Render(fmt.Sprintf("%-32s", key))
	if strings.Contains(text, "\n") || len(text) > width-34 {
		// Long or multi-line values get their own indented block.
		b.WriteString(label + "\n")
		b.WriteString(indent(wrap(text, width-4), 4) + "\n")
		return
	}
	b.WriteString(label + "  " + text + "\n")
}

func indent(s string, n int) string {
	pad := strings.Repeat(" ", n)
	return pad + strings.ReplaceAll(s, "\n", "\n"+pad)
}
