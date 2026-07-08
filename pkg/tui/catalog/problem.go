package catalog

import (
	"fmt"
	"strings"
	"time"
)

// Davis-problem helpers: the queries and record accessors behind the bespoke
// problem page and the entity page's problem pulse. Field names validated
// live: dt.davis.problems records are per-update (a problem emits a record on
// every status transition, event.status_transition OPEN/UPDATED/CLOSED), carry
// smartscape.affected_entities as [{id,name,type}] plus both ID-era arrays,
// dt.davis.event_ids naming the constituent Davis events, and event.end only
// once closed.

// IsProblem reports whether a record is a Davis problem — those route to the
// problem page instead of the flat record inspector.
func IsProblem(rec map[string]any) bool {
	return Str(rec, "event.kind") == "DAVIS_PROBLEM" && Str(rec, "display_id") != ""
}

// ProblemAffectedEntities extracts the problem's affected Smartscape entities
// (names included — no resolution query needed).
func ProblemAffectedEntities(rec map[string]any) []Entity {
	arr, _ := rec["smartscape.affected_entities"].([]any)
	var out []Entity
	for _, e := range arr {
		m, _ := e.(map[string]any)
		if m == nil {
			continue
		}
		ent := Entity{ID: Str(m, "id"), Name: Str(m, "name"), Type: Str(m, "type")}
		if ent.ID != "" {
			out = append(out, ent)
		}
	}
	return out
}

// ProblemEventIDs returns the ids of the problem's constituent Davis events.
func ProblemEventIDs(rec map[string]any) []string {
	arr, _ := rec["dt.davis.event_ids"].([]any)
	var out []string
	for _, e := range arr {
		if s, ok := e.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// problemWindowPad is the context margin around a problem's lifespan — the
// root cause precedes event.start and closing evidence trails event.end.
const problemWindowPad = 5 * time.Minute

// ProblemWindow computes the investigation window the problem page scopes its
// signal tabs to: event.start → event.end, padded, open-ended while the
// problem is still active.
func ProblemWindow(rec map[string]any, now time.Time) Timeframe {
	start, err := time.Parse(time.RFC3339Nano, Str(rec, "event.start"))
	if err != nil {
		// No parseable start: fall back to the default relative window.
		return DefaultTimeframe
	}
	tf := Timeframe{Label: "problem", From: start.Add(-problemWindowPad)}
	if end, err := time.Parse(time.RFC3339Nano, Str(rec, "event.end")); err == nil {
		tf.To = end.Add(problemWindowPad)
		tf.Dur = tf.To.Sub(tf.From)
	} else {
		tf.Dur = now.Sub(tf.From)
	}
	return tf
}

// ProblemPulseQuery fetches the recent problem records affecting an entity —
// the entity page's header pulse. Records are per-update; LatestProblems
// dedupes client-side so the caller also gets the full latest record per
// problem (the signals block jumps straight into the problem page with it).
// The lookback is floored at 24h: an active problem is interesting no matter
// how narrow the global window is.
func ProblemPulseQuery(e Entity, tf Timeframe) string {
	return fmt.Sprintf(
		"fetch dt.davis.problems, from:%s\n| filter not(dt.davis.is_duplicate)\n| filter %s\n| sort timestamp desc\n| limit 200",
		floorTimeframe(tf, 24*time.Hour, "24h"), ProblemFilter(e))
}

// LatestProblems dedupes problem update-records to the latest per display_id,
// preserving input order (newest-first input keeps the newest record).
func LatestProblems(records []map[string]any) []map[string]any {
	seen := map[string]bool{}
	var out []map[string]any
	for _, rec := range records {
		id := Str(rec, "display_id")
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, rec)
	}
	return out
}

// ActiveProblems filters LatestProblems output to the still-active ones.
func ActiveProblems(records []map[string]any) []map[string]any {
	var out []map[string]any
	for _, rec := range LatestProblems(records) {
		if Str(rec, "event.status") == "ACTIVE" {
			out = append(out, rec)
		}
	}
	return out
}

// changeEventFilter matches change-ish events: deployments, config changes,
// annotations, restarts (classic Davis event types, validated live on a
// OneAgent tenant), the SDLC event kind (build/deploy pipeline events), and
// deployment-phrased CUSTOM_INFO events — K8s workload change detection
// emits "Deployment spec change" under that generic type (validated live).
const changeEventFilter = `event.kind == "SDLC_EVENT" or in(event.type, {"CUSTOM_DEPLOYMENT", "CUSTOM_CONFIGURATION", "CUSTOM_ANNOTATION", "PROCESS_RESTART"}) or (event.type == "CUSTOM_INFO" and matchesPhrase(event.name, "deployment"))`

// ChangeEventQuery finds an entity's most recent change-ish event — "what
// changed here lately" is the single most actionable triage fact on a detail
// page. Fixed 7d lookback: a deployment older than the global window is
// exactly the point.
func ChangeEventQuery(e Entity) string {
	return fmt.Sprintf(
		"fetch events, from:now() - 7d\n| filter %s\n| filter %s\n| sort timestamp desc\n| limit 1",
		SignalFilter(e), changeEventFilter)
}

// ProblemSeverity renders the numeric event.severity as its Davis level name
// (the API serializes the enum ordinal; mapping per Davis event severities).
func ProblemSeverity(rec map[string]any) string {
	switch Str(rec, "event.severity") {
	case "1":
		return "INFO"
	case "2":
		return "LOW"
	case "3":
		return "HIGH"
	case "4":
		return "CRITICAL"
	}
	return Str(rec, "event.severity")
}

// ProblemFlags summarizes the problem's boolean odds and ends ("" when
// nothing is noteworthy): frequent event, muted, under maintenance.
func ProblemFlags(rec map[string]any) string {
	var flags []string
	if b, _ := rec["dt.davis.is_frequent_event"].(bool); b {
		flags = append(flags, "frequent event")
	}
	if s := Str(rec, "dt.davis.mute.status"); s != "" && s != "NOT_MUTED" {
		flags = append(flags, "muted")
	}
	if b, _ := rec["maintenance.is_under_maintenance"].(bool); b {
		flags = append(flags, "under maintenance")
	}
	return strings.Join(flags, " · ")
}

// DavisEventsSpec is the problem page's evidence tab: the constituent Davis
// events, one row per event id (update-records collapsed via takeLast, the
// same shape the home problems panel uses). Scope.Arg carries the
// comma-joined event ids. Deliberately NOT in the spec registry — it is only
// reachable from a problem page, never the command bar.
var DavisEventsSpec = &Spec{
	Name: "evidence",
	Kind: KindSignal,
	Desc: "Davis events constituting a problem",
	Query: func(s Scope) string {
		ids := strings.Split(s.Arg, ",")
		quoted := make([]string, 0, len(ids))
		for _, id := range ids {
			if id = strings.TrimSpace(id); id != "" {
				quoted = append(quoted, fmt.Sprintf("%q", id))
			}
		}
		return fmt.Sprintf(`fetch dt.davis.events, from:%s
| filter in(event.id, {%s})
| sort timestamp asc
| summarize { start = takeLast(event.start), end = takeLast(event.end), type = takeLast(event.type), name = takeLast(event.name), status = takeLast(event.status), rootcause = takeLast(dt.davis.is_rootcause_relevant), source = takeLast(dt_source_entity_name), source_id = takeLast(dt.smartscape_source.id), source_type = takeLast(dt.smartscape_source.type), description = takeLast(event.description) }, by:{event.id}
| sort start desc
| limit 100`, s.Timeframe.DQL(), strings.Join(quoted, ", "))
	},
	Columns: []Column{
		{Title: "START", Width: 12, Value: func(rec map[string]any) string { return FormatTime(Str(rec, "start")) },
			Sort: func(rec map[string]any) any { return Str(rec, "start") }},
		{Title: "RC", Width: 2, Value: evidenceRootCause, Class: func(val string) string {
			if val != "" {
				return "error"
			}
			return ""
		}},
		{Title: "TYPE", Field: "type", Width: 28},
		{Title: "NAME", Field: "name"},
		{Title: "STATUS", Field: "status", Width: 6, Class: func(val string) string {
			if val == "ACTIVE" {
				return "error"
			}
			return "dim"
		}},
		{Title: "SOURCE", Width: 24, Value: func(rec map[string]any) string { return StrFirst(rec, "source") }},
	},
	Entity: func(rec map[string]any) *Entity {
		id := Str(rec, "source_id")
		if id == "" {
			return nil
		}
		return &Entity{ID: id, Name: StrFirst(rec, "source"), Type: Str(rec, "source_type")}
	},
	Drills: map[string]string{"l": "logs", "m": "metrics", "p": "problems"},
}

// evidenceRootCause marks root-cause-relevant evidence rows.
func evidenceRootCause(rec map[string]any) string {
	if b, _ := rec["rootcause"].(bool); b {
		return "✱"
	}
	return ""
}
