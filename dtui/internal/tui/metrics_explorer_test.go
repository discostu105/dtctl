package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dynatrace-oss/dtctl/pkg/exec"
	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

func metricKeyRow(key string) map[string]any {
	return map[string]any{
		"metric.key": key,
		"series":     "12",
		"types":      []any{"K8S_POD"},
	}
}

func TestExplorerEnterOpensChartWithScope(t *testing.T) {
	a := testApp(t, "metrics")
	tv := a.top().(*tableView)
	tv.scope.Entity = &catalog.Entity{ID: "K8S_POD-9", Name: "web-0", Type: "K8S_POD"}
	seedRows(t, a, []map[string]any{metricKeyRow("bluebox.session.active")})

	press(a, key("enter"))
	mv, ok := a.top().(*metricsView)
	if !ok {
		t.Fatalf("enter on an explorer row should open the chart, top = %T", a.top())
	}
	if !mv.explore() || mv.key != "bluebox.session.active" {
		t.Fatalf("chart view state = %+v", mv)
	}
	for _, want := range []string{
		"value = avg(bluebox.session.active)",
		`dt.smartscape.k8s_pod == toSmartscapeId("K8S_POD-9")`,
	} {
		if !strings.Contains(mv.dql, want) {
			t.Errorf("chart dql missing %q:\n%s", want, mv.dql)
		}
	}

	// 'a' cycles the aggregation and refetches.
	press(a, key("a"))
	if !strings.Contains(mv.dql, "value = sum(bluebox.session.active)") {
		t.Errorf("agg cycle should rebuild the query:\n%s", mv.dql)
	}
}

func TestExplorerUnscopedChartHasNoFilter(t *testing.T) {
	a := testApp(t, "metrics")
	seedRows(t, a, []map[string]any{metricKeyRow("process.cpu.time")})

	press(a, key("enter"))
	mv := a.top().(*metricsView)
	if strings.Contains(mv.dql, "filter:") {
		t.Errorf("unscoped chart must not compose an entity filter:\n%s", mv.dql)
	}
	if rec, entity := mv.Selection(); rec != nil || entity != nil {
		t.Error("unscoped chart selection should carry no entity")
	}
}

func TestCannedMetricsEnterOpensScopedExplorer(t *testing.T) {
	a := testApp(t, "problems")
	seedRows(t, a, []map[string]any{problemRow()})

	press(a, key("m"))
	if _, ok := a.top().(*metricsView); !ok {
		t.Fatalf("m should open canned charts for SERVICE, top = %T", a.top())
	}
	press(a, key("enter"))
	tv, ok := a.top().(*tableView)
	if !ok || tv.spec.Name != "metrics" {
		t.Fatalf("enter on canned charts should open the explorer, top = %T", a.top())
	}
	for _, want := range []string{
		`dt.smartscape.service == toSmartscapeId("SERVICE-1")`,
		`service.name == "checkout"`,
	} {
		if !strings.Contains(tv.dql, want) {
			t.Errorf("explorer dql missing %q:\n%s", want, tv.dql)
		}
	}
}

func TestDetailPageMetricsTabForUncuratedType(t *testing.T) {
	ds := &dataSource{runFn: func(string) ([]map[string]any, error) { return nil, nil }}
	dv := newDetailView(ds, catalog.Entity{ID: "AWS_X-1", Name: "x", Type: "AWS_X"}, nil, catalog.DefaultTimeframe)

	if len(dv.tabs) < 2 || dv.tabs[1].name != "metrics" {
		t.Fatalf("uncurated detail page should still carry a metrics tab, tabs = %+v", dv.tabs)
	}
	tv, ok := dv.tabs[1].view.(*tableView)
	if !ok || tv.spec.Name != "metrics" {
		t.Fatalf("uncurated metrics tab should be the explorer, got %T", dv.tabs[1].view)
	}
	if !strings.Contains(tv.spec.Query(tv.scope), `dt.smartscape_source.id == toSmartscapeId("AWS_X-1")`) {
		t.Errorf("explorer tab not scoped to the entity:\n%s", tv.spec.Query(tv.scope))
	}
}

func TestCannedChartsFollowAvailabilityProbe(t *testing.T) {
	ds := &dataSource{runFn: func(string) ([]map[string]any, error) { return nil, nil }}
	mv := newMetricsView(ds, catalog.Entity{ID: "K8S_POD-9", Name: "web-0", Type: "K8S_POD"}, catalog.DefaultTimeframe)
	mv.Init()()

	// Probe result: a limit-less pod reports only the universal keys.
	mv.Update(dataMsg{owner: availOwner{mv}, seq: mv.seq, records: []map[string]any{
		{"metric.key": "dt.kubernetes.container.cpu_usage"},
		{"metric.key": "dt.kubernetes.container.memory_working_set"},
		{"metric.key": "dt.kubernetes.pod.network_received_data"},
		{"metric.key": "dt.kubernetes.pod.network_transmitted_data"},
	}})
	// The composed chart query must drop the unavailable limit series — one
	// absent metric zeroes the whole timeseries result (validated live).
	for _, reject := range []string{"cpu_limit", "mem_limit", "throttled"} {
		if strings.Contains(mv.dql, reject) {
			t.Errorf("chart dql must drop unavailable %q:\n%s", reject, mv.dql)
		}
	}

	mv.Update(dataMsg{owner: mv, seq: mv.seq, records: []map[string]any{{
		"cpu":    []any{1.0, 2.0, 3.0},
		"mem":    []any{100.0, 120.0},
		"net_rx": []any{10.0, 11.0},
		"net_tx": []any{5.0, 6.0},
	}}})

	body := mv.View(100, 40)
	for _, want := range []string{"CPU usage", "Memory working set", "Network received"} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics view missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "● CPU limit") {
		t.Errorf("unavailable series must not render a chart row:\n%s", body)
	}
	if !strings.Contains(body, "not reported: CPU limit · CPU throttled · Memory limit") {
		t.Errorf("not-reported note missing:\n%s", body)
	}
}

func TestSplitByDimension(t *testing.T) {
	ds := &dataSource{runFn: func(string) ([]map[string]any, error) { return nil, nil }}
	mv := newMetricChartView(ds, "bluebox.llm.token_count", catalog.Entity{}, catalog.DefaultTimeframe)
	mv.Init()()
	mv.Update(dataMsg{owner: mv, seq: mv.seq, records: []map[string]any{{"value": []any{1.0}}}})

	// 'b' discovers dimensions from sampled series records.
	mv.Update(key("b"))
	mv.Update(dataMsg{owner: dimOwner{mv}, seq: mv.seq, records: []map[string]any{
		{"metric.key": "bluebox.llm.token_count", "gen_ai.request.model": "m1", "k8s.pod.uid": "a"},
		{"metric.key": "bluebox.llm.token_count", "gen_ai.request.model": "m2", "k8s.pod.uid": "b"},
		{"metric.key": "bluebox.llm.token_count", "gen_ai.request.model": "m1", "k8s.pod.uid": "c"},
	}})
	if !mv.dimPick || !mv.InputActive() {
		t.Fatal("dimension picker should be open and own the keyboard")
	}
	picker := mv.View(100, 30)
	for _, want := range []string{"(aggregate)", "gen_ai.request.model  (2 values)", "k8s.pod.uid  (3 values)"} {
		if !strings.Contains(picker, want) {
			t.Errorf("picker missing %q:\n%s", want, picker)
		}
	}

	// Low-cardinality dims list first: down once selects gen_ai.request.model.
	mv.Update(key("j"))
	mv.Update(key("enter"))
	if mv.splitDim != "gen_ai.request.model" {
		t.Fatalf("splitDim = %q", mv.splitDim)
	}
	mv.dql = catalog.MetricSplitQuery(mv.key, mv.agg(), mv.splitDim, mv.entity, mv.tf)
	if !strings.Contains(mv.dql, "by:{gen_ai.request.model}") {
		t.Errorf("split dql = %s", mv.dql)
	}

	// Split results render one chart per dimension value, busiest first,
	// capped with a "+n more" note.
	var recs []map[string]any
	for i := 0; i < 8; i++ {
		recs = append(recs, map[string]any{
			"gen_ai.request.model": fmt.Sprintf("model-%d", i),
			"value":                []any{float64(i), float64(i)},
		})
	}
	mv.seq++
	mv.Update(dataMsg{owner: mv, seq: mv.seq, records: recs})
	body := mv.View(120, 40)
	if !strings.Contains(body, "● model-7") || !strings.Contains(body, "● model-2") {
		t.Errorf("split view missing per-value charts:\n%s", body)
	}
	if strings.Contains(body, "● model-1") || strings.Contains(body, "● model-0") {
		t.Errorf("split view should cap at %d charts:\n%s", splitChartCap, body)
	}
	if !strings.Contains(body, "+2 more series") {
		t.Errorf("split view missing +n more note:\n%s", body)
	}
}

func TestExplorerChartUsesEnrichedMetricMetadata(t *testing.T) {
	ds := &dataSource{runFn: func(string) ([]map[string]any, error) { return nil, nil }}
	mv := newMetricChartView(ds, "dt.host.cpu.idle", catalog.Entity{}, catalog.DefaultTimeframe)
	mv.Init()()

	mv.Update(dataMsg{owner: mv, seq: mv.seq,
		records: []map[string]any{{"value": []any{42.0, 55.5}}},
		metrics: []exec.MetricInfo{{MetricKey: "dt.host.cpu.idle", FieldName: "value", Aggregation: "avg",
			DisplayName: "CPU idle", Description: "Percentage of idle CPU time.", Unit: "Percent"}}})

	body := mv.View(100, 30)
	// The catalogue unit renders the values as percent…
	if !strings.Contains(body, "55.50%") {
		t.Errorf("last value should render with the enriched %% unit:\n%s", body)
	}
	// …and pins the y-axis to a true 0–100 gauge.
	if !strings.Contains(body, "100%") {
		t.Errorf("percent chart should scale to a 0-100 gauge:\n%s", body)
	}
	// displayName and description show as a catalogue-identity line.
	if !strings.Contains(body, "CPU idle — Percentage of idle CPU time.") {
		t.Errorf("catalogue metadata line missing:\n%s", body)
	}

	// An aggregation cycle refetches the same key; a response without
	// metadata (enrichment unavailable) must keep the known identity.
	mv.Update(key("a"))
	mv.Update(dataMsg{owner: mv, seq: mv.seq, records: []map[string]any{{"value": []any{60.0}}}})
	if body := mv.View(100, 30); !strings.Contains(body, "60%") || !strings.Contains(body, "CPU idle") {
		t.Errorf("metric identity should survive a metadata-less refetch:\n%s", body)
	}

	// Split charts inherit the unit too.
	mv.splitDim = "host.name"
	mv.seq++
	mv.Update(dataMsg{owner: mv, seq: mv.seq, records: []map[string]any{
		{"host.name": "node-a", "value": []any{10.0, 20.0}},
	}})
	if body := mv.View(100, 30); !strings.Contains(body, "20%") {
		t.Errorf("split chart should render with the enriched unit:\n%s", body)
	}
}

func TestFmtUnitDurationsAndRates(t *testing.T) {
	cases := []struct {
		f    float64
		unit string
		want string
	}{
		{377941.85, "µs", "377.94 ms"},
		{1500000, "µs", "1.50 s"},
		{0.0044, "s", "4.40 ms"},
		{2.5, "s", "2.50 s"},
		{0.0000021, "s", "2.10 µs"},
		{12.5, "ms", "12.50 ms"},
		{2048, "B/s", "2.0 KiB/s"},
	}
	for _, c := range cases {
		if got := fmtUnit(c.f, c.unit); got != c.want {
			t.Errorf("fmtUnit(%v, %q) = %q, want %q", c.f, c.unit, got, c.want)
		}
	}
}
