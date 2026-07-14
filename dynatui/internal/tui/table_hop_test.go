package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
)

// The logs view scoped to a SERVICE resolves the service's runtime
// processes/containers in a pre-query (Spec.Hop) before the first fetch —
// log records rarely carry service IDs. These tests drive a bare tableView
// through the two-phase load with a scripted data source.

// runCmds executes a command tree against one view, feeding dataMsg (the
// scripted runFn results) back into it and dropping everything else.
func runCmds(v *tableView, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if msg == nil {
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			runCmds(v, c)
		}
		return
	}
	if _, ok := msg.(dataMsg); ok {
		runCmds(v, v.Update(msg))
	}
}

func serviceLogsView(runFn func(dql string) ([]map[string]any, error)) *tableView {
	ds := &dataSource{runFn: runFn}
	svc := &catalog.Entity{ID: "SERVICE-A", Name: "checkout", Type: "SERVICE"}
	return newTableView(ds, catalog.Lookup("logs"),
		catalog.Scope{Entity: svc, Timeframe: catalog.DefaultTimeframe})
}

func TestLogsViewHopsToRuntimeEntities(t *testing.T) {
	var queries []string
	v := serviceLogsView(func(dql string) ([]map[string]any, error) {
		queries = append(queries, dql)
		if strings.HasPrefix(dql, "smartscapeEdges") {
			return []map[string]any{
				{"target_id": "PROCESS-1", "target_type": "PROCESS"},
				{"target_id": "CONTAINER-1", "target_type": "CONTAINER"},
			}, nil
		}
		return []map[string]any{{"timestamp": "2026-07-12T00:00:00Z", "content": "hello"}}, nil
	})
	runCmds(v, v.Init())

	if len(queries) != 2 {
		t.Fatalf("expected hop + list queries, got %d:\n%s", len(queries), strings.Join(queries, "\n---\n"))
	}
	if !strings.HasPrefix(queries[0], "smartscapeEdges") {
		t.Errorf("first query must be the edge hop:\n%s", queries[0])
	}
	for _, want := range []string{
		`dt.smartscape.service == toSmartscapeId("SERVICE-A")`,
		`dt.smartscape.process == toSmartscapeId("PROCESS-1")`,
		`dt.smartscape.container == toSmartscapeId("CONTAINER-1")`,
	} {
		if !strings.Contains(queries[1], want) {
			t.Errorf("widened logs query missing %q:\n%s", want, queries[1])
		}
	}
	if v.loading || v.err != nil || len(v.all) != 1 {
		t.Errorf("view did not settle on the fetched rows: loading=%v err=%v rows=%d", v.loading, v.err, len(v.all))
	}
	if v.Echo() == "" || !strings.Contains(v.Echo(), "PROCESS-1") {
		t.Errorf("echo must show the widened query, got %q", v.Echo())
	}

	// The hop resolves once — a refresh (timeframe change, lens cycle)
	// reuses the widened scope instead of re-walking the topology.
	runCmds(v, v.Refresh())
	if len(queries) != 3 || strings.HasPrefix(queries[2], "smartscapeEdges") {
		t.Errorf("refresh must reuse the widened scope, queries:\n%s", strings.Join(queries, "\n---\n"))
	}
}

func TestLogsViewHopFailureDegradesToServiceScope(t *testing.T) {
	var queries []string
	v := serviceLogsView(func(dql string) ([]map[string]any, error) {
		queries = append(queries, dql)
		if strings.HasPrefix(dql, "smartscapeEdges") {
			return nil, errors.New("edges unavailable")
		}
		return nil, nil
	})
	runCmds(v, v.Init())

	if len(queries) != 2 {
		t.Fatalf("expected hop + list queries, got %d", len(queries))
	}
	if !strings.Contains(queries[1], `dt.smartscape.service == toSmartscapeId("SERVICE-A")`) {
		t.Errorf("fallback logs query must keep the service filter:\n%s", queries[1])
	}
	if strings.Contains(queries[1], "PROCESS") {
		t.Errorf("failed hop must not leak entities into the query:\n%s", queries[1])
	}
	if v.err != nil {
		t.Errorf("a hop failure must not fail the view: %v", v.err)
	}
}

func TestLogsViewSkipsHopWhenNotServiceScoped(t *testing.T) {
	var queries []string
	ds := &dataSource{runFn: func(dql string) ([]map[string]any, error) {
		queries = append(queries, dql)
		return nil, nil
	}}
	host := &catalog.Entity{ID: "HOST-1", Name: "web-1", Type: "HOST"}
	v := newTableView(ds, catalog.Lookup("logs"),
		catalog.Scope{Entity: host, Timeframe: catalog.DefaultTimeframe})
	runCmds(v, v.Init())

	if len(queries) != 1 || !strings.HasPrefix(queries[0], "fetch logs") {
		t.Fatalf("non-service scope must fetch directly, queries:\n%s", strings.Join(queries, "\n---\n"))
	}
}
