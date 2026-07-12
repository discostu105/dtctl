package catalog

import (
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
		{Title: "SCORE", Field: "score", Width: 5, Right: true, Class: classRiskScore},
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

// exposureCell compresses the Davis exposure status into a triage badge:
// reachable from where? NOT_DETECTED is a positive verdict (assessed, not
// exposed) and renders as a dim dash; NOT_AVAILABLE (not assessed) stays
// blank — absence of a verdict must not read like one.
func exposureCell(rec map[string]any) string {
	switch Str(rec, "exposure") {
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
	if b, _ := rec["fix"].(bool); b {
		return "yes"
	}
	return ""
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
