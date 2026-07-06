package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
	"github.com/dynatrace-oss/dtctl/pkg/tui/theme"
)

// queryView is the DQL escape hatch (:query / ctrl+q): an editor above a
// results table whose columns are derived from whatever the query returns.
// Its main entrance is reveal-query — every curated view hands its generated
// DQL here for inspection and tweaking, which makes the TUI a DQL teacher.
type queryView struct {
	ds      *dataSource
	editor  textarea.Model
	results *tableView
	current string // last-submitted DQL
	editing bool

	width, height int
}

func newQueryView(ds *dataSource, dql string, tf catalog.Timeframe) *queryView {
	ta := textarea.New()
	ta.Placeholder = "fetch logs | filter status == \"ERROR\"  (enter runs · ctrl+j newline · tab results)"
	ta.SetHeight(3)
	ta.CharLimit = 4096
	ta.ShowLineNumbers = false
	ta.SetValue(dql)
	ta.Focus()

	v := &queryView{ds: ds, editor: ta, editing: true, current: ""}
	spec := &catalog.Spec{
		Name: "results",
		Kind: catalog.KindSignal,
		Desc: "query results",
		Trace: func(rec map[string]any) string {
			if id := catalog.Str(rec, "trace.id"); id != "" {
				return id
			}
			return catalog.Str(rec, "trace_id")
		},
		Drills: map[string]string{"s": "trace"},
	}
	spec.Query = func(catalog.Scope) string { return v.current }
	v.results = newTableView(ds, spec, catalog.Scope{Timeframe: tf})
	v.results.onData = func(records []map[string]any) {
		spec.Columns = deriveColumns(records)
	}
	return v
}

// Init re-runs an already-submitted query — fresh views never have one, but
// a page restored from history arrives with current set and should come back
// with results, not just editor text.
func (v *queryView) Init() tea.Cmd {
	if v.current != "" {
		return tea.Batch(textarea.Blink, v.results.Refresh())
	}
	return textarea.Blink
}

func (v *queryView) Refresh() tea.Cmd {
	if v.current == "" {
		return nil
	}
	return v.results.Refresh()
}

func (v *queryView) SetTimeframe(tf catalog.Timeframe) tea.Cmd {
	v.results.scope.Timeframe = tf
	return nil // the query text owns its own from: — no silent re-run
}

// InputActive is true while the editor is focused OR the results table's
// filter is — either way a text input owns the keyboard and global keys must
// not fire (typing 'q' should not quit).
func (v *queryView) InputActive() bool { return v.editing || v.results.InputActive() }
func (v *queryView) Crumb() string     { return "query" }
func (v *queryView) DQL() string       { return v.current }

// Busy delegates to the results table (animates the spinner while a query runs).
func (v *queryView) Busy() bool { return v.results.Busy() }

func (v *queryView) Echo() string {
	if v.current == "" {
		return ""
	}
	return v.results.Echo()
}

func (v *queryView) Hints() []keyHint {
	if v.editing {
		return []keyHint{{"enter", "run"}, {"ctrl+j", "newline"}, {"tab", "results"}}
	}
	if v.results.InputActive() { // results-table filter focused
		return v.results.Hints()
	}
	hints := []keyHint{{"tab/i", "edit"}, {"enter", "inspect"}, {"s", "trace"}}
	return append(hints, keyHint{"/", "filter"}, keyHint{"J/K", "sort"})
}

// Selection delegates to the results table (yank, open in browser).
func (v *queryView) Selection() (map[string]any, *catalog.Entity) {
	if v.editing {
		return nil, nil
	}
	return v.results.Selection()
}

func (v *queryView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bodySizeMsg:
		v.width, v.height = msg.width, msg.height
		v.editor.SetWidth(msg.width - 2)
		return v.results.Update(bodySizeMsg{width: msg.width, height: max(msg.height-v.editorHeight(), 1)})

	case dataMsg:
		return v.results.Update(msg)

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

func (v *queryView) handleKey(msg tea.KeyMsg) tea.Cmd {
	if v.editing {
		switch msg.String() {
		case "enter":
			dql := strings.TrimSpace(v.editor.Value())
			if dql == "" {
				return nil
			}
			v.current = dql
			v.editing = false
			v.editor.Blur()
			return v.results.Refresh()
		case "ctrl+j":
			v.editor, _ = v.editor.Update(tea.KeyMsg{Type: tea.KeyEnter})
			return nil
		case "tab":
			v.editing = false
			v.editor.Blur()
			return nil
		case "esc":
			v.editing = false
			v.editor.Blur()
			return claimKey
		}
		var cmd tea.Cmd
		v.editor, cmd = v.editor.Update(msg)
		return cmd
	}

	switch msg.String() {
	case "tab", "i":
		v.editing = true
		return v.editor.Focus()
	}
	return v.results.Update(msg)
}

func (v *queryView) editorHeight() int { return v.editor.Height() + 1 }

func (v *queryView) View(width, height int) string {
	v.width, v.height = width, height
	var b strings.Builder
	b.WriteString(v.editor.View())
	b.WriteString("\n")
	if v.current == "" {
		b.WriteString(theme.Dim.Render("  no query run yet — enter runs, ctrl+q from any view reveals its DQL here"))
		return b.String()
	}
	b.WriteString(v.results.View(width, max(height-v.editorHeight(), 1)))
	return b.String()
}

// deriveColumns builds table columns from result records: known identity
// fields first, then the most prevalent scalar fields, capped for sanity.
func deriveColumns(records []map[string]any) []catalog.Column {
	if len(records) == 0 {
		return []catalog.Column{{Title: "(no rows)", Field: ""}}
	}
	sample := records
	if len(sample) > 100 {
		sample = sample[:100]
	}
	count := map[string]int{}
	for _, rec := range sample {
		for key, val := range rec {
			if val == nil || val == "" || strings.HasPrefix(key, "__") {
				continue
			}
			count[key]++
		}
	}

	// Preferred leading columns when present.
	lead := []string{"timestamp", "display_id", "name", "event.name", "content"}
	var cols []catalog.Column
	used := map[string]bool{}
	addCol := func(key string) {
		if used[key] || len(cols) >= 8 {
			return
		}
		used[key] = true
		col := catalog.Column{Title: strings.ToUpper(key), Field: key}
		switch key {
		case "timestamp":
			col.Width = 12
			col.Value = func(rec map[string]any) string { return catalog.FormatTime(catalog.Str(rec, "timestamp")) }
			col.Sort = func(rec map[string]any) any { return catalog.Str(rec, "timestamp") }
		case "content":
			// flex
		default:
			if len(key) < 24 {
				col.Width = maxInt(len(key)+2, 14)
			}
		}
		cols = append(cols, col)
	}
	for _, key := range lead {
		if count[key] > 0 {
			addCol(key)
		}
	}

	rest := make([]string, 0, len(count))
	for key := range count {
		if !used[key] {
			rest = append(rest, key)
		}
	}
	sort.Slice(rest, func(i, j int) bool {
		if count[rest[i]] != count[rest[j]] {
			return count[rest[i]] > count[rest[j]]
		}
		return rest[i] < rest[j]
	})
	for _, key := range rest {
		addCol(key)
	}

	// At least one flex column so the table fills the width.
	hasFlex := false
	for _, c := range cols {
		if c.Width == 0 {
			hasFlex = true
			break
		}
	}
	if !hasFlex && len(cols) > 0 {
		cols[len(cols)-1].Width = 0
	}
	return cols
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
