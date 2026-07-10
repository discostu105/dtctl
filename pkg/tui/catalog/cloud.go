package catalog

import (
	"fmt"
	"strings"
)

// Cloud inventory and the generic entity browser. AWS nodes on real tenants
// are name-poor: `name` is an empty string on ~98%, the display name lives in
// the `tags:aws` Name tag or the ARN (validated live). The census views feed
// the typed browser through Scope.Arg — every Smartscape type is browsable
// without a bespoke screen.

// censusSpec builds a type-census view (all types, or one cloud's slice) —
// :aws is :entities filtered to AWS_ types, one definition apart. The
// unfiltered census keeps raw type names ("pod" without its K8S_ prefix is
// ambiguous there); a single-cloud slice reads better prettified.
func censusSpec(name string, aliases []string, desc, filter string, limit int, pretty bool) *Spec {
	typeCol := Column{Title: "TYPE", Field: "type"}
	if pretty {
		typeCol = Column{Title: "TYPE", Value: func(rec map[string]any) string { return prettyType(Str(rec, "type")) }}
	}
	return &Spec{
		Name:    name,
		Aliases: aliases,
		Kind:    KindEntity,
		Desc:    desc,
		Query: func(s Scope) string {
			var b strings.Builder
			b.WriteString(`smartscapeNodes "*"`)
			if filter != "" {
				fmt.Fprintf(&b, "\n| filter %s", filter)
			}
			fmt.Fprintf(&b, "\n| summarize count = count(), by:{type}\n| sort count desc\n| limit %d", limit)
			return b.String()
		},
		Columns: []Column{
			typeCol,
			{Title: "COUNT", Field: "count", Width: 6, Right: true},
		},
		EnterTarget: "resources",
		EnterArg:    func(rec map[string]any) string { return Str(rec, "type") },
		Drills:      map[string]string{},
	}
}

var awsSpec = censusSpec("aws", []string{"cloud"},
	"AWS inventory by resource type", `startsWith(type, "AWS_")`, 100, true)

var entitiesSpec = censusSpec("entities", []string{"topo", "census"},
	"All Smartscape entity types (topology census)", "", 200, false)

var resourcesSpec = &Spec{
	Name:         "resources",
	Aliases:      []string{"res"},
	Kind:         KindEntity,
	EntityScoped: false,
	Desc:         "Entities of one type (generic browser)",
	Query: func(s Scope) string {
		typ := s.Arg
		if typ == "" {
			typ = "*"
		}
		return fmt.Sprintf("smartscapeNodes %q\n"+
			"| fieldsAdd display = coalesce(if(name != \"\", name), `tags:aws`[`Name`], aws.arn)\n"+
			"| fields id, name, display, type, region = aws.region, account = aws.account.id, lifetime\n"+
			"| sort display asc\n| limit 500", typ)
	},
	Columns: []Column{
		{Title: "NAME", Value: func(rec map[string]any) string {
			if d := Str(rec, "display"); d != "" {
				return d
			}
			return Str(rec, "id")
		}},
		{Title: "TYPE", Width: 26, Value: func(rec map[string]any) string { return prettyType(Str(rec, "type")) }},
		{Title: "REGION", Field: "region", Width: 12},
		{Title: "ACCOUNT", Field: "account", Width: 12},
		{Title: "SEEN", Width: 5, Right: true, Value: lifetimeAge},
	},
	Entity: func(rec map[string]any) *Entity {
		id := Str(rec, "id")
		if id == "" {
			return nil
		}
		name := Str(rec, "display")
		if name == "" {
			name = Str(rec, "name")
		}
		return &Entity{ID: id, Name: name, Type: Str(rec, "type")}
	},
	Drills: map[string]string{"l": "logs", "s": "traces", "m": "metrics", "p": "problems", "v": "events"},
}

// prettyType renders an entity type compactly: AWS_EC2_INSTANCE → ec2
// instance, K8S_POD → pod. The raw type stays visible in detail pages.
func prettyType(t string) string {
	t = strings.TrimPrefix(t, "AWS_")
	t = strings.TrimPrefix(t, "K8S_")
	return strings.ToLower(strings.ReplaceAll(t, "_", " "))
}
