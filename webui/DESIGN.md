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
The metrics come from whichever family a service reports: requests, service-mesh requests
(no OneAgent), messaging consumers, function invocations. The families are not added up,
because `dt.service.request.count` already counts a OneAgent service's messaging and function
invocations. The others only fill in where it has no data.
Service page: three vital charts that share one crosshair, followed by Logs,
Traces, Problems, Events and Related tabs.

### J4 — "Show me the failing requests" (debugging)
Traces → errors lens → click a trace → **waterfall** in a side panel. Failed
spans are red, a gutter shows each span's self time, span attributes appear on
click, and one more click goes to the service or to the logs for that trace.

### J5 — "Search the logs" (everyone)
Logs explorer: a stacked level histogram (click a bar to zoom into it), the same
filter popup (`F`) as every list (level, namespace, service and source first, then
every field and primary tag in the logs, values counted from a sample on big
tenants), and a full-text search that compiles to DQL as you type (the DQL is visible). The stream is virtualized, and
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

### J9 — AI observability, built around how AI engineers actually work
There are four questions, each with its own entry point. Everything comes from the OpenTelemetry GenAI
conventions (`gen_ai.*` spans) plus LLM-as-judge results.

1. **"What are people asking my agents, and how did it go?"** *Conversations* is the default view.
   It is a list of conversations (`gen_ai.conversation.id`), and each row reads like an inbox: the user's
   opening prompt, the agent's final answer, the agent and service, the steps (LLM and tool calls,
   tool failures), the tokens and how much of them was cached, the duration, and the status. A big search box searches
   *content*: prompts, answers, tool commands, and conversation or trace IDs. A DQL join first
   selects the matching conversations and then aggregates all of their spans, so the counts stay
   complete. The search box is also reachable from ⌘K ("Search AI conversations for …"), and pasting a
   conversation UUID opens it directly.
2. **"Why did this run go wrong?"** The *conversation replay* puts the outcome first: what the user
   *asked* and the *final answer*. The answer is taken from a finishing tool's argument such as
   `complete_run.summary` when there is one, otherwise from the last message. A *run timeline* (a Gantt
   with an LLM lane and a tool lane) shows where the time went, with an LLM-vs-tools breakdown, the token totals
   and a sparkline of the context size. Below that, *How it got there* numbers the turns. Each turn is one LLM call
   (model, latency, tokens), with its collapsible reasoning, its message, and the tool calls it
   requested, each *paired with its execution* (args rendered readably, duration, failure).
   Consecutive bookkeeping calls such as `todo_create ×6` collapse into one row. Every LLM call and tool opens in a
   maximizable panel. HTTP and proxy child spans that only share the conversation ID are left out.
   Every trace that contains GenAI spans shows an "✦ N LLM calls · replay conversation →" summary.
3. **"Is it fast, reliable and affordable?"** The headline numbers (calls, tokens, cache-hit rate, TTFT, tool
   failure rate, eval pass rate) and the charts stay on top. Each table drills into the next step:
   a *model* opens its LLM calls, an *agent* opens its conversations, and a *tool* opens its executions
   (failures first), each linked to its conversation or trace.
4. **"Is quality getting better or worse?"** *Evaluations* starts with a provenance banner that
   explains where the numbers come from: the event type, who emitted them, the OpenPipeline route,
   and the time range. There are four views of the results:
   * *By question* lists the questions the agent keeps failing, with a score trend for each.
   * *By run* lists evaluation batches and their pass rates, to spot regressions.
   * *Failing criteria* lists the expectations that answers miss most.
   * *Results* shows every verdict with its per-criterion reasons.

### J10 — "Write the query" (power user)
The DQL editor uses Grail's own language services:
- **Completion** from `query:autocomplete`: commands, functions with their synopsis and docs, data objects, and field names that exist in *your* data.
- **Value completion** that Grail doesn't offer: after `field ==`, the editor runs your query's own prefix and lists the field's top values with record counts.
- **Live diagnostics** from `query:verify`, underlined at the exact character range.

The editor is CodeMirror 6, loaded only on the Query page. Keys: ⌘↵ runs, ⌘Space completes, ⌘↑/↓ walks
history, ⌘/ comments, ⌘F searches; brackets match and auto-close.

### Cross-cutting details
* **Every ID resolves to a name automatically.** All entity IDs rendered anywhere (inspector, query
  results, evidence, chips) go through one resolver that batches lookups per tick into a single
  Smartscape lookup. IDs Smartscape doesn't know fall back to their classic `dt.entity.*`
  table, so `PROCESS_GROUP-…` reads as "Linux System".
* **Tabs show their data before you click them.** Every tab's query runs up front in the same
  streamed batch. Tabs show their record counts, empty tabs are dimmed and struck through (with "No data
  in this timeframe" on hover), and switching tabs is instant. The active tab's DQL is one click away.
* **Only interactive rows look interactive.** Table rows show a pointer and hover state only
  when clicking them does something.
* **Every detail panel is maximizable.** Use the button, double-click the header, or press `M`. The
  choice is remembered. `Esc` first restores the panel, then closes it.
* **Lists filter by facets, without a facet sidebar.** Lists (services, hosts, Kubernetes, changes,
  problems, vulnerabilities) have one filter field. Free text filters as you type, and the field also
  suggests facet values with counts (`ns:` picks a facet, ⇧↵ excludes a value). Chosen values become
  chips in the panel header. Facetable column headers have a value menu, and a hovered cell offers + or −.
  Numbers are bucketed (CPU ≥ 90 %, failure rate, latency), and filters live in the URL.
* **Every tag is a filter, one key away.** `F` opens a two-pane popup (as in dynatui): fields on the
  left (curated facets, then primary tags, Kubernetes labels and annotations, AWS, Azure and GCP tags,
  then raw attributes, ranked by how many records carry them), values with counts on the right.
  These filters run in DQL, so they reach records beyond the loaded rows and beyond the row cap,
  and "not set" finds what is missing a tag. Services filter on metric dimensions, so primary tags
  work there too. On an entity page, every tag links to all entities of that type with the same tag.
* **Entities show the metrics they have.** The Metrics tab lists every metric series that carries the
  entity's `dt.smartscape.<type>` dimension, such as CloudWatch metrics for RDS, EC2, ELB and EKS, or OTel and
  Kubernetes metrics. Types without curated vitals use the discovered metrics as headline charts.

### Tenants
The context pill in the top bar is a tenant switcher. It lists every context in the dtctl config
(searchable, with safety level) and is also available from ⌘K ("Switch to …"). Switching affects
only this server process; the config file's current context is unchanged. The page reloads on the
same section and timeframe, and detail pages fall back to their list. Cached results and running
queries of the previous tenant are dropped, and cache keys carry a tenant generation, so nothing
leaks across tenants.

### Built for big tenants
Grail scans are bounded: the default scan limit is 500 GB, and a big tenant writes hundreds of GB of
logs per hour. Every heavy view is designed for that limit.
* **Charts sample adaptively.** The log histogram, log facets and error-log trends first run a
  1:10,000 sampled count (about 0.1 s). From it they estimate the bytes the real query would scan and pick the
  smallest `samplingRatio` that stays under a 50 GB budget. Counts are scaled back up and marked ≈,
  and a badge explains why. If a response still reports the scan limit, the ratio rises 10× automatically.
  Small tenants run exact queries.
* **Lists read a narrower window.** Record lists and per-conversation aggregates can't be sampled,
  because sampling drops records. When the span volume is over budget, the AI page reads the most
  recent slice of the timeframe that fits the budget. A banner says so and offers to scan the full range.
  Where the app emits OTel GenAI metrics (`gen_ai.client.*`), the call chart reads them over the full range.
* **Abandoned work is cancelled.** Each distinct query runs once, shared by every panel that wants
  it. The server counts how many callers are still waiting for it, and the browser aborts a batch
  once every query in it has been abandoned (navigation, a new timeframe). When the last caller leaves,
  the query is cancelled at Grail and frees its slot. Stale work never queues the next page.
* **Query activity is visible.** A top-bar indicator shows queries running out of the 8 slots and
  those waiting. Its popover lists in-flight queries (each cancellable), recent executions with duration,
  scan size, queueing delay and outcome, and totals (requested, ran on Grail, served from cache or shared).
  When every slot is busy and queries wait, it says so.
* **Lookups by ID never scan a week.** Trace IDs aren't indexed. Traces are first located with a
  cheap aggregate over progressively wider windows: around the time the link carries (`?t=`),
  then the page timeframe, 24 hours and 7 days. Then they load from their own window. LLM-call and
  conversation links carry their time too.

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

Since then: Experience (RUM), AI observability (conversations, LLM and tool calls, evals), automatic
ID → name resolution, maximizable panels, facet filtering, the attribute and tag filter popup,
discovered entity metrics, adaptive
sampling for big tenants, query cancellation and the Activity view, and the in-app tenant switcher.

Not built (yet): segments, writes of any kind, session replay playback, metric explorer,
custom dashboards.

## 8. Measured (PoC, real tenant)

* Batched round-trip for three typical queries through `dtctl serve web`: **~100 ms**. The CLI's
  ~600 ms per query is almost entirely process start and auth, which the long-lived server avoids.
* A repeat visit, or Pulse→Services (shared cache entry), paints from cache in **0 ms network**.
* JS bundle: **~200 KB gzipped**. Fonts are self-hosted, and the browser fetches only the subsets it needs.

## 9. Development

```bash
make build-webui                  # npm ci + vite build → pkg/webui/dist (embedded via go:embed)
make test-webui                   # vitest (lib/) + Playwright smoke suite against a mocked /api
export DTCTL_DEVELOPMENT=serve    # `serve` is a development-tier feature (or `development: {serve: true}` in config)
go build -o dtctl . && ./dtctl serve web

# UI hot reload: run the Go server for /api and Vite for the UI
./dtctl serve web --no-open &
cd webui && npm run dev           # http://localhost:5173, /api proxied to :7878
```

Code map: `pkg/webui` (HTTP server: batch, cache, guards, static), `cmd/serve.go` (command, hung
under the development-tier `dtctl serve` from `pkg/serve`, +
wiring to `pkg/exec` / `pkg/resources/document`), `webui/src/lib/dql.ts` (every query the UI
runs), `webui/src/pages/*` (one file per destination), `webui/src/components/*` (design system).

Building or changing a screen? Follow [UI_GUIDELINES.md](UI_GUIDELINES.md): page shapes, the
component catalog, states, formatting, wording, keyboard, color and the rules for big tenants.

## 10. Further ideas

* Saved views ("my services") pinned in the rail.
* A watch mode with live tail for logs over a streaming endpoint.
* "Explain this" with Davis CoPilot on any panel.
* A shareable link that encodes the whole state, plus an "open in the real Dynatrace UI" intent link everywhere.
