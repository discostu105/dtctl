# ADR-0012: Navigate in dtui, analyze in the web UI, mutate via dtctl

**Status:** Accepted · **Date:** 2026-07-14

## Context

Phases 1–3 shipped a large catalog, and every new request now lands on the
same question: does this belong in the TUI? [ADR-0011](0011-strictly-read-only.md)
fenced off mutation, but nothing fences *analysis*-shaped features
(correlation views, dashboard-like panels, config tooling). The Phase 4
asset list also raised whether pure configuration — settings, users,
groups — should even be readable here. Without a stated boundary, the web
UI's entire surface is a candidate backlog, which is exactly how a project
of this size gets out of hand.

## Decision

Three verbs, three tools. **dtui owns navigate and explore**: seeing what
exists (entities, topology, assets) and what is happening (signals,
problems). Management assets — settings included — stay in scope as
**read-only browsing** (list, describe, open): *reading* config is
exploring the tenant; authoring it is not. **The web UI owns analyze**:
open-ended correlation, hypothesis testing, dashboards and notebooks,
visual depth. **dtctl owns mutate and automate.**

dtui is the navigation layer between the CLI and the web, and its job ends
when the user knows *which entity* and *which timeframe* matter. Every
screen must know its two exits: `o` deep-links into the web UI (depth),
`c` echoes the equivalent dtctl command (mutation, automation).

## Consequences

- Feature requests that are analysis in disguise are answered with a deep
  link, not a view.
- Asset views (Phase 4) ship as list / describe / open only — no config
  editors, diff tooling, or validation UIs.
- A view that cannot deep-link *and* command-echo is incomplete.
- Scope debates in review cite this boundary; overriding it is a new ADR.
- The full charter — audience, budgets, the feature gate, anti-goals —
  lives in [../design/vision.md](../design/vision.md).
