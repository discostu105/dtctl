# ADR-0003: Declarative view catalog, bespoke screens as the exception

**Status:** Accepted · **Date:** 2026-07-05

## Context

The platform surface is wide (~30 entity/signal views and growing). Thirty
bespoke screens would be unmaintainable and inconsistent.

## Decision

Views are data: a `catalog.Spec` literal (query builder, columns, drills,
enrichment, detail tabs) rendered by one generic table engine. Adding a
view is configuration, not plumbing. Bespoke `viewModel` implementations
exist only where a table genuinely doesn't fit: home, DQL escape hatch,
trace waterfall, session timeline, record inspector, metrics charts,
smartscape navigator, and the problem/vulnerability pages.

## Consequences

- The catalog package (`internal/tui/catalog`) is pure data + query
  builders, free of bubbletea — cheap to golden-test against fixture
  scopes.
- Optional capability interfaces (`selectionProvider`, `dqlProvider`,
  `traceProvider`, `busyReporter`) let the app treat bespoke screens
  uniformly.
- The extension model is documented in
  [../dev/learnings.md](../dev/learnings.md) §2.
