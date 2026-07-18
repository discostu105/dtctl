package recipes

import (
	"context"
	"strings"
	"testing"
	"time"
)

type mockResponse struct {
	match   string
	records []map[string]interface{}
}

type mockRunner struct {
	responses []mockResponse
	calls     []string
}

func (m *mockRunner) RunQuery(_ context.Context, dql string) (*RunResult, error) {
	m.calls = append(m.calls, dql)
	for _, r := range m.responses {
		if strings.Contains(dql, r.match) {
			return &RunResult{Records: r.records, Seconds: 0.1}, nil
		}
	}
	return &RunResult{Seconds: 0.1}, nil // default: empty result
}

func rec(kv ...interface{}) map[string]interface{} {
	m := map[string]interface{}{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func discoverTestPack() *Pack {
	return &Pack{
		APIVersion: APIVersion,
		Kind:       KindPack,
		Metadata:   PackMetadata{Name: "test-pack", Version: "1"},
		Capabilities: map[string]*CapabilityDef{
			"spans":       {DataObject: "spans"},
			"rum":         {DataObject: "user.events"},
			"hosts":       {EntityTypes: []string{"HOST"}},
			"azure":       {EntityTypes: []string{"AZURE_*"}},
			"k8s-metrics": {MetricKey: "dt.kubernetes.*"},
			"rap":         {Probe: "fetch security.events RAPPROBE | limit 1", Window: "24h"},
		},
		Recipes: map[string]*Recipe{
			"good":          {Requires: []string{"spans"}, DQL: "fetch spans GOODQ | limit 25"},
			"needs-rum":     {Requires: []string{"rum"}, DQL: "fetch user.events | limit 5"},
			"undefined-cap": {Requires: []string{"nope"}, DQL: "fetch spans | limit 5"},
			"missing-table": {DataObjects: []string{"user.events"}, DQL: "fetch user.events | limit 5"},
			"low-carriage": {
				MinCarriage: &MinCarriage{Table: "spans", Field: "weird.field", Ratio: 0.9},
				DQL:         "fetch spans | limit 5",
			},
			"probe-verified-empty": {
				DQL:    "fetch spans THRESHOLDQ | limit 5",
				Verify: &Verify{Probe: "fetch spans LIVEPROBE | limit 1", Expect: "records"},
			},
			"probe-dead": {
				DQL:    "fetch spans DEADQ | limit 5",
				Verify: &Verify{Probe: "fetch spans DEADPROBE | limit 1", Expect: "records"},
			},
			"values-zero": {
				DQL:    "fetch spans VALZQ | limit 5",
				Verify: &Verify{Probe: "fetch spans VALPROBE | summarize c = countIf(true)", Expect: "values"},
			},
			"widen-me": {DQL: "fetch spans, from:now()-2h WIDENQ | limit 10"},
			"needs-id": {
				Params: map[string]*Param{"id": {Description: "entity id"}},
				DQL:    "fetch spans | filter x == {{.id | dqlString}}",
			},
		},
	}
}

func discoverTestRunner() *mockRunner {
	return &mockRunner{responses: []mockResponse{
		{"dt.system.data_objects", []map[string]interface{}{
			rec("name", "logs", "fetchable", true), rec("name", "spans", "fetchable", true),
			rec("name", "dt.davis.problems", "fetchable", true), rec("name", "metrics", "fetchable", false),
		}},
		{"dt.system.buckets", []map[string]interface{}{rec("name", "default_logs")}},
		{`smartscapeNodes "*"`, []map[string]interface{}{
			rec("type", "HOST", "c", float64(5)),
			rec("type", "K8S_POD", "c", float64(10)),
			rec("type", "SERVICE", "c", float64(3)),
		}},
		{"metrics from:", []map[string]interface{}{rec("metric.key", "dt.kubernetes.container.cpu")}},
		// carriage scans: 100 total, every probed field carried on 50
		{"fetch spans, from:now()-24h, samplingRatio:100 | summarize total", []map[string]interface{}{
			rec("total", float64(100), "f0", float64(50), "f1", float64(50), "f2", float64(50),
				"f3", float64(50), "f4", float64(50), "f5", float64(50), "f6", float64(50), "f7", float64(50)),
		}},
		// logs fields, in carriageFields order: k8s.pod.name, k8s.namespace.name,
		// loglevel, service.name, dt.smartscape.service, dt.smartscape.host,
		// dt.smartscape.k8s_pod
		{"fetch logs, from:now()-24h, samplingRatio:100 | summarize total", []map[string]interface{}{
			rec("total", float64(100), "f0", float64(60), "f1", float64(60), "f2", float64(100),
				"f3", float64(10), "f4", float64(20), "f5", float64(0), "f6", float64(0)),
		}},
		{"RAPPROBE", []map[string]interface{}{rec("event.type", "DETECTION_FINDING")}},
		{"GOODQ", manyRecords(25)},
		{"LIVEPROBE", []map[string]interface{}{rec("x", "1")}},
		{"VALPROBE", []map[string]interface{}{rec("c", float64(0))}},
		{"from:now()-24h WIDENQ", manyRecords(3)},
	}}
}

func manyRecords(n int) []map[string]interface{} {
	out := make([]map[string]interface{}, n)
	for i := range out {
		out[i] = rec("i", float64(i))
	}
	return out
}

func fixedNow() time.Time { return time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC) }

func derefF(f *float64) interface{} {
	if f == nil {
		return nil
	}
	return *f
}

func TestDiscover(t *testing.T) {
	runner := discoverTestRunner()
	book, report, err := Discover(context.Background(), runner, []*Pack{discoverTestPack()}, DiscoverOptions{
		ContextName: "test", Generator: "test", Now: fixedNow,
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	// Facts.
	if !contains(book.Facts.DataObjects, "spans") || len(book.Facts.Buckets) != 1 {
		t.Errorf("facts: %+v", book.Facts)
	}
	// Catalog partition: metrics is usable_with-marked unfetchable — it must
	// leave dataObjects, land in unfetchable, and be called out in a note.
	if contains(book.Facts.DataObjects, "metrics") ||
		len(book.Facts.Unfetchable) != 1 || book.Facts.Unfetchable[0] != "metrics" {
		t.Errorf("unfetchable partition: dataObjects=%v unfetchable=%v",
			book.Facts.DataObjects, book.Facts.Unfetchable)
	}
	foundNote := false
	for _, n := range book.Facts.Notes {
		if strings.Contains(n, "without fetch support: metrics") {
			foundNote = true
		}
	}
	if !foundNote {
		t.Errorf("missing unfetchable note in %v", book.Facts.Notes)
	}
	if book.Facts.EntityTypes["K8S_POD"] != 10 {
		t.Errorf("census: %v", book.Facts.EntityTypes)
	}

	// Capability evaluation: every definition shape.
	caps := stringSet(book.Facts.Capabilities)
	for _, want := range []string{"spans", "hosts", "k8s-metrics", "rap"} {
		if !caps[want] {
			t.Errorf("capability %s should be present (got %v)", want, book.Facts.Capabilities)
		}
	}
	// Absent entries carry citable evidence for their definition shape.
	absent := stringSet(book.Facts.Absent)
	if !absent["rum (no user.events in the data-object catalog)"] ||
		!absent["azure (no AZURE_* entities in the live census)"] {
		t.Errorf("absent = %v", book.Facts.Absent)
	}

	// Carriage-derived scoping for present entity types only.
	if book.Scoping["K8S_POD"]["logs"].Filter == "" || book.Scoping["AZURE_VM"] != nil {
		t.Errorf("scoping: %+v", book.Scoping)
	}
	// service.name carriage on logs is 0.1 (< 0.5) → the hop strategy wins,
	// carrying dt.smartscape.service's measured 0.2 coverage.
	svcLogs := book.Scoping["SERVICE"]["logs"]
	if svcLogs.Hop != "runs_on" || svcLogs.Coverage == nil || *svcLogs.Coverage != 0.2 {
		t.Errorf("SERVICE logs rule: %+v (coverage %v)", svcLogs, derefF(svcLogs.Coverage))
	}

	// Guard disables, all classified.
	wantGuard := map[string]string{
		"needs-rum":     "capability absent",
		"undefined-cap": "undefined",
		"missing-table": "not in dt.system.data_objects",
		"low-carriage":  "minCarriage",
	}
	for name, frag := range wantGuard {
		d := book.Disabled[name]
		if d == nil || d.Class != DisabledClassGuard || !strings.Contains(d.Reason, frag) {
			t.Errorf("%s: %+v", name, d)
		}
	}

	// probe-empty classification.
	if d := book.Disabled["probe-dead"]; d == nil || d.Class != DisabledClassProbeEmpty {
		t.Errorf("probe-dead: %+v", d)
	}
	if d := book.Disabled["values-zero"]; d == nil || d.Class != DisabledClassProbeEmpty {
		t.Errorf("values-zero (expect: values, one row of zeros): %+v", d)
	}

	// Verified-empty gets a stamp, not a disable.
	if e := book.Recipes["probe-verified-empty"]; e == nil || e.LastRun == nil || e.LastRun.Empty != "legitimate" {
		t.Errorf("probe-verified-empty: %+v", e)
	}

	// Successful probe stamps with limitHit.
	good := book.Recipes["good"]
	if good == nil || good.LastRun == nil || *good.LastRun.Records != 25 || !good.LastRun.LimitHit {
		t.Errorf("good: %+v", good)
	}
	if good.Source != "pack:good@1" {
		t.Errorf("source pointer: %q", good.Source)
	}

	// Widen-on-empty records an override.
	widen := book.Recipes["widen-me"]
	if widen == nil || widen.Override == "" || *widen.LastRun.Records != 3 {
		t.Errorf("widen-me: %+v", widen)
	}

	// Required params: instantiated unprobed.
	if e := book.Recipes["needs-id"]; e == nil || e.LastRun != nil || !strings.Contains(e.Note, "requires params (id)") {
		t.Errorf("needs-id: %+v", e)
	}

	if report.Disabled != 6 || report.Unprobed != 1 {
		t.Errorf("report: %+v", report)
	}
}

func TestBuiltinScopingOTelServiceLogs(t *testing.T) {
	// On an OTel-native environment logs carry service.name — the direct name
	// filter wins over the topology hop, with its measured coverage.
	scoping := builtinScoping(
		map[string]int64{"SERVICE": 10},
		map[string]map[string]float64{"logs": {"service.name": 0.95, "dt.smartscape.service": 0.06}},
	)
	rule := scoping["SERVICE"]["logs"]
	if rule.Hop != "" || !strings.Contains(rule.Filter, "service.name") {
		t.Errorf("expected service.name filter strategy, got %+v", rule)
	}
	if rule.Coverage == nil || *rule.Coverage != 0.95 {
		t.Errorf("coverage: %v", derefF(rule.Coverage))
	}
}

func TestDiscoverFactsOnly(t *testing.T) {
	book, _, err := Discover(context.Background(), discoverTestRunner(), []*Pack{discoverTestPack()}, DiscoverOptions{
		ContextName: "test", Now: fixedNow, FactsOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(book.Recipes) != 0 || len(book.Disabled) != 0 {
		t.Errorf("facts-only must not instantiate recipes: %d/%d", len(book.Recipes), len(book.Disabled))
	}
	if len(book.Facts.Capabilities) == 0 {
		t.Error("facts-only still evaluates capabilities")
	}
}

func TestDiscoverBudgetAborts(t *testing.T) {
	book, report, err := Discover(context.Background(), discoverTestRunner(), []*Pack{discoverTestPack()}, DiscoverOptions{
		ContextName: "test", Now: fixedNow, BudgetQueries: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Queries > 4 {
		t.Errorf("budget not enforced: %d queries", report.Queries)
	}
	found := false
	for _, r := range book.Recipes {
		if strings.Contains(r.Note, "budget exhausted") {
			found = true
		}
	}
	if !found {
		t.Error("expected unprobed budget-exhausted notes")
	}
	hasNote := false
	for _, n := range book.Facts.Notes {
		if strings.Contains(n, "budget exhausted") {
			hasNote = true
		}
	}
	if !hasNote {
		t.Errorf("expected budget note in facts, got %v", book.Facts.Notes)
	}
}

func TestDiscoverCarriesLocalRecipes(t *testing.T) {
	prev := &Book{
		Recipes: map[string]*Recipe{
			"my-local": {Description: "mine", DQL: "fetch logs LOCALQ | limit 3"},
		},
		Declared: map[string]string{"ownership": "by namespace"},
	}
	runner := discoverTestRunner()
	runner.responses = append(runner.responses, mockResponse{"LOCALQ", manyRecords(2)})
	book, _, err := Discover(context.Background(), runner, []*Pack{discoverTestPack()}, DiscoverOptions{
		ContextName: "test", Now: fixedNow, PrevBook: prev,
	})
	if err != nil {
		t.Fatal(err)
	}
	local := book.Recipes["my-local"]
	if local == nil || local.DQL == "" || local.Source != "" {
		t.Fatalf("local recipe not carried: %+v", local)
	}
	if local.LastRun == nil || *local.LastRun.Records != 2 {
		t.Errorf("local recipe not re-stamped: %+v", local.LastRun)
	}
	if book.Declared["ownership"] != "by namespace" {
		t.Errorf("declared not carried: %v", book.Declared)
	}
}
