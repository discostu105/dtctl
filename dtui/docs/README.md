# dtui documentation

User-facing docs (install, configuration, the workspace file) live in the
top-level [README](../README.md); press `?` inside the TUI for the key
reference. This directory is for design and contributor documentation.

## Layout

- **[design/](design/)** — what dtui is and how it's built
  - [tui.md](design/tui.md) — the core design: view catalog, navigation
    model, detail pages, architecture
  - [smartscape-navigator.md](design/smartscape-navigator.md) — the
    topology navigator (`:nav`)
- **[adr/](adr/)** — architecture decision records: the load-bearing
  decisions and why they were made
- **[dev/](dev/)** — contributor notes
  - [learnings.md](dev/learnings.md) — field notes: live-validated
    DQL/Grail facts, the view extension model, bubbletea patterns, how to
    verify the TUI
  - [phases.md](dev/phases.md) — the phase-by-phase shipped log
  - [design-gaps.md](dev/design-gaps.md) — where the design docs and the
    code still disagree, and which side should move

The split from the dtctl repository is designed in dtctl's
[DTUI_SPLIT_DESIGN.md](../../docs/dev/DTUI_SPLIT_DESIGN.md); the shared
config/credential contract is dtctl's
[CONFIG_CONTRACT.md](../../docs/dev/CONFIG_CONTRACT.md).
