package recipes

import (
	"context"
	"strings"
	"testing"
)

func scopeTestBook() *Book {
	cov := func(f float64) *float64 { return &f }
	return &Book{
		Scoping: map[string]map[string]ScopeRule{
			"SERVICE": {
				"logs":  {Hop: "runs_on", Coverage: cov(0.06)},
				"spans": {Filter: `dt.smartscape.service == toSmartscapeId("{{.id}}")`, Coverage: cov(1.0)},
			},
			"K8S_POD": {
				"logs": {Filter: `k8s.pod.name == "{{.name}}"`, Coverage: cov(0.58)},
			},
		},
	}
}

func scopeTestRunner() *mockRunner {
	return &mockRunner{responses: []mockResponse{
		// name lookup: two SERVICE instances plus an OTEL_PROCESS sharing the name
		{`smartscapeNodes "*" | filter name == "backend"`, []map[string]interface{}{
			rec("id", "SERVICE-AAA1111100000000", "name", "backend", "type", "SERVICE"),
			rec("id", "SERVICE-BBB2222200000000", "name", "backend", "type", "SERVICE"),
			rec("id", "OTEL_PROCESS-CCC3333300000000", "name", "backend", "type", "OTEL_PROCESS"),
		}},
		{`filter source_id == toSmartscapeId("SERVICE-AAA1111100000000")`, []map[string]interface{}{
			rec("target_id", "K8S_POD-0000000000000001", "tname", "backend-pod-1"),
			rec("target_id", "CONTAINER-000000000000000A", "tname", "backend"),
		}},
		{`filter source_id == toSmartscapeId("SERVICE-BBB2222200000000")`, []map[string]interface{}{
			rec("target_id", "K8S_POD-0000000000000002", "tname", "backend-pod-2"),
		}},
		{`smartscapeNodes "K8S_POD" | filter id == toSmartscapeId("K8S_POD-0000000000000001")`, []map[string]interface{}{
			rec("id", "K8S_POD-0000000000000001", "name", "backend-pod-1"),
		}},
	}}
}

func TestResolveScopeHop(t *testing.T) {
	res, err := ResolveScope(context.Background(), scopeTestRunner(), scopeTestBook(), "backend", "logs")
	if err != nil {
		t.Fatalf("ResolveScope: %v", err)
	}
	if res.Strategy != "hop:runs_on" {
		t.Errorf("strategy: %s", res.Strategy)
	}
	// Both instances' pods are in the filter — partial instance coverage is
	// the undercount trap this exists to prevent.
	if !strings.Contains(res.Filter, "backend-pod-1") || !strings.Contains(res.Filter, "backend-pod-2") {
		t.Errorf("filter misses pods: %s", res.Filter)
	}
	if !strings.HasPrefix(res.Filter, "in(k8s.pod.name, {") {
		t.Errorf("filter shape: %s", res.Filter)
	}
	if len(res.Entities) != 2 {
		t.Errorf("entities: %+v", res.Entities)
	}
	// The OTEL_PROCESS name-mate is ignored with a note, not a hard error.
	found := false
	for _, n := range res.Notes {
		if strings.Contains(n, "OTEL_PROCESS") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ignored-type note, got %v", res.Notes)
	}
}

func TestResolveScopeFilterByID(t *testing.T) {
	res, err := ResolveScope(context.Background(), scopeTestRunner(), scopeTestBook(), "K8S_POD-0000000000000001", "logs")
	if err != nil {
		t.Fatalf("ResolveScope: %v", err)
	}
	if res.Strategy != "filter" || res.Filter != `k8s.pod.name == "backend-pod-1"` {
		t.Errorf("got %s / %s", res.Strategy, res.Filter)
	}
}

func TestResolveScopeErrors(t *testing.T) {
	if _, err := ResolveScope(context.Background(), scopeTestRunner(), nil, "backend", "logs"); err == nil {
		t.Error("expected error without a book")
	}
	if _, err := ResolveScope(context.Background(), scopeTestRunner(), scopeTestBook(), "backend", "problems"); err == nil {
		t.Error("expected error for a signal without rules")
	}
	if _, err := ResolveScope(context.Background(), scopeTestRunner(), scopeTestBook(), "nope-name", "logs"); err == nil {
		t.Error("expected error for an unknown entity")
	}
}
