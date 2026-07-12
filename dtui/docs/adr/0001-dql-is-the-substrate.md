# ADR-0001: DQL is the substrate, not the interface

**Status:** Accepted · **Date:** 2026-07-05

## Context

Dynatrace users think in nouns (services, pods, problems, logs), not in
query language. At the same time, Grail imposes hard rules: broad unscoped
signal queries fail (scan limits), and every signal query needs entity +
timeframe scope. A TUI could either wrap the REST resource APIs, become a
query IDE, or curate queries.

## Decision

Every view is a curated DQL query under the hood. Drill-down keys compose
scope (entity + timeframe) into the next query automatically, so the
"never run an unscoped query" rule is structural, not advisory. The user
never has to write DQL to navigate — and `ctrl+q` ("reveal query") exposes
the generated DQL of any view in an editable escape hatch for the moment
curation runs out.

## Consequences

- The TUI teaches DQL instead of hiding it: every screen can show how it
  was made, plus the equivalent dtctl command (command echo).
- The query builders are the core asset and must be validated live — Grail
  fails *silently* (wrong field/id/table → empty result, no error). See
  [../dev/learnings.md](../dev/learnings.md) §1.
- Views without a DQL substrate (SLOs, anomaly detectors) are the exception
  and must honestly disable query-derived features (server search, facets).
