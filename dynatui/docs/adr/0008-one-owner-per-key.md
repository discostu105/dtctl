# ADR-0008: One owner per key; digits follow the visible numbering

**Status:** Accepted · **Date:** 2026-07-10 (refined 2026-07-11)

## Context

Keys accreted meanings per context: digits meant five different things
(lens, tab, hotkey, picker), `tab` three. Users could not predict what a
key would do without knowing which view they were in — invisible key scope.

## Decision

Every key has exactly one owner, matching the visual hierarchy: `[`/`]`
drive the lens strip and only the lens strip; `tab` cycles the view's
primary strip; drill letters always mean their signal. For digits the rule
is visibility-based: **digits do what the numbers on screen say; no numbers
visible → global bookmarks.** Exactly one strip on screen is numbered — the
innermost — and entering a page moves the numbering (and the digits) to its
tab bar, or down to an active tab's lens strip. A digit the strip doesn't
show is swallowed with a teaching status, never a hidden jump; `0` is
always jump-home; `esc` pops out to where all ten digits are global again.

## Consequences

- Key behavior is predictable from what is on screen, not from mode
  memory; the UI teaches its own bindings.
- Lens strips and tab bars render digit labels; top-level tables, home,
  and the navigator show none, so bookmarks work where users roam.
- Design history: phases 3.9 → 3.10 in [../dev/phases.md](../dev/phases.md)
  (3.10's visibility rule supersedes 3.9's "digits are always global").
