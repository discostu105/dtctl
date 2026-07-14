# Design ↔ Code Gaps

**Snapshot:** 2026-07-14 (after the Phase 3.12 closure pass) · **Author:** dtctl team

A systematic comparison of the design docs ([../design/tui.md](../design/tui.md),
[../design/smartscape-navigator.md](../design/smartscape-navigator.md)) against
the code, for future reference. [phases.md](phases.md) records what *shipped*;
this file records what did **not** — plus the places where the code moved past
the docs and the design text is the stale side. Each entry names the direction
the gap should close in.

> **Scope rule (decided 2026-07-14):** dynatui is **strictly read-only** —
> mutations (edit / delete / execute) are not planned at all, to limit the
> project's complexity. See [ADR-0011](../adr/0011-strictly-read-only.md).
> Gaps below never include mutating features.

## Features the design describes that the code does not have

Ordered roughly by value-for-effort.

1. **User-assignable hotkeys (`:hotkeys`)** — tui.md's movement table calls
   the digit bookmarks "user-assignable view bookmarks (`:hotkeys` to
   manage)". The code has a fixed map (`0` home … `9` aws, `app.go
   hotkeys`); no manager exists. *Close in code* if demand appears: persist a
   map in dynatui's state dir, `:hotkey <digit> <view>` to assign. Until then
   the doc overstates — the defaults it lists (1–5) also drifted from the
   shipped 0–9 set.
2. **Query escape hatch: live progress, cancellation, renderer cycling** —
   tui.md promises "live progress (scanned GB / records via the SDK's
   `PollUpdate`), cancellation, renderer cycling (table → sparkline →
   braille → bar)". The code has the editor + dynamic-column results table
   only; progress deliberately stays off (the reporter draws on stderr and
   would tear the alternate screen — `datasource.go`), and stale results are
   dropped by `(owner, seq)` instead of cancelled (learnings §3). *Close in
   code*: progress needs `PollUpdate` forwarded as `tea.Msg`s; renderer
   cycling needs the chart renderers wired to arbitrary result shapes.
   Query *history* shipped (3.12).
3. **Runtime discovery, two of five mechanisms** (tui.md §Runtime
   discovery): the **node-type census gating the command bar** ("no `:aws`
   in an Azure-only tenant, with counts shown in the alias popup") and the
   **field census** (`fieldsSnapshot logs/spans` driving inspector field
   ordering by prevalence and an optional column picker) are not built. The
   other three (edge catalog, metric catalog, semantic dictionary) shipped.
   *Close in code*: the census query already exists (`CensusQuery`, the
   navigator overview); feeding it into `updateCmdMatches` is the cheap
   half. The column picker is a real feature — design before building.
4. **`u` = SLOs targeting the entity** — tui.md's drill table assigns `u`
   to SLOs (service → "SLOs targeting it"); the code repurposed `u` for RUM
   sessions (phases 3.5) and no service→SLO path exists anywhere. The hard
   part is honest scoping: SLO definitions carry a DQL/selector, not entity
   ids, so "SLOs targeting this service" has no reliable join. *Close in
   doc* (drop the `u` row's SLO meaning) unless the mapping problem gets a
   real answer.
5. **Problem page "Related" tab** — tui.md's problem-detail table has a
   Related tab (non-affected `smartscape.related_entity.ids`, the wider
   blast radius). The shipped page (3.7) folded *affected* entities into the
   overview and never surfaced related ones. *Close in code*: small, but
   validate the field shapes live first (the affected-id arrays are
   era-split and often null — learnings §1.7; expect the same).
6. **Host detail: Disks / Network tabs** — designed in tui.md, explicitly
   "future tabs" in the shipped note. DISK / NETWORK_INTERFACE child
   entities and `dt.host.disk.*` / `dt.host.net.nic.*` metrics are
   confirmed on-tenant. *Close in code* via the existing containment-tab
   machinery when a host page needs them.
7. **Database detail: Statements / Tables / Callers tabs** — tui.md designs
   a DBA page; the code has the generic entity page plus canned Postgres
   connection metrics (`extras.go`, `specs.go`). Statement hotspots from
   spans (`db.query.text` summarize) are validated DQL (learnings §1.11) —
   the tab is buildable. *Close in code* when databases become a focus.
8. **Pod detail: Config tab** (ConfigMaps / Secrets / PVCs via `uses`
   edges) — not built. The targets are mostly **nodeless** entities
   (K8S_SECRET / K8S_CONFIGMAP have edges but no node records, learnings
   §1.8), so the tab would show raw ids; the related tab already lists the
   edges. *Probably close in doc* — low value until those nodes carry data.
9. **Logs live-follow toggle** — tui.md's signal table lists it. Open
   Question 4 chose re-query over tail-append, and the shipped `R`
   auto-refresh cycle *is* periodic re-query — but there is no
   logs-specific follow UX (jump-to-newest, follow indicator). *Close
   either way*: cheap version = a logs-view toggle that pins auto-refresh +
   cursor-to-newest.
10. **Home panels from the original design** — tui.md §Home lists
    unhealthy-SLO count, top services by error rate, recent deployment
    events, failing workflow executions. The shipped home (phases 3/3.5/
    3.11) converged on: problems, failing services, K8s warnings, vulns,
    frontend errors, attack detections. The **recent-changes panel** (the
    3.7 change-event query exists) is the one piece still worth building;
    workflow executions land with Phase 4 browsing. *Mixed*: doc partly
    superseded, one panel still open.
11. **`:costs` / `:dps`, mobile apps, Azure/GCP inventories** — designed,
    never built; phases 3/3.5 record them as open for lack of data on the
    exploration tenants. *Blocked on data*: needs a tenant that has any.
12. **Mouse support** — tui.md's movement table says "shift-j/k **or click
    header**" for sorting; no mouse handling exists anywhere. *Close in
    doc* unless mouse support becomes a goal.
13. **Navigator Phase 3 (lookahead & polish)** — expand-in-place (`z` on a
    neighbor, depth ≤ 2), visited-nodes minimap, trail yank, schema-cell
    drill. Tracked as open in smartscape-navigator.md §Phasing.
14. **Phase 4: read-only asset browsing** — the management views of tui.md
    (`:workflows` + executions with live log follow, `:dashboards`,
    `:notebooks`, `:documents`, `:settings`, `:extensions`, `:edgeconnects`,
    `:users`, `:groups`, …) exist only as `:slos` and `:detectors`
    (`Spec.API`, phases 3.5). The rest is the remaining roadmap item — now
    scoped to list / describe / open only (ADR-0011).

## Where the code moved past the docs (the doc is the stale side)

Recorded so nobody "fixes" the code back toward stale text. phases.md is
defined as the newest word; these are the concrete spots in tui.md:

- **`u` drill** means RUM sessions / session timeline, not SLOs (3.5) — see
  gap 4.
- **Digit hotkeys**: fixed 0–9 set, and digits follow the numbered strip on
  entered pages (3.9/3.10); the movement-table row still describes
  user-assignable 1–5 defaults.
- **History path** is `~/.local/state/dynatui/history.json` (dynatui's own
  namespace, with one-time migration from dtctl's `tui-history.json`) — the
  movement table names the old dtctl path.
- **Problem page tabs**: affected entities are overview rows, not an
  "Impact" tab (3.7).
- **Traces default lens**: scoped drills open on `all` (GenAI on `genai`),
  only unscoped `:traces` keeps `roots` — tui.md documents this correctly
  in "Findings" §4 but the catalog table's lens list can read as if roots
  were always first.
- **Navigator keys**: mesh toggle is `M`, `g`/`G` stay cursor home/end
  (deviations note at the top of smartscape-navigator.md).

## Closed 2026-07-14 (details in phases.md §3.12)

`:nav <name>` resolution + `dynatui nav <type|id|name>` · `:ctx <name>`
in-session context switch · custom timeframe entry in the `t` picker ·
query history (`ctrl+p`/`ctrl+n`, persisted).
