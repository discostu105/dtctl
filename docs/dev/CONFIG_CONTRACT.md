# The dtctl Config Contract

**Status:** v1, normative since 2026-07-12
**Audience:** dtctl, dtui, and any future `dtctl-*` plugin that reads the
shared configuration. This is the state contract required by
[DTUI_SPLIT_DESIGN.md](DTUI_SPLIT_DESIGN.md) (Landmine 4): everything a
second binary may rely on, and nothing more. Golden fixtures live in
`pkg/config/testdata/contract/`; `pkg/config/contract_test.go` enforces this
document. A change that breaks those tests is a contract change and must
update this spec in the same PR.

## File locations

| Artifact | Path |
|---|---|
| Global config | `$XDG_CONFIG_HOME/dtctl/config` (typically `~/.config/dtctl/config`) |
| Project-local config | `.dtctl.yaml`, discovered upward from the working directory; used **exclusively** (never merged with the global file) |
| Explicit config | `--config <path>` flag, wins over discovery |
| OAuth file store | `$XDG_DATA_HOME/dtctl/oauth-tokens/<sanitized-name>.json`, mode 0600 (dir 0700) |
| Token-refresh lock | `$TMPDIR/dtctl-token-refresh-<sha256[:8] of env:tokenRef>.lock` |

Security note: code-execution keys (aliases, apply hooks) in an
auto-discovered `.dtctl.yaml` are loaded for round-tripping but **never
honored** — see `Config.IsLocal()`.

## Schema (v1)

YAML document. Top-level keys: `apiVersion`, `kind`, `current-context`,
`contexts` (list of `{name, context}`), `tokens` (list of `{name, token}`),
`preferences`, `aliases`, `spill`. Per-context keys: `environment`,
`token-ref`, `safety-level` (`readonly` | `readwrite-mine` | `readwrite-all` |
`dangerously-unrestricted`; empty means `readwrite-all`), `description`,
`hooks`, `spill`. The Go structs in `pkg/config/config.go` are the schema's
source of truth; `testdata/contract/v1-full.yaml` exercises every field.

Semantics both binaries must share: `safety-level` (a `readonly` context means
the same thing everywhere) and token resolution order (see below).

### Version policy

- `apiVersion` spellings accepted as schema v1: **empty** (pre-enforcement
  configs), **`v1`**, and **`dtctl.io/v1`** (written by `dtctl config init`).
- An unrecognized `apiVersion` is a **hard load error** naming the version —
  never a silent misread. This is the version-skew answer: "dtui N supports
  config schema ≤ M" is testable.
- Within v1 the schema evolves **additively only**. Renaming or redefining an
  existing key requires bumping the version.

### Tolerant parsing and round-trip preservation

- Readers ignore unknown keys (yaml.v3 default — do not enable strict mode).
- Writers must not destroy unknown keys: `Config.SaveTo` grafts keys unknown
  to the running build from the file being overwritten back into the saved
  document (top level, per-context and per-token matched by `name`, and
  nested structs — see `pkg/config/preserve.go`). Known keys are owned by the
  writer: deleted contexts and cleared `omitempty` fields stay deleted.
- Comments and key order are **not** preserved; only data survives.

### Environment variable expansion

`$VAR` / `${VAR}` in the file expand from the process environment at load.
Shell positional/special parameters (`$1`, `${10}`, `$@`, …) are preserved
verbatim so hook commands survive. Unset variables expand to the empty
string. Management commands that rewrite the file must load with
`LoadWithoutExpansion` so templates round-trip unexpanded.

## Credential store

- **OS keyring service name: `dtctl`** — shared by every consumer; changing
  it strands all stored credentials.
- Key formats: plain API tokens under their token-ref name; OAuth token sets
  (JSON) under `oauth:<env>:<tokenRef>` with `<env>` ∈ `prod` | `dev` |
  `hard`, legacy entries under `oauth:<tokenRef>`.
- Token resolution order (in `Config.GetToken`): keyring OAuth entry →
  keyring plain token → OAuth file store (when the keyring is unavailable or
  `DTCTL_TOKEN_STORAGE=file`) → inline `token` value in the config file.
- `DTCTL_DISABLE_KEYRING` (any non-empty value) disables the keyring;
  `DTCTL_TOKEN_STORAGE=file` forces the file store.

## Write rules

1. **dtctl owns all config-file writes** — context CRUD, login flows,
   safety-level assignment. dtui and plugins treat the config file as
   **read-only**.
2. **The token store is the one shared write surface.** OAuth refresh tokens
   rotate on use, so any long-running consumer must persist refreshed token
   sets — and must do so through the cross-process refresh lock
   (`pkg/auth`, `TokenManager`), never with an unlocked read-modify-write.
   Concurrent unlocked refreshes double-spend the rotating refresh token and
   strand one side's credentials (`invalid_grant`).
3. **Context overrides are session-local.** The `--context` flag (dtctl and
   dtui) and the `DTCTL_CONTEXT` env var override the current context in
   memory only. The sole way to persist a switch is `dtctl ctx <name>`
   (or `dtctl config use-context`).

## Environment variable overrides

| Variable | Meaning |
|---|---|
| `DTCTL_CONTEXT` | Session-local current-context override; flag `--context` wins over it. Honored by dtctl and dtui. |
| `DTCTL_OUTPUT` | Default output format when `-o/--output` is not given (dtctl only). |
| `DTCTL_DISABLE_KEYRING` | Disable the OS keyring (any non-empty value). |
| `DTCTL_TOKEN_STORAGE` | `file` forces the file-based OAuth store. |

## Golden fixtures

| Fixture | Asserts |
|---|---|
| `v1-full.yaml` | Every known field parses; unknown fields at all levels are tolerated and survive a load-modify-save cycle |
| `v1-minimal.yaml` | Minimal config loads; `apiVersion` is optional |
| `future-version.yaml` | Unsupported schema version fails loudly |

When the repo split happens, the dtui repo vendors these fixtures (or the
published `pkg/config` test module) and runs the same assertions — the
fixtures are the compatibility test between independently released binaries.
