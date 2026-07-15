package catalog

import (
	"strings"
	"testing"
)

// The facet/search injectors (facets.go) rewrite generated DQL by line
// convention instead of a structured pipeline type: the source command owns
// the first line (InjectSearches inserts after it — DQL rejects `search`
// after transforming commands), and the trailing `| sort`/`| limit` block is
// where InjectStages splices narrowing in front of. These tests enforce the
// conventions on every spec so a drifting Query closure fails here instead
// of silently mis-splicing at runtime.

// conventionScopes are the fixture scopes every query renders against.
func conventionScopes() []Scope {
	tf := Timeframe{Label: "2h"}
	return []Scope{
		{Timeframe: tf},
		{Timeframe: tf, Entity: &Entity{ID: "HOST-0000000000000001", Name: "web-1", Type: "HOST"}},
		{Timeframe: tf, Entity: &Entity{ID: "K8S_POD-0000000000000002", Name: "checkout-1", Type: "K8S_POD"}},
		{Timeframe: tf, Arg: "argvalue"},
	}
}

func conventionSpecs() []*Spec {
	return append(All(),
		DavisEventsSpec, VulnEntitiesSpec, VulnAttacksSpec, VulnEntryPointsSpec, VulnTimelineSpec)
}

// sourceCommands are the pipeline heads catalog queries start with.
var sourceCommands = []string{"fetch ", "smartscapeNodes", "smartscapeEdges", "timeseries ", "metrics "}

func TestQueryConventionSourceOwnsFirstLine(t *testing.T) {
	for _, spec := range conventionSpecs() {
		if spec.Query == nil {
			continue
		}
		for _, scope := range conventionScopes() {
			maxLens := len(spec.Lenses)
			if maxLens == 0 {
				maxLens = 1
			}
			for lens := 0; lens < maxLens; lens++ {
				scope.Lens = lens
				dql := spec.Query(scope)
				if dql == "" {
					continue
				}
				first := strings.SplitN(dql, "\n", 2)[0]
				ok := false
				for _, src := range sourceCommands {
					if strings.HasPrefix(first, src) {
						ok = true
						break
					}
				}
				if !ok {
					t.Errorf("%s (lens %d): first line is not a source command — search injection would land mid-pipeline:\n%s",
						spec.Name, lens, first)
				}
				if strings.Contains(first, " | ") {
					t.Errorf("%s (lens %d): first line chains stages inline — searches must inject after the bare source:\n%s",
						spec.Name, lens, first)
				}
			}
		}
	}
}

// Every query must end in the sort/limit tail the stage injector splices in
// front of — a tail-less query would take facet filters after its last stage
// (still correct), but a MID-pipeline trailing block (e.g. ending on a bare
// summarize after a sort) would put filters before the aggregation that
// renames the very fields they reference.
func TestQueryConventionSortLimitTail(t *testing.T) {
	for _, spec := range conventionSpecs() {
		if spec.Query == nil {
			continue
		}
		for _, scope := range conventionScopes() {
			maxLens := len(spec.Lenses)
			if maxLens == 0 {
				maxLens = 1
			}
			for lens := 0; lens < maxLens; lens++ {
				scope.Lens = lens
				dql := spec.Query(scope)
				if dql == "" {
					continue
				}
				lines := strings.Split(dql, "\n")
				if cut := tailStart(lines); cut == len(lines) {
					t.Errorf("%s (lens %d): query has no trailing | sort / | limit — facet stages would append at the very end; add the conventional tail:\n%s",
						spec.Name, lens, dql)
				}
			}
		}
	}
}
