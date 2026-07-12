package tui

import (
	"strings"
	"testing"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

// TestDetailVitalsBlock: the details tab carries a vitals block — the
// Vital-marked utilization series fetched page-level (probe first, then the
// chart query over the available subset) — and enter on a vitals row opens
// the metric's explorer chart scoped to the entity.
func TestDetailVitalsBlock(t *testing.T) {
	a := testApp(t, "hosts")
	seedRows(t, a, []map[string]any{hostRow()})
	press(a, key("enter"))
	dv := a.top().(*detailView)
	if dv.vitalsSpec == nil || dv.vitalsSeq != 1 {
		t.Fatalf("host detail should fire its vitals probe on init (spec=%v seq=%d)", dv.vitalsSpec, dv.vitalsSeq)
	}

	// Probe: only cpu and memory report for this host. The composed chart
	// query must drop the absent series (one missing key zeroes the result).
	cmd := dv.Update(dataMsg{owner: vitalsProbeOwner{dv}, seq: dv.vitalsSeq, records: []map[string]any{
		{"metric.key": "dt.host.cpu.usage"},
		{"metric.key": "dt.host.memory.usage"},
	}})
	if cmd == nil {
		t.Fatal("probe hits should chase the chart query")
	}

	dv.Update(dataMsg{owner: vitalsOwner{dv}, seq: dv.vitalsSeq, records: []map[string]any{{
		"cpu": []any{10.0, 20.0, 78.0},
		"mem": []any{50.0, nil, 60.0},
	}}})

	body := a.top().View(120, 40)
	for _, want := range []string{"vitals (last 2h)", "cpu usage", "memory usage", "78%", "avg 36%", "max 78%"} {
		if !strings.Contains(body, want) {
			t.Errorf("vitals block missing %q:\n%s", want, body)
		}
	}
	// Percent rows carry a btop-style gauge: filled span plus track — the
	// absolute story the normalized sparkline can't tell.
	for _, want := range []string{"███", "░"} {
		if !strings.Contains(body, want) {
			t.Errorf("vitals gauge missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "disk used") {
		t.Errorf("series the probe dropped must not render a row:\n%s", body)
	}

	// The vitals rows are the first selectable rows: enter on the cpu row
	// opens its explorer chart, scoped to the host.
	press(a, key("enter"))
	mv, ok := a.top().(*metricsView)
	if !ok || mv.key != "dt.host.cpu.usage" {
		t.Fatalf("enter on a vitals row should open its explorer chart, top = %T", a.top())
	}
	if mv.entity.ID != "HOST-AAAABBBBCCCCDDDD" {
		t.Errorf("vitals chart should be scoped to the page entity, got %+v", mv.entity)
	}
}

// TestDetailVitalsAbsentWhenNothingReports: an entity whose vitals keys are
// all absent (or a type without vitals) renders no block at all — the vitals
// must never noise up the page.
func TestDetailVitalsAbsentWhenNothingReports(t *testing.T) {
	a := testApp(t, "hosts")
	seedRows(t, a, []map[string]any{hostRow()})
	press(a, key("enter"))
	dv := a.top().(*detailView)

	if cmd := dv.Update(dataMsg{owner: vitalsProbeOwner{dv}, seq: dv.vitalsSeq}); cmd != nil {
		t.Fatal("an empty probe must not chase a chart query")
	}
	if body := a.top().View(120, 40); strings.Contains(body, "vitals") {
		t.Errorf("empty probe must leave the block absent:\n%s", body)
	}
}

// TestDetailContainmentTabs: each curated type carries its most relevant
// next entities as pre-scoped first tabs after the details.
func TestDetailContainmentTabs(t *testing.T) {
	ds := &dataSource{runFn: func(string) ([]map[string]any, error) { return nil, nil }}
	cases := []struct {
		entity catalog.Entity
		tab    string
		scoped string // fragment the started tab's query must carry
	}{
		{catalog.Entity{ID: "HOST-1", Name: "web-01", Type: "HOST"},
			"processes", `host.name == "web-01"`},
		{catalog.Entity{ID: "SERVICE-1", Name: "checkout", Type: "SERVICE"},
			"pods", `source_id == toSmartscapeId("SERVICE-1") and type == "runs_on"`},
		{catalog.Entity{ID: "K8S_POD-1", Name: "checkout-abc", Type: "K8S_POD"},
			"containers", `k8s.pod.name == "checkout-abc"`},
		{catalog.Entity{ID: "K8S_NODE-1", Name: "node-a", Type: "K8S_NODE"},
			"pods", `k8s.node.name == "node-a"`},
		{catalog.Entity{ID: "K8S_DEPLOYMENT-1", Name: "checkout", Type: "K8S_DEPLOYMENT"},
			"pods", `k8s.workload.kind == "deployment" and k8s.workload.name == "checkout"`},
		{catalog.Entity{ID: "K8S_NAMESPACE-1", Name: "shop", Type: "K8S_NAMESPACE"},
			"workloads", `k8s.namespace.name == "shop"`},
		{catalog.Entity{ID: "K8S_CLUSTER-1", Name: "prod", Type: "K8S_CLUSTER"},
			"nodes", `k8s.cluster.name == "prod"`},
	}
	for _, tc := range cases {
		dv := newDetailView(ds, tc.entity, nil, catalog.DefaultTimeframe)
		if dv.tabs[1].name != tc.tab {
			t.Errorf("%s: tabs[1] = %s, want %s", tc.entity.Type, dv.tabs[1].name, tc.tab)
			continue
		}
		tv, ok := dv.tabs[1].view.(*tableView)
		if !ok {
			t.Errorf("%s: containment tab view = %T", tc.entity.Type, dv.tabs[1].view)
			continue
		}
		if q := tv.spec.Query(tv.scope); !strings.Contains(q, tc.scoped) {
			t.Errorf("%s: containment tab not scoped, missing %q:\n%s", tc.entity.Type, tc.scoped, q)
		}
	}

	// A service's deployment surface spans two tabs — pods, then processes —
	// both joined through its runs_on edges.
	svc := newDetailView(ds, catalog.Entity{ID: "SERVICE-1", Name: "checkout", Type: "SERVICE"}, nil, catalog.DefaultTimeframe)
	if svc.tabs[2].name != "processes" {
		t.Errorf("SERVICE: tabs[2] = %s, want processes", svc.tabs[2].name)
	}

	// The related tab — the full unranked edge list — is the LAST tab on
	// every detail page, curated types and uncurated alike.
	for _, e := range []catalog.Entity{
		{ID: "SERVICE-1", Name: "checkout", Type: "SERVICE"},
		{ID: "HOST-1", Name: "web-01", Type: "HOST"},
		{ID: "FRONTEND-1", Name: "shop-ui", Type: "FRONTEND"},
		{ID: "AWS_X-1", Name: "x", Type: "AWS_X"},
	} {
		dv := newDetailView(ds, e, nil, catalog.DefaultTimeframe)
		if last := dv.tabs[len(dv.tabs)-1].name; last != "related" {
			t.Errorf("%s: last tab = %s, want related", e.Type, last)
		}
	}
}
