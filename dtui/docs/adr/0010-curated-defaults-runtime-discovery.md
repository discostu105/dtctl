# ADR-0010: Curated defaults, runtime discovery for the rest

**Status:** Accepted · **Date:** 2026-07-05

## Context

Tenants differ wildly: entity types present, edge verbs, metric
namespaces, field populations, even attribute naming eras (two semconv
generations, two entity-ID eras). Hardcoding shapes breaks silently —
Grail returns empty results, not errors, for wrong names.

## Decision

Hardcode only curated *defaults* (columns, canned charts, lenses where
curation adds value) and discover the rest per environment with cheap,
session-cached metadata queries: node-type census, edge catalog,
`fieldsSnapshot` field census, the `metrics` command, and the semantic
dictionary. The relations panel and navigator are driven by *discovered*
edges, never a hardcoded edge list; chart panels degrade gracefully when a
metric namespace is absent; the record inspector explains fields from the
semantic dictionary in place.

## Consequences

- The view catalog stays small and honest; an Azure-only tenant simply
  doesn't offer `:aws`.
- Queries that bridge eras must OR both discriminators and coalesce both
  display names — the recurring pattern in
  [../dev/learnings.md](../dev/learnings.md) §1.
- Empty results are treated as suspicious during development: no query
  builder ships without live validation.
