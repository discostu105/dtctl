package tui

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
	"github.com/dynatrace-oss/dtctl/pkg/tui/theme"
)

// inspectorView shows a full record grouped by field namespace — records
// routinely carry 50+ dotted fields, so grouping beats flat YAML
// (TUI_DESIGN.md, "Log record inspector"). The most relevant fields render
// first as a highlighted block, and '/' narrows the property list by key or
// value substring.
//
// With facts and a data source set (newEntityInfoView) it doubles as the
// details tab of the entity page: a curated key-facts panel on top of the
// full property list, refreshable via the Smartscape detail query.
type inspectorView struct {
	title string
	rec   map[string]any

	// Entity mode (details tab).
	facts []catalog.Fact
	ds    *dataSource
	dql   string

	seq     int
	loading bool
	err     error

	searchInput textinput.Model
	searching   bool
	search      string

	vp    viewport.Model
	ready bool
}

// priorityFields render first as the "highlights" block, in this order,
// before the namespace groups.
var priorityFields = []string{
	"content", "event.name", "event.description", "display_id", "event.status",
	"event.category", "timestamp", "loglevel", "status", "host.name",
}

func newInspectorView(title string, rec map[string]any) *inspectorView {
	si := textinput.New()
	si.Prompt = "/"
	si.CharLimit = 64
	return &inspectorView{title: title, rec: rec, searchInput: si}
}

// newEntityInfoView builds the details tab of an entity page. rec is the
// already-fetched list row (shown instantly); nil triggers a fetch on Init.
func newEntityInfoView(ds *dataSource, entity catalog.Entity, rec map[string]any) *inspectorView {
	v := newInspectorView(entityName(entity), rec)
	v.facts = catalog.KeyFacts(entity.Type)
	v.ds = ds
	v.dql = catalog.DetailQuery(entity)
	return v
}

func (v *inspectorView) Init() tea.Cmd {
	if v.rec == nil {
		return v.Refresh()
	}
	return nil
}

func (v *inspectorView) Refresh() tea.Cmd {
	if v.ds == nil || v.dql == "" {
		return nil
	}
	v.seq++
	v.loading = true
	v.err = nil
	return v.ds.query(v, v.seq, v.dql)
}

func (v *inspectorView) SetTimeframe(tf catalog.Timeframe) tea.Cmd { return nil }
func (v *inspectorView) InputActive() bool                         { return v.searching }
func (v *inspectorView) Crumb() string                             { return v.title }

func (v *inspectorView) Echo() string {
	if v.dql == "" {
		return ""
	}
	oneline := strings.Join(strings.Fields(strings.ReplaceAll(v.dql, "\n", " ")), " ")
	return fmt.Sprintf("dtctl query '%s'", oneline)
}

func (v *inspectorView) Hints() []keyHint {
	return []keyHint{{"/", "search"}, {"j/k", "scroll"}, {"g/G", "top/bottom"}}
}

func (v *inspectorView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bodySizeMsg:
		v.resize(msg.width, msg.height)
		return nil

	case dataMsg:
		if msg.owner != any(v) || msg.seq != v.seq {
			return nil
		}
		v.loading = false
		if msg.err != nil {
			if v.rec != nil {
				// Keep showing the record we have; surface the failure.
				return statusErr("refresh failed: " + msg.err.Error())
			}
			v.err = msg.err
			return nil
		}
		if len(msg.records) > 0 {
			v.rec = msg.records[0]
		} else if v.rec == nil {
			v.err = errors.New("entity not found (no longer known to Smartscape?)")
		}
		v.setContent()
		return nil

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

func (v *inspectorView) handleKey(msg tea.KeyMsg) tea.Cmd {
	if v.searching {
		switch msg.String() {
		case "enter":
			v.searching = false
			v.searchInput.Blur()
		case "esc":
			v.clearSearch()
		default:
			var cmd tea.Cmd
			v.searchInput, cmd = v.searchInput.Update(msg)
			if v.searchInput.Value() != v.search {
				v.search = v.searchInput.Value()
				v.setContent()
				v.vp.GotoTop()
			}
			return cmd
		}
		return nil
	}

	switch msg.String() {
	case "/":
		v.searching = true
		v.searchInput.Focus()
		return textinput.Blink
	case "esc":
		if v.search != "" {
			v.clearSearch()
			return claimKey
		}
		return nil // app pops the stack
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

func (v *inspectorView) clearSearch() {
	v.searching = false
	v.searchInput.Blur()
	v.searchInput.SetValue("")
	v.search = ""
	v.setContent()
	v.vp.GotoTop()
}

func (v *inspectorView) searchActive() bool { return v.searching || v.search != "" }

func (v *inspectorView) resize(width, height int) {
	bodyH := height
	if v.searchActive() {
		bodyH--
	}
	if bodyH < 1 {
		bodyH = 1
	}
	if !v.ready {
		v.vp = viewport.New(width, bodyH)
		v.ready = true
		v.setContent()
		return
	}
	widthChanged := v.vp.Width != width
	v.vp.Width, v.vp.Height = width, bodyH
	if widthChanged {
		v.setContent()
	}
}

func (v *inspectorView) setContent() {
	if !v.ready {
		return
	}
	v.vp.SetContent(v.content(v.vp.Width))
}

func (v *inspectorView) View(width, height int) string {
	v.resize(width, height)
	if v.rec == nil {
		switch {
		case v.loading:
			return theme.Spinner.Render("⟳ loading…")
		case v.err != nil:
			return theme.Error.Render(wrap(v.err.Error(), width))
		default:
			return theme.Dim.Render("no record")
		}
	}
	out := v.vp.View()
	if v.searchActive() {
		out = " " + v.searchInput.View() + "\n" + out
	}
	return out
}

// content renders the record: the curated facts panel (entity mode), the
// priority-field highlights, then namespace groups (k8s.*, event.*, …)
// sorted by name. A search needle narrows fields by key or value.
func (v *inspectorView) content(width int) string {
	var b strings.Builder

	if len(v.facts) > 0 {
		for _, f := range v.facts {
			if text := f.Value(v.rec); text != "" {
				writeField(&b, f.Label, text, width, theme.FactLabel, 14)
			}
		}
		b.WriteString("\n" + theme.GroupTitle.Render("── properties ") + "\n")
	}

	needle := strings.ToLower(strings.TrimSpace(v.search))
	matches := 0
	rendered := map[string]bool{}

	var prio []string
	for _, key := range priorityFields {
		val, ok := v.rec[key]
		if !ok {
			continue
		}
		rendered[key] = true
		if fieldMatches(needle, key, val) {
			prio = append(prio, key)
		}
	}
	if len(prio) > 0 {
		if len(v.facts) == 0 {
			b.WriteString(theme.GroupTitle.Render("── highlights ") + "\n")
		}
		for _, key := range prio {
			writeField(&b, key, catalog.FormatValue(v.rec[key]), width, v.labelStyle(needle, key, theme.FactLabel), 32)
			matches++
		}
	}

	groups := map[string][]string{}
	for key, val := range v.rec {
		if rendered[key] || !fieldMatches(needle, key, val) {
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
	// Top-level (ungrouped) fields come right after the highlights block.
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
			writeField(&b, key, catalog.FormatValue(v.rec[key]), width, v.labelStyle(needle, key, theme.Label), 32)
			matches++
		}
	}

	if needle != "" && matches == 0 {
		b.WriteString(theme.Dim.Render("  no matching properties"))
	}
	return b.String()
}

// labelStyle highlights keys that themselves match the search needle (a field
// can also be shown because its value matched).
func (v *inspectorView) labelStyle(needle, key string, base lipgloss.Style) lipgloss.Style {
	if needle != "" && strings.Contains(strings.ToLower(key), needle) {
		return theme.Hit
	}
	return base
}

func fieldMatches(needle, key string, val any) bool {
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(key), needle) ||
		strings.Contains(strings.ToLower(catalog.FormatValue(val)), needle)
}

func writeField(b *strings.Builder, key, text string, width int, style lipgloss.Style, labelWidth int) {
	label := style.Render(fmt.Sprintf("%-*s", labelWidth, key))
	if strings.Contains(text, "\n") || len(text) > width-labelWidth-2 {
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
