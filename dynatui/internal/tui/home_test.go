package tui

import (
	"testing"
)

func TestHomePanelsLoadIndependently(t *testing.T) {
	a := testApp(t, "home")
	hv, ok := a.top().(*homeView)
	if !ok {
		t.Fatalf("home initial view, top = %T", a.top())
	}
	if len(hv.panels) != 6 {
		t.Fatalf("panels = %d", len(hv.panels))
	}
	// Each panel got its own DQL.
	for _, p := range hv.panels {
		if p.dql == "" {
			t.Errorf("panel %q has no query", p.title)
		}
	}
}

// TestHomeSelectionAndYank: home rows are first-class selections — y copies
// the row's identity and rows with a Smartscape id yield an entity for the
// global actions (pin, x/X, o).
func TestHomeSelectionAndYank(t *testing.T) {
	a := testApp(t, "home")
	h := a.top().(*homeView)
	h.Update(dataMsg{owner: panelOwner{v: h, idx: 0}, seq: h.seq,
		records: []map[string]any{{"display_id": "P-77", "name": "cpu saturation"}}})
	h.Update(dataMsg{owner: panelOwner{v: h, idx: 1}, seq: h.seq,
		records: []map[string]any{{"dt.smartscape.service": "SERVICE-0000000000000001", "svc": "checkout"}}})

	h.focus, h.cursor = 0, 0
	if text, _, ok := h.YankText(); !ok || text != "P-77" {
		t.Errorf("problems panel yank = %q, %v", text, ok)
	}
	if _, e := h.Selection(); e != nil {
		t.Error("summarized problem rows carry no entity")
	}

	h.focus, h.cursor = 1, 0
	rec, e := h.Selection()
	if rec == nil || e == nil || e.ID != "SERVICE-0000000000000001" || e.Type != "SERVICE" {
		t.Fatalf("services panel selection = %+v", e)
	}
	// The global x now works from home: it walks the selected service.
	press(a, key("x"))
	nv, ok := a.top().(*navView)
	if !ok || nv.mode != navWalk || nv.root.ID != "SERVICE-0000000000000001" {
		t.Fatalf("x on a home service row should open the walk, top = %T", a.top())
	}
}

// Finding #4: a stale in-flight home-panel result (older seq) must not
// overwrite a newer refresh.
func TestHomePanelDropsStaleResults(t *testing.T) {
	a := testApp(t, "home")
	hv := a.top().(*homeView)
	staleSeq := hv.seq

	// A new refresh bumps the generation.
	deliver(a, hv.Refresh())
	if hv.seq == staleSeq {
		t.Fatal("Refresh should bump seq")
	}

	// A late result tagged with the old seq is ignored.
	hv.Update(dataMsg{owner: panelOwner{v: hv, idx: 1}, seq: staleSeq,
		records: []map[string]any{{"svc": "ghost"}}})
	if len(hv.panels[1].records) != 0 {
		t.Errorf("stale result should be dropped, got %d records", len(hv.panels[1].records))
	}

	// A current-seq result is accepted.
	hv.Update(dataMsg{owner: panelOwner{v: hv, idx: 1}, seq: hv.seq,
		records: []map[string]any{{"svc": "real"}}})
	if len(hv.panels[1].records) != 1 {
		t.Errorf("current result should be accepted, got %d", len(hv.panels[1].records))
	}
}
