package catalog

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Breadth views: RUM frontends, Postgres databases, GenAI entities, and
// security vulnerabilities. All field names and scoping dimensions validated
// live (dt.smartscape.frontend, dt.smartscape.db_instance_postgres,
// gen_ai.provider.name, security.events vulnerability.* fields).

var frontendsSpec = &Spec{
	Name:    "frontends",
	Aliases: []string{"fe", "apps", "rum"},
	Kind:    KindEntity,
	Desc:    "Web/mobile frontends (RUM)",
	Query: func(s Scope) string {
		return `smartscapeNodes "FRONTEND"
| fieldsRemove references
| sort name asc
| limit 100`
	},
	Columns: []Column{
		{Title: "NAME", Field: "name"},
		{Title: "TYPE", Width: 6, Value: func(rec map[string]any) string { return Str(rec, "frontend.type") }},
		SparkColumn("REQUESTS", "req", 10),
		SparkColumn("ERRORS", "err", 10),
		{Title: "SEEN", Width: 5, Right: true, Value: lifetimeAge},
	},
	Entity: nodeEntity("FRONTEND"),
	Drills: map[string]string{"m": "metrics", "p": "problems", "v": "events", "u": "sessions", "e": "userevents"},
	Enrich: &EnrichSpec{
		Key:    func(rec map[string]any) string { return Str(rec, "id") },
		By:     "dt.smartscape.frontend",
		Series: []string{"req", "err"},
		Query: func(tf Timeframe, keys []string) string {
			return fmt.Sprintf(
				"timeseries { req = sum(dt.frontend.request.count), err = sum(dt.frontend.error.count) }, by:{dt.smartscape.frontend}, from:%s, interval: %s, filter: { in(dt.smartscape.frontend, {%s}) }",
				tf.DQL(), sparkInterval(tf), idList(keys))
		},
	},
}

var databasesSpec = &Spec{
	Name:    "databases",
	Aliases: []string{"db", "dbs"},
	Kind:    KindEntity,
	Desc:    "Database instances (Postgres)",
	Query: func(s Scope) string {
		return `smartscapeNodes "DB_INSTANCE_POSTGRES"
| fieldsRemove references
| sort name asc
| limit 200`
	},
	Columns: []Column{
		{Title: "NAME", Field: "name"},
		{Title: "SYSTEM", Width: 10, Value: func(rec map[string]any) string { return Str(rec, "db.system") }},
		{Title: "VERSION", Width: 7, Value: func(rec map[string]any) string { return Str(rec, "db.instance.version") }},
		SparkColumn("ACTIVE CONN", "active", 11),
		{Title: "SEEN", Width: 5, Right: true, Value: lifetimeAge},
	},
	Entity: nodeEntity("DB_INSTANCE_POSTGRES"),
	Drills: map[string]string{"m": "metrics", "p": "problems", "v": "events"},
	Enrich: &EnrichSpec{
		Key:    func(rec map[string]any) string { return Str(rec, "id") },
		By:     "dt.smartscape.db_instance_postgres",
		Series: []string{"active"},
		Query: func(tf Timeframe, keys []string) string {
			return fmt.Sprintf(
				"timeseries active = avg(postgres.activity.active), by:{dt.smartscape.db_instance_postgres}, from:%s, interval: %s, filter: { in(dt.smartscape.db_instance_postgres, {%s}) }",
				tf.DQL(), sparkInterval(tf), idList(keys))
		},
	},
}

var genaiSpec = &Spec{
	Name:    "genai",
	Aliases: []string{"ai", "llm"},
	Kind:    KindEntity,
	Desc:    "GenAI agents, services, models, providers",
	Query: func(s Scope) string {
		return `smartscapeNodes "GENAI_AGENT", "GENAI_SERVICE", "GENAI_MODEL", "GENAI_PROVIDER"
| fieldsRemove references
| sort type asc, name asc
| limit 200`
	},
	Columns: []Column{
		{Title: "NAME", Field: "name"},
		{Title: "KIND", Width: 10, Value: func(rec map[string]any) string {
			return strings.ToLower(strings.TrimPrefix(Str(rec, "type"), "GENAI_"))
		}},
		{Title: "PROVIDER", Width: 12, Value: func(rec map[string]any) string { return Str(rec, "gen_ai.provider.name") }},
		{Title: "SERVICE", Width: 20, Value: func(rec map[string]any) string { return Str(rec, "dt.service.name") }},
		{Title: "SEEN", Width: 5, Right: true, Value: lifetimeAge},
	},
	Entity: nodeEntity(""),
	Drills: map[string]string{"p": "problems", "v": "events"},
}

var vulnsSpec = &Spec{
	Name:    "vulnerabilities",
	Aliases: []string{"vulns", "security", "sec"},
	Kind:    KindSignal,
	Desc:    "Security vulnerabilities (Davis Security Score)",
	Query: func(s Scope) string {
		// State reports are periodic snapshots — a short window would hide
		// open vulnerabilities, so the lookback is floored at 24h.
		tf := s.Timeframe.DQL()
		if s.Timeframe.Dur < 24*time.Hour {
			tf = "now() - 24h"
		}
		return fmt.Sprintf(`fetch security.events, from:%s
| filter event.type == "VULNERABILITY_STATE_REPORT_EVENT"
| sort timestamp asc
| summarize { title = takeLast(vulnerability.title), display_id = takeLast(vulnerability.display_id), level = takeLast(vulnerability.risk.level), score = takeLast(vulnerability.risk.score), status = takeLast(vulnerability.resolution.status), affected = takeLast(affected_entities.count), tech = takeLast(vulnerability.technology), cve = takeLast(vulnerability.references.cve), url = takeLast(vulnerability.url) }, by:{vulnerability.id}
| filter status == "OPEN"
| sort score desc
| limit 200`, tf)
	},
	Columns: []Column{
		{Title: "ID", Field: "display_id", Width: 6},
		{Title: "SCORE", Field: "score", Width: 5, Right: true, Class: classRiskScore},
		{Title: "LEVEL", Field: "level", Width: 8, Class: classRiskLevel},
		{Title: "TECH", Field: "tech", Width: 12},
		{Title: "AFFECTED", Field: "affected", Width: 8, Right: true},
		{Title: "CVE", Width: 16, Value: func(rec map[string]any) string { return StrFirst(rec, "cve") }},
		{Title: "TITLE", Field: "title"},
	},
	Drills: map[string]string{},
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

// idList renders Smartscape ids as toSmartscapeId("...") list elements for
// in() filters — plain strings silently match nothing.
func idList(keys []string) string {
	quoted := make([]string, len(keys))
	for i, k := range keys {
		quoted[i] = fmt.Sprintf("toSmartscapeId(%q)", k)
	}
	return strings.Join(quoted, ", ")
}
