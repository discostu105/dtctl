# Field Notes & Learnings

Working notes captured while implementing dynatui. This complements
[the design doc](../design/tui.md): it records the **DQL/Grail facts
validated against a live tenant**, the **TUI extension model**, and the
**traps** that cost time — the things you cannot infer from reading the
code alone.

> **Privacy:** all identifiers below are illustrative placeholders. Never paste
> real environment IDs, entity IDs, or tenant/app names into the repo.

---

## 1. The DQL substrate — facts validated live

Every query in `internal/tui/catalog/` was checked against a real tenant. Grail is
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
  the TUI's *list* queries — never fetch full records into a list. The
  single-record detail query keeps both (the manifest and containment edges
  are exactly what a detail page is for; the inspector collapses huge JSON by
  default). Gotcha: `references` is **not in the default projection** — it
  comes back only with an explicit `| fieldsAdd references` (or `fields`)
  clause (validated live; `fieldsRemove references` on a list query is
  therefore belt-and-braces, not load-bearing).

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
- **Process/container/infra dimensions** (validated live, box tenant):
  `dt.process.*` series carry `dt.smartscape.process` AND `dt.smartscape.host`
  AND `host.name` — one query lists a host's processes ranked by CPU.
  `dt.process.cpu.usage` / `.memory.usage` are **percent of host**;
  `.memory.working_set_size` is bytes. `dt.kubernetes.container.*` series
  carry `dt.smartscape.container` plus `dt.smartscape.k8s_{pod,deployment,
  statefulset,daemonset,namespace,cluster}` — the workload/namespace/cluster
  vitals scope by their own smartscape id. `dt.host.*` series carry
  `host.name`, and a K8s node's name equals its OneAgent host name (EKS) —
  `MetricScopeFilter` adds a `host.name` arm for `K8S_NODE`, which is how a
  node page charts real utilization (allocatable-only otherwise).
- **PROCESS/CONTAINER Smartscape nodes join by plain names**: both carry
  `host.name`; CONTAINER carries every `k8s.*` name (pod, workload, namespace,
  cluster) plus `container.image.name/.version`; a process' pod lives in
  `` process.metadata[`KUBERNETES_FULL_POD_NAME`] `` (backtick map access
  filters server-side). EC2 has **no `cloud.aws.ec2.*` CloudWatch keys** on
  box — EC2 utilization lives on the OneAgent HOST entity; CloudWatch coverage
  is rds/networkelb/eks/ecr.
- `container.image.name` on CONTAINER nodes **flaps** between the repo path
  and a bare 12-hex image id (observed live minutes apart) — render whatever
  is there, don't parse it as a URL.

### 1.6b The `metrics` command — the explorer substrate

- The **`metrics` DQL command** enumerates metric *series* — one record per
  metric key + full dimension set, `from:` bounded (`metrics from:now()-2h`).
  It takes no positional args and no `filter:` parameter; narrow with `| filter`
  and aggregate with `| summarize count(), by:{metric.key}` (~0.7s for a busy
  tenant). `| search` and `fieldsSummary` both work after it, so the standard
  table machinery (server search, facets) applies unchanged.
- Per-entity metric discovery is `metrics | filter <scope> | summarize
  by:{metric.key}` — a single pod on a busy tenant showed **~120 keys**
  (dt.kubernetes/containers/process/runtime plus custom OTel app metrics),
  which is why the explorer view exists at all.
- **OTel-exported service metrics carry `service.name` and legacy
  `dt.entity.service` but NOT `dt.smartscape.service`** — and the legacy
  service id is a *different value* than the Smartscape one. Scoping service
  metrics needs the or-chain in `MetricScopeFilter` (both id eras + the name);
  on the test tenant the name clause grew one service's discovered keys from
  3 to ~35 (http.server.*, db.client.*, gen_ai.*, custom app metrics).
- Metric **metadata is absent**: `metric.unit` / `metric.description` are null
  on every series record and `dt.semantic_dictionary.metrics` doesn't exist.
  Explorer charts render unitless; only canned series carry curated units.
- `dt.service.request.response_time` is **microseconds** (µs), not ms —
  cross-validated against `avg(http.server.request.duration)` (seconds, OTel
  convention) tracking within rounding on the same service.
- **A multi-series `timeseries` query returns ZERO records if ANY requested
  metric has no series at all** for the filter — and `default:` does *not*
  rescue an entirely-absent key (it only fills gaps in existing series). This
  silently blanked the whole pod metrics page for pods without limits set.
  The fix is the two-phase flow in `metricsView`: probe availability with
  `metrics … | summarize by:{metric.key}` first, then compose the timeseries
  from the available subset (`MetricsSpec.AvailabilityQuery` / `.Query`).
- Splitting a metric per dimension is `timeseries value = agg(key), by:{dim}`
  — one record per dimension value. Discover the dims by sampling
  `metrics | filter metric.key == "…" | limit 500` and counting distinct
  values client-side (`discoverDims`); series records carry only dims plus
  `metric.key`, so every other field is a candidate.

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

### 1.10 search & fieldsSummary — the facet substrate

All validated live; implementation in `internal/tui/catalog/facets.go`.

- **`| search "text"` placement is constrained.** It works after the source
  command (`fetch`, `smartscapeNodes`) and after `filter`/`fieldsRemove`, but
  is **rejected after transforming commands** — `parse`, `expand`,
  `summarize` — with `SEARCH_COMMAND_NOT_ALLOWED_AFTER`. Inject it directly
  after the source line (`InjectSearches`), never before the sort/limit tail
  (found the hard way on the pods pipeline). **Chained search stages compose
  as AND** (`| search "*a*"\n| search "*b*"`), so stacked terms need no
  expression syntax.
- **`search` matches whole tokens, not substrings.** `search "fss"` does
  *not* match pod `…-fssb5`; `"fss*"` and `"*fss*"` do. Wrap bare terms in
  `*…*` to get the contains semantics a filter box promises. Matching is
  case-insensitive across all fields.
- **`fieldsSummary <field>, topValues: N`** returns one record per field:
  `{field, count, rawCount, values: [{value, occurrence-count}]}` — counts
  are stringified longs, and **values are stringified even for numeric
  fields** (`"2"` for `logical_cores`).
- **Facet comparisons must go through `toString()`.** `logical_cores == "2"`
  is *silently empty* on a numeric field; `toString(logical_cores) == "2"`
  matches — and composes fine with string fields too, so it is the universal
  exact encoding for a stringified value. Patterns use
  `matchesValue(toString(field), "pay*")`: case-insensitive, `*` wildcards
  allowed at either end, and `toString()` is accepted as its first argument.
- **Array fields facet through the same pattern encoding.**
  `toString(arrayField)` renders the elements into one string
  (`["KUBERNETES_CLUSTER-…"]`), so
  `matchesValue(toString(arrayField), "*ELEMENT*")` matches rows whose array
  contains the element — the inspector's facet-by-array-element rides on
  this. Note `matchesValue` patterns must be **constants**
  (`MANDATORY_PARAMETER_HAS_TO_BE_CONSTANT` with `concat(...)`).
- Facet `filter` stages stay before the sort/limit tail — after a
  `summarize`, that's what makes them filter the exact fields the columns
  (and the facet attribute picker, built from fetched record keys) present.
- **`dt.system.bucket` is the primary narrowing axis — and a whitelist.**
  Buckets are Grail's physical data separation, so a bucket filter prunes
  reads at the source. The field is queryable and `fieldsSummary`-able on
  every bucket-backed table *without* a projection, but only appears in
  responses via `| fieldsAdd dt.system.bucket`. On tables without buckets
  (`dt.entity.*`, `dt.system.buckets`, `dt.semantic_dictionary.*`) any
  reference **fails the whole query** with `FIELD_DOES_NOT_EXIST` — not
  null — so eligibility is the `bucketTables` whitelist (the
  `dt.system.table` values of `fetch dt.system.buckets`, plus the
  `dt.davis.*` / `dt.synthetic.*` views over events, all validated live).
  Bucket facets therefore inject directly after the source + search stages
  (order validated live), where they also survive `summarize`; the
  projection is skipped for API views, whose query is analyzer input.

### 1.11 Span lenses — why the traces view fetches spans directly

The traces list originally aggregated (`summarize … by:{trace.id}`), which was
slower and starved the facet picker: post-summarize only the aggregate fields
exist. Fetching spans directly and slicing with lens filters keeps every span
attribute available. Facts validated live (box tenant, 2h window, ~280k spans):

- **Root heuristic:** `isNull(span.parent_id)` (~33.6k) is the OTel root
  definition and what the lens uses. Dynatrace's `request.is_root_span == true`
  (~42k) is broader — also true on spans whose parent fell outside ingest —
  and the two only overlap on ~31.4k spans; neither subsumes the other.
- **Failure signal:** `span.status_code == "error"` (681) vastly out-catches
  `request.is_failed == true` (7), which exists only on entry spans. The
  errors lens ORs both. `span.status_code` is null on ~99.8% of spans.
- **DB spans:** `isNotNull(db.system.name)` (231k) is broader than
  `db.query.text` (120k) — drivers emit `pool.acquire` etc. without query
  text. The db lens filters on the system attribute (both era names, see
  below), displays the query text with `span.name` fallback.
- **Two semconv eras, disjoint populations.** OTel renamed the database
  attributes on the way to stability (`db.statement` → `db.query.text`,
  `db.name` → `db.namespace`, `db.operation` → `db.operation.name` in
  semconv 1.26; `db.system` → `db.system.name` in 1.30, stable since 1.33) —
  and the two big demo tenants sit on opposite sides. Validated live (4h
  windows): the OneAgent-fed tenant has **1.73M `db.system` / 0
  `db.system.name`** spans (the Dynatrace semantic dictionary itself defines
  `db.system` next to new-style `db.query.text`/`db.namespace` — OneAgent
  emits that mix), the OTLP-fed tenant has **0 / 349k** — the exact inverse.
  Same story for HTTP (652k spans carry only legacy `http.method`, 4.26M the
  stable `http.request.method`) and messaging (`messaging.operation.type` on
  all 14.6k, legacy `messaging.operation` dup-emitted on a subset). A filter
  or column that reads only one era's name silently loses an entire tenant:
  every category discriminator must OR both, every display column coalesce
  both. `request.is_failed` → `transaction.is_failed` is the same pattern on
  the Dynatrace side (dictionary deprecation; both tenants dup-emit both
  today — `catalog.SpanFailed` checks both).
- **RPC and messaging carve out real subsets.** `isNotNull(rpc.system)`
  (1.9M: grpc, apache_axis, aws_api, dotnet_remoting — OneAgent also leaks
  numeric enums "1"/"2" with meaningful `rpc.service`/`rpc.method` beside
  them) and `isNotNull(messaging.system)` (kafka, artemis, mqseries, aws_sqs
  with `messaging.destination.name` + operation type) each got a lens.
  DynamoDB spans carry `db.system` **and** `rpc.system=aws_api` —
  `SpanCategory` gives db precedence. FaaS spans exist (`faas.trigger`,
  140k) but all also categorize via http/rpc, so no faas lens.
- **GenAI spans:** `gen_ai.operation.name` is the discriminator;
  `gen_ai.system` is empty on this tenant while `gen_ai.provider.name`,
  `gen_ai.request.model`, and `gen_ai.usage.*_tokens` are populated.
- **Multi-line cell values shear table rows.** `db.query.text` routinely
  contains newlines (sqlc header comments); any `\n` in a cell breaks the
  row grid. The table cell primitives (`pad`/`cell` in `table.go`) flatten
  whitespace runs before truncation.

### 1.11b span.events, exceptions, and error visibility

Validated live on both tenants (box 24h, demo 2h windows):

- **`span.events` is an array of records** discriminated by `span_event.name`
  — `exception`, `bizevent`, `feature_flag`, `message`, or free
  instrumentation text ("Enqueued", "Fetch cart"). Box emits only exception
  events; demo has the full zoo (1M+ bizevent, 250k exception per day).
- **Exceptions mostly ride on NON-failed spans.** ~98% of exception-bearing
  spans on demo have `span.status_code` null or `"ok"` (a caught-and-handled
  error, a 404 recorded via `HttpServletResponse.sendError`). Any exception
  surfacing keyed off the status verdict misses nearly all of them — hence
  the dedicated exceptions lens, the preview facts, and the waterfall `⚡`
  badge that are independent of `✗ failed`.
- **Two exception spellings, both live on one tenant.** Grail/OneAgent
  serializes `exception.stack_trace` (plus `exception.id`,
  `exception.file.full`, `exception.line_number`,
  `exception.is_caused_by_root`); pure-OTel SDK events carry
  `exception.stacktrace` (701 events on demo had ONLY that spelling).
  `catalog.SpanEvent.Exception()` coalesces both. Cause chains produce
  multiple exception events per span (2–31 seen).
- **Iterative expressions are rejected in `filter`.**
  `filter in("exception", span.events[][span_event.name])` fails with
  `ITERATIVE_EXPRESSION_FOR_FILTER`. The exceptions lens instead
  string-matches the serialized array:
  `contains(toString(span.events), "\"span_event.name\":\"exception\"")` —
  `toString` serializes as `{"key":"value", …}` (no space around `:`),
  validated live. Iterative access works fine after `expand` or in
  `fieldsAdd`.
- **The request verdict exists only on entry spans.** A deep span that
  errored carries just `span.status_code == "error"` — `catalog.SpanErrored`
  (verdict OR status code) is what the waterfall marks; `SpanFailed` alone
  left every non-entry error span unmarked.
- **Minimal error briefs cover ~98% of failed spans**: HTTP status ≥ 400
  (3.3k of 7.7k failed demo spans carried one) → gRPC status code (numeric;
  `4` = DEADLINE_EXCEEDED — name it) → exception type → `span.status_message`.
  Only ~2% of failed spans carry none of the four (`catalog.SpanErrorBrief`).
- **`code.call_stack` is OneAgent-only and sparse.** 683k demo spans in 4h
  carry it (box: zero; OTel's `code.stacktrace` spelling: zero anywhere), but
  per-endpoint only ~2–17% of spans have it stamped. Stack-shaped fields are
  suffix-matched (`call_stack`/`stack_trace`/`stacktrace`) and collapse
  behind their top frame — `vulnerability.stack` is a tech-stack enum and
  must not match.

### 1.12 The expansion tables (RUM, bizevents, dictionary, dt.system, synthetic)

Validated live for Phase 3.5 (box tenant; synthetic on the demo tenant):

- **`user.events` has NO `event.type`** — `summarize by:{event.type}`
  returns one null bucket, silently. The discriminator is
  `characteristics.classifier` (request, error, user_action, view_summary,
  page_summary, navigation, …), and the field set varies per classifier.
  Durations and web vitals are **ns strings** (`"1948000000"` = 1948ms) but
  `web_vitals.cumulative_layout_shift` and `ttfb.*_duration` are raw floats.
- **`user.sessions` is sparse** (single digits over 2h) — the view floors
  its window at 24h (same pattern as vulnerabilities). The session↔events
  join key is `dt.rum.session.id` on both tables, `-0` suffix included.
  `dt.smartscape.frontend` / `frontend.name` are **scalars on events but
  arrays on sessions** — the sessions scope filter goes through
  `matchesPhrase(arrayToString(…))`, the events one through
  `== toSmartscapeId(…)`.
- **`bizevents` payloads are producer-shaped**: one producer writes flat
  `slo_name`, another dotted `slo.name`, a third only `event.category` —
  and `event.category` can be entirely absent. A content column is a
  client-side coalesce chain; `event.type` + `event.provider` are the
  reliable facets. Window floored at 24h (2h hid all but one producer).
- **`dt.semantic_dictionary.fields.model_id` is null on all ~1400 records**
  — a dead column. The real join is reversed: `models.fields` is a string
  array; `expand fields | join [fetch …fields], kind: leftOuter, on: {
  left[field_name] == right[name] }`. Default (inner) join silently drops
  the ~13% of declared names without a definition row. No model is named
  `spans`/`logs` — the Grail table lives in `data_object`, and five
  link-models carry the **literal string "null"** there.
- **`dt.system.data_objects`** distinguishes tables from views by `type`
  (19/344 on box); the `metrics` object is `usable_with:
  ["fieldsSnapshot"]` only — `fetch metrics` is invalid, so the tables
  view refuses enter there. **`dt.system.buckets`** is snake_case
  (`retention_days`, `dt.bucket.class`, `dt.system.table`) and has no
  status column (REST-only). **`load` syntax**: double quotes, absolute
  path, no scheme — wrong shapes fail loud (PARSE_ERROR_SINGLE_QUOTES /
  TABULAR_FILE_MUST_START_WITH_SLASH / UNKNOWN_TABULAR_FILE), and both
  `| search` and facet filters compose after `load` fine.
- **Synthetic is not in Smartscape.** `smartscapeNodes "SYNTHETIC_*"` is
  empty even on tenants with dozens of monitors; the views run on classic
  `fetch dt.entity.synthetic_test` / `dt.entity.http_check` (union via
  `append [ … ]` — validated) with `fieldsAdd lifetime, tags` (unknown
  attributes there **hard-fail** with FIELD_DOES_NOT_EXIST, unlike most of
  Grail). Two metric families keyed by different entity dims
  (`dt.synthetic.browser.*` by `dt.entity.synthetic_test`,
  `dt.synthetic.http.*` by `dt.entity.http_check`) — the availability
  enrichment appends two timeseries and aliases both dims to one `key`
  field. Classic entity ids are plain strings: `in(dim, {"ID"})` without
  `toSmartscapeId`. Execution results: `fetch dt.synthetic.events` (exact
  name), HTTP step events are `http_step_execution` (no "monitor").
- **LogPatternExtractor** (`dt.statistics.clustering.LogPatternExtractor`):
  input `{"logQuery": …}` — the query **must project `timestamp` and
  `content`** (schema-enforced) — plus `numberOfExamples` and
  `generalParameters.timeframe`. The generic `--query` shorthand of
  `dtctl exec analyzer` maps to `timeSeriesData` and is wrong for this
  analyzer. Execution is effectively synchronous (<1s for hundreds of
  records; the SDK's ExecuteAndWait covers the async path). Output items
  `{patternExpression, sampleMatches, numberOfMatches}` arrive **unsorted**
  — sort client-side. The pattern drops straight into
  `| filter matchesPattern(content, "<pattern>")` (validated live; invalid
  patterns fail loud with ERROR_IN_PARSING_PATTERN, a rare non-silent DQL
  error).

### 1.13 API-backed table views (Spec.API)

Views without a DQL substrate (SLOs, anomaly detectors) or with a non-DQL
execution engine (log patterns) set `Spec.API` to a source name; sources are
`func(ctx, scope, dql)` closures built in `sources.go` from the
existing resource handlers and injected via `tui.Options.Sources` — `internal/tui`
stays HTTP-free. Rules learned:

- The composed `Spec.Query` output (when present) is handed to the source as
  input — the patterns source analyzes it, so `/`-searches and facets keep
  working on an analyzer-backed view. Sources that ignore the dql (slos)
  must have `Query == nil`, which makes the table view refuse server
  searches and facets **with a status message** instead of showing narrowing
  pills that silently did nothing.
- `Spec.Echo` supplies the command echo (`dtctl get slos`) since there is no
  query to render. `CanScope` is false by construction when `Query == nil`.
- The SLO API returns definitions only — no status/value/error budget. The
  slos source runs the evaluation endpoint per SLO (parallel, bounded,
  ~12s cap) and merges the first criteria's result; evaluation failures
  degrade to blank cells, never errors.
- The drill into patterns is view-level, not row-level: 'a' needs no
  selection and inherits the view's FULL scope (entity, trace, pattern)
  **and** its server searches/facets (`pushViewMsg.searches/.facets`) —
  patterns describe the list you are looking at. ('g' was unavailable: it
  is go-to-top.)
- **Projection order matters for injected facets.** The analyzer's schema
  demands the logQuery project `timestamp` and `content` — but a
  `| fields` stage inside `Spec.Query` sits BEFORE the facet stages
  `InjectStages` adds at the tail, so every inherited facet would filter a
  projected-away (null) field and feed the analyzer zero records (found in
  review). The projection is therefore appended by the source, after
  composition (`catalog.LogPatternInput`); a trailing
  `… | filter toString(loglevel) == "ERROR" | limit 300 | fields
  timestamp, content` validates live (SUCCESSFUL).
- Interactive facets stay disabled on API views even when they carry a
  Query: the fetched records (patterns) are not the query's rows (logs),
  so a record-attribute facet would inject a filter on a field the
  pipeline never carries. Facets *inherited from the source list* apply
  fine — they came from that pipeline.

### 1.14 GenAI spans — prompts, tool calls, and the dot namespace

Validated live (box tenant, agent workloads):

- **`gen_ai.operation.name`** discriminates `chat`, `execute_tool`, and
  `invoke_agent` (semconv also defines `embeddings`/`text_completion`).
- **Chat spans carry the whole exchange**: `gen_ai.input.messages`,
  `gen_ai.output.messages`, and `gen_ai.system_instructions` are JSON
  *strings* of `[{role, parts}]`; part types seen live: `text`,
  `reasoning`, `tool_call` (`{id, name, arguments}`), and
  `tool_call_response`. Roles: user / assistant / tool. Parse cost is real
  (100 KB+ prompts) — per-row derivations must memoize (the genai lens
  caches under a `__genai.*` record key; column Value funcs run every
  render frame).
- **Tool spans**: `gen_ai.tool.name` / `gen_ai.tool.call.arguments` /
  `gen_ai.tool.call.result`. **Agent spans**: `gen_ai.agent.name`,
  `gen_ai.conversation.id`.
- **Token usage**: `gen_ai.usage.input_tokens` / `.output_tokens` plus
  `gen_ai.usage.cache_read.input_tokens` / `.cache_creation.input_tokens`
  — cache reads dwarf fresh input on agent workloads (fold them into the
  "in" figure or it looks absurdly small).
- **The Smartscape linkage breaks the lowercased-type rule**: GENAI_MODEL
  entities stamp spans as `dt.smartscape.gen_ai.model` (likewise
  `.provider`, `.service`, `.agent`) — a dot namespace, NOT
  `dt.smartscape.genai_model`. `smartscapeField()` special-cases the
  `GENAI_` prefix and `SpanScopable` includes it. The ids appear on spans
  even when `smartscapeNodes "GENAI_*"` returns no nodes (observed live).
- **A second convention exists and dominates some tenants** (demo's
  LangChain/Azure agents): NO `gen_ai.operation.name` — the discriminator
  is `llm.request.type` (chat/completion/embeddings) — and the exchange
  lives in flat numbered attributes: `gen_ai.prompt.N.role/.content`,
  `gen_ai.prompt.N.tool_calls.M.name/.arguments`,
  `gen_ai.completion.N.*`, with cache tokens under
  `gen_ai.usage.cache_read_input_tokens` (underscore, not
  `.cache_read.input_tokens`). The genai lens filter ORs both
  discriminators; `GenAIInput/GenAIOutput` parse either shape into the
  same message model (`FlatGenAIMessages`). These spans still carry the
  `dt.smartscape.gen_ai.*` ids.
- **GenAI spans rarely include trace roots** — a GENAI-scoped traces view
  on the default roots lens is silently empty (found live: 225 matching
  spans, 0 roots). `DefaultSpanLens` opens GENAI drills/tabs on the genai
  lens instead.

### 1.15 The session timeline (RUM waterfall)

- One busy session held **16k request events vs 65 user actions / 61 view
  summaries / 76 errors** — a session timeline must default to the journey
  skeleton (`view_summary`, `user_action`, `navigation`, `error`) and keep
  requests one lens away, or the story drowns in XHR noise.
- **Nesting is time containment, not ids**: `dt.rum.view.id` and
  `user_action.id` were null on the probe tenant. A `view_summary`'s
  `start_time` is the view's *start* (its window spans the whole view), so
  sorting `start_time asc` and nesting each event under the latest
  view/action window that contains its start emits parents directly
  before their children (`catalog.BuildSessionTimeline`).
- **`trace.id` on request events is a plain 32-hex string** (nullable) —
  it drops straight into the trace waterfall. That one field is the whole
  frontend→backend bridge.

### 1.16 Log-pattern expressions carry named exports

`LogPatternExtractor` names every variable matcher in its
`patternExpression` — `'LISTEN ' DQS:f_1`, `'SELECT ' DATA:f_1` — so the
same expression works as **both** the `matchesPattern(content, P)`
predicate and a `| parse content, P` stage (validated live: `f_1` comes
back as a real field, DQS with the quotes already stripped). Detecting
exports requires stripping single-quoted literals first — a literal
`'FOO:bar'` is not an export (`catalog.PatternExports`). Pure-literal
patterns extract nothing and keep the standard log columns. Search
injection stays legal (it lands before the parse stage) and facet filters
compose after it, so parsed fields are facetable.

### 1.17 OAuth expiry in a long-running TUI

The executor's `OnUnauthorized` hook only guarded the **poll loop** — the
initial `query:execute` had no 401 handling, so a TUI idling past the
access token's lifetime failed its next query with "JWT token expired".
The fix sits one layer down, in `pkg/client`: a resty retry condition
that, on 401, re-resolves the token through the OAuth manager (forcing a
refresh when the local cache still looks valid — clock skew, compact
keyring storage), swaps it in, and retries once. Unchanged tokens (static
API tokens, dead credentials) surface the original 401 with no retry, and
a token refreshed seconds ago that is rejected again stops the loop.
Every consumer of the shared resty client — DQL execute + poll, the SLO
evaluation fan-out, Settings, the analyzer — inherits the fix
(`Client.EnableTokenRefresh`, wired in `NewFromConfig`).

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

`home`, `query` (DQL escape hatch), `nav` (smartscape navigator — overview /
type browser / walk, see ../design/smartscape-navigator.md), `waterfall`,
`relations`, `detail`, `inspector`, `metrics` each implement the `viewModel`
interface directly.
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
- **Every view that fires its own queries needs a `seq` generation guard** —
  `tableView`, `waterfall`, `relations`, `inspector`, `metrics`, *and* `home`
  (its per-panel results). A slow result from a superseded refresh (e.g. after
  a timeframe change) must be dropped, or it overwrites fresher data. This is
  the one an early `home` implementation missed.

### A view must never claim a scope it didn't apply

The subtlest class of bug the review found. A global pin (`.`) is handed to the
next command-bar/hotkey jump — but only if that view's query can actually
compose the pinned entity. Two ways the naïve `if UsesScope() { scope.Entity =
pin }` lies:

1. The query **ignores** the entity for that type (a `SERVICE` pin on `:pods` —
   `k8sScopeFilter` returns `""`; a K8s pin on `:vulnerabilities` —
   `VulnEntityFilter` returns `""` because K8s ids don't match the
   `related_entities` id era). The list is unfiltered but the breadcrumb reads
   `pods (checkout)`: data presented as scoped that never was.
2. The query **composes a filter that matches nothing** (a `HOST` pin on
   `:traces` — spans carry no `dt.smartscape.host` field). Silently empty,
   labelled `traces (my-host)`: the user concludes the host has no traces.

The fix is `Spec.CanScope(tf, e)`, honest **by construction**: if
`Query(scoped) == Query(unscoped)` the entity had no effect → don't apply or
claim it (catches case 1 and any future scope-ignoring view for free); an
optional `Scopable(e)` predicate refines case 2 (traces → `SpanScopable`). When
a pin doesn't apply, navigate **unscoped** and say so in the status line rather
than refuse — `:pods` should always give you pods. Drill-down navigation is
type-correct by construction (workload→pods, etc.); only the pin injects
arbitrary types, so it is the only path that needs the guard.

**General principle:** a breadcrumb/header that shows scope is a *promise the
query kept*. Derive the label from what the query actually did, never from what
the user intended.
- **`claimKey`** (a no-op `tea.Cmd` returning `nil`) lets a child consume a key
  the app would otherwise act on — e.g. `esc` that clears a filter instead of
  popping the stack. bubbletea discards `nil` messages, so returning a real
  no-op command is how you say "handled, do nothing."
- **`InputActive()` gating**: while a text input (filter, command bar, query
  editor) owns the keyboard, global single-letter keys (`q`, `x`, `.`, hotkeys)
  must not fire. Every view reports this; the app checks it before its own
  key switch.

---

## 3b. Visual design (theme package)

- **The palette is adaptive with explicit fallbacks.** Every color in
  `internal/tui/theme` is a `lipgloss.CompleteAdaptiveColor`: truecolor hex
  (Catppuccin Mocha/Latte) plus hand-picked ANSI-256 and ANSI-16 fallbacks per
  background flavor. lipgloss picks the variant for the terminal's capability
  and background — never rely on automatic downsampling for the 16-color tier,
  it picks ugly approximations.
- **Selection is a gutter bar + background wash, not `Reverse(true)`.**
  Reverse video inverts whatever colors a cell already has (unreadable over
  class-colored cells); a fixed `SelBg` + dropping per-cell colors on the
  selected row reads as one calm bar. All list views share the pattern:
  `theme.Gutter.Render("▌") + theme.Selected.Render(pad(row, width-1))` — and
  every row budget must account for that 1-cell gutter (the waterfall's column
  math missed it first: rows overflowed and truncated their last column).
- **One global spinner, app-driven.** Views expose `Busy() bool` (optional
  `busyReporter` interface); the app runs a single 90ms `tea.Tick` loop while
  the *visible* view is busy and advances a frame counter in `theme`. Views
  just render `theme.Spin()` — no per-view spinner models. The test helper
  `deliver` must drop `spinnerTickMsg` (like `dataMsg`) or a busy view re-arms
  the tick forever and the test hangs.
- **lipgloss has no border titles** — the home panels hand-roll their boxes
  (`╭─ ● title ─…─╮` + `│` sides) precisely so the title can live in the top
  border. `lipgloss.Width` (ANSI-aware) does the fill math on styled titles.
- **Overlays replace the body, they don't composite.** lipgloss v1 can't
  layer; the command palette / help / timeframe picker render *instead of* the
  body via `lipgloss.Place`. Design overlays to be self-sufficient, not
  peek-through.
- Styled-string layout: `pad`/`cell` (lipgloss.Width) and `ansi.Truncate` are
  safe on already-styled strings; plain `fmt.Sprintf("%-20s", styled)` is not
  (counts escape bytes).
- **`wrap()` (lipgloss `Width(w).Render`) pads every line to the full width**
  with trailing spaces. Any "does this value fit inline?" check against
  wrapped output always fails — strip the padding first (`wrapLines` in
  render.go trims each line). This silently pushed every short string value
  into the two-line block layout before it was caught on a live drive.

## 3c. Typed values & inspector navigation (render.go / inspector.go)

- **`renderValue` classifies record values for display**: entity ids (both
  eras match `^[A-Z][A-Z0-9_]*-[0-9A-F]{16}$`, type = the prefix) become
  accent links; RFC3339 strings render absolute + "· 23m ago"; `*duration*`
  keys holding digit strings render `FormatNs` + raw ns; numeric strings get
  thousands separators — **except identifier-ish keys** (`isIDKey`:
  `aws.account.id` is a label, not a quantity); 16/32-hex are opaque uids;
  maps/arrays and strings that parse as JSON render as an indented
  syntax-highlighted block (keys sky, strings green, numbers peach). The raw
  value is kept beside the styled lines for yank.
- **Density first: scalars and arrays are one line by default.** Values too
  big for their line collapse to a truncated compact preview (`compact`:
  whitespace-squashed string / one-line JSON) marked with a dim `▸`; enter
  expands to the full block (`▾`). **JSON objects are the exception** — they
  read as structure, so they default to the expanded block (enter collapses).
  The earlier layout (label line + indented block for anything long) read
  nicely but wasted half the screen — user feedback killed it within an hour
  of a live drive.
- **The inspector has a field cursor, not a line scroller**: j/k moves over
  fields, the viewport follows, and the selected row's label line gets the
  gutter-bar + wash treatment (per-value colors drop on that line — strip
  ANSI before washing). enter follows the selection: entity id → its detail
  page, `trace*` 32-hex → the waterfall, expandable → toggle. Arrays whose
  elements are all entity ids (`affected_entity_ids`) explode into one
  navigable `key[i]` row each.
- **Selection follows the cursor**: `Selection()` returns the highlighted
  link's entity (falling back to the page entity), so pin/relations/open act
  on what the user is looking at. `y` goes through the `yankProvider`
  interface (app checks it before the entity-id fallback) and copies the
  selected field's raw value. The record's own `id` field is styled opaque,
  not as a link — a self-link is noise (but `id_classic` may be a *different*
  entity and stays navigable).
- **Id-only traversal must back-fill the name.** An entity link carries no
  name; the detail page's tabs share a pointer to the page entity
  (`scope.Entity = &v.entity`), and when the details-tab fetch returns,
  `detailView` copies the learned name in. Unstarted signal tabs then compose
  it — load-bearing for K8s entities, whose log scoping matches plain
  `k8s.*` names (§1.3). Without this, logs on a traversed pod are silently
  empty.

## 3d. Metrics charts (btop-style)

- Filled braille areas (`PlotFilled`), not line plots; per-row **vertical
  gradient** via `theme.Gradient` — top row full series color, lower rows
  blend toward the background ink. Only the truecolor tier fades; 256/16
  fallbacks keep the flat base color (downsampled blends look muddy).
- **Scaling is honest**: zero baseline for filled charts (min-based
  autoscale exaggerates noise into drama), and `%` metrics render as a true
  0–100 gauge — a host at 3% CPU *should* look nearly empty; the header
  carries the numbers (`last` value prominent, min/avg/max dim).
- Charts divide the body height (`(h - 2 - 2n) / n` rows each, clamped 2–9),
  y-axis max/min labels sit on the first/last braille row (`┤` ticks), and a
  single shared time axis (`└ 2h ago … now`) closes the page — every chart
  spans the same window, so per-chart axes would be noise.
- Unit-aware formatting: `"B"` → IEC bytes (a 15.3 GiB axis label, not
  "16106.1M"), `%`/`ms` attach suffixes. Shared as `catalog.FormatUnit`
  (charts, vitals rows) and `catalog.FormatUnitShort` (dense table cells:
  one-decimal percent, spaceless bytes, k8s `500m` millicores).
- **A sparkline alone misleads**: `MiniGraph` normalizes to its own range,
  so a flat 3% and a flat 90% CPU render identically. Every spark column
  pairs the mini-graph with the right-aligned latest value, and the vitals
  block adds a btop-style meter for `%` series — `█…░` scaled 0–100 and
  class-colored by load (ok < 75 ≤ warn < 90 ≤ error) — because percent has
  an absolute scale the trend can't show.

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

## 7. security.events (validated live, demo tenant)

The facts behind the security workspace (`catalog/security.go`):

- **Two report levels, two jobs.** `VULNERABILITY_STATE_REPORT_EVENT`
  records exist at `event.level == "VULNERABILITY"` (the rollup: full
  markdown description, complete `davis_assessment.*`, remediation,
  references — but **counts only**, no entity ids) and `"ENTITY"` (one row
  per affected entity: `affected_entity.*`, the vulnerable component,
  process-instance ids, `related_entities.*.{ids,names}`, and — for CODE
  vulns — `entry_points.entry_point_jsons`). Any rollup query MUST filter
  the level: summarizing both levels into one `by:{vulnerability.id}`
  bucket lets `takeLast` pick fields from either row shape.
- **Array membership pins**: `in("SERVICE-…", related_entities.services.ids)`
  works with plain strings (legacy-era ids — no `toSmartscapeId`).
  SERVICE and HOST ids are identical strings across both ID eras;
  `PROCESS-<hex>` ↔ `PROCESS_GROUP_INSTANCE-<hex>` share the hex suffix
  (checked across every distinct PGI in 7d of attack records), so a
  modern PROCESS pin swaps the prefix. K8s workload/cluster ids in
  `related_entities` do NOT match Smartscape ids (`CLOUD_APPLICATION-…` /
  legacy `KUBERNETES_CLUSTER-…`) — K8s pins refuse honestly.
- **Attacks carry no `vulnerability.id`.** RAP `DETECTION_FINDING` records
  link to code-level vulns only via `vulnerability.code_location.name`
  (exact string match). For library vulns the only linkage is
  entity-based (detections on the affected process groups) — the page's
  attacks tab labels nothing causal and `filter false` keeps it honestly
  empty when the hop resolves no matchable entities (an unfiltered fetch
  would present the tenant's whole attack stream as "this vuln's
  attacks"). `filter false` is legal DQL (constant-filter warning, empty
  result).
- **Attack records are fat and well-stamped**: `finding.*`
  (type/severity/action Blocked|Audited), `entry_point.payload` +
  `url.path` + `function.name` + structured `user_controlled_inputs`
  (with `is_malicious` spans), `actor.ips`, full `http.request.header.*`,
  `trace.id`/`span.id`, `dt.smartscape_source.id` (=`PROCESS-…`), both-era
  process/host ids, and `dt.entity.process_group` — which is why
  `legacyField` grew a PROCESS_GROUP arm.
- **`expand entry_points.entry_point_jsons` works** (one row per entry
  point; each element is a JSON *string* — parse client-side, tolerant of
  malformed docs). Payload user-input values arrive masked (`*****`).
- **State reports are periodic snapshots** (24h floor still right); a
  resolved vuln stops being re-reported, so the page's detail fetch looks
  back 7d. Change events (`VULNERABILITY_STATUS_CHANGE_EVENT` /
  `…ASSESSMENT_CHANGE_EVENT`) are sparse — the timeline floors at 30d.
- **glamour in bubbletea**: don't use `glamour.WithAutoStyle()` — it
  issues a fresh OSC background query mid-session (bubbletea eats the
  reply; ~100ms stall + stray input risk). Pick
  `WithStandardStyle("dark"/"light")` from `lipgloss.HasDarkBackground()`,
  whose verdict is already cached from the first styled frame.
