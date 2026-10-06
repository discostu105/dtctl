# Schema Discovery Design: `fieldsSnapshot` + Semantic Dictionary

**Status:** Design Proposal
**Created:** 2026-10-06
**Author:** dtctl team

## Overview

DQL is schemaless. A guessed or misspelled field name does not fail; it
returns zero rows, and `dtctl verify query` reports the query as valid. Agents
then widen the time window, pay for a bigger scan, and conclude the data does
not exist. Measured on a live environment:

```
dtctl query 'fetch logs | filter log_level == "ERROR" | limit 1'   → 0 rows, no error
dtctl verify query 'fetch logs | filter log_level == "ERROR"'      → {"valid": true}
```

Grail holds everything needed to prevent this, in two places dtctl does not
use today:

| Source | What it answers | Cost |
|---|---|---|
| `fieldsSnapshot <object>` (DQL command) | Which fields exist in `logs`, `spans`, `metrics`, `smartscape.nodes` **on this environment**, and on what share of records (`relative_count`, 0–100, last 24h) | ~0.5 s, **no data consumption** (does not scan raw data) |
| `dt.semantic_dictionary.fields` (1,548 rows) | What a Dynatrace-defined field **means**: type, description, examples, `supported_values` (122 enums such as `loglevel`, `span.kind`), `stability` (stable / experimental / deprecated) | 20–40 ms |
| `dt.semantic_dictionary.models` (2,427 rows) | Which fields belong to a data model (`data_object`, `fields[]`), and for Smartscape entity types: the name field, the ID inputs, relationships, and the legacy `dt.entity.*` equivalent | 20–40 ms |

Joined in one DQL `lookup`, they give an agent what it needs before writing a
query: which fields are really there, how common they are, what they mean,
which values they take, and which are deprecated. Dynatrace grounds Davis
CoPilot's natural-language-to-DQL generation in the semantic dictionary, and
its published agent skills tell agents to discover field names rather than
guess them.

This design has two halves, split by the rule the recipes design states for
all agent ergonomics: curated knowledge is **content**, correctness that
every query needs is **binary**.

- **Content**: four `meta-*` recipes
  ([RECIPES_DESIGN.md](RECIPES_DESIGN.md), branch `docs/recipes-design`),
  joining the recipes prototype's existing discovery domain
  (`meta-data-objects`, `meta-buckets`, `meta-entity-id`,
  `meta-primary-tags`, the last of which already runs `fieldsSnapshot`).
- **Binary**: the empty-result diagnosis that shipped in #672 moves from a
  100-record sample to whole-object `fieldsSnapshot` evidence and learns
  the dictionary's meaning, enum values and deprecations; `verify recipe`
  gains a field-existence check the verify API cannot do.

Every DQL body below was run against a live environment on 2026-10-06.

## Goals

1. **Discover before guessing** — one call answers "what fields exist in
   `logs` here and what do they mean", in a token budget an agent can afford
2. **Turn silent zero rows into a diagnosis** — a filter on a field that does
   not exist in the object says so, names the near match, and cites evidence
3. **Deprecation steering** — `dt.entity.host` is `deprecated`; the stable
   replacement is `dt.smartscape.host`. Say so where the agent is looking
4. **Zero consumption** — every probe runs on catalog tables or
   `fieldsSnapshot`; none scans raw records
5. **One dialect** — discovery is DQL the agent can read and adapt
   (`--dry-run`, `context.query`); no built-in command wraps it

## Non-Goals

- **No new built-in command.** Discovery ships as recipe content. The
  fallback, should recipes not ship, is one paragraph (§5).
- **No data-query flags.** Nothing here filters, aggregates or samples
  records. `fieldsSummary`, value distributions and row sampling are data
  queries and stay in DQL.
- **No bundled dictionary.** The fields table is 363 KB as TOON (~90k
  tokens) and versioned per environment. It is queried live, never embedded
  in the binary or the skill, and never injected wholesale into a response.
- **No custom-field descriptions.** The dictionary describes the Dynatrace
  vocabulary only. On the measured environment 118 of 669 log fields and 182
  of 701 span fields had an entry; the rest are customer-specific and only
  `fieldsSnapshot` knows they exist. The design reports presence for every
  field and meaning for the ones that have one.
- **No relationship-graph traversal.** Entity relationships are listed, not
  walked. `smartscapeEdges` already does that in DQL.

## The Principle: Metadata Is Not Data

[API_DESIGN.md §2](API_DESIGN.md) says `dtctl query` is a dumb pipe and dtctl
must not invent a query language via flags. This design adds no command that
wraps DQL. The two binary-side features it does touch, `dtctl inventory` and
the empty-result diagnosis, already exist and already run DQL internally, so
the carve-out below records what dtctl does rather than extending it:

> dtctl never wraps a **data** query. It may wrap a **metadata** query (what
> exists, what it means, what it relates to) when the answer needs several
> sources or client-side logic, takes no parameters beyond its subject, and
> feeds an agent's ability to write DQL. Each such feature exposes the DQL it
> ran.

Recipes satisfy the last clause by construction. A recipe is a named,
parameterized DQL query with `--dry-run` and `context.query`; the recipes
design's own non-goals keep design principle 2 intact for resource commands.

## Design

### 1. Content: the `meta-*` recipes

Four recipes in `recipes/meta/`. All are `timeframe: none` (catalog state;
`fieldsSnapshot` covers the last 24h on its own), need no scope dimensions,
and are read-only. Field names in the examples are from the dictionary, not
from any environment.

#### `meta-fields` — fields present in a data object

```
dtctl run meta-fields logs                   # fields on ≥1% of records, most common first
dtctl run meta-fields spans --min-pct 0      # every observed field
dtctl run meta-fields logs --search level    # name contains "level": the near-miss hunt
```

```yaml
apiVersion: dtctl.dev/v1alpha1
kind: Recipe
metadata:
  name: meta-fields
  version: 1
  tags: [meta, discovery, fields, schema, semantic-dictionary]
spec:
  summary: Fields present in logs, spans, metrics or Smartscape nodes here, how common each is, and what it means
  timeframe: none
  params:
    object:
      type: enum
      values: [logs, spans, metrics, smartscape.nodes]
      render: identifier
      required: true
      positional: true
      description: Data object (the four fieldsSnapshot supports; others use meta-model-fields)
    min_pct:
      type: int
      default: 1
      min: 0
      max: 100
      description: Hide fields on fewer than this percentage of records
    search:
      type: string
      description: Only fields whose name contains this (case-insensitive); ignores --min-pct
  dql: |
    fieldsSnapshot {{.object}}
    {{- with .search}}
    | filter contains(field, {{.}}, caseSensitive: false)
    {{- else}}
    | filter relative_count >= {{.min_pct}}
    {{- end}}
    | lookup [fetch dt.semantic_dictionary.fields
              | fields name, type, stability, description, supported_values],
             sourceField: field, lookupField: name
    | fieldsAdd pct = round(relative_count, decimals: 1),
        type = lookup.type, stability = lookup.stability,
        description = lookup.description, values = lookup.supported_values
    | fieldsAdd replaced_by = if(stability == "deprecated" and startsWith(field, "dt.entity."),
        concat("dt.smartscape.", substring(field, from: 10)))
    | fields field, pct, type, stability, description, values, replaced_by
    | sort pct desc
    | limit 300
  means: >-
    One row per field seen in the object in the last 24h. pct is the share of
    records carrying it; a filter on a field with low pct matches few rows,
    and a field absent from this list matches none. type, stability,
    description and values come from the semantic dictionary and are null
    for custom fields, which exist all the same. values lists the allowed
    strings where the dictionary defines them; compare with ==, case-sensitive.
    stability deprecated means a stable field exists; replaced_by names it
    for the dt.entity.* family. Field statistics can lag new ingest.
  emptyMeans: >-
    With --search, no field name contains the term: try a shorter stem, or
    the dictionary-wide search in meta-field. Without it, the object has no
    field on --min-pct percent of records, which means the object holds no
    data in the last 24h: check meta-data-objects and meta-buckets.
  next:
    - recipe: meta-field
      when: nonempty
```

Measured output sizes for `logs` on a live environment:

| Scope | Rows | TOON |
|---|---|---|
| default (`--min-pct 1`) | 69 | 14 KB |
| `--min-pct 0`, fields with a dictionary entry | 118 | 25 KB |
| `--min-pct 0` | 669 | hits the recipe envelope bound and spills, as any large result |

#### `meta-model-fields` — fields a data model declares

For objects `fieldsSnapshot` does not support (`bizevents`, `events`,
`dt.davis.problems`, `security.events`, `user.events`, `dt.system.events`,
…) the only field list is the dictionary's.

```
dtctl run meta-model-fields bizevents
dtctl run meta-model-fields dt.davis.problems
```

```yaml
spec:
  summary: Fields the semantic dictionary declares for a data object's models (for objects fieldsSnapshot does not cover)
  timeframe: none
  params:
    object:
      type: string
      required: true
      positional: true
      description: Data object name as in dt.system.data_objects
  dql: |
    fetch dt.semantic_dictionary.models
    | filter data_object == {{.object}}
    | expand field = fields
    | summarize models = collectDistinct(name), by: {field}
    | lookup [fetch dt.semantic_dictionary.fields
              | fields name, type, stability, description, supported_values],
             sourceField: field, lookupField: name
    | fieldsAdd type = lookup.type, stability = lookup.stability,
        description = lookup.description, values = lookup.supported_values
    | fields field, models, type, stability, description, values
    | sort arraySize(models) desc
    | limit 300
  means: >-
    One row per field any model of the object declares; models lists which.
    This is the dictionary's view, not an observation: a declared field can
    be absent from this environment's records, and custom fields never appear
    here. For logs, spans, metrics and smartscape.nodes use meta-fields, which
    reports what is actually present.
  emptyMeans: >-
    The dictionary has no model for this data object (spans and user.events
    have none; logs has only log.general and log.audit). If fieldsSnapshot
    supports the object, meta-fields is the answer; otherwise sample it:
    dtctl query 'fetch <object> | limit 20'.
```

#### `meta-field` — one field, explained and located

```
dtctl run meta-field loglevel
dtctl run meta-field dt.entity.host     # deprecated → replaced_by dt.smartscape.host
```

```yaml
spec:
  summary: What one field means (type, description, allowed values, stability) and which data objects carry it
  timeframe: none
  params:
    name:
      type: string
      required: true
      positional: true
      description: Exact field name
  dql: |
    fetch dt.semantic_dictionary.fields
    | filter name == {{.name}}
    | lookup [fieldsSnapshot logs | filter field == {{.name}}],
             sourceField: name, lookupField: field, prefix: "logs_"
    | lookup [fieldsSnapshot spans | filter field == {{.name}}],
             sourceField: name, lookupField: field, prefix: "spans_"
    | lookup [fieldsSnapshot metrics | filter field == {{.name}}],
             sourceField: name, lookupField: field, prefix: "metrics_"
    | lookup [fieldsSnapshot smartscape.nodes | filter field == {{.name}}],
             sourceField: name, lookupField: field, prefix: "smartscape_"
    | fieldsAdd replaced_by = if(stability == "deprecated" and startsWith(name, "dt.entity."),
        concat("dt.smartscape.", substring(name, from: 10)))
    | fields name, type, stability, replaced_by, description, examples, supported_values,
        logs_pct = round(logs_relative_count, decimals: 1),
        spans_pct = round(spans_relative_count, decimals: 1),
        metrics_pct = round(metrics_relative_count, decimals: 1),
        smartscape_pct = round(smartscape_relative_count, decimals: 1)
  means: >-
    The dictionary entry plus presence: each *_pct is the share of that
    object's records carrying the field in the last 24h, null when not seen
    there. supported_values are the only strings the field takes where the
    dictionary defines them. stability deprecated with replaced_by set means
    the query should use the replacement; the deprecated field may still be
    the one present on older records, so check both pct columns.
  emptyMeans: >-
    The dictionary has no entry with exactly this name. The field may still
    exist as a custom field, or under a near spelling: run
    meta-fields <object> --search <stem>. content and status, the most common
    log fields, have no dictionary entry and are found that way.
  next:
    - recipe: meta-fields
      when: empty
```

#### `meta-entity-type` — a Smartscape node type's model

```
dtctl run meta-entity-type SERVICE
```

```yaml
spec:
  summary: How a Smartscape entity type is modelled — its name field, ID inputs, fields, relationships and legacy dt.entity.* model
  timeframe: none
  params:
    type:
      type: string
      required: true
      positional: true
      pattern: "^[A-Z][A-Z0-9_]*$"
      description: Smartscape node type as used by smartscapeNodes (SERVICE, HOST, K8S_CLUSTER, …)
  dql: |
    fetch dt.semantic_dictionary.models
    | filter smartscape_node_type == {{.type}}
    | fields type = smartscape_node_type, model = name, title,
        name_field = smartscape_node_name, id_inputs = smartscape_id_inputs,
        classic_models, fields, relationships, description
  means: >-
    One row for the type. name_field is the field smartscapeNodes exposes as
    name; fields are the node's properties; relationships are the edge types
    and target types for smartscapeEdges; classic_models names the legacy
    dt.entity.* view and, by the same stem, the dt.entity.* ID field that
    older records carry (dt.entity.service for SERVICE).
  emptyMeans: >-
    No Smartscape model has this node type. Types are uppercase with
    underscores; list the ones present here with
    dtctl query 'smartscapeNodes "*" | summarize count(), by: {type}'.
```

**Discovery flow.** `dtctl inventory` (which objects hold data) →
`meta-fields` or `meta-model-fields` (which fields, what they mean) → a
domain recipe or `dtctl query`. The recipes design's `next` edges link
`meta-data-objects` → `meta-fields`; the skill names the chain once.

### 2. Binary: empty-result diagnosis with whole-object evidence

`pkg/exec/empty_diagnosis.go` (#672) explains an empty fetch result by
sampling 100 records of the fetch stage and comparing the referenced field
names against the sample (`empty_reason.code: field_not_in_sample`). The
sample costs a scan, covers 100 records, and misses rare fields.

For `logs`, `spans`, `metrics` and `smartscape.nodes` the probe becomes
`fieldsSnapshot <object>`: the whole object, no consumption, and a
`relative_count` per field. The sample remains the fallback for other
objects. New finding code `field_not_in_object`:

```json
"empty_reason": {
  "code": "field_not_in_object",
  "field": "log_level",
  "data_object": "logs",
  "did_you_mean": ["loglevel"],
  "evidence": "`log_level` is not among the 669 fields fieldsSnapshot reports for `logs`; `loglevel` is present on 98.2% of records"
}
```

Enrichment, one extra 40 ms dictionary query that runs only when the
diagnosis fired:

- a near match with a dictionary row adds its description and
  `supported_values` to the suggestion line, so the next query is right on
  the name and the value;
- a referenced field that is `deprecated` adds
  `# deprecated: use dt.smartscape.host` (the `dt.entity.*` → `dt.smartscape.*`
  stem rule, confirmed by `classic_models` in the dictionary);
- the suggestions name `dtctl run meta-fields <object>` when recipes are
  present, else the raw `fieldsSnapshot` + `lookup` query.

Precedence is the recipes design's: a concrete finding
(`field_not_in_object`, `field_not_in_sample`, `metric_not_in_window`) comes
before a recipe's `emptyMeans`, which is added as a warning. The non-empty
hot path is unchanged.

### 3. Binary: `verify recipe` checks fields exist

`dtctl verify recipe --all` validates every rendered query through the DQL
verify API, which accepts unknown field names. In the live job it gains:

- **field existence**: for a recipe over `logs`, `spans`, `metrics` or
  `smartscape.nodes`, every field the rendered DQL references in `filter`,
  `by:` and `fields` must appear in `fieldsSnapshot` of that object (a
  warning, not a failure: a field can be legitimately absent on one
  environment);
- **deprecated field**: a referenced field that the dictionary marks
  `deprecated` is a warning naming the replacement, so built-in recipes stay
  on stable fields except where a legacy ID is the point.

Both reuse the field-reference extraction and the `fieldsSnapshot` probe
from §2.

### 4. Skill text

`skills/dtctl/SKILL.md` gains a four-line "Discover fields before guessing"
block under Initialization naming `meta-fields` and `meta-field`, and
`references/DQL-reference.md` carries the raw `fieldsSnapshot` + `lookup`
query for agents without the recipe layer. The `describe field` /
`describe schema` hint in `cmd/root.go` points at `dtctl run meta-fields`.

### 5. Fallback if recipes do not ship

Recipes are `experimental` and their phase 2 is gated on an evaluation. If
the layer is shelved, the four DQL bodies above become `dtctl describe schema
<object|TYPE>` and `dtctl describe field <name>` under `sdk/schema/` (a
Runner-driven package like `sdk/inventory`) with the same output columns and
`context.dql` carrying the query. Nothing in §2–§4 changes. Writing the
content as recipes first loses nothing either way.

## Known gaps in the dictionary

The design degrades to "present, undescribed" rather than hiding these:

- `content` and `status`, the two most common log fields, have **no row in
  the fields table** (they appear only in the `log.general` model's field
  list). `meta-fields logs` shows them at 100% with null meaning;
  `meta-field content` is empty and its `emptyMeans` says why.
- There is **no model for `spans`** and only `log.general` / `log.audit` for
  `logs`; 2,295 of 2,427 models are `dt.entity.*` / `dt.smartscape.*`
  topology. `meta-model-fields` is therefore useful for event-shaped objects
  (`bizevents`, `dt.davis.problems`, `dt.system.events`), not for signal
  streams, which have `fieldsSnapshot` instead.
- `event.kind`, `event.category` and `event.status` have no
  `supported_values`.
- `fieldsSnapshot` notes that newly ingested fields can take time to appear.
  Evidence strings say "fieldsSnapshot reports", never "does not exist".
- The dictionary has no `replaced_by` column. The `dt.entity.<x>` →
  `dt.smartscape.<x>` stem rule is confirmed by `classic_models` on the
  `dt.smartscape.*` models; other deprecated fields get the flag without a
  replacement.

## Technical Design

| Layer | Location | Notes |
|---|---|---|
| Recipes | `recipes/meta/meta-{fields,model-fields,field,entity-type}.yaml` | Content only; golden rendered DQL in `recipes/testdata/golden/rendered/`; `meta-data-objects.next` gains an edge to `meta-fields` |
| Diagnosis | `pkg/exec/empty_diagnosis.go` | `diagnoseFields` chooses `fieldsSnapshot` for the four supported objects, sample otherwise; dictionary enrichment behind the existing `probeFunc` so tests substitute a fake |
| Verify | `pkg/recipes/` lint + `cmd/verify_recipe.go` live mode | Reuses `referencedFields` and the `fieldsSnapshot` probe |
| Skill | `skills/dtctl/` | §4; `TestSkillNamesRealRecipes` covers the new names |

Budget: the diagnosis stays inside `emptyProbeBudget` (20 s), which it uses
for the sample probe today. Recipes are bounded by the recipe envelope bound
and the shared query limits.

Permissions: `fieldsSnapshot` needs read access to at least one bucket of the
object (per the DQL docs). The scope the dictionary tables need is to be
confirmed in phase 1 against a read-scoped token; a 403 yields the usual
`insufficient_scope` error with the missing scope named.

## Implementation Phases

1. **Content** (§1, §4) — four recipes with golden rendered DQL, the skill
   block, the `root.go` hint; no Go change beyond the hint
2. **Diagnosis** (§2) — swap the probe, add `field_not_in_object`, dictionary
   enrichment and the deprecation line; extend `empty_diagnosis_test.go`
3. **Verify** (§3) — field-existence and deprecated-field warnings in the
   live `verify recipe` job
4. **Docs** — `IMPLEMENTATION_STATUS.md`, `docs/RECIPES.md` (meta domain),
   `AGENTS.md` (Agent Output Mode section)

## Testing Strategy

- **Recipes**: the offline recipe suite (schema, template references, golden
  rendered DQL per param combination: `object` × `search`/`min_pct`); the
  live `verify recipe --all` job; a nightly smoke run that `meta-fields logs`
  returns `timestamp` at 100% on every test environment.
- **Diagnosis**: fake `probeFunc` returning canned `fieldsSnapshot` and
  dictionary rows; cases for supported object, unsupported object (sample
  fallback), near match with and without a dictionary row, deprecated field
  with and without a `classic_models` back-reference, probe failure (no
  finding, advice unchanged).
- **E2E**: an empty `fetch logs | filter log_level == "ERROR"` yields
  `field_not_in_object` with `did_you_mean: [loglevel]` and the enum in the
  suggestion line.

## References

- [RECIPES_DESIGN.md](RECIPES_DESIGN.md) (branch `docs/recipes-design`) — the
  recipe format, the `meta-` domain, `emptyMeans` precedence, `verify recipe`
- [Semantic Dictionary](https://docs.dynatrace.com/docs/discover-dynatrace/references/semantic-dictionary) — fields, data models, stability levels
- [DQL data source commands](https://docs.dynatrace.com/docs/shortlink/data-source-commands) — `fieldsSnapshot`, `describe`
- [Davis CoPilot: query with natural language](https://docs.dynatrace.com/docs/discover-dynatrace/platform/davis-ai/copilot/copilot-dql) — dictionary-grounded DQL generation
- Empty-result diagnosis: `pkg/exec/empty_diagnosis.go` (#672)
- Metadata precedent: `dtctl inventory` (`sdk/inventory/`)
