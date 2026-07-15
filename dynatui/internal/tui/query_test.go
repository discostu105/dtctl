package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestCtrlQRevealsQuery(t *testing.T) {
	a := testApp(t, "hosts")
	press(a, tea.KeyMsg{Type: tea.KeyCtrlQ})
	qv, ok := a.top().(*queryView)
	if !ok {
		t.Fatalf("ctrl+q should open query view, top = %T", a.top())
	}
	if !strings.Contains(qv.editor.Value(), `smartscapeNodes "HOST"`) {
		t.Errorf("editor not pre-filled: %q", qv.editor.Value())
	}
}

// Submitted queries persist as history and ctrl+p / ctrl+n cycle them in the
// editor (tui.md: the escape hatch carries "query history").
func TestQueryHistoryCyclesInEditor(t *testing.T) {
	a := testApp(t, "hosts")
	press(a, key(":"))
	press(a, key("query"))
	press(a, key("enter"))
	qv := a.top().(*queryView)

	run := func(dql string) {
		press(a, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(dql)})
		press(a, key("enter"))
		press(a, tea.KeyMsg{Type: tea.KeyTab}) // back to the editor
		qv.editor.SetValue("")
	}
	run("fetch logs")
	run("fetch spans")
	if len(a.qhist.queries) != 2 || a.qhist.queries[0] != "fetch spans" {
		t.Fatalf("history = %v", a.qhist.queries)
	}

	press(a, tea.KeyMsg{Type: tea.KeyCtrlP})
	if got := qv.editor.Value(); got != "fetch spans" {
		t.Fatalf("ctrl+p should recall the last query, editor = %q", got)
	}
	press(a, tea.KeyMsg{Type: tea.KeyCtrlP})
	if got := qv.editor.Value(); got != "fetch logs" {
		t.Fatalf("second ctrl+p should step further back, editor = %q", got)
	}
	press(a, tea.KeyMsg{Type: tea.KeyCtrlN})
	press(a, tea.KeyMsg{Type: tea.KeyCtrlN})
	if got := qv.editor.Value(); got != "" {
		t.Fatalf("ctrl+n past the newest entry should restore the draft, editor = %q", got)
	}

	// Re-running an old query moves it to the front instead of duplicating.
	a.qhist.add("fetch logs")
	if len(a.qhist.queries) != 2 || a.qhist.queries[0] != "fetch logs" {
		t.Fatalf("MRU dedup failed: %v", a.qhist.queries)
	}
}
func TestDeriveColumnsPrefersIdentityAndSkipsEmpties(t *testing.T) {
	cols := deriveColumns([]map[string]any{
		{"timestamp": "2026-07-06T16:00:00Z", "content": "x", "name": "", "level": "INFO"},
		{"timestamp": "2026-07-06T16:00:01Z", "content": "y", "name": "", "level": "WARN"},
	})
	if len(cols) == 0 || cols[0].Title != "TIMESTAMP" {
		t.Fatalf("first column = %+v", cols)
	}
	for _, c := range cols {
		if c.Field == "name" {
			t.Error("all-empty field must not become a column")
		}
	}
}
