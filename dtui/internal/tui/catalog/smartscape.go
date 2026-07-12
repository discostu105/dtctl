package catalog

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Smartscape topology helpers: the queries and record accessors behind the
// relations panel ('x') and the smartscape navigator (:nav). Facts validated
// live (see docs/TUI_LEARNINGS.md §1): smartscapeNodes/smartscapeEdges are
// commands, not tables; ids must be compared via toSmartscapeId() (a plain
// string comparison silently matches nothing); edge records carry no names
// (resolve them with a second batched nodes query); source_type/target_type
// are lazy projections that must be materialized with fieldsAdd; and some
// edge endpoints (K8S_SECRET, K8S_CONFIGMAP) have no node record at all.

// Edge is one Smartscape edge as seen from a self entity: the direction, the
// relationship verb, and the entity on the other end.
type Edge struct {
	Outgoing  bool
	Verb      string // edge type: is_part_of, runs_on, calls, routes_to, …
	OtherID   string
	OtherType string
}

// EdgeQueryLimit caps EdgesQuery — a full page means the edge count is a
// floor, not a total (high-degree nodes like a busy cluster exceed it).
const EdgeQueryLimit = 200

// EdgesQuery fetches both edge directions in one query (validated live —
// source-only misses incoming routes_to / is_part_of edges).
func EdgesQuery(id string) string {
	return fmt.Sprintf(`smartscapeEdges "*"
| filter source_id == toSmartscapeId(%[1]q) or target_id == toSmartscapeId(%[1]q)
| fields source_id, source_type, type, target_id, target_type
| limit %[2]d`, id, EdgeQueryLimit)
}

// NamesQuery resolves display names for a set of entity ids in one batched
// nodes query (edge records carry no names).
func NamesQuery(ids []string) string {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = fmt.Sprintf("toSmartscapeId(%q)", id)
	}
	return fmt.Sprintf("smartscapeNodes \"*\"\n| filter in(id, {%s})\n| fields id, name, type\n| limit 200",
		strings.Join(quoted, ", "))
}

// EdgeRank orders relation rows for reading: structure (what this thing runs
// on, owns, is part of — a host's processes, containers, K8s node) before the
// communication mesh (calls, routes_to), which on a busy host is a hundred
// host→host rows that would bury the structure.
func EdgeRank(verb string) int {
	switch verb {
	case "calls", "routes_to":
		return 1
	}
	return 0
}

// MeshVerb reports whether a relationship verb belongs to the communication
// mesh rather than the structural skeleton (the navigator's structure-only
// toggle hides mesh edges).
func MeshVerb(verb string) bool { return EdgeRank(verb) > 0 }

// BuildEdges turns edge records into direction-aware edges: structural edges
// first, outgoing before incoming, grouped by verb, neighbors ordered by type
// then id. The order deliberately ignores display names — they resolve in a
// second query, and a name-based order would reshuffle rows under the cursor
// when it lands.
func BuildEdges(selfID string, records []map[string]any) []Edge {
	var out []Edge
	for _, rec := range records {
		src, dst := Str(rec, "source_id"), Str(rec, "target_id")
		e := Edge{Verb: Str(rec, "type")}
		if src == selfID {
			e.Outgoing = true
			e.OtherID, e.OtherType = dst, Str(rec, "target_type")
		} else {
			e.OtherID, e.OtherType = src, Str(rec, "source_type")
		}
		if e.OtherID == "" || e.OtherID == selfID {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := EdgeRank(out[i].Verb), EdgeRank(out[j].Verb); a != b {
			return a < b
		}
		if out[i].Outgoing != out[j].Outgoing {
			return out[i].Outgoing
		}
		if out[i].Verb != out[j].Verb {
			return out[i].Verb < out[j].Verb
		}
		if out[i].OtherType != out[j].OtherType {
			return out[i].OtherType < out[j].OtherType
		}
		return out[i].OtherID < out[j].OtherID
	})
	return out
}

// ServiceRunsOnStage renders the DQL stages that narrow a smartscapeNodes
// list to the runs_on targets of a service — its deployment surface (pods,
// containers, processes, hosts). A service is a detection construct: its
// runtime nodes carry no service field to filter by, so the scope is a
// topology join (validated live on two tenants: join composes on smartscape
// commands with left[id] == right[target_id]). The stray right.target_id the
// join adds is removed so it never leaks into records or facets. "" for
// non-service entities.
func ServiceRunsOnStage(e Entity) string {
	if e.Type != "SERVICE" {
		return ""
	}
	return fmt.Sprintf(`| join [smartscapeEdges "*" | filter source_id == toSmartscapeId(%q) and type == "runs_on" | fields target_id | limit %d], on:{left[id] == right[target_id]}, kind:inner
| fieldsRemove right.target_id`, e.ID, EdgeQueryLimit)
}

// LogHopQuery resolves the runtime entities a service runs on — the
// processes and containers whose IDs log records actually carry. Logs are
// emitted by processes, not services (a service is a detection construct),
// so most log records carry no service ID: on one tenant the busiest
// service had ZERO service-stamped log lines but ~2k via its process
// (validated live). PROCESS and CONTAINER are the hop targets because logs
// carry dt.smartscape.process and dt.smartscape.container; runs_on HOST is
// deliberately excluded (a host's full log stream is not "this service's
// logs") and K8S_POD adds nothing — pod logs match through the pod's
// container, and log records carry k8s.pod.name but the edge target has no
// name to match it by. "" for scopes that need no hop: other entity types
// are stamped on their logs directly, and multi-entity scopes (a problem's
// affected set) are already wide.
func LogHopQuery(s Scope) string {
	if len(s.Entities) > 0 || s.Entity == nil || s.Entity.Type != "SERVICE" {
		return ""
	}
	return fmt.Sprintf(`smartscapeEdges "*"
| filter source_id == toSmartscapeId(%q) and type == "runs_on"
| fieldsAdd target_type
| filter in(target_type, {"PROCESS", "CONTAINER"})
| fields target_id, target_type
| limit 50`, s.Entity.ID)
}

// LogHopEntities merges LogHopQuery's records into the widened entity set:
// the service first (its arms still match service-stamped logs), then each
// process/container once.
func LogHopEntities(s Scope, records []map[string]any) []Entity {
	if s.Entity == nil {
		return nil
	}
	ents := []Entity{*s.Entity}
	seen := map[string]bool{s.Entity.ID: true}
	for _, rec := range records {
		id, typ := Str(rec, "target_id"), Str(rec, "target_type")
		if id == "" || typ == "" || seen[id] {
			continue
		}
		seen[id] = true
		ents = append(ents, Entity{ID: id, Type: typ})
	}
	return ents
}

// CensusQuery counts every Smartscape entity type — the navigator overview's
// type list (same aggregation as the :entities census view).
func CensusQuery() string {
	return `smartscapeNodes "*"
| summarize count = count(), by:{type}
| sort count desc
| limit 200`
}

// SchemaQuery aggregates the whole topology to type-level edges — which types
// connect to which, via which verb, how often (the navigator overview's
// schema panel). source_type/target_type are lazy projections and must be
// materialized with fieldsAdd before the summarize can group by them.
func SchemaQuery() string {
	return `smartscapeEdges "*"
| fieldsAdd source_type, target_type
| summarize count = count(), by:{source_type, type, target_type}
| sort count desc
| limit 2000`
}

// SchemaEdge is one type-level relationship aggregate: Count edges of verb
// Verb from SourceType to TargetType.
type SchemaEdge struct {
	SourceType string
	Verb       string
	TargetType string
	Count      int
}

// BuildSchema parses SchemaQuery records, dropping degenerate rows.
func BuildSchema(records []map[string]any) []SchemaEdge {
	var out []SchemaEdge
	for _, rec := range records {
		e := SchemaEdge{
			SourceType: Str(rec, "source_type"),
			Verb:       Str(rec, "type"),
			TargetType: Str(rec, "target_type"),
			Count:      IntValue(rec["count"]),
		}
		if e.Verb == "" || (e.SourceType == "" && e.TargetType == "") {
			continue
		}
		out = append(out, e)
	}
	return out
}

// TypeInstancesQuery lists the entities of one type for the navigator's type
// browser: id, name, and a display fallback for name-poor AWS nodes (the Name
// tag or the ARN — `name` is empty on most AWS inventory, validated live).
func TypeInstancesQuery(typ string) string {
	return fmt.Sprintf("smartscapeNodes %q\n"+
		"| fieldsAdd display = coalesce(if(name != \"\", name), `tags:aws`[`Name`], aws.arn)\n"+
		"| fields id, name, display, type\n"+
		"| sort display asc\n| limit 500", typ)
}

// ProblemOverlayQuery fetches recent problem records tenant-wide — the
// navigator's health overlay. One query per refresh, intersected client-side
// against visible nodes (never one query per node). Records are per-update;
// callers dedupe with ActiveProblems. Fixed 24h lookback, the same floor as
// the detail-page pulse: an active problem is interesting no matter how
// narrow the global window is.
func ProblemOverlayQuery() string {
	return `fetch dt.davis.problems, from:now() - 24h
| filter not(dt.davis.is_duplicate)
| sort timestamp desc
| limit 1000`
}

// ProblemAffectedIDs returns every entity id a problem record touches, across
// both ID eras — Smartscape ids from smartscape.affected_entities plus the
// legacy affected_entity_ids values. The eras don't interconvert, so matching
// a node set against both is the only honest join.
func ProblemAffectedIDs(rec map[string]any) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, e := range ProblemAffectedEntities(rec) {
		add(e.ID)
	}
	if arr, ok := rec["affected_entity_ids"].([]any); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok {
				add(s)
			}
		}
	}
	return out
}

// IntValue coerces a Grail numeric value to an int — Grail serializes counts
// as float64 or, for longs, as decimal strings. 0 when absent or unparseable.
func IntValue(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	case int:
		return n
	}
	return 0
}
