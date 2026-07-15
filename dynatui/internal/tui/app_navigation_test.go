package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHotkeysJumpEverywhereAndLettersJumpTabs(t *testing.T) {
	a := testApp(t, "problems")
	press(a, key("4"))
	if tv, ok := a.top().(*tableView); !ok || tv.spec.Name != "pods" {
		t.Fatalf("hotkey 4 should jump to pods, top = %v", a.top().Crumb())
	}
	if len(a.stack) != 1 {
		t.Fatalf("hotkey jump should replace the stack")
	}

	// On a detail page the drill letters jump to their tab...
	seedRows(t, a, []map[string]any{podRow("a", "ns1", "Running", 0)})
	press(a, key("enter"))
	dv, ok := a.top().(*detailView)
	if !ok {
		t.Fatalf("enter on pod should open detail, top = %T", a.top())
	}
	press(a, key("v"))
	if _, stillDetail := a.top().(*detailView); !stillDetail || dv.tabs[dv.active].name != "events" {
		t.Fatalf("v on detail page must jump to the events tab (active=%s, top=%T)", dv.tabs[dv.active].name, a.top())
	}
	// The events tab shows a lens strip — the innermost numbered strip — so
	// digits address it: 2 picks the alerts lens, not a page tab.
	ev, ok := dv.activeView().(*tableView)
	if !ok {
		t.Fatalf("events tab view = %T", dv.activeView())
	}
	press(a, key("2"))
	if _, stillDetail := a.top().(*detailView); !stillDetail || dv.tabs[dv.active].name != "events" {
		t.Fatalf("digit on a lensed tab must stay there (active=%s, top=%T)", dv.tabs[dv.active].name, a.top())
	}
	if got := ev.spec.LensAt(ev.scope.Lens).Name; got != "alerts" {
		t.Fatalf("2 on the events tab must pick the alerts lens, got %s", got)
	}
	// A digit past the strip is swallowed with a hint, never a hidden jump.
	press(a, key("9"))
	if _, stillDetail := a.top().(*detailView); !stillDetail {
		t.Fatalf("out-of-range digit must stay on the page, top=%T", a.top())
	}
	// On a lens-less tab the tab bar is the numbered strip again: 2 picks
	// the second tab (a pod's containers).
	dv.setActive(0) // details — no lens strip
	press(a, key("2"))
	if _, stillDetail := a.top().(*detailView); !stillDetail || dv.tabs[dv.active].name != "containers" {
		t.Fatalf("digit on a lens-less tab must pick a page tab (active=%s, top=%T)", dv.tabs[dv.active].name, a.top())
	}
	// esc pops out — the digits are global bookmarks again.
	press(a, key("esc"))
	press(a, key("3"))
	if tv, ok := a.top().(*tableView); !ok || tv.spec.Name != "hosts" || len(a.stack) != 1 {
		t.Fatalf("digit after esc must be a global bookmark (top=%T, depth=%d)", a.top(), len(a.stack))
	}
}
func TestCommandBarArgsPrefillFilter(t *testing.T) {
	a := testApp(t, "problems")
	press(a, key(":"))
	for _, r := range "pods checkout" {
		press(a, key(string(r)))
	}
	press(a, key("enter"))
	tv, ok := a.top().(*tableView)
	if !ok || tv.spec.Name != "pods" || tv.filter != "checkout" {
		t.Fatalf("':pods checkout' → %v filter %q", a.top().Crumb(), tv.filter)
	}
}

// Finding #6: with empty command-bar input, enter opens the highlighted
// suggestion rather than silently closing.
func TestCommandBarEmptyEnterOpensHighlighted(t *testing.T) {
	a := testApp(t, "problems")
	press(a, key(":"))
	// Cycle to the second suggestion, then enter with no text typed. The
	// palette lists the bespoke screens first (home, query, nav), so index 1
	// is "query" — the crumb, not the view type, carries the assertion.
	press(a, tea.KeyMsg{Type: tea.KeyTab})
	cp := overlayAs[*cmdPalette](t, a)
	want := cp.matches[cp.sel].Name
	press(a, key("enter"))
	if a.overlay != nil {
		t.Fatal("enter should close the command bar")
	}
	if a.top().Crumb() != want {
		t.Fatalf("empty enter should open highlighted %q, got %v", want, a.top().Crumb())
	}
}

// Finding #7: '0' → home works from a detail page (only 1..N switch tabs).
func TestDetailPageZeroHotkeyReachesHome(t *testing.T) {
	a := testApp(t, "hosts")
	seedRows(t, a, []map[string]any{hostRow()})
	press(a, key("enter")) // open host detail
	if _, ok := a.top().(*detailView); !ok {
		t.Fatalf("expected detail page, got %T", a.top())
	}
	press(a, key("0")) // hotkey home — must not be swallowed as a tab
	if _, ok := a.top().(*homeView); !ok {
		t.Fatalf("'0' on a detail page should jump home, got %T", a.top())
	}
}

// Finding #8: while a filter input is focused, the footer shows input-mode
// hints, not the global keys that would just type characters.
func TestFooterHintsAdaptToInputFocus(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{problemRow()})
	press(a, key("/")) // focus the filter
	footer := a.renderFooter()
	if strings.Contains(footer, "quit") || strings.Contains(footer, "timeframe") {
		t.Errorf("footer must not advertise global keys while typing:\n%s", footer)
	}
	if !strings.Contains(footer, "server search") {
		t.Errorf("footer should show input-mode hints:\n%s", footer)
	}
}
