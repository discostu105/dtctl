package recipes

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	texttemplate "text/template"

	"github.com/dynatrace-oss/dtctl/pkg/util/template"
)

// ScopeEntity is one resolved smartscape entity.
type ScopeEntity struct {
	ID   string `json:"id" yaml:"id"`
	Name string `json:"name" yaml:"name"`
	Type string `json:"type" yaml:"type"`
}

// ScopeResult is the outcome of `resolve scope`: a filter expression that
// references the entity (or entities — one display name can map to several
// instances) in one signal, plus the honesty metadata an agent needs to
// price it (strategy, coverage, hop targets).
type ScopeResult struct {
	Signal   string        `json:"signal" yaml:"signal"`
	Strategy string        `json:"strategy" yaml:"strategy"` // "filter" or "hop:<edge>"
	Filter   string        `json:"filter" yaml:"filter"`
	Coverage *float64      `json:"coverage,omitempty" yaml:"coverage,omitempty"`
	Entities []ScopeEntity `json:"entities" yaml:"entities"`
	Targets  []string      `json:"targets,omitempty" yaml:"targets,omitempty"` // hop target names in the filter
	Notes    []string      `json:"notes,omitempty" yaml:"notes,omitempty"`
}

var entityIDRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-[0-9A-F]{8,}$`)

// ResolveScope resolves how to reference an entity (given by smartscape ID or
// display name) in one signal, using the book's scoping rules. A display name
// matching several entities of one type resolves to a combined filter over
// all of them — partial instance coverage is the T5-class undercount trap.
func ResolveScope(ctx context.Context, runner Runner, book *Book, entityArg, signal string) (*ScopeResult, error) {
	if book == nil {
		return nil, fmt.Errorf("no recipe book for this context — run `dtctl recipes discover` first")
	}
	entities, err := lookupEntities(ctx, runner, entityArg)
	if err != nil {
		return nil, err
	}

	// A display name can match several entity types (a service and the OTel
	// processes behind it). Keep the types that have a scoping rule for the
	// requested signal; exactly one surviving type is the unambiguous read.
	byType := map[string][]ScopeEntity{}
	for _, e := range entities {
		byType[e.Type] = append(byType[e.Type], e)
	}
	var candidates []string
	for t := range byType {
		if _, ok := book.Scoping[t][signal]; ok {
			candidates = append(candidates, t)
		}
	}
	sort.Strings(candidates)
	var dropped []string
	switch len(candidates) {
	case 1:
		for t := range byType {
			if t != candidates[0] {
				dropped = append(dropped, fmt.Sprintf("%d %s", len(byType[t]), t))
			}
		}
		entities = byType[candidates[0]]
	case 0:
		return nil, fmt.Errorf("no scoping rule covers %q for signal %s (entity types matched: %s; types with rules: %s)",
			entityArg, signal, strings.Join(sortedKeysOf(byType), ", "), strings.Join(sortedKeysOf(book.Scoping), ", "))
	default:
		return nil, fmt.Errorf("%q matches several entity types with %s scoping rules (%s) — pass a smartscape ID instead",
			entityArg, signal, strings.Join(candidates, ", "))
	}
	etype := candidates[0]
	rule := book.Scoping[etype][signal]

	res := &ScopeResult{Signal: signal, Entities: entities, Coverage: rule.Coverage}
	sort.Strings(dropped)
	for _, d := range dropped {
		res.Notes = append(res.Notes, fmt.Sprintf("ignored %s entities sharing the name (no %s scoping rule for that type)", d, signal))
	}
	if rule.Hop != "" {
		return resolveHop(ctx, runner, rule, res)
	}

	res.Strategy = "filter"
	parts := make([]string, 0, len(entities))
	for _, e := range entities {
		rendered, rerr := renderScopeFilter(rule.Filter, e)
		if rerr != nil {
			return nil, rerr
		}
		parts = append(parts, rendered)
	}
	res.Filter = strings.Join(parts, " or ")
	if len(parts) > 1 {
		res.Filter = "(" + res.Filter + ")"
		res.Notes = append(res.Notes, fmt.Sprintf("%d entities share this name — the filter covers all of them", len(parts)))
	}
	if rule.Coverage != nil && *rule.Coverage < 0.9 {
		res.Notes = append(res.Notes, fmt.Sprintf(
			"coverage %.2f: the filtered field is carried on only part of the records — results are a lower bound", *rule.Coverage))
	}
	return res, nil
}

// resolveHop widens an entity to its topology neighbours (e.g. SERVICE
// runs_on pods) and emits a name-based filter — direct entity stamping on
// this signal silently lies at its measured coverage, so the hop is the
// authoritative strategy.
func resolveHop(ctx context.Context, runner Runner, rule ScopeRule, res *ScopeResult) (*ScopeResult, error) {
	res.Strategy = "hop:" + rule.Hop
	var podNames, containerNames []string
	for _, e := range res.Entities {
		q := fmt.Sprintf(`smartscapeEdges "%s" | filter source_id == toSmartscapeId("%s") | fieldsAdd tname = getNodeName(target_id) | fields target_id, tname | limit 500`, rule.Hop, e.ID)
		out, err := runner.RunQuery(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("topology hop query failed: %w", err)
		}
		for _, rec := range out.Records {
			id, _ := rec["target_id"].(string)
			name, _ := rec["tname"].(string)
			if name == "" {
				continue
			}
			switch {
			case strings.HasPrefix(id, "K8S_POD-"):
				podNames = append(podNames, name)
			case strings.HasPrefix(id, "CONTAINER-"):
				containerNames = append(containerNames, name)
			}
		}
	}
	sort.Strings(podNames)
	switch {
	case len(podNames) > 0:
		res.Targets = dedupe(podNames)
		res.Filter = fmt.Sprintf("in(k8s.pod.name, {%s})", quoteJoin(res.Targets))
		res.Notes = append(res.Notes,
			"pod-name filter via "+rule.Hop+" topology; pod names rotate on redeploys — re-resolve for older timeframes")
	case len(containerNames) > 0:
		res.Targets = dedupe(containerNames)
		res.Filter = fmt.Sprintf("in(k8s.container.name, {%s})", quoteJoin(res.Targets))
		res.Notes = append(res.Notes,
			"container-name filter via "+rule.Hop+" topology — container names are not unique per service; results may include other workloads")
	default:
		return nil, fmt.Errorf("no %s targets with usable names found for %s", rule.Hop, res.Entities[0].ID)
	}
	if rule.Coverage != nil {
		res.Notes = append(res.Notes, fmt.Sprintf(
			"direct entity stamping covers only %.2f of records on this signal — that is why the hop is authoritative", *rule.Coverage))
	}
	return res, nil
}

// lookupEntities turns an ID or display name into resolved entities.
func lookupEntities(ctx context.Context, runner Runner, arg string) ([]ScopeEntity, error) {
	if entityIDRe.MatchString(arg) {
		etype := arg[:strings.IndexByte(arg, '-')]
		q := fmt.Sprintf(`smartscapeNodes "%s" | filter id == toSmartscapeId("%s") | fields id, name | limit 1`, etype, arg)
		out, err := runner.RunQuery(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("entity lookup failed: %w", err)
		}
		name := arg
		if len(out.Records) > 0 {
			if n, ok := out.Records[0]["name"].(string); ok && n != "" {
				name = n
			}
		}
		return []ScopeEntity{{ID: arg, Name: name, Type: etype}}, nil
	}

	q := fmt.Sprintf(`smartscapeNodes "*" | filter name == %s | fields id, name, type | limit 20`, template.DQLString(arg))
	out, err := runner.RunQuery(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("entity lookup failed: %w", err)
	}
	if len(out.Records) == 0 {
		return nil, fmt.Errorf("no smartscape entity named %q — pass a smartscape ID (e.g. SERVICE-...) or an exact display name", arg)
	}
	var entities []ScopeEntity
	for _, rec := range out.Records {
		id, _ := rec["id"].(string)
		name, _ := rec["name"].(string)
		if id == "" {
			continue
		}
		entities = append(entities, ScopeEntity{ID: id, Name: name, Type: id[:strings.IndexByte(id, '-')]})
	}
	return entities, nil
}

// renderScopeFilter renders a scoping filter template with .id/.name.
func renderScopeFilter(filterTmpl string, e ScopeEntity) (string, error) {
	tmpl, err := texttemplate.New("scope").Parse(filterTmpl)
	if err != nil {
		return "", fmt.Errorf("invalid scoping filter template: %w", err)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, map[string]string{"id": e.ID, "name": e.Name}); err != nil {
		return "", err
	}
	return b.String(), nil
}

func quoteJoin(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = template.DQLString(n)
	}
	return strings.Join(quoted, ", ")
}

func dedupe(items []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range items {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func sortedKeysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
