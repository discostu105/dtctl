# dtui Split & Plugin System — Design Proposal

**Status:** Phase 1 executed 2026-07-10 — in-repo split: the TUI lives in its
own Go module and binary (`dtui/`, module `github.com/dynatrace-oss/dtui`),
`dtctl tui` forwards to the `dtui` binary on PATH, and the root module is
charmbracelet-free (guarded by `make dtctl-check-lean`). Contract hardening
executed 2026-07-12 — `DTCTL_CONTEXT` is a production env override in both
binaries, the config schema version is enforced on load, unknown fields
survive load-modify-save, and the contract is specified in
[CONFIG_CONTRACT.md](CONFIG_CONTRACT.md) with golden fixtures (Sequencing
steps 1-partial and 2 below). The sdk session-layer promotion, the plugin
dispatcher, and the repo split are still pending.
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

**dtctl owns config and context management entirely; dtui is a pure
consumer.** That ownership is consumed by *importing dtctl's Go packages*
(statically linked), never by shelling out to an installed dtctl binary — the
k9s/kubeconfig model (k9s does not require kubectl; it reads the same file via
client-go), not the lazygit/git model. "Install dtctl to create contexts" is
product positioning (a soft prerequisite in the docs), not a runtime
dependency: a container with a baked-in config file runs dtui alone.

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
  and `cmd/tui*.go`. (Since Phase 1 the code lives in `dtui/internal/tui/`
  plus the `dtui` main package.)
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
- **OAuth refresh locking exists but is bypassable** (corrected 2026-07-10;
  an earlier revision wrongly claimed no locking exists): refresh tokens
  rotate on use, and `pkg/auth/refresh_lock_unix.go` / `_windows.go` implement
  a cross-process refresh lock that `GetToken` holds. `TokenManager.RefreshToken`
  bypasses it by design — and the TUI's 401 forced-refresh path calls exactly
  that method (see Landmines).
- **`DTCTL_CONTEXT` exists only in tests** — there is no production env-var
  context override today (precedent for env config exists:
  `DTCTL_DISABLE_KEYRING`, `DTCTL_TOKEN_STORAGE`, `DTCTL_SPILL*`).
  ✅ **Resolved 2026-07-12**: `DTCTL_CONTEXT` is a production override in
  dtctl (`LoadConfig`) and dtui (flag > env > workspace match > file), always
  session-local. `DTCTL_OUTPUT`, documented in QUICK_START but previously
  dead (the viper bindings were never read), now works too.
- **Dispatch hook point exists**: `cmd/root.go` already intercepts
  unknown-command errors to add suggestions.

---

## Decision 1 — Split the TUI, but sequence it

The decision is not "one repo or two"; it is "what is the contract between
them". Split when the contract is real, not before.

**Recommended staging:**

1. **In-repo seam work** (now): promote the session layer into the sdk
   (Decision 2), harden the config contract (Landmines).
2. **Enforce the seam** — ✅ done 2026-07-10 (Phase 1): the TUI is its own Go
   module + binary inside the repo (`dtui/`), which yields the lean-dtctl and
   separate-versioning benefits immediately and reversibly. It compiles
   against the root module's packages (the pragmatic bridge below),
   charmbracelet, and its own packages; `dtctl tui` is a PATH forwarder and
   `make dtctl-check-lean` keeps the TUI stack out of the root module.
   Narrowing the import surface to the promoted sdk happens with step 1,
   off the critical path.
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

**Pragmatic bridge** (the Phase-1 mechanism): `pkg/` is not `internal/`, so
dtui can legally import `github.com/dynatrace-oss/dtctl/pkg/config` etc. from
the root module with zero refactoring — this is how the in-repo `dtui/` module
consumes dtctl today (via `replace` directives), and how a separate dtui repo
would consume the published root module. It takes the sdk promotion off the
split's critical path. Don't let it become permanent across a repo split: the
root module makes no API-stability promises, so promote the session layer
before (or with) the repo split.

## Decision 3 — Config & context: dtctl owns management entirely; dtui is a pure consumer

(Revised 2026-07-10: sharpened from "shared contract with dtui
standalone-capable" to full dtctl ownership — simpler, and what Phase 1
implements.)

Options considered:

| Option | Verdict | Why |
|---|---|---|
| dtui gets its own config/credentials | ❌ | Double onboarding, drifting contexts, two credential stores; kills "drop into the TUI wherever dtctl points" |
| dtctl as installed prerequisite (shell out) | ❌ | A TUI makes dozens of concurrent cancellable calls — process-spawn latency, output-parsing fragility, version skew; handing tokens across process boundaries is worse than in-process keyring reads. Note Go has no runtime code sharing: an installed dtctl binary can serve dtui **only** via shell-out, so "prerequisite" buys nothing architecturally |
| **dtctl owns all config/context management; dtui consumes it by importing dtctl's Go packages** | ✅ | k9s/kubeconfig model; existing users get dtui working instantly with zero migration; dtui ships no onboarding, no `ctx` CRUD, no config-write path — the whole two-writer config contract disappears |

Consequences (Phase-1 state in parentheses):

- dtui reads `~/.config/dtctl/config` and keyring service `"dtctl"` through
  dtctl's own packages (today `pkg/config`/`pkg/client` via the pragmatic
  bridge; later the promoted sdk package).
- **All context management lives in dtctl**: create/edit/delete, login flows,
  safety-level assignment, every write to the config file. dtui's first-run
  message with no contexts points at `dtctl ctx create`. "Install dtctl" is a
  soft prerequisite in the docs — dtui never checks for or invokes the binary.
- dtui may **switch** between existing contexts session-locally
  (implemented: `dtui --context` overrides in memory, never persists) — the
  same line k9s draws: switch in-app, create/edit elsewhere.
- dtui gets its **own namespace for UI-only state**: `~/.local/state/dtui/`
  (implemented: view history lives in `~/.local/state/dtui/history.json`,
  migrated on first run from `~/.local/state/dtctl/tui-history.json`). Auth
  and contexts never live there.
- The shared config file is **read-only for dtui**. The **token store is the
  one exception**: OAuth refresh tokens rotate on use, so a long-running dtui
  must persist refreshed token sets or it strands dtctl's stored copy —
  dtui is unavoidably a token-store *writer*, through the same locked refresh
  path dtctl uses (see Landmine 1).

## Landmines — harden before the split

These sit exactly on the shared boundary; fix them while everything is one
repo.

1. **OAuth forced-refresh bypasses the refresh lock.** (Corrected 2026-07-10:
   an earlier revision claimed no locking exists.) Refresh tokens rotate on
   use, and the codebase already guards concurrent refreshes with a
   cross-process file lock (`pkg/auth/refresh_lock_unix.go` / `_windows.go`)
   that `TokenManager.GetToken` holds around the refresh. But
   `TokenManager.RefreshToken` **bypasses that lock** — its own WARNING
   comment says concurrent callers risk `invalid_grant` from refresh-token
   rotation — and the TUI's forced-refresh-on-401 path
   (`RefreshedTokenForContext` in `pkg/client/oauth_support.go`) calls exactly
   that method. A long-running dtui racing a concurrent dtctl refresh on the
   same context can strand one side's credentials. This also settles Decision
   3's scope: dtui is read-only for the config file but necessarily a
   *writer* to the token store.
   ✅ **Fixed 2026-07-10**: `RefreshToken` now acquires the cross-process
   lock and re-reads the store under it — a refresh completed by another
   process while waiting is reused instead of double-spending the rotating
   refresh token; an unchanged store still always refreshes (the 401-retry
   contract). `GetToken` calls the extracted unlocked internal
   (`refreshTokenLocked`) since it already holds the lock. Covered by
   `pkg/auth/token_manager_refresh_lock_test.go`, including a
   rotation-faithful concurrent regression test against the real file lock.
2. **Current-context is shared mutable state.** Today `dtctl query --context X`
   *persists* the context switch to disk. If dtui inherits write-through
   semantics, an open TUI where the user hits `:ctx staging` silently repoints
   every script and agent using dtctl on that machine. dtui context switches
   must be **session-local**; the persisting `--context` behavior in dtctl
   itself deserves rethinking (see `DTCTL_CONTEXT` below).
   ✅ **Resolved 2026-07-12** (and the claim corrected: verified against
   current code, `--context` no longer persists — only `dtctl ctx <name>`
   writes the switch). `--context` and the new `DTCTL_CONTEXT` env override
   are session-local in both binaries; the rule is written into
   [CONFIG_CONTRACT.md](CONFIG_CONTRACT.md) §Write rules.
3. **Config schema versioning.** Before two independent readers exist: add a
   schema version field, tolerant parsing of unknown fields, and
   round-trip preservation on write.
   ✅ **Fixed 2026-07-12**: `apiVersion` is enforced on load (`""`, `v1`, and
   the `dtctl.io/v1` spelling from `dtctl config init` all mean v1; anything
   else is a hard error naming the version), unknown fields are ignored on
   load, and `SaveTo` grafts unknown keys back from the file being
   overwritten (`pkg/config/preserve.go`) so an older writer never destroys
   a newer writer's fields — deletions of known keys still stick.
4. **Write the contract down.** A short spec: file path, schema + version,
   keyring service name, OAuth store layout, write rules — with golden
   fixtures both repos test against.
   ✅ **Done 2026-07-12**: [CONFIG_CONTRACT.md](CONFIG_CONTRACT.md), enforced
   by `pkg/config/contract_test.go` against golden fixtures in
   `pkg/config/testdata/contract/`.
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
  independently valuable for CI/scripting and it is the natural plugin
  contract. ✅ Done 2026-07-12 (dtctl and dtui; session-local by contract).

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

1. **sdk session layer** (contexts, credentials + OAuth-store locking fix
   ✅ 2026-07-10, client-from-context, safety semantics) + `DTCTL_CONTEXT`
   production env override ✅ 2026-07-12. Keep aliases/hooks/spill CLI-side.
   The session-layer promotion itself is the remaining piece.
2. **Config contract hardening** — ✅ done 2026-07-12: schema version
   enforced, tolerant parsing + round-trip preservation of unknown fields,
   contract spec ([CONFIG_CONTRACT.md](CONFIG_CONTRACT.md)) + golden
   fixtures (`pkg/config/testdata/contract/`).
3. **Plugin dispatcher** + `dtctl plugin list` + conventions doc (one small
   PR; only after step 1).
4. **Enforce the seam** — ✅ done 2026-07-10 (Phase 1, pulled ahead of steps
   1–3 via the pragmatic bridge): own Go module + binary in-repo (`dtui/`),
   `dtctl tui` forwards, root module TUI-free. Narrowing dtui's imports from
   `pkg/*` to the promoted sdk lands with step 1.
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
4. **Does dtui ever write the shared config?** Resolved 2026-07-10: the
   config file — never (dtctl owns all management; no dtui onboarding is
   planned). The token store — necessarily yes, because OAuth refresh-token
   rotation forces refreshed sets to be persisted; writes go through the
   shared refresh lock (Landmine 1).

## References

- [CONFIG_CONTRACT.md](CONFIG_CONTRACT.md) — the config/state contract
  (Landmine 4), normative since 2026-07-12
- `dtui/docs/TUI_DESIGN.md` — the TUI design this proposal extracts
- `dtui/docs/TUI_LEARNINGS.md` — field notes; moved to dtui with the code
  2026-07-10 (as did `TUI_SMARTSCAPE_NAVIGATOR.md`)
- `docs/dev/ARCHITECTURE.md` — Phase-2 "Plugin System" line superseded here
- `docs/dev/context-safety-levels.md`, `pkg/safety/` — safety semantics that
  become part of the shared contract
- [k9s](https://k9scli.io/) — shared-config-without-prerequisite precedent
- [kubectl plugins](https://kubernetes.io/docs/tasks/extend-kubectl/kubectl-plugins/) — dispatch semantics adopted here
- [gh CLI extensions](https://cli.github.com/manual/gh_extension) — the "installer" model to consider *later*, if ever
