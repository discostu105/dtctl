package tui

import (
	"strings"
	"testing"

	"github.com/dynatrace-oss/dynatui/internal/tui/catalog"
)

func TestIntentLinks(t *testing.T) {
	env := "https://abc.apps.dynatrace.com"
	first := func(opts []linkOption) string {
		if len(opts) == 0 {
			return ""
		}
		return opts[0].URL
	}
	problem := first(linkOptionsFor(env, map[string]any{"event.kind": "DAVIS_PROBLEM", "event.id": "e-1"}, nil, "", ""))
	if !strings.Contains(problem, "/ui/intent/dynatrace.davis.problems/view-problem#") {
		t.Errorf("problem link = %s", problem)
	}
	trace := first(linkOptionsFor(env, nil, nil, "abc123", ""))
	if !strings.Contains(trace, "/ui/intent/dynatrace.distributedtracing/view-trace#") {
		t.Errorf("trace link = %s", trace)
	}
	pod := linkOptionsFor(env, nil, &catalog.Entity{ID: "K8S_POD-1", Type: "K8S_POD"}, "", "")
	if !strings.Contains(first(pod), "/ui/intent/dynatrace.kubernetes/view-entity-dt.smartscape.k8s_pod#") {
		t.Errorf("pod link = %s", first(pod))
	}
	// Typed entities offer their app plus the topology explorer.
	if len(pod) != 2 || !strings.Contains(pod[1].URL, "/ui/intent/dynatrace.smartscape/view_topology_in_context#") {
		t.Errorf("pod options = %+v, want app + topology", pod)
	}
	generic := first(linkOptionsFor(env, nil, &catalog.Entity{ID: "DISK-1", Type: "DISK"}, "", ""))
	if !strings.Contains(generic, "/ui/intent/dynatrace.smartscape/view_topology_in_context#") {
		t.Errorf("generic link = %s", generic)
	}
}

// TestOpenWithOptions covers the picker composition: record-carried URLs
// (the vulnerability page's designated link) and the query-as-notebook
// fallback both surface as targets.
func TestOpenWithOptions(t *testing.T) {
	env := "https://abc.apps.dynatrace.com"
	vuln := map[string]any{
		"display_id": "S-4",
		"url":        "https://abc.live.dynatrace.com/ui/security/problem/129",
	}
	opts := linkOptionsFor(env, vuln, nil, "", "fetch security.events")
	if len(opts) != 2 {
		t.Fatalf("options = %+v, want record url + notebook", opts)
	}
	if opts[0].URL != vuln["url"] {
		t.Errorf("first option = %+v, want the record's url field", opts[0])
	}
	if !strings.Contains(opts[1].URL, "/ui/intent/dynatrace.notebooks/view-query#") {
		t.Errorf("second option = %+v, want notebook query", opts[1])
	}
	// Nothing at all → no options.
	if got := linkOptionsFor(env, nil, nil, "", ""); len(got) != 0 {
		t.Errorf("empty selection produced options: %+v", got)
	}
}
