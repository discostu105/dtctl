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

Grail already holds everything needed to prevent this, in two places dtctl
does not use today:

| Source | What it answers | Cost |
|---|---|---|
| `fieldsSnapshot <object>` (DQL command) | Which fields exist in `logs`, `spans`, `metrics`, `smartscape.nodes` **on this environment**, and on what share of records (`relative_count`, 0–100) | ~0.5 s, **no data consumption** (does not scan raw data) |
| `dt.semantic_dictionary.fields` (1,548 rows) | What a Dynatrace-defined field **means**: type, description, examples, `supported_values` (122 enums such as `loglevel`, `span.kind`), `stability` (stable / experimental / deprecated) | 20–40 ms |
| `dt.semantic_dictionary.models` (2,427 rows) | Which fields belong to a data model (`data_object`, `fields[]`), and for Smartscape entity types: the name field, the ID inputs, relationships, and the legacy `dt.entity.*` equivalent | 20–40 ms |

Joined in one DQL `lookup`, they give an agent what it needs before writing a
query: which fields are really there, how common they are, what they mean,
which values they take, and which are deprecated. This document proposes
exposing that join through dtctl, and using it to sharpen the empty-result
diagnosis that shipped in #672.

Dynatrace itself grounds Davis CoPilot's natural-language-to-DQL generation in
the semantic dictionary, and its published agent skills tell agents to
discover field names rather than guess them. dtctl should make that the path
of least resistance.

## Goals

1. **Discover before guessing** — one command answers "what fields exist in
   `logs` here and what do they mean", in a token budget an agent can afford
2. **Turn silent zero rows into a diagnosis** — a filter on a field that does
   not exist in the object says so, names the near match, and cites evidence
3. **Deprecation steering** — `dt.entity.host` is `deprecated`; the stable
   replacement is `dt.smartscape.host`. Say so where the agent is looking
4. **Zero consumption** — every probe runs on catalog tables or
   `fieldsSnapshot`; none scans raw records
5. **Stay on one dialect** — every output carries the DQL it ran, so the raw
   query stays the canonical way and the command is a shortcut, not a second
   language

## Non-Goals

- **No data-query flags.** Nothing here filters, aggregates or samples
  records. `fieldsSummary`, value distributions and row sampling are data
  queries and stay in DQL (see the principle below).
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
must not invent a query language via flags. This design does not touch that
rule; it makes explicit a carve-out dtctl already relies on. `dtctl inventory`
is nothing but DQL under the hood (data-object catalog, buckets, entity
census, metric catalog) composed with client-side judgment.

> dtctl never wraps a **data** query. It may wrap a **metadata** query (what
> exists, what it means, what it relates to) when the answer needs several
> sources or client-side logic, takes no parameters beyond its subject, and
> feeds an agent's ability to write DQL. Each such command exposes the DQL it
> ran.

Schema discovery meets all three tests: it joins `fieldsSnapshot` with two
dictionary tables and needs fallbacks, its only parameter is the object or
field name, and an agent that does not yet know the field names cannot be
expected to know the tables that describe them.

## Design

### 1. `dtctl describe schema <data-object>`

Fields present in a data object, most common first, with meaning where the
dictionary has one.

```
dtctl describe schema logs                 # fields on ≥1% of records (default)
dtctl describe schema logs --all           # every observed field
dtctl describe schema spans --limit 40     # cap the list
dtctl describe schema bizevents            # no fieldsSnapshot support → dictionary models only
dtctl describe schema SERVICE              # a Smartscape entity type (see §3)
```

**Resolution.** For `logs`, `spans`, `metrics` and `smartscape.nodes` the
command runs one query:

```dql
fieldsSnapshot logs
| lookup [fetch dt.semantic_dictionary.fields
          | fields name, type, description, supported_values, stability],
         sourceField:field, lookupField:name
| filter relative_count >= 1
| fields field, pct=round(relative_count, decimals:1), type=lookup.type,
         stability=lookup.stability, description=lookup.description,
         values=lookup.supported_values
| sort pct desc
```

For every other fetchable object (`bizevents`, `events`, `dt.davis.problems`,
`security.events`, `user.events`, …) `fieldsSnapshot` is not supported, so
the field list comes from the dictionary models whose `data_object` matches,
unioned and joined to the fields table the same way. The output then has no
`pct` column and says so in `basis`.

**Output.** TOON by default in agent mode, table for humans. One row per
field; `description` is passed through untrimmed (average 85 characters) and
`values` is omitted when empty. Measured sizes on a live environment:

| Scope | Rows | TOON |
|---|---|---|
| `logs`, default cut (≥1% of records) | 69 | 14 KB |
| `logs --all` with a dictionary entry | 118 | 25 KB |
| `logs --all` | 669 | spills (`result-file`), as any large query result |

`context` carries `basis` (`"observed"` for `fieldsSnapshot`, `"dictionary"`
for the models fallback), `dql` (the queries run, verbatim), and the usual
`total`/`has_more`. A field with `stability: deprecated` whose name is
`dt.entity.<x>` gets a `replaced_by: dt.smartscape.<x>` hint when the
corresponding `dt.smartscape.<x>` model lists it under `classic_models`.

```yaml
# dtctl describe schema logs -o yaml (abridged, synthetic)
basis: observed
fields:
  - field: timestamp
    pct: 100
    type: timestamp
    stability: stable
    description: The time the record was observed.
  - field: content            # present everywhere, no dictionary entry
    pct: 100
  - field: loglevel
    pct: 98.2
    type: string
    stability: stable
    description: The log event severity level.
    values: [ALERT, CRITICAL, DEBUG, EMERGENCY, ERROR, FATAL, INFO, NONE, NOTICE, SEVERE, TRACE, WARN]
  - field: k8s.namespace.name
    pct: 61.0
    type: string
    stability: stable
    description: The name of the namespace that the pod is running in.
  - field: dt.entity.host
    pct: 44.7
    type: string
    stability: deprecated
    replaced_by: dt.smartscape.host
```

### 2. `dtctl describe field <name>`

One field: its dictionary entry plus where it occurs.

```
dtctl describe field loglevel
dtctl describe field dt.entity.host        # → deprecated, replaced_by dt.smartscape.host
dtctl describe field log_level             # → not in the dictionary; near matches: loglevel, log.raw_level
```

Two queries: the dictionary row (`fetch dt.semantic_dictionary.fields |
filter name == "<name>"`), and presence across the four `fieldsSnapshot`
objects (`fieldsSnapshot logs | filter field == "<name>"`, same for spans,
metrics, smartscape.nodes), reported as `occurs_in: {logs: 98.2, spans: 0.4}`.
When the dictionary has no row, the command lists near matches from the
dictionary and from the observed field sets using the existing
`pkg/suggest` edit-distance helper, and exits 0 with an empty entry: not
finding a field is an answer, not an error.

### 3. Entity types via the models table

`dtctl describe schema SERVICE` (any `smartscape_node_type` value, uppercase)
reads the matching `dt.smartscape.*` model and returns what an agent needs to
write `smartscapeNodes` queries: `name_field` (`dt.service.name`),
`id_inputs`, `fields`, `relationships`, and `classic_model`
(`dt.entity.service`) so the agent knows how to join legacy IDs found in
logs and spans. No new command; the argument shape selects the source.

### 4. Empty-result diagnosis: whole-object evidence

`pkg/exec/empty_diagnosis.go` (#672) already explains an empty fetch result by
sampling 100 records of the fetch stage and comparing referenced field names
against the sample (`empty_reason.code: field_not_in_sample`). The sample
costs a scan, covers 100 records, and can miss rare fields.

For `logs`, `spans`, `metrics` and `smartscape.nodes` the probe becomes
`fieldsSnapshot <object>`: the whole object, no consumption, and a
`relative_count` per field. The sample remains the fallback for other
objects. New finding code `field_not_in_object`, with evidence that names
the source:

```json
"empty_reason": {
  "code": "field_not_in_object",
  "field": "log_level",
  "data_object": "logs",
  "did_you_mean": ["loglevel"],
  "evidence": "`log_level` is not among the 669 fields fieldsSnapshot reports for `logs`; `loglevel` is present on 98.2% of records"
}
```

When a near match has a dictionary row, the suggestion line adds its
description and `supported_values`, so the agent's next query is right on
both the name and the value. A referenced field that is `deprecated` in the
dictionary adds a `# deprecated: use dt.smartscape.host` line whether or not
the result was empty. The dictionary lookup is one extra 40 ms query and runs
only when the diagnosis already fired, so the non-empty hot path is
unchanged except for the deprecation check, which only runs when the query
text references a `dt.entity.*` field.

### 5. Skill text

`skills/dtctl/SKILL.md` gains a four-line "Discover fields before guessing"
block under Initialization, and `references/DQL-reference.md` gains the raw
`fieldsSnapshot` + `lookup` query, so an agent without dtctl's commands (or
with an older binary) still has the canonical path. The `describe field` /
`describe schema` hint in `cmd/root.go` (currently an error that points at
`dt.system.data_objects`) is removed once the commands exist.

## Why not just the skill?

Putting the queries in the skill alone gets perhaps two thirds of the value:
agents that run `--agent` without the skill installed get nothing, the
`lookup` join is fiddly enough that models get it wrong under pressure, and
only dtctl can wire the result into the empty-result diagnosis. The skill
text ships first regardless (zero code), and the commands make it reliable.

## Known gaps in the dictionary

The design degrades to "present, undescribed" rather than hiding these:

- `content` and `status`, the two most common log fields, have **no row in
  the fields table** (they appear only in the `log.general` model's field
  list). `describe schema logs` shows them at 100% with no description.
- There is **no model for `spans`** and only `log.general` / `log.audit` for
  `logs`; 2,295 of 2,427 models are `dt.entity.*` / `dt.smartscape.*`
  topology. The models fallback (§1) is therefore useful for event-shaped
  objects (`bizevents`, `dt.davis.problems`, `dt.system.events`), not for
  signal streams, which have `fieldsSnapshot` instead.
- `event.kind`, `event.category` and `event.status` have no
  `supported_values`.
- `fieldsSnapshot` notes that newly ingested fields can take time to appear.
  Evidence strings say "fieldsSnapshot reports", never "does not exist".

## Technical Design

| Layer | Location | Notes |
|---|---|---|
| Discovery | `sdk/schema/` | Runner-driven like `sdk/inventory` (`Runner` interface over DQL); no file I/O, no output. `Describe(ctx, object)`, `DescribeField(ctx, name)`, `DescribeEntityType(ctx, typ)`. Returns typed structs with `Basis` and the `DQL []string` it ran |
| Commands | `cmd/describe_schema.go`, `cmd/describe_field.go` | Read-only: no safety check. Register `schema` and `field` under `describe`; remove the hint in `cmd/root.go` |
| Diagnosis | `pkg/exec/empty_diagnosis.go` | `diagnoseFields` chooses `fieldsSnapshot` for the four supported objects, sample otherwise; dictionary enrichment behind the existing `probeFunc` so tests substitute a fake |
| Output | `pkg/output/` | TOON/table/JSON/YAML through the existing printers; golden tests in `pkg/output/golden_test.go` with synthetic rows |
| Skill | `skills/dtctl/` | §5 |

Budget: every command is bounded by the existing query timeout flags; the
diagnosis stays inside `emptyProbeBudget` (20 s), which it currently uses for
the sample probe.

Permissions: `fieldsSnapshot` needs read access to at least one bucket of the
object (per the DQL docs). The scope the dictionary tables need is to be
confirmed in phase 2 against a read-scoped token; a 403 on either yields the
usual `insufficient_scope` error with the missing scope named.

## Implementation Phases

1. **Skill text** (§5) — no code, ship immediately
2. **`describe schema` + `describe field`** (§1–3) — `sdk/schema`, two
   commands, golden tests, unit tests with a fake Runner, one E2E against
   `logs`
3. **Diagnosis upgrade** (§4) — swap the probe, add `field_not_in_object`,
   add the dictionary enrichment and deprecation line, extend
   `empty_diagnosis_test.go`
4. **Docs** — `IMPLEMENTATION_STATUS.md`, `AGENTS.md` (Agent Output Mode
   section), `docs/QUICK_START.md`

## Testing Strategy

- **Unit**: fake Runner returning canned `fieldsSnapshot` and dictionary rows;
  cases for supported object, unsupported object (models fallback), entity
  type, unknown field with near matches, deprecated field with and without a
  `classic_models` back-reference.
- **Golden**: `describe schema` and `describe field` in table/TOON/JSON/YAML
  with synthetic field names only (no environment identifiers).
- **E2E**: `dtctl describe schema logs` returns `timestamp` at 100%;
  `dtctl describe field loglevel` returns the enum; an empty `fetch logs |
  filter log_level == "ERROR"` yields `field_not_in_object` with
  `did_you_mean: [loglevel]`.

## References

- [Semantic Dictionary](https://docs.dynatrace.com/docs/discover-dynatrace/references/semantic-dictionary) — fields, data models, stability levels
- [DQL data source commands](https://docs.dynatrace.com/docs/shortlink/data-source-commands) — `fieldsSnapshot`, `describe`
- [Davis CoPilot: query with natural language](https://docs.dynatrace.com/docs/discover-dynatrace/platform/davis-ai/copilot/copilot-dql) — dictionary-grounded DQL generation
- Empty-result diagnosis: `pkg/exec/empty_diagnosis.go` (#672)
- Metadata precedent: `dtctl inventory` (`sdk/inventory/`)
