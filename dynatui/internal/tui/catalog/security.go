package catalog

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Security workspace: the vulnerabilities list and its helpers. All field
// names and filter shapes validated live against security.events:
// VULNERABILITY-level state reports roll a vulnerability up (counts only, no
// entity ids); ENTITY-level reports carry the affected entity, its vulnerable
// component, and related-entity id arrays — the level an entity pin must read.
// in("<id>", related_entities.*.ids) works as plain-string array membership
// (legacy-era ids; no toSmartscapeId). PROCESS-<hex> and
// PROCESS_GROUP_INSTANCE-<hex> share the hex suffix (validated across all
// distinct PGIs in 7d of attack records), so the modern-era pin swaps the
// prefix. K8s workload/cluster ids in related_entities do NOT match
// Smartscape ids — those types stay unscopable.

// vulnLenses slice the vulnerabilities list by triage state. Muted
// vulnerabilities are deliberately out of the default lens: someone muted
// them so they would stop burying the actionable ones.
var vulnLenses = []Lens{
	{Name: "open", Desc: "open, unmuted vulnerabilities", Filter: `status == "OPEN" and muted != "MUTED"`},
	{Name: "muted", Desc: "open but muted vulnerabilities", Filter: `status == "OPEN" and muted == "MUTED"`},
	{Name: "all", Desc: "every vulnerability, resolved and muted included"},
}

// vulnStateFloor widens the query window to cover the periodic state-report
// snapshots — a short window would hide open vulnerabilities.
func vulnStateFloor(tf Timeframe) string {
	return floorTimeframe(tf, 24*time.Hour, "24h")
}

// vulnSummarizeAliases is the takeLast rollup both list shapes share. The
// aliases feed the columns, the preview pane, and the detail page's seed
// record, so both the unscoped and the pinned list row carry the same keys.
const vulnSummarizeAliases = `title = takeLast(vulnerability.title), display_id = takeLast(vulnerability.display_id), level = takeLast(vulnerability.risk.level), score = takeLast(vulnerability.risk.score), status = takeLast(vulnerability.resolution.status), muted = takeLast(vulnerability.mute.status), tech = takeLast(vulnerability.technology), stack = takeLast(vulnerability.stack), cve = takeLast(vulnerability.references.cve), cvss = takeLast(vulnerability.cvss.base_score), exposure = takeLast(vulnerability.davis_assessment.exposure_status), exploit = takeLast(vulnerability.davis_assessment.exploit_status), fix = takeLast(vulnerability.is_fix_available), code_location = takeLast(vulnerability.code_location.name), url = takeLast(vulnerability.url)`

var vulnsSpec = &Spec{
	Name:    "vulnerabilities",
	Aliases: []string{"vulns", "security", "sec"},
	Kind:    KindSignal,
	Desc:    "Security vulnerabilities (Davis Security Score)",
	Query: func(s Scope) string {
		var b strings.Builder
		fmt.Fprintf(&b, "fetch security.events, from:%s\n", vulnStateFloor(s.Timeframe))
		if f := vulnScopeFilter(s); f != "" {
			// An entity pin reads the ENTITY-level state reports — only those
			// carry affected/related entity ids (the VULNERABILITY level rolls
			// up counts) — and adds the vulnerable component to the rollup.
			b.WriteString("| filter event.type == \"VULNERABILITY_STATE_REPORT_EVENT\" and event.level == \"ENTITY\"\n")
			fmt.Fprintf(&b, "| filter %s\n", f)
			b.WriteString("| sort timestamp asc\n")
			fmt.Fprintf(&b, "| summarize { %s, component = takeLast(affected_entity.vulnerable_component.name) }, by:{vulnerability.id}\n", vulnSummarizeAliases)
		} else {
			b.WriteString("| filter event.type == \"VULNERABILITY_STATE_REPORT_EVENT\" and event.level == \"VULNERABILITY\"\n")
			b.WriteString("| sort timestamp asc\n")
			fmt.Fprintf(&b, "| summarize { %s, affected = takeLast(affected_entities.count) }, by:{vulnerability.id}\n", vulnSummarizeAliases)
		}
		if f := lensAt(vulnLenses, s.Lens).Filter; f != "" {
			fmt.Fprintf(&b, "| filter %s\n", f)
		}
		b.WriteString("| sort score desc\n| limit 200")
		return b.String()
	},
	Lenses:  vulnLenses,
	Columns: vulnColumns(vulnAffectedColumn),
	// A pinned list is per-entity: the affected count would always read 1-ish,
	// the vulnerable component names what to upgrade.
	ScopeColumns: func(s Scope) []Column {
		if vulnScopeFilter(s) == "" {
			return nil
		}
		return vulnColumns(Column{Title: "COMPONENT", Field: "component", Width: 30})
	},
	Scopable: func(e Entity) bool { return VulnEntityFilter(e) != "" },
	Drills:   map[string]string{},
}

var vulnAffectedColumn = Column{Title: "AFFECTED", Field: "affected", Width: 8, Right: true}

// vulnColumns builds the list column set around the one slot that differs
// between the unscoped (affected count) and pinned (component) shapes.
func vulnColumns(slot Column) []Column {
	return []Column{
		{Title: "ID", Field: "display_id", Width: 6},
		{Title: "SCORE", Width: 5, Right: true, Class: classRiskScore,
			Value: func(rec map[string]any) string { return FormatScore(rec["score"]) },
			Sort:  func(rec map[string]any) any { return rec["score"] }},
		{Title: "LEVEL", Field: "level", Width: 8, Class: classRiskLevel},
		{Title: "EXPOSURE", Width: 8, Value: exposureCell, Class: classExposure},
		{Title: "EXPLOIT", Width: 7, Value: exploitCell, Class: classExploit},
		{Title: "FIX", Width: 3, Value: fixCell, Class: func(val string) string {
			if val != "" {
				return "ok"
			}
			return ""
		}},
		{Title: "STACK", Width: 8, Value: func(rec map[string]any) string { return vulnStack(Str(rec, "stack")) }},
		{Title: "TECH", Field: "tech", Width: 10},
		slot,
		{Title: "CVE", Width: 16, Value: func(rec map[string]any) string { return StrFirst(rec, "cve") }},
		{Title: "TITLE", Field: "title"},
	}
}

// exposureCell compresses the Davis exposure status into a triage badge.
func exposureCell(rec map[string]any) string {
	return exposureBadge(Str(rec, "exposure"))
}

// exposureBadge maps the Davis exposure enum to a short verdict: reachable
// from where? NOT_DETECTED is a positive verdict (assessed, not exposed) and
// renders as a dim dash; NOT_AVAILABLE (not assessed) stays blank — absence
// of a verdict must not read like one.
func exposureBadge(status string) string {
	switch status {
	case "PUBLIC_NETWORK":
		return "public"
	case "ADJACENT_NETWORK":
		return "adjacent"
	case "NOT_DETECTED":
		return "-"
	}
	return ""
}

func classExposure(val string) string {
	switch val {
	case "public":
		return "error"
	case "adjacent":
		return "warn"
	case "-":
		return "dim"
	}
	return ""
}

// exploitCell marks vulnerabilities with a publicly available exploit.
func exploitCell(rec map[string]any) string {
	if Str(rec, "exploit") == "AVAILABLE" {
		return "avail"
	}
	return ""
}

func classExploit(val string) string {
	if val == "avail" {
		return "error"
	}
	return ""
}

// fixCell marks vulnerabilities a component upgrade would fix.
func fixCell(rec map[string]any) string {
	if fixAvailable(rec) {
		return "yes"
	}
	return ""
}

// fixAvailable coalesces the summarize alias and the raw record's
// fix-availability flag.
func fixAvailable(rec map[string]any) bool {
	if b, ok := rec["fix"].(bool); ok {
		return b
	}
	b, _ := rec["vulnerability.is_fix_available"].(bool)
	return b
}

// vulnStack shortens the vulnerability.stack enum for a narrow column.
func vulnStack(stack string) string {
	switch stack {
	case "CODE_LIBRARY":
		return "library"
	case "CONTAINER_ORCHESTRATION":
		return "k8s"
	}
	return strings.ToLower(stack)
}

// VulnEntityFilter renders the ENTITY-level state-report condition that scopes
// the vulnerabilities list to one entity ("" = this type cannot scope it).
// SERVICE and HOST ids are identical strings across both ID eras (validated
// live); a modern PROCESS id maps onto the affected-process PGI ids by prefix
// swap. K8s ids mismatch between eras, so K8s types honestly refuse.
func VulnEntityFilter(e Entity) string {
	switch e.Type {
	case "SERVICE":
		return fmt.Sprintf("in(%q, related_entities.services.ids)", e.ID)
	case "HOST":
		return fmt.Sprintf("(affected_entity.id == %q or in(%q, related_entities.hosts.ids))", e.ID, e.ID)
	case "PROCESS", "PROCESS_GROUP_INSTANCE":
		return fmt.Sprintf("in(%q, affected_entity.affected_processes.ids)", pgiID(e.ID))
	case "PROCESS_GROUP":
		return fmt.Sprintf("affected_entity.id == %q", e.ID)
	}
	return ""
}

// vulnScopeFilter is VulnEntityFilter over the scope's pinned entity.
func vulnScopeFilter(s Scope) string {
	if s.Entity == nil {
		return ""
	}
	return VulnEntityFilter(*s.Entity)
}

// pgiID converts a modern-era PROCESS id to its legacy PROCESS_GROUP_INSTANCE
// twin — the id era affected_entity.affected_processes.ids carries.
func pgiID(id string) string {
	if hex, ok := strings.CutPrefix(id, "PROCESS-"); ok {
		return "PROCESS_GROUP_INSTANCE-" + hex
	}
	return id
}

// IsVulnerability reports whether a record stands for a vulnerability —
// those route to the vulnerability page instead of the flat record
// inspector. The list's summarized rows keep vulnerability.id (the summarize
// by: key) and raw state/change records carry it natively; attack records
// don't (they carry finding.* plus only a code location) and keep the
// inspector.
func IsVulnerability(rec map[string]any) bool {
	return Str(rec, "vulnerability.id") != "" && !isAttackRecord(rec)
}

// VulnDetailQuery fetches the latest full VULNERABILITY-level state report
// for one vulnerability — the page's overview facts and markdown description.
// Fixed 7d lookback: a just-resolved vulnerability stops being re-reported,
// but its page must still resolve for a while.
func VulnDetailQuery(id string) string {
	return fmt.Sprintf(`fetch security.events, from:now() - 7d
| filter event.type == "VULNERABILITY_STATE_REPORT_EVENT" and event.level == "VULNERABILITY" and vulnerability.id == %q
| sort timestamp desc
| limit 1`, id)
}

// --- attacks ------------------------------------------------------------

// attacksSpec lists Runtime Application Protection detections — actual
// exploit attempts observed at runtime (SSRF, SQL/JNDI/CMD injection), each
// carrying the entry point, the payload, the actor IPs, and a trace id. No
// lookback floor: attacks are timely events and an empty short window is a
// truthful answer. Third-party detections (GuardDuty etc.) are deliberately
// excluded for now — their rows carry none of the entry-point substance this
// view is built around (a "cloud" lens is the natural follow-up).
//
// Scope.Arg carries a vulnerability.code_location.name: the vulnerability
// page's attacks tab scopes by exact code location — the precise attack↔vuln
// linkage for code-level vulnerabilities (attack records carry no
// vulnerability.id; validated live).
var attacksSpec = &Spec{
	Name:    "attacks",
	Aliases: []string{"attack", "exploits", "rap"},
	Kind:    KindSignal,
	Desc:    "Attack detections (Runtime Application Protection)",
	Query: func(s Scope) string {
		var b strings.Builder
		fmt.Fprintf(&b, "fetch security.events, from:%s\n", s.Timeframe.DQL())
		b.WriteString("| filter event.type == \"DETECTION_FINDING\" and product.name == \"Runtime Application Protection\"\n")
		if s.Arg != "" {
			fmt.Fprintf(&b, "| filter vulnerability.code_location.name == %q\n", s.Arg)
		} else if f := ScopeSignalFilter(s); f != "" {
			fmt.Fprintf(&b, "| filter %s\n", f)
		}
		b.WriteString("| sort timestamp desc\n| limit 300")
		return b.String()
	},
	Columns: attackColumns,
	Entity:  signalSourceEntity,
	Trace:   func(rec map[string]any) string { return Str(rec, "trace.id") },
	// A SERVICE pin widens to the service's runtime entities first — attack
	// records carry process/container ids, not service ids (same story as
	// logs).
	Hop:    &HopSpec{Query: LogHopQuery, Apply: LogHopEntities},
	Drills: map[string]string{"s": "trace", "l": "logs", "p": "problems", "v": "events", "m": "metrics"},
	Scopable: func(e Entity) bool {
		switch e.Type {
		case "SERVICE", "HOST", "PROCESS", "CONTAINER", "PROCESS_GROUP":
			return true
		}
		return strings.HasPrefix(e.Type, "K8S_")
	},
}

// attackColumns render a detection row; shared by the standalone attacks
// view and the vulnerability page's attacks tab.
var attackColumns = []Column{
	{Title: "TIME", Width: 12,
		Value: func(rec map[string]any) string { return FormatTime(Str(rec, "timestamp")) },
		Sort:  func(rec map[string]any) any { return Str(rec, "timestamp") }},
	{Title: "TYPE", Field: "finding.type", Width: 14},
	{Title: "ACTION", Field: "finding.action", Width: 7, Class: classAttackAction},
	{Title: "SEVERITY", Field: "finding.severity", Width: 8, Class: classRiskLevel},
	{Title: "SOURCE", Width: 15, Value: func(rec map[string]any) string { return StrFirst(rec, "actor.ips") }},
	{Title: "PROCESS", Width: 24, Value: attackProcess},
	{Title: "LOCATION", Value: func(rec map[string]any) string {
		return ShortCodeLocation(Str(rec, "vulnerability.code_location.name"))
	}},
}

// classAttackAction colors the blocked/observed verdict: a Blocked attack is
// the system working, an Audited one reached the vulnerable code.
func classAttackAction(val string) string {
	switch val {
	case "Blocked":
		return "ok"
	case "Audited":
		return "warn"
	}
	return ""
}

// attackProcess names the attacked workload, preferring the K8s name over the
// verbose detected process-group name.
func attackProcess(rec map[string]any) string {
	return firstNonEmpty(Str(rec, "k8s.workload.name"), Str(rec, "dt.process_group.detected_name"))
}

// ShortCodeLocation trims a fully-qualified code location to Class.method:
// "org.dynatrace.ssrf.ProxyController.proxyUrl(String):89" reads as
// "ProxyController.proxyUrl(String):89" — the package path adds width, not
// information, in a table cell.
func ShortCodeLocation(loc string) string {
	head := loc
	if i := strings.Index(loc, "("); i >= 0 {
		head = loc[:i]
	}
	parts := strings.Split(head, ".")
	if len(parts) <= 2 {
		return loc
	}
	prefix := strings.Join(parts[:len(parts)-2], ".") + "."
	return strings.TrimPrefix(loc, prefix)
}

// --- vulnerability page tabs ---------------------------------------------
//
// The specs below power the vulnerability page's tabs. Deliberately NOT in
// the registry — they are only reachable from a vulnerability page, never
// the command bar (the DavisEventsSpec pattern). Scope.Arg carries the
// vulnerability.id.

// VulnEntitiesSpec is the page's affected-entities tab: one row per affected
// entity from the ENTITY-level state reports — the level that alone carries
// entity ids, components, and process instances. Rows are navigable: l/v/p/m
// drill into the entity's signals (the vuln→logs hop the flat list lacked).
var VulnEntitiesSpec = &Spec{
	Name: "entities",
	Kind: KindSignal,
	Desc: "Entities affected by a vulnerability",
	Query: func(s Scope) string {
		return fmt.Sprintf(`fetch security.events, from:%s
| filter event.type == "VULNERABILITY_STATE_REPORT_EVENT" and event.level == "ENTITY" and vulnerability.id == %q
| dedup affected_entity.id, sort:{timestamp desc}
| sort affected_entity.name asc
| limit 500`, vulnStateFloor(s.Timeframe), s.Arg)
	},
	Columns: []Column{
		{Title: "NAME", Field: "affected_entity.name"},
		{Title: "TYPE", Width: 13, Value: func(rec map[string]any) string {
			return strings.ToLower(strings.ReplaceAll(Str(rec, "affected_entity.type"), "_", " "))
		}},
		{Title: "COMPONENT", Field: "affected_entity.vulnerable_component.name", Width: 30},
		{Title: "PROCESSES", Width: 9, Right: true, Value: func(rec map[string]any) string {
			ids, _ := rec["affected_entity.affected_processes.ids"].([]any)
			if len(ids) == 0 {
				return ""
			}
			return fmt.Sprintf("%d", len(ids))
		}},
		{Title: "DATA ASSETS", Field: "affected_entity.reachable_data_assets.count", Width: 11, Right: true,
			Class: func(val string) string {
				if val != "" && val != "0" {
					return "warn"
				}
				return "dim"
			}},
		{Title: "STATUS", Field: "vulnerability.resolution.status", Width: 8, Class: func(val string) string {
			if val == "RESOLVED" {
				return "dim"
			}
			return ""
		}},
		{Title: "MUTED", Width: 5, Value: func(rec map[string]any) string {
			if Str(rec, "vulnerability.mute.status") == "MUTED" {
				return "yes"
			}
			return ""
		}},
	},
	// Enter shows the full entity-level record (entry points, related ids —
	// the inspector renders entity ids as navigable links); re-routing to the
	// same vulnerability page it came from would be a circle.
	EnterTarget: "inspect",
	Entity:      VulnAffectedEntity,
	Drills:      map[string]string{"l": "logs", "v": "events", "p": "problems", "m": "metrics"},
}

// VulnAffectedEntity maps an ENTITY-level state report row to a drillable
// Smartscape entity. HOST ids are era-identical; a PROCESS_GROUP row drills
// through its first affected process instance (modern PROCESS id = legacy
// PGI id with the prefix swapped — validated live across all sampled PGIs).
// KUBERNETES_NODE ids don't survive the era mismatch — no drill.
func VulnAffectedEntity(rec map[string]any) *Entity {
	id := Str(rec, "affected_entity.id")
	name := Str(rec, "affected_entity.name")
	switch Str(rec, "affected_entity.type") {
	case "HOST":
		if id == "" {
			return nil
		}
		return &Entity{ID: id, Name: name, Type: "HOST"}
	case "PROCESS_GROUP":
		ids, _ := rec["affected_entity.affected_processes.ids"].([]any)
		if len(ids) == 0 {
			return nil
		}
		pgi, _ := ids[0].(string)
		hex, ok := strings.CutPrefix(pgi, "PROCESS_GROUP_INSTANCE-")
		if !ok {
			return nil
		}
		if names, _ := rec["affected_entity.affected_processes.names"].([]any); len(names) > 0 {
			if n, _ := names[0].(string); n != "" {
				name = n
			}
		}
		return &Entity{ID: "PROCESS-" + hex, Name: name, Type: "PROCESS"}
	}
	return nil
}

// VulnAttacksSpec is the page's attacks tab for vulnerabilities WITHOUT a
// code location (library vulns): a hop resolves the affected entities first,
// then the detections on them. This linkage is entity-based, not causal —
// the PROCESS column keeps the provenance visible. Code-level vulns skip
// this spec entirely: the page hands their code location to the registered
// attacks spec for the exact match.
var VulnAttacksSpec = &Spec{
	Name: "attacks",
	Kind: KindSignal,
	Desc: "Attack detections on a vulnerability's affected entities",
	Query: func(s Scope) string {
		var b strings.Builder
		fmt.Fprintf(&b, "fetch security.events, from:%s\n", s.Timeframe.DQL())
		b.WriteString("| filter event.type == \"DETECTION_FINDING\" and product.name == \"Runtime Application Protection\"\n")
		if f := ScopeSignalFilter(s); f != "" {
			fmt.Fprintf(&b, "| filter %s\n", f)
		} else {
			// The hop found no matchable affected entities — an unfiltered
			// list would pass off the whole tenant's attacks as this
			// vulnerability's. filter false keeps the tab honestly empty.
			b.WriteString("| filter false\n")
		}
		b.WriteString("| sort timestamp desc\n| limit 300")
		return b.String()
	},
	Columns: attackColumns,
	Entity:  signalSourceEntity,
	Trace:   func(rec map[string]any) string { return Str(rec, "trace.id") },
	Hop:     &HopSpec{Query: VulnAttackHopQuery, Apply: VulnAttackHopEntities},
	Drills:  map[string]string{"s": "trace", "l": "logs", "p": "problems", "v": "events", "m": "metrics"},
}

// VulnAttackHopQuery resolves a vulnerability's affected entities before the
// attacks fetch ("" once a scope is already in place).
func VulnAttackHopQuery(s Scope) string {
	if s.Arg == "" || len(s.Entities) > 0 {
		return ""
	}
	return fmt.Sprintf(`fetch security.events, from:now() - 24h
| filter event.type == "VULNERABILITY_STATE_REPORT_EVENT" and event.level == "ENTITY" and vulnerability.id == %q
| dedup affected_entity.id, sort:{timestamp desc}
| fields affected_entity.id, affected_entity.type, affected_entity.name
| limit 100`, s.Arg)
}

// VulnAttackHopEntities merges the hop records into the scope's entity set.
// PROCESS_GROUP matches attack records via dt.entity.process_group, HOST via
// its era-identical id; KUBERNETES_NODE ids don't cross the era divide.
func VulnAttackHopEntities(s Scope, records []map[string]any) []Entity {
	var ents []Entity
	seen := map[string]bool{}
	for _, rec := range records {
		id, typ := Str(rec, "affected_entity.id"), Str(rec, "affected_entity.type")
		if id == "" || seen[id] || (typ != "PROCESS_GROUP" && typ != "HOST") {
			continue
		}
		seen[id] = true
		ents = append(ents, Entity{ID: id, Name: Str(rec, "affected_entity.name"), Type: typ})
	}
	return ents
}

// VulnEntryPointsSpec is the page's entry-points tab (code-level vulns): the
// HTTP paths and payloads through which tainted input reaches the vulnerable
// code, expanded from the ENTITY-level reports' entry-point JSON documents
// (`expand` on the array validated live; the JSON parses client-side).
var VulnEntryPointsSpec = &Spec{
	Name: "entry points",
	Kind: KindSignal,
	Desc: "Entry points and payloads of a code-level vulnerability",
	Query: func(s Scope) string {
		return fmt.Sprintf(`fetch security.events, from:%s
| filter event.type == "VULNERABILITY_STATE_REPORT_EVENT" and event.level == "ENTITY" and vulnerability.id == %q
| dedup affected_entity.id, sort:{timestamp desc}
| expand entry_point = entry_points.entry_point_jsons
| fields affected_entity.name, entry_point
| limit 300`, vulnStateFloor(s.Timeframe), s.Arg)
	},
	Columns: []Column{
		{Title: "ENTITY", Field: "affected_entity.name", Width: 28},
		{Title: "PATH", Width: 28, Value: func(rec map[string]any) string { return ParseEntryPoint(rec).Path }},
		{Title: "INPUTS", Width: 6, Right: true, Value: func(rec map[string]any) string {
			ep := ParseEntryPoint(rec)
			if ep.Inputs == 0 {
				return ""
			}
			out := fmt.Sprintf("%d", ep.Inputs)
			if ep.Malicious {
				out += " ⚠"
			}
			return out
		}, Class: func(val string) string {
			if strings.Contains(val, "⚠") {
				return "warn"
			}
			return ""
		}},
		{Title: "PAYLOAD", Width: 0, Value: func(rec map[string]any) string { return ParseEntryPoint(rec).Payload }},
	},
	Drills: map[string]string{},
}

// EntryPoint is one parsed entry-point document.
type EntryPoint struct {
	Path      string
	Payload   string
	Inputs    int
	Malicious bool // any user-controlled input flagged malicious
}

// ParseEntryPoint decodes the row's entry-point JSON (tolerant: a malformed
// document yields the zero value, never an error surface).
func ParseEntryPoint(rec map[string]any) EntryPoint {
	raw := Str(rec, "entry_point")
	if raw == "" {
		return EntryPoint{}
	}
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil {
		return EntryPoint{}
	}
	ep := EntryPoint{Path: Str(m, "entry_point.url.path"), Payload: Str(m, "entry_point.payload")}
	inputs, _ := m["entry_point.user_controlled_inputs"].([]any)
	ep.Inputs = len(inputs)
	for _, in := range inputs {
		im, _ := in.(map[string]any)
		if b, _ := im["user_controlled_input.is_malicious"].(bool); b {
			ep.Malicious = true
		}
	}
	return ep
}

// VulnTimelineSpec is the page's timeline tab: the vulnerability's status
// and assessment change events — who muted it, when it reopened, what the
// platform recomputed. 30d floor: state transitions are sparse.
var VulnTimelineSpec = &Spec{
	Name: "timeline",
	Kind: KindSignal,
	Desc: "Status and assessment changes of a vulnerability",
	Query: func(s Scope) string {
		return fmt.Sprintf(`fetch security.events, from:%s
| filter in(event.type, {"VULNERABILITY_STATUS_CHANGE_EVENT", "VULNERABILITY_ASSESSMENT_CHANGE_EVENT"}) and vulnerability.id == %q
| sort timestamp desc
| limit 300`, floorTimeframe(s.Timeframe, 30*24*time.Hour, "30d"), s.Arg)
	},
	Columns: []Column{
		{Title: "TIME", Width: 12,
			Value: func(rec map[string]any) string { return FormatTime(Str(rec, "timestamp")) },
			Sort:  func(rec map[string]any) any { return Str(rec, "timestamp") }},
		{Title: "EVENT", Width: 10, Value: func(rec map[string]any) string {
			switch Str(rec, "event.type") {
			case "VULNERABILITY_STATUS_CHANGE_EVENT":
				return "status"
			case "VULNERABILITY_ASSESSMENT_CHANGE_EVENT":
				return "assessment"
			}
			return strings.ToLower(Str(rec, "event.type"))
		}},
		{Title: "TRANSITION", Field: "event.status_transition", Width: 10, Class: classTransition},
		{Title: "SCOPE", Width: 13, Value: func(rec map[string]any) string {
			return strings.ToLower(Str(rec, "event.level"))
		}},
		{Title: "CHANGED", Width: 24, Value: func(rec map[string]any) string {
			return FormatValue(rec["event.change_list"])
		}},
		{Title: "BY", Field: "event.trigger.user", Width: 10, Class: func(val string) string {
			if val == "SYSTEM" {
				return "dim"
			}
			return ""
		}},
		{Title: "DESCRIPTION", Field: "event.description"},
	},
	EnterTarget: "inspect",
	Drills:      map[string]string{},
}

// classTransition colors the lifecycle verdicts: closing is good news,
// (re)opening is not.
func classTransition(val string) string {
	switch val {
	case "CLOSE":
		return "ok"
	case "NEW_OPEN", "REOPEN":
		return "warn"
	case "MUTE", "UNMUTE":
		return "dim"
	}
	return ""
}

// FormatScore renders a risk score the way security scores read ("9.8",
// "10") — FormatValue's generic two decimals would print "9.80".
func FormatScore(v any) string {
	f, ok := FloatValue(v)
	if !ok {
		return FormatValue(v)
	}
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', 1, 64)
}

func classRiskScore(val string) string {
	score, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return ""
	}
	switch {
	case score >= 9:
		return "error"
	case score >= 7:
		return "warn"
	}
	return ""
}

func classRiskLevel(val string) string {
	switch val {
	case "CRITICAL":
		return "error"
	case "HIGH":
		return "warn"
	case "MEDIUM":
		return ""
	default:
		return "dim"
	}
}
