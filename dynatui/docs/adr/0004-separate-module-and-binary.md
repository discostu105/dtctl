# ADR-0004: dynatui is a separate Go module and binary; `dtctl tui` forwards

**Status:** Accepted · **Date:** 2026-07-08 (executed 2026-07-10)

## Context

dtctl (thin API wrapper for agents/CI) and the TUI (opinionated, fast-moving,
human-facing) are different products wearing one binary. dtctl's personas
should not carry bubbletea/lipgloss, and TUI feature churn should not drown
dtctl's tracker. A full repo split mid-churn would put a release boundary
exactly where iteration is fastest.

## Decision

Sequence the split behind real contracts. Now: dynatui is its own Go module and
binary **inside the dtctl repository** (`dynatui/`, module
`github.com/dynatrace-oss/dynatui`), consuming dtctl via `replace` directives.
`dtctl tui` stays working as a kubectl-style forwarder to the `dynatui`/
`dtctl-tui` binary on PATH. Later: split the repo when the seam proves stable
(signal: TUI PRs stop needing same-PR changes in `pkg/` or `sdk/`).

Full analysis: [DYNATUI_SPLIT_DESIGN.md](../../../docs/dev/DYNATUI_SPLIT_DESIGN.md)
in the dtctl repo.

## Consequences

- The dtctl root module stays TUI-free, enforced by `make dtctl-check-lean`;
  CI builds both modules so root-module changes keep dynatui compiling.
- dynatui gets its own User-Agent (`dynatui/<version>`) and its own state dir
  (`~/.local/state/dynatui/`).
- The split gave dtctl a general exec-plugin convention (`dtctl-<name>` on
  PATH); dynatui is its first plugin.
