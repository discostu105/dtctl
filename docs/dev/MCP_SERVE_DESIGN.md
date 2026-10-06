# `dtctl serve mcp` — MCP Protocol Adapter Design

**Status:** Proposed
**Created:** 2026-10-06
**Audience:** anyone adding a protocol under `dtctl serve`, or deciding what dtctl should look like to an MCP client.

> **Builds on:** [SERVICE_ENGINE_DESIGN.md](SERVICE_ENGINE_DESIGN.md) (the engine
> and the `serve` parent command), [COMMAND_PROFILES_DESIGN.md](COMMAND_PROFILES_DESIGN.md)
> (the surface axis), [context-safety-levels.md](context-safety-levels.md) (the
> permission axis), [GENERIC_API_ACCESS.md](GENERIC_API_ACCESS.md) (`exec api`).
> `pkg/serve/serve.go` already reserves the slot: "room for `dtctl serve mcp`
> and others without redefining what bare `serve` means."

## Overview

`dtctl serve mcp` runs dtctl as a [Model Context Protocol](https://modelcontextprotocol.io)
server. It is the second protocol under `dtctl serve`, beside `serve http`, and
like it a thin adapter over `pkg/engine`: an MCP tool call carries one dtctl
command line, and the tool result carries what the CLI would have printed.

The point is the same as for `serve http` — **the CLI is the API**. There is no
second catalog of typed tools to keep in sync with the command tree. An MCP
client gets two tools: one that runs a dtctl command line and one that returns
the machine-readable command catalog the CLI already ships for agents.
Everything dtctl can do, including the spec-governed `exec api` passthrough to
any Dynatrace API, is reachable through them, under exactly the restriction
axes the engine already enforces per request.

```text
MCP client ──tools/call dtctl {command:"query 'fetch logs | limit 5'"}──▶ serve mcp
                                                                              │ engine.Execute
                                                                              ▼
                                                                    cmd tree (--agent appended)
                                                                              │ stdout = agent envelope
          ◀──────────────── content[0].text = stdout (byte-identical) ────────┘
```

### Why now

- **The local open-source Dynatrace MCP server is deprecated.** Its final
  release (2.1.2, 2026-07) points local-IDE users to "Dynatrace-for-AI skills
  plus dtctl" and remote/agent-to-agent users to the hosted Remote MCP Server.
- **The hosted Remote MCP Server is read-only.** It exposes ~20 Davis analysis
  tools (problems, forecasting, anomaly detectors, changepoints, docs Q&A,
  NL↔DQL) and no write tools — no dashboards, notebooks, workflows, settings.
  Its own migration notes recommend pairing it with dtctl for management tasks.
- **Clients without a shell lost their only option.** Claude Desktop without a
  terminal, IDE chat panes, enterprise agent platforms that forbid subprocesses:
  they could run the deprecated local server and cannot run a CLI. For them MCP
  is the only transport, and dtctl's operational surface is exactly what the
  remote server lacks.
- **The engine made the adapter cheap.** `pkg/engine` already provides
  per-request isolation, the five restriction axes, admission control, output
  budgets, and byte-identical CLI output. `serve http` proves the adapter shape
  in ~300 lines. `serve mcp` is the same shape on a different wire.

### What this is not

Coding agents that *have* a shell (Claude Code, Cursor, Codex CLI, Copilot) are
better served by the CLI plus the bundled skill, and the docs must keep saying
so. Every MCP tool definition costs context on every turn; a CLI costs nothing
until it is called, and `dtctl commands` gives the agent progressive disclosure
for free. `serve mcp` is for hosts that cannot exec, not a replacement for the
primary persona.

## Goals

1. **One command surface.** The MCP tool set is derived from the command tree,
   never hand-maintained beside it. Adding a dtctl command adds nothing to
   `pkg/serve/mcp.go`.
2. **Tiny tool list.** Two native tools. The guidance from every major MCP
   server author in 2025–2026 (Anthropic, GitHub, AWS, Cloudflare, the k8s
   servers) converges on the same point: tool definitions are the context
   bloat, and a generic executor plus a catalog beats one tool per endpoint.
3. **The engine's axes, unchanged.** Safety level, profile, stability floor,
   environment mask and capabilities apply exactly as they do to `serve http`.
   No MCP-specific "read-only mode" or "toolsets" — those words already have
   owners (safety levels and profiles).
4. **Host-credentialed local mode.** A stdio server launched by a desktop
   client runs as the user's pinned dtctl context — keyring, OAuth refresh and
   all — because that is the user's expectation and the only auth that works
   without a token in a config file.
5. **Writes are gated, visibly.** A mutating command line asks the human first
   (MCP elicitation) unless the operator opted out at server start, and the
   result makes clear what changed.
6. **Room for the hosted server.** A later phase can pass the Remote MCP
   Server's Davis analysis tools through the same process, so a client needs
   one server entry, not two.

## Non-Goals

- **Typed per-resource tools** (`dtctl_get_workflows`, `dtctl_create_slo`, …).
  See [Design decisions](#design-decisions). The engine design forbids "a
  second, parallel API surface that drifts from the CLI"; a typed tool catalog
  is that surface.
- **A replacement for the skill.** The DQL reference and the howto are exposed
  as MCP resources for clients that cannot install skills; the skill remains
  the primary knowledge channel.
- **A security boundary.** Same posture as `serve http`: a correctness boundary
  inside one process, no authentication of its own on the HTTP transport,
  untrusted callers belong behind a process boundary. Scope the Dynatrace token.
- **Streaming tool results.** `--watch`, `--follow` and `--live` stay refused
  (`capability_disabled`), as in `serve http`. MCP tools are request/response.
- **Being the remote server.** Phase 2 proxies the hosted server's tools; it
  does not reimplement Davis analyzers natively.

## Landscape (facts the design rests on)

Verified in October 2026 against primary sources; items marked *(unverified)*
could not be confirmed and must be re-checked before phase 2 ships.

| Fact | Consequence |
|---|---|
| `dynatrace-oss/dynatrace-mcp` deprecated at 2.1.2; 18 tools incl. writes (Slack, email, event, notebook) with elicitation gates; per-session Grail scan budget | There is precedent for elicitation on writes and for a scan budget; both carry over. |
| Hosted server: `https://{env}.apps.dynatrace.com/platform-reserved/mcp-gateway/v0.1/servers/dynatrace-mcp/mcp`, streamable HTTP, bearer = platform token or confidential OAuth client; needs `mcp-gateway:servers:invoke` + `mcp-gateway:servers:read` plus Davis/storage scopes; no public clients, no dynamic client registration | A proxy must bring its own token; dtctl's context already holds one. Scope tables need the two gateway scopes. |
| Hosted server tool names are `v0.1`, several Early Access/Preview, no writes, `execute-dql` capped at 1000 records | Discover remote tools at startup via `tools/list`; never hard-code names. Native `query` supersedes `execute-dql`. |
| Whether customers can register their own servers in the gateway *(unverified)*; any Grail budget on the hosted server *(unverified)* | Not relied on. |
| Anthropic tool-design guidance: fewer, workflow-level tools; `defer_loading`/tool search kicks in at ~10 tools or ~10K tokens of definitions; Claude Code caps a tool result at 25K tokens by default | Two tools stay well under every threshold; results must be budgeted (they are — agent mode already pages lists to 50 and bounds inline query output). |
| GitHub removed server-side dynamic toolsets (2026-05) in favour of client-side deferral; AWS and Cloudflare moved to single `execute`-style tools over whole APIs | Generic executor is the direction of the field, not a shortcut. |

## User experience

### Starting the server

```bash
dtctl config set development.serve on           # same opt-in as serve http
dtctl serve mcp                                 # stdio, current context
dtctl serve mcp --context prod-agent            # stdio, pinned context
dtctl serve mcp --transport http --addr 127.0.0.1:7212 --environment-url https://abc12345.apps.dynatrace.com
```

A client config entry (Claude Desktop, Cursor, VS Code — all share the shape):

```json
{
  "mcpServers": {
    "dtctl": {
      "command": "dtctl",
      "args": ["serve", "mcp", "--context", "prod-agent"],
      "env": { "DTCTL_DEVELOPMENT": "serve" }
    }
  }
}
```

| Flag | Default | Meaning |
|---|---|---|
| `--transport` | `stdio` | `stdio` (host-credentialed, one client) or `http` (streamable HTTP, per-request bearer) |
| `--context` | current context | stdio only: the dtctl context every call runs as. The agent cannot change it. |
| `--environment-url` | — | http only: the environment every request targets; the bearer token comes per request |
| `--addr` | `127.0.0.1:7212` | http only: listen address |
| `--yes` | off | skip elicitation on mutating commands (unattended automation that has its own approval step) |
| `--allow-spill` | off | grant `HostDiskSpill`: large results spill to the host disk and come back as a resource link instead of inline rows |
| `--min-stability` | `stable` | the engine's default floor; `experimental` widens it |
| `--stability-exception` | — | repeatable; admits one below-floor command or flag (e.g. `inventory`) |
| `--remote` | off | phase 2: also expose the hosted Remote MCP Server's tools (see below) |
| `--max-duration`, `--max-queued` | engine defaults | as `serve http` |

Profile and safety level are **not** flags. They come from the pinned context
(stdio) or the request (http), exactly as the profiles design prescribes: an
operator binds them to the context the agent inherits, and the agent cannot
widen either from inside a tool call.

### What the client sees

**Tools** (two):

```jsonc
{
  "name": "dtctl",
  "title": "Run a dtctl command",
  "description": "Run one dtctl command line against the configured Dynatrace environment and return exactly what the CLI prints. Pattern: <verb> <resource> [flags]. Call dtctl_commands first to learn the available verbs, resources and flags; prefer 'query' with DQL over resource-specific filtering. No shell: no pipes, redirection or variable expansion.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "command": { "type": "string", "description": "The command line after 'dtctl', e.g. \"get workflows -o json\". POSIX quoting." },
      "files":   { "type": "object", "additionalProperties": { "type": "string" }, "description": "Virtual files the command refers to (-f x.yaml). Returned with any writebacks." },
      "stdin":   { "type": "string", "description": "Standard input for commands that read it (-f -)." }
    },
    "required": ["command"]
  },
  "annotations": { "readOnlyHint": false, "destructiveHint": true, "openWorldHint": true }
}
```

```jsonc
{
  "name": "dtctl_commands",
  "title": "List available dtctl commands",
  "description": "The machine-readable catalog of commands available in this server's context: verbs, resources, aliases, flags, which commands mutate, and required scopes. Reflects the active profile and safety level. Call once at the start of a session.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "scope": { "type": "string", "description": "A verb or resource to narrow to, e.g. \"workflows\" or \"get\"." },
      "level": { "type": "string", "enum": ["minimal", "brief", "full"], "default": "minimal" }
    }
  },
  "annotations": { "readOnlyHint": true }
}
```

`dtctl_commands` is sugar over `dtctl commands …` through the same engine path
— it exists as a separate tool because a catalog call is the first thing every
session does and clients render a dedicated tool more reliably than a resource.
The minimal catalog is ~5 KB; `brief` ~30 KB; `full` is for debugging.

**Resources** (static, read-only):

| URI | Content |
|---|---|
| `dtctl://commands/howto` | `dtctl commands howto` — the LLM-oriented usage guide (~6.5 KB) |
| `dtctl://reference/dql` | `skills/dtctl/references/DQL-reference.md`, embedded at build time |
| `dtctl://reference/troubleshooting` | `skills/dtctl/references/troubleshooting.md` |
| `dtctl://spill/<id>` | phase 1 with `--allow-spill`: a spilled result file, readable in pages |

The skill files are already bundled for `dtctl skills install`; serving them as
resources costs nothing and gives shell-less clients the same knowledge.

### A tool call, end to end

```text
tools/call dtctl {"command": "get slos --limit 5"}
```

1. Parse `command` with POSIX rules (the engine does this; `argv` is not
   exposed over MCP — a string is what the model produces).
2. **Refuse host-steering flags.** `--context`, `--config` and `--profile`
   anywhere in argv return an MCP tool error without executing: the server's
   context is pinned, and the agent must not be able to retarget it. (The
   engine's session scrubbing covers env vars; argv is the adapter's job.)
3. **Classify.** Resolve the first non-flag token against
   `commands.MutatingVerbs` (after alias resolution — see open questions). A
   mutating verb with `--yes` unset triggers an elicitation:
   *"dtctl is about to run `delete slo abc-123` against `prod-agent`
   (safety level readwrite-all). Proceed?"* Decline → tool error
   `elicitation_declined`, nothing executed. A client that does not support
   elicitation gets the same error with a hint to start the server with
   `--yes`. This is a UX gate, not the enforcement: the safety level still
   decides inside the run.
4. **Execute.** `engine.Execute` with the command line plus `--agent` appended
   (so the output is the envelope, never a human table), `Capabilities{}`
   except `HostDiskSpill` when `--allow-spill`, and the server's limits.
5. **Return.** `content[0]` is `stdout` verbatim as text — byte-identical to the
   CLI, which is what the equality test guarantees and what makes the bundled
   skill's knowledge transferable. `isError` is `exitCode != 0`; `stderr` is
   appended as a second text block only when non-empty. `truncated` adds a
   trailing note (`"[output truncated at N bytes; narrow the query or use
   --limit/--fields]"`). Written-back files come back as additional text blocks
   named by path, so `apply --write-id` round-trips.

The envelope's `suggestions`, error codes (`safety_blocked`,
`profile_blocked`, `stability_blocked`, `unsupported_in_service`,
`capability_disabled`, `insufficient_scope`), DQL error positions and the
command-repair hints (#672) all arrive unchanged — the agent-mode work of the
last releases is the MCP UX.

## Design

### Where it lives

```
pkg/serve/mcp.go           newMCPCommand(), registered in NewCommand() beside http
pkg/serve/mcp_tools.go     the two tool handlers + argv guard + classification
pkg/serve/mcp_resources.go catalog/howto/reference resources
pkg/serve/mcp_remote.go    phase 2: hosted-server discovery and pass-through
```

Same shape as `http.go`: the `RunE` carries the `cmd.RunActive()` guard, every
request becomes one `engine.Execute`, the protocol server holds no dtctl logic.
`main` already dispatches `serve` outside the invocation lock; nothing changes
there. The development gate is the existing `serve` feature key — `serve mcp`
graduates with `serve`, not on its own.

**Dependency:** the official Go SDK, `github.com/modelcontextprotocol/go-sdk`
(`mcp` package: `Server`, `AddTool`, `AddResource`, `StdioTransport`,
`StreamableHTTPHandler`, elicitation). Root module only — `pkg/serve` imports
`pkg/engine`, which imports `cmd` and the whole tree, so the root module is
already the heavy one. `sdk/` stays untouched.

### Two tenant modes, one new engine seam

`serve http` is multi-tenant: every request carries `environmentUrl` + `token`,
and `cmd.Session` detaches the run from the host config. A stdio MCP server
launched by a desktop client needs the opposite — **the user's own context**
with keyring-held OAuth and automatic refresh — and today `engine.Request`
requires `EnvironmentURL`/`Token`.

`cmd.RunOptions` already supports it (`Session == nil` reads the host config),
so the change is in `pkg/engine`:

```go
type Request struct {
    // ... existing fields ...

    // HostContext runs the request as the named context of the host's own
    // dtctl config (keyring, OAuth refresh, context-bound safety level and
    // profile). Mutually exclusive with EnvironmentURL/Token. The request
    // cannot change it: --context/--config in Argv are a usage error, and
    // DTCTL_CONTEXT/DTCTL_CONFIG in Env are ignored.
    HostContext string
}
```

| | stdio (`HostContext`) | http (`Session`) |
|---|---|---|
| Credentials | host config + keyring, OAuth refresh | per-request bearer token |
| Environment | the context's | `--environment-url` (per server instance) |
| Safety level / profile | the context's bindings | request fields, as `serve http` |
| `BlockedCommands` | engine policy, unchanged | engine policy, unchanged |
| Env scrubbing | `DTCTL_CONTEXT`, `DTCTL_CONFIG`, `DTCTL_PROFILE` pinned, not scrubbed; everything else as today | as today |

The blocked-command policy applies in both modes: `config`, `ctx`, `auth`,
`account`, `alias`, `edit`, `plugin`, `skills`, `doctor`, `completion`,
`inspect`, `serve` are absent. In host mode that is what pins the context —
an agent that cannot see `ctx` cannot `ctx use prod`. `HostContext` is the
only engine change this design needs; `serve http` is unaffected by it.

For the http transport, one server instance serves one environment
(`--environment-url`) and the bearer comes per request. Multi-environment
deployments run one instance per environment — the same "scale with
instances" rule the engine already imposes for concurrency. Putting the
environment in a header was considered and rejected: a per-request
environment plus a per-request token is a credential-forwarding proxy, which
is a different threat model from "dtctl for one tenant".

### Output budget and spill

Agent mode already bounds what a command prints: lists page at 50, query rows
compact and cap fields, `--max-output-bytes` cuts the rest, and the engine's
`MaxOutputBytes` is the backstop. The adapter adds nothing — a tool result is
the envelope, and the envelope was designed for exactly this consumer.

`HostDiskSpill` is **off by default**: an MCP client may have no file access,
so a path in the result would be useless. The envelope's `summary-only` kind
(manifest without `path`) already handles "too big and nowhere to put it", and
steers the agent to narrow the query. With `--allow-spill` — for hosts whose
agent can read files, or for the phase-1 `dtctl://spill/<id>` resource — the
`result-file` kind comes back with the path *and* a `resource_link` content
block, and `inspect` is re-admitted for that server (it is blocked by the
engine policy only because a service request has no disk; a spilling server
does).

### Serialization and deadlines

One invocation at a time per process is a non-issue on stdio (one client, and
MCP clients serialize tool calls per server in practice). On http it is the
same constraint `serve http` documents; `MaxQueued` → JSON-RPC error with a
retry hint. `MaxDuration` (5 min default) bounds every call; the adapter emits
MCP progress notifications on a 10 s tick for long-running `query`/`exec` so
clients that render them do not time out silently.

### Phase 2 — passing the hosted Remote MCP Server through

With `--remote`, the server connects to
`{env}/platform-reserved/mcp-gateway/v0.1/servers/dynatrace-mcp/mcp` as an MCP
*client*, lists its tools, and re-exposes a filtered set under a `dt_` prefix:

- **Discover, never hard-code.** The remote tool list is `v0.1` with Early
  Access and Preview entries; names will move. `tools/list` at startup, cached
  for the process lifetime, re-listed on a `tools/list_changed` notification.
- **Drop overlaps.** `execute-dql` (native `query` is uncapped, budgeted and
  spill-aware) and `find-documents` (native `get dashboards/notebooks`). Keep
  one NL→DQL path — the remote `create-dql` — and drop `exec copilot nl2dql`
  from the howto when `--remote` is on, so the agent is not offered two.
- **Keep the analysis tools.** Problems, problem details, forecasting,
  changepoints, the three anomaly-detection analyzers, Kubernetes events,
  vulnerabilities, security posture/summary, log patterns, troubleshooting
  guides, docs Q&A. These are Davis capabilities dtctl does not wrap and should
  not reimplement.
- **Auth.** The bearer is the context's platform token (stdio) or the
  request's (http). It must carry `mcp-gateway:servers:invoke` and
  `mcp-gateway:servers:read` on top of the Davis and storage scopes; a 401/403
  from the gateway is reported once at startup as a scope finding, with the
  `dtctl account create token --scope …` recipe, and the remote tools are
  simply not registered — never a hard failure of the native surface. `dtctl
  --check-scopes` and `docs/TOKEN_SCOPES.md` gain the two gateway scopes.
- **Not available on Managed.** The gateway is SaaS-only; `--remote` on a
  non-`apps.dynatrace.com` URL is a startup warning, not an error.
- **Annotations.** Remote tools are forwarded with `readOnlyHint: true` (every
  hosted tool is read-only today) and never pass through elicitation.

The combined default surface is 2 native + ~12 remote tools — within the
range where clients still load every definition eagerly.

## Design decisions

- **Generic command tool, not typed tools.** Three reasons, in order of weight.
  (1) The engine design's first goal: no second surface that drifts. A typed
  catalog would need an enum per resource, a schema per verb, and a test that
  they match `pkg/commands` — machinery to approximate what the catalog already
  *is*. (2) Context cost: 150 verb/resource pairs even as 7 consolidated tools
  is 7 schemas with large enums, every turn; one tool with a string is
  constant. (3) Knowledge transfer: the bundled skill, the howto, the
  envelope's suggestions and the command-repair hints all speak dtctl command
  lines. A typed tool would make the agent translate between two vocabularies.
  The cost is that the model must know dtctl syntax — which `dtctl_commands`,
  the howto resource and the repair hints exist to teach, and which the field
  (AWS `call_aws`, Cloudflare Code Mode) has shown models handle well.
- **`serve mcp`, not `mcp serve`, not a plugin.** Verb first, as every dtctl
  command; `serve` is the parent `pkg/serve` already defines, and `mcp` is the
  protocol beside `http`. A plugin (`dtctl-mcp`) would have to re-implement the
  engine's isolation outside the process that owns it.
- **No `--read-only`, no `--toolsets`.** Both exist in other MCP servers; here
  both already have owners. "No writes" is `safety-level: readonly` on the
  context; "fewer commands" is a profile. Adding MCP-only synonyms would create
  the two-knobs confusion the profiles design spends a section preventing.
- **Elicitation as UX, safety level as enforcement.** The gate asks the human
  because an unattended `delete` is the one thing every MCP user fears; the
  safety level decides because the gate can be skipped (`--yes`, a client
  without elicitation) and the engine cannot be.
- **`--agent` appended, always.** An MCP result is machine-consumed; a human
  table in it is a bug. The envelope is also what carries the error codes and
  suggestions the whole agent-mode effort produced.
- **Resources for knowledge, tools for action.** Clients differ wildly in how
  they surface resources (Claude Code: @-mention only), so the one thing every
  session needs — the catalog — is also a tool. Everything else that is static
  text is a resource and costs no tool-definition tokens.
- **Host mode is an engine seam, not an adapter hack.** The adapter could call
  `cmd.Run` directly with `Session == nil`, bypassing the engine. It must not:
  the engine is where `BlockedCommands`, limits and the output cap live, and
  `serve http`'s guarantees come from going through it. One `HostContext`
  field keeps both protocols on the same path.

## Security and safety notes

- **Same threat model as `serve http`.** No authentication of its own on the
  http transport; localhost by default; a correctness boundary, not a sandbox.
  Untrusted agents belong behind a process boundary with a scoped token.
- **The agent cannot retarget the server.** `--context`/`--config`/`--profile`
  in argv are refused before execution; `ctx`/`config`/`auth` are blocked by
  policy; `DTCTL_CONTEXT` and friends are pinned (host mode) or scrubbed
  (session mode).
- **Tokens never cross the MCP boundary.** The tool result is CLI stdout, which
  never prints a token; `auth` is blocked; the remote pass-through forwards the
  bearer to the gateway only, never into a tool result.
- **Mutations are visible.** Elicitation names the command, context and safety
  level; the envelope names what changed; `--yes` is a deliberate operator
  choice at server start, not something a tool call can set.
- **Prompt injection via results.** Everything a tool returns came from the
  tenant's data (log lines, dashboard titles). That is the client's problem to
  frame as data, as with every MCP server; dtctl adds no instructions of its
  own to results beyond the envelope's `suggestions`, which the client already
  treats as dtctl's voice.

## Maturity

Development-tier under the `serve` feature key, graduating together with
`serve http` when its open items settle (per-command cancellation audit,
admission state per engine instance) plus the MCP-specific ones below. The
`HostContext` engine seam is `pkg/engine` API and ships with the engine's
stability.

## Open questions

1. **Alias resolution before classification.** In host mode the user's aliases
   apply (`alias` the *command* is blocked; alias *expansion* is not). The
   mutating-verb check must run after expansion, or `dtctl rmwf x` slips past
   elicitation. Expose the resolver from `cmd` or classify inside the run and
   surface a pre-execution hook — the latter is cleaner but touches `cmd`.
2. **Structured content.** MCP supports `structuredContent` beside text. The
   envelope is already JSON; emitting it as structured content with an
   `outputSchema` would let clients render tables. Deferred: it is a second
   output shape, and the equality guarantee is about the text.
3. **`inventory` in the presets.** Neither the `query` nor `investigate`
   profile preset lists `inventory`, and `inventory` is `experimental`, so an
   MCP agent on a stable floor cannot run it without `--stability-exception
   inventory`. Both are worth fixing independently of this design.
4. **Progress and cancellation.** The 10 s progress tick is cheap; honouring an
   MCP `cancelled` notification needs the engine's cooperative cancellation to
   actually reach the command — the same audit the engine design lists.
5. **Hosted-server details marked *(unverified)*** in the landscape table:
   confirm before phase 2.

## Phasing

1. **Engine:** `Request.HostContext` with the argv/env pinning rules and a leak
   test beside `TestRunProfileMaskDoesNotLeakBetweenInvocations`.
2. **`serve mcp` stdio:** the two tools, three resources, argv guard,
   elicitation on mutating verbs, `--allow-spill`. Equality test: a tool
   result's text equals `dtctl <command> --agent` run locally with the same
   context.
3. **http transport:** streamable HTTP with per-request bearer, `--environment-url`,
   the `serve http` limits and shutdown behaviour.
4. **`--remote`:** discovery, filter, scope finding, `dt_` namespace, gateway
   scopes in `TOKEN_SCOPES.md` and `--check-scopes`.

Docs to update when implementing: `docs/SERVE.md` (new protocol section),
`docs/AGENT_MODE.md` (MCP as a transport for the envelope), `docs/TOKEN_SCOPES.md`
(gateway scopes, phase 4), `AGENTS.md` (the `pkg/serve` line), and
`IMPLEMENTATION_STATUS.md`.
