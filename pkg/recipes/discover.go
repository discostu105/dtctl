package recipes

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RunResult is one probe/query outcome as the generator sees it.
type RunResult struct {
	Records []map[string]interface{}
	Seconds float64
	Partial string // truncation notification, when any
}

// Runner executes DQL on the live environment. The cmd layer implements it
// over the existing DQL executor (with scan caps); tests implement it over
// fixtures.
type Runner interface {
	RunQuery(ctx context.Context, dql string) (*RunResult, error)
}

// DiscoverOptions parameterizes a generation run.
type DiscoverOptions struct {
	ContextName string
	Generator   string
	Now         func() time.Time
	Segments    []SegmentFact // pre-fetched by the caller (segments come from the API, not DQL)
	PrevBook    *Book         // carried: declared facts + local recipes (bodies never rewritten)
	FactsOnly   bool          // phase-1 mode: facts + scoping only, no recipe probing
	// Budget (mandatory, not advisory — §4.1): discovery aborts with a
	// partial book rather than overrunning.
	BudgetQueries int     // 0 = 250
	BudgetSeconds float64 // 0 = 600
}

// DiscoverReport is the consumption receipt of a generation run.
type DiscoverReport struct {
	Queries  int
	Seconds  float64
	Probed   int
	Disabled int
	Unprobed int
	Notes    []string
}

type budgetRunner struct {
	runner  Runner
	report  *DiscoverReport
	queries int
	seconds float64
}

var errBudgetExhausted = fmt.Errorf("discovery budget exhausted")

func (b *budgetRunner) run(ctx context.Context, dql string) (*RunResult, error) {
	if b.queries <= 0 || b.seconds <= 0 {
		return nil, errBudgetExhausted
	}
	res, err := b.runner.RunQuery(ctx, dql)
	b.report.Queries++
	b.queries--
	if res != nil {
		b.report.Seconds += res.Seconds
		b.seconds -= res.Seconds
	}
	return res, err
}

// Discover runs the discovery battery and pack instantiation against a live
// environment and returns the generated recipe book plus its consumption
// receipt. Probes run cheapest-first: data objects → buckets → census →
// metric catalog → carriage → capability probes → recipe bodies.
func Discover(ctx context.Context, runner Runner, packs []*Pack, opts DiscoverOptions) (*Book, *DiscoverReport, error) {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	report := &DiscoverReport{}
	br := &budgetRunner{
		runner:  runner,
		report:  report,
		queries: valueOr(opts.BudgetQueries, 250),
		seconds: valueOrF(opts.BudgetSeconds, 600),
	}

	book := &Book{
		APIVersion: APIVersion,
		Kind:       KindBook,
		Metadata: BookMetadata{
			Name:        opts.ContextName,
			Context:     opts.ContextName,
			GeneratedAt: now().UTC().Format(time.RFC3339),
			Generator:   opts.Generator,
		},
		Recipes:  map[string]*Recipe{},
		Disabled: map[string]*Disabled{},
	}
	for _, p := range packs {
		book.Metadata.Packs = append(book.Metadata.Packs, PackRef{Name: p.Metadata.Name, Version: p.Metadata.Version})
	}
	if opts.PrevBook != nil {
		book.Declared = opts.PrevBook.Declared
	}
	book.Facts.Segments = opts.Segments

	// --- facts battery (hard errors here abort: without facts nothing below is meaningful)
	dataObjects, err := br.stringColumn(ctx, "fetch dt.system.data_objects | fields name | sort name asc | limit 5000", "name")
	if err != nil {
		return nil, report, fmt.Errorf("data-object discovery failed: %w", err)
	}
	book.Facts.DataObjects = dataObjects
	objects := stringSet(dataObjects)

	if buckets, err := br.stringColumn(ctx, "fetch dt.system.buckets | fields name | sort name asc | limit 1000", "name"); err == nil {
		book.Facts.Buckets = buckets
	} else if err != errBudgetExhausted {
		report.Notes = append(report.Notes, "bucket discovery failed: "+firstLine(err.Error()))
	}

	census := map[string]int64{}
	if res, err := br.run(ctx, `smartscapeNodes "*" | summarize c = count(), by:{type} | sort c desc | limit 1000`); err == nil {
		for _, rec := range res.Records {
			if t, ok := rec["type"].(string); ok {
				census[t] = asInt64(rec["c"])
			}
		}
		book.Facts.EntityTypes = census
	} else if err != errBudgetExhausted {
		report.Notes = append(report.Notes, "entity census failed: "+firstLine(err.Error()))
	}

	// Capability definitions merged across packs (later pack wins).
	defs := map[string]*CapabilityDef{}
	for _, p := range packs {
		for name, def := range p.Capabilities {
			defs[name] = def
		}
	}

	var metricKeys []string
	if anyMetricDef(defs) {
		if keys, err := br.stringColumn(ctx, "metrics from:now()-2h | summarize c = count(), by:{metric.key} | limit 10000", "metric.key"); err == nil {
			metricKeys = keys
		} else if err != errBudgetExhausted {
			report.Notes = append(report.Notes, "metric catalog failed: "+firstLine(err.Error()))
		}
	}

	book.Facts.FieldCarriage = br.measureCarriage(ctx, objects, carriageFields(packs), report)

	// --- capability evaluation from definitions (structural shapes first, probes last)
	book.Facts.Capabilities, book.Facts.Absent = br.evaluateCapabilities(ctx, defs, objects, census, metricKeys)

	book.Scoping = builtinScoping(census, book.Facts.FieldCarriage)

	if opts.FactsOnly {
		return book, report, nil
	}

	// --- recipe instantiation: merged pack namespace + carried local recipes
	merged := map[string]*Recipe{}
	for _, p := range packs {
		version := p.Metadata.Version
		for name, r := range p.Recipes {
			rc := *r
			rc.Source = fmt.Sprintf("pack:%s@%s", name, version)
			merged[name] = &rc
		}
	}
	if opts.PrevBook != nil {
		for name, r := range opts.PrevBook.Recipes {
			if r.Source == "" { // local recipe: body is the user's, never rewritten
				rc := *r
				merged[name] = &rc
			}
		}
	}

	names := make([]string, 0, len(merged))
	for n := range merged {
		names = append(names, n)
	}
	sort.Strings(names)

	caps := stringSet(book.Facts.Capabilities)
	at := now().UTC().Format(time.RFC3339)
	for _, name := range names {
		r := merged[name]
		entry, disabled := br.instantiate(ctx, name, r, defs, caps, objects, book.Facts.FieldCarriage, at)
		if disabled != nil {
			book.Disabled[name] = disabled
			report.Disabled++
			continue
		}
		if entry.LastRun != nil {
			report.Probed++
		} else {
			report.Unprobed++
		}
		book.Recipes[name] = entry
	}
	if br.queries <= 0 || br.seconds <= 0 {
		note := fmt.Sprintf("discovery budget exhausted after %d queries / %.0fs — unprobed recipes carry a note", report.Queries, report.Seconds)
		book.Facts.Notes = append(book.Facts.Notes, note)
		report.Notes = append(report.Notes, note)
	}
	return book, report, nil
}

// instantiate evaluates guards for one recipe and probe-executes it,
// returning either a book entry or a classified disabled record.
func (b *budgetRunner) instantiate(ctx context.Context, name string, r *Recipe, defs map[string]*CapabilityDef, caps, objects map[string]bool, carriage map[string]map[string]float64, at string) (*Recipe, *Disabled) {
	// guard 1: requires — every name must have a definition (fail closed)
	for _, cap := range r.Requires {
		if _, defined := defs[cap]; !defined {
			return nil, &Disabled{Class: DisabledClassGuard, At: at,
				Reason: fmt.Sprintf("requires: [%s] — capability undefined by any installed pack", cap)}
		}
		if !caps[cap] {
			return nil, &Disabled{Class: DisabledClassGuard, At: at,
				Reason: fmt.Sprintf("requires: [%s] — capability absent", cap)}
		}
	}
	// guard 2: dataObjects — fetch targets must exist
	for _, obj := range r.DataObjects {
		if !objects[obj] {
			return nil, &Disabled{Class: DisabledClassGuard, At: at,
				Reason: fmt.Sprintf("dataObjects guard: %s not in dt.system.data_objects", obj)}
		}
	}
	// guard 3: minCarriage
	if mc := r.MinCarriage; mc != nil {
		ratio, measured := carriage[mc.Table][mc.Field]
		if !measured || ratio < mc.Ratio {
			return nil, &Disabled{Class: DisabledClassGuard, At: at,
				Reason: fmt.Sprintf("minCarriage guard: %s carried on %.1f%% of %s (< %.1f%%)", mc.Field, ratio*100, mc.Table, mc.Ratio*100)}
		}
	}

	entry := &Recipe{Source: r.Source}
	if r.Source == "" { // local recipe keeps its full body in the book
		entry = cloneLocal(r)
	}

	required := RequiredParams(r)
	if len(required) > 0 {
		entry.Note = fmt.Sprintf("not probed: requires params (%s)", strings.Join(required, ", "))
		return entry, nil
	}

	rendered, err := Render(r, nil)
	if err != nil {
		return nil, &Disabled{Class: DisabledClassGuard, At: at, Reason: "render error: " + firstLine(err.Error())}
	}

	res, err := b.run(ctx, rendered.DQL)
	if err == errBudgetExhausted {
		entry.Note = "not probed: discovery budget exhausted"
		return entry, nil
	}
	if err != nil {
		return nil, &Disabled{Class: DisabledClassGuard, At: at, Reason: "query error: " + firstLine(err.Error())}
	}

	if len(res.Records) > 0 {
		entry.LastRun = stampFor(res, rendered.DQL, at)
		return entry, nil
	}

	// Empty result: classify with the verify probe when there is one.
	if r.Verify != nil && r.Verify.Probe != "" {
		probe, perr := b.run(ctx, r.Verify.Probe)
		if perr == errBudgetExhausted {
			entry.Note = "not probed: discovery budget exhausted"
			return entry, nil
		}
		if perr != nil {
			return nil, &Disabled{Class: DisabledClassGuard, At: at, Reason: "verify probe error: " + firstLine(perr.Error())}
		}
		if probeVerifies(probe, r.Verify.Expect) {
			zero := 0
			entry.LastRun = &LastRun{At: at, Records: &zero, Seconds: round2(res.Seconds), Empty: "legitimate", Params: ParamsProvenanceDefault}
			entry.Note = "probe verified the capability; no matches in the window"
			return entry, nil
		}
		reason := "probe returned 0 records"
		if r.Verify.Expect == "values" {
			reason = "probe returned no non-zero values"
		}
		return nil, &Disabled{Class: DisabledClassProbeEmpty, At: at, Reason: reason + " — event absence only: re-probed on every refresh"}
	}

	// No probe: widen-on-empty is universal generator behavior — retry at 24h
	// and record the widened default as an override.
	if widened, orig, ok := widenTo24h(rendered.DQL); ok {
		wres, werr := b.run(ctx, widened)
		if werr == nil && len(wres.Records) > 0 {
			entry.Override = fmt.Sprintf("widened %s to now()-24h — 0 in the default window, %d at 24h", orig, len(wres.Records))
			entry.LastRun = stampFor(wres, widened, at)
			return entry, nil
		}
	}
	zero := 0
	entry.LastRun = &LastRun{At: at, Records: &zero, Seconds: round2(res.Seconds), Params: ParamsProvenanceDefault}
	entry.Note = "0 records; unverified (recipe has no probe)"
	return entry, nil
}

func cloneLocal(r *Recipe) *Recipe {
	rc := *r
	rc.LastRun = nil
	rc.Override = ""
	rc.Note = ""
	return &rc
}

// stampFor builds a lastRun stamp from an execution outcome.
func stampFor(res *RunResult, dql, at string) *LastRun {
	n := len(res.Records)
	lr := &LastRun{At: at, Records: &n, Seconds: round2(res.Seconds), Params: ParamsProvenanceDefault}
	if limit, ok := lastLimit(dql); ok && n == limit {
		lr.LimitHit = true
	}
	if res.Partial != "" {
		lr.Partial = firstLine(res.Partial)
	}
	return lr
}

// probeVerifies interprets a probe result per expect: records (default) —
// >=1 record; values — a single-row aggregate needs at least one non-zero
// numeric value (one row of zeros is not verified).
func probeVerifies(res *RunResult, expect string) bool {
	if expect == "values" && len(res.Records) == 1 {
		for _, v := range res.Records[0] {
			if f, ok := asFloat(v); ok && f != 0 {
				return true
			}
		}
		return false
	}
	return len(res.Records) > 0
}

// evaluateCapabilities evaluates every definition: structural shapes against
// the discovered facts, probe shapes against the live environment. A probe
// hard error is evidence of absence (e.g. inner-map access on a missing
// field), not of breakage.
func (b *budgetRunner) evaluateCapabilities(ctx context.Context, defs map[string]*CapabilityDef, objects map[string]bool, census map[string]int64, metricKeys []string) (present, absent []string) {
	names := make([]string, 0, len(defs))
	for n := range defs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		def := defs[name]
		ok := false
		switch {
		case def.DataObject != "":
			ok = objects[def.DataObject]
		case len(def.EntityTypes) > 0:
			for _, pattern := range def.EntityTypes {
				for t, c := range census {
					if c > 0 && globMatch(pattern, t) {
						ok = true
						break
					}
				}
			}
		case def.MetricKey != "":
			for _, k := range metricKeys {
				if globMatch(def.MetricKey, k) {
					ok = true
					break
				}
			}
		case def.Probe != "":
			if res, err := b.run(ctx, def.Probe); err == nil && len(res.Records) > 0 {
				ok = true
			}
		}
		if ok {
			present = append(present, name)
		} else {
			absent = append(absent, name)
		}
	}
	return present, absent
}

// measureCarriage batches countIf(isNotNull(...)) probes — one sampled,
// capped scan per table — into a per-table, per-field carriage matrix.
func (b *budgetRunner) measureCarriage(ctx context.Context, objects map[string]bool, fields map[string][]string, report *DiscoverReport) map[string]map[string]float64 {
	matrix := map[string]map[string]float64{}
	tables := make([]string, 0, len(fields))
	for t := range fields {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	for _, table := range tables {
		if !objects[table] {
			continue
		}
		cols := fields[table]
		var parts []string
		for i, f := range cols {
			parts = append(parts, fmt.Sprintf("f%d = countIf(isNotNull(%s))", i, f))
		}
		q := fmt.Sprintf("fetch %s, from:now()-24h, samplingRatio:100 | summarize total = count(), %s", table, strings.Join(parts, ", "))
		res, err := b.run(ctx, q)
		if err != nil || len(res.Records) == 0 {
			if err != nil && err != errBudgetExhausted {
				report.Notes = append(report.Notes, fmt.Sprintf("carriage probe failed for %s: %s", table, firstLine(err.Error())))
			}
			continue
		}
		rec := res.Records[0]
		total := asInt64(rec["total"])
		if total == 0 {
			continue
		}
		row := map[string]float64{}
		for i, f := range cols {
			row[f] = round4(float64(asInt64(rec[fmt.Sprintf("f%d", i)])) / float64(total))
		}
		matrix[table] = row
	}
	return matrix
}

// carriageFields is the measured field list per table: a core set of
// commonly-filtered fields plus every minCarriage guard field in the packs.
func carriageFields(packs []*Pack) map[string][]string {
	fields := map[string][]string{
		"spans": {"http.request.method", "http.method", "request.is_failed", "transaction.is_failed",
			"dt.smartscape.service", "dt.smartscape.host", "dt.smartscape.k8s_pod"},
		"logs": {"k8s.pod.name", "k8s.namespace.name", "loglevel",
			"dt.smartscape.service", "dt.smartscape.host", "dt.smartscape.k8s_pod"},
	}
	for _, p := range packs {
		for _, r := range p.Recipes {
			if mc := r.MinCarriage; mc != nil && mc.Table != "" && mc.Field != "" {
				if !contains(fields[mc.Table], mc.Field) {
					fields[mc.Table] = append(fields[mc.Table], mc.Field)
				}
			}
		}
	}
	return fields
}

// builtinScoping emits the entity→signal scoping rules for entity types
// present on this environment, with coverage derived from measured carriage —
// one measurement, two views, never maintained independently.
func builtinScoping(census map[string]int64, carriage map[string]map[string]float64) map[string]map[string]ScopeRule {
	cov := func(table, field string) *float64 {
		if v, ok := carriage[table][field]; ok {
			return &v
		}
		return nil
	}
	scoping := map[string]map[string]ScopeRule{}
	if census["SERVICE"] > 0 {
		scoping["SERVICE"] = map[string]ScopeRule{
			"spans":    {Filter: `dt.smartscape.service == toSmartscapeId("{{.id}}")`, Coverage: cov("spans", "dt.smartscape.service")},
			"logs":     {Hop: "runs_on", Coverage: cov("logs", "dt.smartscape.service")},
			"problems": {Filter: `matchesPhrase(arrayToString(affected_entity_ids), "{{.id}}")`},
		}
	}
	if census["K8S_POD"] > 0 {
		scoping["K8S_POD"] = map[string]ScopeRule{
			"logs":  {Filter: `k8s.pod.name == "{{.name}}"`, Coverage: cov("logs", "k8s.pod.name")},
			"spans": {Filter: `dt.smartscape.k8s_pod == toSmartscapeId("{{.id}}")`, Coverage: cov("spans", "dt.smartscape.k8s_pod")},
		}
	}
	if census["HOST"] > 0 {
		scoping["HOST"] = map[string]ScopeRule{
			"logs":  {Filter: `dt.smartscape.host == toSmartscapeId("{{.id}}")`, Coverage: cov("logs", "dt.smartscape.host")},
			"spans": {Filter: `dt.smartscape.host == toSmartscapeId("{{.id}}")`, Coverage: cov("spans", "dt.smartscape.host")},
		}
	}
	return scoping
}

// --- small helpers

func (b *budgetRunner) stringColumn(ctx context.Context, dql, column string) ([]string, error) {
	res, err := b.run(ctx, dql)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, rec := range res.Records {
		if s, ok := rec[column].(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

func anyMetricDef(defs map[string]*CapabilityDef) bool {
	for _, d := range defs {
		if d.MetricKey != "" {
			return true
		}
	}
	return false
}

var limitRe = regexp.MustCompile(`\|\s*limit\s+(\d+)`)

// lastLimit returns the value of the last `| limit N` in a query.
func lastLimit(dql string) (int, bool) {
	matches := limitRe.FindAllStringSubmatch(dql, -1)
	if len(matches) == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(matches[len(matches)-1][1])
	return n, err == nil
}

var windowRe = regexp.MustCompile(`from:\s*now\(\)\s*-\s*(\d+)([smh])`)

// widenTo24h rewrites the first sub-24h `from:now()-X` window to 24h.
func widenTo24h(dql string) (widened, original string, ok bool) {
	m := windowRe.FindStringSubmatch(dql)
	if m == nil {
		return "", "", false
	}
	n, _ := strconv.Atoi(m[1])
	hours := float64(n)
	switch m[2] {
	case "s":
		hours /= 3600
	case "m":
		hours /= 60
	}
	if hours >= 24 {
		return "", "", false
	}
	return strings.Replace(dql, m[0], "from:now()-24h", 1), m[0], true
}

func globMatch(pattern, s string) bool {
	ok, err := path.Match(pattern, s)
	return err == nil && ok
}

func stringSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, s := range items {
		set[s] = true
	}
	return set
}

func contains(items []string, s string) bool {
	for _, it := range items {
		if it == s {
			return true
		}
	}
	return false
}

func asInt64(v interface{}) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	}
	return 0
}

func asFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 220 {
		s = s[:220] + "…"
	}
	return s
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

func round4(f float64) float64 {
	return float64(int(f*10000+0.5)) / 10000
}

func valueOr(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

func valueOrF(v, def float64) float64 {
	if v > 0 {
		return v
	}
	return def
}
