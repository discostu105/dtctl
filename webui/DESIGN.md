# dtctl web — design proposal

> Proof of concept. Served locally by `dtctl serve web`. Read-only.
> Goal: find out what a *best-in-market* observability web UI could feel like
> when it is built around speed, a few well-designed journeys, and care for small details.

## 1. Why the current web UI disappoints, and what we do instead

The complaints about observability UIs are consistent: they're slow, there are
too many apps, and you can't tell where you are. dynatui shows that the same data
can feel fast and coherent, but only for people who live in a terminal. This
proposal brings dynatui's model to the browser.

| Complaint | Root cause | Our answer |
|---|---|---|
| **Slow** | Each app boots its own bundle. Panels fetch one by one. Every navigation shows spinners. | One ~200 KB app. Queries go out as a single **streamed batch**, so panels paint as results arrive. Results come from a **stale-while-revalidate cache** in both the server and the browser. Data is **prefetched on hover**. Long lists are virtualized. Navigating back is instant. |
| **Not intuitive** | The UI is organized around apps (one per product team), not around the questions people ask. | The UI is organized around **journeys**: "what's wrong?", "is my service OK?", "why did this fail?". Every ID and entity name is a link. Your scope and timeframe come with you when you follow one. |
| **Not modern** | Heavy chrome, low information density, mouse-only. | A dense but calm layout, **keyboard-first** (`⌘K` for everything, single-key navigation). Dark and light themes are designed separately (light is not an inverted dark). Typography is crisp and numbers are tabular. |
| **Opaque** | You can't see what a panel is computing or how long it took. | **Every panel is honest.** It shows its DQL, execution time, and whether the result came from cache, and it can open in Query with one click. A panel shows "No data in timeframe" only when that is true; errors are never swallowed. |

## 2. Product principles

1. **Speed is the feature.** Target <100 ms from click to first useful paint for
   anything visited before, and progressive paint for everything else. Show the
   page layout immediately with shimmer placeholders. Never block the page on its slowest panel.
2. **Answer first, detail on demand.** Every page starts with what deserves
   attention (red things first). Detail opens in place in side panels, so you
   keep your list and your position in it.
3. **Everything is connected.** The data model is *entity + timeframe → signals*,
   the same as dynatui's `Scope`. Any entity can show its logs, traces, events,
   problems, metrics and relations, all through one entity page.
4. **Keyboard and mouse are equal citizens.** `⌘K` palette, `g`-prefixed jumps,
   `/` to filter, `t` for timeframe, `r` to refresh, `j/k` + `↵` in lists, `esc` to close
   panels, `?` for the cheat sheet. Rows are real links (`⌘`-click opens a new tab).
5. **Love for the user.** Copy-on-click IDs, relative time with absolute time on
   hover, URL-addressable state (every view can be shared), recents, deep links into
   the Dynatrace apps when you need the full power, and a "copy as dtctl command"
   action.

## 3. Information architecture

```
┌─ rail ─┐┌─ top bar: breadcrumb · ⌘K search · timeframe · refresh · env ───────┐
│ Pulse   ││                                                                     │
│ Problems││                          page                                      │
│ Services││                                                                     │
│ K8s     ││                                                                     │
│ Hosts   ││                                                                     │
│ Logs    ││                                                                     │
│ Traces  ││                                                                     │
│ Changes ││                                                                     │
│ Security││                                                                     │
│ ─────── ││                                                                     │
│ Query   ││                                                                     │
│ Smartscape│                                                                    │
│ Documents││                                                                    │
└────────┘└─────────────────────────────────────────────────────────────────────┘
```

* **Pulse** (home): one triage screen that answers "is anything on fire right now?"
* **Signals**: Problems, Logs, Traces, Experience (RUM), AI (GenAI), Changes (events/deployments), Security.
* **Topology**: Services, Kubernetes, Hosts, Smartscape (all entity types).
* **Power**: Query (a DQL workbench) and Documents (dashboards and notebooks, which open in Dynatrace).

There are 15 destinations, all one keystroke away (`g p` problems, `g s` services, `g u` experience, `g a` AI, …).

## 4. Key journeys

### J1 — "Is anything wrong?" (on-call, morning check)
Pulse → the KPI strip (active problems, failing services, error logs, critical
vulnerabilities, deployments) → the active problems list → click a problem.
**Love:** KPI tiles are links. A sparkline shows the trend, not only the current
level. Deployment markers appear on the error chart, so "did a deploy cause
this?" can be answered at a glance.

### J2 — "Why is this problem happening?" (incident)
Problem page: a header with status, duration (live-ticking while the problem is
active), category, and affected entities as chips. Below it:
* an **impact timeline** of error logs and failed spans in the problem window (−30 m … end), with the problem band shaded,
* **Davis analysis** (rendered markdown) and an evidence list,
* tabs for **Logs · Traces · Events**, scoped to the affected entities and the problem window (the global timeframe deliberately does not apply here; this matches dynatui),
* an **Open in Dynatrace** deep link (intent URL).

### J3 — "Is my service healthy?" (service owner)
Services list: RED metrics (throughput, failure rate, latency) as sparklines plus
current values, sortable, with an instant client-side filter. Failing services sort first.
Service page: three vital charts that share one crosshair, followed by Logs,
Traces, Problems, Events and Related tabs.

### J4 — "Show me the failing requests" (debugging)
Traces → errors lens → click a trace → **waterfall** in a side panel. Failed
spans are red, a gutter shows each span's self time, span attributes appear on
click, and one more click goes to the service or to the logs for that trace.

### J5 — "Search the logs" (everyone)
Logs explorer: a stacked level histogram (click a bar to zoom into it), facets
(level, namespace, service, source) with counts, and a full-text search that
compiles to DQL as you type (the DQL is visible). The stream is virtualized, and
clicking a record expands it. **Love:** log levels have consistent colors; JSON
bodies are pretty-printed; trace IDs are links.

### J6 — "Something weird — let me query" (power user)
Query: a DQL editor with syntax highlighting and history (kept per environment), `⌘↵` to
run. The result renders as a **table, or a chart when it is a timeseries**,
chosen automatically. The editor shows scanned bytes and execution time, and any
query in the app can be opened here.

### J7 — "What is this thing and what is it connected to?" (exploration)
Smartscape: a census of entity types, then instances of a type, then the
**entity page**. The Related tab groups edges by verb and direction
(runs on ▸, calls ▸, ◂ called by), and every node is clickable.

### J8 — "How do real users experience my app?" (frontend / product)
Experience: each frontend with p75 Core Web Vitals (LCP, INP, CLS, TTFB), each rated
good / needs improvement / poor by shape *and* color. Pick a frontend, then work through its sessions,
grouped errors and pages. A session opens as a journey timeline (views, navigations,
actions, errors) on the session's own time axis. Requests that carry a trace ID link
**straight into the backend trace waterfall**, so one path runs from a click to the database.
**Love:** real users only by default, because synthetic monitors also emit RUM. RUM data is
sparse, so the window is floored at 24h and the page says so. Flags, device icons and replay markers are shown.

### J9 — "What are my agents and LLMs doing, and are they any good?" (AI engineering)
AI: LLM calls, tokens, prompt-cache hit rate, time to first token, tool failure rate
and eval pass rate. Below that are tables of models, agents (each one links to its GenAI entity and spans) and tools.
Any LLM call opens a **conversation view**: role-tagged turns, with reasoning, tool calls and tool
results shown as collapsible blocks, and system instructions collapsed. LLM-as-judge evaluations
show each criterion as met or not, with the judge's reason. All of this comes from the OpenTelemetry GenAI conventions.

### Cross-cutting details
* **Every ID resolves to a name automatically.** All entity IDs rendered anywhere (inspector, query
  results, evidence, chips) go through one resolver that batches lookups per tick into a single
  Smartscape lookup. IDs Smartscape doesn't know fall back to their classic `dt.entity.*`
  table, so `PROCESS_GROUP-…` reads as "Linux System".
* **Every detail panel is maximizable.** Use the button, double-click the header, or press `M`. The
  choice is remembered. `Esc` first restores the panel, then closes it.

## 5. Visual design

* **Not Strato.** A neutral, high-density style in the spirit of Linear, Vercel and
  Raycast: a near-black canvas, surfaces separated by hairlines (not shadows),
  one accent color (indigo `#7c83ff`), and status colors reserved for status.
* **Type:** Inter Variable (UI, 13 px base) and JetBrains Mono (IDs, DQL, log
  content), both self-hosted so pages render with no font flash and no network
  fetch. Tabular numerals in every column.
* **Color roles:**
  - Status: critical `#f0505a`, warning `#f5a524`, ok `#3dd68c`, info `#7c83ff`. Status is always shown with an icon or label, never by color alone.
  - Log levels: ERROR/SEVERE → critical, WARN → warning, INFO → neutral, DEBUG/TRACE → muted.
  - Chart series: the validated eight-slot categorical palette in fixed order (blue, orange, aqua, yellow, magenta, green, violet, red), with separate dark and light steps.
* **Charts** use uPlot (canvas, ~45 KB, renders 100k points in a few milliseconds).
  - Marks: 2 px lines, a 10 % area wash, hairline grids, one y-axis.
  - Interaction: a crosshair with a tooltip on every chart, and synchronized cursors within a page.
  - Bars are ≤ 24 px with 4 px rounded data ends.
* **Motion:** 120–160 ms ease-out, only for opening and closing panels. Data never animates in (it shouldn't look as if it is still loading).

## 6. Architecture

```
browser (React 19 SPA, embedded in dtctl)            dtctl serve web (Go)
┌──────────────────────────────────────┐   POST    ┌────────────────────────────┐
│ TanStack Query cache (SWR, prefetch) │ /api/batch│ batch → fan-out (≤8 conc.) │
│ batcher: queries in one tick → 1 req │──────────▶│ single-flight + TTL cache  │──▶ Grail (DQL)
│ NDJSON stream → resolve per query    │◀──────────│ stream each result as done │
└──────────────────────────────────────┘  NDJSON   └────────────────────────────┘
```

* **Server** (`pkg/webui`, `cmd/serve.go`): reuses the current context, so the
  token never leaves the process. It listens only on `127.0.0.1`, enforces a
  loopback Host header (DNS-rebinding guard), and requires an `X-Dtctl-Web`
  header on the API (CSRF guard). There are no mutating endpoints.
  - Relative timeframes (`now-2h`) are resolved on the server, so the cache key stays stable across refreshes.
  - Assets are gzipped once at startup and served `immutable`.
* **Client** (`webui/`): Vite, React 19, TypeScript, Tailwind v4, TanStack Query
  and Virtual, wouter (routing), cmdk (palette), uPlot (charts), and Radix
  (tooltips and popovers).
  - Each feature is a small set of DQL builders plus components; there is no per-page backend.
  - Builders reuse dynatui's proven queries: dual-era entity filters, problem dedupe by `display_id`, and RED via `timeseries`.

## 7. Scope of the PoC

Built: Pulse, Problems (list and detail), Services, Kubernetes (workloads, pods,
nodes), Hosts, entity page (for any Smartscape type), Logs explorer, Traces with
waterfall, Changes, Security (vulnerabilities), Query workbench, Smartscape
browser, Documents, the ⌘K palette, the timeframe picker, and light and dark themes.

Since then: Experience (RUM), AI observability, automatic ID → name resolution, maximizable panels.

Not built (yet): segments, writes of any kind, session replay playback, metric explorer,
custom dashboards, multi-context switching in the UI (restart with `--context`).

## 8. Measured (PoC, real tenant)

* Batched round-trip for three typical queries through `dtctl serve web`: **~100 ms**. The CLI's
  ~600 ms per query is almost entirely process start and auth, which the long-lived server avoids.
* A repeat visit, or Pulse→Services (shared cache entry), paints from cache in **0 ms network**.
* JS bundle: **~200 KB gzipped**. Fonts are self-hosted, and the browser fetches only the subsets it needs.

## 9. Development

```bash
make build-webui                  # npm ci + vite build → pkg/webui/dist (embedded via go:embed)
go build -o dtctl . && ./dtctl serve web

# UI hot reload: run the Go server for /api and Vite for the UI
./dtctl serve web --no-open &
cd webui && npm run dev           # http://localhost:5173, /api proxied to :7878
```

Code map: `pkg/webui` (HTTP server: batch, cache, guards, static), `cmd/serve.go` (command +
wiring to `pkg/exec` / `pkg/resources/document`), `webui/src/lib/dql.ts` (every query the UI
runs), `webui/src/pages/*` (one file per destination), `webui/src/components/*` (design system).

## 10. Further ideas

* Saved views ("my services") pinned in the rail.
* A watch mode with live tail for logs over a streaming endpoint.
* "Explain this" with Davis CoPilot on any panel.
* A shareable link that encodes the whole state, plus an "open in the real Dynatrace UI" intent link everywhere.
