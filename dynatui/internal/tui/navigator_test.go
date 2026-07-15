package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
)

// seedNav injects a main-query result into a navigator view, bypassing the
// data source (same seam as seedRows for tables).
func seedNav(t *testing.T, a *app, rows []map[string]any) *navView {
	t.Helper()
	v, ok := a.top().(*navView)
	if !ok {
		t.Fatalf("top view is %T, want *navView", a.top())
	}
	v.Update(dataMsg{owner: v, seq: v.seq, records: rows, elapsed: time.Second, dql: v.dql})
	return v
}

func edgeRec(src, srcType, verb, dst, dstType string) map[string]any {
	return map[string]any{
		"source_id": src, "source_type": srcType, "type": verb,
		"target_id": dst, "target_type": dstType,
	}
}

func TestNavCommandOpensOverview(t *testing.T) {
	a := testApp(t, "problems")
	press(a, key(":"))
	press(a, key("nav")) // one rune burst — real terminals coalesce too
	press(a, key("enter"))
	v, ok := a.top().(*navView)
	if !ok || v.mode != navOverview {
		t.Fatalf(":nav should open the overview, top = %T", a.top())
	}
	if !strings.Contains(v.dql, `smartscapeNodes "*"`) {
		t.Errorf("overview dql:\n%s", v.dql)
	}
}

// The palette must SHOW the bespoke screens, not just accept their names —
// ":nav" reading "no matching view" made the navigator look nonexistent.
func TestCmdPaletteListsBespokeViews(t *testing.T) {
	a := testApp(t, "problems")
	press(a, key(":"))
	names := func() []string {
		var out []string
		for _, s := range a.cmdMatches {
			out = append(out, s.Name)
		}
		return out
	}
	got := strings.Join(names(), " ")
	for _, want := range []string{"home", "query", "nav"} {
		if !strings.Contains(got, want) {
			t.Errorf("empty palette must list %q: %s", want, got)
		}
	}
	press(a, key("nav")) // typed input must match the entry
	if len(a.cmdMatches) == 0 || names()[0] != "nav" {
		t.Fatalf("typing nav must rank the navigator first, matches = %v", names())
	}
	// Partial input + highlighted suggestion routes through the bespoke path.
	press(a, key("enter"))
	if _, ok := a.top().(*navView); !ok {
		t.Fatalf("enter on the nav suggestion should open the navigator, top = %T", a.top())
	}
}

func TestNavCommandWithTypeOpensBrowser(t *testing.T) {
	a := testApp(t, "problems")
	press(a, key(":"))
	press(a, key("nav service"))
	press(a, key("enter"))
	v, ok := a.top().(*navView)
	if !ok || v.mode != navBrowser || v.typ != "SERVICE" {
		t.Fatalf(":nav service should browse SERVICE, top = %T", a.top())
	}
}

func TestNavCommandWithEntityIDOpensWalk(t *testing.T) {
	a := testApp(t, "problems")
	press(a, key(":"))
	press(a, key("nav HOST-0D8DA6F3E704257C"))
	press(a, key("enter"))
	v, ok := a.top().(*navView)
	if !ok || v.mode != navWalk || v.root.ID != "HOST-0D8DA6F3E704257C" || v.root.Type != "HOST" {
		t.Fatalf(":nav <id> should walk from it, top = %T", a.top())
	}
}

// ":nav payments" is ambiguous — type-shaped once uppercased, but usually a
// name. The type browse runs first; landing empty re-shapes it into the name
// search instead of dead-ending on "no PAYMENTS entities".
func TestNavAmbiguousArgBrowsesTypeThenFallsBackToNameSearch(t *testing.T) {
	a := testApp(t, "problems")
	press(a, key(":"))
	press(a, key("nav payments"))
	press(a, key("enter"))
	v, ok := a.top().(*navView)
	if !ok || v.mode != navBrowser || v.typ != "PAYMENTS" || v.searchFallback != "payments" {
		t.Fatalf(":nav payments should browse PAYMENTS with a name fallback, top = %T", a.top())
	}
	seedNav(t, a, nil)
	if v.search != "payments" || v.typ != "" {
		t.Fatalf("empty type browse must fall back to the name search, search=%q typ=%q", v.search, v.typ)
	}
	if !strings.Contains(v.dql, `matchesValue(name, "*payments*")`) {
		t.Errorf("search dql:\n%s", v.dql)
	}
}

func TestNavNameSearchUniqueMatchWalks(t *testing.T) {
	a := testApp(t, "problems")
	press(a, key(":"))
	press(a, key("nav checkout-gw")) // hyphen: not type-shaped, straight to search
	press(a, key("enter"))
	v, ok := a.top().(*navView)
	if !ok || v.mode != navBrowser || v.search != "checkout-gw" {
		t.Fatalf(":nav checkout-gw should open the name search, top = %T", a.top())
	}
	seedNav(t, a, []map[string]any{
		{"id": "SERVICE-0000000000000001", "name": "checkout-gw", "type": "SERVICE"},
	})
	if v.mode != navWalk || v.root.ID != "SERVICE-0000000000000001" || v.root.Name != "checkout-gw" {
		t.Fatalf("a unique name match must walk straight to the entity, mode=%v root=%+v", v.mode, v.root)
	}
	if !strings.Contains(v.dql, `toSmartscapeId("SERVICE-0000000000000001")`) {
		t.Errorf("walk dql:\n%s", v.dql)
	}
}

func TestNavNameSearchMultiMatchDisambiguates(t *testing.T) {
	a := testApp(t, "problems")
	press(a, key(":"))
	press(a, key("nav checkout-svc"))
	press(a, key("enter"))
	v := seedNav(t, a, []map[string]any{
		{"id": "SERVICE-0000000000000002", "name": "checkout-svc-canary", "type": "SERVICE"},
		{"id": "SERVICE-0000000000000001", "name": "checkout-svc", "type": "SERVICE"},
	})
	if v.mode != navBrowser || v.search != "checkout-svc" {
		t.Fatalf("multi-match must stay a disambiguation list, mode=%v search=%q", v.mode, v.search)
	}
	if catalog.Str(v.instances[0], "name") != "checkout-svc" {
		t.Errorf("exact name match must rank first: %+v", v.instances)
	}
	press(a, key("enter"))
	w, ok := a.top().(*navView)
	if !ok || w.mode != navWalk || w.root.ID != "SERVICE-0000000000000001" {
		t.Fatalf("enter must walk the highlighted match, top = %T", a.top())
	}
}

// `dynatui nav <arg>` — the CLI form of the :nav argument (Options.InitialArg).
func TestNavCLIArgumentRoutesTheInitialView(t *testing.T) {
	a, err := newApp(Options{ContextName: "test", SafetyLevel: "readonly",
		InitialView: "nav", InitialArg: "HOST-0D8DA6F3E704257C"})
	if err != nil {
		t.Fatal(err)
	}
	v, ok := a.top().(*navView)
	if !ok || v.mode != navWalk || v.root.ID != "HOST-0D8DA6F3E704257C" {
		t.Fatalf("dynatui nav <id> should walk from it, top = %T", a.top())
	}

	a, err = newApp(Options{ContextName: "test", SafetyLevel: "readonly",
		InitialView: "nav", InitialArg: "checkout-gw"})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok = a.top().(*navView); !ok || v.search != "checkout-gw" {
		t.Fatalf("dynatui nav <name> should open the name search, top = %T", a.top())
	}
}

func TestGlobalXOpensWalkFromSelection(t *testing.T) {
	a := testApp(t, "pods")
	seedRows(t, a, []map[string]any{podRow("checkout-1", "shop", "Running", 0)})
	press(a, key("X"))
	v, ok := a.top().(*navView)
	if !ok || v.mode != navWalk || v.root.ID != "K8S_POD-checkout-1" {
		t.Fatalf("X should open the navigator rooted at the selection, top = %T", a.top())
	}
	if !strings.Contains(v.dql, `toSmartscapeId("K8S_POD-checkout-1")`) {
		t.Errorf("walk dql:\n%s", v.dql)
	}
}

func TestWalkGroupsHopsAndBacktracks(t *testing.T) {
	a := testApp(t, "pods")
	seedRows(t, a, []map[string]any{podRow("checkout-1", "shop", "Running", 0)})
	press(a, key("X"))
	v := seedNav(t, a, []map[string]any{
		edgeRec("K8S_POD-checkout-1", "K8S_POD", "runs_on", "K8S_NODE-1", "K8S_NODE"),
		edgeRec("SERVICE-1", "SERVICE", "routes_to", "K8S_POD-checkout-1", "K8S_POD"),
	})
	disablePreview(a) // keep the test synchronous (no debounce ticks)

	// Flattened tree: root, runs_on group, node, routes_to group, service.
	kinds := make([]navRowKind, len(v.rows))
	for i, r := range v.rows {
		kinds[i] = r.kind
	}
	want := []navRowKind{navRowRoot, navRowGroup, navRowNeighbor, navRowGroup, navRowNeighbor}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Fatalf("rows = %v, want %v", kinds, want)
	}

	// enter on the K8S_NODE neighbor commits a hop: trail grows, root moves.
	press(a, key("j"))
	press(a, key("j"))
	press(a, key("enter"))
	if v.root.ID != "K8S_NODE-1" || len(v.trail) != 1 || v.trail[0].ID != "K8S_POD-checkout-1" {
		t.Fatalf("hop: root = %+v trail = %+v", v.root, v.trail)
	}
	if !v.loading {
		t.Error("an uncached hop must fetch")
	}
	seedNav(t, a, []map[string]any{
		edgeRec("K8S_POD-checkout-1", "K8S_POD", "runs_on", "K8S_NODE-1", "K8S_NODE"),
	})

	// ← backtracks; the original root re-roots from the cache, zero queries.
	press(a, key("left"))
	if v.root.ID != "K8S_POD-checkout-1" || len(v.trail) != 0 {
		t.Fatalf("backtrack: root = %+v trail = %+v", v.root, v.trail)
	}
	if v.loading {
		t.Error("backtracking to a cached node must not refetch")
	}
	if len(v.edges) != 2 {
		t.Errorf("cached edges = %d, want 2", len(v.edges))
	}
}

func TestWalkDirectionAndMeshToggles(t *testing.T) {
	a := testApp(t, "pods")
	seedRows(t, a, []map[string]any{podRow("checkout-1", "shop", "Running", 0)})
	press(a, key("X"))
	v := seedNav(t, a, []map[string]any{
		edgeRec("K8S_POD-checkout-1", "K8S_POD", "runs_on", "K8S_NODE-1", "K8S_NODE"),
		edgeRec("SERVICE-1", "SERVICE", "routes_to", "K8S_POD-checkout-1", "K8S_POD"),
	})
	disablePreview(a)

	neighborIDs := func() []string {
		var out []string
		for _, r := range v.rows {
			if r.kind == navRowNeighbor {
				out = append(out, r.edge.OtherID)
			}
		}
		return out
	}

	press(a, key("i")) // outgoing only
	if got := neighborIDs(); len(got) != 1 || got[0] != "K8S_NODE-1" {
		t.Errorf("outgoing only = %v", got)
	}
	press(a, key("i")) // incoming only
	if got := neighborIDs(); len(got) != 1 || got[0] != "SERVICE-1" {
		t.Errorf("incoming only = %v", got)
	}
	press(a, key("i")) // both again
	press(a, key("M")) // structure only: routes_to is mesh
	if got := neighborIDs(); len(got) != 1 || got[0] != "K8S_NODE-1" {
		t.Errorf("structure only = %v", got)
	}
}

func TestWalkGroupCapAndExpand(t *testing.T) {
	a := testApp(t, "pods")
	seedRows(t, a, []map[string]any{podRow("checkout-1", "shop", "Running", 0)})
	press(a, key("X"))
	var recs []map[string]any
	for i := 0; i < 20; i++ {
		recs = append(recs, edgeRec("K8S_POD-checkout-1", "K8S_POD", "calls",
			fmt.Sprintf("SERVICE-%02d", i), "SERVICE"))
	}
	v := seedNav(t, a, recs)
	disablePreview(a)

	count := func(kind navRowKind) int {
		n := 0
		for _, r := range v.rows {
			if r.kind == kind {
				n++
			}
		}
		return n
	}
	if count(navRowNeighbor) != navGroupCap || count(navRowMore) != 1 {
		t.Fatalf("capped group: %d neighbors, %d more-rows", count(navRowNeighbor), count(navRowMore))
	}
	// enter on the "+N more" row reveals the rest — never a silent cap.
	press(a, key("G"))
	press(a, key("enter"))
	if count(navRowNeighbor) != 20 || count(navRowMore) != 0 {
		t.Errorf("expanded group: %d neighbors, %d more-rows", count(navRowNeighbor), count(navRowMore))
	}
}

func TestOverviewEnterOpensBrowserThenWalk(t *testing.T) {
	a := testApp(t, "nav")
	v := seedNav(t, a, []map[string]any{
		{"type": "SERVICE", "count": float64(2)},
	})
	if v.mode != navOverview {
		t.Fatalf("initial view mode = %v", v.mode)
	}
	press(a, key("enter"))
	bv, ok := a.top().(*navView)
	if !ok || bv.mode != navBrowser || bv.typ != "SERVICE" {
		t.Fatalf("enter on a census row should open the type browser, top = %T", a.top())
	}
	seedNav(t, a, []map[string]any{
		{"id": "SERVICE-1", "name": "checkout", "display": "checkout", "type": "SERVICE"},
	})
	disablePreview(a)
	press(a, key("enter"))
	wv, ok := a.top().(*navView)
	if !ok || wv.mode != navWalk || wv.root.ID != "SERVICE-1" || wv.root.Name != "checkout" {
		t.Fatalf("enter on an instance should open walk mode, top = %T", a.top())
	}
	// esc pops levels: walk → browser → overview.
	press(a, key("esc"))
	if a.top() != bv {
		t.Fatalf("esc should pop back to the browser, top = %T", a.top())
	}
}

func TestProblemOverlayMarksBothEras(t *testing.T) {
	a := testApp(t, "nav")
	v := seedNav(t, a, []map[string]any{{"type": "SERVICE", "count": float64(1)}})
	v.Update(dataMsg{owner: navProblemsOwner{v}, seq: v.probSeq, records: []map[string]any{{
		"display_id":   "P-1",
		"event.status": "ACTIVE",
		"smartscape.affected_entities": []any{
			map[string]any{"id": "SERVICE-NEW1", "name": "checkout", "type": "SERVICE"},
		},
		"affected_entity_ids": []any{"SERVICE-OLD1"},
	}}})
	if !v.probLoaded {
		t.Fatal("overlay must mark itself loaded")
	}
	if len(v.probByID["SERVICE-NEW1"]) != 1 || len(v.probByID["SERVICE-OLD1"]) != 1 {
		t.Errorf("both id eras must be marked: %v", v.probByID)
	}
	if v.probByType["SERVICE"] != 1 {
		t.Errorf("per-type problem count = %d, want 1", v.probByType["SERVICE"])
	}
}

func TestNavHistoryRoundTrip(t *testing.T) {
	a := testApp(t, "home")
	v := newNavWalkView(a.ds, catalog.Entity{ID: "HOST-1", Name: "web-01", Type: "HOST"}, a.tf)
	v.trail = []catalog.Entity{{ID: "SERVICE-1", Name: "checkout", Type: "SERVICE"}}

	ref, ok := pageRefOf(v)
	if !ok || ref.Kind != "nav" || ref.View != "walk" {
		t.Fatalf("pageRef = %+v", ref)
	}
	restored, err := a.viewFromRef(ref, a.tf)
	if err != nil {
		t.Fatal(err)
	}
	rv, ok := restored.(*navView)
	if !ok || rv.mode != navWalk || rv.root.ID != "HOST-1" {
		t.Fatalf("restored = %#v", restored)
	}
	if len(rv.trail) != 1 || rv.trail[0].ID != "SERVICE-1" {
		t.Errorf("a restored walk must keep its trail: %+v", rv.trail)
	}

	// The other two levels restore too.
	bref, _ := pageRefOf(newNavBrowserView(a.ds, "SERVICE", a.tf))
	if b, err := a.viewFromRef(bref, a.tf); err != nil || b.(*navView).typ != "SERVICE" {
		t.Errorf("browser restore: %v %#v", err, b)
	}
	oref, _ := pageRefOf(newNavView(a.ds, a.tf))
	if o, err := a.viewFromRef(oref, a.tf); err != nil || o.(*navView).mode != navOverview {
		t.Errorf("overview restore: %v %#v", err, o)
	}
}

func TestWalkDrillScopesToHighlightedNode(t *testing.T) {
	a := testApp(t, "pods")
	seedRows(t, a, []map[string]any{podRow("checkout-1", "shop", "Running", 0)})
	press(a, key("X"))
	seedNav(t, a, []map[string]any{
		edgeRec("K8S_POD-checkout-1", "K8S_POD", "runs_on", "K8S_NODE-1", "K8S_NODE"),
	})
	disablePreview(a)
	press(a, key("j"))
	press(a, key("j")) // the K8S_NODE neighbor
	press(a, key("l"))
	tv, ok := a.top().(*tableView)
	if !ok || tv.spec.Name != "logs" {
		t.Fatalf("l should drill to logs, top = %T", a.top())
	}
	if tv.scope.Entity == nil || tv.scope.Entity.ID != "K8S_NODE-1" {
		t.Fatalf("logs must scope to the highlighted neighbor, scope = %+v", tv.scope.Entity)
	}
}

func TestNavFilterNarrowsAndEscClears(t *testing.T) {
	a := testApp(t, "nav")
	v := seedNav(t, a, []map[string]any{
		{"type": "SERVICE", "count": float64(2)},
		{"type": "K8S_POD", "count": float64(9)},
	})
	press(a, key("/"))
	press(a, key("pod"))
	press(a, key("enter"))
	if len(v.rows) != 1 || catalog.Str(v.rows[0].rec, "type") != "K8S_POD" {
		t.Fatalf("filtered rows = %+v", v.rows)
	}
	press(a, key("esc")) // clears the filter, does not pop the page
	if len(v.rows) != 2 || a.top() != v {
		t.Fatalf("esc must clear the filter first: rows = %d top = %T", len(v.rows), a.top())
	}
}
