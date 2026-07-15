package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
)

// pageSpecs are the deliberately-unregistered specs wired by name from
// bespoke pages — their targets must resolve like everything else's.
func pageSpecs() []*catalog.Spec {
	return []*catalog.Spec{
		catalog.DavisEventsSpec,
		catalog.VulnEntitiesSpec,
		catalog.VulnAttacksSpec,
		catalog.VulnEntryPointsSpec,
		catalog.VulnTimelineSpec,
	}
}

// Every Spec.EnterTarget must be an enterHandlers sentinel or a catalog view
// name, and every drill target a view name or a bespoke drill special —
// otherwise enter/drill dead-ends at runtime with "unknown view".
func TestEnterTargetsAndDrillsResolve(t *testing.T) {
	// The bespoke targets drill() handles without a catalog lookup.
	drillSpecials := map[string]bool{"trace": true, "session": true, "trace-logs": true}

	for _, spec := range append(catalog.All(), pageSpecs()...) {
		if spec.EnterTarget != "" {
			_, sentinel := enterHandlers[spec.EnterTarget]
			if !sentinel && catalog.Lookup(spec.EnterTarget) == nil {
				t.Errorf("%s: EnterTarget %q is neither an enterHandlers sentinel nor a view name",
					spec.Name, spec.EnterTarget)
			}
		}
		for key, target := range spec.Drills {
			if !drillSpecials[target] && catalog.Lookup(target) == nil {
				t.Errorf("%s: drill %q -> %q resolves to no view", spec.Name, key, target)
			}
		}
	}
}

// Every stack view type must round-trip through its history codec: snapshot
// to a pageRef and rebuild to the same concrete type. A missing or one-sided
// codec entry would silently drop the page from the persistent history.
func TestHistoryCodecsRoundTrip(t *testing.T) {
	a := testApp(t, "hosts")
	tf := a.tf
	entity := catalog.Entity{ID: "HOST-0000000000000001", Name: "web-1", Type: "HOST"}
	rec := map[string]any{"display_id": "P-1", "vulnerability.id": "V-1", "title": "x"}

	views := []viewModel{
		newHomeView(a.ds, tf),
		newTableView(a.ds, catalog.Lookup("hosts"), catalog.Scope{Timeframe: tf}),
		newQueryView(a.ds, "fetch logs", tf, a.qhist),
		newDetailView(a.ds, entity, nil, tf),
		newMetricsView(a.ds, entity, tf),
		newRelationsView(a.ds, entity, tf),
		newNavView(a.ds, tf),
		newNavBrowserView(a.ds, "SERVICE", tf),
		newNavSearchView(a.ds, "checkout", tf),
		newNavWalkView(a.ds, entity, tf),
		newWaterfallView(a.ds, "abc123", "", tf),
		newTimelineView(a.ds, "sess-1", nil, tf),
		newProblemView(a.ds, rec, time.Now()),
		newVulnerabilityView(a.ds, rec, tf),
		newInspectorView(a.ds, "record", rec),
	}
	for _, v := range views {
		ref, ok := pageRefOf(v)
		if !ok {
			t.Errorf("%T: no history codec snapshots it", v)
			continue
		}
		if ref.Kind == "" {
			t.Errorf("%T: codec left Kind empty", v)
			continue
		}
		rebuilt, err := a.viewFromRef(ref, tf)
		if err != nil {
			t.Errorf("%T: rebuild failed: %v", v, err)
			continue
		}
		if got, want := fmt.Sprintf("%T", rebuilt), fmt.Sprintf("%T", v); got != want {
			t.Errorf("round-trip changed the view type: %s -> %s", want, got)
		}
	}
}
