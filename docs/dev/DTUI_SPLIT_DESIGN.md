# dtui Split & Plugin System — Design Proposal

**Status:** Proposal — decision pending
**Created:** 2026-07-08
**Author:** dtctl team

## Summary

Split the TUI out of dtctl into a separate project (working name **dtui**) at the
product level — its own repo, binary, release cadence, and issue tracker — but
**sequence the split behind two contracts that must become real first**:

1. **The sdk Go module** as the code seam (a promoted "session layer":
   contexts, credentials, client construction, safety semantics).
2. **The config file + OS keyring** as the state contract (shared, versioned,
   documented).

dtctl is **not** a runtime prerequisite for dtui. dtui reads the same config
and keyring through the same sdk library — the k9s/kubeconfig model (k9s does
not require kubectl; it reads the same file via client-go), not the
lazygit/git model (shelling out to an installed binary).

Additionally, adopt a **lean kubectl-style exec plugin convention** for dtctl.
dtui ships as its first plugin (`dtctl-tui`), which preserves the `dtctl tui`
UX across the split and gives the core project a standing scope-defense
mechanism ("great idea — ship it as a plugin"). No plugin registry, no
installer, no gRPC — the exec convention only.

---

## Motivation

dtctl and the TUI are different products wearing one binary:

| | dtctl (core) | TUI |
|---|---|---|
| Primary personas | AI agents, CI/CD, scripting; occasionally devs | Humans, interactive triage |
| Domain logic | Deliberately thin API wrapper; no opinionated DQL | Heavy: curated DQL, entity semantics, dual semconv eras, GenAI rendering |
| Feature pressure | Low; API-surface-driven | High; opinionated UX requests |
| Change velocity | Stable | Rapid (phases 1–3.6 shipped in days) |
| Maintenance profile | Low | High |

Further arguments for the split:

- **Binary hygiene**: dtctl's primary personas install it in containers and CI;
  they should not carry bubbletea/lipgloss.
- **Issue triage**: "add a column to the pods view" requests stop drowning
  dtctl's tracker.
- **Shipping vehicle**: the TUI currently lives on a fork branch. If upstream
  does not want ~17k lines of TUI, a separate project is how it ships at all.

## Current State (measured 2026-07-08)

These measurements ground the plan; re-verify before executing.

- **TUI size**: ~12k LOC excluding tests (~17k including) across `pkg/tui/`
  and `cmd/tui*.go`.
- **Coupling surface is already thin** (by design — see TUI_DESIGN.md
  "Reuse, don't fork" via narrow adapters in `datasource.go`). Outside its own
  packages the TUI imports only:
  - `pkg/exec` (DQL execution; itself delegates polling to `sdk/api/query`)
  - `pkg/client` (client-from-context, OAuth refresh, 401-retry)
  - `pkg/config` (contexts, keyring, safety levels)
  - `pkg/output` (sparkline/braille/chart renderers)
  - `pkg/resources/{slo,anomalydetector,analyzer}` (API-backed views)
  - charmbracelet (only third-party dependency)
- **The sdk has never been consumed as a real dependency**: the root `go.mod`
  uses `replace github.com/dynatrace-oss/dtctl/sdk => ./sdk` with a zero
  pseudo-version, and no `sdk/v*` tags exist. dtui would be its **first true
  external consumer**.
- **`pkg/config` is cleanly promotable**: ~4.3k lines (incl. tests), depends
  only on xdg/yaml/keyring — no cobra. It mixes shareable concerns (contexts,
  token resolution, keyring, OAuth file store, XDG paths, safety levels) with
  CLI-only concerns (command aliases, pre/post-apply hooks, spill config).
- **Keyring service name** is hardcoded `"dtctl"`
  (`pkg/config/keyring.go`) — this is the de-facto credential contract.
- **`pkg/config/oauth_file_store.go` has no file locking or atomic-rename
  discipline** (needs verification + hardening — see Landmines).
- **`DTCTL_CONTEXT` exists only in tests** — there is no production env-var
  context override today (precedent for env config exists:
  `DTCTL_DISABLE_KEYRING`, `DTCTL_TOKEN_STORAGE`, `DTCTL_SPILL*`).
- **Dispatch hook point exists**: `cmd/root.go` already intercepts
  unknown-command errors to add suggestions.

---

## Decision 1 — Split the TUI, but sequence it

The decision is not "one repo or two"; it is "what is the contract between
them". Split when the contract is real, not before.

**Recommended staging:**

1. **In-repo seam work** (now): promote the session layer into the sdk
   (Decision 2), harden the config contract (Landmines).
2. **Enforce the seam**: `pkg/tui` + `cmd/tui` compile against **only** the
   sdk, charmbracelet, and their own packages. Optionally make the TUI its own
   Go module + binary inside the repo — this yields the lean-dtctl and
   separate-versioning benefits immediately and reversibly.
3. **Split the repo** when the seam proves stable. The measurable signal:
   **TUI feature PRs stop needing same-PR changes in `pkg/` or `sdk/`.**
   (Counter-example from Phase 3.6: the 401-retry landed in `pkg/client` for
   the TUI's sake. Splitting repos mid-churn puts a release boundary exactly
   where iteration is fastest.)
4. Extract via `git filter-repo`; `dtctl tui` becomes a forwarder (Decision 4).

## Decision 2 — Code sharing: promote a session layer into the sdk

**Moves into the sdk** (new package, e.g. `sdk/session` or `sdk/config`):

- Context/config model: load/save, context resolution, environment URLs
  (`sdk/urls` already exists), token references.
- Credential resolution: keyring (service `"dtctl"`), file fallback, OAuth
  store — `sdk/credstore` already covers part of this.
- **Client construction from a context**: auth resolution, OAuth refresh,
  401-retry, rate limiting, pagination (today split across `pkg/client` and
  `sdk/httpclient`).
- **Safety-level semantics.** If a `readonly` context must mean the same thing
  in both tools (TUI_DESIGN.md says it does), safety is part of the shared
  contract, not a CLI feature.

**Stays out of the sdk:**

- CLI-only config: command aliases, pre/post-apply hooks, spill config.
- **Terminal renderers** (sparkline/braille/chart) — the "no display logic in
  the sdk" rule is correct. Options: extract a tiny standalone terminal-viz
  module with zero Dynatrace coupling, or let dtui fork them and diverge
  toward lipgloss-native implementations (TUI_DESIGN.md already flags the
  two-styling-systems tension; divergence may be healthy).
- Resource display-field metadata; the TUI's ViewSpec catalog is dtui-domain.

**Pragmatic bridge** (know it exists, don't let it become permanent): `pkg/`
is not `internal/`, so a separate dtui repo can legally import
`github.com/dynatrace-oss/dtctl/pkg/config` etc. from the published root
module today, with zero refactoring. Acceptable while the sdk promotion lands
(e.g. if upstream review is slow); the root module makes no API-stability
promises.

## Decision 3 — Config & context: shared state, shared library, independent binaries

Options considered:

| Option | Verdict | Why |
|---|---|---|
| dtui gets its own config/credentials | ❌ | Double onboarding, drifting contexts, two credential stores; kills "drop into the TUI wherever dtctl points" |
| dtctl as installed prerequisite (shell out) | ❌ | A TUI makes dozens of concurrent cancellable calls — process-spawn latency, output-parsing fragility, version skew; handing tokens across process boundaries is worse than in-process keyring reads |
| **Shared config file + keyring as a versioned contract; reader in the sdk** | ✅ | k9s/kubeconfig model; existing users get dtui working instantly with zero migration |

Consequences:

- dtui reads `~/.config/dtctl/config` and keyring service `"dtctl"` via the
  promoted sdk package.
- dtui stays **standalone-capable**: on first run with no config, offer
  minimal guided context creation (same library, same file format) or point at
  dtctl. v1 can just point.
- dtui gets its **own namespace for UI-only state**: `~/.config/dtui/`,
  `~/.local/state/dtui/` — theme, hotkeys, view history (the current
  `~/.local/state/dtctl/tui-history.json` migrates there). Auth and contexts
  never live there.
- dtui treats the shared config as **read-only in v1** (reload on change);
  this avoids needing a two-writer contract on day one.

## Landmines — harden before the split

These sit exactly on the shared boundary; fix them while everything is one
repo.

1. **OAuth refresh races.** The OAuth file store has no file locking or
   atomic-rename. A long-running dtui plus concurrent dtctl invocations can
   refresh the same token concurrently; if refresh tokens rotate on use, one
   process strands the other's credentials. Required: file locking, atomic
   writes (write-temp + rename), re-read-before-refresh single-flight. **This
   is the sharpest technical risk of the whole plan.**
2. **Current-context is shared mutable state.** Today `dtctl query --context X`
   *persists* the context switch to disk. If dtui inherits write-through
   semantics, an open TUI where the user hits `:ctx staging` silently repoints
   every script and agent using dtctl on that machine. dtui context switches
   must be **session-local**; the persisting `--context` behavior in dtctl
   itself deserves rethinking (see `DTCTL_CONTEXT` below).
3. **Config schema versioning.** Before two independent readers exist: add a
   schema version field, tolerant parsing of unknown fields, and
   round-trip preservation on write.
4. **Write the contract down.** A short spec: file path, schema + version,
   keyring service name, OAuth store layout, write rules — with golden
   fixtures both repos test against.
5. **Client identity.** dtui needs its own `User-Agent` (`pkg/version` is
   dtctl-branded); parameterize the app name in the sdk client.
6. **macOS keychain UX**: a second binary means a second keychain-access
   prompt. Expected; document it.

---

## Decision 4 — Plugin system: lean, exec-style, honest scope

> Supersedes the `ARCHITECTURE.md` Phase-2 line "Plugin System (using Go
> plugins or exec-based)" and the `API_DESIGN.md` future-idea line: the answer
> is **exec-based**; Go plugins are rejected (below).

**Verdict:** build the kubectl/git-style exec convention — `dtctl foo` →
find `dtctl-foo` on `PATH` → exec — and nothing heavier. Honest framing: if
the dtui split were not happening, plugins would be premature (third-party
demand today is ~zero; a framework with no plugins is pure liability). What
changes the math: the expensive part (the sdk session layer + config contract)
is already required for dtui, and the dispatcher converts the dtui migration
from "deprecation stub" into a general mechanism.

**The dispatcher is not the cost; the contract is.** Do not ship the
dispatcher before the sdk session layer exists — plugins that cannot
authenticate are useless.

### What it buys

1. **The dtui shipping vehicle**: dtui ships from its own repo as `dtctl-tui`
   (packaging alias `dtui`); `dtctl tui` keeps working via dispatch.
2. **Scope defense**: a standing, non-hostile answer to opinionated feature
   requests — "that's a plugin". kubectl uses this valve constantly; for a
   thin-API-wrapper project it is a governance tool.
3. **Org-internal namespace**: `dtctl-acme-onboard` without forks.

### What it does not buy — say it in the docs

- **No ecosystem is coming soon.** Success metric: "dtui ships cleanly and
  feature requests get deflected", not plugin count.
- **Safety becomes convention, not enforcement.** A plugin can mutate a tenant
  whose context is `readonly`. Same stance as kubectl, but dtctl *advertises*
  safety levels, so the gap is more visible. Mitigation: export the safety
  checker in the sdk, mandate it in the author guide, accept the honor system.
- **Agent-envelope fragmentation.** Plugins won't emit the `--agent` envelope
  unless they use sdk helpers — and agents are the primary persona. Provide
  envelope/output helpers in the sdk; make `dtctl commands` (the agent
  bootstrap catalog) list discovered plugins, otherwise agents can't see them.
- **Misdirected support burden**: plugin bugs filed as dtctl bugs.
  `dtctl plugin list` naming binary + path helps triage.

### Specification (v1)

**Dispatch** (kubectl semantics):

- On unknown first argument, search `PATH` for `dtctl-<name>` using the
  longest dash-joined match: `dtctl foo bar baz` tries `dtctl-foo-bar-baz`,
  then `dtctl-foo-bar` (arg `baz`), then `dtctl-foo` (args `bar baz`).
- Exec with remaining args and exit code passed through verbatim. Windows:
  `.exe` suffix.
- **Built-in commands always win** — plugins cannot shadow core names.
  Implementation hook: the existing unknown-command error path in
  `cmd/root.go` (before the suggestion enhancer).

**Contract to plugins — env vars, never secrets:**

| Variable | Meaning |
|---|---|
| `DTCTL_CONTEXT` | Context-name override (reflects `--context` if given) |
| `DTCTL_CONFIG` | Config file path in effect |
| `DTCTL_AGENT=1` | Agent mode active — emit the JSON envelope |
| `DTCTL_PLAIN=1` | `--plain` in effect — no color, no prompts |
| `DTCTL_CALLER_VERSION` | dtctl version, for compat decisions |

- **No tokens in env or argv.** Plugins resolve credentials themselves via the
  sdk (same keyring service). Passing secrets through the environment is the
  one design mistake that is hard to walk back.
- Prerequisite worth doing regardless: make `DTCTL_CONTEXT` a **real
  production env override** (today it exists only in tests). It is
  independently valuable for CI/scripting, it is the natural plugin contract,
  and it fixes the `--context`-persists-to-disk trap for one-shot invocations.

**Management surface (v1, complete):**

- `dtctl plugin list` — scan `PATH`, print name/binary/path, warn on
  attempted shadowing of core commands. Nothing else.
- `dtctl commands` includes discovered plugins in the machine-readable
  catalog.

**Author kit** = the sdk being built anyway (session-from-context, safety
checker, envelope/output helpers) plus a two-page conventions doc: flag
conventions (`-o json`, `--plain`), `NO_COLOR`, exit codes, safety, envelope.

**Migration gotcha:** built-ins shadow plugins by design, so the existing
built-in `tui` command must become a thin forwarder (exec `dtctl-tui` if
found, else print install instructions) — or be removed in the release where
dtui ships.

### Non-goals — explicitly rejected

- **Go's native `plugin` package** (`.so` loading): no Windows, exact
  toolchain lock-in, CGO pain. Dead end for distributed CLIs.
- **hashicorp/go-plugin (gRPC)**: built for long-lived bidirectional provider
  processes (Terraform/Vault); wild overkill for CLI subcommands.
- **A krew clone / `dtctl plugin install` / registry**: defer until real
  third-party plugins exist (rule of thumb: ≥5). An installer imports
  supply-chain and trust questions an ecosystem of one does not need.
- **Embedded scripting (Lua/Starlark/WASM)**: a different problem. Note:
  k9s-style user-customizable views (YAML ViewSpecs) is a **dtui** feature on
  dtui's roadmap — do not conflate TUI extensibility with CLI plugins.

---

## Sequencing

1. **sdk session layer** (contexts, credentials + OAuth-store locking fix,
   client-from-context, safety semantics) + `DTCTL_CONTEXT` production env
   override. Keep aliases/hooks/spill CLI-side.
2. **Config contract hardening**: schema version, tolerant parsing, contract
   spec + golden fixtures.
3. **Plugin dispatcher** + `dtctl plugin list` + conventions doc (one small
   PR; only after step 1).
4. **Enforce the seam**: TUI compiles against sdk + charmbracelet + own
   packages only; optionally own Go module + binary in-repo.
5. **Split** when TUI PRs stop touching `pkg/`/`sdk/` in the same change:
   `git filter-repo` → dtui repo; `dtctl tui` becomes the forwarder; dtui is
   the first plugin and the contract dogfood.
6. **Invest further only on evidence**: installer/registry/scaffolding when
   actual external plugins appear.

## Risks & honest expectations

- **Maintainer math**: two repos = two CI pipelines, release flows
  (release-please + `sdk/v*` tagging discipline — the sdk gets its first real
  consumer), security-bump streams, doc sites. If this is mostly one
  maintainer, the in-repo separate-binary stage may last a long time — that is
  fine and intended.
- **Version skew**: users will run dtctl X + dtui Y. The config schema version
  plus a documented compat statement ("dtui N supports config schema ≤ M") is
  the answer; test it with the shared golden fixtures.
- **Ecosystem may never materialize**: acceptable; the plugin mechanism is
  cheap and pays for itself via dtui + scope defense alone.

## Open Questions

1. **Upstream relationship**: is the TUI (and the sdk session-layer
   refactoring) intended for dynatrace-oss upstream, or is dtui an independent
   project consuming the published sdk? If upstream stalls, the root-module
   import bridge (Decision 2) is the interim path.
2. **Naming**: check "dtui" for collisions (registries, GitHub, brew) before
   branding.
3. **Renderer strategy**: extract a standalone terminal-viz module vs fork
   into dtui and let implementations diverge.
4. **Does dtui ever write the shared config?** v1: no (read-only + reload).
   Revisit when dtui grows guided onboarding.

## References

- `docs/dev/TUI_DESIGN.md` — the TUI design this proposal extracts
- `docs/dev/TUI_LEARNINGS.md` — field notes; moves to dtui with the code
- `docs/dev/ARCHITECTURE.md` — Phase-2 "Plugin System" line superseded here
- `docs/dev/context-safety-levels.md`, `pkg/safety/` — safety semantics that
  become part of the shared contract
- [k9s](https://k9scli.io/) — shared-config-without-prerequisite precedent
- [kubectl plugins](https://kubernetes.io/docs/tasks/extend-kubectl/kubectl-plugins/) — dispatch semantics adopted here
- [gh CLI extensions](https://cli.github.com/manual/gh_extension) — the "installer" model to consider *later*, if ever
