# dtui

`dtui` is an interactive terminal UI for Dynatrace — a k9s-style navigator
over observability primitives: problems, services, hosts, Kubernetes, traces
with a span waterfall, logs with Davis pattern clustering, events, RUM, SLOs
with live evaluation, a Grail data explorer, and a Smartscape topology
navigator. Every view is a curated DQL query under the hood; drill-down keys
compose entity and timeframe scope automatically, and `ctrl+q` reveals the
generated query whenever curation runs out.

> **Status**: dtui currently lives inside the dtctl repository as its own Go
> module and binary, in preparation for becoming a separate project — see
> [DTUI_SPLIT_DESIGN.md](../docs/dev/DTUI_SPLIT_DESIGN.md).

## Install

From the dtctl repository root:

```bash
make install-dtui   # installs `dtui` into your Go bin dir
# or just build it: make build-dtui  →  bin/dtui
```

`dtctl tui` finds `dtctl-tui` or `dtui` on PATH and forwards to it, so the
familiar entry point keeps working.

## Configuration

dtui is a pure consumer of dtctl's configuration: it reads the same config
file (`~/.config/dtctl/config`) and credential store, and never writes them.
Create and manage contexts with dtctl:

```bash
dtctl ctx create   # then:
dtui               # home triage view on the current context
dtui pods          # jump straight into a view
dtui --context prod   # session-local override, never persisted
```

dtui keeps its own UI state (navigation history) under
`~/.local/state/dtui/`.

## Documentation

- [docs/TUI_DESIGN.md](docs/TUI_DESIGN.md) — design: view catalog, navigation
  model, investigation flows, detail pages, architecture
- [docs/TUI_LEARNINGS.md](docs/TUI_LEARNINGS.md) — field notes and
  tenant-validated DQL facts
- [docs/TUI_SMARTSCAPE_NAVIGATOR.md](docs/TUI_SMARTSCAPE_NAVIGATOR.md) — the
  topology navigator (`:nav`)

Press `?` inside the TUI for the full key reference.
