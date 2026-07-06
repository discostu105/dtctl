# TUI Implementation — Field Notes & Learnings

Working notes captured while implementing Phases 2–3 of the interactive TUI
(`dtctl tui`). This complements [TUI_DESIGN.md](TUI_DESIGN.md) (the design) and
[ARCHITECTURE.md](ARCHITECTURE.md): it records the **DQL/Grail facts validated
against a live tenant**, the **TUI extension model**, and the **traps** that
cost time — the things you cannot infer from reading the code alone.

> **Privacy:** all identifiers below are illustrative placeholders. Never paste
> real environment IDs, entity IDs, or tenant/app names into the repo.

---

## 1. The DQL substrate — facts validated live

Every query in `pkg/tui/catalog/` was checked against a real tenant. Grail is
introspectable, but it **fails silently**: a wrong field, wrong id type, or
wrong data-object name returns `{"records": []}` with exit 0 — *no error*. This
is the single most important thing to internalize. Always confirm a query
returns non-empty before trusting a field name.

### 1.1 Smartscape is a command, not a table

- `smartscapeNodes "TYPE"` and `smartscapeEdges "*"` are **starting commands**,
  not fetchable data objects. `fetch smartscapeNodes` → `UNKNOWN_DATA_OBJECT`;
  `fetch smartscape.nodes` → `DATA_OBJECT_NOT_SUPPORTED`. A type argument is
  mandatory (`smartscapeNodes` alone → `NO_PARAMETERS_FOR_COMMAND`).
- Multi-type and wildcard both work in one query:
  `smartscapeNodes "K8S_DEPLOYMENT", "K8S_STATEFULSET", "K8S_DAEMONSET"` and
  `smartscapeNodes "K8S_*"` — so a combined "workloads" view is **one query**,
  no client-side merge.
- `from:` is a command parameter: `smartscapeNodes "K8S_POD", from:now()-2h`.
  Without it you get every entity alive at *any* point in the default window —
  including dead pods (`Succeeded`/`Failed`). A "current" view must filter
  `k8s.pod.phase` or lifetime recency.
- Nodes also carry `references` (a lazy forward-only adjacency object) and, for
  K8s, `k8s.object` (the entire manifest as a multi-KB JSON string). **Always
  project** with `| fields ...` or `| fieldsRemove references, k8s.object` in
  the TUI — never fetch full records into a list.

### 1.2 The two ID casts you cannot forget

| Field kind | Comparison requires | Silent-empty trap if omitted |
|---|---|---|
| `dt.smartscape.*` ids (nodes, edges, metric dims) | `toSmartscapeId("SERVICE-…")` | yes |
| span `trace.id` (a UID type) | `toUid("32-hex")` | yes |
| log `trace_id` (a plain string) | plain `== "…"` | n/a (works as string) |
| `dt.entity.*` ids | plain string | n/a |
| plain names (`k8s.pod.name`, `service.name`) | plain string | n/a |

The asymmetry between **span `trace.id`** (UID → `toUid`) and **log `trace_id`**
(plain string) is the crux of the log↔trace jump: a log's `trace_id` value drops
straight into `toUid()` to fetch its spans, and a span's `trace.id` (rendered as
32-hex) drops into a plain `trace_id == "…"` to fetch its logs. Both directions
were round-trip validated.

### 1.3 Signal scoping: entity fields differ per record type

The costliest bug of the session. **Log records carry `k8s.*` name attributes
but NO `dt.smartscape.k8s_*` fields.** A logs query scoped only by
`dt.smartscape.k8s_pod == toSmartscapeId(...)` returns *empty* — not an error, a
silently empty table. Davis events, by contrast, carry both.

`catalog.SignalFilter` therefore or-s three matchers: the smartscape id, the
legacy `dt.entity.*` id, **and** the plain `k8s.*` name (via `k8sNameFilter`),
plus `dt.smartscape_source.id`. A nonexistent field in an or-chain compares as
null (harmless), so over-specifying is safe; under-specifying loses data.

Spans, meanwhile, *do* carry `dt.smartscape.service` / `dt.smartscape.k8s_*` /
`dt.smartscape.container` — but **not** for hosts. Hence
`catalog.SpanScopable(type)` gates the `s` (traces) drill to types that spans
can actually be filtered by.

### 1.4 k8s.object parsing

- Parse with the DPL JSON matcher: `| parse k8s.object, "JSON:obj"`, then
  bracket-access `obj[status][phase]`. **`parseJson()` does not exist**
  (`UNKNOWN_FUNCTION`).
- The JSON matcher yields **all numbers as strings** — `restartCount:"0"`,
  `capacity.pods:"29"`. Wrap in `toLong()` for arithmetic
  (`sum(toLong(cs[restartCount]))`); booleans parse as real booleans.
- DaemonSet replica status uses different keys than Deployment/StatefulSet:
  `numberReady`/`desiredNumberScheduled` vs `readyReplicas`/`replicas`. Use
  `coalesce()` across both for a combined workloads READY column.
- `expand cs = obj[status][containerStatuses]` **drops pods with no
  containerStatuses** (some Pending pods). Acceptable for a ready/restart
  summary; know it truncates.
- Kubernetes omits `readyReplicas` entirely when zero are ready — treat missing
  as `0/N`, not `N/A`.

### 1.5 JSON serialization quirks (dtctl `-o json`)

- **Longs, counts, durations, status codes serialize as JSON strings**
  (`"37"`, `"500"`, `"5194"`), while **floats stay numbers** (`3.1`) and
  **booleans stay booleans**. Sort/parse code must handle both — see
  `catalog.sortable()` and the numeric-string `strconv.ParseFloat` fallback in
  `table.go`.
- Grail **durations** serialize as a string of **nanoseconds** (`"4845165"` =
  4.85ms). `toString(duration)` instead renders seconds. `catalog.FormatNs`
  parses the raw ns.
- **timeseries `interval` is a nanosecond string** (`"300000000000"` = 5m);
  series arrays are `[]any` of `float64` and **`nil`** (empty buckets). Sparkline
  code must tolerate nulls anywhere (see `catalog.FloatSeries`).

### 1.6 Metrics catalog & batched enrichment

- **There is no `dtctl metrics` subcommand.** Discover keys via DQL:
  `fetch metric.series | filter startsWith(metric.key, "dt.kubernetes") |
  summarize count(), by:{metric.key}`. `fetch metric.series | filter metric.key
  == "…" | limit 1` reveals every dimension a series carries (use to find the
  right filter field).
- Batched sparkline enrichment (one query per visible page) works exactly as
  hoped and stays under ~1s for the whole set:
  `timeseries cpu = avg(…), by:{k8s.pod.name}, filter: { in(k8s.pod.name,
  {"a","b"}) }, interval: 5m`. The `in(field, {"a","b"})` **curly-brace set
  literal** is the working syntax; for smartscape ids wrap each element in
  `toSmartscapeId(...)`. Result: one record per key with the by-field as a flat
  JSON key beside the series array.
- K8s metric **units**: all CPU metrics are **millicores** (a 500m limit shows
  as `500`); memory is **bytes**.
- **No restart/OOM metric exists** on the tenant. Proxy via
  `dt.kubernetes.events` filtered by `k8s.event.reason` (OOMKilling/BackOff),
  or `dt.kubernetes.containers` by `container_state`. Raw K8s events are **not**
  in the `fetch events` table (only `DAVIS_EVENT`/`DAVIS_PROBLEM` there).
- Not every entity reports every metric: services with no traffic simply have
  **no record** (treat missing as "no data", not zero). The service timeseries
  also emits a record with a **null by-key** — skip it when building the id→series
  map.

### 1.7 dt.davis.problems is a transition log, not a problem list

Rows are **status-transition events** (many rows per problem). To get currently
open problems you must:
```
| sort timestamp asc
| summarize { status = takeLast(event.status), … }, by:{display_id}
| filter status == "ACTIVE"
```
Affected-entity id arrays differ by era: `smartscape.affected_entity.ids` uses
`K8S_*` ids and is often **null**; `affected_entity_ids` (classic) uses
`CLOUD_APPLICATION-…`/`KUBERNETES_CLUSTER-…` and is fuller but doesn't match
`dt.smartscape.host/service` directly. Match with `matchesPhrase` over both.

### 1.8 smartscapeEdges — the relations panel

- Default projection is only `{source_id, target_id, type}`. `source_type`/
  `target_type` are **lazy** — add `| fields source_id, source_type, type,
  target_id, target_type`. Edge records **never** carry names.
- Query **both directions in one query** — source-only misses incoming edges
  (`K8S_SERVICE --routes_to--> pod`, `CONTAINER --is_part_of--> pod`):
  `| filter source_id == toSmartscapeId(ID) or target_id == toSmartscapeId(ID)`.
- Resolve names in a second batched query:
  `smartscapeNodes "*" | filter in(id, {toSmartscapeId(...), ...}) | fields id,
  name, type`. **Not every edge endpoint has a node** (`K8S_SECRET`,
  `K8S_CONFIGMAP` have edges but no node records) — fall back to the raw id.
- Both queries ~0.7s; fine for an interactive panel with a brief spinner.

### 1.9 AWS inventory is name-poor

- On a lightweight-poller tenant, `name` is an **empty string** (not null) on
  ~98% of AWS nodes. Display fallback:
  `coalesce(if(name != "", name), ` + "`tags:aws`[`Name`]" + `, aws.arn)`.
  Note the field is literally named `` `tags:aws` `` (colon) and needs backticks.
- `aws.account.id` / `aws.arn` are 100% populated; `aws.region` ~97% (absent on
  global resources like S3). `aws.object` may not exist at all (no instance
  type/state/AZ then).
- AWS-only census in one query: `smartscapeNodes "*" | filter startsWith(type,
  "AWS_") | summarize count(), by:{type}`.
- CloudWatch metrics embed the dimension in the key name
  (`cloud.aws.rds.CPUUtilization.By.DBInstanceIdentifier`); scope by
  `dt.smartscape_source.id == toSmartscapeId(...)`.

---

## 2. TUI extension model — how to add a view

The catalog is **declarative data**, not screens. Adding an entity/signal view
is a `catalog.Spec` literal; the generic `tableView` engine renders it.

```go
var myViewSpec = &Spec{
    Name, Aliases, Kind,       // KindEntity | KindSignal
    Desc,
    Query   func(Scope) string // scope-aware DQL; compose s.Entity/s.Arg/s.TraceID
    Columns  []Column          // Field or Value; Width (0=flex); Right; Class; Sort
    Entity   func(rec) *Entity // the entity a row stands for (scope for drills)
    Drills   map[string]string // 'l'→logs, 's'→traces, 'm'→metrics, …
    EntityScoped bool          // entity view whose Query composes s.Entity
    EnterTarget  string        // k9s containment: enter → child view (detail moves to 'd')
    EnterArg     func(rec) string // census row → typed browser via Scope.Arg
    Trace        func(rec) string // row's trace id (log→trace, trace→waterfall)
    Enrich   *EnrichSpec       // batched sparkline columns
}
```

Key design points learned:

- **`Scope` carries everything a query composes**: `Entity`, `Timeframe`,
  `Arg` (generic-browser node type), `TraceID`. Keep it small; add a field
  rather than smuggling state through globals.
- **`Spec.UsesScope()`** (`Kind==Signal || EntityScoped`) decides whether the
  pin (`.`) and the scope crumb apply — a view that ignores scope must not
  *claim* it in the breadcrumb.
- **`EnrichSpec`** = one batched `timeseries ... by:{…}, filter: in(…)` per
  refresh. `Key(rec)` is the row's join value, `By` the result field carrying
  it; results land under `EnrichKey(alias)` and render via `SparkColumn`.
  Failures degrade to blank cells by design — never surface an enrichment error.
- **Curated where curation adds value, introspection everywhere else.** The
  generic `resources` view browses *any* Smartscape type from the census; only
  pods/hosts/services/etc. get bespoke columns.
- **Two special `Drills` targets** beyond view names: `"metrics"` (canned
  charts) and `"trace"`/`"trace-logs"` (waterfall / trace-scoped logs).
  `EnterTarget: "waterfall"` is the sentinel for the bespoke waterfall screen.

### Bespoke screens (not table-driven)

`home`, `query` (DQL escape hatch), `waterfall`, `relations`, `detail`,
`inspector`, `metrics` each implement the `viewModel` interface directly.
Optional capability interfaces let the app treat them uniformly:
`selectionProvider` (pin/relations/yank/open), `dqlProvider` (ctrl+q reveal),
`traceProvider` (waterfall's trace id).

---

## 3. bubbletea message-flow patterns

- **Views mutate in place (pointer receivers) and stay alive when covered**, so
  `esc` restores them with data + cursor intact. The breadcrumb stack owns the
  lifetimes.
- **Stale results are dropped by `(owner, seq)`, never by cancellation.** The
  DQL executor prints cancellation notices to stderr, which would tear the
  alternate screen — so in-flight queries run to completion and old results are
  discarded on arrival. Every async query is a `tea.Cmd` goroutine.
- **Distinct owners disambiguate multiplexed queries on one view's seq:**
  `enrichOwner{v}`, `nameOwner{v}`, `panelOwner{v, idx}`. A view's list query,
  its enrichment query, and (home) its per-panel queries all share the view's
  `seq` generation but route by owner type.
- **`dataMsg` is broadcast to every view on both the live and `prev` stacks
  (deduped)** so a parent still loading below a drill-down completes, and detail
  pages forward results to their lazily-started tabs. Views drop results they
  don't own.
- **`claimKey`** (a no-op `tea.Cmd` returning `nil`) lets a child consume a key
  the app would otherwise act on — e.g. `esc` that clears a filter instead of
  popping the stack. bubbletea discards `nil` messages, so returning a real
  no-op command is how you say "handled, do nothing."
- **`InputActive()` gating**: while a text input (filter, command bar, query
  editor) owns the keyboard, global single-letter keys (`q`, `x`, `.`, hotkeys)
  must not fire. Every view reports this; the app checks it before its own
  key switch.

---

## 4. Server-side scope beats client-side filter for capped lists

A subtle correctness bug: jumping to a limit-capped list (e.g. pods, 800 cap)
and applying a **client-side** `/`-filter can miss a row that fell outside the
fetched page. When you know the exact target (a pod name from a home-panel
warning), push it into the **query** as `Scope.Entity` so the server narrows,
not the client. See the home "kubernetes warnings" panel action and
`k8sScopeFilter` for `K8S_POD`.

Corollary: the k8s-warnings panel looks back 24h, so its jump must widen the
timeframe to 24h too — otherwise a pod that died hours ago is gone from a 2h
window.

---

## 5. Verifying the TUI live

The TUI refuses to start in an auto-detected agent environment and captured
panes strip ANSI — so drive it in tmux:

```bash
tmux new-session -d -s t -x 150 -y 40 \
  "$BIN tui --no-agent 2>err.log; sleep 60"
tmux send-keys -t t 4        # one keypress per call — batching coalesces into one KeyMsg
tmux capture-pane -t t -p    # -e to see the reverse-video selected row
```

- **`--no-agent`** is mandatory from a Claude/agent session (agent env vars
  propagate into tmux).
- **One `send-keys` per keypress.** Multiple keys in one call can merge into a
  single bubbletea `KeyMsg` ("jjjjj") that matches nothing.
- Stress tiny terminals (`-x 60 -y 12`) — layout math must not underflow
  (`strings.Repeat` with negative counts panics; clamp bar/column widths).
- A rich demo tenant with a K8s cluster, AWS inventory, Postgres, GenAI, RUM,
  and live problems/vulns exercises every view in one place.

### Testing without a TTY

bubbletea models are pure (`Update(msg) → cmd`): drive with synthetic messages,
chase returned navigation `tea.Cmd`s back into the app, and seed rows via the
`dataSource.runFn` seam (never hit HTTP). Assert on navigation, scope
composition, and DQL strings. Catalog `Query` templates are golden-tested
against fixture scopes — cheap regression protection for scope composition.

---

## 6. Live-exploration workflow (how the DQL above was validated)

The DQL facts in §1 came from a fan-out of read-only probe agents, one per
domain (K8s entities, K8s metrics, traces, AWS, edges, enrichment, breadth),
each running `dtctl query '…' -o json` against the tenant with a strict schema
for validated-query / fields / gotchas output. Rules that made it reliable:

- Always small limits and a 2h window; widen only when empty.
- Treat empty results as suspicious (silent-empty trap) — verify non-empty
  before declaring a field name "validated."
- Report **exact copy-paste DQL**, field names with one example value each, and
  every gotcha hit. The gotchas were worth more than the happy-path queries.

This front-loaded exploration is why nearly every query worked on first live
drive — the traps were already known before a line of view code was written.
