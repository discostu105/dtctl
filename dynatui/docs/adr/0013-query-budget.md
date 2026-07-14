# ADR-0013: Every keystroke has a query budget

**Status:** Accepted · **Date:** 2026-07-14

## Context

Every navigation keystroke triggers DQL against Grail, which bills by data
scanned. [ADR-0001](0001-dql-is-the-substrate.md) already makes unscoped
queries structurally impossible, but nothing bounds query *count* or
*shape*. "Avoid expensive queries" fails as a guideline because keywords
are bad proxies: the service→pods topology join runs over
`smartscapeNodes`/`smartscapeEdges` (cheap metadata) while a logs×spans
correlation is a double scan of signal data — both say `join`. Metric
enrichment columns already add a second query per page, and per-row
fan-out is the classic way a TUI becomes a cost problem.

## Decision

A navigation action costs at most **one primary scoped Grail query**, plus
at most **one batched enrichment query per visible page** — enrichment
degrades to blank cells, never errors, and never delays the primary rows.
Curated views run **no cross-signal correlation** (logs×spans and friends);
the user composes correlation by navigating (span → `l` → its logs).
Topology and metadata queries — node census, edge catalog, field census,
metric catalog, semantic dictionary — are exempt: cheap, session-cached
([ADR-0010](0010-curated-defaults-runtime-discovery.md)). The escape hatch
(`ctrl+q`) is user-owned: any query goes, with cost made **visible**
(scanned GB / records progress) rather than forbidden.

## Consequences

- A PR adding a column, panel, or tab must be able to state its queries;
  anything needing per-row fan-out is redesigned or rejected.
- Some depth requests become deep links on budget grounds alone — that is
  working as intended ([ADR-0012](0012-navigate-analyze-mutate.md)).
- Cost visibility extends scope honesty ([ADR-0007](0007-scope-honesty.md))
  from *what was queried* to *what it cost*; the query-hatch progress gap
  in [../dev/design-gaps.md](../dev/design-gaps.md) gains its "why".
- Backtracking must stay free: the breadcrumb stack keeps popped views'
  data, and topology caches make re-walks zero-query.
