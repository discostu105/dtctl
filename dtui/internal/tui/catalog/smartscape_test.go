package catalog

import (
	"strings"
	"testing"
)

func TestEdgesQueryMatchesBothDirections(t *testing.T) {
	dql := EdgesQuery("K8S_POD-1")
	if !strings.Contains(dql, `source_id == toSmartscapeId("K8S_POD-1") or target_id == toSmartscapeId("K8S_POD-1")`) {
		t.Errorf("edges query must match both directions:\n%s", dql)
	}
}

func TestNamesQueryCastsEveryID(t *testing.T) {
	dql := NamesQuery([]string{"HOST-1", "SERVICE-2"})
	if !strings.Contains(dql, `in(id, {toSmartscapeId("HOST-1"), toSmartscapeId("SERVICE-2")})`) {
		t.Errorf("names query must cast every id:\n%s", dql)
	}
}

func TestBuildEdgesSplitsDirections(t *testing.T) {
	edges := BuildEdges("K8S_POD-1", []map[string]any{
		{"source_id": "K8S_POD-1", "target_id": "K8S_NODE-1", "type": "runs_on", "target_type": "K8S_NODE"},
		{"source_id": "K8S_SERVICE-1", "target_id": "K8S_POD-1", "type": "routes_to", "source_type": "K8S_SERVICE"},
	})
	if len(edges) != 2 {
		t.Fatalf("edges = %d", len(edges))
	}
	if !edges[0].Outgoing || edges[0].OtherID != "K8S_NODE-1" || edges[0].OtherType != "K8S_NODE" {
		t.Errorf("outgoing edge = %+v", edges[0])
	}
	if edges[1].Outgoing || edges[1].OtherID != "K8S_SERVICE-1" {
		t.Errorf("incoming edge = %+v", edges[1])
	}
}

func TestBuildEdgesOrdersStructureBeforeMesh(t *testing.T) {
	edges := BuildEdges("HOST-1", []map[string]any{
		{"source_id": "HOST-1", "target_id": "HOST-2", "type": "calls", "target_type": "HOST"},
		{"source_id": "PROCESS-1", "target_id": "HOST-1", "type": "runs_on", "source_type": "PROCESS"},
	})
	if len(edges) != 2 || edges[0].Verb != "runs_on" || edges[1].Verb != "calls" {
		t.Errorf("structure must sort before mesh: %+v", edges)
	}
}

func TestBuildEdgesDropsSelfAndEmpty(t *testing.T) {
	edges := BuildEdges("HOST-1", []map[string]any{
		{"source_id": "HOST-1", "target_id": "HOST-1", "type": "calls", "target_type": "HOST"},
		{"source_id": "HOST-1", "target_id": "", "type": "calls"},
	})
	if len(edges) != 0 {
		t.Errorf("self-loops and empty endpoints must drop: %+v", edges)
	}
}

func TestSchemaQueryMaterializesLazyTypesBeforeSummarize(t *testing.T) {
	dql := SchemaQuery()
	add := strings.Index(dql, "fieldsAdd source_type, target_type")
	sum := strings.Index(dql, "summarize")
	if add < 0 || sum < 0 || add > sum {
		t.Errorf("source_type/target_type are lazy projections and must be fieldsAdd-ed before the summarize:\n%s", dql)
	}
}

func TestBuildSchemaParsesAndDropsDegenerates(t *testing.T) {
	schema := BuildSchema([]map[string]any{
		{"source_type": "SERVICE", "type": "calls", "target_type": "SERVICE", "count": "1200"},
		{"source_type": "K8S_POD", "type": "runs_on", "target_type": "K8S_NODE", "count": float64(890)},
		{"source_type": "", "type": "", "target_type": "", "count": float64(1)},
	})
	if len(schema) != 2 {
		t.Fatalf("schema = %+v", schema)
	}
	if schema[0].Count != 1200 || schema[1].Count != 890 {
		t.Errorf("counts must parse from both string and float: %+v", schema)
	}
}

func TestTypeInstancesQueryFallsBackToTagAndARN(t *testing.T) {
	dql := TypeInstancesQuery("AWS_EC2_INSTANCE")
	if !strings.Contains(dql, `smartscapeNodes "AWS_EC2_INSTANCE"`) {
		t.Errorf("query must fetch the requested type:\n%s", dql)
	}
	if !strings.Contains(dql, "coalesce(if(name != \"\", name), `tags:aws`[`Name`], aws.arn)") {
		t.Errorf("name-poor AWS nodes need the display fallback:\n%s", dql)
	}
}

func TestNameSearchQueryWrapsAndSanitizesTheTerm(t *testing.T) {
	dql := NameSearchQuery("payments")
	if !strings.Contains(dql, `matchesValue(name, "*payments*")`) {
		t.Errorf("name search must wrap the term for contains semantics:\n%s", dql)
	}
	// matchesValue allows wildcards only at either end — user-typed stars
	// inside the term must not reach the pattern.
	dql = NameSearchQuery("pay*ments*")
	if !strings.Contains(dql, `matchesValue(name, "*payments*")`) {
		t.Errorf("embedded stars must be stripped:\n%s", dql)
	}
}

func TestProblemAffectedIDsCoversBothEras(t *testing.T) {
	ids := ProblemAffectedIDs(map[string]any{
		"smartscape.affected_entities": []any{
			map[string]any{"id": "SERVICE-NEW1", "name": "checkout", "type": "SERVICE"},
		},
		"affected_entity_ids": []any{"SERVICE-OLD1", "SERVICE-NEW1"},
	})
	want := []string{"SERVICE-NEW1", "SERVICE-OLD1"}
	if len(ids) != len(want) || ids[0] != want[0] || ids[1] != want[1] {
		t.Errorf("ids = %v, want %v (both eras, deduped)", ids, want)
	}
}

func TestIntValueCoercesGrailNumbers(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want int
	}{
		{float64(42), 42}, {"1200", 1200}, {7, 7}, {nil, 0}, {"x", 0},
	} {
		if got := IntValue(tc.in); got != tc.want {
			t.Errorf("IntValue(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestMeshVerb(t *testing.T) {
	if !MeshVerb("calls") || !MeshVerb("routes_to") || MeshVerb("runs_on") || MeshVerb("is_part_of") {
		t.Error("mesh verbs are calls and routes_to")
	}
}
