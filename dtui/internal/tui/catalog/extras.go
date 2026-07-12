package catalog

import (
	"fmt"
	"strings"
)

// Breadth views: RUM frontends, Postgres databases, and GenAI entities. All
// field names and scoping dimensions validated live (dt.smartscape.frontend,
// dt.smartscape.db_instance_postgres, gen_ai.provider.name). The security
// views live in security.go.

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
		SparkColumn("REQUESTS", "req", 15, ""),
		SparkColumn("ERRORS", "err", 15, ""),
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
		SparkColumn("ACTIVE CONN", "active", 14, ""),
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
	// 's' lands on the traces view scoped via dt.smartscape.gen_ai.* (the
	// spans of this agent/model/provider — the genai lens slices them); 'm'
	// opens the metric explorer through the same dimensions.
	Drills: map[string]string{"s": "traces", "m": "metrics", "p": "problems", "v": "events"},
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
