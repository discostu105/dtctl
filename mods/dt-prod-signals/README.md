# dt-prod-signals — a Claude Code mod (prototype)

Shows Dynatrace production signals for the file Claude is working on, inside
Claude Code. When Claude reads or edits a source file, the mod runs one
read-only `dtctl query` in the background and draws the answer above the
prompt:

```
◆ prod internal/cart/checkout.go · 48k spans · 310 errors (0.6%) · 52 exceptions · p95 412ms · last 2h
  worst: cart.checkout · shop-backend (production) — 310 err · payment provider timeout
[ Details ] [ Ask Claude ] [ Hide ]
```

- **Details** (or `/prod-signals [file]`) opens a pane with the recently touched
  files, their worst operations, how each was matched, the scanned bytes, and
  the DQL behind it.
- **Ask Claude** hands the worst failure and its DQL to Claude as a prompt.
- The model gets a tool, `mcp__dt-prod-signals__lookup`, to check a file's
  production behaviour before it changes it.

Strictly read-only: the only Dynatrace call is `dtctl query`.

## How a file is matched to spans

| Signal | Used for |
|---|---|
| `vcs.repository.url.full` == this repo's `git remote get-url origin` | scoping to the repo's services (toggle: `matchRepo`) |
| `otel.scope.name` ends with `/<dir>` | Go — the package's import path, so Go is matched **per package** |
| `code.namespace` == the class | Java / Kotlin / Scala under `src/main/<lang>/` |
| `code.filepath` ends with the last two path segments | any language that stamps it |
| an exception stack frame contains the path tail (or `Class.`) | Python, JS, JVM, … |

Test files, vendored code and non-source files are never queried. "No match"
means no telemetry was attributed to the file, not that the code is unused —
spans from generic instrumentation (HTTP servers, DB clients) carry no code
location.

## Cost

One query per file, cached for 10 minutes. Spans are not pre-filtered by
service, so a lookup scans the tenant's spans for the timeframe (a few GB for
`2h` on a busy tenant; the pane shows the scanned bytes). Keep the timeframe
short.

## Options

Set under `/config` (or `pluginConfigs["dt-prod-signals"].options` in settings):

| Option | Default | |
|---|---|---|
| `timeframe` | `2h` | `30m`, `2h`, `6h` or `24h` |
| `context` | *(current)* | dtctl context to query |
| `matchRepo` | `true` | filter on `vcs.repository.url.full`; turn off if your spans lack it |
| `autoLookup` | `true` | query on Read/Edit/Write; off leaves the tool and `/prod-signals` |

## Run it

Requires Claude Code 2.1.287+ and `dtctl` on `PATH` with a working context.

```bash
claude --plugin-dir ./mods/dt-prod-signals
```

The band and pane draw in the terminal and the desktop app's Code tab; in a
headless or cloud session the hooks and the tool still work, but nothing is
drawn.

## Develop

```bash
claude plugin validate mods/dt-prod-signals
claude plugin test mods/dt-prod-signals     # 14 tests, dtctl mocked
cd mods/dt-prod-signals && tsc -p .          # after Claude Code has loaded the mod once
```

`hooks/signals.ts` is the pure logic (file → DQL, dtctl envelope → rows);
`hooks/register.tsx` wires it to Claude Code's events. The typings and
`tsconfig.json` are written by Claude Code when it loads the mod and are not
committed.
