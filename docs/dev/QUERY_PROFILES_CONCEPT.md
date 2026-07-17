# Query Profiles — Concept

> Status: concept / discussion draft.
> Goal: make AI agents (and humans) efficient on a **specific** Dynatrace tenant,
> without hardcoding opinionated DQL into dtctl.
> 2026-07-17: core claims verified against two live tenants (see §10);
> schema refined with carriage/coverage findings. Complete example files:
> [examples/queryprofile/](examples/queryprofile/).

## 1. Problem

dtctl is deliberately shallow on DQL: it executes queries, it doesn't know them.
That is the right call for the core CLI, but it pushes the hard part onto the
caller — and for AI agents the hard part is expensive:

- **Grail fails silently — or worse, partially.** A wrong field name or the
  wrong semconv-era field returns `{"records":[]}` with exit 0, so an agent
  can't distinguish "nothing there" from "wrong query". (Some cases now emit a
  warning — e.g. comparing a smartscape ID to a string literal — but dtctl does
  not yet surface these structurally; see §3.) The verified-worse case is
  **partial carriage**: on live tenants, `dt.smartscape.service` is present on
  4–20% of log records — a naive service filter on logs returns *non-empty but
  silently incomplete* results, which no error channel will ever flag.
- **Every tenant is different.** OTel-only vs OneAgent, k8s vs cloud vs RUM,
  different buckets, different tagging strategies for identifying ownership and
  environment. Generic examples (dynatrace-for-ai has hundreds) are a great
  syllabus but half of them return empty on any given tenant, and the agent
  doesn't know which half.
- **The knowledge exists but is trapped.** The dynatui catalog (tui branch)
  encodes exactly this domain knowledge — curated, live-validated DQL per noun,
  entity→filter-field mappings per data source, dual-era coalescing, discovery
  queries — but as Go code inside a TUI, invisible to agents and to dtctl.

## 2. Core idea: the Query Profile

A **Query Profile** is a per-tenant YAML artifact that dtctl loads per context.
It has three kinds of content:

1. **Facts** — what this tenant *is*: which capabilities/data sources exist,
   entity-type census, semconv era(s), buckets, tagging conventions. An agent
   reads this once instead of discovering it by failed queries.
2. **Recipes** — named, parameterized, *verified-against-this-tenant* DQL
   queries with descriptions, typed params, and follow-up links.
3. **Scoping rules** — how to reference an entity of type X in data source Y
   (the `SignalFilter` knowledge from dynatui), exposed as data + template
   helpers rather than trial and error.

The profile is **generated, not hand-written** (though hand-editing and team
layers are supported), and it is **a cache of discovery with provenance, not a
hardcode** — which keeps it compatible with dynatui's ADR-0010 ("curated
defaults, runtime discovery"): the profile *is* the runtime discovery, persisted
and validated, with a refresh story.

### 2.1 Three layers

```text
┌─────────────────────────────────────────────────────────┐
│ Tenant layer (generated)                                │
│   facts, verified recipes, discovered scoping,          │
│   disabled recipes w/ reason ("no RUM data")            │
│   ~/.config/dtctl/profiles/<context>.yaml               │
├─────────────────────────────────────────────────────────┤
│ Org/team layer (hand-curated, shareable)                │
│   your recipes, tagging strategy declarations           │
│   ("owner lives in tags.owner", "env = ns prefix")      │
│   imported from a repo / exported like aliases          │
│   — org-level SCOPING lives in Dynatrace Segments       │
│     where possible; this layer references them (§5)     │
├─────────────────────────────────────────────────────────┤
│ Base pack (curated, universal, versioned)               │
│   recipe *templates* with capability guards             │
│   ("requires: [k8s]", "requires: [rum]"),               │
│   dual-era coalesce pairs, ID-cast rules                │
│   — sourced from dynatui catalog + learnings.md,        │
│     and convertible from dynatrace-for-ai examples      │
└─────────────────────────────────────────────────────────┘
```

The generator takes base pack + tenant discovery + org declarations and emits
the tenant layer. Precedence on lookup: tenant > org > base (same layered
pattern as `EffectiveSpillConfig`).

### 2.2 Schema sketch

```yaml
apiVersion: dtctl.dev/v1alpha1
kind: QueryProfile
metadata:
  name: acme-prod
  context: prod                      # optional binding to a dtctl context
  generatedAt: 2026-07-17T09:00:00Z
  generator: dtctl profile generate v0.1
  pack: dynatrace-recipes v2026.07   # base pack provenance

facts:
  capabilities: [k8s, otel-spans, logs, davis, security]   # detected
  absent: [rum, aws, synthetic, bizevents]                  # detected empty
  entityTypes: { K8S_POD: 1234, SERVICE: 210, HOST: 42 }   # census
  buckets: [default_logs_events, custom_audit]
  # Era/field carriage is measured PER FIELD PAIR, not tenant-wide: live tenants
  # dual-write some pairs (request.is_failed AND transaction.is_failed both
  # 100%) while being era-split on others (http.method 13% / http.request.method
  # 87%). A single "semconvEras: [otel]" fact is too coarse — recipes consult
  # the pair they actually filter on.
  fieldCarriage:
    spans:
      http.request.method: 0.87      # otel era
      http.method: 0.13              # oneagent era — coalesce both
      request.is_failed: 1.0         # dual-written: either works
      transaction.is_failed: 1.0
      dt.smartscape.service: 1.0     # spans reliably carry service IDs
      dt.smartscape.host: 0.84       # partial! host-scoped span queries lose 16%
    logs:
      k8s.pod.name: 0.58
      dt.smartscape.service: 0.20    # partial — see scoping.SERVICE.logs
      dt.smartscape.k8s_pod: 0.0     # never carried (verified on 2 tenants)
  tagging: none                      # verified: entity tags empty on this tenant;
                                     # ownership via k8s.namespace.name conventions
  segments:                          # discovered via `dtctl get segments` — §5
    - { uid: gAAAAAAAAAA, name: Log bucket, variables: [bucket] }
    - { uid: hBBBBBBBBBB, name: Host group, variables: [hostgroup] }
  notes:
    - "Payment services log to bucket custom_audit, not default"

scoping:                             # entity type -> per-signal filter idiom
  SERVICE:
    spans:
      filter: 'dt.smartscape.service == toSmartscapeId("{{.id}}")'
      coverage: 1.0                  # verified: 100% of spans carry it
    logs:
      filter: hop                    # runs_on topology widening
      coverage: 0.20                 # direct service-stamping exists but is
                                     # partial — hop is authoritative, the
                                     # stamped subset alone silently lies
    problems:
      filter: 'matchesPhrase(arrayToString(affected_entity_ids), "{{.id}}")'
  K8S_POD:
    logs:
      filter: 'k8s.pod.name == "{{.name}}"'  # logs carry names, never pod IDs
    spans:
      filter: 'dt.smartscape.k8s_pod == toSmartscapeId("{{.id}}")'
      coverage: 0.47

queries:
  pods-restarting:
    description: Pods with container restarts, worst first
    params:
      namespace: { default: "", description: "exact k8s namespace, empty = all" }
      timeframe: { default: "now()-2h" }
    dql: |
      smartscapeNodes "K8S_POD", from:{{.timeframe}}
      {{- if .namespace }}
      | filter k8s.namespace.name == "{{.namespace}}"
      {{- end }}
      | parse k8s.object, "JSON:obj"
      ...
    verified: { at: 2026-07-17T09:00:12Z, records: 37 }
    followups: [pod-logs, pod-events]

  slow-payment-requests:             # org-layer recipe, tenant-specific
    description: p95 latency of payment services (tag owner=payments)
    ...

disabled:
  rum-slowest-pages: { reason: "no RUM data in tenant (user.events empty)" }
```

Notes on the schema:

- **Params use the existing engine.** Recipes render through
  `pkg/util/template` (`{{.var}}`, `default`) and the existing `--set` flag —
  no new templating system.
- **The hard logic stays in code, not YAML.** dynatui's scope knowledge is
  partly closures (era coalescing, ID casts, hop widening). Rather than
  inventing an expression language, dtctl exposes it as **template functions**:
  `{{ scopeFilter .entity "logs" }}`, `{{ eraCoalesce "db.system" }}`,
  `{{ smartscapeId .id }}`. Universal knowledge = code in dtctl/sdk; tenant
  variability = data in the profile. This is what makes the profile
  serializable at all.
- **`verified` is first-class.** Because Grail fails silently, a recipe without
  a verification stamp is a guess. The generator runs each candidate with a
  small limit and records the result; recipes that return empty on this tenant
  are moved to `disabled` with a reason instead of silently shipping.
- **Display names via `getNodeName()`, not raw fields.** Verified on live data:
  grouping by `service.name` yields null for whole service populations
  (OneAgent-fed spans). `getNodeName(dt.smartscape.service)` resolves the
  Smartscape display name regardless of era — recipe templates should prefer it
  over coalescing raw name fields.
- **Verification records coverage, not just a boolean.** The busiest service on
  a verified tenant emits *zero* log lines — a correct scoping rule and an
  empty result coexist legitimately. So `verified` on entity-scoped recipes
  stores a coverage map (e.g. "logs exist for 31 of 144 services") rather than
  one non-empty probe, and the envelope can tell the agent which case it hit.
- **Params can carry a `values:` DQL** (idea borrowed from Segment variables,
  §5): an optional query that yields the parameter's valid values on this
  tenant. Agents get discoverable, tenant-valid arguments; humans get shell
  completion.

### 2.3 Example files, size, and complexity

Worked examples live next to this doc — all recipe DQL in them was executed
against live tenants first:

- [examples/queryprofile/tenant-profile.example.yaml](examples/queryprofile/tenant-profile.example.yaml)
  — the *materialized* tenant-layer form: facts (incl. carriage matrix and
  discovered segments), scoping with coverage, 11 fully-templated recipes,
  and disabled entries with reasons.
- [examples/queryprofile/base-pack.example.yaml](examples/queryprofile/base-pack.example.yaml)
  — pack mechanisms: capability guards (`requires:`), carriage-conditional
  variants (the era decision as data), and verification specs (probe vs
  coverage-map).
- [examples/queryprofile/base-pack.full.example.yaml](examples/queryprofile/base-pack.full.example.yaml)
  — a **full 44-recipe pack** mined from the dynatui catalog (15) and the
  dynatrace-for-ai skills (29), every DQL body executed verbatim on two live
  tenants (§10.1).
- [examples/queryprofile/tenant-profile.box.generated.yaml](examples/queryprofile/tenant-profile.box.generated.yaml)
  / [tenant-profile.demo.generated.yaml](examples/queryprofile/tenant-profile.demo.generated.yaml)
  / [tenant-profile.fxz.generated.yaml](examples/queryprofile/tenant-profile.fxz.generated.yaml)
  / [tenant-profile.gmg.generated.yaml](examples/queryprofile/tenant-profile.gmg.generated.yaml)
  — **actual generation outputs** for four tenants of very different
  character, in the *referencing* form (pack pointer + verification stamp
  instead of DQL copies): the same pack survives as 38, 44, 41, and 42 usable
  recipes with per-tenant disabled lists, floors, and scan-limit partials.

**How large do profiles grow?** Measured, not estimated — the full 44-recipe
pack is **530 lines** (~12 lines/recipe in compact pack form; a fully-templated
recipe with params runs ~25 lines), and the two *generated* tenant profiles in
referencing form are **~160 lines each** regardless of pack size:

| Part | Size (measured) |
|---|---|
| 44-recipe pack (compact) | 530 lines |
| fully-templated recipe (materialized) | ~25 lines each |
| generated tenant profile, referencing form | ~160 lines (facts + stamps + overrides + disabled) |
| projected 80-recipe pack, fully templated | ~2,000–2,500 lines, 50–70 KB |

The referencing form is the important discovery: the tenant layer does not
need to duplicate pack DQL — a pack pointer plus a verification stamp (and an
`override:` only where the tenant diverges, e.g. a floored timeframe) keeps
the per-tenant artifact small, diffable, and regenerable.

That is fine on disk and hopeless as agent context — which is why the CLI
surface is progressive: `profile show` returns facts only (~1–2k tokens),
`recipes` returns one line per recipe (~1–2k tokens for 60 recipes), and only
`recipes show <name>` / `run` touch a full recipe. No consumer ever loads the
whole file.

**Complexity governors** (what keeps recipes from becoming a DSL):

- A recipe's DQL body stays ≤ ~20 lines; template logic is limited to
  presence-of-param conditionals (`{{if .namespace}}`). If a recipe needs
  more, the logic moves into a template func (code) or splits into a
  followup recipe.
- No joins/multi-phase flows in recipes (dynatui's two-phase metric probe
  stays code). Correlation is composed by the agent via `followups`, mirroring
  dynatui ADR-0013's "no cross-signal joins in curated views".
- Pack `when:` conditions compare discovered facts only (capabilities,
  carriage thresholds) — deliberately not an expression language.

## 3. CLI surface

```bash
# generation & lifecycle
dtctl profile generate            # discovery + pack instantiation + verification
dtctl profile show [-o json]      # the tenant briefing (facts, small)
dtctl profile verify              # re-validate recipes, flag drift (cron/CI-able)
dtctl profile import team.yaml    # org layer, like alias import/export

# consumption
dtctl recipes                     # list recipes: name, description, params
dtctl run pods-restarting --set namespace=payments
dtctl run pod-logs --set name=checkout-7d9f   # follow-up from previous result
```

- `run` internally = load recipe → render template → existing `DQLExecutor`.
  It tags requests via the existing `dt-client-context` header
  (`context=profile:pods-restarting`) for observability.
- Both commands are statically registered Cobra commands that load YAML at
  runtime, so they appear automatically in `dtctl commands` and the agent
  catalog. Recipes are *not* dynamic top-level commands — that keeps the alias
  resolver and command catalog untouched.
- In agent mode, the envelope's existing `suggestions` channel carries
  `followups` ("next: dtctl run pod-logs --set name=...") — turning the
  dynatui drill graph into agent guidance.
- Read-only by design: recipes are DQL only. No mutation verbs in profiles
  (mirrors dynatui ADR-0011 and keeps them outside the safety-checker surface).
- **Prerequisite: surface Grail notifications in the envelope.** Grail emits
  warnings for some mistakes (verified: comparing a smartscape ID against a
  string literal warns "Convert strings using `toSmartscapeId()`..."), but
  dtctl currently prints them as loose stderr text — they never reach the agent
  envelope's `context.warnings`. Mapping query notifications into the envelope
  is a small, profile-independent fix that gives agents a self-correction
  channel and should land first.

### Trust model

Profiles resolved from the global config dir (`~/.config/dtctl/profiles/`) are
trusted. A repo-local profile (checked into a project) hits the same boundary
as aliases-from-local-config: honored only after an explicit one-time
acknowledgement recorded by content hash. DQL is read-only, but a malicious
local profile could still steer an agent into querying and leaking sensitive
data, so local profiles get the same "untrusted working directory" treatment.

## 4. The generator

### 4.1 What it runs (deterministic discovery)

All of these exist, live-validated, in dynatui today:

| Discovery | Query (from dynatui) | Feeds |
|---|---|---|
| Entity census | `smartscapeNodes "*" \| summarize count(), by:{type}` | `facts.entityTypes`, capability guards |
| Topology schema | `smartscapeEdges "*" \| summarize by:{source_type,type,target_type}` | scoping (hops), followups |
| Data objects & buckets | `fetch dt.system.data_objects` / `dt.system.buckets` | `facts.buckets`, table existence |
| Field docs | `fetch dt.semantic_dictionary.fields` | field validation, era detection |
| Metric availability | `metrics \| summarize by:{metric.key}` + two-phase probe | which sparkline/metric recipes survive |
| Era/carriage matrix | one `summarize countIf(isNotNull(f))` scan per table over the pack's field-pair list | `facts.fieldCarriage`, coalesce behavior |
| Tag/value discovery | `fieldsSummary` on entity `tags`, `k8s.namespace.name`, span attrs | tagging hints (must handle "none") |

Then: filter base-pack recipes by capability guards → render for this tenant →
execute each with `\| limit 1`-style probes → stamp `verified` or move to
`disabled`.

Probe-cost notes (from running the battery live): the whole discovery set is
~15 queries per tenant; carriage probes batch many `countIf(isNotNull(...))`
into a single scan per table; `samplingRatio:` (verified working on logs and
spans) keeps large-tenant scans cheap and is fine for presence/ratio facts.
Tag discovery must probe multiple channels — entity `tags` via
`fieldsSummary` (verified empty on a real tenant), k8s labels, span
attributes — and be able to conclude `tagging: none` rather than inventing
a convention.

### 4.2 Where does it live?

Recommendation: **generator in dtctl (`dtctl profile generate`), knowledge in a
pack, shared logic in the sdk** — not in dynatui.

- The generator needs an authenticated client and context handling; dtctl owns
  both (split-design ADR-0005). Requiring a TUI install to make *agents* work
  would be backwards — agent environments are exactly where dtctl-without-
  dynatui runs.
- The discovery queries and scope-filter helpers move from
  `dynatui/internal/tui/catalog` into a shared home (e.g. `sdk/dql` or
  `pkg/dqlknowledge`). dynatui then consumes them from there — same direction
  as the `sdk/session` promotion, and it removes duplication rather than
  creating it.
- The **base pack is data, versioned separately** (own repo, or contributed
  into dynatrace-for-ai as a machine-readable companion to the skills). Both
  the dtctl generator and (later) dynatui can consume it. This is also the
  reconciliation with DYNATUI_SPLIT_DESIGN.md: the ViewSpec *catalog* stays
  dynatui-domain, but its distilled, serializable subset (DQL + params +
  followups, minus rendering closures) becomes the pack.

### 4.3 Agent-assisted annotation (optional second phase)

Some tenant knowledge is not deterministically discoverable: "owner lives in
`tags.owner`", "prod is the `prod-*` namespaces", "payment services log to the
audit bucket". A guided profiling session — an agent (via the dtctl skill)
interviews the tenant with `fieldsSummary`/census recipes and the human with
2-3 questions, then writes the org-layer annotations — fits naturally on top:
deterministic generator for facts/verification, agent pass for semantics.
The profile schema doesn't change; only who writes the org layer.

## 5. Relationship to Dynatrace Segments

Grail Segments are the platform-native neighbor of exactly one slice of this
concept, verified live: a segment is a named, versioned, shared, server-side
object of per-`dataObject` filter expressions plus typed variables — and a
variable's value domain is itself a DQL query. Example from a real tenant:

```json
{ "name": "Log bucket",
  "includes": [ { "dataObject": "logs", "filter": "dt.system.bucket = \"$bucket\"" } ],
  "variables": { "type": "query", "value": "fetch dt.system.buckets | ... | fields bucket = name" } }
```

dtctl already applies them at query time: `dtctl query "..." -S "uid?bucket=x"`
(repeatable, AND-combined, inline variable binding).

**Overlap.** Org-level scoping conventions ("team X = this host group / these
buckets / this app slice") are what segments were built for. For that slice,
segments beat profile YAML on every axis that matters: centrally managed, one
owner, versioned, and honored by *all* Dynatrace surfaces (notebooks,
dashboards, apps), not just dtctl. The org layer therefore treats segments as
the **system of record for organizational scoping** and stores references, not
copies.

**Non-overlap.** Segments are filters, not queries: no projection,
aggregation, or followups (recipes remain the query layer, and recipe × `-S`
compose orthogonally at the API level). They carry no facts, no verification,
and cannot express entity-level mechanics — the `runs_on` hop needs a topology
pre-query, and casts/carriage are per-record-type knowledge. They are also
tenant-locked, while the base pack is cross-tenant.

**Integration hooks:**

1. **Discover** — `profile generate` lists segments into `facts.segments`;
   agents scope with `-S <uid>` instead of reconstructing team filters.
2. **Mine** — segment definitions are human-curated tenant semantics in
   machine-readable form ("Host group" ⇒ this org slices by host group). The
   generator reads them before the agent-assisted annotation pass (§4.3) asks
   any human anything.
3. **Reference, don't duplicate** — org-layer scoping that exists as a segment
   is stored as `segments: [uid]` on the recipe (see `audit-log-search` in the
   example profile).
4. **Write back** — conventions that profiling discovers can be deposited as
   segments (`dtctl create segment` exists), making the platform the owner and
   every Dynatrace app a beneficiary.

Caveat: segment application has sharp edges on some API surfaces (e.g. Davis
views rejecting bucket parameters), so "apply segment X" gets the same
verification stamp as everything else.

## 6. Why this saves agent tokens

Today an agent session looks like: read generic DQL reference → compose query →
empty result → guess why → retry (×N). With a profile:

1. `dtctl profile show -o json` — one small call, tenant briefing in context.
2. `dtctl recipes` — pick by description instead of composing from scratch.
3. `dtctl run <recipe> --set ...` — verified query, correct scoping, no silent-
   empty ambiguity: if a *verified* recipe returns empty, "truly nothing" is
   now the likely reading, and the envelope can say so
   (`"this recipe was verified non-empty at generation time"`).
4. Follow-up suggestions replace planning tokens.

The escape hatch is unchanged: raw `dtctl query` remains for everything the
profile doesn't cover, and recipes echo their DQL (like dynatui's `ctrl+q`)
so agents can learn from and modify them — recipes as few-shot examples that
are *known to work on this tenant* beat any static example corpus.

## 7. Other ideas considered (complementary, not competing)

- **Distill-from-usage.** dtctl already tags queries with `dt-client-context`.
  Opt-in local logging of successful agent queries + `dtctl profile distill`
  promotes recurring ones into the org layer — the agent's own trial-and-error
  becomes institutional memory instead of being re-paid every session.
- **Tenant briefing renderer.** `dtctl profile brief -o markdown` emits a
  one-page tenant map for pasting into CLAUDE.md/AGENTS.md, and
  `dtctl skills install` can render it as a per-tenant reference next to the
  generic skill. (Keep the generic skill teaching "call `dtctl profile show`
  at session start" as the primary path — files go stale, calls don't.)
- **Scope resolver as a command.** `dtctl resolve scope <entity-id> --for logs`
  exposes the SignalFilter/hop knowledge directly as a callable, independent of
  any profile — useful even for hand-written queries. (Same code as the
  template funcs; just a second door to it.)
- **Convert dynatrace-for-ai.** Its hundreds of examples are exactly base-pack
  material: add capability guards + params, and the generator turns "hundreds
  of examples, unknown which work" into "the 40 that verifiably work here".
- **MCP surface (later).** Once recipes are typed (name, description, params),
  exposing each as an MCP tool per tenant is mechanical — the most
  token-efficient agent interface possible. Not needed for v1; the CLI +
  agent envelope already serve every supported agent.
- **Rejected: profile as dynamic top-level commands** (`dtctl pods-restarting`).
  Conflicts with the alias resolver, pollutes the command catalog, and gains
  nothing over `dtctl run <name>`.
- **Rejected: full ViewSpec export from dynatui.** Columns/formatting/lens UI
  concerns don't serialize (closures) and don't help agents; only the query,
  params, scoping, and drill graph do. Export the distillate, not the catalog.

## 8. Phasing

1. **v0 — schema + loader + `recipes`/`run`** (no generator). Hand-written
   profiles already deliver value: teams codify their queries per context;
   agents stop re-deriving them. Smallest possible slice, proves the UX.
2. **v1 — base pack + deterministic generator + verification.** Move discovery
   queries + scope helpers from dynatui to the shared layer; author the initial
   pack from the dynatui catalog + learnings.md; `profile generate/verify`.
3. **v2 — org layer + agent-assisted annotation + briefing/skill rendering +
   distill-from-usage.** dynatui optionally consumes profiles (org recipes
   appear as views), closing the loop.

## 9. Open questions

- Pack distribution: bundled snapshot in the dtctl release vs fetched
  (`dtctl profile update-pack`)? Bundled-with-override is the likely answer.
- Verification cost & permissions: `profile generate` runs dozens of probe
  queries — needs a visible budget (dynatui ADR-0013 spirit) and read scopes
  only; document expected Grail consumption.
- Staleness policy: TTL on `facts`? `verify` on first use per day? Explicit
  only?
- Multi-context tenants (same tenant, several contexts): profile keyed by
  environment URL rather than context name?
- Naming: "profile" collides with cloud monitoring profiles in docs; maybe
  "tenant profile" / "query pack" split naming needs a pass.

## 10. Live verification (2026-07-17, two tenants)

The discovery battery and the load-bearing claims were executed via
`dtctl query` against two real tenants ("box" — a dev tenant, "demo" — a large
demo tenant). No customer identifiers below.

**The two tenants are radically different, as predicted.** Box:
OTel-process-centric (3,040 `OTEL_PROCESS` vs **12** `SERVICE` entities),
AWS + Postgres entity inventory, pure-OTel http fields. Demo: K8s + AWS + GCP
+ network-device entities, 144 services, **mixed-era** http fields (13%
OneAgent / 87% OTel), RUM-heavy (13M user events/day). Both tenants had RUM
and bizevents — assumptions about what a tenant lacks were wrong both times,
which is the argument for generating facts instead of assuming them.

**Confirmed as proposed:**

- All discovery queries (census, edges, buckets, data objects, `fieldsSummary`,
  `metrics` catalog) work and are cheap; `samplingRatio:` works for probe scans.
- The ID-cast trap: `dt.smartscape.service == "SERVICE-..."` → 0 records;
  `== toSmartscapeId("SERVICE-...")` → 23k records, same window.
- Logs never carry `dt.smartscape.k8s_pod` (0% on both tenants); k8s name
  fields are the only pod handle in logs.
- The service→logs gap: the busiest demo service by spans has **zero**
  service-stamped log lines; its `runs_on` edges (pod, container, process,
  host) resolve correctly for hop widening.

**Refinements the verification forced (now folded into §§1–4):**

- *Partial carriage is the norm, not absence*: `dt.smartscape.service` on 4%
  (box) / 20% (demo) of logs; `dt.smartscape.host` on 39% / 84% of spans.
  Scoping rules therefore carry `coverage` numbers, and partial fields are
  treated as "silently lying if used alone".
- *Era is per-field-pair, not per-tenant*: box is era-pure on http fields yet
  dual-writes `request.is_failed`/`transaction.is_failed` (both 100%); demo is
  mixed on http. `facts.semconvEras` was replaced by `facts.fieldCarriage`.
- *Both tenants' spans carry both service-ID generations at 100%* — the
  era-coalescing burden sits on attribute fields, not on span service IDs.
- *`service.name` is null for whole span populations* (OneAgent-fed); recipes
  use `getNodeName()` for display names.
- *Verified-empty needs coverage semantics*: the busiest service logging
  nothing is legitimate; boolean verification can't distinguish it from broken
  scoping (§2.2 note).
- *Grail warnings exist but are lost*: the cast mistake now produces a server
  warning which dtctl prints as plain stderr text, outside the agent envelope —
  promoting it into `context.warnings` is a prerequisite fix (§3).
- *`dt.davis.problems` was ~1:1 rows-to-problems on both tenants over 7d* —
  the "transition log" behavior from dynatui's learnings is real but not
  universal; the `takeLast ... by:{display_id}` dedup stays as a cheap
  defensive pattern, not as a load-bearing assumption.

### 10.1 Pack simulation: 44 recipes × 4 tenants (2026-07-17)

To test the whole pipeline in practice, a 44-recipe pack was assembled (15
recipes distilled from the dynatui catalog, 29 from the dynatrace-for-ai
skills — deliberately kept verbatim, including a skill's `status == "ERROR"`
log filter and uppercase `AND`s) and executed end-to-end against four tenants
of very different character: a small OTel-centric dev tenant, a fully-loaded
demo tenant, a network-observability tenant (Juniper/external network devices,
synthetic locations), and a very large enterprise tenant (280k OS services,
66M spans/hour). 176 recipe runs plus classification probes. Results:

| | dev | demo | netobs | large |
|---|---|---|---|---|
| executed cleanly | 44 | 44 | 42 | 43 |
| hard query errors | 0 | 0 | **2** | **1** |
| returned data | 28 | 44 | 38 | 41 |
| verified-empty (threshold/transient) | 10 | 0 | 2 | 1 |
| disabled with evidence | 6 | 0 | 3 | 2 |
| scan-limit partials | 0 | 0 | 0 | **1** |

The two-tenant headline ("zero errors — divergence is only data presence")
did **not** survive tenants three and four, and that correction is the
strongest evidence for the concept: 173/176 runs were clean, but three hard
errors appeared that only per-tenant verification can catch. The run forced
these generator-design lessons, now pack semantics (`verify:`/`portability:`
hints in the full pack file):

1. **Record-count verification lies for single-row aggregates.** A severity
   summary returned "1 record" on a tenant with zero detections — every count
   was 0. Single-row summaries need value-level verification
   (`verify: aggregate-values`). This was a real false-verified caught only
   because the probe was re-checked.
2. **Threshold-empty ≠ capability-absent.** Ten of the dev tenant's 16
   empties were healthy findings ("no host >80% CPU", "no OOMKills") — the
   capability existed (66k `dt.kubernetes.*` series). The generator must
   probe the capability *without* the threshold filter, then stamp the
   thresholded recipe (`verify: threshold`); the two states must read
   differently to an agent.
3. **Floors are load-bearing.** GenAI spans: 0 in a 2h window, 4,566 in 24h
   on the same tenant. dynatui's `floorTimeframe` pattern (sessions, vulns,
   bizevents) becomes a pack-level `floor:` attribute, and the tenant layer
   records the applied floor as an `override:`.
4. **Carriage thresholds disable recipes.** `dt.failure_detection.results`
   exists on the dev tenant — on 5 of 143,707 spans (0.003%). A field that
   sparse makes a recipe misleading, not merely weak; the profile disables it
   with the coverage number as the reason.
5. **Cost belongs in the stamp.** The 24h group-by-content log recipe took
   11.8s on the demo tenant; the metrics catalog took 2.4s on the dev tenant
   and **34s** on the large one. The probe already sees execution time (and
   the envelope carries `scannedBytes`); recording it per recipe lets agents
   prefer the cheap recipe — the profile's answer to dynatui's ADR-0013 query
   budget, and load-bearing at enterprise scale.
6. **Hard errors exist, and they're portability classes, not typos.** The
   dynatui nodes query hard-fails (`INNER_FIELD_OF_FIELD_DOES_NOT_EXIST`) on
   2 of 4 tenants: inner-map access on an absent field (`` `tags:k8s.labels`[…] ``)
   *errors*, unlike top-level absent fields which compare null-safely. And
   `fetch dt.entity.synthetic_test` throws `UNKNOWN_DATA_OBJECT` on a tenant
   that *has* synthetic data in `dt.synthetic.events` — table existence must
   be guarded via `dt.system.data_objects`, not inferred from the capability.
   Both queries were live-validated when written; only cross-tenant probing
   exposed them. Fix shape: carriage-selected variants (label-free nodes) and
   data-object guards.
7. **Scan limits create verified-partial.** On the large tenant the 24h
   bizevents scan stopped at Grail's 500 GB limit and returned plausible
   results *plus a warning* — silently incomplete to any consumer that
   ignores stderr. The profile stamps these `partial:` with the remediation
   (sampling/bucket filter), and the envelope-warnings prerequisite (§3)
   graduates from nice-to-have to mandatory.
8. **Entity types are platform-flavored.** The Postgres recipe
   (`DB_INSTANCE_POSTGRES`) is empty on a tenant holding 56k
   `AZURE_MICROSOFT_DBFORPOSTGRESQL_*` entities — same technology, different
   Smartscape types per integration. Packs need per-platform variants keyed
   off the entity census.
9. **Instrumentation variance is per-field even within one capability.** One
   tenant's GenAI spans (azure.ai.openai) carry usage tokens and provider but
   a null `gen_ai.request.model` — so token recipes verify while by-model
   recipes are structurally empty. A capability flag is never enough; the
   carriage matrix is the real contract.

Smaller confirmations: `smartscapeNodes "AWS_*"` wildcards and the composed
KSPM latest-scan join worked unmodified on all four tenants; the GenAI 24h
floor validated again (0 spans at 2h vs 30 at 24h on the netobs tenant); and
`attacks-recent` proved the transient class (detections exist on all four
tenants over 24h, none from RAP in a 2h window on three of them).
