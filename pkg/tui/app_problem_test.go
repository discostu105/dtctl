package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
)

// richProblemRow is a realistic dt.davis.problems record: per-update shape,
// evidence ids, a Davis description, and a mixed affected set.
func richProblemRow() map[string]any {
	return map[string]any{
		"display_id":         "P-500",
		"event.kind":         "DAVIS_PROBLEM",
		"event.status":       "ACTIVE",
		"event.severity":     "4",
		"event.category":     "ERROR",
		"event.name":         "Failure rate increase on checkout",
		"event.description":  "The failure rate increased to 12.4% (baseline 0.3%).",
		"event.start":        time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339Nano),
		"dt.davis.event_ids": []any{"111_222", "333_444"},
		"smartscape.affected_entities": []any{
			map[string]any{"id": "SERVICE-1", "name": "checkout", "type": "SERVICE"},
			map[string]any{"id": "HOST-2", "name": "web-01", "type": "HOST"},
		},
	}
}

func TestEnterOnProblemOpensProblemPage(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{richProblemRow()})

	press(a, key("enter"))
	pv, ok := a.top().(*problemView)
	if !ok {
		t.Fatalf("enter on a problem should open the problem page, top = %T", a.top())
	}
	if pv.Crumb() != "P-500" {
		t.Errorf("crumb = %q", pv.Crumb())
	}

	names := make([]string, len(pv.tabs))
	for i, tab := range pv.tabs {
		names[i] = tab.name
	}
	want := []string{"overview", "evidence", "logs", "traces", "events", "details"}
	if strings.Join(names, " ") != strings.Join(want, " ") {
		t.Fatalf("tabs = %v, want %v", names, want)
	}

	body := ansi.Strip(pv.View(140, 40))
	for _, s := range []string{"P-500", "ACTIVE", "CRITICAL", "impact — 2 affected", "checkout", "davis says"} {
		if !strings.Contains(body, s) {
			t.Errorf("problem page missing %q:\n%s", s, body)
		}
	}
}

func TestProblemTabsScopeToAffectedEntitiesAndWindow(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{richProblemRow()})
	press(a, key("enter"))
	pv := a.top().(*problemView)

	tabIdx := func(name string) int {
		for i, tab := range pv.tabs {
			if tab.name == name {
				return i
			}
		}
		t.Fatalf("no %s tab", name)
		return -1
	}

	deliverView(pv, pv.setActive(tabIdx("logs")))
	logs := pv.tabs[tabIdx("logs")].view.(*tableView)
	if !strings.Contains(logs.dql, `toSmartscapeId("SERVICE-1")`) ||
		!strings.Contains(logs.dql, `dt.smartscape_source.id == toSmartscapeId("HOST-2")`) {
		t.Errorf("logs tab not scoped to ALL affected entities:\n%s", logs.dql)
	}
	if !strings.Contains(logs.dql, "toTimestamp(") {
		t.Errorf("logs tab not scoped to the problem window:\n%s", logs.dql)
	}

	deliverView(pv, pv.setActive(tabIdx("traces")))
	traces := pv.tabs[tabIdx("traces")].view.(*tableView)
	if !strings.Contains(traces.dql, `dt.smartscape.service == toSmartscapeId("SERVICE-1")`) {
		t.Errorf("traces tab not scoped to the service:\n%s", traces.dql)
	}
	if strings.Contains(traces.dql, "HOST-2") {
		t.Errorf("traces tab must drop span-unscopable HOST:\n%s", traces.dql)
	}

	deliverView(pv, pv.setActive(tabIdx("evidence")))
	evidence := pv.tabs[tabIdx("evidence")].view.(*tableView)
	if !strings.Contains(evidence.dql, `in(event.id, {"111_222", "333_444"})`) {
		t.Errorf("evidence tab not scoped to the problem's event ids:\n%s", evidence.dql)
	}

	// The page owns its window: the global timeframe picker must not reach in.
	if cmd := pv.SetTimeframe(catalog.Timeframe{Label: "7d", Dur: 7 * 24 * time.Hour}); cmd != nil {
		t.Error("problem page should ignore global timeframe changes")
	}
}

func TestProblemPageWithoutSpanScopableAffected(t *testing.T) {
	a := testApp(t, "problems")
	row := richProblemRow()
	row["smartscape.affected_entities"] = []any{
		map[string]any{"id": "HOST-2", "name": "web-01", "type": "HOST"},
	}
	seedRows(t, a, []map[string]any{row})
	press(a, key("enter"))
	pv := a.top().(*problemView)
	for _, tab := range pv.tabs {
		if tab.name == "traces" {
			t.Fatal("problem page should omit the traces tab when nothing is span-scopable")
		}
	}
}

func TestProblemOverviewNavigatesToAffectedEntity(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{richProblemRow()})
	press(a, key("enter"))

	// Cursor starts on the first affected entity; enter opens its page.
	press(a, key("enter"))
	dv, ok := a.top().(*detailView)
	if !ok {
		t.Fatalf("enter on an impact row should open the entity page, top = %T", a.top())
	}
	if dv.entity.ID != "SERVICE-1" {
		t.Errorf("opened entity = %+v", dv.entity)
	}

	// The overview's selection also powers pin/relations/yank.
	press(a, key("esc"))
	pv := a.top().(*problemView)
	press(a, key("j"))
	if _, e := pv.Selection(); e == nil || e.ID != "HOST-2" {
		t.Errorf("selection after j = %+v", e)
	}
}

func TestDetailPagePulseAndSignalsBlock(t *testing.T) {
	a := testApp(t, "hosts")
	row := hostRow()
	row["__enrich.cpu"] = []any{1.0, 5.0, 3.0}
	seedRows(t, a, []map[string]any{row})
	press(a, key("enter"))
	dv := a.top().(*detailView)

	// Before the pulse resolves the slot holds without claiming anything.
	if head := ansi.Strip(dv.View(140, 40)); !strings.Contains(head, "… problems") {
		t.Errorf("loading pulse should render a placeholder:\n%s", head)
	}

	// The pulse resolves: two update-records of one problem plus a closed one.
	active := richProblemRow()
	older := richProblemRow()
	older["event.status"] = "CLOSED"
	closed := richProblemRow()
	closed["display_id"] = "P-501"
	closed["event.status"] = "CLOSED"
	a.Update(dataMsg{owner: pulseOwner{v: dv}, seq: dv.pulseSeq,
		records: []map[string]any{active, older, closed}})

	head := ansi.Strip(dv.View(140, 40))
	if !strings.Contains(head, "⚠ 1 active problem") {
		t.Errorf("pulse should count deduped active problems:\n%s", head)
	}
	if !strings.Contains(head, "cpu ") {
		t.Errorf("pulse should render the row's enrichment sparkline:\n%s", head)
	}
	if !strings.Contains(head, "seen ") {
		t.Errorf("pulse should render the entity age:\n%s", head)
	}

	// The details tab grew a signals block; its first row is the problem —
	// enter jumps straight into the problem page.
	if !strings.Contains(head, "signals") || !strings.Contains(head, "P-500") {
		t.Errorf("details tab should render the signals block:\n%s", head)
	}
	press(a, key("enter"))
	if _, ok := a.top().(*problemView); !ok {
		t.Fatalf("enter on the signal row should open the problem page, top = %T", a.top())
	}
}

func TestDetailSignalsChangeEventRow(t *testing.T) {
	a := testApp(t, "hosts")
	seedRows(t, a, []map[string]any{hostRow()})
	press(a, key("enter"))
	dv := a.top().(*detailView)
	insp := dv.tabs[0].view.(*inspectorView)

	a.Update(dataMsg{owner: changeOwner{v: insp}, records: []map[string]any{{
		"event.name": "Deployment change",
		"event.kind": "DAVIS_EVENT",
		"event.type": "CUSTOM_DEPLOYMENT",
		"timestamp":  time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano),
	}}})

	body := ansi.Strip(dv.View(140, 40))
	if !strings.Contains(body, "last change") || !strings.Contains(body, "Deployment change") {
		t.Errorf("details tab should render the change row:\n%s", body)
	}

	// With no active problems the change row is the first selectable row;
	// enter inspects the event.
	press(a, key("enter"))
	iv, ok := a.top().(*inspectorView)
	if !ok || iv.Crumb() != "Deployment change" {
		t.Fatalf("enter on the change row should inspect the event, top = %T (%q)", a.top(), a.top().Crumb())
	}
}

func TestDetailTabBadgesShowLoadedCounts(t *testing.T) {
	a := testApp(t, "hosts")
	seedRows(t, a, []map[string]any{hostRow()})
	press(a, key("enter"))
	dv := a.top().(*detailView)

	logsIdx := -1
	for i, tab := range dv.tabs {
		if tab.name == "logs" {
			logsIdx = i
		}
	}
	if logsIdx < 0 {
		t.Fatal("host page has no logs tab")
	}
	// Unvisited tabs carry no badge.
	if bar := ansi.Strip(dv.View(140, 40)); strings.Contains(bar, "logs (") {
		t.Errorf("unvisited tab must not show a count:\n%s", bar)
	}

	deliverView(dv, dv.setActive(logsIdx))
	logs := dv.tabs[logsIdx].view.(*tableView)
	logs.Update(dataMsg{owner: logs, seq: logs.seq, records: []map[string]any{
		{"content": "a"}, {"content": "b"},
	}})
	if bar := ansi.Strip(dv.View(140, 40)); !strings.Contains(bar, "logs (2)") {
		t.Errorf("loaded tab should badge its row count:\n%s", bar)
	}
}

func TestInspectorLinksBlockHoistsReferences(t *testing.T) {
	a := testApp(t, "logs")
	rec := map[string]any{
		"timestamp":               time.Now().UTC().Format(time.RFC3339Nano),
		"content":                 "connection pool exhausted",
		"trace_id":                "abcdef0123456789abcdef0123456789",
		"dt.smartscape_source.id": "SERVICE-AAAABBBBCCCC0001",
		"docs.url":                "https://docs.example.invalid/pool",
		"loglevel":                "ERROR",
	}
	seedRows(t, a, []map[string]any{rec})
	press(a, key("enter"))
	insp := a.top().(*inspectorView)

	body := ansi.Strip(insp.View(140, 40))
	if !strings.Contains(body, "▍ links") {
		t.Fatalf("record inspector should render a links block:\n%s", body)
	}
	// Traces rank first, then entities, then URLs.
	links := body[strings.Index(body, "▍ links"):]
	ti, ei, ui := strings.Index(links, "trace_id"), strings.Index(links, "dt.smartscape_source.id"), strings.Index(links, "docs.url")
	if ti < 0 || ei < 0 || ui < 0 || !(ti < ei && ei < ui) {
		t.Errorf("links order (trace %d, entity %d, url %d):\n%s", ti, ei, ui, links)
	}
	// Hoisted keys must not repeat in the namespace groups below.
	if strings.Count(body, "dt.smartscape_source.id") != 1 {
		t.Errorf("hoisted key rendered twice:\n%s", body)
	}
}

func TestProblemPageDigitsSwitchTabs(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{richProblemRow()})
	press(a, key("enter"))
	pv := a.top().(*problemView)

	press(a, key("6"))
	if pv.active != 5 || pv.tabs[5].name != "details" {
		t.Fatalf("digit should switch to the details tab, active = %d", pv.active)
	}
	if _, ok := pv.activeView().(*inspectorView); !ok {
		t.Fatalf("details tab view = %T", pv.activeView())
	}
	// The details tab is the raw record — full details stay available.
	if body := ansi.Strip(pv.View(140, 40)); !strings.Contains(body, "event.description") {
		t.Errorf("details tab should show the full record:\n%s", body)
	}
}
