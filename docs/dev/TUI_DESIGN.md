# TUI (Interactive Mode) Design Proposal

**Status:** Design Proposal
**Created:** 2026-07-05
**Author:** dtctl team

## Overview

This document proposes an interactive terminal UI for dtctl — `dtctl tui` — in the
spirit of [k9s](https://k9scli.io/) for Kubernetes: a persistent, keyboard-driven
navigator over the **primitives of an observability platform**. The user thinks
in nouns — services, hosts, pods, logs, traces, problems, frontends, cloud
resources — types an alias (`:svc`, `:pods`, `:logs`), and moves between them
with single keystrokes that follow the topology.

The defining design decision: **DQL is the substrate, not the interface.**
Every view is a curated query under the hood, and every drill-down composes
scope (entity + timeframe) into the next query automatically — but the user
never has to write DQL to navigate. A "reveal query" escape hatch exposes the
generated DQL for the moment curation runs out.

The design is grounded in two inputs:

1. **The dtctl codebase** — existing verbs/resources, the output layer's
   renderers (tables, sparklines, braille graphs, progress bars, live/watch
   modes), and the config/safety model the TUI must respect.
2. **The [dynatrace-for-ai](https://github.com/Dynatrace/dynatrace-for-ai) repo** —
   Dynatrace's official skills catalog, which both enumerates the platform's
   surface area (the view catalog below mirrors its skill domains) and encodes
   the canonical investigation flows (problem → entities → logs → traces).

`ARCHITECTURE.md` already lists "Interactive Mode: TUI using bubbletea/lipgloss"
as a future idea; this document turns that line into a concrete design.

---

## Background: What a Dynatrace User Expects to Navigate

Distilled from the `dynatrace-for-ai` skills catalog, the platform's surface
area falls into three layers:

- **Entities (topology)** — things that exist: services, hosts, processes,
  containers, Kubernetes objects, cloud resources (AWS/Azure/GCP), databases,
  web/mobile applications. Connected via **Smartscape** relationships
  (`runs_on`, `belongs_to`, `calls`, `instance_of`).
- **Signals (streams)** — things that happen: logs, spans/traces, metrics,
  events (deployments, K8s events, Davis events), Davis **problems**, security
  findings, RUM sessions/errors/Web Vitals, billing/usage events.
- **Assets (management)** — things you configure: SLOs, workflows, dashboards,
  notebooks, anomaly detectors, settings, extensions — dtctl's existing CRUD
  surface.

The skills also encode two hard platform rules that shape navigation:

- **Broad, unscoped signal queries fail** (Grail's 500 GB scan limit). Signals
  must be entered *through* an entity or a problem, which supplies scope.
- **Every signal query carries entity + timeframe.** Drill-down navigation must
  compose that scope so the user never types it.

And one canonical flow that every shipped workflow prompt follows:

```
Problem ──▶ affected entities ──▶ scoped logs ──▶ related traces ──▶ root cause
```

A TUI's job is to make *entities the map* and *signals the terrain*: browse the
map freely, and drop into any signal already scoped to where you're standing.

---

## Goals

1. **Complete primitive coverage** — every noun a Dynatrace operator expects
   (services → cloud infra → frontends) is a named view, reachable in ≤2
   keystrokes plus an alias.
2. **Topology-following navigation** — from any entity, jump to its related
   entities and its signals with single keys; scope composes automatically.
3. **No query language required** — curated views with sensible columns
   (services show RED metrics, hosts show CPU/mem/disk, pods show restarts)
   cover the 95% path; DQL remains available as an escape hatch, never a
   prerequisite.
4. **k9s-grade navigation ergonomics** — command bar with fuzzy aliases,
   breadcrumbs, back/last-view toggles, scope pinning, filtering, sorting,
   hotkeys — fast enough to live in.
5. **Respect the existing model** — contexts, keyring credentials, safety
   levels, agent detection, and color rules behave identically to the CLI.
6. **Reuse, don't fork** — the TUI is a presentation layer over
   `pkg/resources/`, `pkg/exec/`, and `sdk/`; no duplicated business logic.

## Non-Goals

- **Not a query IDE.** The DQL escape hatch is one view, not the center of
  gravity.
- **Not a dashboard renderer.** Dashboards/notebooks are listed and opened via
  browser deep links, not rendered as tiles.
- **Not an agent surface.** The TUI never activates in agent mode (`--agent`,
  detected AI environments, non-TTY); agents keep the JSON envelope.
- **Not a replacement CLI.** Every view shows its CLI/DQL equivalent
  ("command echo"), so the TUI teaches the CLI rather than hiding it.
- **Not an editor.** `e` on a management asset shells out to `$EDITOR` via the
  existing `edit` flow.
- **No new SDK API surface** for phases 1–3.

---

## The Surface Area: View Catalog

Every view has a canonical name, k9s-style short aliases, curated columns, and
a set of drill-downs. Views are declarative (`ViewSpec`, see Architecture) so
adding one is configuration, not plumbing.

### Entity views (the map)

| View | Aliases | Backing data | Curated columns | Enter drills into |
|---|---|---|---|---|
| Services | `:services`, `:svc` | Smartscape + `dt.service.request.*` | name, technology, requests/min ▁▃▅ , error % , p95 latency, active problems | service detail (RED charts + endpoints) |
| Hosts | `:hosts`, `:ho` | Smartscape + host metrics | name, OS, CPU % ▁▃▅, mem %, disk %, net, state, problems | host detail (processes, containers) |
| Processes | `:processes`, `:pg` | Smartscape | name, technology, host, CPU, memory, restarts | process detail |
| Containers | `:containers` | Smartscape / container metrics | name, image, host/pod, CPU, mem, restarts | container detail |
| K8s clusters | `:clusters` | Smartscape (K8s) | name, version, nodes, pods, CPU/mem pressure, problems | nodes of cluster |
| K8s nodes | `:nodes`, `:no` | Smartscape (K8s) | name, cluster, status, CPU/mem alloc, pods, conditions | pods on node |
| K8s namespaces | `:namespaces`, `:ns` | Smartscape (K8s) | name, cluster, workloads, pods, quota usage | workloads in namespace |
| K8s workloads | `:workloads`, `:dep` | Smartscape (K8s) | name, kind, namespace, desired/ready, restarts, age, problems | pods of workload |
| K8s pods | `:pods`, `:po` | Smartscape (K8s) | name, namespace, node, phase, ready, restarts, OOMKills, age | pod detail (containers, K8s events) |
| Cloud: AWS | `:aws` | Smartscape (AWS) | grouped by service: EC2, RDS, Lambda, ELB, ECS/EKS… → per-type list with type-appropriate columns | resource detail |
| Cloud: Azure | `:azure` | Smartscape (Azure) | VMs, AKS, SQL, App Service, Functions… | resource detail |
| Cloud: GCP | `:gcp` | Smartscape (GCP) | GCE, GKE, Cloud Run, Pub/Sub… | resource detail |
| Databases | `:databases`, `:db` | Smartscape + DB spans | name, technology, host, calls/min, failure %, avg latency | statement hotspots (from spans) |
| Web apps (RUM) | `:frontends`, `:apps-web` | RUM (`user.*`) | name, type, active sessions, Apdex/Web Vitals (LCP/CLS/INP), JS error rate | frontend detail (vitals, errors, sessions) |
| Mobile apps | `:apps-mobile` | RUM | name, platform, sessions, crash rate, top crash | crash groups |
| Entities (generic) | `:entities`, `:topo` | `smartscapeNodes` | any type — free browse of the topology when no curated view fits | entity detail + relations |

### Signal views (the terrain)

Signal views opened from the command bar default to a safe scope (current
pinned scope, or a guided entity picker if the query would otherwise be
unbounded). Opened via drill-down they inherit the selection's scope.

| View | Aliases | Backing data | Notes |
|---|---|---|---|
| Problems | `:problems`, `:pb` | `dt.davis.problems` | severity, status, title, root cause, impact, age; **the investigation entry point** |
| Logs | `:logs` | `logs` | live-follow toggle, severity coloring, grouped-by-pattern mode, record inspector |
| Traces | `:traces`, `:spans` | `spans` | failed/slowest span tables per scope; trace-ID lookup (`:trace <id>`); waterfall view |
| Metrics | `:metrics` | `timeseries` | metric browser for the scoped entity; braille/sparkline charts |
| Events | `:events` | `events`, `dt.davis.events` | deployments, K8s events, Davis events; filterable by kind |
| Security | `:security`, `:vulns` | `security.events` | vulnerabilities (CVE, DSS score, affected entities), detections (MITRE), posture findings |
| RUM sessions | `:sessions` | `user.sessions` | per-app user sessions, duration, errors; session detail = action timeline |
| Costs | `:costs`, `:dps` | `dt.system.events` | DPS consumption by capability/entity, trend |

### Management views (assets — secondary)

The existing generic resource browser: `:slos`, `:workflows` (+ executions with
live log follow), `:dashboards`, `:notebooks`, `:documents`, `:segments`,
`:buckets`, `:detectors`, `:settings`, `:extensions`, `:edgeconnects`,
`:users`, `:groups`, … — one `ResourceView` implementation configured per type
from the existing `pkg/resources/<name>` display fields. These get list /
filter / describe / open / edit / delete / exec, but no bespoke layouts.

---

## Navigation Model

This is the heart of the design. Navigation has four mechanisms that compose:
**aliases** (jump anywhere), **drill-downs** (follow meaning), **relations**
(follow topology), and **scope** (carry context).

### 1. Command bar — `:`

- `:` opens the command bar; typing fuzzy-matches view names and aliases
  (`:po` → pods, `:sv` → services) with an inline completion popup.
- Arguments narrow the jump: `:logs error`, `:pods checkout`, `:trace <id>`,
  `:ctx prod-eu`.
- `:q` quits, `:help` opens the key reference.

### 2. Drill-down vocabulary — the same keys everywhere

A single, consistent verb vocabulary; every view supports the subset that makes
sense. Learn it once, use it on anything:

| Key | Meaning | On a service | On a pod | On a problem | On a log record |
|---|---|---|---|---|---|
| `enter` | primary detail | service detail | pod detail | problem detail | full record |
| `l` | logs | service logs | pod logs | problem-scoped logs | — |
| `s` | traces/spans | service traces | pod traces | problem-scoped traces | jump to trace |
| `m` | metrics | RED charts | pod metrics | RC entity metrics | — |
| `p` | problems | problems on service | problems on pod | — | — |
| `v` | events | deployments/events | K8s events | evidence events | — |
| `x` | related entities | host, callers, callees | workload, node, ns | affected entities | source entity |
| `u` | SLOs | SLOs targeting it | — | related SLOs | — |
| `d` | describe (YAML pager) | ✓ | ✓ | ✓ | ✓ |
| `o` | open in browser (deep link) | ✓ | ✓ | ✓ | ✓ |

All of `l s m p v u` open the target view **pre-scoped to the selection and the
active timeframe** — this is how the "never run an unscoped query" rule becomes
structural rather than advisory.

### 3. Relations — `x`, the topology hop

`x` on any entity opens the **relations panel**, populated from Smartscape:

```
┌ relations: checkout-svc (SERVICE) ─────────────────┐
│ runs on        host-4711            HOST           │
│ instance of    checkout             SERVICE_GROUP  │
│ calls          payments-gw          SERVICE        │
│ called by      edge-gateway         SERVICE        │
│ part of        prod-cluster/shop    K8S_NAMESPACE  │
└─────────────────────────────────────── enter: go ──┘
```

`enter` navigates to that entity's detail view. This one panel makes the whole
topology walkable: service → host → its other processes → a noisy neighbor;
pod → workload → namespace → the sibling workload that's actually broken.

### 4. Scope — pin where you're standing

- `.` on any entity **pins it as the global scope** (shown in the header, e.g.
  `⌖ ns: shop-prod`). While pinned, every view opened from the command bar is
  filtered to it: `:pods` shows the namespace's pods, `:logs` the namespace's
  logs, `:problems` its problems. `.` again (or `ctrl-x`) unpins.
- Pinning composes with drill-down scope; drill-down always wins (it's more
  specific).
- `t` opens the **timeframe picker** (30m / 2h / 24h / 7d / custom); the active
  timeframe is global and applies to every metric column and signal view.

### 5. Movement & recall

| Key | Action |
|---|---|
| `esc` | back (pop breadcrumb stack; view state and data preserved) |
| `-` | toggle between the two most recent views (k9s-style) |
| `/` | incremental filter of the current table; `esc` clears |
| `shift-j/k` or click header | sort by column, toggle direction |
| `1`–`9` | hotkeys — user-assignable view bookmarks (`:hotkeys` to manage; defaults: 1 problems, 2 services, 3 hosts, 4 pods, 5 logs) |
| `r` / `R` | refresh now / cycle auto-refresh (off/10s/30s/60s) |
| `ctrl-q` | **reveal query** — open the current view's generated DQL in the query escape hatch |
| `c` | copy the equivalent dtctl command for the current view |
| `?` | help overlay (all bindings, per-view) |

**Breadcrumbs** render in the header:
`problems ▸ P-2407 ▸ logs (checkout-svc, 14:02–14:31)` — every element of the
path carries its scope, and `esc` walks back up with state intact.

### Example journeys

**Incident triage** (the `dt-incident-response` flow, ~8 keystrokes):
`1` (problems) → `enter` on the critical one → `l` (logs, pre-scoped, grouped
by pattern) → `enter` on the top error group → `s` (jump to its trace) →
waterfall shows the failing downstream call → `x` → the culprit service →
`v` shows a deployment event 4 minutes before the problem started.

**K8s crashloop**: `:po` → `/crash` → `enter` on the pod → `v` (K8s events:
OOMKilled) → `x` → workload → `m` (memory chart: sawtooth against the limit)
→ `.` pin the namespace → `:events` to see what else changed there.

**Slow frontend**: `:frontends` → sort by LCP → `enter` → Web Vitals panel →
`s` (traces behind the slow pages) → backend service in the waterfall → `p`
(its problems) — frontend-to-backend in five keys, the `dt-obs-frontends`
correlation path.

---

## User Experience

### Entry point

```bash
dtctl tui                # launch, home view, current context
dtctl tui pods           # launch directly into a view (any alias works)
dtctl tui --context prod # launch against a specific context
```

Guards, checked before entering the alternate screen: not a TTY → error; agent
mode → structured error envelope (`"ui is interactive-only"`); `--plain` →
error suggesting non-interactive equivalents.

### Layout

```
┌ dtctl ─ ctx: prod-eu ─ safety: readonly ─ ⌖ ns: shop-prod ─ last 2h ⟳ 30s ──┐
│ ▸ pods (shop-prod)                                                          │
├──────────────────────────────────────────────────────────────────────────────┤
│ NAME                    NODE      PHASE     READY  RST  OOM  CPU     AGE    │
│ checkout-7d4f8-x2lp4    node-3    Running   2/2    14   2    ▂▅▇▅    2d     │
│ payments-5c9b7-qq8minor node-1    Running   1/1    0    0    ▁▁▂▁    9d     │
│ cart-6b5d4-mm2yz        node-2    CrashLoop 0/1    31   0    ▁▁▁▁    3h     │
├──────────────────────────────────────────────────────────────────────────────┤
│ <l>ogs <s>pans <m>etrics <p>roblems e<v>ents <x>rel <.>pin  </>filter <?>   │
└──────────────────────────────────────────────────────────────────────────────┘
```

- **Header**: context, safety level (color-coded: readonly green,
  readwrite-mine yellow, readwrite-all/unrestricted red), pinned scope,
  timeframe, refresh; breadcrumb line below.
- **Footer**: the drill-down vocabulary available on the current selection,
  plus a transient status line used for errors and **command echo** — after
  every navigation it prints the CLI equivalent, e.g.
  `≡ dtctl query 'fetch logs | filter k8s.pod.name == "cart-6b5d4-mm2yz" …' --from -2h`.

### Home view (`:home`, default)

A triage landing page, echoing `dt-health-check` / `dt-daily-standup`: active
problems by severity, unhealthy-SLO count, top services by error rate, recent
deployment events, failing workflow executions. Panels load independently
(spinner per panel); `enter` on a panel opens the full view.

### The DQL escape hatch (`:query` / `ctrl-q`)

One view, deliberately last in this document: a query editor + results pane
with the existing live progress (scanned GB / records via the SDK's
`PollUpdate`), cancellation, renderer cycling (table → sparkline → braille →
bar), and query history. Its main entrance is `ctrl-q` from any view —
**reveal the query behind what you're looking at**, tweak it, and re-run. This
is where curated navigation gracefully hands over to power users, and it makes
the TUI a DQL *teacher*: every screen can show you how it was made.

### Mutations and safety

The TUI is read-first; entity and signal views have no mutating actions at all.
On management assets (`e`dit, `ctrl-d`elete, e`x`ecute workflow):

1. **Safety-checker gated** — `safety.Checker.Check(op, ownership)` decides
   whether the action even appears in the footer; in a `readonly` context the
   TUI is purely a browser.
2. **Confirmed** — modal equivalents of `prompt.ConfirmDeletion`
   (type-to-confirm for data-destructive ops), matching CLI behavior.
3. **Echoed** — the status line prints the equivalent CLI command afterwards.

---

## Detail Pages

`enter` on any row opens a detail page. The designs below are grounded in a
live-tenant exploration with dtctl itself (Smartscape node shapes, verified
edge types, `fieldsSnapshot logs/spans`, the `metrics` command, and the
`dt.semantic_dictionary` tables) — every field and metric named here was
confirmed to exist. Where the tenant taught us a gotcha, it's called out.

### Common anatomy

Every detail page shares one chrome:

```
┌ svc: checkout-svc ──────────────── SERVICE · SERVICE-4F2A9C… · ⧉ 2 problems ┐
│ runs on 3 pods · prod-cluster/shop · seen 42d · last 2h                      │
│ ╭──────────╮──────────────────────────────────────────────────────────────  │
│ │ Overview │ Endpoints   Infrastructure   Problems                          │
│ ╰──────────╯                                                                 │
│  <tab body>                                                                  │
├──────────────────────────────────────────────────────────────────────────────┤
│ <tab>/[ ] switch tab  <l s m p v x u> signals  <o>pen <d>escribe <esc> back │
└──────────────────────────────────────────────────────────────────────────────┘
```

- **Identity header** (2 lines): display name, type badge, entity ID
  (truncated, `y` to yank), problem indicator, and a *relationship one-liner*
  built from Smartscape references (where it runs, what it belongs to, age
  from `lifetime`).
- **Tab bar**: `tab` / `shift-tab` (also `[` / `]`) cycle tabs. Tabs hold
  content that *belongs to* the object (summaries, embedded lists, charts).
  The signal keys (`l s m p v`) keep their global meaning — they *leave* the
  page into a full, pre-scoped signal view. Rule of thumb: tabs answer "what
  is this thing's state?", signal keys answer "let me dig into its telemetry".
- **Lazy tabs**: each tab loads on first focus (spinner per tab), so opening a
  detail page costs one query, not five.
- Every chart on a tab is a `timeseries` query over the global timeframe;
  every embedded list is `enter`-able (rows navigate to their own detail or a
  scoped signal view).

### Problem detail (`:problems` → enter)

The most bespoke page — it is the front door of every investigation.
Grounded in `dt.davis.problems` fields.

```
┌ problem: P-2508 ────────────── DAVIS_PROBLEM · CRITICAL · ACTIVE · 34m ─────┐
│ Failure rate increase on checkout-svc                                        │
│ ╭──────────╮───────────────────────────────────────────────────────────────  │
│ │ Overview │ Evidence   Impact   Related                                     │
│ ╰──────────╯                                                                  │
│ Started    14:02:11 (34m ago)          Status      ACTIVE (open → active)    │
│ Category   ERROR                        Impact      SERVICE                   │
│ Cluster    prod-cluster                 Namespace   shop                      │
│ Workload   checkout (deployment)                                              │
│ Flags      ⚑ frequent-event  ·  not muted  ·  no maintenance window          │
│                                                                               │
│ Davis says:                                                                   │
│  The failure rate of checkout-svc increased to 12.4% (baseline 0.3%).        │
│  Root cause: connection pool exhaustion on payments-gw.        (event.descr.) │
└───────────────────────────────────────────────────────────────────────────────┘
```

| Tab | Content | Source |
|---|---|---|
| **Overview** | title, severity/status badges with `event.status_transition`, start + duration, `event.description` (Davis's own explanation, wrapped), impact level, K8s context (`k8s.cluster.name`, `k8s.namespace.name`, `k8s.workload.kind/name`), flags (`dt.davis.is_duplicate`, `is_frequent_event`, `mute.status`, `maintenance.is_under_maintenance`) | `dt.davis.problems` |
| **Evidence** | timeline of the constituent Davis events — kind, type (e.g. `RESOURCE_CONTENTION_EVENT`), start, source entity; `enter` → event detail | `dt.davis.event_ids` → `dt.davis.events` |
| **Impact** | affected-entities table (name, type, → entity detail) | `smartscape.affected_entity.ids/types` + `affected_entity_names` |
| **Related** | related (non-affected) entities — the wider blast radius | `smartscape.related_entity.ids` |

Signal keys are scoped to **the problem's affected entities and its time
window** (`event.start` → now/close): `l` error logs grouped by pattern, `s`
failed traces, `m` metrics of the root-cause entity, `v` events in the window.
This is the canonical `dt-troubleshoot-problem` flow as four keystrokes.

### Service detail (`:svc` → enter)

**Tenant gotcha**: SERVICE Smartscape nodes are sparse (little more than
`name` and detection version) — everything interesting comes from metrics and
spans. The page is therefore chart- and span-driven.

| Tab | Content | Source |
|---|---|---|
| **Overview** | three braille charts over the timeframe: request rate, failure rate %, response time (`avg`, with p90 toggle where percentiles exist); totals + delta vs previous window | `timeseries` on `dt.service.request.count`, `.failure_count`, `.response_time` |
| **Endpoints** | table: endpoint, req/min, fail %, avg/max duration; sortable; `enter` → traces view filtered to that endpoint | `fetch spans \| filter dt.smartscape.service == <id> \| summarize by:{endpoint.name}` |
| **Infrastructure** | where it runs, as an indented tree: pods → containers → nodes → hosts; each row `enter`-able | Smartscape `runs_on` / `belongs_to` edges (verified: SERVICE runs_on K8S_POD/CONTAINER/HOST/PROCESS) |
| **Problems** | problems whose affected entities include this service | `dt.davis.problems` filtered on `affected_entity_ids` |

`u` lists SLOs targeting the service; `s` opens the full traces view scoped to
it; `x` walks `calls` edges (callers/callees).

### Host detail (`:hosts` → enter)

HOST nodes are field-rich (`os.*`, `cores`, `memory`, `ip`, `cloud.provider`,
`aws.*`, `hypervisor.type`, `dt.host_group.id`) and the tenant confirms DISK
and NETWORK_INTERFACE as child entities plus a deep `dt.host.*` metric
namespace — enough for a btop-style page.

```
│ ╭──────────╮────────────────────────────────────────────────────────────────
│ │ Overview │ Processes   Disks   Network   Containers
│ ╰──────────╯
│ Ubuntu 22.04 (x86_64) · 8 cores (16 logical) · 32 GiB · AWS ec2 m6i.2xlarge
│ ip 10.179.57.179 · host group prod-eu · uptime 42d · OneAgent monitored
│
│ CPU   ▂▃▅▇▆▅▃▂▁▂▃▄  61%      MEM  ▄▄▅▅▅▆▆▆▆▇▇▇  78%     LOAD  2.1 / 5m
│ DISK r/w ▁▂▁▁▃▁     34 MB/s  NET rx/tx ▂▃▂▂▅▃   210 Mb/s
```

| Tab | Content | Source |
|---|---|---|
| **Overview** | identity block + four charts: CPU (`dt.host.cpu.usage`, stacked user/system/iowait/steal toggle), memory (`dt.host.memory.avail.percent`), load (`.cpu.load/load5m/load15m`), disk & net top-lines; the EC2 instance behind it linked via `runs_on AWS_EC2_INSTANCE` | `dt.host.*` metrics + node fields |
| **Processes** | table: name, technology, CPU, memory (sortable — "what's eating this host") | PROCESS `runs_on` HOST + `dt.process.*` metrics |
| **Disks** | per-disk table: mount, used % (gradient bar), free, IOPS r/w, latency, inodes | DISK `belongs_to` HOST + `dt.host.disk.*` |
| **Network** | per-NIC table: rx/tx throughput, packets, errors, drops | NETWORK_INTERFACE `belongs_to` HOST + `dt.host.net.nic.*` |
| **Containers** | containers on the host: name, image, pod, CPU, memory | CONTAINER `runs_on` HOST |

### K8s pod detail (`:po` → enter)

K8S_POD nodes carry `k8s.pod.phase`, workload/replicaset/node/namespace names,
and full labels/annotations (`tags:k8s.labels`) — plus `dt.kubernetes.container.*`
metrics for the limits/usage story.

| Tab | Content | Source |
|---|---|---|
| **Overview** | phase, node (→), workload (→), namespace, age, cost center; labels/annotations (collapsed, `L` expands); charts: CPU usage vs requests/limits, memory working-set vs limit, CPU throttling, pod network rx/tx | node fields + `dt.kubernetes.container.cpu_usage/.cpu_throttled/.limits_*/.requests_*/.memory_working_set`, `dt.kubernetes.pod.network_*` |
| **Containers** | per-container: name, image, ready, restarts, CPU/mem vs limits (gradient bars) | CONTAINER `is_part_of` K8S_POD |
| **Events** | K8s events for the pod (created, scheduled, OOMKilled, backoff…) | `fetch events` scoped to pod |
| **Config** | mounted ConfigMaps, Secrets (names only), PVCs with capacity/used | `uses` edges → K8S_CONFIGMAP / K8S_SECRET / K8S_PERSISTENTVOLUMECLAIM + `dt.kubernetes.persistentvolumeclaim.*` |

`l` opens the pod's log stream (follow-capable) — the single most common K8s
action. Workload and node details follow the same pattern: **workload** =
overview (desired vs ready from `dt.kubernetes.workload.pods_desired`,
conditions, HPA via `K8S_HORIZONTALPODAUTOSCALER uses` edge) + pods tab +
events tab; **node** = overview (conditions, allocatable vs usage from
`dt.kubernetes.node.*`, the HOST behind it via `runs_on`) + pods tab.

### Frontend detail (`:frontends` → enter)

Backed by FRONTEND nodes (`frontend.type` web/mobile) and the `dt.frontend.*`
metric namespace, which the tenant confirms includes full Web Vitals.

| Tab | Content | Source |
|---|---|---|
| **Overview** | type, instrumentation id; charts: active sessions & users (estimated), user-action rate & duration, error count | `dt.frontend.session.active.estimated_count`, `.user.active.estimated_count`, `.user_action.count/.duration`, `.error.count` |
| **Web Vitals** | LCP / CLS / INP (+ FID, TTFB, DOM-interactive, load-event) charts, each colored against Good / Needs-improvement / Poor thresholds | `dt.frontend.web.page.largest_contentful_paint`, `.cumulative_layout_shift`, `.interaction_to_next_paint`, `.first_input_delay`, `dt.frontend.web.navigation.*` |
| **Errors** | error-rate trend + top error groups | `dt.frontend.error.count` + `user.events` where ingested |
| **Sessions** | recent sessions: user, duration, actions, errors; `enter` → session action timeline | `user.sessions` / `user.events` |

### Database detail (`:db` → enter)

The tenant monitors Postgres deeply: DB_INSTANCE/DB_DATABASE/DB_TABLE/DB_INDEX
entities and a wide `postgres.*` metric namespace — enough for a real DBA page.

| Tab | Content | Source |
|---|---|---|
| **Overview** | instance/database identity, connection & conflict trends, I/O and SLRU cache charts | `postgres.activity.*`, `postgres.io.*`, `postgres.slru.*`, `postgres.database_conflicts.*` |
| **Statements** | top statements by calls / total time / failures; `enter` → traces containing that statement | `fetch spans \| filter isNotNull(db.query.text) \| summarize by:{db.operation.name, db.query.text}` |
| **Tables** | per-table rows/size/scans/bloat indicators | DB_TABLE entities + `postgres.tables.*` (28 metric keys confirmed) |
| **Callers** | services calling this database | `calls` edges, backward |

### Cloud resource detail (generic, all `AWS_* / AZURE_* / GCP_*` types)

One generic page covers the ~40 AWS types found in the tenant (EC2, ELB/target
groups, EKS/ECS, RDS/DynamoDB, VPC/subnets/SGs, IAM, …):

| Tab | Content | Source |
|---|---|---|
| **Properties** | all node fields, namespaced groups (`aws.*`), tags table | Smartscape node (heavy `aws.object` JSON only fetched on demand via `d`) |
| **Relations** | attached / part-of / uses / used-by, as a two-direction list | `references` (static forward) + `smartscapeEdges` (backward/dynamic) |
| **Metrics** | provider metrics for the resource | `cloud.aws.*` keys filtered by resource dimension |

Type-specific ViewSpecs can add a tab (e.g. target-group → healthy-targets)
without a new page implementation.

### Trace waterfall (traces view → enter)

Confirmed span fields: `span.name`, `span.kind`, `span.parent_id`, `duration`,
`start_time`, `request.is_failed`, `endpoint.name`, `http.route`,
`db.system.name` / `db.query.text`, `code.function`, `trace.id`.

```
┌ trace: b617ac8d… ──────────────────── 12 spans · 341ms · 1 failed ──────────┐
│ ▼ POST /checkout                 server   checkout-svc   ████████████  341ms │
│   ▼ authorize                    internal checkout-svc    ██▁            41ms │
│   ▼ POST /payments/charge        client   checkout-svc      ████████    212ms │
│     ▼ POST /payments/charge      server   payments-gw        ███████    198ms │
│       ✗ SELECT pool.acquire      client   payments-gw          █████    170ms │
│   ▼ INSERT orders                client   checkout-svc              ██   38ms │
└───────────────────────────────────────────────────────────────────────────────┘
```

Tree from `span.parent_id`; bars proportional on the trace's time axis; kind
and service columns; failed spans (`request.is_failed`) marked `✗` red.
`enter` on a span → attribute inspector; `l` → logs with the same `trace.id`
(both directions of the logs↔traces link); `x` → the span's service entity.

### Log record inspector (logs view → enter)

Full record in a pager, but **grouped by namespace** rather than flat YAML —
the tenant shows log records routinely carry 50+ fields:

```
│ 14:02:41.113  ERROR                                                          │
│ content   connection pool exhausted: timeout acquiring connection after 30s  │
│ ── kubernetes ──────────────  ── entity ─────────────────  ── http ───────── │
│ cluster    prod-cluster        dt.smartscape.service …      method  POST     │
│ namespace  shop                dt.entity.service     …      status  502      │
│ pod        payments-gw-7d4f…                                route   /charge  │
```

`content` always on top and wrapped; namespace groups (`k8s.*`, `http.*`,
entity IDs, `dt.openpipeline.*`) collapsible; field descriptions from the
semantic dictionary shown on focus (see Runtime discovery). `s` jumps to the
trace when a trace ID is present; `x` to the source entity.

### Findings that shape all pages

Three lessons from the live exploration, baked into the design:

1. **Dual entity-ID eras.** Records carry both deprecated `dt.entity.*` and
   modern `dt.smartscape.*` fields (logs in the tenant have
   `dt.entity.service` *and* `dt.smartscape.service`). Scope filters generated
   by drill-downs must match on either until migration completes.
2. **Entity nodes vary wildly in richness.** HOST and K8S_POD are field-rich;
   SERVICE and FRONTEND are nearly bare and get their substance from metrics
   and spans. `DetailSpec` must let a page be chart-first, not assume
   properties exist.
3. **Silent emptiness.** Wrong node/edge/metric names return empty results,
   not errors. Detail tabs therefore render explicit "no data in timeframe /
   not monitored" states, and the relations panel is driven by *discovered*
   edges (see below), never a hardcoded edge list.

---

## Architecture

### Library choice

**bubbletea + bubbles + lipgloss** (charmbracelet), as anticipated in
`ARCHITECTURE.md`. Elm-style `Model/Update/View` fits "views over shared app
state"; async API results and `PollUpdate` progress arrive as `tea.Msg`s;
`bubbles` supplies table, viewport, textinput, spinner, and help widgets.

Alternatives considered: tview/tcell (heavier, imperative) and growing the
hand-rolled ANSI layer (`live.go`, `progress.go`) into a framework (rejected —
input handling, focus, and compositing are exactly what a framework should
own). Consequence to accept: two styling systems — `pkg/output/styles.go` raw
ANSI for CLI output, lipgloss in the TUI. The chart/sparkline/braille renderers
emit plain strings and embed cleanly in either; a `pkg/tui/theme` adapter keeps
the palette consistent and delegates capability detection to the existing
`ColorEnabled()` logic. New dependencies go in the root module only — **the SDK
stays TUI-free**.

### The ViewSpec catalog — declarative views

The catalog above must not become thirty bespoke screens. Entity and signal
views are data:

```go
type ViewSpec struct {
    Name      string            // "pods"
    Aliases   []string          // "po"
    Kind      ViewKind          // Entity | Signal | Asset
    List      QueryTemplate     // scope-aware DQL (or resource-handler call)
    Enrich    []MetricColumn    // e.g. CPU sparkline: separate timeseries query,
                                //   batched per page, rendered via output.RenderColoredSparkline
    Columns   []ColumnSpec      // field, header, width, align, colorRule
    Drill     map[Verb]Target   // 'l' → logs view + scope mapping, 's' → traces, …
    Relations RelationSpec      // how to resolve Smartscape neighbors for 'x'
    Detail    DetailSpec        // identity header + []TabSpec (see Detail Pages);
                                //   each TabSpec = query/queries + layout, loaded lazily
}
```

- `QueryTemplate` renders with the current `Scope` (pinned entity, drill-down
  entity, timeframe) — reusing `pkg/util/template`.
- Metric-column enrichment runs as a second, batched query per visible page
  (capped rows, single `timeseries` query with `by:` the entity id), so entity
  lists stay fast and enrichment failures degrade to blank cells, never errors.
- Adding a cloud resource type or a new signal view = adding a ViewSpec, not
  writing a screen.

Management assets use the same table shell configured from the existing
`pkg/resources/<name>` display fields instead of a DQL template.

### Runtime discovery — the environment describes itself

The live-tenant exploration confirmed that Grail is fully introspectable, so
the TUI hardcodes shapes only as *defaults* and discovers the rest per
environment, cached per session:

| Mechanism | DQL | Powers |
|---|---|---|
| Node-type census | `smartscapeNodes "*" \| summarize count(), by:{type}` | which entity views appear in the command bar at all (no `:aws` in an Azure-only tenant), with counts shown in the alias popup |
| Edge catalog | `smartscapeEdges "*" \| summarize count(), by:{source_type, type, target_type}` | the relations panel (`x`) and which drill-downs each detail page offers — never a hardcoded edge list (wrong edges fail *silently* as empty results) |
| Field census | `fieldsSnapshot logs` / `fieldsSnapshot spans` | record-inspector field ordering (by prevalence) and an optional column picker for signal views |
| Metric catalog | `metrics` command (keys + dimensions) | the `:metrics` browser for a scoped entity, and graceful degradation of chart panels when a metric namespace is absent |
| Semantic dictionary | `fetch dt.semantic_dictionary.fields / .models` | on-focus field descriptions, units, and enum values in inspectors; model→table mapping for the generic entity browser |

All five are cheap metadata queries, fetched lazily on first use and cached
for the session (refreshed on context switch). This keeps the ViewSpec catalog
small and honest: curated defaults where curation adds value, introspection
everywhere else.

### Package layout

```text
cmd/
  tui.go                 # cobra command: guards, flag wiring, launches tui.App
pkg/tui/
  app.go                 # root tea.Model: view stack, command bar, routing, global keys
  theme/                 # lipgloss styles; adapter over output.ColorEnabled()
  catalog/               # ViewSpec definitions (one file per domain:
                         #   k8s.go, hosts.go, services.go, cloud_aws.go, rum.go, …)
  views/                 # the generic engines, not per-noun screens:
    table.go             #   entity/signal/asset table shell driven by ViewSpec
    detail.go            #   panel-composed detail view driven by DetailSpec
    problems.go          #   problem detail (bespoke: evidence timeline)
    waterfall.go         #   trace waterfall (bespoke)
    logs.go              #   log stream + record inspector (bespoke: follow mode)
    query.go             #   DQL escape hatch
    home.go, ctx.go, help.go
  components/            # statusbar, cmdbar, confirm modal, timeframe picker,
                         #   relations panel, dql-progress bar, chart pane
  scope.go               # Scope{Entity, Entities, Timeframe, Pins}; renders DQL fragments
  topo.go                # Smartscape relation resolution for the relations panel
  datasource.go          # async adapters: pkg/exec + pkg/resources calls → tea.Cmd/tea.Msg
```

Rules, mirroring existing layering: `pkg/tui` imports `pkg/resources`,
`pkg/exec`, `pkg/output` (renderers), `pkg/safety`, `pkg/config`; nothing
imports `pkg/tui` except `cmd/tui.go`; no HTTP in `pkg/tui`; every API call is a
`tea.Cmd` goroutine with `context.Context` cancellation tied to view lifetime.

### Refresh & data handling

- Views own their data + `lastFetched`; auto-refresh is a per-view `tea.Tick`
  honoring the global interval. The breadcrumb stack keeps popped views alive
  for instant `esc`.
- `/` filtering and sorting are client-side over the fetched page; entity
  lists are paged (DQL limit + "load more" on scroll-past-end). Server-side
  narrowing is what scope pinning is for — consistent with the "no custom
  query flags" principle.
- Signal views opened without any scope prompt for one (entity picker) rather
  than running an unbounded query.

### Testing

- **Model tests**: bubbletea models are pure (`Update(msg) → model, cmd`) —
  drive with synthetic messages, assert navigation, scope composition, and
  safety gating. No TTY needed.
- **Catalog tests**: every ViewSpec's `QueryTemplate` renders against fixture
  scopes to golden DQL strings — catching scope-composition regressions
  cheaply.
- **View snapshots**: `View()` output → golden files via the existing
  `cmd/testutil/golden.go` harness (fixed dimensions, plain styling, synthetic
  data per the privacy rules).
- **Datasource tests**: httptest mocks, same patterns (and pagination guards)
  as existing resource tests.

---

## Implementation Phases

### Phase 1 — Shell + the core map (services, hosts, problems, logs)

- `cmd/tui.go` guards; app shell: command bar, breadcrumbs, footer, help,
  theme adapter, timeframe picker.
- ViewSpec engine (table shell + detail shell) with the first catalog slice:
  **problems, services, hosts, logs** — enough for the core triage loop.
- Drill-down vocabulary (`l m p v d o enter esc -`), command echo, refresh.
- Read-only. Success criterion: the incident-triage journey works end to end.

### Phase 2 — Topology + traces + Kubernetes

- Relations panel (`x`) over `smartscapeNodes`; scope pinning (`.`).
- Traces view + waterfall; log ↔ trace jumps.
- Kubernetes catalog: clusters, nodes, namespaces, workloads, pods, K8s events.
- Metric-column enrichment (sparklines in entity tables), sorting, hotkeys.

### Phase 3 — Breadth: cloud, frontends, security, costs, escape hatch

- AWS/Azure/GCP inventories, databases, web/mobile apps + RUM views
  (sessions, Web Vitals, errors), security findings, DPS costs.
- DQL escape hatch with `ctrl-q` reveal-query, live progress, history.
- Home/overview view.

### Phase 4 — Assets & mutations

- Management resource browser for the full existing CRUD surface; workflow
  executions with live log follow.
- Safety-gated edit (`$EDITOR` suspend/restore), delete confirms, workflow
  execute. Clipboard for command echo. Stretch: export a breadcrumb trail as
  a notebook.

Each phase ships independently; Phase 1 alone is a usable "k9s for Dynatrace
triage".

---

## Open Questions

1. **Bare `dtctl` launch**: the command is `dtctl tui`; should a bare `dtctl`
   in an interactive terminal also launch it? Proposal: no — bare `dtctl`
   keeps printing help (agents probe with bare invocations).
2. **Entity list sourcing**: Smartscape (`smartscapeNodes`) vs classic
   `dt.entity.*` fetches — Smartscape is the strategic choice per
   `dt-migration`, but column availability per entity type needs a spike;
   ViewSpec isolates the decision per view.
3. **Metric enrichment cost**: sparkline columns mean one extra timeseries
   query per page per view. Acceptable with paging + caching per refresh tick?
   Needs measurement; worst case they become opt-in per view (`ctrl-m`).
4. **Live log follow semantics**: periodic re-query with advancing window
   (like `--live`) vs tail-style append; append needs stable ordering
   guarantees from Grail — start with re-query.
5. **Windows terminal support**: bubbletea handles Windows, but alternate
   screen + `$EDITOR` suspend needs explicit testing (existing
   `console_windows.go` VT enablement must run before bubbletea init).
6. **Smartscape topology *visualization*** (graph drawing) — deferred; the
   relations panel covers navigation without a graph-layout problem.

## References

- `docs/dev/ARCHITECTURE.md` — prior "Interactive Mode" future idea
- `docs/dev/WATCH_MODE_DESIGN.md` — existing live/watch semantics
- `pkg/output/progress.go`, `live.go`, `watch.go` — current live rendering
- `pkg/output/sparkline.go`, `braille.go`, `chart.go`, `barchart.go` — reusable chart renderers
- `pkg/safety/checker.go`, `docs/dev/context-safety-levels.md` — safety model
- [dynatrace-for-ai](https://github.com/Dynatrace/dynatrace-for-ai) — skills & workflow prompts that informed the catalog and flows
- [k9s](https://k9scli.io/) — navigation-model inspiration (aliases, hotkeys, drill-downs)
