# dtui — Vision & Scope Charter

**Status:** Accepted · **Created:** 2026-07-14 · **Author:** dtctl team

This document is the boundary of the project. It exists so that scope
questions get settled by citation, not relitigated per PR. The underlying
decisions are recorded as ADRs
([0012](../adr/0012-navigate-analyze-mutate.md),
[0013](../adr/0013-query-budget.md),
[0014](../adr/0014-breadth-not-depth.md), plus the earlier ones they build
on); this file is the usable summary and the feature gate.

## Mission

dtui is a keyboard-driven terminal navigator for Dynatrace. It makes a
tenant's topology, signals, and assets *walkable* — fast enough to live in —
and hands over to the web UI or the CLI the moment a task stops being
navigation.

## Who it's for: terminal-first, with a two-minute question

The audience is not a job title. dtui serves the person **already in a
terminal** — a developer mid-debug, an on-call engineer mid-incident — who
has a question like: *is my service erroring? what is this pod doing? what
changed since the deploy? what does this problem touch?* Success means
answering in seconds, in single keystrokes, without writing DQL, and without
switching to a browser.

Deliberately not served: reporting, management summaries, wallboard/NOC
displays, unattended monitoring.

## The boundary: three verbs, three tools

([ADR-0012](../adr/0012-navigate-analyze-mutate.md))

| Verb | Tool | Meaning |
|---|---|---|
| **Navigate & explore** | dtui | see what exists and what is happening: entities, topology, signals, and read-only assets — settings included, because *reading* config is exploring the tenant |
| **Analyze** | web UI / notebooks | open-ended correlation, hypothesis testing, dashboard building, visual depth |
| **Mutate & automate** | dtctl | create / edit / delete / execute, scripting, agents |

dtui's job ends when you know **which entity** and **which timeframe**
matter. Every screen has two exits: `o` deep-links into the web UI (depth),
`c` echoes the equivalent dtctl command (mutation, automation). A view that
can't do both is incomplete.

## Principles

1. **Render to decide, not to present**
   ([ADR-0014](../adr/0014-breadth-not-depth.md)). Sparklines, badges, and
   gradient bars exist so you can pick the next keystroke at a glance.
   Presentation-grade output — axes, legends, dashboard tiles, report
   quality — is the web UI's job.
2. **Breadth across nouns, capped depth per noun**
   ([ADR-0014](../adr/0014-breadth-not-depth.md)). Every primitive an
   operator thinks in gets a view; no primitive gets an analytics suite.
   A noun is *done* at list → detail → scoped drills → exits.
3. **Every keystroke has a query budget**
   ([ADR-0013](../adr/0013-query-budget.md)). One primary scoped query per
   navigation action, one batched enrichment per page, no cross-signal
   correlation in curated views. The escape hatch is user-owned, with cost
   made visible rather than forbidden.
4. **Zero configuration** ([ADR-0010](../adr/0010-curated-defaults-runtime-discovery.md)).
   dtui works on an arbitrary tenant out of the box: curated defaults plus
   runtime discovery, never per-user setup. Configuration surface *is*
   scope — no skins, no plugin system, no keybinding remapping.
5. **Honesty** ([ADR-0007](../adr/0007-scope-honesty.md)). Never claim a
   scope the query didn't apply, never render silent emptiness as truth,
   and surface cost instead of hiding it.
6. **Strictly read-only** ([ADR-0011](../adr/0011-strictly-read-only.md)).
   dtui never mutates anything; the command echo is the handover.

## Budgets

Reviewed against, not guaranteed to the millisecond:

- A keystroke paints feedback immediately (well under 100 ms) and never
  blocks on the network — fetches are async behind spinners.
- A curated view shows first rows, or an explicit empty/error state, within
  ~2 s on a typical tenant.
- Launch to interactive under ~1 s; data loads after, per panel.
- Canonical journeys stay under ~8 keystrokes; every cataloged view is
  reachable in ≤ 2 keystrokes plus an alias.

## The gate — every new feature must pass all six

1. It shows what exists or what is happening. It does not change it and
   does not report on it.
2. It is read-only ([ADR-0011](../adr/0011-strictly-read-only.md)).
3. It fits the query budget ([ADR-0013](../adr/0013-query-budget.md)).
4. It is catalog data — a ViewSpec/TabSpec, not a bespoke screen
   ([ADR-0003](../adr/0003-declarative-view-catalog.md)); a bespoke screen
   requires an ADR.
5. At its depth cap it hands over — deep link or command echo — instead of
   growing ([ADR-0014](../adr/0014-breadth-not-depth.md)).
6. It works with zero configuration on an arbitrary tenant
   ([ADR-0010](../adr/0010-curated-defaults-runtime-discovery.md)).

A feature that fails any item defaults to **no**. Overriding the gate is an
ADR, not a PR comment thread.

## Anti-goals

The standing "no" list — including decisions already made, so the fence is
one place:

- **Not a mutation surface** ([ADR-0011](../adr/0011-strictly-read-only.md)).
- **Not a dashboard renderer** — dashboards/notebooks are listed and
  deep-linked, never rendered as tiles.
- **Not a query IDE** — the DQL hatch is one view, not the center of gravity.
- **Not an agent surface** — agents keep the JSON envelope.
- **No graph drawing** ([ADR-0009](../adr/0009-navigate-dont-draw.md)).
- **Not a monitoring daemon** — dtui is a session tool measured in minutes:
  no alerting, no notifications, no wallboard mode, no background polling
  beyond view auto-refresh.
- **No customization surface** — no plugin system, no themes/skins, no user
  keybinding remapping. Every knob is maintenance tail.
- **No report or export pipelines** — `y` yanks a value; anything more is
  dtctl's output layer.
- **Not a compatibility layer over every platform era** — bridge the
  validated eras (semconv, entity-ID generations), degrade honestly,
  and drop bridges when the platform does.

## Steady state — what done looks like

This project is designed to **finish**, not to grow forever:

- The primitive nouns are covered (breadth), each at its capped depth.
- The key vocabulary is frozen — the drill keys (`l s m p v x …`) are
  muscle-memory API; changing their meaning is a breaking change.
- Runtime discovery absorbs tenant variance, so new tenants don't mean new
  code.

After that, the work is platform-era tracking, curation tweaks, and fixes —
not new subsystems. The complexity budget assumes one or two maintainers:
when a feature needs a new subsystem, the default answer is no
([ADR-0011](../adr/0011-strictly-read-only.md) set the precedent), and a
stale design promise is better deleted than implemented
([../dev/design-gaps.md](../dev/design-gaps.md) tracks which is which).
