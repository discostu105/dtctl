# Query Profiles — Concept

> Status: concept / discussion draft.
> Goal: make AI agents (and humans) efficient on a **specific** Dynatrace tenant,
> without hardcoding opinionated DQL into dtctl.

## 1. Problem

dtctl is deliberately shallow on DQL: it executes queries, it doesn't know them.
That is the right call for the core CLI, but it pushes the hard part onto the
caller — and for AI agents the hard part is expensive:

- **Grail fails silently.** A wrong field name, a missing `toSmartscapeId()`
  cast, or the wrong semconv-era field returns `{"records":[]}` with exit 0.
  An agent can't distinguish "nothing there" from "wrong query", so it burns
  tokens on trial-and-error loops.
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
  semconvEras: [otel]                # or [oneagent, otel] — drives coalescing
  entityTypes: { K8S_POD: 1234, SERVICE: 210, HOST: 42 }   # census
  buckets: [default_logs_events, custom_audit]
  tagging:                           # org-declared or agent-annotated
    ownerField: tags.owner
    envDiscriminator: "k8s.namespace.name prefix (prod-*, stg-*)"
  notes:
    - "Payment services log to bucket custom_audit, not default"

scoping:                             # entity type -> per-signal filter idiom
  SERVICE:
    spans: 'dt.smartscape.service == toSmartscapeId("{{.id}}")'
    logs:  hop                       # logs need runs_on topology widening
    problems: 'matchesPhrase(arrayToString(affected_entity_ids), "{{.id}}")'
  K8S_POD:
    logs:  'k8s.pod.name == "{{.name}}"'   # logs carry names, not smartscape ids
    spans: 'dt.smartscape.k8s_pod == toSmartscapeId("{{.id}}")'

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
| Era detection | probe `http.method` vs `http.request.method` counts, etc. | `facts.semconvEras`, coalesce behavior |
| Tag/value discovery | `fieldsSummary` on `tags.*`, `k8s.namespace.name`, ... | tagging hints, param value hints |

Then: filter base-pack recipes by capability guards → render for this tenant →
execute each with `\| limit 1`-style probes → stamp `verified` or move to
`disabled`.

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

## 5. Why this saves agent tokens

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

## 6. Other ideas considered (complementary, not competing)

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

## 7. Phasing

1. **v0 — schema + loader + `recipes`/`run`** (no generator). Hand-written
   profiles already deliver value: teams codify their queries per context;
   agents stop re-deriving them. Smallest possible slice, proves the UX.
2. **v1 — base pack + deterministic generator + verification.** Move discovery
   queries + scope helpers from dynatui to the shared layer; author the initial
   pack from the dynatui catalog + learnings.md; `profile generate/verify`.
3. **v2 — org layer + agent-assisted annotation + briefing/skill rendering +
   distill-from-usage.** dynatui optionally consumes profiles (org recipes
   appear as views), closing the loop.

## 8. Open questions

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
