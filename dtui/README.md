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

### Workspace file (`.dynatrace.yaml`)

Commit a `.dynatrace.yaml` to a software project and every developer who runs
`dtui` inside that workspace (any subdirectory — the file is found by walking
up, like `.git`) lands in the right context automatically: the project's
filter segments pre-selected, the right environment, view, and timeframe.

```yaml
# .dynatrace.yaml — Dynatrace workspace defaults for this repository.
version: 1

# Pick the dtctl context whose environment URL matches (session-local;
# --context wins; no match = warning, your current context stays active).
environment: https://abc12345.apps.dynatrace.com

# Initial view (the CLI argument wins) and default timeframe (<n>m/<n>h/<n>d).
view: pods
timeframe: 2h

# Grail filter segments applied to every DQL-backed view (max 10,
# AND-combined). Reference by name or UID; names must match exactly
# (case-insensitive) — ambiguous or unknown names warn instead of guessing.
segments:
  - payments-prod
  - segment: 4lpVjcpcsjd
    variables:
      environment: [production]
```

Guarantees: the file carries **no credentials, contexts, or executable keys**
by construction — it can only narrow what a session shows, never change what
it can do. It merges on top of your personal dtctl config (unlike a local
`.dtctl.yaml`, which replaces it), is applied session-locally, has no
environment-variable expansion, and a broken file degrades to a startup
warning — never a failed launch. In the TUI, `S` opens the segment picker to
change or clear the selection at any time, and `alt+s` toggles the applied
set off and back on in place (selection and variable bindings kept) — handy
for comparing the workspace's scoped view against the whole tenant. Segments
with variables prompt for their values right in the picker (`v` edits
bindings later), so the `variables:` block above is a team default, not a
requirement.

## Documentation

- [docs/TUI_DESIGN.md](docs/TUI_DESIGN.md) — design: view catalog, navigation
  model, investigation flows, detail pages, architecture
- [docs/TUI_LEARNINGS.md](docs/TUI_LEARNINGS.md) — field notes and
  tenant-validated DQL facts
- [docs/TUI_SMARTSCAPE_NAVIGATOR.md](docs/TUI_SMARTSCAPE_NAVIGATOR.md) — the
  topology navigator (`:nav`)

Press `?` inside the TUI for the full key reference.
