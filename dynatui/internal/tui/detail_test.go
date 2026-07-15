package tui

import (
	"strings"
	"testing"
)

// TestFrontendDetailConnectsRUM: a frontend's detail page carries its user
// sessions and events as tabs — the frontend is wired into the RUM story.
func TestFrontendDetailConnectsRUM(t *testing.T) {
	a := testApp(t, "frontends")
	seedRows(t, a, []map[string]any{{"id": "FRONTEND-1", "name": "shop", "type": "FRONTEND"}})
	press(a, key("enter"))
	dv, ok := a.top().(*detailView)
	if !ok {
		t.Fatalf("enter on a frontend → %s", a.top().Crumb())
	}
	names := make([]string, len(dv.tabs))
	for i, tab := range dv.tabs {
		names[i] = tab.name
	}
	joined := strings.Join(names, " ")
	for _, want := range []string{"sessions", "userevents", "events", "problems"} {
		if !strings.Contains(joined, want) {
			t.Errorf("frontend tabs = %v, want %s", names, want)
		}
	}
	// Frontends emit no log records and spans carry no frontend field —
	// those tabs would be silently empty.
	if strings.Contains(joined, "logs") || strings.Contains(joined, "traces") {
		t.Errorf("frontend tabs must not include logs/traces: %v", names)
	}
}

// TestNestedLensStripOnDetailPage: the drill letters jump straight to their
// page tab, the brackets drive the active tab's own lens strip, and the
// entered page's digits address its numbered tab bar. GenAI entities open
// traces on the genai lens — their spans rarely include roots.
func TestNestedLensStripOnDetailPage(t *testing.T) {
	a := testApp(t, "genai")
	seedRows(t, a, []map[string]any{{"id": "GENAI_MODEL-1", "name": "claude", "type": "GENAI_MODEL"}})
	press(a, key("enter"))
	dv, ok := a.top().(*detailView)
	if !ok {
		t.Fatalf("enter on a genai entity → %s", a.top().Crumb())
	}
	traceIdx := -1
	for i, tab := range dv.tabs {
		if tab.name == "traces" {
			traceIdx = i
		}
	}
	if traceIdx < 0 {
		t.Fatalf("genai detail page has no traces tab (tabs %v)", dv.tabs)
	}
	// The drill letter jumps straight to the traces tab.
	press(a, key("s"))
	if dv.active != traceIdx {
		t.Fatalf("s must jump to the traces tab, active = %d", dv.active)
	}
	inner, ok := dv.tabs[traceIdx].view.(*tableView)
	if !ok || inner.spec.Name != "traces" {
		t.Fatalf("traces tab view = %T", dv.tabs[traceIdx].view)
	}
	if got := inner.spec.LensAt(inner.scope.Lens).Name; got != "genai" {
		t.Errorf("genai entity's traces tab must open on the genai lens, got %s", got)
	}
	if !strings.Contains(inner.dql, "dt.smartscape.gen_ai.model") {
		t.Errorf("traces tab must scope via the gen_ai dot namespace:\n%s", inner.dql)
	}
	// The brackets drive the visible lens strip; the page tab must not change.
	press(a, key("]"))
	if _, still := a.top().(*detailView); !still {
		t.Fatalf("] on a lensed tab must not leave the page, top = %s", a.top().Crumb())
	}
	if dv.active != traceIdx {
		t.Errorf("] must not switch page tabs while a lens strip is visible")
	}
	if got := inner.spec.LensAt(inner.scope.Lens).Name; got != "all" {
		t.Errorf("] must cycle the inner lens genai → all, got %s", got)
	}
	// tab still cycles the page tabs.
	press(a, key("tab"))
	if dv.active == traceIdx {
		t.Error("tab must still cycle the page tabs")
	}
	// The digits address the innermost numbered strip: back on the traces
	// tab, 2 picks its second lens (errors) directly instead of a tab.
	press(a, key("s"))
	press(a, key("2"))
	if _, still := a.top().(*detailView); !still || dv.active != traceIdx {
		t.Fatalf("digit on a lensed tab must stay there (active=%d, top=%T)", dv.active, a.top())
	}
	if got := inner.spec.LensAt(inner.scope.Lens).Name; got != "errors" {
		t.Errorf("2 on the traces tab must pick the errors lens, got %s", got)
	}
}
