# ADR-0006: Read-first UI; mutations are safety-gated

**Status:** Partially superseded by [ADR-0011](0011-strictly-read-only.md)
(2026-07-14): the read-first stance stands and is now absolute — mutations
were dropped from the plan entirely, so the safety-gating design below is
moot. · **Date:** 2026-07-05

## Context

dtctl carries a context safety model (readonly / readwrite-mine /
readwrite-all) that agents and operators rely on. A TUI that mutates
casually — or behaves differently from the CLI under the same context —
would break that trust.

## Decision

The TUI is read-first: entity and signal views have no mutating actions at
all. Management-asset mutations (edit/delete/execute, Phase 4) are gated
three ways: the safety checker decides whether the action even appears in
the footer (a `readonly` context makes the TUI purely a browser), modals
mirror the CLI's type-to-confirm semantics, and the status line echoes the
equivalent CLI command afterwards. The TUI also never activates in agent
mode (`--agent`, detected AI environments, non-TTY) — agents keep the JSON
envelope.

## Consequences

- Safety semantics live in the shared `sdk/session` layer (ADR-0005), so
  "readonly means the same thing in both tools" holds by construction.
- The header shows the active safety level color-coded at all times.
- Read-only exploration can be handed to anyone without tenant risk.
