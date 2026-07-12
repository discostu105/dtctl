# ADR-0007: Scope honesty — never claim a scope the query didn't apply

**Status:** Accepted · **Date:** 2026-07-07

## Context

A global pin (`.`) hands an arbitrary entity to whatever view opens next,
but not every query can compose every entity type. Naively applying the pin
lies in two ways: the query ignores the entity (unfiltered data labelled as
scoped) or composes a filter that matches nothing (silently empty, "this
host has no traces"). Both mislead an investigation at its most critical
moment.

## Decision

A breadcrumb or header that shows scope is a *promise the query kept* —
derive the label from what the query actually did, never from what the user
intended. `Spec.CanScope` enforces this by construction: if
`Query(scoped) == Query(unscoped)`, the entity had no effect, so it is
neither applied nor claimed; an optional `Scopable` predicate refines the
matches-nothing case (e.g. spans carry no host field). When a pin doesn't
apply, navigate unscoped and say so in the status line — `:pods` should
always give you pods.

## Consequences

- Scope-ignoring views are caught for free, including future ones.
- Views that cannot honor a scope say so (dimmed segment pill, `jumpTo`
  status) instead of silently showing wrong data.
- The same principle governs segments and API-backed views: features that
  can't reach a view are visibly disabled, never silently ignored. See
  [../dev/learnings.md](../dev/learnings.md) §3.
