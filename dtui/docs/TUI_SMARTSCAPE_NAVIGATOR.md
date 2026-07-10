# Smartscape Navigator — TUI Concept

**Status:** Implemented (Phases 1 & 2) — Phase 3 (lookahead & polish) open
**Created:** 2026-07-08 · **Implemented:** 2026-07-08 (verified live on the box tenant)
**Author:** dtctl team

> Implementation deviations from the draft below: the mesh toggle is **`M`**,
> not `t` (`t` is the global timeframe picker and never reaches views), and
> `g`/`G` stay cursor home/end — the overview is `esc` or `:nav` away.
> Live verification surfaced verbs beyond the documented four (`belongs_to`,
> `uses`); they group generically and rank as structure. The schema query's
> lazy-projection + summarize combination is now **validated live**. A full
> edge page renders as "N+ relations (edge limit)" so truncation never reads
> as completeness. Code: `pkg/tui/navigator.go`, `pkg/tui/catalog/smartscape.go`.

## Summary

A dedicated top-level TUI app (`:nav`, aliases `smartscape`, `navigator`) for
exploring the Smartscape topology graph. Three levels, one layout language:

1. **Overview** — what exists: entity-type census *plus* the type-level
   relationship schema ("SERVICE calls SERVICE ×1.2k, runs_on HOST ×142").
2. **Type browser** — the instances of one type, with health dots.
3. **Walk mode** — an ego-centric, re-rootable neighbor tree around one
   entity, with a persistent **trail** breadcrumb, a **preview pane**, and
   graph-semantic backtracking.

It answers the user request directly: *overview over existing entities and
their relationships* (level 1), *walking those relationships* (level 3),
*drilling into each* (existing detail pages + scoped signal drills, reused).

**Explicitly rejected: drawing the graph.** A force-directed hairball is the
part of web Smartscape that does *not* work, and lipgloss v1 cannot composite
overlays anyway (TUI_LEARNINGS §3b). The terminal-native answer is grouped
lists, trees, and a trail — the ranger/Miller-column idiom, not ASCII art.

---

## The problem: why graph navigation is hard (in any UI)

The navigator is designed feature-by-feature against five failure modes of
topology exploration. Each feature below exists to kill one of these:

| # | Failure mode | Navigator answer |
|---|---|---|
| 1 | **Hairball** — too many nodes/edges at once | Ego view only; neighbors grouped by (direction, verb); fan-out caps with `… +N more`; mesh/structure toggle |
| 2 | **Losing your place** — five hops in, no idea where you are | Persistent trail breadcrumb; `←` backtracks the *walk*, not the page stack; session node cache makes revisits instant |
| 3 | **Committing before knowing** — hopping just to see if a node is interesting | Preview pane (key facts + health of the highlighted neighbor, no hop); later: expand-in-place lookahead |
| 4 | **Undifferentiated edges** — containment and traffic mixed | Verb grouping, structure-before-mesh ranking (proven in `relationsView.edgeRank`), direction glyphs `▸`/`◂` |
| 5 | **No sense of scale or health** — which neighbor matters? | Counts on every group, active-problem dots on every node, edge counts in the overview schema |

## Prior art in this repo — and the gap

- `pkg/tui/relations.go` — `relationsView` behind global `x`: one-hop edge
  list, both directions in one query, batched name resolution, structure-first
  ranking. **This is the navigator's data seed.** Its limitation is UX: every
  hop pushes a detail page onto the app stack, so a 10-hop walk is 10 stack
  entries and zero sense of the path (failure mode 2).
- `entitiesSpec` (`:topo` / `:census`, `catalog/cloud.go:35`) — type census
  table. Counts only; no relationships (half of level 1).
- `detailView`'s "related" tab (`detail.go:276`) — embedded one-hop relations.
- `TUI_DESIGN.md` Open Question 6 defers Smartscape topology *drawing*. This
  concept resolves it: don't draw; navigate.

The gap is not data access — it is a **walking surface**: trail, peek,
grouping, health, and an overview of the type schema.

---

## Design

### One layout language

Every level is `[list on the left] [preview on the right]` with a context
line on top. Below ~100 columns the preview pane collapses (toggle: `tab`).

### Level 1 — Overview (`:nav`)

```text
┌ nav ▸ overview ──────────────────────────────────── prod ▪ last 24h ┐
│ TYPE                 COUNT  ●   │ SERVICE                            │
│ K8S_POD                890  2   │ 142 entities · 3 with problems     │
│▌SERVICE                142  3 ▐ │                                    │
│ K8S_WORKLOAD           120  1   │ outgoing                           │
│ HOST                    48  ·   │   calls        ▸ SERVICE     1.2k  │
│ K8S_NODE                12  ·   │   calls        ▸ DB_INSTANCE   84  │
│ DB_INSTANCE_POSTGRES     6  1   │   runs_on      ▸ HOST         142  │
│ FRONTEND                 4  ·   │ incoming                           │
│ …                               │   is_part_of   ◂ K8S_WORKLOAD 138  │
│                                 │   calls        ◂ SERVICE     1.2k  │
│ enter browse · / filter         │   calls        ◂ FRONTEND      12  │
└──────────────────────────────────────────────────────────────────────┘
```

Left: census (type, count, active-problem count). Right: the **schema
neighborhood** of the highlighted type — which verbs connect it to which peer
types, with edge counts. This is the meta-graph: small enough to be readable,
and it *is* the "overview over entities and their relationships".

`enter` → level 2 for the highlighted type.

### Level 2 — Type browser (`:nav SERVICE`)

```text
┌ nav ▸ SERVICE ─────────────────────────────────────── 142 entities ┐
│ NAME               ●  │ payments                                    │
│ cart               ·  │ SERVICE-A1B2C3D4E5F60718                    │
│ inventory          ·  │ ● 1 active problem: P-2607…                 │
│▌payments           ● ▐│ ─ key facts ─                               │
│ shipping           ·  │ technology: java                            │
│ …                     │ k8s.namespace: checkout                     │
│ / filter · enter walk │ ─ relations ─  calls▸3  runs_on▸1  ◂12      │
└──────────────────────────────────────────────────────────────────────┘
```

A deliberately lightweight list (name, health, id) — not an embedded
`tableView`. Users who want the full curated table with lenses and sparklines
already have `:services` et al.; this level exists only to pick a walk root.
`enter` → level 3 rooted at the highlighted entity.

### Level 3 — Walk mode (`:nav SERVICE-A1B2…` or `X` anywhere)

```text
┌ nav ▸ walk ──────────────────────────────────────── prod ▪ last 24h ┐
│ TRAIL  overview ▸ SERVICE payments ▸ HOST ip-10-0-3-17 ▸ K8S_POD ca…│
├──────────────────────────────────────┬───────────────────────────────┤
│▌K8S_POD cart-7f9d        ● 1 problem▐│ SERVICE payments              │
│                                      │ SERVICE-A1B2C3D4E5F60718  ● ok│
│ ▾ outgoing                           │ ─ key facts ─                 │
│   ▾ calls (2)                        │ technology: java              │
│     ▸ SERVICE payments               │ k8s.namespace: checkout       │
│     ▸ SERVICE inventory              │ ─ health ─                    │
│   ▾ runs_on (1)                      │ 0 active problems             │
│     ▸ K8S_NODE ip-10-0-3-17          │                               │
│ ▾ incoming                           │ →/enter walk here · d details │
│   ▾ is_part_of (1)                   │ l/m/s/v signals · . pin       │
│     ◂ K8S_WORKLOAD cart              │                               │
│   ▸ calls (12)  SERVICE   +12 hidden │                               │
└──────────────────────────────────────┴───────────────────────────────┘
```

- **Root row** at the top is cursor position 0 — the selection contract
  (`selectionProvider`) means every global action (`.` pin, `o` open in
  browser, `y` yank id, `x` quick relations, drills) works on the root too.
- **Groups** = (direction, verb), collapsible, structure ranked before mesh
  (`edgeRank` reused), capped per group with `… +N hidden` (expand on demand).
- **Preview** follows the cursor (debounced ~250 ms, session-cached): name,
  id, health from the shared problem set, top `KeyFacts`.
- **`enter`/`→` re-roots** the walker on the highlighted neighbor and pushes
  the old root onto the **trail**. **`←`/`backspace` backtracks** the trail.
  Re-rooting is instant when the node is in the session cache.

### The trail vs the page stack — the key UX decision

Today, walking via `x` means: relations page → enter → detail page → `x` →
relations page → … The app stack becomes the walk history, which conflates
"where I am in the graph" with "how I got to this screen".

The navigator separates them:

- **Hops are trail entries** (internal to the walk view, breadcrumb-visible,
  `←` to backtrack). A 15-hop walk is *one* stack entry.
- **Levels are page pushes** (overview → browser → walk), so the app-wide
  `esc` = "back one page" convention is untouched: `esc` leaves the walk,
  `←` walks backward. No new `esc` semantics.

### Key vocabulary (walk mode)

Consistency rule: the navigator adopts the app's existing drill vocabulary
instead of vim-purist `h/l` (which would collide with `l` = logs).

| Key | Action |
|---|---|
| `↑/↓` `j/k` | move cursor (root, group headers, neighbors) |
| `enter` `→` | re-root on highlighted neighbor (push trail) |
| `←` `backspace` | backtrack one hop (pop trail) |
| `z` | collapse/expand group (space pages, as in every list); later: expand neighbor in place |
| `d` | full detail page for highlighted node (existing `detailMsg`) |
| `l` `m` `s` `v` | logs/metrics/traces/events scoped to highlighted node |
| `i` | cycle direction filter: both → outgoing → incoming |
| `M` | toggle mesh edges (`calls`, `routes_to`) — structure-only view |
| `/` | filter neighbors by name |
| `x` `.` `o` `y` `ctrl+q` | global: quick relations, pin scope, open browser, yank, reveal DQL |
| `esc` | leave navigator (normal page-back) |

### Entry points

- `:nav` → overview; `:nav <type>` → browser; `:nav <id|name>` → walk
  (name → resolution query → existing disambiguation picker on multi-match).
- **Global `X`** ("big x"): from any view with a selected entity, open the
  navigator rooted there — the capital sibling of `x`. Works from problems
  (roots at the first affected entity, same as `x` semantics today), from
  detail pages, from any entity table.
- CLI: `dtctl tui nav [<type|id|name>]`.
- `x` stays as-is: the quick, cheap one-hop peek. The navigator complements
  it; whether it eventually subsumes `relationsView` is an open question.

### Composition with scope — walking *is* scoping

`.` pins the highlighted node as the global scope, then `:logs`, `:traces`,
`:problems` are scoped to it — the navigator becomes the scope *picker* for
the whole TUI. `o` on any node opens the Smartscape intent link
(`view_topology_in_context`, `links.go:78`) for the true visual graph when
the terminal isn't enough. `ctrl+q` reveals the edges DQL — the navigator
doubles as a `smartscapeEdges` teaching tool, consistent with "DQL is the
substrate" (TUI_DESIGN.md).

---

## Data plan

All DQL, no new API surface. Every query below reuses the production
`dataSource.query` path; the two walk queries already exist in
`relations.go` and move to `catalog/smartscape.go` so both views share them.

**Census** (overview left, 1 query per refresh):

```dql
smartscapeNodes "*" | summarize count = count(), by:{type} | sort count desc
```

**Schema** (overview right, 1 query per refresh, cached for the session):

```dql
smartscapeEdges "*"
| fieldsAdd source_type, target_type   // lazy projections — must materialize (TUI_LEARNINGS §1.8)
| summarize count = count(), by:{source_type, type, target_type}
| sort count desc
```

> ✅ Validated live (box tenant, 2026-07-08): the summarize over the
> `fieldsAdd`-materialized lazy projections works; counts serialize as
> strings (handled by `catalog.IntValue`).

**Type instances** (browser):

```dql
smartscapeNodes "<TYPE>" | fields id, name, type | sort name asc | limit 500
```

**Ego edges + names** (walk hop — verbatim `relations.go:65,72` today):

```dql
smartscapeEdges "*"
| filter source_id == toSmartscapeId("<ID>") or target_id == toSmartscapeId("<ID>")
| fields source_id, source_type, type, target_id, target_type | limit 200

smartscapeNodes "*" | filter in(id, {toSmartscapeId("…"), …}) | fields id, name, type
```

**Preview facts** (debounced, cached): `catalog.DetailQuery` (`detail.go:25`),
rendered through `catalog.KeyFacts`.

**Health overlay** (1 query per refresh, shared across all levels): reuse
`catalog.ActiveProblems` (`problem.go:105`), then intersect client-side
against visible node ids — matching **both id eras**: Smartscape ids in
`smartscape.affected_entities[].id` *and* legacy ids in
`affected_entity_ids` (the dual-era hazard, TUI_LEARNINGS §1.3/§1.7). No
per-node problem queries, ever.

### Session cache

One `topoCache` per navigator instance: `nodes map[id]record`,
`neighbors map[id][]edge`, `problems map[id-era-normalized]bool`, LRU-bounded
(~500 nodes). Backtracking and re-visits are zero-query. Cleared on `ctrl+r`
and on timeframe change (`SetTimeframe` refetches; note Smartscape is
essentially "now" — the timeframe mainly affects the problem overlay).

### Cost budget

| Action | Queries |
|---|---|
| Open overview | 2 (+1 problems, shared) |
| Open type browser | 1 |
| Hop (uncached) | 2 |
| Hop (cached) / backtrack | 0 |
| Preview highlight (uncached) | 1, debounced |

Guardrails: 200-edge limit per ego query (existing), per-group render cap
with explicit `+N hidden` (no silent truncation), high-degree warning in the
group header when the edge limit was hit.

### Known data sharp edges (inherit, don't fight)

- **Nodeless edge endpoints** (`K8S_SECRET`, `K8S_CONFIGMAP` — edges but no
  node record, TUI_LEARNINGS §1.8): render dimmed with the raw id; hop
  allowed, preview says "no node record".
- **No relationship metadata API**: verbs are discovered empirically; unknown
  verbs land in a generic group and still work.
- **Management zones don't exist in Grail Smartscape** — out of scope; would
  require the classic Monitored Entities API v2, which this concept
  deliberately does not add (DQL-only keeps the TUI seam thin for the dtui
  split — see DTUI_SPLIT_DESIGN.md).

---

## Implementation shape

Follows the bespoke-top-level-view pattern (`home`/`query`), per the
extension model in TUI_LEARNINGS §2:

| File | Change |
|---|---|
| `pkg/tui/navigator.go` (new) | `navView` implementing `viewModel` + `busyReporter` + `selectionProvider` + `dqlProvider`. One type, three modes; each *level* is a separate pushed instance (overview → browser → walk), so `esc` pops levels naturally. Cursor model: flattened tree rows. Rendering models: `relations.go` (grouping), `waterfall.go` (tree), `home.go` (panels). |
| `pkg/tui/catalog/smartscape.go` (new) | Query builders as testable data: `CensusQuery`, `SchemaQuery`, `TypeInstancesQuery`, plus `EdgesQuery`/`NamesQuery` moved from `relations.go` (which becomes a consumer). |
| `pkg/tui/view.go` | `navMsg{mode, root Entity, arg string}` + helper. |
| `pkg/tui/app.go` | `viewFor` case, cmdbar special, `dispatch` case, global `X` key next to `x` (`app.go:417`). |
| `cmd/tui.go` | Arg validation + completion for `nav`. |
| `pkg/tui/history.go` | `pageRefOf`/`viewFromRef` for `{mode, type, root, trail}` — a restored walk keeps its trail. |

No changes to `datasource.go`, `theme/`, `pkg/output`, `pkg/`, or `sdk/` —
a pure-TUI feature that does not widen the dtui split seam.

**Tests:** catalog query builders as unit tests (convention); navigator model
tests on the `app_phase2_test.go` fixture pattern (edge fixtures already
exist there); live verification via the tmux workflow before claiming any
DQL works.

## Phasing

**Phase 1 — Walk mode MVP** ✅ shipped
Walk view: grouped ego list, trail, preview (identity + health + cached
facts), `enter` re-root, `←` backtrack, `d` detail, drills via selection,
global `X`, `:nav <id|TYPE>`, shared queries in `catalog/smartscape.go`,
history (trail survives restore).

**Phase 2 — Overview & health** ✅ shipped
Census + schema overview, type browser, session-wide active-problem overlay
(dual-era matching), debounced preview facts, collapsible groups, fan-out
caps with explicit `+N more`, `i`/`M` filters.

**Phase 3 — Lookahead & polish**
Expand-in-place (`z` on a neighbor shows *its* neighbors inline, depth
≤ 2 — peek two hops without moving), visited-nodes minimap, trail yank
(id list or reconstructed DQL), schema-cell drill (overview edge row →
the actual edge list).

## Risks & honest expectations

- **`smartscapeEdges "*"` scans per hop** — same cost `x` pays today; the
  cache amortizes it, but a pathological walk across 50 unvisited high-degree
  nodes is 100 queries. The 60 s fetch timeout and seq-based staleness
  (owner/seq convention) already handle slow tenants.
- **Preview churn** — cursor-follows-preview needs debounce discipline or it
  spams the query API; the (owner, seq) drop-stale pattern covers
  correctness, the debounce covers cost.
- **Alias collision** — `topo`/`census` currently belong to `entitiesSpec`.
  Navigator takes `nav`/`smartscape`/`navigator`; folding the census view
  into the overview is a later decision, not a Phase-1 breaking change.

## Open questions

1. ~~Does the navigator eventually subsume `relationsView`?~~ **Decided —
   yes:** `x` and `X` both open the walk; the standalone relations page is
   gone. One-hop quick-peek lives on as the detail page's embedded related
   tab (`relationsView` survives only for that and for restoring old history
   entries).
2. Should the overview replace `entitiesSpec` (`:topo`) once stable?
3. Expand-in-place vs preview-only: is 2-hop lookahead worth the render
   complexity, or does the cache make re-rooting cheap enough?
4. Timeframe semantics on topology: pass `from:` to `smartscapeNodes`/`Edges`
   (topology-at-time) or pin topology to "now" and scope only signals?

## References

- `pkg/tui/relations.go` — data seed (ego edges, names, edge ranking)
- `pkg/tui/waterfall.go` — tree rendering model
- `docs/dev/TUI_DESIGN.md` — Open Question 6 (topology view), scope system,
  "DQL is the substrate"
- `docs/dev/TUI_LEARNINGS.md` — §1.1/§1.2/§1.8 Smartscape DQL facts,
  §2 extension model, §3 message-flow patterns, §5 live verification
- `docs/dev/DTUI_SPLIT_DESIGN.md` — seam discipline this feature respects
- [ranger](https://github.com/ranger/ranger) — Miller-column walking idiom
- k9s `related` views — precedent for grouped one-hop lists in a TUI
