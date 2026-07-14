package catalog

import (
	"strings"
	"testing"
)

func TestMetricsExplorerQueryComposition(t *testing.T) {
	spec := Lookup("metrics")
	if spec == nil {
		t.Fatal("metrics explorer spec not registered")
	}

	unscoped := spec.Query(fixtureScope(nil))
	want := "metrics from:now() - 2h\n" +
		"| summarize series = count(), types = collectDistinct(dt.smartscape_source.type), by:{metric.key}\n" +
		"| sort metric.key asc\n| limit 1000"
	if unscoped != want {
		t.Errorf("unscoped explorer query:\n%s\nwant:\n%s", unscoped, want)
	}

	scoped := spec.Query(fixtureScope(&Entity{ID: "K8S_POD-9", Name: "web-0", Type: "K8S_POD"}))
	if !strings.Contains(scoped, `| filter dt.smartscape.k8s_pod == toSmartscapeId("K8S_POD-9")`) {
		t.Errorf("scoped explorer query missing pod filter:\n%s", scoped)
	}
	if !spec.CanScope(fixtureScope(nil).Timeframe, Entity{ID: "AWS_X-1", Type: "AWS_X"}) {
		t.Error("explorer should scope to any entity type via dt.smartscape_source.id")
	}
}

func TestMetricScopeFilterServiceMatchesOTelDimensions(t *testing.T) {
	// OTel metrics carry service.name and legacy dt.entity.service but no
	// dt.smartscape.service (validated live) — all three must or-chain.
	f := MetricScopeFilter(Entity{ID: "SERVICE-42", Name: "checkout", Type: "SERVICE"})
	for _, want := range []string{
		`dt.smartscape.service == toSmartscapeId("SERVICE-42")`,
		`dt.entity.service == "SERVICE-42"`,
		`service.name == "checkout"`,
		`dt.smartscape_source.id == toSmartscapeId("SERVICE-42")`,
	} {
		if !strings.Contains(f, want) {
			t.Errorf("service metric filter missing %q:\n%s", want, f)
		}
	}
	// An id-only service (name unresolved) degrades to the id clauses.
	if f := MetricScopeFilter(Entity{ID: "SERVICE-42", Type: "SERVICE"}); strings.Contains(f, "service.name") {
		t.Errorf("nameless service must not emit a name clause: %s", f)
	}
	// Non-service types never match by name.
	if f := MetricScopeFilter(Entity{ID: "HOST-1", Name: "web", Type: "HOST"}); strings.Contains(f, "service.name") {
		t.Errorf("host filter must not emit service.name: %s", f)
	}
}

func TestExploreMetricsSpec(t *testing.T) {
	spec := ExploreMetricsSpec("bluebox.session.active", "sum")
	if len(spec.Series) != 1 || spec.Series[0].Alias != "value" || spec.Series[0].Title != "sum(bluebox.session.active)" {
		t.Fatalf("explore spec series = %+v", spec.Series)
	}

	tf := Timeframe{Label: "30m"}
	unscoped := spec.Query(Entity{}, tf, nil)
	if unscoped != "timeseries { value = sum(bluebox.session.active) }, from:now() - 30m" {
		t.Errorf("unscoped chart query = %s", unscoped)
	}

	scoped := spec.Query(Entity{ID: "K8S_POD-9", Type: "K8S_POD"}, tf, nil)
	if !strings.Contains(scoped, `filter: { dt.smartscape.k8s_pod == toSmartscapeId("K8S_POD-9")`) {
		t.Errorf("scoped chart query missing entity filter:\n%s", scoped)
	}

	// Keys DQL rejects as bare identifiers are backtick-escaped.
	odd := ExploreMetricsSpec("custom.metric-with-dash", "avg").Query(Entity{}, tf, nil)
	if !strings.Contains(odd, "avg(`custom.metric-with-dash`)") {
		t.Errorf("odd key should be escaped: %s", odd)
	}
}

func TestMetricSplitAndDimsQueries(t *testing.T) {
	tf := Timeframe{Label: "2h"}
	e := Entity{ID: "K8S_POD-9", Type: "K8S_POD"}

	dims := MetricDimsQuery("bluebox.llm.token_count", e, tf)
	for _, want := range []string{
		"metrics from:now() - 2h",
		`| filter metric.key == "bluebox.llm.token_count"`,
		`| filter dt.smartscape.k8s_pod == toSmartscapeId("K8S_POD-9")`,
		"| limit 500",
	} {
		if !strings.Contains(dims, want) {
			t.Errorf("dims query missing %q:\n%s", want, dims)
		}
	}
	if q := MetricDimsQuery("x.y", Entity{}, tf); strings.Contains(q, "toSmartscapeId") {
		t.Errorf("unscoped dims query must not filter by entity:\n%s", q)
	}

	split := MetricSplitQuery("bluebox.llm.token_count", "sum", "gen_ai.request.model", Entity{}, tf)
	if !strings.HasPrefix(split, "timeseries value = sum(bluebox.llm.token_count), by:{gen_ai.request.model}, from:now() - 2h") {
		t.Errorf("split query = %s", split)
	}
}

func TestMetricRelatesTo(t *testing.T) {
	got := metricRelatesTo(map[string]any{"types": []any{"K8S_POD", nil, "CONTAINER"}})
	if got != "CONTAINER K8S_POD" {
		t.Errorf("metricRelatesTo = %q", got)
	}
	if metricRelatesTo(map[string]any{}) != "" {
		t.Error("missing types should render empty")
	}
}

func TestCannedServiceMetricsSpanBothEras(t *testing.T) {
	svc := MetricsFor("SERVICE")
	q := svc.Query(Entity{ID: "SERVICE-42", Name: "checkout", Type: "SERVICE"}, Timeframe{Label: "2h"}, nil)
	for _, want := range []string{
		"odur = avg(http.server.request.duration)",
		`service.name == "checkout"`,
		`dt.smartscape.service == toSmartscapeId("SERVICE-42")`,
	} {
		if !strings.Contains(q, want) {
			t.Errorf("service metrics query missing %q:\n%s", want, q)
		}
	}
	// Response time is microseconds in Grail; the µs unit must be declared so
	// the chart scales it (was mislabeled ms).
	for _, s := range svc.Series {
		if s.Alias == "rt" && s.Unit != "µs" {
			t.Errorf("response time unit = %q, want µs", s.Unit)
		}
	}
}

func TestSeriesExprComposesDefault(t *testing.T) {
	s := MetricSeries{Alias: "deadlocks", Key: "postgres.deadlocks.count", Agg: "sum", Default: "0"}
	if got := s.Expr(); got != "deadlocks = sum(postgres.deadlocks.count, default: 0)" {
		t.Errorf("Expr = %q", got)
	}
}
