# ADR-0005: dtctl owns configuration; dynatui is a pure consumer

**Status:** Accepted · **Date:** 2026-07-08 (sharpened 2026-07-10)

## Context

Two tools over the same tenants must not mean double onboarding, drifting
contexts, or two credential stores. Shelling out to an installed dtctl was
rejected (process-spawn latency for dozens of concurrent calls, token
handoff across process boundaries); so was a dynatui-owned config.

## Decision

The k9s/kubeconfig model: dynatui reads dtctl's config file
(`~/.config/dtctl/config`) and keyring (service `"dtctl"`) by importing the
shared `sdk/session` packages — statically linked, never by invoking the
dtctl binary. All context management (create/edit/delete, login, safety
levels) lives in dtctl; dynatui may *switch* contexts session-locally
(`--context`, `DTCTL_CONTEXT`, `:ctx`) but never persists the switch.
UI-only state (view history) lives in dynatui's own namespace,
`~/.local/state/dynatui/`.

The one write exception: the OAuth token store. Refresh tokens rotate on
use, so a long-running dynatui must persist refreshed token sets — through the
same cross-process refresh lock dtctl uses.

## Consequences

- Existing dtctl users get dynatui working instantly; dynatui ships no
  onboarding, no `ctx` CRUD, no config-write path.
- "Install dtctl to create contexts" is product positioning, not a runtime
  dependency (a container with a baked-in config runs dynatui alone).
- The contract is normative in the dtctl repo's
  [CONFIG_CONTRACT.md](../../../docs/dev/CONFIG_CONTRACT.md), with golden
  fixtures both sides test against.
