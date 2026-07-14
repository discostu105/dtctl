# ADR-0014: Breadth across nouns, capped depth; render to decide

**Status:** Accepted · **Date:** 2026-07-14

## Context

The design's first goal promises a view for every noun an operator thinks
in. The scope risk is not the number of nouns — it is each noun accreting
web-UI-grade depth: more tabs, richer charts, per-domain analytics. At the
same time, dtui already invests in visuals (sparklines, braille charts,
gradient bars) that earn their keep through glanceability, so a blunt "no
visual fidelity" rule would ban the wrong thing. The actual creep vector
is presentation-grade rendering: axes, legends, export-quality charts,
dashboards as tiles.

## Decision

Coverage is **broad across nouns and capped per noun**. A noun is done at:
list → detail (curated tabs) → pre-scoped signal drills → exits (`o` deep
link, `c` command echo). Beyond that cap the answer is an exit, not a new
tab. Bespoke pages stay the enumerated exceptions
([ADR-0003](0003-declarative-view-catalog.md)) — problem, vulnerability,
trace waterfall, navigator — and a new one requires an ADR.

Rendering exists to **pick the next keystroke**: glanceable sparklines,
badges, gradient bars, color-coded severity. Never to present:
no presentation-grade charts, no dashboard-tile rendering, no report
output.

## Consequences

- "Don't chase completeness" becomes checkable: a noun is complete when it
  has its list, its detail, and its drills — not when it matches the web UI.
- Chart changes are judged by "does this change which row the user picks
  next", not by resolution or beauty.
- Detail pages stay one screen deep; depth requests route to `o`.
- Missing nouns (`:costs`, mobile apps, Azure/GCP inventories —
  [../dev/design-gaps.md](../dev/design-gaps.md) §11) remain valid backlog:
  breadth is the goal; depth is not.
