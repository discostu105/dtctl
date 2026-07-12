# ADR-0009: Navigate the topology, don't draw it

**Status:** Accepted · **Date:** 2026-07-08

## Context

"Show me Smartscape in the terminal" invites graph drawing. A
force-directed hairball is the part of web Smartscape that does *not* work,
ASCII graph layout is poor at any real fan-out, and lipgloss v1 cannot
composite overlays anyway.

## Decision

The smartscape navigator (`:nav`, global `X`) navigates instead of drawing:
an entity-type census plus type-level relationship schema (overview), a
lightweight type browser, and an ego-centric **walk mode** — neighbors
grouped by (direction, verb), structure ranked before mesh, fan-out capped
with explicit `+N more`, a persistent trail breadcrumb (`←` backtracks the
walk, `esc` leaves the page), and a cursor-following preview pane. The
ranger/Miller-column idiom, not ASCII art. For the true visual graph, `o`
deep-links to web Smartscape.

## Consequences

- Hops are trail entries, not page pushes — a 15-hop walk is one stack
  entry and the app-wide `esc` convention survives.
- One tenant-wide problem query per refresh overlays health on every node;
  a session topo cache makes backtracks zero-query.
- Design: [../design/smartscape-navigator.md](../design/smartscape-navigator.md).
