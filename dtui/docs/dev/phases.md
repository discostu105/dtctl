# Implementation Phases — Shipped Log

The phase-by-phase record of what shipped, extracted from the design doc
([../design/tui.md](../design/tui.md)). Each entry captures the state of the
feature *as it landed*, including refinements that superseded earlier phases —
read newest-to-oldest for the current behavior.



### Phase 1 — Shell + the core map (services, hosts, problems, logs) ✅ implemented

- `main.go` guards; app shell: command bar, breadcrumbs, footer, help,
  theme adapter, timeframe picker.
- ViewSpec engine (table shell + detail shell) with the first catalog slice:
  **problems, services, hosts, logs** — enough for the core triage loop.
- Drill-down vocabulary (`l m p v d o enter esc -`), command echo, refresh.
- Tabbed entity detail page (`enter` on an entity row): curated key-facts
  panel + full properties, a **related** tab (the entity's Smartscape
  neighbors — a host's processes/containers/K8s node — embedded relations
  view; enter navigates, the highlighted neighbor drives pin/x/o), a
  containment tab where one exists (K8S_NODE → its pods, completing
  host → related → node → pods; pods edge to the node, never the host),
  and metrics / logs / events / problems as lazily-loaded pre-scoped tabs
  (`tab` to switch, drill letters jump to their tab). Record inspector with a highlights block and
  `/` property search.
- Read-only. Success criterion: the incident-triage journey works end to end.

### Phase 2 — Topology + traces + Kubernetes ✅ implemented

- Relations panel (`x`) over `smartscapeEdges` (both directions in one
  query, batched name resolution, raw-id fallback for nodeless types);
  scope pinning (`.` / `ctrl-x`).
- Traces view (direct span fetch sliced by lenses — roots · errors ·
  server · client · db · genai · all; no aggregation, so queries stay fast
  and every span attribute reaches the inspector and the facet picker;
  root heuristic `isNull(span.parent_id)`) + span waterfall
  (`toUid()` cast, tree from `span.parent_id`, proportional bars, failed
  markers, auto-widening window); log ↔ trace jumps in both directions
  (`s` on a log record, `l` on a waterfall — log `trace_id` is a plain
  string, span `trace.id` is a UID).
- Kubernetes catalog: clusters, nodes, namespaces, workloads
  (deployments + statefulsets + daemonsets in one multi-type query with
  coalesced ready/desired), pods (READY/RST/PHASE from `parse
  k8s.object, "JSON:obj"`); containment navigation (enter on a workload
  → its pods; detail on `d`). Live-validated gotcha baked into
  `SignalFilter`: log records carry `k8s.*` name attributes but **no**
  `dt.smartscape.k8s_*` fields, so K8s log scoping matches by name.
- Metric-column enrichment (batched `timeseries ... by:{...}, filter:
  in(...)` per page, braille sparkline cells, blank-cell degradation),
  column sorting (`J`/`K`, smart default direction, empties last),
  digit hotkeys (0 home … 9 aws).

### Phase 3 — Breadth: cloud, frontends, security, escape hatch ✅ implemented

- AWS inventory (census by type → typed list with `tags:aws` Name-tag
  display fallback) and the generic `:entities` browser over any
  Smartscape type via one `resources` view; Postgres databases; web
  frontends with request/error sparklines and Web-Vitals charts; GenAI
  agents/services/models/providers; security vulnerabilities
  (deduplicated `security.events` state reports, 24h lookback floor).
  Azure/GCP inventories and DPS costs remain open (no data on the
  exploration tenant).
- DQL escape hatch (`:query`) with `ctrl-q` reveal-query from every
  view, dynamic result columns, and `o` opening the query as a notebook.
  Live progress/history remain open.
- Home triage view: active problems, failing services (from failed
  spans), Kubernetes warning events, open vulnerabilities — panels load
  independently, enter jumps into the full view pre-filtered.
- Browser deep links (`o`) via intent URLs: problems → Davis problems
  app, traces → Distributed Tracing, K8s/services/databases → their
  apps by `nodeId`, anything else → Smartscape topology; `y`/`c` yank
  ids and CLI commands over OSC 52.

### Phase 3.5 — Data & analysis breadth ✅ implemented

- **RUM**: `:sessions` (24h-floored window — sessions are sparse; lenses
  all · errors · bounced; enter = the session's event timeline) and
  `:userevents` (lenses all · errors · actions · views · requests, the
  views lens carrying Core Web Vitals columns with threshold coloring;
  `s` jumps request events to their trace waterfall). Frontends gained
  `u`/`e` drills into both. Home gained a "frontend errors (24h)" panel.
- **Business events**: `:bizevents` (24h floor, type/provider facets, a
  best-effort content column — producers name payloads inconsistently).
- **Semantic dictionary**: one `:dictionary` view (aliases `models`,
  `fields`, `dict`) whose lens strip carries models · fields · stable ·
  experimental · deprecated — the same tabs every other lensed view has.
  Enter on a model opens its fields (fields lens, `Arg` = model, via the
  expand + leftOuter-join pipeline — fields.model_id is dead, see
  learnings.md §1.12); enter on a field opens the full definition (examples,
  enums) in the inspector.
- **Data explorer**: `:tables` (19 tables / 344 views, fieldsSnapshot-only
  objects refuse entry), `:buckets` (records/size/retention; enter samples
  the bucket via `dt.system.bucket ==`), `:files` (Grail lookup data;
  enter runs `load "<path>"`), all feeding the generic `:records` sampler
  with derived columns.
- **Synthetic**: `:synthetic` (classic-entity union of browser + HTTP
  monitors with an availability-sparkline enrichment across both metric
  families) → enter → `:executions` (dt.synthetic.events; lenses
  runs · steps · failed).
- **Log patterns**: `a` on any logs view runs the Davis
  `LogPatternExtractor` analyzer over exactly the visible query (scope +
  server searches + facets); enter on a pattern drills back into the
  matching records via `matchesPattern`.
- **API-backed views**: the catalog gained `Spec.API` — named non-DQL
  sources wired in `main.go` (`tui.Options.Sources`) — powering
  `:slos` (definitions + parallel per-SLO live evaluation for
  status/SLI/error-budget columns) and `:detectors` (Settings API), with
  facets/server-search honestly disabled where no DQL exists.

### Phase 3.6 — Connection & polish ✅ implemented

- **Session timeline (the RUM waterfall)**: enter on a session renders its
  events proportionally on the session's time axis, nested by time
  containment (views → user actions → requests/errors), colored by kind,
  with journey · requests · errors · all lenses (a busy session is 16k
  requests vs ~65 actions — the journey skeleton is the default). `s` on a
  request row jumps into its backend trace waterfall; `u` on any RUM event
  row jumps back to its session's timeline; `e` keeps the flat sortable
  events table. Sessions ↔ events ↔ traces are two keystrokes apart in
  every direction.
- **GenAI is about prompts and tool calls**: the traces genai lens leads
  with the operation (chat · tool · agent), the last user prompt or the
  tool call (name + arguments), model, and token usage (cache-read tokens
  folded into "in"). The waterfall badges GenAI spans (✦ chat, ⚙ tool,
  ◈ agent) and swaps their labels for the prompt/tool text plus a
  `⟨in→out⟩` token annotation. The span inspector renders the whole
  exchange as a first-class **conversation** section — system prompt,
  every turn role-colored, reasoning marked, tool calls/results — instead
  of opaque JSON. GenAI entities scope traces/logs/metrics through the
  `dt.smartscape.gen_ai.*` fields (dot namespace — see learnings.md).
- **Pattern drill parses**: enter on a log pattern now also applies the
  extractor's DPL expression via `| parse content, "<pattern>"` — the
  pattern's named tokens (`f_1`, `f_2`, …) become real table columns
  (and facet/sort targets), so a pattern's variables are analyzable, not
  just visible.
- **Open-with picker**: `o` gathers every browser target the selection
  supports — the record's native app, URLs the record itself carries
  (`vulnerability.url` fixed the dead vulnerability page), the trace, the
  entity's app, the Smartscape topology, the query as a notebook — opening
  directly when there is one and raising a numbered picker when several
  apply (`y` yanks the URL instead).
- **Semantic dictionary everywhere**: one session-cached fetch of
  `dt.semantic_dictionary.fields` powers an `ⓘ` footer in every record
  inspector describing the field under the cursor (description · unit ·
  stability) — the data model explains itself in place.
- **Auth that survives the session**: OAuth access tokens expire under a
  long-running TUI; `pkg/client` now retries a 401 once with a re-resolved
  (force-refreshed) token, covering the initial query execute (the SDK's
  `OnUnauthorized` only guarded the poll loop) and every REST-backed
  source.
- **Brand header**: the Dynatrace-gradient wordmark (`▛▞▟ dtctl`, lime →
  teal → blue → purple) plus the environment host next to the context
  name.
- **Frontends wired into RUM**: a frontend's detail page carries sessions
  and userevents tabs (replacing logs/traces, which frontends never
  match); enter on a session row inside the tab drills straight into the
  session timeline, and `d` there opens the session's own record — the
  event → session navigation.
- **Nested lens strips**: when a detail tab shows its own lens strip
  (traces, sessions), `[`/`]` drive that strip; tab/shift+tab keep cycling
  the page tabs. (Superseded refinement — see "One owner per key" below:
  digits stayed global hotkeys and the brackets stopped falling back to
  the tab bar.)
- **Both GenAI instrumentation eras**: the genai lens, badges, detail
  column, tokens, and the conversation section understand the semconv
  convention (`gen_ai.operation.name`, JSON message blobs) *and* the
  traceloop/LangChain one (`llm.request.type`, flat numbered
  `gen_ai.prompt.N.*` / `gen_ai.completion.N.*` attributes) — and a GenAI
  entity's traces tab/drill opens on the genai lens, since agent spans
  rarely include trace roots.

### Phase 3.7 — Actionable detail pages ✅ implemented

- **Pulse header on entity pages**: the identity header gained a second line
  answering "is this thing on fire?" on every tab — active-problem count
  (one `dt.davis.problems` query per page open, lookback floored at 24h,
  update-records deduped by `display_id`), the list row's enrichment
  sparklines re-rendered for free (`__enrich.*` already rode in on the
  record), and the entity's age. Tab labels badge their row count once a
  tab has loaded (`logs (312)`, `evidence (4)`) — no speculative count
  queries.
- **Signals block on the details tab**: between the key facts and the
  properties, navigable rows for the entity's active problems (enter → the
  problem page) and its latest change-ish event (deployments, config
  changes, restarts, SDLC events, and the CUSTOM_INFO-typed "Deployment
  spec change" K8s workload events — validated live on both tenants; fixed
  7d lookback), enter → the event record. Quiet entities show nothing —
  the pulse line already tells that story.
- **The problem page**: enter on a Davis problem opens a bespoke tabbed
  page instead of the flat inspector ('d' keeps the raw record; the full
  record also stays one tab away on "details"). Overview = curated facts,
  the affected entities as navigable rows, and `event.description` wrapped
  ("Davis says"). Evidence = the constituent `dt.davis.events` fetched by
  `dt.davis.event_ids` (update-records collapsed per event id, root-cause
  relevance marked ✱). Logs/traces/events tabs are pre-scoped to **all**
  affected entities and the problem's own window (`event.start` →
  `event.end`/now, ±5m context pad) — the global timeframe picker
  deliberately does not reach into the page. Catalog got two extensions
  for this: `Scope.Entities` (or-joined signal/span filters; span filter
  keeps only span-scopable types and the traces tab is omitted when none
  qualify) and absolute `Timeframe.From/To` windows rendered as
  `toTimestamp("…"), to:toTimestamp("…")` through the same `from:%s` slot
  every query template uses (validated live).
- **Per-kind record highlights**: the inspector's priority block is now
  `catalog.PriorityFields(rec)` — problems, Davis events, vulnerabilities
  (both the summarized aliases and raw `vulnerability.*` names), spans,
  RUM sessions/events, and synthetic executions each hoist their own
  essentials; log-shaped records keep the original list.
- **Links block in record inspectors**: the record's exits — trace ids,
  entity ids (arrays exploded), URLs — hoist into one `▍ links` section
  between the highlights and the namespace groups, ranked traces →
  entities → URLs, capped at 8 rows (an overflowing key stays whole in its
  group), names resolved by the existing batched lookup. Entity pages keep
  facts + related instead.
- **KeyFacts gaps**: GENAI_* (provider — the nodes are otherwise bare) and
  K8S_NAMESPACE (cluster) gained curated facts.

### Phase 3.8 — Smartscape navigator ✅ implemented

Full design: [../design/smartscape-navigator.md](../design/smartscape-navigator.md).

- **`:nav`** (aliases `smartscape`, `navigator`): a dedicated topology app in
  three stacked levels — overview (type census + type-level relationship
  schema from one `smartscapeEdges` summarize, lazy `source_type`/
  `target_type` materialized via `fieldsAdd`, validated live on box),
  type browser (instances with health dots), and **walk mode**: an
  ego-centric neighbor tree grouped by (direction, verb), structure ranked
  before mesh, per-group render cap with explicit `+N more`, and a
  breadcrumb **trail** — hops re-root in place (← backtracks), so a
  15-hop walk is one stack entry and esc keeps its page-back meaning.
- **Global `X`**: walk the topology from any selected entity — the capital
  sibling of `x` (quick one-hop panel). `:nav <TYPE>` browses a type,
  `:nav <entity-id>` walks from it.
- **Health overlay**: one tenant-wide `dt.davis.problems` query per refresh
  (24h floor), deduped and intersected client-side against visible nodes in
  **both id eras** — problem dots on every node, per-type counts on the
  census, never a per-node query.
- **Preview pane**: cursor-following (debounced 250 ms, session-cached
  `DetailQuery`) identity + health + curated `KeyFacts`; on by default
  (`P` is the app-wide preview toggle), auto-hidden under 100 columns.
  Nodeless edge endpoints (`K8S_SECRET` et al.) render dimmed raw ids and
  preview as "no node record".
- **Session topo cache**: edges and details cache per navigator instance —
  backtracks and re-visits are zero-query; `r` clears and refetches.
- The shared query builders (`EdgesQuery`, `NamesQuery`, `BuildEdges`,
  `EdgeRank`) moved from `relations.go` into `catalog/smartscape.go`;
  the relations panel consumes them unchanged. Live-observed verbs beyond
  the documented four — `belongs_to`, `uses` — group generically and rank
  as structure.
- Deliberate key deviations from the design doc: the mesh toggle is `M`
  (`t` is the global timeframe picker) and `g`/`G` stay cursor home/end
  (overview is esc or `:nav` away) — consistency with the app vocabulary
  beat the draft bindings.

### Phase 3.9 — One owner per key ✅ implemented

Digits used to mean five different things by context (lens, tab, hotkey,
picker); tab meant three. Every key now has exactly one owner, matching the
visual hierarchy:

- **Digits 0-9**: global hotkeys, on every screen. Lens strips and tab bars
  never claim them. (Refined by "Entering rescopes the keyboard" below:
  entered pages reclaimed 1-9 for their numbered tab bars.)
- **`[` / `]`**: the lens strip, and only the lens strip — including a strip
  nested inside a detail tab. On a view without lenses the brackets say so
  instead of silently doing something else (the old fallback to tab cycling
  is gone: the same key must not change meaning between tabs).
- **`tab` / `shift+tab`**: the tab bar (detail pages, problem page) and the
  home panels. Nothing anywhere else. (Refined below: tab now cycles the
  view's primary strip, which on a plain table is the lens strip.)
- **Drill letters** (`l s v p m u e`): pre-scoped signal views; on tabbed
  pages they jump to the same-named tab unless the active tab's rows drill
  by that key (per-row meaning wins).
- **Peek pane on by default**: tables and the navigator render the selected
  row's highlights (PriorityFields / KeyFacts — client-side, zero queries)
  in a side pane at ≥110 columns, a bottom panel on narrower-but-tall
  screens, auto-hidden when cramped (bottom panel needs ≥30 rows). **`P`**
  flips the preference app-wide — one sticky setting, not per-view state —
  so enter is reserved for committing to a page, not for peeking.
- Same rework shipped the shared severity rendering (ITIL `event.severity`
  as SEV1–SEV5 badges — 1 is worst; the old word mapping was inverted),
  the `:events` hub with all/alerts/changes/system/audit lenses (system
  and audit surface `dt.system.events`), `x`=`X`= navigator walk, and
  `:aws` as an `:entities` census preset.

### Phase 3.10 — Entering rescopes the keyboard ✅ implemented

Phase 3.9's "digits global everywhere" treated the symptom (invisible key
scope) by banning context. The durable rule is visibility-based: **digits do
what the numbers on screen say; no numbers visible → global bookmarks.**

- **Exactly one strip on screen is numbered — the innermost one — and the
  digits address it.** Detail and problem pages show digit labels on their
  tab bar (`1 details  2 processes … 7 related`) and `1`–`9` switch tabs directly.
  When the active tab's table shows its own lens strip, the numbering
  moves down to it (`1 roots  2 errors … 9 all` — the tab bar drops its
  numbers) and the digits pick lenses; tab/shift+tab and the drill
  letters still switch tabs. A digit the numbered strip doesn't show is
  swallowed with a teaching status, never a hidden jump; `0` stays the
  jump home from anywhere (it never appears on a strip), and `esc` pops
  out to where all ten keys are global again. Top-level tables, home, and
  the navigator show no numbers, so digits stay global bookmarks there —
  the original lens-strip/hotkey overlap stays fixed where users roam.
- **`tab` cycles the view's primary strip**: page tabs when entered,
  panels on home, and the lens strip on plain tables and the session
  timeline — one unmodified key for the most common slice-switch (`[`/`]`
  are AltGr chords on German-layout keyboards). The brackets remain the
  explicit lens-cycling keys everywhere.

### Phase 3.11 — Security workspace ✅ implemented

The vulnerability view grew from a flat list into a workspace; every field
and filter shape below was validated live against a demo tenant.

- **Enriched `:vulns` list**: the summarize rollup now reads only
  VULNERABILITY-level state reports (entity-level rows silently polluted
  the old rollup) and adds the Davis assessment triage badges — EXPOSURE
  (`public` red / `adjacent` yellow / `-` assessed-clear / blank
  unassessed), EXPLOIT (`avail` red), FIX (`yes` green) — plus stack/tech
  and lenses (open · muted · all; muted vulns leave the default lens).
- **Entity → vulnerabilities pinning works**: a SERVICE/HOST/PROCESS pin
  switches the query to the ENTITY-level reports (the level that carries
  ids) — `in("<id>", related_entities.services.ids)` array membership,
  era-identical HOST ids, and the PROCESS↔PROCESS_GROUP_INSTANCE hex-suffix
  prefix swap — and swaps AFFECTED for the COMPONENT column. K8s types
  honestly refuse (their ids don't match the `related_entities` era).
- **Vulnerability page** (enter on a vuln; `d` keeps the raw record):
  tabbed like the problem page. `overview` refetches the latest full state
  report and renders the Davis assessment facts (risk vs CVSS, exposure,
  data assets, vulnerable-function usage, exploit, mute audit trail),
  remediation, and the vendor's markdown description via glamour (style
  from lipgloss' cached dark/light verdict — no mid-session terminal
  query; plain-text fallback). `entities` lists the affected entities with
  component/processes/data assets and l/v/p/m drills (PROCESS_GROUP rows
  drill through their first process instance). `attacks` shows RAP
  detections — exact `vulnerability.code_location.name` match for
  code-level vulns, affected-entity hop for library vulns (`filter false`
  keeps it honestly empty when nothing matches: attacks carry no
  vulnerability.id). `entry points` (code vulns) expands the entry-point
  JSON docs into paths/payloads/malicious-input flags. `timeline` lists
  status/assessment change events (30d floor). Enter on tab rows inspects
  (the `inspect` EnterTarget sentinel) instead of re-opening the page.
- **`:attacks` view**: Runtime Application Protection detections with
  Blocked (green) / Audited (yellow) verdicts, source IPs, target process,
  trimmed code location; preview shows the payload and entry point; rows
  carry `trace.id` (`s` → waterfall) and a PROCESS source entity; SERVICE
  pins widen through the log hop. GuardDuty/third-party detections and a
  `:findings` view for third-party scanners stay deferred.
- **Home**: an `attack detections (24h)` panel joins the triage page; the
  vulnerabilities panel filters like the list (VULNERABILITY level,
  unmuted).

### Phase 3.12 — Design-promise closure ✅ implemented

Four behaviors the design doc promised but the code lacked, closed in one
pass (all validated live on the demo tenant):

- **`:nav <name>` resolves names** (and `dtui nav <type|id|name>` from the
  CLI): a cross-type `matchesValue(name, "*term*")` lookup — a unique match
  walks straight to the entity, a multi-match becomes the browser as a
  disambiguation list (type column added, exact-name hits ranked first,
  health dots and preview apply unchanged). An ambiguous lowercase token
  (":nav payments" is type-shaped too) browses the type first and re-shapes
  into the name search when the browse lands empty — ":nav service" keeps
  meaning the SERVICE browser. Live gotcha: hosts pair with a same-named
  ONEAGENT node, so unique matches are rarer than expected — the
  disambiguation list is the common path.
- **Custom timeframe**: the `t` picker grew its fifth entry — any relative
  window (`45m`, `12h`, `3d`), the same labels the workspace file takes
  (`catalog.ParseTimeframe`). The pill shows the applied window
  (`custom (45m)`) and the highlight lands on it when the active window is
  no preset.
- **Query history**: the escape hatch remembers submitted DQL (MRU, capped
  at 50, persisted to `~/.local/state/dtui/queries.json`); `ctrl+p`/`ctrl+n`
  cycle it in the editor with the live draft stashed — up/down stay cursor
  movement in the multi-line editor. Live progress and renderer cycling
  remain open.
- **`:ctx <name>` switches contexts in-session** (no argument lists them).
  The dtui main package supplies a wiring factory (`Options.SwitchContext`)
  that reloads the config, points it at the requested context **in memory
  only** — the session-local contract of `--context` holds; an open TUI
  never writes the shared config — and rebuilds the executor, API sources,
  and segment lister. Applying a switch drops every tenant-specific piece
  of state: the pin, applied segments, the semantic-dictionary cache, and
  both view stacks (entity ids and fetched data don't survive the tenant
  boundary; in-flight results die with the discarded views; `-` must not
  resurrect them). The session lands on home; the timeframe is the one
  global that carries over, and `H` records under the new context.

### Phase 4 — Assets & mutations

- Management resource browser for the full existing CRUD surface; workflow
  executions with live log follow.
- Safety-gated edit (`$EDITOR` suspend/restore), delete confirms, workflow
  execute. Clipboard for command echo. Stretch: export a breadcrumb trail as
  a notebook.

Each phase ships independently; Phase 1 alone is a usable "k9s for Dynatrace
triage".
