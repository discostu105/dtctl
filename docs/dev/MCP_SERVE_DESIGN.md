# `dtctl serve mcp` — MCP Protocol Adapter Design

**Status:** Proposed
**Created:** 2026-10-06
**Audience:** anyone adding a protocol under `dtctl serve`, deciding what dtctl should look like to an MCP client, or comparing dtctl with the Dynatrace Remote MCP Server.

> **Builds on:** [SERVICE_ENGINE_DESIGN.md](SERVICE_ENGINE_DESIGN.md) (the engine
> and the `serve` parent command),
> [RECIPES_DESIGN.md](https://github.com/dynatrace-oss/dtctl/blob/docs/recipes-design/docs/dev/RECIPES_DESIGN.md)
> (the typed layer and the evaluation harness; on branch `docs/recipes-design`
> at the time of writing — repoint to `RECIPES_DESIGN.md` once it lands),
> [COMMAND_PROFILES_DESIGN.md](COMMAND_PROFILES_DESIGN.md)
> (the surface axis), [context-safety-levels.md](context-safety-levels.md) (the
> permission axis), [GENERIC_API_ACCESS.md](GENERIC_API_ACCESS.md) (`exec api`).
> `pkg/serve/serve.go` already reserves the slot: "room for `dtctl serve mcp`
> and others without redefining what bare `serve` means."

## Overview

`dtctl serve mcp` runs dtctl as a [Model Context Protocol](https://modelcontextprotocol.io)
server. It is the second protocol under `dtctl serve`, beside `serve http`, and
like it a thin adapter over `pkg/engine`: an MCP tool call carries one dtctl
command line (or one recipe invocation), and the tool result carries what the
CLI would have printed.

The point is the same as for `serve http` — **the CLI is the API**. There is no
hand-maintained catalog of typed tools beside the command tree. The client gets
four tools: run a command line, read the command catalog, search the recipe
book, run a recipe. Everything dtctl can do — including the spec-governed
`exec api` passthrough and every recipe — is reachable through them, under the
restriction axes the engine already enforces per request. Recipes supply the
*typed* layer, and they do so as content: a recipe's declared params are
already a schema, so they can be surfaced as individual MCP tools without a
second surface to keep in sync.

```text
MCP client ──tools/call dtctl     {command:"query 'fetch logs | limit 5'"}──▶ serve mcp
           ──tools/call dtctl_run {recipe:"services-failures", params:{…}}──▶    │ engine.Execute
                                                                                 ▼
                                                                       cmd tree (--agent appended)
                                                                                 │ stdout = agent envelope
          ◀──────────────── content[0].text = stdout (byte-identical) ───────────┘
```

### Why now

- **The local open-source Dynatrace MCP server is deprecated.** Its final
  release (2.1.2, 2026-07) points local-IDE users to "Dynatrace-for-AI skills
  plus dtctl" and remote/agent-to-agent users to the hosted Remote MCP Server.
- **The hosted Remote MCP Server is read-only analysis.** It exposes ~20
  Davis-backed tools (problems, forecasting, anomaly detectors, changepoints,
  docs Q&A, NL↔DQL) and no write tools — no dashboards, notebooks, workflows,
  settings. Its own migration notes recommend pairing it with dtctl.
- **Clients without a shell lost their only option.** Claude Desktop without a
  terminal, IDE chat panes, enterprise agent platforms that forbid subprocesses:
  they could run the deprecated local server and cannot run a CLI. For them MCP
  is the only transport, and dtctl's operational surface is what the remote
  server lacks.
- **The engine made the adapter cheap.** `pkg/engine` already provides
  per-request isolation, the five restriction axes, admission control, output
  budgets, and byte-identical CLI output. `serve http` proves the adapter shape
  in ~300 lines.
- **Recipes make typed tools cheap too.** A recipe is a named, parameterized,
  self-describing query with a declared schema and an `emptyMeans`. That is the
  "workflow-level tool with semantic fields" every 2025–2026 tool-design guide
  asks for, and it exists as content, lint-gated, with golden rendered DQL.

### What this is not

Coding agents that *have* a shell (Claude Code, Cursor, Codex CLI, Copilot) are
better served by the CLI plus the bundled skill, and the docs must keep saying
so. Every MCP tool definition costs context on every turn; a CLI costs nothing
until it is called, and `dtctl commands` / `dtctl get recipes` give the agent
progressive disclosure for free. `serve mcp` is for hosts that cannot exec, not
a replacement for the primary persona.

It is also **not a proxy for the Dynatrace Remote MCP Server**. See
[Two servers, kept apart](#two-servers-kept-apart).

## Goals

1. **One command surface.** The MCP tool set is derived from the command tree
   and the recipe book, never hand-maintained beside them. Adding a dtctl
   command or a recipe adds nothing to `pkg/serve/mcp*.go`.
2. **Small default tool list.** Four tools. The guidance from every major MCP
   server author (Anthropic, GitHub, AWS, Cloudflare, the k8s servers)
   converges: tool definitions are the context bloat; a generic executor plus a
   catalog beats one tool per endpoint; typed tools earn their place only when
   they encode knowledge the model would otherwise have to discover.
3. **The engine's axes, unchanged.** Safety level, profile, stability floor,
   environment mask and capabilities apply exactly as to `serve http`. No
   MCP-specific "read-only mode" or "toolsets" — those words already have owners
   (safety levels and profiles).
4. **Host-credentialed local mode.** A stdio server launched by a desktop
   client runs as the user's pinned dtctl context — keyring, OAuth refresh and
   all — because that is the user's expectation and the only auth that works
   without a token in a config file.
5. **Writes are gated, visibly.** A mutating command line asks the human first
   (MCP elicitation) unless the operator opted out at server start.
6. **Clean separation from the hosted server, and measurable against it.**
   dtctl's server and Dynatrace's server are different products with different
   owners, release cadences and auth; a client lists both. The design makes the
   comparison between them, and between dtctl's own transports, an experiment
   the existing eval harness can run.

## Non-Goals

- **A hand-written typed tool catalog** (`dtctl_get_workflows`,
  `dtctl_create_slo`, …). The engine design forbids "a second, parallel API
  surface that drifts from the CLI"; a typed tool catalog in Go is that
  surface. Typed tools come from recipes, which are content.
- **Proxying the Remote MCP Server.** Not even as an option. See below.
- **A replacement for the skill.** The DQL reference and the howto are exposed
  as MCP resources for clients that cannot install skills; the skill remains
  the primary knowledge channel.
- **A security boundary.** Same posture as `serve http`: a correctness boundary
  inside one process, no authentication of its own on the HTTP transport,
  untrusted callers belong behind a process boundary. Scope the Dynatrace token.
- **Streaming tool results.** `--watch`, `--follow` and `--live` stay refused
  (`capability_disabled`), as in `serve http`.

## Landscape (facts the design rests on)

Verified in October 2026 against primary sources; items marked *(unverified)*
could not be confirmed and must be re-checked before they are relied on.

| Fact | Consequence |
|---|---|
| `dynatrace-oss/dynatrace-mcp` deprecated at 2.1.2; 18 tools incl. writes (Slack, email, event, notebook) with elicitation gates; per-session Grail scan budget | Precedent for elicitation on writes; carries over. |
| Hosted server: `https://{env}.apps.dynatrace.com/platform-reserved/mcp-gateway/v0.1/servers/dynatrace-mcp/mcp`, streamable HTTP, bearer = platform token or confidential OAuth client; needs `mcp-gateway:servers:invoke` + `mcp-gateway:servers:read` plus Davis/storage scopes; no public clients, no dynamic client registration | A dtctl-minted platform token can serve both servers if the scope tables carry the two gateway scopes. |
| Hosted server tool names are `v0.1`, several Early Access/Preview, all read-only, `execute-dql` capped at 1000 records | Different stability story from dtctl's tiers; a reason not to wrap it. Native `query` and recipes are the comparable surface. |
| Whether customers can register their own servers in the gateway *(unverified)*; any Grail budget on the hosted server *(unverified)* | Not relied on. |
| Anthropic tool-design guidance: fewer, workflow-level tools; client-side deferral (`defer_loading`, Claude Code's tool search) kicks in at ~10 tools or ~10K tokens of definitions; Claude Code caps a tool result at 25K tokens by default | Four tools stay under every threshold; materialised recipe tools are viable only where deferral exists; results must be budgeted (agent mode already does this). |
| GitHub removed server-side dynamic toolsets (2026-05) in favour of client-side deferral; AWS and Cloudflare moved to single `execute`-style tools over whole APIs | Generic executor is the direction of the field. |
| Recipes eval (eight rounds, three models, two tenants): agents with a recipe book were the most correct arm, used ~40% of the calls and wall time, and read recipes more than they ran them; pull-only discovery failed (0 recipe uses in 168 runs) | Recipes belong in the MCP surface; discovery must push (tool descriptions, envelope hints), not only pull. |

## Two servers, kept apart

A client config lists two servers, and the design keeps them distinct on
purpose:

| | `dtctl serve mcp` | Dynatrace Remote MCP Server |
|---|---|---|
| Owner, release | dtctl, open source; development → experimental → stable per [stability tiers](../STABILITY.md) | Dynatrace platform; `v0.1` gateway, per-tool Early Access / Preview / GA |
| What it is | the operational surface: get/describe/apply/delete/exec, `query`, `inventory`, `exec api`, the recipe book — read **and** write, DQL-transparent | Davis-hosted reasoning: forecasting, anomaly detectors, changepoints, root-cause, docs Q&A, NL↔DQL — capabilities that need Davis compute |
| Auth | the user's dtctl context (keyring, OAuth refresh, safety level, profile) or a per-request bearer | platform token / confidential OAuth client with `mcp-gateway:*` scopes |
| Provenance in a transcript | the envelope: `context.query`, `context.recipe.{name,version,source}`, exit code | an opaque "agent" result |
| When to reach for it | anything that touches a resource, needs the DQL visible, needs a write, or is a known question (recipe) | Davis analysis over data dtctl already found |

An earlier draft proxied the hosted server's tools through `serve mcp` so a
client needed one entry. That is rejected:

- **It forwards credentials** from dtctl's context to a third endpoint, which
  is a different threat model from "dtctl for one tenant".
- **It hides which side answered.** A proxied tool inherits dtctl's name,
  filtering and stability badge while its behaviour is the gateway's.
- **It couples dtctl's stability story to a `v0.1` endpoint** whose tool names
  are still moving.
- **It makes comparison impossible.** Every remote call would be shaped by
  dtctl's filter; the [evaluation](#evaluation) needs the hosted server as its
  own arm, unmediated.

MCP clients support several servers natively. What the proxy would have bought
is kept as two conveniences that leave the separation intact:

- `dtctl serve mcp --print-client-config` emits **both** server entries — the
  dtctl one, and the official hosted one with the context's environment URL
  filled in and the gateway-scope token recipe (`dtctl account create token
  --scope mcp-gateway:servers:invoke,mcp-gateway:servers:read,…`) in a
  comment — so setup is still one command.
- `docs/TOKEN_SCOPES.md` and `--check-scopes` gain the two gateway scopes, so
  one dtctl-minted platform token serves both servers.

## User experience

### Starting the server

```bash
dtctl config set development.serve on           # same opt-in as serve http
dtctl serve mcp                                 # stdio, current context
dtctl serve mcp --context prod-agent            # stdio, pinned context
dtctl serve mcp --expose-recipes k8s,problems   # also list those domains' recipes as typed tools
dtctl serve mcp --transport http --addr 127.0.0.1:7212 --environment-url https://abc12345.apps.dynatrace.com
dtctl serve mcp --print-client-config           # both server entries, ready to paste
```

| Flag | Default | Meaning |
|---|---|---|
| `--transport` | `stdio` | `stdio` (host-credentialed, one client) or `http` (streamable HTTP, per-request bearer) |
| `--context` | current context | stdio only: the dtctl context every call runs as. The agent cannot change it. |
| `--environment-url` | — | http only: the environment every request targets; the bearer token comes per request |
| `--addr` | `127.0.0.1:7212` | http only: listen address |
| `--expose-recipes` | off | `<domain,…>` or `all`: materialise those recipes as individual typed tools (see [Recipes](#recipes-the-typed-layer)); default off pending the evaluation |
| `--yes` | off | skip elicitation on mutating commands (unattended automation with its own approval step) |
| `--allow-spill` | off | grant `HostDiskSpill`: large results spill to the host disk and come back as a resource link |
| `--min-stability` | `stable` | the engine's default floor; `experimental` widens it (recipes are `experimental`, see below) |
| `--stability-exception` | — | repeatable; admits one below-floor command or flag (e.g. `run`, `inventory`) |
| `--log-calls` | — | JSONL call log for evaluation (stdio only; host state) |
| `--print-client-config` | — | print the client configuration and exit |
| `--max-duration`, `--max-queued` | engine defaults | as `serve http` |

Profile and safety level are **not** flags. They come from the pinned context
(stdio) or the request (http), as the profiles design prescribes: an operator
binds them to the context the agent inherits, and the agent cannot widen either
from inside a tool call.

**Stability floor and recipes.** `run`, `get recipes` and `describe recipe`
are `experimental`, and the engine's default floor is `stable`. A server that
exposes recipe tools therefore admits them explicitly
(`--stability-exception run --stability-exception "get recipes"`), and
`--expose-recipes` implies those exceptions. The floor stays `stable` for
everything else, so an MCP host does not inherit the whole experimental tier
by wanting recipes.

### What the client sees

**Tools** (four by default):

```jsonc
{
  "name": "dtctl",
  "title": "Run a dtctl command",
  "description": "Run one dtctl command line against the configured Dynatrace environment and return exactly what the CLI prints. Pattern: <verb> <resource> [flags]. Before writing DQL for a common question, call dtctl_recipes — a recipe is a verified, parameterized query that explains its own result. Call dtctl_commands once to learn the verbs, resources and flags. No shell: no pipes, redirection or variable expansion.",
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

```jsonc
{
  "name": "dtctl_recipes",
  "title": "Find a recipe for a question",
  "description": "Search the recipe book: verified, parameterized DQL queries for common questions (pod restarts, failed requests of a service, open problems, …). Returns name, the question each recipe answers, its required params and default window. Hides recipes the environment has no data for. Use the question itself as the search.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "search": { "type": "string", "description": "Words from the question, e.g. \"pods oom killed\"." },
      "domain": { "type": "string", "description": "Narrow to one domain (k8s, services, problems, …). Omit search and domain for the domain index." },
      "tag":    { "type": "string" }
    }
  },
  "annotations": { "readOnlyHint": true }
}
```

```jsonc
{
  "name": "dtctl_run",
  "title": "Run a recipe",
  "description": "Run a recipe by name with typed params. The result carries the rendered DQL (context.query), the effective window, what the rows mean, what an empty result means, and ready-to-run follow-ups. Prefer this over writing DQL when dtctl_recipes found a match.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "recipe": { "type": "string" },
      "params": { "type": "object", "additionalProperties": true, "description": "The recipe's params by name, as dtctl_recipes listed them." },
      "from":   { "type": "string", "description": "Window start: a duration ago (2h, 7d) or RFC3339." },
      "to":     { "type": "string" },
      "scope":  { "type": "object", "additionalProperties": true, "description": "Scope dimensions the recipe declares: cluster, namespace, tag, …" },
      "dry_run": { "type": "boolean", "description": "Return the rendered DQL and window without executing." }
    },
    "required": ["recipe"]
  },
  "annotations": { "readOnlyHint": true }
}
```

`dtctl_commands`, `dtctl_recipes` and `dtctl_run` are sugar over `dtctl
commands …`, `dtctl get recipes …` and `dtctl run …` through the same engine
path. They exist as separate tools because each is a *first* call in a session
and clients render dedicated tools more reliably than a resource, and because
a typed `params` object is what a model produces more accurately than a
flag string. `dtctl_run` adds nothing a `dtctl` call with `run <recipe>
--param …` would not do; its result is the same envelope.

**Resources** (static, read-only):

| URI | Content |
|---|---|
| `dtctl://commands/howto` | `dtctl commands howto` — the LLM-oriented usage guide (~6.5 KB) |
| `dtctl://recipes/<name>` | `describe recipe <name>`: params, timeframe, DQL, `means`, `emptyMeans`, `next` |
| `dtctl://reference/dql` | `skills/dtctl/references/DQL-reference.md`, embedded at build time |
| `dtctl://reference/troubleshooting` | `skills/dtctl/references/troubleshooting.md` |
| `dtctl://spill/<id>` | with `--allow-spill`: a spilled result file, readable in pages |

### Recipes: the typed layer

A recipe maps onto an MCP tool definition almost field for field:

| Recipe | MCP tool |
|---|---|
| `metadata.name` | `name`: `recipe_` + name with `-` → `_` (`services-failures` → `recipe_services_failures`; MCP names are `[A-Za-z0-9_-]`, ≤ 64) |
| `summary`, `means` | `description` — the summary is written as the question the recipe answers, which is also what search ranks against |
| `params` (`string`/`int`/`bool`/`enum`/`list`, `required`, `default`, `min`/`max`, `pattern`) | `inputSchema` — a direct JSON Schema translation (`enum` → `enum`, `list` → `array` of `string`, `pattern` → `pattern`) |
| `timeframe` (unless `none`/`fixed`), `scope` | framework params `from`/`to` and the declared scope dimensions |
| read-only by construction (DQL only) | `readOnlyHint: true` |
| `emptyMeans`, `next`, `context.query`, `context.window`, `context.scope` | arrive in the result envelope unchanged |

This is the typed-tool layer this design otherwise refuses, and it is
admissible for one reason: it is **content, not code**. The schema already
exists in the YAML, is validated by the loader and `dtctl verify recipe`, is
pinned by golden rendered DQL, and changes under content review. Generating a
tool definition from it is a projection, not a second surface.

The constraint is count. 45 built-in recipes today, 300–500 at maturity, plus
app and org layers (RECIPES_DESIGN §11). Listing them all eagerly is the
context bloat the whole design avoids, so the server mirrors the recipes
design's own progressive disclosure rather than inventing one:

| Level | In the CLI | Over MCP |
|---|---|---|
| 0 | `dtctl commands` shows `run` | the `dtctl` tool description names `dtctl_recipes` |
| 1–2 | `get recipes`, `--search`, `--domain` | `dtctl_recipes` |
| 3 | `describe recipe` | `dtctl://recipes/<name>` resource, or `dtctl_run` with `dry_run` |
| run | `dtctl run <recipe> …` | `dtctl_run`, or a materialised `recipe_<name>` tool |

`--expose-recipes` materialises a slice as individual tools, on top of the
four. It is **off by default**, for two reasons. First, context: forty typed
tools cost more per turn than one `dtctl_run`, and only clients with deferred
tool loading (Claude Code's tool search; the API's `defer_loading`) make a large
set free. Second, evidence: whether a materialised tool is *used better* than
`dtctl_run` with a `params` object is exactly the kind of question the recipes
eval found surprising answers to (agents read recipes more than they ran them;
a pointer that is a runnable command beats a lookup). The
[evaluation](#evaluation) decides the default; until then the flag exists so
the measurement can be made. What is listed is further bounded by what already
bounds `get recipes`: the profile (`run` or `run <name>` in the allowlist) and
the inventory filter (recipes whose `requires` is `absent` here are hidden).

**Discovery pushes, as the recipes design requires.** The `dtctl` tool's
result is the agent envelope, so a `query` that matches a recipe already comes
back with a bound, runnable `dtctl run …` suggestion and the recipe's `checks:`
warnings — the MCP server adds nothing and loses nothing. The one MCP-specific
push is the `dtctl` tool description naming `dtctl_recipes` before DQL, because
a description is read on every turn and a skill is not.

**Layers follow the tenant mode.** stdio/host mode sees what the CLI sees:
built-in, user, org and synced app bundles (host state: lock, store, user
directory). http/session mode sees built-in plus `Request.RecipeApps`, in
memory only — the rule the recipes design already states for the engine. No
MCP-specific rule is needed.

### A tool call, end to end

```text
tools/call dtctl {"command": "get slos --limit 5"}
```

1. Parse `command` with POSIX rules (the engine does this; `argv` is not
   exposed over MCP — a string is what the model produces). `dtctl_run`
   assembles argv itself from `recipe`, `params`, `from`/`to` and `scope`,
   emitting `--name=value` words, never positionals, for values the model
   supplied (a value is data and must not become a flag — the same rule the
   recipes design applies to its own follow-up suggestions).
2. **Refuse host-steering flags.** `--context`, `--config` and `--profile`
   anywhere in argv return an MCP tool error without executing: the server's
   context is pinned, and the agent must not retarget it. (The engine's session
   scrubbing covers env vars; argv is the adapter's job.)
3. **Classify.** Resolve the first non-flag token — after alias expansion, see
   [Open questions](#open-questions) — against `commands.MutatingVerbs`. A
   mutating verb with `--yes` unset triggers an elicitation:
   *"dtctl is about to run `delete slo abc-123` against `prod-agent` (safety
   level readwrite-all). Proceed?"* Decline → tool error `elicitation_declined`,
   nothing executed. A client without elicitation support gets the same error
   with a hint to start the server with `--yes`. This is a UX gate, not the
   enforcement: the safety level still decides inside the run. `run` is not a
   mutating verb (recipes are DQL), so recipe tools never elicit.
4. **Execute.** `engine.Execute` with the command line plus `--agent` appended
   (so the output is the envelope, never a human table), `Capabilities{}`
   except `HostDiskSpill` when `--allow-spill`, the server's limits, and the
   stability exceptions the flags imply.
5. **Return.** `content[0]` is `stdout` verbatim as text — byte-identical to the
   CLI, which is what the equality test guarantees and what makes the bundled
   skill's knowledge transferable. `isError` is `exitCode != 0`; `stderr` is
   appended as a second text block only when non-empty. `truncated` adds a
   trailing note. Written-back files come back as additional text blocks named
   by path, so `apply --write-id` round-trips.
6. **Log** (with `--log-calls`): one JSONL line — tool, a digest of the
   arguments, recipe name/version/source if any, duration, bytes in and out,
   exit code, `truncated`, elicitation outcome. Never the argument values or
   the output: the log is for counting, and a transcript holds the content.

The envelope's `suggestions`, error codes (`safety_blocked`,
`profile_blocked`, `stability_blocked`, `unsupported_in_service`,
`capability_disabled`, `insufficient_scope`), DQL error positions, empty-result
diagnosis, recipe hints and command-repair hints all arrive unchanged — the
agent-mode work of the last releases is the MCP UX.

## Design

### Where it lives

```
pkg/serve/mcp.go            newMCPCommand(), registered in NewCommand() beside http
pkg/serve/mcp_tools.go      the four tool handlers, argv guard, classification, call log
pkg/serve/mcp_recipes.go    recipe → tool-definition projection for --expose-recipes
pkg/serve/mcp_resources.go  catalog/howto/recipe/reference resources
pkg/serve/mcp_config.go     --print-client-config
```

Same shape as `http.go`: the `RunE` carries the `cmd.RunActive()` guard, every
request becomes one `engine.Execute`, the protocol server holds no dtctl logic.
`main` already dispatches `serve` outside the invocation lock; nothing changes
there. The development gate is the existing `serve` feature key — `serve mcp`
graduates with `serve`, not on its own.

**Dependencies.** The official Go SDK,
`github.com/modelcontextprotocol/go-sdk` (`mcp` package: `Server`, `AddTool`,
`AddResource`, `StdioTransport`, `StreamableHTTPHandler`, elicitation). Root
module only — `pkg/serve` imports `pkg/engine`, which imports `cmd` and the
whole tree, so the root module is already the heavy one; `sdk/` stays
untouched. The recipe projection imports `pkg/recipes` (types, loader, listing)
and nothing from `cmd/`; it reads the same loaded book the `run` subtree is
built from. **This design therefore lands after, or rebased on, the recipes
branch**; phase 1 below can start on the engine seam independently.

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
| Recipe layers | built-in, user, org, synced app bundles | built-in + `Request.RecipeApps` |
| `BlockedCommands` | engine policy, unchanged | engine policy, unchanged |
| Env scrubbing | `DTCTL_CONTEXT`, `DTCTL_CONFIG`, `DTCTL_PROFILE` pinned, not scrubbed; everything else as today | as today |

The blocked-command policy applies in both modes: `config`, `ctx`, `auth`,
`account`, `alias`, `edit`, `plugin`, `skills`, `doctor`, `completion`,
`inspect`, `serve`, and `recipes` (sync/add/remove — host state) are absent. In
host mode that is what pins the context: an agent that cannot see `ctx` cannot
`ctx use prod`. `HostContext` is the only engine change this design needs;
`serve http` is unaffected by it.

For the http transport, one server instance serves one environment
(`--environment-url`) and the bearer comes per request. Multi-environment
deployments run one instance per environment — the same "scale with
instances" rule the engine already imposes for concurrency. A per-request
environment header was considered and rejected: a per-request environment plus
a per-request token is a credential-forwarding proxy, which is a different
threat model from "dtctl for one tenant".

### Output budget and spill

Agent mode already bounds what a command prints: lists page at 50, query rows
compact and cap fields, recipe envelopes are bounded to 16 KB,
`--max-output-bytes` cuts the rest, and the engine's `MaxOutputBytes` is the
backstop. The adapter adds nothing.

`HostDiskSpill` is **off by default**: an MCP client may have no file access,
so a path in the result would be useless. The envelope's `summary-only` kind
(manifest without `path`) already handles "too big and nowhere to put it" and
steers the agent to narrow the query. With `--allow-spill` — for hosts whose
agent can read files — the `result-file` kind comes back with the path *and* a
`resource_link` content block, and `inspect` is re-admitted for that server (it
is blocked by the engine policy only because a service request has no disk; a
spilling server does).

### Serialization and deadlines

One invocation at a time per process is a non-issue on stdio (one client, and
MCP clients serialize tool calls per server in practice). On http it is the
same constraint `serve http` documents; `MaxQueued` → JSON-RPC error with a
retry hint. `MaxDuration` (5 min default) bounds every call; the adapter emits
MCP progress notifications on a 10 s tick for long-running `query`/`run`/`exec`
so clients that render them do not time out silently.

## Evaluation

The recipes design ships an evaluation harness (`test/evals/recipes/`:
headless `claude -p` per cell, byte-identical prompts, a blind 0–3 judge
against independently measured ground truth, paired bootstrap CIs, a logging
`dtctl` wrapper with read-only enforcement, a policy audit). It varies
*knowledge* — skill, recipes, nudges — at a fixed transport. This design adds a
**transport axis** to the same harness, same tasks, same model, same tenants,
same windows, `-n 3`:

| Arm | Transport | Tools visible |
|---|---|---|
| CLI | Bash + skill (the harness's existing arms) | `dtctl` on PATH |
| MCP-generic | `dtctl serve mcp` | `dtctl`, `dtctl_commands`, `dtctl_recipes`, `dtctl_run` |
| MCP-typed | `dtctl serve mcp --expose-recipes <domains under test>` | the four, plus `recipe_*` |
| Remote | the hosted Remote MCP Server only | its ~20 tools |
| Both | `dtctl serve mcp` + Remote | the union |

The harness's `--strict-mcp-config` flips from "MCP off" to "exactly these
servers"; the Bash allow-list is empty for the MCP arms. Metrics are the
harness's — correctness with silent-wrong separated, calls, errored and empty
calls, tokens in/out, cost, wall time, scanned GB — plus two the transport
axis needs: **tool-definition tokens per turn** (the fixed cost each arm pays
before doing anything; measured from the transcript) and **result bytes per
call** (from `--log-calls` for dtctl, from the transcript for the hosted
server — which is why no proxy is needed to measure it).

The questions it answers, in order of stakes:

1. **Does dtctl over MCP lose correctness against the CLI?** If by much, MCP
   stays a fallback for shell-less hosts and the docs say so plainly; the tool
   descriptions are the first thing to iterate.
2. **Do materialised recipe tools beat `dtctl_run`?** Decides whether
   `--expose-recipes` defaults on (for clients with deferral) or stays an
   operator choice.
3. **Where does the hosted server beat dtctl, and where not?** Tells the docs
   which Davis tools to point users at, and tells the recipe backlog which
   questions to cover — a comparison that feeds content, not just a benchmark.

Nothing in the harness or its results names a tenant, a URL or a customer
(the harness's existing privacy rule).

## Design decisions

- **Generic command tool, with recipes as the typed layer.** The generic tool
  gives the whole surface at constant context cost and zero drift; recipes
  give typed, self-describing tools for the questions that recur, as content
  with its own review loop. A hand-written typed catalog in Go would give the
  worst of both: drift *and* context cost.
- **No proxy for the hosted server.** Ownership, auth, stability and
  measurability all argue the same way; two config entries are cheap and
  `--print-client-config` writes them. See [Two servers, kept apart](#two-servers-kept-apart).
- **`serve mcp`, not `mcp serve`, not a plugin.** Verb first, as every dtctl
  command; `serve` is the parent `pkg/serve` already defines, and `mcp` is the
  protocol beside `http`. A plugin (`dtctl-mcp`) would have to re-implement the
  engine's isolation outside the process that owns it.
- **`--expose-recipes` off by default.** Count and evidence, above. A default
  decided by measurement beats one decided by taste; the recipes design gates
  its own phase 2 the same way.
- **No `--read-only`, no `--toolsets`.** Both exist in other MCP servers; here
  both already have owners. "No writes" is `safety-level: readonly` on the
  context; "fewer commands" is a profile. MCP-only synonyms would create the
  two-knobs confusion the profiles design spends a section preventing.
- **Elicitation as UX, safety level as enforcement.** The gate asks the human
  because an unattended `delete` is the one thing every MCP user fears; the
  safety level decides because the gate can be skipped (`--yes`, a client
  without elicitation) and the engine cannot be.
- **`--agent` appended, always.** An MCP result is machine-consumed; a human
  table in it is a bug. The envelope is also what carries the error codes,
  suggestions and recipe hints the whole agent-mode effort produced.
- **Resources for knowledge, tools for action.** Clients differ wildly in how
  they surface resources (Claude Code: @-mention only), so the first-call
  things — catalog, recipe search — are tools. Static text is a resource and
  costs no tool-definition tokens.
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
  in argv are refused before execution; `ctx`/`config`/`auth`/`recipes` are
  blocked by policy; `DTCTL_CONTEXT` and friends are pinned (host mode) or
  scrubbed (session mode).
- **Tokens never cross the MCP boundary.** The tool result is CLI stdout, which
  never prints a token; `auth` is blocked; nothing is forwarded to any third
  endpoint — the hosted server is the client's own, separate connection.
- **Recipe content is prompt input.** The recipes design's trust rules
  (declared, synced, pinned sources; app bundles only with `originAppId` and
  only when declared) are what bound which recipe text an MCP tool description
  can carry. A materialised `recipe_*` tool's description is that text, so
  `--expose-recipes` surfaces exactly the layers the host mode would run —
  nothing a `dtctl run` on the same machine would not already execute.
- **Mutations are visible.** Elicitation names the command, context and safety
  level; the envelope names what changed; `--yes` is a deliberate operator
  choice at server start, not something a tool call can set.
- **The call log holds no content.** `--log-calls` records counts, sizes and
  digests; argument values and output stay in the client's transcript, under
  the client's retention.

## Maturity

Development-tier under the `serve` feature key, graduating together with
`serve http` when its open items settle (per-command cancellation audit,
admission state per engine instance) plus the MCP-specific ones below. The
`HostContext` engine seam is `pkg/engine` API and ships with the engine's
stability. `--expose-recipes` is additionally bound to the recipe commands'
own tier (`experimental`): a stable `serve mcp` with experimental recipe tools
is fine, since the floor machinery already expresses it.

## Open questions

1. **Alias resolution before classification.** In host mode the user's aliases
   apply (`alias` the *command* is blocked; alias *expansion* is not). The
   mutating-verb check must run after expansion, or `dtctl rmwf x` slips past
   elicitation. Expose the resolver from `cmd`, or classify inside the run and
   surface a pre-execution hook — the latter is cleaner but touches `cmd`.
2. **Structured content.** MCP supports `structuredContent` beside text. The
   envelope is already JSON; emitting it as structured content with an
   `outputSchema` would let clients render tables. Deferred: it is a second
   output shape, and the equality guarantee is about the text.
3. **Recipe tool descriptions.** `summary` alone, or `summary` + `means`? The
   former is cheaper per turn; the latter saves a `describe` call after the
   result. The eval's token and call counts decide.
4. **Progress and cancellation.** The 10 s progress tick is cheap; honouring an
   MCP `cancelled` notification needs the engine's cooperative cancellation to
   actually reach the command — the same audit the engine design lists.
5. **Per-value `requires`** (RECIPES_DESIGN open question 12) would let a
   materialised family tool hide enum values instead of the whole tool. Follow
   the recipes decision.
6. **Hosted-server details marked *(unverified)*** in the landscape table:
   confirm before `--print-client-config` writes the remote entry.

## Phasing

1. **Engine:** `Request.HostContext` with the argv/env pinning rules and a leak
   test beside `TestRunProfileMaskDoesNotLeakBetweenInvocations`. Independent
   of the recipes branch.
2. **`serve mcp` stdio:** the four tools, the resources, argv guard,
   elicitation on mutating verbs, `--allow-spill`, `--log-calls`,
   `--print-client-config`. Equality test: a tool result's text equals
   `dtctl <command> --agent` run locally with the same context. Depends on
   `pkg/recipes` for `dtctl_recipes`/`dtctl_run`.
3. **`--expose-recipes`:** the recipe → tool projection, with a golden test
   that the generated `inputSchema` for every built-in recipe matches a
   checked-in file (content changes show as a reviewable diff, like the golden
   rendered DQL).
4. **Evaluation:** the transport arms in `test/evals/recipes/`. Decides the
   `--expose-recipes` default and the docs' guidance on CLI vs MCP vs hosted.
5. **http transport:** streamable HTTP with per-request bearer,
   `--environment-url`, the `serve http` limits and shutdown behaviour.

Docs to update when implementing: `docs/SERVE.md` (new protocol section, the
two-servers guidance), `docs/AGENT_MODE.md` (MCP as a transport for the
envelope), `docs/RECIPES.md` (recipes over MCP), `docs/TOKEN_SCOPES.md`
(the two gateway scopes), `AGENTS.md` (the `pkg/serve` line), and
`IMPLEMENTATION_STATUS.md`.
