package catalog

import (
	"strings"
	"testing"
)

// The log-scope hop exists because log records rarely carry service IDs
// (validated live: a busy service with zero service-stamped log lines but
// ~2k via its process). These tests pin the hop's query shape and the
// widened entity set it produces.

func TestLogHopQueryOnlyForSingleServiceScope(t *testing.T) {
	svc := &Entity{ID: "SERVICE-A", Name: "checkout", Type: "SERVICE"}
	q := LogHopQuery(Scope{Entity: svc})
	for _, want := range []string{
		`smartscapeEdges "*"`,
		`source_id == toSmartscapeId("SERVICE-A")`,
		`type == "runs_on"`,
		`in(target_type, {"PROCESS", "CONTAINER"})`,
	} {
		if !strings.Contains(q, want) {
			t.Errorf("hop query missing %q:\n%s", want, q)
		}
	}

	// Non-service entities are stamped on their logs directly — no hop.
	host := &Entity{ID: "HOST-1", Type: "HOST"}
	if q := LogHopQuery(Scope{Entity: host}); q != "" {
		t.Errorf("HOST scope must not hop, got:\n%s", q)
	}
	// Unscoped and multi-entity scopes (a problem's affected set) don't hop.
	if q := LogHopQuery(Scope{}); q != "" {
		t.Errorf("unscoped view must not hop, got:\n%s", q)
	}
	if q := LogHopQuery(Scope{Entity: svc, Entities: []Entity{*svc, *host}}); q != "" {
		t.Errorf("multi-entity scope must not hop, got:\n%s", q)
	}
}

func TestLogHopEntitiesKeepServiceFirstAndDedupe(t *testing.T) {
	svc := &Entity{ID: "SERVICE-A", Name: "checkout", Type: "SERVICE"}
	ents := LogHopEntities(Scope{Entity: svc}, []map[string]any{
		{"target_id": "PROCESS-1", "target_type": "PROCESS"},
		{"target_id": "CONTAINER-1", "target_type": "CONTAINER"},
		{"target_id": "PROCESS-1", "target_type": "PROCESS"}, // duplicate edge
		{"target_id": "", "target_type": "PROCESS"},          // degenerate
	})
	if len(ents) != 3 {
		t.Fatalf("widened set = %v, want service + process + container", ents)
	}
	if ents[0].ID != "SERVICE-A" {
		t.Errorf("service must stay first, got %v", ents[0])
	}

	// The widened set must compose into a filter with an arm per entity:
	// the service's own IDs still match directly-stamped records.
	f := ScopeSignalFilter(Scope{Entities: ents})
	for _, want := range []string{
		`dt.smartscape.service == toSmartscapeId("SERVICE-A")`,
		`dt.smartscape.process == toSmartscapeId("PROCESS-1")`,
		`dt.smartscape.container == toSmartscapeId("CONTAINER-1")`,
	} {
		if !strings.Contains(f, want) {
			t.Errorf("widened filter missing %q:\n%s", want, f)
		}
	}

	// No hop targets: the singleton service set — same filter as before.
	solo := LogHopEntities(Scope{Entity: svc}, nil)
	if len(solo) != 1 || solo[0].ID != "SERVICE-A" {
		t.Errorf("empty hop must keep the plain service scope, got %v", solo)
	}
}

func TestLogsSpecHopIsWired(t *testing.T) {
	spec := Lookup("logs")
	if spec.Hop == nil {
		t.Fatal("logs spec must declare the service log-scope hop")
	}
	svc := &Entity{ID: "SERVICE-A", Type: "SERVICE"}
	if spec.Hop.Query(Scope{Entity: svc}) == "" {
		t.Error("logs hop must resolve for a SERVICE scope")
	}
	// The traces view scopes spans by dt.smartscape.service directly — no hop.
	if tr := Lookup("traces"); tr.Hop != nil {
		t.Error("traces spec must not hop — spans carry service IDs")
	}
}

func TestDefaultSpanLensScopedDefaults(t *testing.T) {
	// Scoped traces must not open on roots: root spans belong only to the
	// trace's entry service, so roots ANDed with an entity scope is silently
	// empty for most entities (validated live).
	for typ, want := range map[string]string{
		"SERVICE":     "all",
		"CONTAINER":   "all",
		"K8S_POD":     "all",
		"GENAI_MODEL": "genai",
		"GENAI_AGENT": "genai",
	} {
		if got := lensAt(spanLenses, DefaultSpanLens(typ)).Name; got != want {
			t.Errorf("DefaultSpanLens(%s) → %s, want %s", typ, got, want)
		}
	}
}
