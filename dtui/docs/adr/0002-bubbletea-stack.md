# ADR-0002: bubbletea + lipgloss as the UI stack

**Status:** Accepted · **Date:** 2026-07-05

## Context

dtctl already had a hand-rolled ANSI live-rendering layer (`pkg/output`:
progress, watch, sparklines, braille charts). The TUI needs input handling,
focus management, and screen compositing on top.

## Decision

Use **bubbletea + bubbles + lipgloss** (charmbracelet). Elm-style
`Model/Update/View` fits "views over shared app state"; async API results
arrive as `tea.Msg`s. Rejected: tview/tcell (heavier, imperative) and
growing the hand-rolled ANSI layer into a framework (input, focus, and
compositing are exactly what a framework should own).

## Consequences

- Two styling systems coexist: raw ANSI in dtctl's CLI output, lipgloss in
  dtui. The chart renderers emit plain strings and embed in either; the
  `internal/tui/theme` adapter keeps palettes and capability detection
  consistent.
- charmbracelet dependencies live in the dtui module only; dtctl's root
  module and the SDK stay TUI-free (`make dtctl-check-lean`, ADR-0004).
- bubbletea models are pure, so navigation/scope logic tests need no TTY.
- lipgloss v1 cannot composite overlays — overlays replace the body, and
  border titles are hand-rolled (see [../dev/learnings.md](../dev/learnings.md) §3b).
