package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
)

// Bubbletea models are pure enough to drive with synthetic messages; no TTY
// or network is needed. Commands returned by Update are executed manually
// only when they are known to be side-effect free (navigation messages) —
// data-source commands are never run.

func testApp(t *testing.T, initial string) *app {
	t.Helper()
	a, err := newApp(Options{ContextName: "test", SafetyLevel: "readonly", InitialView: initial})
	if err != nil {
		t.Fatal(err)
	}
	// Test seam: queries resolve to empty result sets instead of hitting HTTP.
	a.ds.runFn = func(string) ([]map[string]any, error) { return nil, nil }
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	deliver(a, a.Init())
	return a
}

func key(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// press sends a key and chases returned navigation messages back into the app.
func press(a *app, k tea.KeyMsg) {
	_, cmd := a.Update(k)
	deliver(a, cmd)
}

// deliver executes a command and feeds resulting messages back, resolving
// tea.Batch trees. Data-source results (from the runFn seam) are dropped so
// tests control view data via seedRows.
func deliver(a *app, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if msg == nil {
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			deliver(a, c)
		}
		return
	}
	if _, isData := msg.(dataMsg); isData {
		return
	}
	_, next := a.Update(msg)
	deliver(a, next)
}

// seedRows injects fetched records into the top table view, bypassing the
// data source.
func seedRows(t *testing.T, a *app, rows []map[string]any) {
	t.Helper()
	tv, ok := a.top().(*tableView)
	if !ok {
		t.Fatalf("top view is %T, want *tableView", a.top())
	}
	tv.seq++
	tv.loading = true
	tv.Update(dataMsg{owner: tv, seq: tv.seq, records: rows, elapsed: time.Second, dql: tv.spec.Query(tv.scope)})
}

func problemRow() map[string]any {
	return map[string]any{
		"display_id":   "P-100",
		"event.status": "ACTIVE",
		"event.name":   "Failure rate increase",
		"event.start":  time.Now().UTC().Format(time.RFC3339Nano),
		"smartscape.affected_entities": []any{
			map[string]any{"id": "SERVICE-1", "name": "checkout", "type": "SERVICE"},
		},
	}
}

func TestDrillComposesScopeAndEscRestores(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{problemRow()})

	// 'l' drills into logs scoped to the problem's affected entity.
	press(a, key("l"))
	logs, ok := a.top().(*tableView)
	if !ok || logs.spec.Name != "logs" {
		t.Fatalf("after l: top = %v", a.top().Crumb())
	}
	if logs.scope.Entity == nil || logs.scope.Entity.ID != "SERVICE-1" {
		t.Fatalf("logs scope entity = %+v, want SERVICE-1", logs.scope.Entity)
	}
	if !strings.Contains(logs.dql, `dt.smartscape.service == toSmartscapeId("SERVICE-1")`) {
		t.Errorf("drill did not compose scope into DQL:\n%s", logs.dql)
	}
	if len(a.stack) != 2 {
		t.Fatalf("stack depth = %d, want 2", len(a.stack))
	}

	// esc pops back to problems with state intact.
	press(a, key("esc"))
	if top, ok := a.top().(*tableView); !ok || top.spec.Name != "problems" || len(top.rows) != 1 {
		t.Fatalf("esc did not restore problems view with data")
	}

	// '-' toggles back to the logs stack.
	press(a, key("-"))
	if top, ok := a.top().(*tableView); !ok || top.spec.Name != "logs" {
		t.Fatalf("'-' did not restore last view, top = %v", a.top().Crumb())
	}
}

func TestEnterOpensInspector(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{problemRow()})

	press(a, key("enter"))
	if _, ok := a.top().(*inspectorView); !ok {
		t.Fatalf("enter should open inspector, top = %T", a.top())
	}
	if a.top().Crumb() != "checkout" {
		t.Errorf("inspector crumb = %q", a.top().Crumb())
	}
	body := a.top().View(120, 30)
	if !strings.Contains(body, "Failure rate increase") {
		t.Errorf("inspector body missing record content:\n%s", body)
	}
}

func TestMetricsDrillForCuratedAndUncuratedTypes(t *testing.T) {
	a := testApp(t, "problems")
	row := problemRow()
	seedRows(t, a, []map[string]any{row})

	press(a, key("m"))
	mv, ok := a.top().(*metricsView)
	if !ok {
		t.Fatalf("m should open metrics view for SERVICE, top = %T", a.top())
	}
	if !strings.Contains(mv.dql, "dt.service.request.count") {
		t.Errorf("service metrics dql = %s", mv.dql)
	}
	press(a, key("esc"))

	// An uncurated entity type shows a status message instead of navigating.
	row["smartscape.affected_entities"] = []any{
		map[string]any{"id": "AWS_X-1", "name": "x", "type": "AWS_X"},
	}
	press(a, key("m"))
	if _, ok := a.top().(*metricsView); ok {
		t.Fatal("m must not navigate for uncurated entity types")
	}
	if a.status == "" || !a.statusErr {
		t.Errorf("expected error status, got %q", a.status)
	}
}

func TestCommandBarJumpReplacesStack(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{problemRow()})
	press(a, key("l")) // depth 2

	press(a, key(":"))
	if !a.cmdActive {
		t.Fatal("':' should open the command bar")
	}
	for _, r := range "ho" {
		press(a, key(string(r)))
	}
	press(a, key("enter"))

	if len(a.stack) != 1 {
		t.Fatalf("command-bar jump should replace the stack, depth = %d", len(a.stack))
	}
	if tv, ok := a.top().(*tableView); !ok || tv.spec.Name != "hosts" {
		t.Fatalf("jump target = %v, want hosts", a.top().Crumb())
	}
}

func TestTimeframePickerAppliesGlobally(t *testing.T) {
	a := testApp(t, "hosts")
	press(a, key("t"))
	if !a.tfActive {
		t.Fatal("'t' should open the timeframe picker")
	}
	press(a, key("1"))
	if a.tfActive || a.tf.Label != "30m" {
		t.Fatalf("picker did not apply: tf = %s", a.tf.Label)
	}
}

func TestFilterNarrowsRows(t *testing.T) {
	a := testApp(t, "problems")
	other := problemRow()
	other["event.name"] = "Slowdown on payments"
	seedRows(t, a, []map[string]any{problemRow(), other})

	press(a, key("/"))
	for _, r := range "slowdown" {
		press(a, key(string(r)))
	}
	tv := a.top().(*tableView)
	if len(tv.rows) != 1 {
		t.Fatalf("filter rows = %d, want 1", len(tv.rows))
	}
	// esc clears the filter (and must not pop the view).
	press(a, key("esc"))
	press(a, key("esc"))
	tv = a.top().(*tableView)
	if len(tv.rows) != 2 || len(a.stack) != 1 {
		t.Fatalf("esc should clear filter first: rows=%d depth=%d", len(tv.rows), len(a.stack))
	}
}

func TestViewRendersChrome(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{problemRow()})
	out := a.View()
	for _, want := range []string{"ctx:", "test", "readonly", "problems", "P-100", "dtctl query"} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q", want)
		}
	}
	if h := len(strings.Split(out, "\n")); h != 40 {
		t.Errorf("View() height = %d lines, want 40", h)
	}
}

func TestQuitAndHelpKeys(t *testing.T) {
	a := testApp(t, "hosts")
	_, cmd := a.Update(key("?"))
	if cmd != nil || !a.helpActive {
		t.Fatal("'?' should open help")
	}
	if !strings.Contains(a.View(), "dtctl tui — keys") {
		t.Error("help overlay not rendered")
	}
	press(a, key("esc"))
	if a.helpActive {
		t.Error("esc should close help")
	}

	if _, cmd := a.Update(key("q")); cmd == nil {
		t.Error("'q' should quit")
	}
}

func TestUnknownInitialViewFails(t *testing.T) {
	if _, err := newApp(Options{InitialView: "bogus"}); err == nil {
		t.Fatal("bogus initial view must error")
	}
}

func TestCatalogTimeframeDefaultsAgree(t *testing.T) {
	if catalog.DefaultTimeframe.Label != "2h" {
		t.Errorf("unexpected default timeframe %s", catalog.DefaultTimeframe.Label)
	}
}
