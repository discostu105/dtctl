package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dtctl/pkg/tui/catalog"
)

// Facets & server search: '/' + enter promotes the client filter to a
// server-side | search stage; 'f' builds attribute=value filters from
// fieldsSummary suggestions; 'F' (and the esc chain) clears.

// The search stage must inject directly after the source command — on an
// aggregating pipeline (pods: parse | expand | summarize) DQL rejects it
// anywhere later (SEARCH_COMMAND_NOT_ALLOWED_AFTER, found on a live drive).
func TestServerSearchPrecedesTransformingCommands(t *testing.T) {
	a := testApp(t, "pods")
	seedRows(t, a, []map[string]any{{"id": "K8S_POD-1", "name": "checkout-1", "namespace": "shop", "phase": "Running"}})
	tv := a.top().(*tableView)

	press(a, key("/"))
	typeText(a, "pool")
	press(a, key("enter"))

	lines := strings.Split(tv.dql, "\n")
	if len(lines) < 2 || lines[1] != `| search "*pool*"` {
		t.Fatalf("search must follow the source line:\n%s", tv.dql)
	}
}

func typeText(a *app, text string) {
	for _, r := range text {
		press(a, key(string(r)))
	}
}

func TestFilterEnterPromotesToServerSearch(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{problemRow()})
	tv := a.top().(*tableView)

	press(a, key("/"))
	typeText(a, "payment")
	press(a, key("enter"))

	if len(tv.searches) != 1 || tv.searches[0] != "payment" || tv.filter != "" {
		t.Fatalf("enter should promote filter to server search: searches=%v filter=%q", tv.searches, tv.filter)
	}
	if !strings.Contains(tv.dql, `| search "*payment*"`) {
		t.Errorf("dql missing search stage:\n%s", tv.dql)
	}
	// The stage must narrow before the cap truncates.
	if strings.Index(tv.dql, `| search`) > strings.Index(tv.dql, `| limit`) {
		t.Errorf("search stage must precede the limit:\n%s", tv.dql)
	}
	// A faceted/searched page is a place — it must be findable in history.
	if !historyContains(a, `⌕payment`) {
		t.Errorf("server search not recorded in history: %+v", a.hist.entries)
	}

	// F clears and refetches unnarrowed.
	press(a, key("F"))
	if len(tv.searches) != 0 || strings.Contains(tv.dql, "| search") {
		t.Errorf("F should clear the server search:\n%s", tv.dql)
	}
}

func TestEscClearsServerSearchBeforePopping(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{problemRow()})
	tv := a.top().(*tableView)

	press(a, key("/"))
	typeText(a, "x")
	press(a, key("enter"))
	if len(tv.searches) != 1 || tv.searches[0] != "x" {
		t.Fatalf("searches = %v, want [x]", tv.searches)
	}

	press(a, key("esc"))
	if len(tv.searches) != 0 || len(a.stack) != 1 {
		t.Fatalf("esc should clear the search, not pop: searches=%v depth=%d", tv.searches, len(a.stack))
	}
}

func TestFacetPickerFlow(t *testing.T) {
	a := testApp(t, "hosts")
	seedRows(t, a, []map[string]any{hostRow()})
	tv := a.top().(*tableView)

	var summaryDQL string
	a.ds.runFn = func(dql string) ([]map[string]any, error) {
		summaryDQL = dql
		return nil, nil
	}

	press(a, key("f"))
	if tv.facetMode != facetFieldStage {
		t.Fatal("'f' should open the attribute picker")
	}
	if !tv.InputActive() {
		t.Fatal("picker must own the keyboard (InputActive)")
	}
	joined := strings.Join(tv.facetFields, " ")
	if !strings.Contains(joined, "sku") || !strings.Contains(joined, "name") {
		t.Fatalf("attribute candidates missing scalars: %v", tv.facetFields)
	}
	if strings.Contains(joined, "lifetime") || strings.Contains(joined, "ip") {
		t.Fatalf("maps/arrays must not be facet candidates: %v", tv.facetFields)
	}

	// Quick-search narrows to one attribute; enter explores its values.
	// 's'/'k' are drill/move keys — InputActive gating keeps them as text.
	typeText(a, "sku")
	if got := tv.facetFieldMatches(); len(got) != 1 || got[0] != "sku" {
		t.Fatalf("attribute quick-search → %v, want [sku]", got)
	}
	press(a, key("enter"))
	if tv.facetMode != facetValueStage || tv.facetField != "sku" {
		t.Fatalf("enter should explore values: mode=%d field=%q", tv.facetMode, tv.facetField)
	}
	if !strings.Contains(summaryDQL, "| fieldsSummary sku, topValues: 25") {
		t.Errorf("fieldsSummary query not issued:\n%s", summaryDQL)
	}
	if strings.Contains(summaryDQL, "| limit") {
		t.Errorf("fieldsSummary must summarize the unlimited set:\n%s", summaryDQL)
	}

	// Server suggestions arrive; enter applies the highlighted value exactly.
	tv.Update(dataMsg{owner: facetOwner{v: tv, field: "sku"}, seq: tv.seq, records: []map[string]any{{
		"field":  "sku",
		"values": []any{map[string]any{"value": "t3.large", "count": "12"}},
	}}})
	if len(tv.facetOptions) != 1 {
		t.Fatalf("facetOptions = %v", tv.facetOptions)
	}
	press(a, key("enter"))
	if tv.facetMode != facetOff || len(tv.facets) != 1 {
		t.Fatalf("enter should apply the facet and close: mode=%d facets=%v", tv.facetMode, tv.facets)
	}
	if want := (catalog.Facet{Field: "sku", Value: "t3.large"}); tv.facets[0] != want {
		t.Fatalf("facet = %+v, want %+v", tv.facets[0], want)
	}
	if !strings.Contains(tv.dql, `| filter toString(sku) == "t3.large"`) {
		t.Errorf("dql missing facet stage:\n%s", tv.dql)
	}
	if !strings.Contains(tv.Crumb(), "sku=t3.large") {
		t.Errorf("crumb must claim the applied facet, got %q", tv.Crumb())
	}
	if !historyContains(a, "sku=t3.large") {
		t.Errorf("faceted view not recorded in history: %+v", a.hist.entries)
	}
}

// historyContains reports whether any recorded trail mentions the marker.
func historyContains(a *app, marker string) bool {
	for _, e := range a.hist.entries {
		for _, ref := range e.Stack {
			if strings.Contains(ref.Crumb, marker) {
				return true
			}
		}
	}
	return false
}

func TestSearchTermsStackAndReplace(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{problemRow()})
	tv := a.top().(*tableView)

	press(a, key("/"))
	typeText(a, "pay")
	press(a, key("enter"))
	press(a, key("/"))
	typeText(a, "err")
	press(a, key("enter")) // enter stacks a second term
	if len(tv.searches) != 2 || tv.searches[0] != "pay" || tv.searches[1] != "err" {
		t.Fatalf("searches = %v, want [pay err]", tv.searches)
	}
	if !strings.Contains(tv.dql, `| search "*pay*"`) || !strings.Contains(tv.dql, `| search "*err*"`) {
		t.Errorf("dql missing chained search stages:\n%s", tv.dql)
	}

	press(a, key("/"))
	typeText(a, "new")
	press(a, tea.KeyMsg{Type: tea.KeyEnter, Alt: true}) // alt+enter replaces
	if len(tv.searches) != 1 || tv.searches[0] != "new" {
		t.Fatalf("alt+enter should replace the terms, searches = %v", tv.searches)
	}
}

func TestFacetManagerEditAndRemove(t *testing.T) {
	a := testApp(t, "hosts")
	seedRows(t, a, []map[string]any{hostRow()})
	tv := a.top().(*tableView)
	tv.searches = []string{"web"}
	tv.facets = []catalog.Facet{{Field: "sku", Value: "t3.large"}}

	// The manager lists actives first; selection starts on the attributes.
	press(a, key("f"))
	entries := tv.facetEntries()
	if entries[0].kind != entrySearch || entries[1].kind != entryFacet {
		t.Fatalf("manager order = %+v", entries[:2])
	}
	if tv.facetSel != 2 {
		t.Fatalf("selection should start on the first attribute, got %d", tv.facetSel)
	}

	// Edit the facet: ↑ to it, enter → value stage prefilled, apply replaces.
	press(a, tea.KeyMsg{Type: tea.KeyUp})
	press(a, key("enter"))
	if tv.facetMode != facetValueStage || tv.facetField != "sku" || tv.facetEdit != 0 {
		t.Fatalf("edit mode = (%d, %q, %d)", tv.facetMode, tv.facetField, tv.facetEdit)
	}
	if tv.facetInput.Value() != "t3.large" {
		t.Fatalf("edit input not prefilled: %q", tv.facetInput.Value())
	}
	tv.facetInput.SetValue("t3.micro")
	press(a, key("enter"))
	if len(tv.facets) != 1 || tv.facets[0].Value != "t3.micro" {
		t.Fatalf("edit should replace in place, facets = %v", tv.facets)
	}

	// Edit the search term the same way.
	press(a, key("f"))
	press(a, tea.KeyMsg{Type: tea.KeyUp})
	press(a, tea.KeyMsg{Type: tea.KeyUp})
	press(a, key("enter"))
	if tv.facetMode != facetValueStage || tv.facetField != "" || tv.facetInput.Value() != "web" {
		t.Fatalf("search edit = (%d, %q, %q)", tv.facetMode, tv.facetField, tv.facetInput.Value())
	}
	tv.facetInput.SetValue("db")
	press(a, key("enter"))
	if len(tv.searches) != 1 || tv.searches[0] != "db" {
		t.Fatalf("search edit should replace the term, searches = %v", tv.searches)
	}

	// Remove the facet individually (ctrl+x); the search stays.
	press(a, key("f"))
	press(a, tea.KeyMsg{Type: tea.KeyUp}) // onto the facet
	press(a, tea.KeyMsg{Type: tea.KeyCtrlX})
	if len(tv.facets) != 0 || len(tv.searches) != 1 {
		t.Fatalf("ctrl+x should remove only the facet: facets=%v searches=%v", tv.facets, tv.searches)
	}
	if tv.facetMode != facetFieldStage {
		t.Fatal("removal should keep the manager open")
	}
}

func TestInspectorFacetsListBeneath(t *testing.T) {
	a := testApp(t, "hosts")
	seedRows(t, a, []map[string]any{hostRow()})
	tv := a.top().(*tableView)

	press(a, key("d")) // record inspector for the selected host
	insp, ok := a.top().(*inspectorView)
	if !ok {
		t.Fatalf("top = %T, want inspector", a.top())
	}
	// Move the cursor onto the sku field and facet by it.
	for i, row := range insp.rows {
		if row.key == "sku" {
			insp.cursor = i
			break
		}
	}
	press(a, key("f"))
	if len(a.stack) != 1 || a.top() != viewModel(tv) {
		t.Fatalf("'f' should pop back to the list, depth=%d", len(a.stack))
	}
	if len(tv.facets) != 1 || tv.facets[0] != (catalog.Facet{Field: "sku", Value: "t3.large"}) {
		t.Fatalf("facets = %v", tv.facets)
	}
	if !strings.Contains(tv.dql, `| filter toString(sku) == "t3.large"`) {
		t.Errorf("list not refetched with the facet:\n%s", tv.dql)
	}
}

func TestDetailPageFacetsListBeneath(t *testing.T) {
	a := testApp(t, "hosts")
	seedRows(t, a, []map[string]any{hostRow()})
	tv := a.top().(*tableView)

	press(a, key("enter")) // tabbed detail page, details tab = entity inspector
	dv, ok := a.top().(*detailView)
	if !ok {
		t.Fatalf("top = %T, want detail page", a.top())
	}
	insp := dv.tabs[0].view.(*inspectorView)
	for i, row := range insp.rows {
		if row.key == "sku" {
			insp.cursor = i
			break
		}
	}
	press(a, key("f"))
	if len(a.stack) != 1 || a.top() != viewModel(tv) {
		t.Fatalf("'f' on the details tab should facet the list beneath, depth=%d", len(a.stack))
	}
	if len(tv.facets) != 1 || tv.facets[0] != (catalog.Facet{Field: "sku", Value: "t3.large"}) {
		t.Fatalf("facets = %v", tv.facets)
	}
}

func TestInspectorFacetGuardsMissingField(t *testing.T) {
	a := testApp(t, "hosts")
	seedRows(t, a, []map[string]any{hostRow()})
	tv := a.top().(*tableView)

	// An inspector over a record with a field the list's rows don't carry
	// (e.g. reached via a fresh detail fetch) must not silently empty it.
	deliver(a, a.dispatch(inspectMsg{title: "x", rec: map[string]any{"exotic.field": "v"}}))
	insp := a.top().(*inspectorView)
	for i, row := range insp.rows {
		if row.key == "exotic.field" {
			insp.cursor = i
			break
		}
	}
	press(a, key("f"))
	if len(tv.facets) != 0 {
		t.Fatalf("facet on a missing field must be refused, got %v", tv.facets)
	}
	if len(a.stack) != 2 {
		t.Fatalf("refused facet must not pop the inspector, depth=%d", len(a.stack))
	}
	if !a.statusErr || !strings.Contains(a.status, "exotic.field") {
		t.Errorf("status should name the missing field: %q", a.status)
	}
}

func TestInspectorFacetOnArrayElement(t *testing.T) {
	a := testApp(t, "problems")
	rec := problemRow()
	rec["affected_entity_ids"] = []any{"CLOUD_APPLICATION-0000000000000001"}
	seedRows(t, a, []map[string]any{rec})
	tv := a.top().(*tableView)

	press(a, key("d")) // raw record inspector (enter = problem page)
	insp := a.top().(*inspectorView)
	for i, row := range insp.rows {
		if row.key == "affected_entity_ids" {
			insp.cursor = i
			break
		}
	}
	press(a, key("f"))
	want := catalog.Facet{Field: "affected_entity_ids", Value: "*CLOUD_APPLICATION-0000000000000001*"}
	if len(tv.facets) != 1 || tv.facets[0] != want {
		t.Fatalf("array-element facet = %v, want %+v", tv.facets, want)
	}
	if !strings.Contains(tv.dql, `matchesValue(toString(affected_entity_ids), "*CLOUD_APPLICATION-0000000000000001*")`) {
		t.Errorf("array facet must go through toString+matchesValue:\n%s", tv.dql)
	}
}

func TestFacetPatternValue(t *testing.T) {
	a := testApp(t, "hosts")
	seedRows(t, a, []map[string]any{hostRow()})
	tv := a.top().(*tableView)

	press(a, key("f"))
	typeText(a, "name")
	press(a, key("enter")) // explore values for "name"
	if tv.facetField != "name" {
		t.Fatalf("facetField = %q, want name", tv.facetField)
	}

	// A typed '*' pattern wins over suggestions.
	typeText(a, "web-*")
	press(a, key("enter"))
	if len(tv.facets) != 1 || tv.facets[0].Value != "web-*" {
		t.Fatalf("facets = %v", tv.facets)
	}
	if !strings.Contains(tv.dql, `| filter matchesValue(toString(name), "web-*")`) {
		t.Errorf("dql missing pattern stage:\n%s", tv.dql)
	}
}

func TestFacetEscStagesBack(t *testing.T) {
	a := testApp(t, "hosts")
	seedRows(t, a, []map[string]any{hostRow()})
	tv := a.top().(*tableView)

	press(a, key("f"))
	press(a, key("enter")) // into value stage for the first attribute
	if tv.facetMode != facetValueStage {
		t.Fatalf("mode = %d, want value stage", tv.facetMode)
	}
	press(a, key("esc")) // back to attributes, not out
	if tv.facetMode != facetFieldStage || len(a.stack) != 1 {
		t.Fatalf("esc should step back to attributes: mode=%d depth=%d", tv.facetMode, len(a.stack))
	}
	press(a, key("esc")) // out of the picker, view stays
	if tv.facetMode != facetOff || len(a.stack) != 1 {
		t.Fatalf("esc should close the picker: mode=%d depth=%d", tv.facetMode, len(a.stack))
	}
}

func TestFacetsSurviveTimeframeChange(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{problemRow()})
	tv := a.top().(*tableView)
	tv.facets = []catalog.Facet{{Field: "event.status", Value: "ACTIVE"}}
	tv.searches = []string{"payment"}

	deliver(a, a.setTimeframe(catalog.Timeframes[3]))
	if !strings.Contains(tv.dql, `| search "*payment*"`) ||
		!strings.Contains(tv.dql, `| filter toString(event.status) == "ACTIVE"`) {
		t.Errorf("facets/search must survive a timeframe refetch:\n%s", tv.dql)
	}
	if !strings.Contains(tv.dql, "7d") {
		t.Errorf("timeframe not applied:\n%s", tv.dql)
	}
}

func TestStaleFieldsSummaryResultDropped(t *testing.T) {
	a := testApp(t, "hosts")
	seedRows(t, a, []map[string]any{hostRow()})
	tv := a.top().(*tableView)

	press(a, key("f"))
	typeText(a, "sku")
	press(a, key("enter"))

	// A result for a different exploration (superseded field) must not land.
	tv.Update(dataMsg{owner: facetOwner{v: tv, field: "name"}, seq: tv.seq, records: []map[string]any{{
		"field":  "name",
		"values": []any{map[string]any{"value": "web-01", "count": "1"}},
	}}})
	if len(tv.facetOptions) != 0 || !tv.facetLoading {
		t.Fatalf("stale facet result must be dropped: options=%v", tv.facetOptions)
	}
}
