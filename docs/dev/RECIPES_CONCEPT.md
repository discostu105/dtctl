# Recipes — Concept

> Status: concept / discussion draft.
> Goal: make AI agents (and humans) efficient on a **specific** Dynatrace
> environment, without hardcoding opinionated DQL into dtctl.
> 2026-07-17: core claims verified against two live tenants (see §10);
> schema refined with carriage/coverage findings; pack simulation extended to
> five tenants (§10.1). Complete example files:
> [examples/recipes/](examples/recipes/).
> 2026-07-18: design review applied (§10.2) — three layers collapsed to
> **two artifacts** (packs + recipe book); `variants:` replaced by
> **portable-first DQL**; verify taxonomy collapsed to `probe`/`expect` plus
> three fixed guard shapes; stamps redefined as a **living cache** refreshed
> on every run; trust model aligned with the actual local-config behavior;
> envelope-warnings prerequisite corrected (mostly already implemented);
> generation got a mandatory budget; privacy guidance added.
> 2026-07-18 (later): rebased onto upstream main and reconciled with the
> merged **Command Profiles** feature (#351), which claimed the word
> "profile" — this concept was renamed **Query Profiles → Tenant Profiles →
> Environment Awareness** (§10.3): `kind: EnvironmentAwareness`,
> `kind: RecipePack`, CLI `dtctl awareness`.
> 2026-07-18 (third round): adversarial review applied and the concept
> re-centered on **recipes** (§10.4) — the feature is Recipes, the
> per-tenant artifact is the **recipe book** (`kind: RecipeBook`), the CLI
> noun is `dtctl recipes` (one bootstrap call: facts + recipe index).
> Stamp write path specified (atomic + locked + first-class read-only
> mode); stamps renamed `lastRun:` with param provenance; `disabled:`
> entries classified and re-probed (never a one-way door); capabilities
> now carry **discovery definitions** in the pack (no more free-floating
> enum); typed params replace the false "escaped by default"; the probe
> lifecycle command is `refresh` (collision with `dtctl verify`);
> diagnose-on-empty extended to **diagnose-on-filter**; phasing re-cut
> facts-first with the pack machinery gated on a measured eval (§6.1);
> example transcripts renamed and relabeled per the privacy rule.
> 2026-07-18 (completion pass): examples completed against the
> dynatrace-for-ai skills and the dynatui catalog (§10.5) — the `followups:`
> drill graph authored **in the pack** (61 edges; serialized from dynatui's
> `Spec.Drills`), seven drill-target/coverage recipes added (entity-logs /
> entity-spans / entity-problems / trace-by-id / service-errors /
> azure-census / gcp-census), followup semantics specified (§2.2), the
> licensing-gated `dps` capability re-defined as a probe, and transcript
> capability lists completed from measured evidence.

## 1. Problem

dtctl is deliberately shallow on DQL: it executes queries, it doesn't know them.
That is the right call for the core CLI, but it pushes the hard part onto the
caller — and for AI agents the hard part is expensive:

- **Grail fails silently — or worse, partially.** A wrong field name or the
  wrong semconv-era field returns `{"records":[]}` with exit 0, so an agent
  can't distinguish "nothing there" from "wrong query". (Some cases now emit a
  warning — e.g. comparing a smartscape ID to a string literal — and agent
  mode already forwards WARNING-severity query notifications into the
  envelope's `context.warnings`; the remaining gap is advice coverage for
  cases like the cast hint, see §3.) The verified-worse case is
  **partial carriage**: on live tenants, `dt.smartscape.service` is present on
  4–20% of log records — a naive service filter on logs returns *non-empty but
  silently incomplete* results, which no error channel will ever flag.
- **Every environment is different.** OTel-only vs OneAgent, k8s vs cloud vs
  RUM, different buckets, different tagging strategies for identifying
  ownership and environment tiers. Generic examples (dynatrace-for-ai has
  hundreds) are a great syllabus but half of them return empty on any given
  environment, and the agent doesn't know which half.
- **The knowledge exists but is trapped.** The dynatui catalog (tui branch)
  encodes exactly this domain knowledge — curated, live-validated DQL per noun,
  entity→filter-field mappings per data source, dual-era coalescing, discovery
  queries — but as Go code inside a TUI, invisible to agents and to dtctl.

## 2. Core idea: verified per-environment recipes

**Recipes** is the feature: dtctl knows which queries are *true of the
specific environment* (tenant) behind the current context — and what that
environment is. The persisted artifact is the **recipe book**
(`kind: RecipeBook`, pairing with `kind: RecipePack`), a per-environment
YAML that dtctl loads per context. It has three kinds of content:

1. **Facts** — what this environment *is*: which data objects (fetch
   targets) exist, entity-type census, per-field carriage, buckets, plus
   free-form notes worth knowing. An agent reads this once instead of
   discovering it by failed queries.
2. **Recipes** — named, parameterized, *verified-against-this-environment*
   DQL queries with descriptions, typed params, and follow-up links.
3. **Scoping rules** — how to reference an entity of type X in data source Y
   (the `SignalFilter` knowledge from dynatui), exposed as data plus a
   `resolve scope` command rather than trial and error.

The recipe book is **generated, not hand-written** (hand-written *local
recipes* are the phase-2 on-ramp, and declared facts and org packs are
supported), and it is **a cache of discovery with
provenance, not a hardcode** — which keeps it compatible with dynatui's
ADR-0010 ("curated defaults, runtime discovery"): the recipe book *is* the
runtime discovery, persisted and validated. In its referencing form (§2.3) it
is best understood as a **lockfile**: a pack pointer, pinned versions,
resolution results, per-environment deviations — regenerable, diffable, and
not meant for hand-editing.

Two properties do most of the safety work:

- **Verification is local.** A broken or stale pack recipe degrades into a
  `disabled:` entry with evidence on *this* environment, not into a wrong
  answer. Pack quality and user outcomes are decoupled.
- **Stamps are a living cache** (§4.0). Every execution of a recipe refreshes
  its stamp, so staleness self-heals for everything actually in use.

### 2.1 Two artifacts

The earlier draft had three layers (base pack / org layer / tenant layer).
The org layer collapsed into "just another pack" — its scoping half belongs in
Dynatrace Segments anyway (§5), and its declarations half is a small `declared`
facts section. What remains is two artifacts:

```text
┌──────────────────────────────────────────────────────────┐
│ Packs (curated, versioned, cross-environment — plural)   │
│   capability definitions (name -> how to discover it)    │
│   + recipe templates + guards (requires / dataObjects /  │
│   minCarriage) + verify specs (probe / expect)           │
│   base pack: mined from the dynatui catalog +            │
│   learnings.md, convertible from dynatrace-for-ai;       │
│   org packs: your recipes, added by explicit import      │
│   — org-level SCOPING lives in Dynatrace Segments;       │
│     packs reference segment UIDs, never copy filters     │
├──────────────────────────────────────────────────────────┤
│ Recipe book (generated + continuously refreshed)         │
│   facts (discovered) + declared (org/human, own section),│
│   scoping with coverage, per-recipe stamps (lockfile),   │
│   overrides, disabled recipes with classified evidence   │
│   ~/.config/dtctl/recipes/<context>.yaml                 │
└──────────────────────────────────────────────────────────┘
```

The generator takes the installed packs + environment discovery and
emits/refreshes the recipe book. Precedence on lookup: book override >
later pack > earlier pack — the same more-specific-wins, per-field direction
as `EffectiveSpillConfig`'s global→context merge (two fixed layers there; an
ordered list here).

### 2.2 Schema sketch

```yaml
apiVersion: dtctl.dev/v1alpha1
kind: RecipeBook
metadata:
  name: acme-prod
  context: prod                      # optional binding to a dtctl context
  generatedAt: 2026-07-17T09:00:00Z
  generator: dtctl recipes discover v0.1
  packs:                             # ordered, lowest precedence first
    - { name: dynatrace-recipes, version: "2026.07" }
    - { name: acme-recipes, version: "3" }    # org knowledge = another pack

facts:
  # Everything inside `facts` is DISCOVERED by probes — uniform provenance.
  # `capabilities`/`absent` are EVALUATED from the packs' capability
  # DEFINITIONS (see the pack side below): every capability name is defined
  # by *how it is discovered* — there is no free-floating enum. (The earlier
  # draft defined the vocabulary circularly as "the union of `requires:`
  # across installed packs" with no truth condition, which produced
  # contradictory readings in the committed transcripts; §10.4.) A defined
  # capability is a fact about the environment even when no installed recipe
  # requires it.
  capabilities: [k8s, k8s-metrics, spans, logs, davis, security]
  absent: [rum, aws-cloudwatch, synthetic]
  entityTypes: { K8S_POD: 1234, SERVICE: 210, HOST: 42 }   # census
  dataObjects: [logs, spans, dt.davis.problems]  # fetch targets that exist —
                                     # guard source; NOT inferable from the
                                     # census (verified: a tenant with
                                     # synthetic events lacked the classic
                                     # entity tables)
  buckets: [default_logs_events, custom_audit]
  # Era/field carriage is measured PER FIELD PAIR, not tenant-wide: live tenants
  # dual-write some pairs (request.is_failed AND transaction.is_failed both
  # 100%) while being era-split on others (http.method 13% / http.request.method
  # 87%). A single "semconvEras: [otel]" fact is too coarse — recipes consult
  # the pair they actually filter on.
  fieldCarriage:
    spans:
      http.request.method: 0.87      # otel era
      http.method: 0.13              # oneagent era — filter with typed OR
      request.is_failed: 1.0         # dual-written: either works
      transaction.is_failed: 1.0
      dt.smartscape.service: 1.0     # spans reliably carry service IDs
      dt.smartscape.host: 0.84       # partial! host-scoped span queries lose 16%
    logs:
      k8s.pod.name: 0.58
      dt.smartscape.service: 0.20    # partial — see scoping.SERVICE.logs
      dt.smartscape.k8s_pod: 0.0     # never carried (verified on 2 tenants)
  segments:                          # discovered via `dtctl get segments` — §5
    - { uid: gAAAAAAAAAA, name: Log bucket, variables: [bucket] }
    - { uid: hBBBBBBBBBB, name: Host group, variables: [hostgroup] }
  notes:
    - "Payment services log to bucket custom_audit, not default"
    - "entity tags empty (fieldsSummary) — this org does not tag"
    # scale/tagging observations are notes; the former `volumes:` and
    # `tagging:` keys were cut (§10.4) — no consumer ever read them

declared:                            # human/org-declared semantics — its OWN
  ownership: "k8s.namespace.name conventions"   # top-level section, never
                                     # mixed into discovered `facts`:
                                     # provenance stays visible per section,
                                     # and a hand-edited claim can never
                                     # masquerade as a probe result

scoping:                             # entity type -> per-signal filter idiom
  SERVICE:                           # (data; consumed by `resolve scope` and
    spans:                           #  by agents directly — see §3).
                                     # coverage values are DERIVED from
                                     # facts.fieldCarriage at generation —
                                     # one measurement, two views, never
                                     # maintained independently
      filter: 'dt.smartscape.service == toSmartscapeId("{{.id}}")'
      coverage: 1.0                  # verified: 100% of spans carry it
    logs:
      hop: runs_on                   # topology widening — a NAMED STRATEGY
                                     # resolved in code by `resolve scope`,
                                     # not a magic sentinel in the filter slot
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

recipes:
  pods-restarting:
    description: Pods with container restarts, worst first
    source: pack:pods-restarting@2026.07
    params:
      namespace: { default: "", description: "exact k8s namespace, empty = all" }
      timeframe: { type: duration, default: "now()-2h" }
    dql: |
      smartscapeNodes "K8S_POD", from:{{.timeframe}}
      {{- if .namespace }}
      | filter k8s.namespace.name == {{.namespace | dqlString}}
      {{- end }}
      | parse k8s.object, "JSON:obj"
      ...
    lastRun:                         # the stamp — refreshed on EVERY
      at: 2026-07-17T09:00:12Z       # execution (§4.0); named for what it
      records: 37                    # records (a last-run cache), not for a
      seconds: 0.8                   # verification claim it can't make
      params: default                # or a short hash of the rendered
                                     # non-default param set — drift and
                                     # "was N at T" reasoning apply only to
                                     # default-param stamps (§4.0). The pack
                                     # pin is `source:` above; retained pack
                                     # versions are immutable (§4.2), so no
                                     # separate content hash is needed
                                     # (`recipeSha` cut, §10.4)
    followups: [pod-logs, pod-events]

  slow-payment-requests:             # org-pack recipe, environment-specific
    description: p95 latency of payment services
    source: pack:slow-payment-requests@3
    ...

disabled:
  rum-slowest-pages:
    class: guard                     # structural evidence (dataObjects /
    at: 2026-07-17T09:00:31Z         # minCarriage / hard query error) —
    reason: "user.events not in dt.system.data_objects"
                                     # durable; re-evaluated by `refresh`
  attacks-recent:
    class: probe-empty               # absence-of-events evidence — WEAK by
    at: 2026-07-17T09:00:33Z         # §10.1 lesson 2: a quiet window is
    reason: "0 DETECTION_FINDING events in 24h"
                                     # indistinguishable from a missing
                                     # capability. Re-probed on every
                                     # refresh; `query --recipe` executes it
                                     # anyway (with a warning) and a
                                     # non-empty result resurrects it —
                                     # never a one-way door (§4.0)
```

And the pack side (guards + verify — the whole vocabulary):

```yaml
# In a RecipePack file: capability definitions, then recipes with three
# fixed guard shapes + a two-field verify spec.

capabilities:
  # Every capability name is DEFINED by how the generator discovers it —
  # fixed definition shapes, mirroring the guards. Structural evidence
  # (dataObject / entityTypes / metricKey) is preferred; `probe:` (with a
  # mandatory `window:`) is the escape hatch for capabilities only visible
  # as events, and is WEAK evidence by §10.1 lesson 2 — probe-defined
  # capabilities are re-evaluated on every `refresh`, like probe-empty
  # disables (§4.0).
  spans:        { dataObject: spans }
  rum:          { dataObject: user.events }
  hosts:        { entityTypes: [HOST] }
  aws:          { entityTypes: ["AWS_*"] }
  k8s-metrics:  { metricKey: "dt.kubernetes.*" }
  security-rap:
    probe: 'fetch security.events, from:now()-24h | filter event.type ==
            "DETECTION_FINDING" and detection.source == "RAP" | limit 1'
    window: 24h                      # probes carry their from: explicitly
                                     # (self-executable as written); window:
                                     # is the metadata refresh/widen logic
                                     # reads

recipes:
  service-errors:
    description: Failed-request count and rate per service
    requires: [spans]                # guard 1: capabilities — every name
                                     #   MUST have a definition in some
                                     #   installed pack (lint + fail-closed)
    dataObjects: [spans]             # guard 2: fetch targets must exist in
                                     #   dt.system.data_objects
    # guard 3 (rare): minimum field carriage, as a structured object —
    # minCarriage: { table: spans, field: request.is_failed, ratio: 0.5 }
    verify:
      probe: 'fetch spans, from:now()-30m, samplingRatio:100 | limit 1'
      # threshold-free existence proof, used to classify empty results
      expect: records                # or `values` for single-row aggregates
    params:
      timeframe: { type: duration, default: "now()-30m" }
      sampling: { type: int, default: "10" }
    dql: |
      # portable form — no variants; see schema notes
      ...
```

Notes on the schema:

- **Params are typed and validated before render.** Recipes render through
  `pkg/util/template` (`{{.var}}`, `default`) and the existing `--set` flag —
  no new templating system. The shipped dtctl skill already teaches
  `query -f q.dql --set host=...`; recipes formalize that practice with names,
  descriptions, and stamps. Types: `string` (the default — rendered
  exclusively through **`dqlString`**, a *new* template func (the engine
  ships only `default` today) that emits a
  properly escaped, quoted DQL string literal: `== {{.ns | dqlString}}`,
  never `== "{{.ns}}"`), `int`, `duration` (timeframe expressions), `enum`
  (valid values from `values:` below), `identifier` (field/bucket names), and
  `dql-filter` (a raw expression *by declared contract*, e.g. one produced by
  `resolve scope`). Non-string values are validated against their type before
  rendering — `--set 'limit=100 | fieldsRemove content'` is rejected, not
  interpolated. Two lint rules close the remaining holes: a pack lint refuses
  bare `{{.param}}` interpolation of `string` params outside `dqlString`,
  and `--set` with a key no recipe param (or bound segment variable)
  declares is an **error** — a typo'd key (`--set namepace=…`) must never
  silently render the unfiltered `{{if .namespace}}` branch and return
  plausible, wrong results. (The earlier draft claimed params were "escaped
  by default"; they were not — `timeframe`/`limit`/`sampling` interpolated
  raw in every example. The type system is the honest fix, §10.4.)
- **Portable-first DQL — there is no `variants:` mechanism.** The earlier
  draft selected per-environment recipe variants from measured carriage. The
  five-tenant simulation (§10.1) showed that every observed divergence has a
  portable form instead: era-split fields → **typed OR** in filters
  (`http.request.method == "GET" or http.method == "GET"` — also the
  index-safe form; function-wrapped fields in filters can mute Grail indexes)
  and `coalesce()` only in projections/aggregations; platform-flavored entity
  types → **multi-type/wildcard unions** (`smartscapeNodes
  "DB_INSTANCE_POSTGRES", "AZURE_MICROSOFT_DBFORPOSTGRESQL_*"`); optional
  enrichment fields that *hard-error* when absent (inner-map access) → **split
  into a followup recipe** that gets disabled where the field is missing;
  table existence → the `dataObjects` guard. Of 44 recipes × 5 tenants, only
  3 needed any guard beyond `requires:`, and none needed body *selection*
  once written portably. Variants are readmitted only if a real case
  survives portable-first.
- **Capabilities are defined by their discovery, not asserted.** A pack's
  `capabilities:` section maps every name to a fixed definition shape:
  `dataObject:` (table exists), `entityTypes:` (census match, wildcards
  allowed), `metricKey:` (metric-catalog match), or `probe:` + mandatory
  `window:` (for capabilities only visible as events). `discover`/`refresh`
  evaluate the definitions and write the results to
  `facts.capabilities`/`absent` — the recipe book's capability list is
  environment-true and reproducible, and meaningful even for capabilities
  nothing currently requires (a tenant can be `azure`-capable with no azure
  recipe installed). The earlier draft had no definitions — the vocabulary
  was "the union of `requires:` across packs", which was circular and
  produced contradictory readings in the committed transcripts (§10.4).
  Definitions merge across packs with the same precedence as recipes;
  `requires:` naming a capability no installed pack defines is a pack lint
  error, and at evaluation time an undefined capability fails closed (the
  recipe lands in `disabled`, same rule as unknown keywords). Probe-defined
  capabilities carry the §10.1-lesson-2 weakness — absence of events is not
  absence of capability — so their negative results are re-evaluated on
  every `refresh`, and structural shapes are preferred wherever one exists.
- **Guards are three fixed shapes, not expressions.** `requires` (capability
  names, each backed by a definition as above), `dataObjects` (table
  existence), `minCarriage` (structured table/field/ratio object — e.g. a
  field carried on 0.003% of spans makes a recipe misleading, not weak).
  Deliberately no expression language; anything richer is code in dtctl.
- **Verify is two fields, not five kinds.** The earlier taxonomy
  (threshold/transient/aggregate-values/carriage/capability) collapsed:
  *threshold* and *transient* are one class — "empty may be legitimate; prove
  the capability with a threshold-free `probe:`, then empty means 'no
  offenders right now'". *Carriage* and *capability* are one class — inputs
  must exist, which is what guards do. What remains per recipe: an optional
  `probe:` and `expect: records|values`. Both are specified, not prose:
  `expect: records` — the probe verifies iff it returns ≥1 record;
  `expect: values` — a single-row aggregate verifies iff at least one
  aggregated value is non-zero (a 1-row summary of zeros is not verified —
  a real false-verified caught in §10.1), and the outcome is recorded in the
  stamp (`valuesNonZero: true`) so value-classification leaves a trace.
  Entity-scoped recipes declare a structured coverage probe instead —
  `coverage: { per: SERVICE, signal: logs, window: 24h }` — whose result is
  a fraction ("logs exist for 31 of 144 services"), not a boolean.
  (`probeVia: <recipe>` was cut, §10.4 — probe reuse is pack-authoring
  dedup, not schema.) `floor:` is gone too: **widen-on-empty is universal
  generator behavior** (probe again at 24h; record the widened default as an
  `override:` in the recipe book) rather than a schema keyword.
- **Stamps are last-run records, refreshed on every execution** (§4.0), and
  the key is named `lastRun:` for exactly that reason — the earlier
  `verified:` overpromised (a last-run cache is not a verification claim,
  and "verify" is already a dtctl verb with a different meaning, §3). Stamp
  fields: `at`, `records`, `seconds`, `params` (default-or-hash provenance,
  §4.0), plus `limitHit: true` when `records` equals a limit (a maxed count
  is weak signal — record it distinctly), `warning:`/`partial:` for
  scan-limit truncation, and `empty: legitimate` with a `note:`.
- **Classification is first-class.** Because Grail fails silently, a recipe
  without a stamp is a guess. The generator probes each candidate and stamps
  it; recipes that fail guards or return misleading results are moved to
  `disabled` with the evidence as the reason — *classified* evidence
  (`class: guard` vs `class: probe-empty`, §4.0) plus a timestamp — instead
  of silently shipping. Disabled-with-evidence is negative knowledge — often
  the most token-valuable content in the recipe book ("don't try RUM
  here"), which is also why a wrong disable is the most damaging wrong
  output the feature can produce and why disables are re-probed, never
  permanent (§4.0).
- **Recipes without `source:` are local.** A recipe defined directly in the
  recipe book — hand-written or distilled from usage (§7) — carries no
  pack pointer; the referencing/lockfile form applies only to pack-backed
  recipes. Local recipes are the phase-2 on-ramp (§8) and are exempt from
  regeneration: `discover`/`refresh` re-stamps them but never rewrites their
  bodies.
- **Schema evolution is fail-closed for guards (v1alpha1).** A pack recipe
  carrying a guard or verify keyword this dtctl version does not know is
  **not instantiated** — it lands in `disabled` with
  `reason: "unknown keyword <x> — pack requires newer dtctl"` — never
  silently instantiated without the guard (fail-open would un-guard exactly
  the recipes most likely to mislead). Unknown keys elsewhere in an
  recipe book are preserved on rewrite and ignored on read, so an older
  dtctl doesn't destroy a newer generator's output. Breaking changes bump
  `apiVersion` with one-version read compatibility.
- **Cut this round (§10.4), with the usual readmission bar:** `volumes:` and
  `tagging:` (no consumer ever read them — scale and tagging observations
  are notes), `probeVia:`, `recipeSha` (redundant once retained pack
  versions are immutable, §4.2).
- **Display names via `getNodeName()`, not raw fields.** Verified on live
  data: grouping by `service.name` yields null for whole service populations
  (OneAgent-fed spans). `getNodeName(dt.smartscape.service)` resolves the
  Smartscape display name regardless of era — recipes prefer it over
  coalescing raw name fields.
- **Verification records coverage, not just a boolean.** The busiest service
  on a verified tenant emits *zero* log lines — a correct scoping rule and an
  empty result coexist legitimately. So entity-scoped recipes store a coverage
  map (e.g. "logs exist for 31 of 144 services") rather than one non-empty
  probe, and the envelope can tell the agent which case it hit.
- **Params can carry a `values:` DQL** (idea borrowed from Segment variables,
  §5): an optional query that yields the parameter's valid values on this
  environment — also the value domain for `type: enum` params. Agents get
  discoverable, environment-valid arguments; humans get shell completion.
  (Note `values:` queries execute against the live environment — completion
  must cache and cap them.) On recipes that declare `segments:`, `--set`
  also binds segment variables of the same name, so users and agents see
  **one** parameter surface (§5). One surface needs unambiguous names: a
  template param and a segment variable sharing a name is a pack lint error
  — the recipe must rename its param.
- **Followups are pack-authored knowledge with specified semantics**
  (completion pass, §10.5). `followups:` is the serialized dynatui drill
  graph (`Spec.Drills`/`EnterTarget`: selected row's entity → its
  logs/traces/problems views) — cross-environment content that belongs in
  the pack, not generator output; `discover` only validates it. Names
  resolve against the book's **merged recipe namespace** (all installed
  packs + local recipes): an edge naming a recipe no installed pack defines
  is dropped from the book at generation with a note (pack lint warns —
  cross-pack edges are legitimate, so not an error), and an edge whose
  target sits in `disabled:` is **not emitted as an envelope suggestion**
  (suggesting a known-dead recipe steers agents into exactly the
  empty-result loop recipes exist to prevent) while staying in the pack for
  environments where the target is alive. Edges carry **no param binding**
  by design: dynatui's drills bind the selected row's entity in code; the
  CLI equivalent is the agent reading the target's typed params (plus
  `resolve scope` for scoping ones) — a `bind:` mapping is a schema keyword
  that must earn its place under the governor's default-no. The
  dynatrace-for-ai skills carry no comparable model (their routing is prose
  "Related Skills" sections), so the drill graph is the pack's genuinely
  additive channel over a skills conversion.

### 2.3 Example files, size, and complexity

Worked examples live next to this doc — all recipe DQL in them was executed
against live tenants first (bodies revised by the 2026-07-18 portable-first
pass are marked `revised:`, completion-pass additions `added:` — both need
(re-)verification):

- [examples/recipes/recipebook.example.yaml](examples/recipes/recipebook.example.yaml)
  — the *materialized* form for a fictional tenant: facts (incl. carriage
  matrix and discovered segments), scoping with coverage, fully-templated
  recipes with typed params, a local (`source:`-less) recipe, and classified
  disabled entries. Its `source:` pointers reference a fictional superset
  pack — only some resolve against the committed example packs (marked in
  the file).
- [examples/recipes/base-pack.example.yaml](examples/recipes/base-pack.example.yaml)
  — pack mechanisms: the two guard shapes, the `probe`/`expect` verify
  spec, typed params, and portable-first DQL in place of the former
  variants.
- [examples/recipes/base-pack.full.example.yaml](examples/recipes/base-pack.full.example.yaml)
  — the **full pack, 52 recipes**: 44 mined from the dynatui catalog (15)
  and the dynatrace-for-ai skills (29), every DQL body executed on live
  tenants (§10.1); plus the nodes-inventory/nodes-labels split and seven
  completion-pass entries (`added:` — the entity/trace drill targets the
  followup graph needs, and azure/gcp census coverage). Carries the
  61-edge `followups:` drill graph. `revised:`/`added:` entries need
  (re-)verification.
- [examples/recipes/recipebook.dev.generated.yaml](examples/recipes/recipebook.dev.generated.yaml)
  / [recipebook.demo.generated.yaml](examples/recipes/recipebook.demo.generated.yaml)
  / [recipebook.netobs.generated.yaml](examples/recipes/recipebook.netobs.generated.yaml)
  / [recipebook.large.generated.yaml](examples/recipes/recipebook.large.generated.yaml)
  / [recipebook.playground.generated.yaml](examples/recipes/recipebook.playground.generated.yaml)
  — **hand-assembled transcripts of the manual §10.1 verification run**
  (no generator exists yet — the earlier "actual generation outputs" label
  overstated), in the *referencing* (lockfile) form the generator would
  emit: the same pack survives as 38, 44, 41, 42, and 42 usable recipes
  with per-environment disabled lists, widened windows, and scan-limit
  partials. Each file records only the facts that run measured, which is
  why their facts sections differ — the generator's golden tests (§4.1)
  are what will enforce one schema. Filenames use the §10.1 neutral tenant
  names and distinctive entity counts are rounded, per the privacy rule
  (§3).

**How large do recipe books grow?** Measured, not estimated — the full
52-recipe pack is ~830 lines (~12 lines/recipe in compact form; capability
definitions, provenance comments, and the followup graph account for the
rest; a fully-templated recipe with params runs ~25 lines), and the
*generated* recipe books in referencing form are **~175–195 lines each**
regardless of pack size:

| Part | Size (measured) |
|---|---|
| 52-recipe pack (compact, incl. capability definitions + followup graph) | ~830 lines |
| fully-templated recipe (materialized) | ~25 lines each |
| generated recipe book, referencing form | ~175–195 lines (facts + stamps + overrides + disabled) |
| projected 80-recipe pack, fully templated | ~2,000–2,500 lines, 50–70 KB |

The referencing form is the important discovery: the recipe book does not
need to duplicate pack DQL — a pack pointer plus a verification stamp (and an
`override:` only where the environment diverges, e.g. a widened timeframe)
keeps the per-environment artifact small, diffable, and regenerable. It is a
lockfile.

That is fine on disk and hopeless as agent context — which is why the CLI
surface is progressive: `dtctl recipes` returns the environment briefing —
facts plus a one-line-per-recipe index (~2–4k tokens for 60 recipes) — and
only `describe recipe` / execution touch a full recipe. No consumer ever
loads the whole book.

**Complexity governors** (what keeps this from becoming a DSL):

- A recipe's DQL body stays ≤ ~20 lines; template logic is limited to
  presence-of-param conditionals (`{{if .namespace}}`). If a recipe needs
  more, the logic moves into a template func (code) or splits into a
  followup recipe.
- No joins/multi-phase flows in recipes (dynatui's two-phase metric probe
  stays code — and the render-time `runs_on` hop was moved out of recipes for
  the same reason, see §3 `resolve scope`). Correlation is composed by the
  agent via `followups`, mirroring dynatui ADR-0013's "no cross-signal joins
  in curated views".
- Pack guards are **three fixed structured shapes** compared against
  discovered facts — deliberately not an expression language, and new guard
  shapes require the same evidence bar that removed `variants:`.
- The metadata vocabulary itself is governed: the five-tenant run grew the
  schema by ten ad-hoc semantics before the 2026-07-18 collapse (§10.2).
  New schema keywords need a case that the existing shapes provably cannot
  express — the default answer to "add a keyword" is no.

## 3. CLI surface

`dtctl commands` answers *"what can I run?"*; **`dtctl recipes`** answers
*"what is true here?"* — the same bare-noun catalog grammar, and together
they are the two agent-bootstrap calls. `dtctl recipes` is **one** call:
the environment facts briefing plus a one-line-per-recipe index (a separate
`get recipes` was dropped — same data, one less bootstrap call). A single
recipe is still a resource for the existing verb-noun grammar (`describe
recipe`, `query --recipe`). Counted honestly (the earlier "no new verbs"
claim was false), the new top-level names are: **`recipes`** (the noun
command, with lifecycle subcommands) and **`resolve`** (scoping as a
callable — it can fold in as `recipes scope` if a smaller surface wins).
`run` is *deferred* ergonomic sugar and `pack` lives under `recipes pack` —
both consciously, because dtctl dispatches unknown commands to
`dtctl-<name>` plugins and built-ins always win: a new top-level `run` or
`pack` would silently shadow any existing `dtctl-run` / `dtctl-pack`
plugin.

```bash
# consumption
dtctl recipes [-o json|markdown]      # the bootstrap call: facts + recipe index
dtctl describe recipe pods-restarting # full recipe: DQL, params, stamp, followups
dtctl query --recipe pods-restarting --set namespace=payments
dtctl verify query --recipe pods-restarting --set ns=x  # render + validate, NO execution
dtctl resolve scope SERVICE-abc123 --for logs           # scoping as a callable

# lifecycle (subcommand grammar mirrors `dtctl commands howto`)
dtctl recipes discover                    # prefetch: discovery + pack instantiation + probes
dtctl recipes refresh                     # re-run probes, refresh stamps, report drift
dtctl recipes pack import acme-pack.yaml  # add an org pack (explicit act of trust; v2)
```

- **Why `refresh`, not `verify`:** the CLI already has `dtctl verify`
  ("verify resources **without executing** them", `cmd/verify.go`) — a free,
  offline dry-run. A subcommand that live-executes probes and spends Grail
  budget must not reuse that word with the opposite cost model; an agent
  that learned "verify = free" would misprice it. `refresh` is also the
  honest name under §4.0 (it refreshes a cache). Render-without-execute for
  recipes composes with the *existing* verb instead: `dtctl verify query
  --recipe <name> --set …` renders the template and validates the DQL
  without running it — the `verify query -f … --set …` idiom already works
  today. Similarly `run` vs `exec`: `dtctl exec` owns long-running platform
  executions (workflow, function, …) with polling semantics; a recipe is
  just a query, so `query --recipe` is the primary spelling and `run`, if
  it ever ships, is a mnemonic alias.

- Execution internally = resolve recipe (book override > packs) → render
  template → existing `DQLExecutor`. Everything a recipe needs to pass
  through already exists as `query` flags (`--default-timeframe-start/-end`,
  `--default-sampling-ratio`, `--default-scan-limit-gbytes`,
  `--max-result-records`, `-S`). Requests are tagged via the existing
  `dt-client-context` header (free-form `context` string:
  `recipe:pods-restarting`) for observability.
- **Every execution refreshes the recipe's stamp** (§4.0). A recipe that was
  verified non-empty and starts returning empty flips its stamp — drift
  detection is free and continuous, not a cron job.
- `recipes` / `describe recipe` are statically registered Cobra commands
  that load YAML at runtime, so they appear in `dtctl commands` and the
  agent catalog. Recipes are *not* dynamic top-level commands — that keeps
  the alias resolver and command catalog untouched. (Honest accounting:
  *registration* is automatic; the agent-envelope enrichment
  (`enrichAgent`), golden output tests, and scope tables are manual
  per-command work, and `recipe` is the first resource backed by a local
  file rather than the API — resolver and printer assumptions need explicit
  checking, counted in §8.)
- `dtctl recipes -o markdown` doubles as the briefing renderer: a one-page
  environment map for pasting into CLAUDE.md/AGENTS.md. (Keep the skill
  teaching "call `dtctl recipes` at session start" as the primary path —
  files go stale, calls don't.) Note `markdown` is a **new** output format:
  the shared output system knows json|yaml|csv|toon|table|wide today, so
  this is a small per-command formatter, not free plumbing.
- **`resolve scope`** exposes the scoping table (and the `runs_on` hop, which
  needs a topology pre-query) as a first-class command that prints a filter
  expression for any entity/signal pair. It is useful for hand-written
  queries, keeps recipes single-phase and echoable, and is the v1 home of the
  hop logic — the `{{ scopeFilter }}` template func is deferred (§7).
- In agent mode, the envelope's existing `suggestions` channel carries
  `followups` ("next: dtctl query --recipe pod-logs --set name=...") — turning the
  dynatui drill graph into agent guidance.
- Read-only by design: recipes are DQL only. No mutation verbs in recipe
  books (mirrors dynatui ADR-0011 and keeps them outside the safety-checker
  surface). The one adjacent mutation — depositing discovered conventions as
  Segments (§5) — goes through `dtctl create segment` and therefore through
  the standard safety checker and confirmation.
- **Envelope warnings: mostly already implemented** (correction to the
  earlier draft, which called this an unlanded prerequisite).
  `notificationAdvice()` in `pkg/exec/dql.go` already maps
  WARNING/WARN/ERROR query notifications into `context.warnings` and
  classified `context.suggestions` on both agent-mode paths
  (`pkg/exec/spill_exec.go`); the stderr-only behavior is the human path.
  Remaining phase-0 work: confirm the severity tag on the smartscape-cast
  notification and add it to the advice classifier (currently five
  categories: scan-limit, result-limit, timeout, sampling, consumption).
- **Diagnose-on-filter** (phase 0.5, recipe-book-independent): in agent mode,
  dtctl probes carriage for the fields referenced in a query's filters (one
  sampled, scan-capped `countIf(isNotNull(...))` scan per touched table,
  session-cached) and adds findings to `context.warnings` — *"filter
  references `dt.smartscape.k8s_pod`, carried on 0.0% of logs on this
  environment"*, or, for the non-empty case, *"…carried on 20% — results
  are silently incomplete"*. Zero-record results are the loudest trigger,
  but the earlier zero-only design (diagnose-on-*empty*) missed §1's worst
  failure mode: partial carriage returns non-empty, plausible, silently
  incomplete data that no error channel flags — so the probe fires on
  filtered fields **regardless of record count**. This attacks silent-empty
  *and* silent-partial inside the escape hatch — raw `query` is where
  agents spend most tokens once they leave recipe rails, and real
  investigations always leave the rails after the first hop. It reuses the
  generator's carriage probe and needs no recipe book at all.

### Relationship to command profiles (#351)

Command profiles (merged, `COMMAND_PROFILES_DESIGN.md`) restrict *which
commands dtctl exposes* — a default-deny allowlist bound to a context
(`profile:` field, `DTCTL_PROFILE`). They are a third axis alongside this
concept and safety levels, and the three compose on one context:

| Axis | Question | Feature |
|---|---|---|
| Surface | Which commands exist here? | Command profile (`profile: query`) |
| Permission | What may a command do? | Safety level (`safety-level: readonly`) |
| Knowledge | What is true of this environment? | Recipes (the recipe book) |

An embedded agent product pins all three: `profile: query` +
`safety-level: readonly` + a generated recipe book for the same context.
This is also why the concept was renamed away from "profile" (§10.3) — two
features called "profile" on the same context object, surfaced in the same
agent envelope (`dtctl commands` emits `"profile": "query"`), would be
unsupportable.

Composition consequences:

- **Presets must list the recipe commands.** The always-available floor is
  just `commands` + `help`, so nothing here is reachable in a restricted
  profile by default. When this ships, the `query` and `investigate` presets
  should gain `recipes`, `describe recipe`, **and `resolve`** — the earlier
  list omitted `resolve`, which would have left a `profile: query` agent
  able to run recipes but unable to obtain scoping filters, exactly where
  off-rails tokens go.
- **`query --recipe` rides the `query` allowlist entry for free.** Recipe
  execution via a flag on `query` is automatically inside every profile that
  allows `query`; a new top-level `run` verb would need explicit preset
  entries. One more reason `run` stays optional sugar — locked-down products
  can simply not list it.
- **Masking is orthogonal to knowledge.** A profile hiding `recipes` does
  not invalidate the recipe book; it hides the door. The generator
  (`recipes discover`) is a lifecycle command a restricted agent profile
  would typically *not* include — the embedding product generates the
  recipe book out-of-band, the agent only reads it.

### Trust model

The real precedent in the code is stricter than the earlier draft claimed:
exec-capable keys (aliases, hooks) from a discovered repo-local `.dtctl.yaml`
are **unconditionally ignored** with a one-line warning
(config loading owned by `sdk/session` — `pkg/config` is its shim — and
`cmd/alias_resolve.go`) — there is no content-hash
acknowledgement mechanism in the codebase, and this concept no longer assumes
one. #352 sharpened the same line from the other side: an **explicitly
named** config (`--config` or `DTCTL_CONFIG`) *is* trusted (`cmd/root.go` —
"honored only from the global config, --config, or DTCTL_CONFIG"). The rule
is: discovered = refused, explicitly named = trusted. Recipe books and
packs follow it:

- Artifacts in the global config dir (`~/.config/dtctl/recipes/`,
  `~/.config/dtctl/packs/`) are trusted.
- A repo-local recipe book or pack is **never loaded implicitly**. Teams
  share packs through `dtctl recipes pack import`, which is the explicit
  act of trust (and prompts, showing what is being imported) — the same
  explicit-vs-discovered line #352 draws for configs.
- **Stamps are local claims** — "my generator probed my environment". Import
  and export strip `lastRun:` stamps; imported recipes are unstamped until
  `recipes refresh` runs locally. A stamp is never portable trust — where
  the boundary is *how the file arrived*, not where it sits: a product that
  provisions an recipe book into the config dir (the embedded scenario
  above) has performed the explicit act, and its stamps are trusted like any
  local artifact; stripping applies to import/export, which cross machines
  and owners.
- **Lifecycle follows the context.** `dtctl ctx delete` offers to remove the
  context's recipe book — otherwise a stale book silently outlives the
  context it described.
- **Recipe-book content is untrusted input to agents.** Descriptions, notes,
  and followups are prose that agents read and act on; an imported pack is
  executable DQL plus agent-steering text. DQL is read-only, but a malicious
  pack could still steer an agent into querying sensitive data and leaking it
  via later tool calls. Import is the trust boundary; the import prompt says
  so.

### Privacy of generated recipe books

Generated recipe books embed environment facts: bucket names, segment
UIDs, entity census, volume figures, prose notes ("payment services log to
the audit bucket"). They belong in the config dir, not in repos. Guidance
that ships with the feature: the skill and docs warn against committing
generated recipe books; `recipes export --redacted` strips identifying
facts (segments, buckets, notes, exact entity counts) for the cases where a
team wants a reviewable artifact in a repo. The rule covers **filenames**,
not just contents — a file named after an environment ID defeats every
in-file anonymization. This mirrors the project's own privacy rule for its
codebase and issue tracker, and this round applied it to the concept's own
example transcripts: three files whose names were prefixes of real
environment IDs were renamed to neutral tenant names and their distinctive
entity counts rounded (§10.4) — the examples are the first `--redacted`
consumers in spirit.

## 4. The generator and the living cache

### 4.0 Stamps are a cache that every execution refreshes

The single biggest design risk of a "generated artifact" is staleness: stamps
are strongest the moment they are written and decay from there, and a stale
"verified" is *worse* than no claim — it converts healthy agent skepticism
into misplaced confidence. The answer is architectural, not policy:

- **Every recipe execution is a probe.** `query --recipe` rewrites the
  recipe's stamp (`at`, `records`, `seconds`, `limitHit`, warnings) from the
  live outcome. Stamps of recipes in actual use are always fresh; drift
  surfaces the moment it happens, in the envelope.
- `recipes discover` is the **prefetch** that warms the cache (and the only
  step that computes facts/carriage and evaluates guards). `recipes
  refresh` re-runs it. Neither is the sole writer.
- Only `facts` need a refresh trigger (they have no organic refresh); the
  discovery battery is ~15 cheap queries, so refreshing facts older than
  N days is affordable — but it must never stall session bootstrap:
  `dtctl recipes` always answers immediately from the file, surfacing
  `factsAge` in the output, and refreshes stale facts *after* answering (or
  on explicit `refresh`). Staleness is priced, not blocking. This replaces
  the earlier TTL/cron open question.

**The write path is specified, not assumed.** Every stamp refresh is a
read-modify-write of a shared file, and parallel agent sessions on one
machine are a normal working pattern, so:

- Writes are **atomic**: render to a temp file in the same directory, then
  rename over the original — never in-place truncation. (The config writer's
  plain `os.WriteFile` is not the precedent to copy here.)
- A **best-effort advisory lock** (`flock`; precedent: the OAuth refresh
  lock in `sdk/session/refresh_lock_unix.go`) serializes concurrent
  writers; on contention the slower writer re-reads and re-applies its one
  stamp before renaming. Stamps are per-recipe keys, so re-read-then-merge
  loses nothing.
- Stamp writes are **best-effort by contract**: a failed write (read-only
  dir, lock timeout) degrades to a warning in the envelope — it never fails
  the query that produced the result.
- **Read-only deployments are first-class, not accidental.** With the file
  unwritable (or read-only mode set explicitly), dtctl refreshes nothing
  and instead surfaces stamp age in the envelope (`stampAge: 21d`), so the
  agent prices staleness rather than trusting it. This is the honest answer
  for the embedded scenario (§3), where the product generates the recipe
  book out-of-band and the restricted agent only reads: the living cache cannot
  self-heal there, and the design stops pretending it does — the product
  re-generates on its own schedule, the envelope reports the age.

**`disabled:` is never a one-way door.** The living cache only refreshes
recipes that execute, and disabled recipes are excluded from execution —
left alone, a wrong disable would persist forever, and negative knowledge is
exactly the content agents trust most, so a wrong disable is the most
damaging wrong output the feature can produce. Three rules prevent it:

- Disable evidence is **classified**. `class: guard` — structural facts (a
  missing data object, carriage below threshold, a hard query error):
  durable, re-evaluated only by `refresh`/`discover`. `class: probe-empty` —
  absence of events in a window: *weak* evidence by the concept's own §10.1
  lesson 2 (a quiet weekend is indistinguishable from a missing capability).
- `probe-empty` disables are **re-probed on every `refresh`** (the probes
  are the cheap, threshold-free kind), and capability-level conclusions
  ("X not active") require structural evidence — event absence alone only
  ever justifies "none observed in this window".
- A disabled recipe is still **executable**: `query --recipe` runs it with
  the disable evidence as a warning, and a non-empty result resurrects it on
  the spot. Every disabled entry carries `at:` so its evidence can age.

**Stamps record their conditions.** Refresh-on-every-execution would
otherwise poison the cache: a hand-picked 30-day timeframe stamps a record
count the defaults never reproduce, and drift detection would flip on
parameter choice rather than environment change. So the stamp carries
`params: default` or a short hash of the rendered non-default param set, and
drift comparison, widen-on-empty reasoning, and the envelope's "was N at T"
line consider **only default-param stamps**; a custom-param run still
refreshes `at`/`seconds` (cost is cost) but never overwrites the
default-param baseline.

### 4.1 What it runs (deterministic discovery)

All of these exist, live-validated, in dynatui today:

| Discovery | Query (from dynatui) | Feeds |
|---|---|---|
| Entity census | `smartscapeNodes "*" \| summarize count(), by:{type}` | `facts.entityTypes`, capability guards |
| Topology schema | `smartscapeEdges "*" \| summarize by:{source_type,type,target_type}` | scoping (hops), followups |
| Data objects & buckets | `fetch dt.system.data_objects` / `dt.system.buckets` | `facts.dataObjects`, `facts.buckets`, guards |
| Field docs | `fetch dt.semantic_dictionary.fields` | field validation, era detection |
| Metric availability | `metrics \| summarize by:{metric.key}` + two-phase probe | which sparkline/metric recipes survive, `metricKey:` capability definitions |
| Capability evaluation | the packs' capability definitions (§2.2), structural shapes first, `probe:` shapes last | `facts.capabilities`/`absent`, `requires` guards |
| Era/carriage matrix | one `summarize countIf(isNotNull(f))` scan per table over the packs' field list | `facts.fieldCarriage`, `minCarriage` guards |
| Tag/value discovery | `fieldsSummary` on entity `tags`, `k8s.namespace.name`, span attrs | `facts.notes` (must be able to conclude "this org does not tag") |

Then, per recipe: evaluate guards (`requires` / `dataObjects` /
`minCarriage`) → render → run `verify.probe` if declared → execute the body →
interpret per `expect:` → stamp, **widen-on-empty** (retry at 24h; record the
widened default as an `override:`), or move to `disabled` with the evidence.

**Probe protocol and budget (mandatory, not advisory).** The 2026-07-17
simulation itself tripped Grail's 500 GB scan limit with an unsampled 24h
bizevents probe on the large tenant, and the metrics catalog cost 34s there —
generation is a real consumption event, and a scheduled `verify` multiplies
it. Therefore:

- probes run **cheapest-first**: data-object existence → capability probes →
  full recipe bodies; carriage probes batch many `countIf(isNotNull(...))`
  into a single scan per table;
- every probe carries a scan cap (the existing
  `--default-scan-limit-gbytes` plumbing) and sampling defaults
  (`samplingRatio:` verified working on logs and spans, fine for
  presence/ratio facts);
- a **global budget** aborts discovery with a partial recipe book rather
  than overrunning (dynatui ADR-0013 spirit);
- discovery ends with a **consumption receipt**: queries run, wall time,
  scanned GB (the envelope already carries `scannedBytes`);
- cost lands **in the stamp** (`seconds`, and scan warnings as `partial:`) so
  agents can prefer the cheap recipe — load-bearing at enterprise scale,
  where the same catalog query cost 2.4s on a small tenant and 34s on a
  large one;
- discovery needs read scopes only; document expected Grail consumption.

Tag discovery must probe multiple channels — entity `tags` via
`fieldsSummary` (verified empty on a real tenant), k8s labels, span
attributes — and be able to conclude "this org does not tag" (a note)
rather than inventing a convention.

**Testing strategy — the generator is code, not a ritual.** Every
load-bearing generator behavior — capability evaluation, guard evaluation,
widen-on-empty, `expect: values` classification, disable classes, budget
abort, `partial:`/`limitHit` stamping — runs against a mock Grail server in
unit tests (the same discipline AGENTS.md mandates for paginated mock
servers), and the emitted recipe book is covered by **golden files**: the
five §10.1 tenants become recorded probe fixtures, and `discover` against
fixture N must reproduce the committed transcript byte-for-byte. Until that
exists, the generator's behaviors are validated only by one unrepeatable
manual run — which is exactly how the five committed transcripts silently
drifted apart in schema in the first place (§2.3).

### 4.2 Where does it live?

Recommendation: **generator in dtctl (`dtctl recipes discover`), knowledge
in packs, shared logic in the sdk** — not in dynatui.

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
  followups, minus rendering closures) becomes the pack. Note the honest
  cost: the pack is then the third copy of Dynatrace query knowledge (skills,
  dynatui catalog, pack) — §9 carries the ownership question. Local
  verification is the safety net: a rotten pack degrades to disabled entries
  with evidence, never to wrong answers.
- **A lockfile needs a store.** Whatever distributes packs, dtctl retains
  every pack version it has ever instantiated, immutably, under
  `~/.config/dtctl/packs/<name>/<version>/` — a recipe book full of
  `source: pack:name@version` pointers is only as durable as the versions
  it points at, and a routine dtctl upgrade replacing a bundled pack must
  not dangle every pointer on the machine. Because retained versions are
  immutable, `source:` is a sufficient pin and stamps need no separate
  content hash (`recipeSha` was cut on these grounds, §10.4). Unreferenced
  retained versions are garbage-collected when no recipe book points at
  them.

### 4.3 Agent-assisted annotation (optional second phase)

Some environment knowledge is not deterministically discoverable: "owner
lives in `tags.owner`", "prod is the `prod-*` namespaces", "payment services
log to the audit bucket". A guided profiling session — an agent (via the
dtctl skill) interviews the environment with `fieldsSummary`/census recipes
and the human with 2-3 questions, then writes `facts.declared` and, where the
convention is scoping, deposits a Segment (§5) — fits naturally on top:
deterministic generator for facts/verification, agent pass for semantics.
The recipe book schema doesn't change; only who writes the declared facts.

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
segments beat pack YAML on every axis that matters: centrally managed, one
owner, versioned, and honored by *all* Dynatrace surfaces (notebooks,
dashboards, apps), not just dtctl. Org packs therefore treat segments as the
**system of record for organizational scoping** and store references, not
copies.

**Non-overlap.** Segments are filters, not queries: no projection,
aggregation, or followups (recipes remain the query layer, and recipe × `-S`
compose orthogonally at the API level). They carry no facts, no verification,
and cannot express entity-level mechanics — the `runs_on` hop needs a topology
pre-query, and casts/carriage are per-record-type knowledge. They are also
environment-locked, while packs are cross-environment.

**Integration hooks:**

1. **Discover** — `recipes discover` lists segments into `facts.segments`;
   agents scope with `-S <uid>` instead of reconstructing team filters.
2. **Mine** — segment definitions are human-curated environment semantics in
   machine-readable form ("Host group" ⇒ this org slices by host group). The
   generator reads them before the agent-assisted annotation pass (§4.3) asks
   any human anything.
3. **Reference, don't duplicate** — org scoping that exists as a segment is
   stored as `segments: [uid]` on the recipe (see `audit-log-search` in the
   example recipe book). **One parameter surface:** on such recipes,
   `--set` binds segment variables by name as well as template params, so the
   caller never juggles `--set` and `-S uid?var=` syntaxes for one
   invocation.
4. **Write back** — conventions that profiling discovers can be deposited as
   segments (`dtctl create segment` exists), making the platform the owner
   and every Dynatrace app a beneficiary. This is a mutating operation: it
   goes through the standard safety checker and explicit confirmation, never
   silently from a discovery run. The write-back stance is also the hedge
   against platform preemption — if Dynatrace ships native environment-
   context surfaces, the recipe book shrinks toward facts+stamps while the
   knowledge already lives platform-side.

Caveat: segment application has sharp edges on some API surfaces (e.g. Davis
views rejecting bucket parameters), so "apply segment X" gets the same
verification stamp as everything else.

## 6. Why this saves agent tokens

Today an agent session looks like: read generic DQL reference → compose query →
empty result → guess why → retry (×N). With recipes:

1. `dtctl recipes -o json` — one small call: environment facts plus the
   recipe index, in context.
2. Pick by description instead of composing from scratch.
3. `dtctl query --recipe <name> --set ...` — a stamped query with correct
   scoping and honest empty-result semantics: if a recipe whose
   default-param stamp was non-empty at time T returns empty now, the
   envelope says exactly that — *"37 records at 09:00, 0 now"* — which
   narrows the question to "nothing matches, or ingest changed since T".
   It deliberately does **not** assert "truly nothing": ingest lag makes
   fresh-and-wrong coexist precisely during the incidents being
   investigated, so the stamp is evidence for a comparison, never proof of
   absence.
4. Follow-up suggestions replace planning tokens.

And — just as important — **off the rails**: real investigations leave
recipes after the first hop, and that is where diagnose-on-filter (§3) and
envelope warnings keep raw `query` self-explaining. Recipes buy verified
starting points and facts; the runtime diagnosis buys correctness everywhere
else. The escape hatch is unchanged: raw `dtctl query` remains for
everything the recipe book doesn't cover, and recipes echo their DQL (like
dynatui's `ctrl+q`) so agents can learn from and modify them — recipes as
few-shot examples that are *known to work on this environment* should beat
any static example corpus. "Should" is a hypothesis, not a result — §6.1.

### 6.1 The benefit claim is measurable — and so far unmeasured

Honesty requires separating what §10.1 proved from what it did not. It
proved the *pack* executes cleanly across five very different tenants, and
that per-environment verification catches real hard errors, silent
partials, and licensing gaps. It did **not** measure whether agents
improve: there is no eval, and the cheapest counterfactual — the ~830-line
pack pasted into a static skill — has been dismissed by assertion, never by
data. The §10.1 yield is also asymmetric in a way this doc must own:
recipe-level failures were mostly *loud* (3 hard errors, ~6% disabled —
cheap for an agent to recover from), while the silent-*wrong* protection
(carriage, scoping coverage, licensing gaps) concentrates in ~30 lines of
facts. That asymmetry is why the phasing is facts-first (§8).

Before the pack machinery is built (§8 phase 3), run a three-arm eval on
realistic investigation tasks:

1. static dtctl skill only;
2. skill + the pack rendered as markdown (no verification, no stamps, no
   facts);
3. full recipe book (facts + stamped recipes + envelope integration).

Measured per arm: tokens per completed task, tool calls, retries after
empty results, and wrong-answer rate (the silent-partial traps). Arm 2 vs
arm 3 is the honest test of whether *verification* — the expensive part —
pays for itself beyond what static example text already buys. A cheap proxy
available immediately: queries are already tagged via `dt-client-context`,
so instrument empty-result and retry rates in agent sessions before and
after phases 0.5/1 ship.

## 7. Other ideas considered (complementary, not competing)

- **Distill-from-usage.** dtctl already tags queries with `dt-client-context`.
  Opt-in local logging of successful agent queries + `dtctl recipes distill`
  promotes recurring ones into an org pack. Complements stamp-refresh (§4.0):
  refresh keeps *known* recipes true; distill turns *novel* recurring queries
  into recipes — the agent's trial-and-error becomes institutional memory.
- **Per-environment skill rendering.** `dtctl skills install` can render the
  `-o markdown` briefing as an environment-specific reference next to the
  generic skill. (The primary path stays "call `dtctl recipes` at session
  start" — files go stale, calls don't.)
- **`scopeFilter` as a template func** (`{{ scopeFilter .id "logs" }}`) —
  deferred. Render-time hop widening is a hidden two-phase flow (topology
  pre-query inside `run`), which is exactly what the complexity governors
  prohibit recipes from being, and it breaks recipe portability (echoed DQL
  with a baked entity list, recipes unusable in notebooks). `resolve scope`
  plus `type: dql-filter` params cover the need composably; revisit the
  template func only if that proves clumsy in practice.
- **Convert dynatrace-for-ai.** Its hundreds of examples are exactly base-pack
  material: add guards + params, and the generator turns "hundreds of
  examples, unknown which work" into "the 40 that verifiably work here".
  Verified against the installed set (27 skills, §10.5): the examples are
  already machine-separable — fenced as ```` ```dql ```` (runnable),
  ```` ```dql-template ```` (with `<param>` placeholders that map onto typed
  params), and ```` ```dql-snippet ```` (fragments) — so mining is largely
  mechanical; the skills carry no follow-up model, which the pack's drill
  graph adds on top.
  The corollary is a **single-source rule**: a recipe mined from a skill
  must either replace the skill's example or be generated from the same
  source file. The §10.1 pack already fixed queries the skills still carry
  verbatim — the moment the copies diverge, agents see two disagreeing
  versions of the same knowledge, and the pack quietly becomes the fourth
  copy problem it was supposed to solve. §9 carries the ownership question.
- **MCP surface (later).** Once recipes are typed (name, description, params),
  exposing each as an MCP tool per environment is mechanical — the most
  token-efficient agent interface possible. Not needed for v1; the CLI +
  agent envelope already serve every supported agent.
- **Rejected: recipes as dynamic top-level commands** (`dtctl pods-restarting`).
  Conflicts with the alias resolver, pollutes the command catalog, and gains
  nothing over `dtctl query --recipe <name>`.
- **Rejected: full ViewSpec export from dynatui.** Columns/formatting/lens UI
  concerns don't serialize (closures) and don't help agents; only the query,
  params, scoping, and drill graph do. Export the distillate, not the catalog.
- **Rejected (2026-07-18): `variants:` / carriage-selected recipe bodies.**
  Portable-first DQL covered every case observed across five tenants (§2.2);
  a selection engine would optimize the rare case at the cost of a schema
  axis that grows combinatorially (era × platform × labels). Readmit only on
  a concrete case portable-first cannot express.
- **Rejected (2026-07-18): a distinct org layer schema.** Org knowledge is a
  pack (recipes) plus `facts.declared` (semantics) plus Segments (scoping) —
  three existing homes, no fourth artifact.

## 8. Phasing

Re-cut in the third review round (§10.4): the measured silent-wrong
protection lives in facts, facts need no pack to exist, and the pack
machinery's benefit is unproven (§6.1) — so facts ship first and the
expensive parts are gated on evidence.

- **Phase 0 — envelope polish** *(mostly done — correction in §3)*: verify
  the severity tag on the smartscape-cast notification; add it to the advice
  classifier. Standalone value, no recipe machinery.
- **Phase 0.5 — diagnose-on-filter** in agent mode: carriage probe of
  filter-referenced fields — on empty results *and* on non-empty results
  whose filters touch partially-carried fields — session-cached,
  scan-capped. No schema, no files; attacks the root problem (silent-empty
  *and* silent-partial) inside raw `query`, where agents spend most tokens.
- **Phase 1 (v0) — facts-only recipe book.** `dtctl recipes` (briefing) +
  `resolve scope` + the pack-independent §4.1 battery (census, edges, data
  objects, buckets, carriage for commonly-filtered fields) with the §4.0
  write path and the mandatory probe budget. No recipe entries, no packs.
  This is where §10.1 located the silent-wrong protection, it needs no pack
  to exist, and it gives the §6.1 eval its arm-3 substrate.
- **Phase 2 (v0.5) — recipes, minimal.** Schema + loader + `describe
  recipe` / `query --recipe` (and `verify query --recipe` rendering) over
  **hand-written local recipes** (no `source:`) with typed params; stamps
  refresh on every execution (§4.0). Honestly framed: this is named,
  parameterized queries over the existing `query -f`/`--set` machinery —
  the smallest slice that proves the UX. Teams codify their queries per
  context; agents stop re-deriving them. No generator, no pack pointers.
- **Phase 3 (v1) — base pack + generator-as-prefetch + verification.
  Gated on the §6.1 eval** showing the full recipe book (arm 3) beats
  pack-as-markdown (arm 2). Move discovery queries + scope helpers from
  dynatui to the shared layer; author the initial pack from the dynatui
  catalog + learnings.md (single-source rule, §7); capability definitions;
  `recipes discover`/`refresh` with the mandatory budget; guards;
  classified disabled-with-evidence; pack retention (§4.2); generator
  golden tests (§4.1). Add `recipes`, `describe recipe`, and `resolve` to
  the `query` and `investigate` command-profile presets (§3).
- **Phase 4 (v2) — demand-gated**: org packs + `recipes pack import` (trust
  boundary), agent-assisted annotation (§4.3), per-environment skill
  rendering, distill-from-usage, MCP surface, `run` sugar (it shadows any
  `dtctl-run` plugin, so it must earn its place). dynatui optionally
  consumes recipe books (org recipes appear as views), closing the loop.

## 9. Open questions

- Pack distribution: bundled snapshot in the dtctl release vs fetched
  (`recipes pack update`)? Bundled-with-override is the likely answer —
  and retention (§4.2) makes either safe for existing recipe books.
- **Pack ownership and maintenance economics.** The pack is a data product:
  cross-environment quality requires a recurring verification fleet (the
  §10.1 exercise, automated) and releases tracking semconv churn. Who owns
  it — this repo, dynatrace-for-ai (the single-source rule, §7), or a new
  home? Local verification bounds the damage of a stale pack (disabled
  entries, not wrong answers), but somebody owns freshness.
- **The §6.1 eval corpus.** Which investigation tasks, on which tenants,
  scored how? The eval gates phase 3, so its design is real work that needs
  an owner too.
- Multi-context environments (same environment, several contexts): recipe
  book keyed by environment URL rather than context name?
- Redaction defaults: exactly which facts does `--redacted` strip, and should
  `export` default to redacted?

Resolved since the first draft: staleness policy (living cache, §4.0);
write-path concurrency and read-only deployments (§4.0); disabled-entry
lifecycle (classified evidence + re-probe, §4.0); facts refresh trigger
(non-blocking, age surfaced, §4.0); capability semantics (definitions in
the pack, §2.2); schema evolution (fail-closed guards, §2.2); pack
retention (§4.2); naming (Environment Awareness after the #351 collision,
§10.3 — then **Recipes**, §10.4); org-layer schema (collapsed, §2.1);
verification-cost policy (mandatory budget, §4.1).

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
- *Grail warnings and the envelope* — corrected 2026-07-18: the cast mistake
  produces a server warning, and the original draft claimed it never reaches
  the agent envelope. Code inspection shows `notificationAdvice()`
  (`pkg/exec/dql.go`) already promotes WARNING/WARN/ERROR notifications into
  `context.warnings`/`context.suggestions` on both agent-mode emission paths
  (`pkg/exec/spill_exec.go`); the loose-stderr behavior is the *human* path.
  What remains is classifier coverage — the cast hint is not among the five
  advice categories — and confirming the severity tag the API puts on that
  notification (below WARNING it would be filtered out today).
- *`dt.davis.problems` was ~1:1 rows-to-problems on both tenants over 7d* —
  the "transition log" behavior from dynatui's learnings is real but not
  universal; the `takeLast ... by:{display_id}` dedup stays as a cheap
  defensive pattern, not as a load-bearing assumption.

### 10.1 Pack simulation: 44 recipes × 5 tenants (2026-07-17)

To test the whole pipeline in practice, a 44-recipe pack was assembled (15
recipes distilled from the dynatui catalog, 29 from the dynatrace-for-ai
skills — deliberately kept verbatim, including a skill's `status == "ERROR"`
log filter and uppercase `AND`s) and executed end-to-end against five tenants
of very different character: a small OTel-centric dev tenant, a fully-loaded
demo tenant, a network-observability tenant (Juniper/external network devices,
synthetic locations), a very large enterprise tenant (280k OS services,
66M spans/hour), and a mixed playground tenant where nearly every capability
is simultaneously active. 220 recipe runs plus classification probes. Results:

| | dev | demo | netobs | large | playground |
|---|---|---|---|---|---|
| executed cleanly | 44 | 44 | 42 | 43 | 44 |
| hard query errors | 0 | 0 | **2** | **1** | 0 |
| returned data | 28 | 44 | 39 | 41 | 42 |
| verified-empty (threshold/transient) | 10 | 0 | 2 | 1 | 0 |
| disabled with evidence | 6 | 0 | 3 | 2 | 2 |
| scan-limit partials | 0 | 0 | 0 | **1** | 0 |

The two-tenant headline ("zero errors — divergence is only data presence")
did **not** survive tenants three and four, and that correction is the
strongest evidence for the concept: 217/220 runs were clean, but three hard
errors appeared that only per-environment verification can catch. The run
forced these generator-design lessons. (They originally accreted ten ad-hoc
pack semantics — `verify:` ×5, `floor:`, `portability:`, `partial:`,
`override:`, platform variants; the 2026-07-18 revision collapsed them into
the guard/verify vocabulary of §2.2, as noted per lesson:)

1. **Record-count verification lies for single-row aggregates.** A severity
   summary returned "1 record" on a tenant with zero detections — every count
   was 0. Single-row summaries need value-level verification. This was a real
   false-verified caught only because the probe was re-checked. *(Now:
   `expect: values`.)*
2. **Threshold-empty ≠ capability-absent.** Ten of the dev tenant's 16
   empties were healthy findings ("no host >80% CPU", "no OOMKills") — the
   capability existed (66k `dt.kubernetes.*` series). The generator must
   probe the capability *without* the threshold filter, then stamp the
   thresholded recipe; the two states must read differently to an agent.
   *(Now: threshold-free `probe:` / `probeVia:`; covers the transient class
   too.)*
3. **Sparse windows need widening.** GenAI spans: 0 in a 2h window, 4,566 in
   24h on the same tenant. dynatui's `floorTimeframe` pattern (sessions,
   vulns, bizevents) is real. *(Now: widen-on-empty is universal generator
   behavior; the applied window lands as an `override:` in the recipe
   book — no `floor:` keyword.)*
4. **Carriage thresholds disable recipes.** `dt.failure_detection.results`
   exists on the dev tenant — on 5 of 143,707 spans (0.003%). A field that
   sparse makes a recipe misleading, not merely weak; the recipe book
   disables it with the coverage number as the reason. *(Now: the
   `minCarriage` guard.)*
5. **Cost belongs in the stamp.** The 24h group-by-content log recipe took
   11.8s on the demo tenant; the metrics catalog took 2.4s on the dev tenant
   and **34s** on the large one. The probe already sees execution time (and
   the envelope carries `scannedBytes`); recording it per recipe lets agents
   prefer the cheap recipe — the recipe-book answer to dynatui's ADR-0013 query
   budget, and load-bearing at enterprise scale. *(Now: `seconds` in every
   stamp — and §4.1 additionally caps the generator's own spend, which this
   run did not: the 500 GB scan below happened during generation.)*
6. **Hard errors exist, and they're portability classes, not typos.** The
   dynatui nodes query hard-fails (`INNER_FIELD_OF_FIELD_DOES_NOT_EXIST`) on
   2 of 4 tenants: inner-map access on an absent field (`` `tags:k8s.labels`[…] ``)
   *errors*, unlike top-level absent fields which compare null-safely. And
   `fetch dt.entity.synthetic_test` throws `UNKNOWN_DATA_OBJECT` on a tenant
   that *has* synthetic data in `dt.synthetic.events` — table existence must
   be guarded via `dt.system.data_objects`, not inferred from the capability.
   Both queries were live-validated when written; only cross-tenant probing
   exposed them. *(Now: portable splits — a label-free base recipe plus a
   labels followup — and the `dataObjects` guard.)*
7. **Scan limits create verified-partial.** On the large tenant the 24h
   bizevents scan stopped at Grail's 500 GB limit and returned plausible
   results *plus a warning* — silently incomplete to any consumer that
   ignores the warning channel. The recipe book stamps these `partial:`
   with the remediation (sampling/bucket filter). The agent envelope already
   carries these warnings (§3); the generator must read the same channel when
   stamping.
8. **Entity types are platform-flavored.** The Postgres recipe
   (`DB_INSTANCE_POSTGRES`) is empty on a tenant holding 56k
   `AZURE_MICROSOFT_DBFORPOSTGRESQL_*` entities — same technology, different
   Smartscape types per integration. *(Now: portable multi-type/wildcard
   unions in one body — not per-platform variants.)*
9. **Instrumentation variance is per-field even within one capability.** One
   tenant's GenAI spans (azure.ai.openai) carry usage tokens and provider but
   a null `gen_ai.request.model` — so token recipes verify while by-model
   recipes are structurally empty. A capability flag is never enough; the
   carriage matrix is the real contract.
10. **Some data is licensing/tier-gated, not instrumentation-gated.** The
   playground tenant runs literally everything (RAP, synthetic, GenAI, k8s
   labels — the only tenant where the whole pack's *instrumentation* surface
   is active) yet `dt.system.events` is completely empty: no DPS billing
   telemetry, so both cost recipes disable. The inverse of every other gap —
   and undetectable from any amount of observability data, only from probing
   the table itself.

Smaller confirmations: `smartscapeNodes "AWS_*"` wildcards and the composed
KSPM latest-scan join worked unmodified on all four tenants; the GenAI 24h
widening validated again (0 spans at 2h vs 30 at 24h on the netobs tenant);
and `attacks-recent` proved the transient class (detections exist on all four
tenants over 24h, none from RAP in a 2h window on three of them).

### 10.2 Design review — applied revisions (2026-07-18)

A full review of the concept (including a code-level check of every "reuses
existing machinery" claim) produced these changes, all folded into the
sections above:

- **Naming**: Query Profile → **Tenant Profile** (`kind: TenantProfile`) —
  the example files were already named `tenant-profile.*`; "query profile"
  undersold the facts half and collided with alerting/monitoring profiles.
  The profile's `queries:` section became `recipes:` (matching the pack and
  avoiding collision with `dtctl query`); `metadata.pack` became a structured
  `packs:` list. The referencing form is explicitly framed as a lockfile.
  *(Superseded the same day by the #351 reconciliation — see §10.3.)*
- **CLI grammar**: recipes are a resource — `get recipes` /
  `describe recipe` / `query --recipe` are the primary spellings (zero new
  verbs, automatic catalog/agent integration); `run` is sugar. `resolve
  scope` promoted from "other ideas" to the core surface; the `scopeFilter`
  template func demoted to deferred (§7) because render-time hop widening is
  a hidden two-phase flow.
- **Two artifacts instead of three layers** (§2.1): the org layer dissolved
  into "another pack" + `facts.declared` + Segments.
- **Portable-first DQL replaced `variants:`** (§2.2): typed-OR era filters,
  multi-type unions, enrichment splits, data-object guards. Grounded in the
  §10.1 data: ~85% of recipe×tenant outcomes needed nothing but
  run-and-record; no observed case needs body selection.
- **Verify taxonomy collapsed** from five prose kinds + `floor:` +
  `portability:` to `probe`/`probeVia` + `expect: records|values`, three
  fixed guard shapes, and widen-on-empty as generator behavior.
- **Living cache** (§4.0): stamps refresh on every execution;
  discover/verify are prefetch; the staleness open question is dissolved
  rather than answered. Stamps gained `recipeSha` (survive unrelated pack
  upgrades) and `limitHit` (a maxed count is weak signal).
- **Diagnose-on-empty** added as phase 0.5 — carriage probing at query time
  covers agents *off* the recipe rails, where most investigation tokens go.
- **Two claims corrected against the code**: (1) the local-config trust
  precedent is unconditional refusal, not content-hash acknowledgement — the
  trust model now matches reality and treats import as the explicit trust
  boundary, with stamps stripped on import/export; (2) agent-mode envelope
  warnings largely exist already (`notificationAdvice()`), so the §3
  prerequisite shrank to classifier/severity coverage.
- **Generation budget made mandatory** (§4.1) after the simulation itself
  tripped a 500 GB scan; consumption receipt added.
- **Privacy section** added: generated files carry environment identifiers;
  keep them out of repos, `export --redacted` for shareable artifacts.
- **Example-file fixes**: the box file's contradictory GenAI stamp
  (override recorded but stamp still showed the pre-widening probe) now
  reflects the widened run with `limitHit`; the netobs "returned data" cell
  reconciled (38 → 39); `requires: [none]` sentinel dropped; at-limit stamps
  marked `limitHit` throughout; facts vocabularies aligned with the pack
  guard enum (ad-hoc strings like "genai-sparse" moved to notes).

### 10.3 Upstream reconciliation — rebase + rename (2026-07-18)

The branch was rebased onto dynatrace-oss/dtctl `main` (15 commits), which
merged three changes this concept must acknowledge:

- **#351 Command Profiles** — a default-deny command allowlist bound to a
  context. It claimed the entire "profile" vocabulary: config `profiles:`
  map, `profile:` field on `Context`, `DTCTL_PROFILE`, `--profile` on
  `set-context`, a `"profile"` field in the `dtctl commands` agent envelope,
  and the (explicitly reserved) future `dtctl profile` command group. Both
  features live on the same context object and target the same agent
  audience, so "Tenant Profile" became unsupportable hours after it was
  coined. **Rename applied: Query Profiles → Tenant Profiles → Environment
  Awareness.** "Environment" is the official dtctl term for the tenant
  (`--environment` on `set-context`, environment URLs in `sdk/urls`);
  "awareness" names the knowledge axis of the clean triad — surface (command
  profile) / permission (safety level) / knowledge (awareness). Concretely:
  `kind: EnvironmentAwareness`, `kind: RecipePack` (was QueryPack —
  symmetric with the `recipes:` section), CLI `dtctl awareness` as a
  bare-noun catalog command mirroring `dtctl commands` (with
  `discover`/`verify` subcommands, precedent `commands howto`), config dir
  `~/.config/dtctl/awareness/`, org-pack import moved to `dtctl pack
  import`, and the separate `brief` renderer folded into `dtctl awareness
  -o markdown`. A new §3 subsection specifies the composition (presets must
  list the awareness commands; `query --recipe` rides the `query` allowlist
  entry for free — a top-level `run` would not, reinforcing run-as-sugar).
- **#352 explicit-config trust** — `DTCTL_CONFIG`/`--config` are trusted
  while discovered local configs stay refused. Cited in the trust model as
  the second precedent for the explicit-vs-discovered trust line that
  `pack import` follows.
- **#356 `dtctl commands` tri-mode** (minimal overview default, `--brief`,
  `--full`) — no claim here breaks, but it cements that "brief" means terse
  output mode in this CLI, which had already ruled out "Tenant Briefing" as
  the artifact name.

### 10.4 Adversarial review — applied revisions (2026-07-18, third round)

A third review round (multi-agent: seven dimension critics, adversarial
verification of every major finding against the doc, all eight example
files, and the code, plus a completeness pass) confirmed the diagnosis and
the evidence discipline but found the living cache under-specified, the
capability vocabulary circular, and the central benefit claim unmeasured.
All changes are folded into the sections above; the record:

- **The concept now resolves around "recipes"** (author decision during
  review application). Fourth and final naming step: Query Profiles →
  Tenant Profiles → Environment Awareness → **Recipes**. The center of
  gravity was already the recipe — the pack is a `RecipePack`, the stamps
  are recipe stamps, the commands agents touch are recipe commands — and
  "awareness" was an abstract label for a concrete thing. The per-tenant
  artifact is now the **recipe book** (`kind: RecipeBook`, pairing
  naturally with `RecipePack`), stored at
  `~/.config/dtctl/recipes/<context>.yaml`; the CLI noun is
  **`dtctl recipes`** — one bootstrap call merging the facts briefing and
  the one-line recipe index (the separate `get recipes` was dropped;
  `describe recipe` / `query --recipe` unchanged); lifecycle:
  `dtctl recipes discover|refresh|pack import|export`.
- **Capabilities are now discoverable by definition** (author decision
  during review application). The review found the enum circular — "the
  union of `requires:` across installed packs", with no probe defining
  truth, and the five transcripts demonstrating two contradictory readings
  (`hosts` required and verified everywhere yet listed nowhere;
  `aws-lambda` listed nowhere and therefore disabled). Rather than drop
  the vocabulary, every capability name now carries a **discovery
  definition** in the pack (`dataObject:` / `entityTypes:` / `metricKey:` /
  `probe:`+`window:`); `discover`/`refresh` evaluate definitions into
  `facts.capabilities`/`absent`; undefined names are lint errors and fail
  closed; probe-defined (event-window) capabilities are weak evidence and
  re-evaluated on every refresh (§2.2).
- **Write path specified** (§4.0): atomic temp-file+rename, best-effort
  flock (`sdk/session/refresh_lock_unix.go` precedent), stamp writes that
  never fail the producing query, and first-class read-only mode with
  stamp age in the envelope. Previously the embedded read-only scenario
  structurally could not refresh stamps — contradicting the staleness
  answer for exactly that deployment — and two parallel sessions could
  tear the file.
- **`disabled:` declassified as a one-way door** (§4.0): evidence classes
  (`class: guard` durable vs `class: probe-empty` re-probed on refresh),
  `at:` timestamps, disabled recipes still executable with a warning and
  resurrectable by a non-empty result. The dev-tenant transcript contained
  a live instance of the exact error §10.1 lesson 2 warns about (24h event
  absence recorded as "Runtime Application Protection not active"); its
  entry is now classified and reworded.
- **Stamps renamed and scoped** (§2.2, §4.0): `verified:` → `lastRun:` (a
  last-run cache is not a verification claim); stamps record param
  provenance (`params: default`/hash) and drift/"was N at T" reasoning is
  restricted to default-param stamps; §6's "fresh stamp + empty = truly
  nothing" weakened to comparison wording — ingest lag makes fresh-and-
  wrong coexist precisely during incidents.
- **"Escaped by default" was false** (§2.2): only `dqlString` call sites
  escaped; `timeframe`/`limit`/`sampling` interpolated raw in every
  example, and `--set 'limit=100 | fieldsRemove content'` rewrote the
  query. Replaced by typed params (`string`/`int`/`duration`/`enum`/
  `identifier`/`dql-filter`) validated before render, a pack lint on bare
  interpolation, and rejection of unknown `--set` keys (a typo'd key
  silently rendered the unfiltered `{{if}}` branch — silent-wrong).
- **Verb collisions resolved** (§3): the lifecycle probe command is
  `refresh`, not `verify` — existing `dtctl verify` means *validate
  without executing* (the opposite cost model), and render-without-execute
  composes with that existing verb as `verify query --recipe`. "Zero new
  verbs" corrected to honest accounting (new: `recipes`, `resolve`); `pack`
  folded under `recipes pack` and `run` deferred (plugin shadowing:
  built-ins silently break `dtctl-run`/`dtctl-pack`); `resolve` added to
  the preset list (a `profile: query` agent could previously run recipes
  but never obtain scoping filters).
- **Diagnose-on-empty → diagnose-on-filter** (§3, §8): the zero-record
  trigger missed the doc's own worst failure mode — non-empty,
  silently-partial results. The carriage probe now fires on filtered
  fields regardless of record count.
- **Benefit claim demoted to hypothesis** (§6.1): §10.1 proved recipes
  execute, not that agents improve, and the pack-as-static-skill
  counterfactual had been dismissed by assertion. Phase 3 (pack machinery)
  is gated on a three-arm eval; phasing re-cut facts-first (§8) because
  the measured silent-wrong protection concentrates in facts, not recipes.
- **Schema evolution + pack store specified** (§2.2, §4.2): unknown pack
  keywords fail closed per recipe (fail-open would silently un-guard);
  every instantiated pack version is retained immutably (a lockfile needs
  a store — one upgrade must not dangle every `source:` pointer);
  `volumes:`, `tagging:`, `probeVia:`, `recipeSha` cut (no consumer /
  redundant with the store).
- **Generator testing mandated** (§4.1): recorded probe fixtures + golden
  transcripts; the five committed tenant files had silently diverged in
  schema precisely because no generator or test enforced one.
- **Examples brought under the privacy rule and relabeled** (§2.3, §3):
  three transcript filenames were prefixes of real environment IDs —
  renamed to neutral tenant names, distinctive entity counts rounded, and
  the privacy section now covers filenames explicitly. "Actual generation
  outputs" relabeled as hand-assembled transcripts of the manual run;
  local recipes (no `source:`) given defined semantics; the materialized
  example marked as referencing a fictional superset pack.
- **Trust/lifecycle reconciliation** (§3): provisioned config-dir files
  are trusted — the boundary is *how a file arrived*, and stamp-stripping
  applies to import/export, not to an embedding product's own provisioning;
  `ctx delete` offers to remove the context's recipe book; the local-config
  trust citation now points at `sdk/session` (the owner) rather than the
  `pkg/config` shim; implementation-cost notes de-optimized ("automatic"
  catalog integration is registration only; `-o markdown` is a new
  formatter; `recipe` is the first local-file-backed resource).

### 10.5 Completion pass — examples vs skills and dynatui (2026-07-18, fourth round)

A fourth pass reviewed the concept plus all eight example files against the
two knowledge sources the pack claims to mine — the installed
dynatrace-for-ai skills and the dynatui catalog — and re-derived the §10.1
numbers from the transcripts.

**Held under re-derivation:**

- The §10.1 results table reconciles exactly with all five transcripts
  (usable 38/44/41/42/42; disabled, verified-empty, hard-error, and
  scan-partial counts all re-derived from the committed files).
- Code claims re-verified (`notificationAdvice()` on both agent paths,
  `cmd/verify.go`'s no-execution semantics, the flock precedent, the
  template engine) — with one correction: `dqlString` does not exist yet;
  the engine ships only `default`, and §2.2 now says so.
- The skills survey (27 installed dt-* skills) confirmed §7's conversion
  claim and sharpened it: examples are fenced `dql`/`dql-template`/
  `dql-snippet` with `<param>` placeholders — largely mechanical to mine —
  and **no skill carries a follow-up model** (routing is prose "Related
  Skills"), so the drill graph is the pack's additive channel.
- The dynatui survey confirmed §2/§4.2: the drill graph exists only as
  compiled Go (`Spec.Drills`/`EnterTarget`, row-entity-scoped); the segment
  picker's `VariablesQuery` is the live precedent for `values:` params; and
  the TUI loads no recipe/awareness files today — phase 4's "dynatui
  consumes recipe books" is a genuinely new path, not an integration of
  something half-present.

**Fixed:**

- **The full pack carried no followup graph** — one edge across 45 recipes,
  under a header claiming followups are packing-step output, contradicting
  §2.2 and the excerpt pack (where they are pack-authored). The graph is
  now authored in the pack (61 edges) and followup semantics are specified
  in §2.2: merged-namespace resolution, dangling edges dropped with a note
  (lint warns, cross-pack edges legitimate), disabled targets suppressed
  from envelope suggestions, no param binding (governor default-no).
- **The drill targets did not exist**: slow-traces advertised trace IDs no
  recipe consumed; entity-logs/service-errors lived only in the excerpt
  pack; entity-problems only in the fictional book. Added, marked `added:`
  and needing live verification like `revised:`: trace-by-id (documenting
  the `toUid()` cast trap), entity-logs, entity-spans, entity-problems,
  service-errors — plus azure-census/gcp-census closing the
  dt-obs-azure/dt-obs-gcp coverage gap (both capabilities were defined
  with zero recipes using them). 52 entries total.
- **The `dps` capability definition contradicted its own comment**: defined
  structurally (`dataObject:`) although §10.1 #10's point is that
  `dt.system.events` exists-but-empty on licensing-gated tenants — the
  structural shape reads "present" on exactly the tenants that lack it,
  and the playground transcript's `absent: [dps]` only follows from a
  probe. Now a `probe:` + `window: 7d` definition (weak evidence,
  re-evaluated on refresh).
- **Transcript capability lists completed from measured evidence**: demo
  gained `gcp` (GCP entities in its census) and `k8s-node-labels`
  (nodes-inventory ran with label columns); dev gained `k8s-node-labels`
  (same evidence); large gained `k8s-node-labels` in `absent:` (the labels
  hard-error that disabled its nodes-inventory). The remaining per-file
  facts divergence stays documented (§2.3) until generator golden tests
  enforce one schema.
- **Cross-reference and framing fixes**: the book's service-errors
  "resolves: both committed packs" annotation was false (true now that the
  recipe is ported); entity-logs/entity-problems pins updated; the excerpt
  pack is explicitly the same pack's fully-templated *presentation*, not a
  second pack under the same name@version (which would have violated §4.2
  immutability at the example level); the §2.2 sketch probe now carries
  its `from:` explicitly, matching the committed packs.

Deliberately not done: no followups forced onto leaf recipes (11 remain
terminal — census/inventory/one-row summaries with no sensible next hop),
and no `bind:` param mapping on followup edges (§2.2).
