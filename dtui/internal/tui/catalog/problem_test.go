package catalog

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func testProblemRec() map[string]any {
	return map[string]any{
		"display_id":                 "P-42",
		"event.kind":                 "DAVIS_PROBLEM",
		"event.name":                 "Failure rate increase",
		"event.status":               "ACTIVE",
		"event.severity":             "4",
		"event.start":                "2026-07-08T07:50:00.000000000Z",
		"dt.davis.event_ids":         []any{"111_222", "333_444"},
		"dt.davis.is_frequent_event": true,
		"smartscape.affected_entities": []any{
			map[string]any{"id": "SERVICE-1", "name": "checkout", "type": "SERVICE"},
			map[string]any{"id": "HOST-2", "name": "web-01", "type": "HOST"},
		},
	}
}

func TestTimeframeAbsoluteDQL(t *testing.T) {
	from := time.Date(2026, 7, 8, 7, 45, 0, 0, time.UTC)
	to := time.Date(2026, 7, 8, 8, 0, 0, 0, time.UTC)

	tf := Timeframe{From: from, To: to, Dur: to.Sub(from)}
	want := `toTimestamp("2026-07-08T07:45:00.000Z"), to:toTimestamp("2026-07-08T08:00:00.000Z")`
	if got := tf.DQL(); got != want {
		t.Errorf("absolute DQL = %q, want %q", got, want)
	}

	open := Timeframe{From: from, Dur: time.Hour}
	if got := open.DQL(); got != `toTimestamp("2026-07-08T07:45:00.000Z")` {
		t.Errorf("open-ended DQL = %q", got)
	}

	rel := Timeframe{Label: "2h", Dur: 2 * time.Hour}
	if got := rel.DQL(); got != "now() - 2h" {
		t.Errorf("relative DQL = %q", got)
	}
	if rel.Absolute() || !tf.Absolute() {
		t.Error("Absolute() misclassifies windows")
	}
}

func TestFloorTimeframeKeepsAbsoluteWindows(t *testing.T) {
	abs := Timeframe{From: time.Date(2026, 7, 8, 7, 45, 0, 0, time.UTC), Dur: time.Minute}
	if got := floorTimeframe(abs, 24*time.Hour, "24h"); got != abs.DQL() {
		t.Errorf("floorTimeframe widened an absolute window: %q", got)
	}
	rel := Timeframe{Label: "2h", Dur: 2 * time.Hour}
	if got := floorTimeframe(rel, 24*time.Hour, "24h"); got != "now() - 24h" {
		t.Errorf("floorTimeframe should widen a short relative window, got %q", got)
	}
}

func TestScopeSignalFilter(t *testing.T) {
	svc := Entity{ID: "SERVICE-1", Name: "checkout", Type: "SERVICE"}
	host := Entity{ID: "HOST-2", Name: "web-01", Type: "HOST"}

	if got := ScopeSignalFilter(Scope{}); got != "" {
		t.Errorf("unscoped filter = %q", got)
	}
	// A single entity composes exactly like SignalFilter (no parens).
	if got := ScopeSignalFilter(Scope{Entity: &svc}); got != SignalFilter(svc) {
		t.Errorf("single-entity filter = %q", got)
	}
	multi := ScopeSignalFilter(Scope{Entities: []Entity{svc, host}})
	if !strings.Contains(multi, "("+SignalFilter(svc)+")") ||
		!strings.Contains(multi, "("+SignalFilter(host)+")") ||
		!strings.Contains(multi, ") or (") {
		t.Errorf("multi-entity filter = %q", multi)
	}
	// Entities wins over Entity when both are set.
	if got := ScopeSignalFilter(Scope{Entity: &host, Entities: []Entity{svc}}); got != SignalFilter(svc) {
		t.Errorf("Entities should win over Entity, got %q", got)
	}
}

func TestScopeSpanFilterDropsUnscopableTypes(t *testing.T) {
	svc := Entity{ID: "SERVICE-1", Type: "SERVICE"}
	pod := Entity{ID: "K8S_POD-3", Type: "K8S_POD"}
	host := Entity{ID: "HOST-2", Type: "HOST"} // spans carry no host scope field

	got := ScopeSpanFilter(Scope{Entities: []Entity{svc, host, pod}})
	if !strings.Contains(got, `dt.smartscape.service == toSmartscapeId("SERVICE-1")`) ||
		!strings.Contains(got, `dt.smartscape.k8s_pod == toSmartscapeId("K8S_POD-3")`) {
		t.Errorf("span filter missing scopable entities: %q", got)
	}
	if strings.Contains(got, "HOST-2") {
		t.Errorf("span filter must drop unscopable HOST: %q", got)
	}
	if got := ScopeSpanFilter(Scope{Entities: []Entity{host}}); got != "" {
		t.Errorf("all-unscopable set should compose no filter, got %q", got)
	}
}

func TestProblemWindow(t *testing.T) {
	now := time.Date(2026, 7, 8, 8, 30, 0, 0, time.UTC)

	active := testProblemRec()
	w := ProblemWindow(active, now)
	if !w.Absolute() || !w.To.IsZero() {
		t.Fatalf("active problem window = %+v, want open-ended absolute", w)
	}
	wantFrom := time.Date(2026, 7, 8, 7, 45, 0, 0, time.UTC) // start - 5m pad
	if !w.From.Equal(wantFrom) {
		t.Errorf("window from = %v, want %v", w.From, wantFrom)
	}
	if w.Dur != now.Sub(wantFrom) {
		t.Errorf("window dur = %v", w.Dur)
	}

	closed := testProblemRec()
	closed["event.end"] = "2026-07-08T08:00:00.000000000Z"
	w = ProblemWindow(closed, now)
	wantTo := time.Date(2026, 7, 8, 8, 5, 0, 0, time.UTC) // end + 5m pad
	if !w.To.Equal(wantTo) {
		t.Errorf("closed window to = %v, want %v", w.To, wantTo)
	}

	// A record without a parseable start falls back to the default window.
	if w := ProblemWindow(map[string]any{}, now); w.Absolute() {
		t.Errorf("windowless record should fall back to relative, got %+v", w)
	}
}

func TestProblemRecordAccessors(t *testing.T) {
	rec := testProblemRec()
	if !IsProblem(rec) {
		t.Error("IsProblem should recognize a Davis problem record")
	}
	if IsProblem(map[string]any{"event.kind": "DAVIS_EVENT"}) {
		t.Error("IsProblem must not match Davis events")
	}
	ents := ProblemAffectedEntities(rec)
	if len(ents) != 2 || ents[0].ID != "SERVICE-1" || ents[0].Name != "checkout" || ents[1].Type != "HOST" {
		t.Errorf("affected entities = %+v", ents)
	}
	if ids := ProblemEventIDs(rec); len(ids) != 2 || ids[0] != "111_222" {
		t.Errorf("event ids = %v", ids)
	}
	// event.severity is the ITIL ordinal (1 = most severe, 5 = least severe,
	// per the semantic dictionary) — rendered as an honest SEVn badge, not an
	// ordinal word.
	if sev := ProblemSeverity(rec); sev != "SEV4" {
		t.Errorf("severity = %q, want SEV4", sev)
	}
	if flags := ProblemFlags(rec); flags != "frequent event" {
		t.Errorf("flags = %q", flags)
	}
}

func TestLatestAndActiveProblems(t *testing.T) {
	records := []map[string]any{
		{"display_id": "P-1", "event.status": "ACTIVE"}, // newest record per problem first
		{"display_id": "P-1", "event.status": "OPEN"},   // older update, dropped
		{"display_id": "P-2", "event.status": "CLOSED"},
		{"display_id": "P-3", "event.status": "ACTIVE"},
	}
	latest := LatestProblems(records)
	if len(latest) != 3 {
		t.Fatalf("latest = %d records, want 3", len(latest))
	}
	active := ActiveProblems(records)
	if len(active) != 2 || Str(active[0], "display_id") != "P-1" || Str(active[1], "display_id") != "P-3" {
		t.Errorf("active = %+v", active)
	}
}

func TestProblemPulseQuery(t *testing.T) {
	e := Entity{ID: "SERVICE-1", Type: "SERVICE"}
	q := ProblemPulseQuery(e, Timeframe{Label: "2h", Dur: 2 * time.Hour})
	if !strings.Contains(q, "now() - 24h") {
		t.Errorf("pulse lookback should floor at 24h:\n%s", q)
	}
	if !strings.Contains(q, "not(dt.davis.is_duplicate)") || !strings.Contains(q, `"SERVICE-1"`) {
		t.Errorf("pulse query = %s", q)
	}
	q = ProblemPulseQuery(e, Timeframe{Label: "7d", Dur: 7 * 24 * time.Hour})
	if !strings.Contains(q, "now() - 7d") {
		t.Errorf("pulse should follow a wider global window:\n%s", q)
	}
}

func TestChangeEventQuery(t *testing.T) {
	q := ChangeEventQuery(Entity{ID: "SERVICE-1", Type: "SERVICE"})
	for _, want := range []string{
		"fetch events, from:now() - 7d",
		`dt.smartscape.service == toSmartscapeId("SERVICE-1")`,
		"CUSTOM_DEPLOYMENT",
		`event.kind == "SDLC_EVENT"`,
		"| limit 1",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("change query missing %q:\n%s", want, q)
		}
	}
}

func TestDavisEventsSpecQuery(t *testing.T) {
	window := Timeframe{
		From: time.Date(2026, 7, 8, 7, 45, 0, 0, time.UTC),
		To:   time.Date(2026, 7, 8, 8, 5, 0, 0, time.UTC),
	}
	q := DavisEventsSpec.Query(Scope{Arg: "111_222,333_444", Timeframe: window})
	for _, want := range []string{
		"fetch dt.davis.events",
		`in(event.id, {"111_222", "333_444"})`,
		`toTimestamp("2026-07-08T07:45:00.000Z")`,
		"by:{event.id}",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("evidence query missing %q:\n%s", want, q)
		}
	}
	if Lookup("evidence") != nil {
		t.Error("DavisEventsSpec must stay out of the command-bar registry")
	}
}

func TestPriorityFieldsPerKind(t *testing.T) {
	cases := []struct {
		name string
		rec  map[string]any
		want string // a field that must lead the highlight set
	}{
		{"problem", map[string]any{"event.kind": "DAVIS_PROBLEM"}, "display_id"},
		{"davis event", map[string]any{"event.kind": "DAVIS_EVENT"}, "event.name"},
		{"vulnerability", map[string]any{"vulnerability.id": "V-1"}, "title"},
		{"span", map[string]any{"span.kind": "server"}, "span.name"},
		{"session", map[string]any{"user_action_count": "5"}, "start_time"},
		{"rum event", map[string]any{"characteristics.classifier": "Request"}, "start_time"},
		{"synthetic", map[string]any{"monitor.name": "checkout flow"}, "timestamp"},
		{"log (default)", map[string]any{"content": "boom"}, "content"},
	}
	for _, c := range cases {
		fields := PriorityFields(c.rec)
		if len(fields) == 0 || fields[0] != c.want {
			t.Errorf("%s: PriorityFields[0] = %v, want %q", c.name, fields, c.want)
		}
	}

	// A span's highlight set names whichever service field the record carries
	// (extension spans have only dt.service.name), never both at once.
	svcCases := []struct {
		rec  map[string]any
		want string
	}{
		{map[string]any{"span.kind": "client", "service.name": "checkout"}, "service.name"},
		{map[string]any{"span.kind": "client", "dt.service.name": "ext"}, "dt.service.name"},
		{map[string]any{"span.kind": "client", "service.name": "checkout", "dt.service.name": "checkout"}, "service.name"},
	}
	for _, c := range svcCases {
		fields := PriorityFields(c.rec)
		if !slices.Contains(fields, c.want) {
			t.Errorf("span PriorityFields = %v, want %q listed (rec %v)", fields, c.want, c.rec)
		}
	}
}

func TestKeyFactsCoverNewTypes(t *testing.T) {
	genai := KeyFacts("GENAI_MODEL")
	found := false
	for _, f := range genai {
		if f.Label == "provider" {
			found = true
			if v := f.Value(map[string]any{"gen_ai.provider.name": "openai"}); v != "openai" {
				t.Errorf("provider fact = %q", v)
			}
		}
	}
	if !found {
		t.Error("GENAI_* types should carry a provider fact")
	}
	ns := KeyFacts("K8S_NAMESPACE")
	found = false
	for _, f := range ns {
		if f.Label == "cluster" {
			found = true
		}
	}
	if !found {
		t.Error("K8S_NAMESPACE should carry a cluster fact")
	}
}
