package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
)

// testAppHist is testApp with a persistent history file.
func testAppHist(t *testing.T, initial, histPath string) *app {
	t.Helper()
	a, err := newApp(Options{ContextName: "test", SafetyLevel: "readonly", InitialView: initial, HistoryPath: histPath})
	if err != nil {
		t.Fatal(err)
	}
	a.ds.runFn = func(string) ([]map[string]any, error) { return nil, nil }
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	deliver(a, a.Init())
	return a
}

func TestHistoryRecordsTrailsAndRestoresStack(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{problemRow()})

	press(a, key("l")) // drill: problems › logs (scoped to SERVICE-1)
	if len(a.hist.entries) != 1 || len(a.hist.entries[0].Stack) != 2 {
		t.Fatalf("drill should record a 2-page trail, entries = %+v", a.hist.entries)
	}

	// Jump away; the drill trail is now history, the hosts page is current.
	press(a, key("3"))
	if tv := a.top().(*tableView); tv.spec.Name != "hosts" {
		t.Fatalf("hotkey 3 should open hosts, got %s", tv.spec.Name)
	}

	press(a, key("H"))
	hp := overlayAs[*historyPicker](t, a)
	// The current page (hosts) is hidden; the drill trail leads the list.
	if len(hp.list) != 1 || hp.list[0].Stack[1].View != "logs" {
		t.Fatalf("history list = %+v", hp.list)
	}
	if !strings.Contains(a.View(), "problems › logs") {
		t.Errorf("picker should render the breadcrumb trail:\n%s", a.View())
	}

	press(a, key("enter"))
	if a.overlay != nil {
		t.Fatal("enter should close the picker")
	}
	if len(a.stack) != 2 {
		t.Fatalf("restore should rebuild the whole trail, depth = %d", len(a.stack))
	}
	logs, ok := a.top().(*tableView)
	if !ok || logs.spec.Name != "logs" {
		t.Fatalf("restored top = %v", a.top().Crumb())
	}
	if logs.scope.Entity == nil || logs.scope.Entity.ID != "SERVICE-1" {
		t.Fatalf("restored scope entity = %+v", logs.scope.Entity)
	}
	if !strings.Contains(logs.dql, `toSmartscapeId("SERVICE-1")`) {
		t.Errorf("restored view did not refetch with scope:\n%s", logs.dql)
	}
	// esc walks back through the restored trail.
	press(a, key("esc"))
	if tv, ok := a.top().(*tableView); !ok || tv.spec.Name != "problems" {
		t.Fatalf("esc after restore should land on problems, top = %v", a.top().Crumb())
	}
}

func TestHistorySurvivesSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "tui-history.json")

	a := testAppHist(t, "problems", path)
	seedRows(t, a, []map[string]any{problemRow()})
	press(a, key("l")) // problems › logs
	press(a, key("/"))
	for _, r := range "co" {
		press(a, key(string(r)))
	}
	press(a, key("enter")) // promote to a server-side search

	// 'q' snapshots the final stack — including the search typed after the
	// push — and saves.
	if _, cmd := a.Update(key("q")); cmd == nil {
		t.Fatal("'q' should quit")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("history file not written: %v", err)
	}

	// Next session: the trail (with the search) is offered and restorable.
	b := testAppHist(t, "home", path)
	press(b, key("H"))
	hp := overlayAs[*historyPicker](t, b)
	if len(hp.list) == 0 {
		t.Fatalf("previous session's history missing, list = %+v", hp.list)
	}
	top := hp.list[0].Stack[len(hp.list[0].Stack)-1]
	if top.View != "logs" || len(top.Searches) != 1 || top.Searches[0] != "co" {
		t.Fatalf("persisted top page = %+v, want logs with search co", top)
	}
	press(b, key("enter"))
	logs, ok := b.top().(*tableView)
	if !ok || logs.spec.Name != "logs" || len(logs.searches) != 1 || logs.searches[0] != "co" {
		t.Fatalf("restored page = %v searches=%v", b.top().Crumb(), logs.searches)
	}
	if !strings.Contains(logs.dql, `| search "*co*"`) {
		t.Errorf("restored view did not refetch with the search stage:\n%s", logs.dql)
	}
}

func TestHistoryDedupsRevisits(t *testing.T) {
	a := testApp(t, "problems")
	press(a, key("3")) // hosts
	press(a, key("4")) // pods
	press(a, key("3")) // hosts again — must move to front, not duplicate

	hosts := 0
	for _, e := range a.hist.entries {
		if len(e.Stack) == 1 && e.Stack[0].View == "hosts" {
			hosts++
		}
	}
	if hosts != 1 {
		t.Fatalf("hosts recorded %d times, want 1 (entries: %d)", hosts, len(a.hist.entries))
	}
	if a.hist.entries[0].Stack[0].View != "hosts" {
		t.Fatalf("revisit should move hosts to the front, got %+v", a.hist.entries[0].Stack)
	}
}

func TestHistoryScopedToContext(t *testing.T) {
	h := loadHistory("", "prod")
	h.entries = []historyEntry{
		{Stack: []pageRef{{Kind: "table", View: "hosts"}}, Context: "prod", Visited: time.Now()},
		{Stack: []pageRef{{Kind: "table", View: "pods"}}, Context: "staging", Visited: time.Now()},
	}
	list := h.forContext("")
	if len(list) != 1 || list[0].Stack[0].View != "hosts" {
		t.Fatalf("history must only offer the current context's pages, got %+v", list)
	}
}

func TestHistoryCapsEntries(t *testing.T) {
	a := testApp(t, "problems")
	for i := 0; i < maxHistoryEntries+10; i++ {
		// Distinct signatures via distinct filters.
		deliver(a, a.dispatch(pushViewMsg{spec: catalog.Lookup("hosts"),
			scope: catalog.Scope{Timeframe: a.tf}, replace: true, filter: strings.Repeat("x", i%60) + "y"}))
	}
	if len(a.hist.entries) != maxHistoryEntries {
		t.Fatalf("entries = %d, want cap %d", len(a.hist.entries), maxHistoryEntries)
	}
}

func TestHistoryRestoresEveryPageKind(t *testing.T) {
	a := testApp(t, "hosts")
	entity := catalog.Entity{ID: "HOST-AAAABBBBCCCCDDDD", Name: "web-01", Type: "HOST"}
	trace := strings.Repeat("ab", 16)
	rec := map[string]any{"display_id": "P-1", "timestamp": "2026-07-01T00:00:00Z"}

	views := []viewModel{
		newHomeView(a.ds, a.tf),
		newTableView(a.ds, catalog.Lookup("logs"), catalog.Scope{Timeframe: a.tf, Entity: &entity}),
		newQueryView(a.ds, "fetch logs", a.tf, a.qhist),
		newDetailView(a.ds, entity, nil, a.tf),
		newMetricsView(a.ds, entity, a.tf),
		newRelationsView(a.ds, entity, a.tf),
		newWaterfallView(a.ds, trace, "", a.tf),
		newInspectorView(a.ds, "P-1", rec),
	}
	wantKinds := []string{"home", "table", "query", "detail", "metrics", "relations", "waterfall", "inspector"}

	for i, v := range views {
		ref, ok := pageRefOf(v)
		if !ok {
			t.Fatalf("%T not describable", v)
		}
		if ref.Kind != wantKinds[i] {
			t.Fatalf("%T kind = %q, want %q", v, ref.Kind, wantKinds[i])
		}
		restored, err := a.viewFromRef(ref, a.tf)
		if err != nil {
			t.Fatalf("restore %s: %v", ref.Kind, err)
		}
		switch rv := restored.(type) {
		case *tableView:
			if rv.spec.Name != "logs" || rv.scope.Entity == nil || rv.scope.Entity.ID != entity.ID {
				t.Errorf("table restore lost identity: %+v", rv.scope)
			}
		case *queryView:
			if rv.current != "fetch logs" || rv.editing {
				t.Errorf("query restore should arrive submitted, current=%q editing=%v", rv.current, rv.editing)
			}
		case *detailView:
			if rv.entity != entity {
				t.Errorf("detail restore entity = %+v", rv.entity)
			}
		case *waterfallView:
			if rv.traceID != trace {
				t.Errorf("waterfall restore trace = %q", rv.traceID)
			}
		case *inspectorView:
			if rv.title != "P-1" || catalog.Str(rv.rec, "display_id") != "P-1" {
				t.Errorf("inspector restore = %q %+v", rv.title, rv.rec)
			}
		}
	}
}

func TestHistoryRestoresTimeframe(t *testing.T) {
	a := testApp(t, "hosts")
	entry := historyEntry{
		Stack:     []pageRef{{Kind: "table", View: "pods", Crumb: "pods"}},
		Context:   "test",
		Timeframe: "7d",
		Visited:   time.Now(),
	}
	deliver(a, a.restoreEntry(entry))
	if a.tf.Label != "7d" {
		t.Fatalf("restore should apply the entry's timeframe, tf = %s", a.tf.Label)
	}
	if tv := a.top().(*tableView); tv.scope.Timeframe.Label != "7d" {
		t.Fatalf("restored view timeframe = %s", tv.scope.Timeframe.Label)
	}
}

func TestHistorySkipsUnrestorablePages(t *testing.T) {
	a := testApp(t, "hosts")
	entry := historyEntry{
		Stack: []pageRef{
			{Kind: "table", View: "gone-view", Crumb: "gone"},
			{Kind: "table", View: "pods", Crumb: "pods"},
		},
		Context: "test",
		Visited: time.Now(),
	}
	deliver(a, a.restoreEntry(entry))
	if len(a.stack) != 1 {
		t.Fatalf("unknown view should be skipped, depth = %d", len(a.stack))
	}
	if tv := a.top().(*tableView); tv.spec.Name != "pods" {
		t.Fatalf("surviving page = %s", tv.spec.Name)
	}

	// A trail with nothing restorable reports instead of clearing the screen.
	before := a.top()
	deliver(a, a.restoreEntry(historyEntry{Stack: []pageRef{{Kind: "table", View: "gone"}}, Context: "test"}))
	if a.top() != before || !a.statusErr {
		t.Fatalf("fully dead entry should keep the current page and set an error status, status=%q", a.status)
	}
}

func TestHistoryPickerEmptyShowsStatus(t *testing.T) {
	a := testApp(t, "hosts")
	press(a, key("H"))
	if a.overlay != nil {
		t.Fatal("picker must not open with no history")
	}
	if a.status == "" {
		t.Error("expected a status explaining the empty history")
	}
}

func TestHistorySaveMergesConcurrentSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hist.json")

	// Two sessions share the file; each records its own trail. The second
	// save must union with the first, not clobber it.
	s1 := loadHistory(path, "test")
	s1.record([]viewModel{newTableView(&dataSource{}, catalog.Lookup("hosts"), catalog.Scope{Timeframe: catalog.DefaultTimeframe})}, "2h")
	s1.save()

	s2 := loadHistory(path, "test") // opened before s1 saved? either way: loaded independently
	s2.record([]viewModel{newTableView(&dataSource{}, catalog.Lookup("pods"), catalog.Scope{Timeframe: catalog.DefaultTimeframe})}, "2h")
	s2.entries = s2.entries[:1] // simulate a session that never saw s1's entry
	s2.save()

	views := map[string]bool{}
	for _, e := range loadHistory(path, "test").entries {
		views[e.Stack[0].View] = true
	}
	if !views["hosts"] || !views["pods"] {
		t.Fatalf("concurrent saves must merge, got %v", views)
	}
}

func TestHistoryLoadToleratesCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hist.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := loadHistory(path, "test")
	if len(h.entries) != 0 {
		t.Fatalf("corrupt file should load empty, got %d entries", len(h.entries))
	}
}
