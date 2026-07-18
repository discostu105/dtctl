package recipes

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/dynatrace-oss/dtctl/pkg/util/template"
)

// Param types. String is the default and renders exclusively through the
// dqlString template func; dql-filter is the one type that renders raw, by
// declared contract (values typically come from scoping rules).
const (
	TypeString     = "string"
	TypeInt        = "int"
	TypeDuration   = "duration"
	TypeEnum       = "enum"
	TypeIdentifier = "identifier"
	TypeDQLFilter  = "dql-filter"
)

// ParamsProvenanceDefault marks a stamp produced by an all-defaults run.
// Drift reasoning and "was N at T" comparisons use only default-param stamps.
const ParamsProvenanceDefault = "default"

var (
	intRe = regexp.MustCompile(`^-?\d+$`)
	// duration accepts timeframe expressions ("now()-30m", "7d", RFC3339
	// timestamps) — anything without quote/pipe/backslash metacharacters.
	durationRe = regexp.MustCompile(`^[0-9A-Za-z ()+:.,/-]+$`)
	// identifier accepts field/bucket/table names (dt.entity.cloud:aws:x, k8s.*).
	identifierRe = regexp.MustCompile(`^[0-9A-Za-z_.:*-]+$`)
)

// validateParam checks one value against its declared type before rendering.
// Non-string values are validated, not escaped — `--set 'limit=100 | drop'`
// is rejected, never interpolated.
func validateParam(name string, p *Param, value string) error {
	switch p.Type {
	case "", TypeString, TypeDQLFilter:
		return nil // string escapes via dqlString; dql-filter is raw by contract
	case TypeInt:
		if !intRe.MatchString(value) {
			return fmt.Errorf("param %q must be an integer, got %q", name, value)
		}
	case TypeDuration:
		if value != "" && !durationRe.MatchString(value) {
			return fmt.Errorf("param %q must be a timeframe expression (e.g. now()-30m, 7d), got %q", name, value)
		}
	case TypeEnum, TypeIdentifier:
		if value != "" && !identifierRe.MatchString(value) {
			return fmt.Errorf("param %q (%s) contains invalid characters: %q", name, p.Type, value)
		}
	default:
		return fmt.Errorf("param %q has unknown type %q", name, p.Type)
	}
	return nil
}

// Rendered is the outcome of rendering a recipe with a set of values.
type Rendered struct {
	DQL        string
	Provenance string // "default" or a short hash of the non-default values
}

// Render validates the caller's --set values against the recipe's typed
// params, applies defaults, and renders the DQL template.
//
//   - a --set key no param declares is an ERROR: a typo'd key must never
//     silently render an unfiltered template branch (RECIPES_CONCEPT.md §2.2)
//   - a param without a default and without a value is an error listing what
//     is required
func Render(r *Recipe, setValues map[string]string) (*Rendered, error) {
	vars := map[string]interface{}{}
	nonDefault := map[string]string{}

	for key, value := range setValues {
		p, ok := r.Params[key]
		if !ok {
			return nil, fmt.Errorf("unknown param %q — declared params: %s", key, paramNames(r))
		}
		if err := validateParam(key, p, value); err != nil {
			return nil, err
		}
		vars[key] = value
		if p.Default == nil || *p.Default != value {
			nonDefault[key] = value
		}
	}

	var missing []string
	for name, p := range r.Params {
		if _, ok := vars[name]; ok {
			continue
		}
		if p.Default == nil {
			missing = append(missing, name)
			continue
		}
		vars[name] = *p.Default
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("recipe requires params: %s (pass --set %s=<value>)",
			strings.Join(missing, ", "), missing[0])
	}

	dql, err := template.RenderTemplate(r.DQL, vars)
	if err != nil {
		return nil, fmt.Errorf("failed to render recipe: %w", err)
	}
	dql = strings.TrimSpace(dql)
	if dql == "" {
		return nil, fmt.Errorf("recipe has no DQL body (pack not installed?)")
	}

	return &Rendered{DQL: dql, Provenance: provenance(nonDefault)}, nil
}

// RequiredParams lists params without defaults, sorted.
func RequiredParams(r *Recipe) []string {
	var required []string
	for name, p := range r.Params {
		if p.Default == nil {
			required = append(required, name)
		}
	}
	sort.Strings(required)
	return required
}

func paramNames(r *Recipe) string {
	if len(r.Params) == 0 {
		return "(none)"
	}
	names := make([]string, 0, len(r.Params))
	for n := range r.Params {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// provenance is "default" for an all-defaults run, else a short hash of the
// sorted non-default key=value pairs — enough to tell a custom-param stamp
// from the default-param baseline without recording the values themselves.
func provenance(nonDefault map[string]string) string {
	if len(nonDefault) == 0 {
		return ParamsProvenanceDefault
	}
	keys := make([]string, 0, len(nonDefault))
	for k := range nonDefault {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%s=%s\n", k, nonDefault[k])
	}
	return hex.EncodeToString(h.Sum(nil))[:8]
}
