# ADR-0011: dtui is strictly read-only

**Status:** Accepted · **Date:** 2026-07-14 · **Supersedes:** the mutation
half of [ADR-0006](0006-read-first-safety-gated.md)

## Context

ADR-0006 made the TUI read-*first* and planned Phase 4 mutations
(edit / delete / workflow execute) behind three gates: the safety checker,
type-to-confirm modals, and command echo. Every one of those gates is
complexity — `$EDITOR` suspend/restore across the alternate screen,
CLI-parity confirmation modals, per-resource ownership resolution, and a
test surface that must prove a TUI can never mutate what the CLI would have
refused. Meanwhile the CLI already does all of it, and the command echo
(`c`) hands the user the exact command to run there.

## Decision

**dtui never mutates anything. Mutations are not deferred — they are not
planned at all.** The scope cap exists mainly to limit the project's
complexity. Phase 4 shrinks to *read-only asset browsing*: list, describe,
open-in-browser for the management surface (workflows + executions,
dashboards, notebooks, documents, settings, …). Editing, deleting, and
executing stay dtctl's job; the TUI's answer to "now change it" is the
command echo.

## Consequences

- No safety-gated footer actions, no confirmation modals, no `$EDITOR`
  suspend handling — entire subsystems that now never need to exist.
- "Read-only exploration can be handed to anyone without tenant risk"
  (ADR-0006's promise) is now unconditional, not a property of the
  context's safety level.
- The safety level stays in the header — it is still the identity of the
  context the user is pointed at — but it gates nothing inside dtui.
- The `c` command-echo key is the mutation path: dtui teaches the CLI
  command, the user runs it where the safety model lives.
- If mutations are ever reconsidered, that is a new ADR reversing this one,
  not a Phase-4 backlog item.
