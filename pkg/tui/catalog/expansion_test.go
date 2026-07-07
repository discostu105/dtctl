package catalog

import (
	"strings"
	"testing"
	"time"
)

// Query-composition tests for the expansion views (RUM, bizevents, semantic
// dictionary, data explorer, synthetic, log patterns). These are the same
// golden-DQL-shape checks the earlier catalog slices use: cheap regression
// protection for scope composition.

func TestSessionsQueryFloorsTimeframe(t *testing.T) {
	short := sessionsSpec.Query(Scope{Timeframe: Timeframes[0]}) // 30m
	if !strings.Contains(short, "from:now() - 24h") {
		t.Errorf("sessions must floor the window at 24h:\n%s", short)
	}
	week := Timeframe{Label: "7d", Dur: 7 * 24 * time.Hour}
	long := sessionsSpec.Query(Scope{Timeframe: week})
	if !strings.Contains(long, "from:now() - 7d") {
		t.Errorf("a wider window must pass through:\n%s", long)
	}
}

func TestSessionsScopeByFrontend(t *testing.T) {
	fe := Entity{ID: "FRONTEND-1", Name: "shop", Type: "FRONTEND"}
	dql := sessionsSpec.Query(Scope{Timeframe: DefaultTimeframe, Entity: &fe})
	// Sessions carry the frontend as an ARRAY — the filter must go through
	// the rendered-array match, not a scalar comparison.
	if !strings.Contains(dql, `matchesPhrase(arrayToString(dt.smartscape.frontend`) {
		t.Errorf("sessions frontend scope must match the array field:\n%s", dql)
	}
	if !sessionsSpec.CanScope(DefaultTimeframe, fe) {
		t.Error("sessions must accept a FRONTEND pin")
	}
	if sessionsSpec.CanScope(DefaultTimeframe, Entity{ID: "HOST-1", Type: "HOST"}) {
		t.Error("sessions must reject a HOST pin (query would not change)")
	}
}

func TestUserEventsSessionTimeline(t *testing.T) {
	dql := userEventsSpec.Query(Scope{Timeframe: DefaultTimeframe, Arg: "SESSION-1"})
	if !strings.Contains(dql, `| filter dt.rum.session.id == "SESSION-1"`) {
		t.Errorf("session arg must filter by dt.rum.session.id:\n%s", dql)
	}
	if !strings.Contains(dql, "sort start_time asc") {
		t.Errorf("a session timeline reads oldest-first:\n%s", dql)
	}
}

func TestUserEventsFrontendScopeAndLens(t *testing.T) {
	fe := Entity{ID: "FRONTEND-1", Type: "FRONTEND"}
	dql := userEventsSpec.Query(Scope{Timeframe: DefaultTimeframe, Entity: &fe, Lens: 1})
	if !strings.Contains(dql, `dt.smartscape.frontend == toSmartscapeId("FRONTEND-1")`) {
		t.Errorf("user.events frontend scope is a scalar smartscape id:\n%s", dql)
	}
	if !strings.Contains(dql, `characteristics.classifier == "error"`) {
		t.Errorf("lens 1 must slice errors:\n%s", dql)
	}
	// There is NO event.type on user.events — the classifier is the
	// discriminator (validated live); guard against regressions to it.
	if strings.Contains(dql, "event.type") {
		t.Errorf("user.events has no event.type field:\n%s", dql)
	}
}

func TestBizeventsFloorsTimeframe(t *testing.T) {
	dql := bizeventsSpec.Query(Scope{Timeframe: DefaultTimeframe}) // 2h
	if !strings.Contains(dql, "from:now() - 24h") {
		t.Errorf("bizevents must floor the window at 24h:\n%s", dql)
	}
}

func TestFieldsQueryJoinsModel(t *testing.T) {
	dql := fieldsSpec.Query(Scope{Timeframe: DefaultTimeframe, Arg: "span"})
	for _, want := range []string{
		`| filter name == "span"`,
		"| expand fields",
		"kind: leftOuter", // 9 of span's 70 declared fields have no fields row
	} {
		if !strings.Contains(dql, want) {
			t.Errorf("fields(model) query must contain %q:\n%s", want, dql)
		}
	}
	flat := fieldsSpec.Query(Scope{Timeframe: DefaultTimeframe, Lens: 3})
	if !strings.Contains(flat, `stability == "deprecated"`) {
		t.Errorf("stability lens must filter:\n%s", flat)
	}
}

func TestRecordsSamplerArgForms(t *testing.T) {
	tf := Scope{Timeframe: DefaultTimeframe}
	cases := []struct{ arg, want string }{
		{"logs", "fetch logs\n| limit 200"},
		{"logs@default_logs", "fetch logs\n| filter dt.system.bucket == \"default_logs\"\n| limit 200"},
		{"load:/lookups/teams/v1", "load \"/lookups/teams/v1\"\n| limit 200"},
	}
	for _, c := range cases {
		tf.Arg = c.arg
		if got := recordsSpec.Query(tf); got != c.want {
			t.Errorf("records(%q) =\n%s\nwant\n%s", c.arg, got, c.want)
		}
	}
	// Table names compose into DQL — anything outside the safe alphabet is
	// refused and falls back to the catalog browse.
	tf.Arg = `logs" | fieldsRemove x | fetch `
	if got := recordsSpec.Query(tf); !strings.Contains(got, "dt.system.data_objects") {
		t.Errorf("hostile arg must fall back to the catalog:\n%s", got)
	}
}

func TestTablesEnterRefusesNonFetchable(t *testing.T) {
	fetchable := map[string]any{"name": "logs", "usable_with": []any{"fetch", "describe"}}
	if got := tablesSpec.EnterArg(fetchable); got != "logs" {
		t.Errorf("fetchable table EnterArg = %q", got)
	}
	snapshot := map[string]any{"name": "metrics", "usable_with": []any{"fieldsSnapshot"}}
	if got := tablesSpec.EnterArg(snapshot); got != "" {
		t.Errorf("fieldsSnapshot-only table must refuse enter, got %q", got)
	}
}

func TestBucketAndFileEnterArgs(t *testing.T) {
	bucket := map[string]any{"name": "default_logs", "dt.system.table": "logs"}
	if got := bucketsSpec.EnterArg(bucket); got != "logs@default_logs" {
		t.Errorf("bucket EnterArg = %q", got)
	}
	file := map[string]any{"name": "/lookups/teams/v1"}
	if got := filesSpec.EnterArg(file); got != "load:/lookups/teams/v1" {
		t.Errorf("file EnterArg = %q", got)
	}
}

func TestSyntheticLensSwitchesSource(t *testing.T) {
	all := syntheticSpec.Query(Scope{Timeframe: DefaultTimeframe})
	if !strings.Contains(all, "append [fetch dt.entity.http_check") {
		t.Errorf("all lens must union both monitor tables:\n%s", all)
	}
	http := syntheticSpec.Query(Scope{Timeframe: DefaultTimeframe, Lens: 2})
	if strings.Contains(http, "synthetic_test") || !strings.Contains(http, "dt.entity.http_check") {
		t.Errorf("http lens must fetch only http checks:\n%s", http)
	}
}

func TestSyntheticEnrichUnionsBothFamilies(t *testing.T) {
	dql := syntheticSpec.Enrich.Query(DefaultTimeframe, []string{"SYNTHETIC_TEST-1", "HTTP_CHECK-1"})
	for _, want := range []string{
		"dt.synthetic.browser.availability",
		"dt.synthetic.http.availability",
		`in(dt.entity.synthetic_test, {"SYNTHETIC_TEST-1", "HTTP_CHECK-1"})`,
		"| fieldsAdd key = dt.entity.http_check",
	} {
		if !strings.Contains(dql, want) {
			t.Errorf("synthetic enrich must contain %q:\n%s", want, dql)
		}
	}
}

func TestExecutionsScopedByMonitor(t *testing.T) {
	dql := executionsSpec.Query(Scope{Timeframe: DefaultTimeframe, Arg: "HTTP_CHECK-1", Lens: 3})
	if !strings.Contains(dql, `| filter dt.synthetic.monitor.id == "HTTP_CHECK-1"`) {
		t.Errorf("executions must scope by monitor id:\n%s", dql)
	}
	if !strings.Contains(dql, `result.state != "SUCCESS"`) {
		t.Errorf("failed lens must filter:\n%s", dql)
	}
}

func TestPatternsQueryShape(t *testing.T) {
	pod := Entity{ID: "K8S_POD-1", Name: "checkout-1", Type: "K8S_POD"}
	dql := patternsSpec.Query(Scope{Timeframe: DefaultTimeframe, Entity: &pod})
	// The analyzer's logQuery must project exactly what the schema demands.
	if !strings.Contains(dql, "| fields timestamp, content") {
		t.Errorf("patterns logQuery must project timestamp and content:\n%s", dql)
	}
	if !strings.Contains(dql, `k8s.pod.name == "checkout-1"`) {
		t.Errorf("patterns must compose the entity scope:\n%s", dql)
	}
	if patternsSpec.API != "log-patterns" {
		t.Errorf("patterns view must run through the log-patterns source, got %q", patternsSpec.API)
	}
}

func TestLogsPatternScope(t *testing.T) {
	dql := logsSpec.Query(Scope{Timeframe: DefaultTimeframe, Pattern: `IPADDR:ip ' - - '`})
	if !strings.Contains(dql, `| filter matchesPattern(content, "IPADDR:ip ' - - '")`) {
		t.Errorf("logs must compose the pattern scope:\n%s", dql)
	}
}

func TestAPIViewsHaveNoQueryButEcho(t *testing.T) {
	for _, spec := range []*Spec{slosSpec, detectorsSpec} {
		if spec.Query != nil {
			t.Errorf("%s: API-only views carry no DQL", spec.Name)
		}
		if spec.API == "" || spec.Echo == nil {
			t.Errorf("%s: API views need a source name and a CLI echo", spec.Name)
		}
		if spec.CanScope(DefaultTimeframe, Entity{ID: "HOST-1", Type: "HOST"}) {
			t.Errorf("%s: a pin can never scope an API view without a query", spec.Name)
		}
	}
}

func TestExpansionAliasesResolve(t *testing.T) {
	for alias, want := range map[string]string{
		"se": "sessions", "ue": "userevents", "biz": "bizevents",
		"dict": "models", "fld": "fields", "tbl": "tables",
		"bkt": "buckets", "lookups": "files", "syn": "synthetic",
		"slo": "slos", "ad": "detectors", "pat": "patterns", "runs": "executions",
	} {
		spec := Lookup(alias)
		if spec == nil || spec.Name != want {
			t.Errorf("Lookup(%q) = %v, want %s", alias, spec, want)
		}
	}
}
