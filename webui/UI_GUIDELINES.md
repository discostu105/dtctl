# dtctl web — UI guidelines

The rules that keep the web UI consistent. [DESIGN.md](DESIGN.md) explains *why* the product
looks the way it does; this file explains *how* to build a screen so it feels like the rest.
When the code and this file disagree, fix one of them in the same change.

---

## 1. Page anatomy

Every destination is one of three shapes. Pick one; don't invent a fourth.

### List page (Problems, Services, Hosts, Kubernetes, Traces, Experience, AI, Changes, Security, Documents, Smartscape)

```
PageHeader   title · sub = "<what is shown> · <tfPhrase(...)>"   actions = lens Segmented / buttons
[Kpi row]    optional, only when the numbers answer the page's first question
Panel        head = <FacetSummary fc={fc} noun="services" />    actions = <FacetSearch fc={fc} />
  DataTable  rows={fc.rows} facets={fc} autoFocus onOpen → detail href
```

- Facets, not filter bars: declare `Facet<T>[]` next to the columns, call `useFacets(rows, FACETS)`,
  give filterable columns a `facet:` key. Filters live in the URL (`?f=key:value`, `?q=`); pages with
  several lists pass `{ param: 's' }` etc. so they don't collide.
- Lists of Smartscape nodes or services also take attribute filters: `useAttrs(source)` with an
  `AttrSource` from `lib/attrs.ts`, the list query wrapped in `withAttrs(query, attrs.filters)` (or
  `attrCondition` inside a timeseries `filter:{}`), and `useFacets(rows, FACETS, { attrs })`. These
  run on the server, reach every tag, label and field (`?a=field=value`, `-` excludes), and work past
  the row cap. Curated facets stay for the few dimensions people use daily; everything else is in
  the filter popup (`F`), which shows both kinds. Pass `fetching` and `limit` to `FacetSummary` so a
  capped list says so.
- Attribute filters that mean the same thing on another view (tags, primary tags, namespace,
  cluster) carry across tabs; view-specific ones are dropped.
- The primary list gets `autoFocus` so `j/k/Enter` work without a click (exception: pages whose
  primary control is a search box, e.g. AI conversations).
- `sub` names the data, then the timeframe. Use `tfPhrase(tf, requested)` so a widened or narrowed
  window says so ("last 24 hours (widened from last 2 hours)"). Leave the timeframe out for data
  that isn't time-bound (Documents, Smartscape census).

### Detail page (Problem, Entity, Trace, Session, AI conversation)

```
BackLink     fallback = the owning list ("← Services"), or "← <previous page title>" when there is history
DetailHeader icon · tone · title · badges · meta = [<IdCopy/>, facts…] · actions = <OpenInDynatrace/>
DataTabs     tabs with counts; auto-selects the first tab that has data until the user picks one
```

- Wrap not-found and error states in `DetailFallback` so the user always has a way back.
- `tone`: `crit` for an active problem or failed item, `accent` for a live object, `muted` for
  closed/historical.
- The ID is always first in the meta row, copyable via `IdCopy`. Never show a raw ID anywhere else
  when a name can be resolved (`lib/names.ts`).

### Tool page (Query, Logs)

Full-height editor or stream plus a result area. They may skip `Panel`, but still use `PageHeader`
(Logs is the known exception, see §11) and the shared table, inspector and side panel.

### Layout

- All pages are fluid full width with `p-5`. Never put `max-w-*` on a page container; constrain
  prose blocks only.
- Grids collapse to one column below `lg`. Test at 1280 px and at 2560 px.
- Side panels: `SidePanel size="default"` (`min(640px, 48vw)`) for row details, `size="wide"`
  (`min(720px, 52vw)`) for content-heavy panels (LLM call, tool call). Don't pass custom widths.
- Panels that show a chart or table are maximizable through `Panel`; don't hand-roll this.

---

## 2. Component catalog

Reach for these before writing markup. If you need a variant, extend the component.

| Need | Use | Where |
|---|---|---|
| Page title row | `PageHeader` | `components/Panel.tsx` |
| Detail title block | `DetailHeader`, `IdCopy`, `OpenInDynatrace` | `components/DetailHeader.tsx` |
| Back navigation | `BackLink`, `DetailFallback` | `components/BackLink.tsx` |
| Headline number | `Kpi` (value, sub, spark, tone, href/onClick, loading) | `components/Kpi.tsx` |
| Card with query provenance | `Panel` (title or head, actions, spec → "Open in Query →") | `components/Panel.tsx` |
| Table | `DataTable` (virtualized, keyboard, facets, empty state) | `components/DataTable.tsx` |
| Tabs | `DataTabs` (counts, data-aware) | `components/DataTabs.tsx` |
| Lens / mode switch | `Segmented` (with counts when known) | `components/ui.tsx` |
| Filtering | `useFacets`, `FacetSearch`, `FacetSummary` | `components/Facets.tsx` |
| Row details | `SidePanel`, `Inspector` (hides empty values) | `components/signals.tsx` |
| Key-value facts | `Facts` | `components/ui.tsx` |
| Time cell | `When` (relative + clock, absolute in tooltip); `TimeAgo` inline | `components/ui.tsx` |
| Chart | `TimeChart`, `StackedBars`, `Legend` | `components/Chart.tsx` |
| Spans, problems, logs | `SpanTable`, `ProblemsTable`, `LogStream`, `RiskBadge` | `components/signals.tsx` |
| States | `Skeleton`, `SkeletonRows`, `Empty`, `ErrorBox` | `components/ui.tsx` |
| Sampling / scan notices | `SampledBadge`, `ScanNotice` | `components/Sampled.tsx` |
| Tooltip | `Tip` | `components/ui.tsx` |

Pages never import from other pages for new code. Shared pieces go to `components/` (see the known
debt in §11 for the remaining exceptions).

---

## 3. Loading, empty and error states

Render in this order: **error → loading → empty → data**.

- **Error:** `ErrorBox` with the server message. Never swallow an error into an empty state: "no
  data" and "query failed" are different answers.
- **Loading:** `Skeleton` or `SkeletonRows` with the final layout's shape. No spinners in content
  areas; keep the previous data visible while refetching (`placeholderData`).
- **Empty:** `Empty` with domain wording that says *what* is missing and, if it helps, *why* or
  *what to do*: "No problems in the last 2 hours", "No Kubernetes workloads reported". Use the
  timeframe phrase only for time-bound data. When facets hide everything, `DataTable` already says
  "Nothing matches these filters" with a **Clear all** button.
- Counts in headers and tabs show `…` only while loading, never at 0.

---

## 4. Formatting

All numbers and times go through `lib/format.ts`. No `toFixed`, no `toLocaleString` in pages.

| Value | Function | Example |
|---|---|---|
| Count | `fmtInt` | 12,345 |
| Large count in a tight spot | `fmtCompact` | 12.3k |
| Percent (0–100) | `fmtPct` | 4.2% |
| Duration in ns / µs / ms / s | `fmtNs` / `fmtUs` / `fmtMs` / `fmtSec` | 182 ms |
| Bytes | `fmtBytes` | 3.4 GB |
| Value with a metric unit | `fmtUnit(unit)(v)` | |
| Tokens | `fmtTokens` (lib/ai) | 18.2k |
| Clock time | `fmtClock` | 14:03:22 |

Time placement:

- **List time column:** `<When value=…/>` with header **Started**, **Time**, **Last seen** or **Age**.
- **Event streams** (logs, timeline, waterfall ticks): `fmtTime`.
- **Facts on a detail page:** `fmtDateTime`.
- Every relative time shows the absolute time in a tooltip.

Type names go through `shortType` so they read naturally ("AWS EC2 instance", "DaemonSet",
"OTel process"). Add new acronyms to `TYPE_WORDS`.

---

## 5. Links and navigation

- Build in-app URLs only with `lib/links.ts` (`entityHref`, `problemHref`, `traceHref(id, t?, spanId?)`,
  `convHref`, `sessionHref`, `vulnHref`, `queryHref`). Pass the time hint `t` whenever you know
  when the thing happened, so the target page doesn't have to search wide windows.
- Every list row opens something (`onOpen`). A row that can't open anything shouldn't look clickable.
- Cross-links carry context: a change links to `/changes?f=workload:…`, a service to its traces with
  the service facet set.
- Link wording: **Open in Query →** for provenance, **Run it in Query →** for a tool's DQL,
  **Open in Dynatrace** (with the external-link icon) when leaving the app. The external-link icon
  means "leaves this app" and nothing else.
- Selections that should survive reload or be shareable go in the URL (filters, lens, tab, selected
  row). Use `replace` for ephemeral state so Back stays meaningful.

---

## 6. Terminology

Use these words exactly; don't introduce synonyms.

| Say | Not | Meaning |
|---|---|---|
| **Experience** | RUM, Real users | the section |
| **Frontend** | app, application (as a dimension) | a monitored frontend |
| **Traces** | Requests | the section |
| **Spans** | traces, requests (as rows) | rows in a trace list |
| **Failed** / **Failure rate** | Errors, error rate (for spans or requests) | a request or span ended in failure |
| **Errors** | Failures | log levels and JS errors only |
| **LLM calls**, **Tool calls**, **Cached** | completions, functions, cache hits | GenAI spans |
| **Started**, **Time**, **Last seen**, **Age** | Timestamp, Date, Created | time column headers |
| **Clear all** | Reset, Remove filters | clears every facet |
| **Filter <noun>…** | Search… | facet search placeholder |
| **newest spans** / **newest sessions** | — | `sub` when a list hit its record cap |

Sentence case everywhere ("Failure rate", not "Failure Rate"). Separate facts with ` · `.

---

## 7. Keyboard

- `g` is the global go-to prefix (`g p`, `g t`, …). Never bind `g`/`G` inside a component.
- Lists: `j`/`k` or arrows to move, `Home`/`End` for first/last, `Enter` to open.
- `/` filters the current list, `F` opens the filter popup (fields left, values right: `↵` picks,
  `⇧↵` excludes, `⌘↵`/`Space` picks and stays open, `key=value↵` picks in one go), `T` opens the timeframe, `R` refreshes, `?` toggles help, `⌘K` opens the palette.
- `M` maximizes or restores the detail panel; `⌘`-click opens a row in a new tab.
- `Esc` steps back one level per press: clear the focused filter, then blur it, then close the side panel.
- Trace view: `←`/`→` collapse or expand a span, `N`/`⇧N` next or previous search match, `E` next failed span,
  `C` critical path, `Z` zoom to span (`0` resets).
- Any new binding goes into the help overlay in `components/Shell.tsx` in the same change.

---

## 8. Color

- Only semantic tokens from `styles.css`: `ink-*`, `line`, `panel`, `sunken`, `raised`, `accent`,
  `ok`, `warn`, `crit` and their `-wash` variants, and the series colors `--s1…--s8`.
  No hex values or Tailwind palette colors (`red-500`) in components.
- **`--crit`** for failures and errors in charts and badges; **`--warn`** for degraded or "worth
  a look" (slow, error-level logs next to failed requests); **`--ok`** only where green carries
  meaning.
- GenAI uses `--genai-llm`, `--genai-tool`, `--genai-user` everywhere: conversation, waterfall,
  legend.
- Charts always have a `Legend` when they show more than one series; the legend wording matches the
  column and tab wording.
- Check every new screen in light and dark.

---

## 9. Data and scale

The UI must work on very large tenants. These rules aren't optional.

- **All DQL lives in `lib/dql.ts`** (or a domain lib such as `lib/ai.ts`), never inline in pages,
  so every query can be found, reviewed and shown via "Open in Query".
- **Heavy aggregates** (charts over spans, logs, events) use `useAdaptiveDql(table, spec, scaleFields)`.
  It probes the scan size, adds `samplingRatio`, scales counts and shows `SampledBadge`.
- **Record lists** over big tables use `useScanWindow(table, tf)`. It narrows the timeframe when the
  scan would exceed the budget, and `ScanNotice` explains this and offers a full scan.
- **ID lookups** (trace, span, session) never scan more than the page timeframe blindly: pass a time
  hint, or locate first over widening windows (`traceLocateQuery`: hint → page timeframe → 24h → 7d).
- **No hover prefetch** of pages whose queries scan big tables (`/ai`, `/rum`).
- **Cancellation:** pass the query's `signal` through `runDql`. Queries nobody waits for anymore are
  cancelled on the server; the Activity popover (top bar) shows running, queued and recent queries.
  When adding a page, open it and check Activity for redundant or slow queries.
- Set a sensible `ttl` on specs: long for metadata and census, short for "now" data.
- The UI is **read-only**. Anything that mutates must go through dtctl's safety checker and is out of
  scope for this PoC.

---

## 10. Privacy

- No customer names, employee names, usernames, emails or environment IDs in code, docs, comments,
  test data or commit messages. Use synthetic data and `@example.invalid` addresses.
- Screenshots from a real tenant are for local review only; never commit them.

---

## 11. Checklist for a new page or panel

- [ ] Uses one of the three page shapes, `PageHeader` or `BackLink` + `DetailHeader`.
- [ ] Lists use `useFacets` + `FacetSearch` + `FacetSummary`, columns have `facet:` keys, filters live in the URL.
- [ ] Lists of entities support attribute filters (`useAttrs` + `withAttrs`), and tags on detail pages link back to them (`listHref`).
- [ ] Primary list has `autoFocus`; every row opens something via `lib/links`.
- [ ] Error, loading and empty states, in that order, with domain wording.
- [ ] Numbers and times go through `lib/format`; time columns use `When`.
- [ ] Wording matches §6; colors are tokens only (§8).
- [ ] Queries are in `lib/dql.ts`; heavy ones use `useAdaptiveDql` / `useScanWindow`.
- [ ] Checked the Activity popover for redundant or slow queries.
- [ ] Filters compare the field as stored: `dt.smartscape.host == toSmartscapeId("HOST-…")`, `trace.id == toUid("…")` —
  never `toString(field) == "…"`, which skips Grail's indexes (5.5 s vs 0.8 s for metric discovery on a large tenant). `lib/dql-lint.test.ts` enforces it.
- [ ] A capped list (`| limit N`, `FacetSummary limit=`) shows the badge when it is hit: Grail cuts at 1000 records unless the
      query ends in its limit or the spec sets `maxRecords`; a companion query joined to the list (metrics by entity) covers every
      entity, not its own first 1000; and on big tenants the cap keeps the interesting rows (sort trouble-first, then by name).
- [ ] Pass the cap to `useFacets` (`limit:` or `capped:`), and give every curated facet that shows a raw attribute its `field:`:
      on a capped list the popup and cell filters then count and filter it on the server, not in the loaded slice.
- [ ] `make test-webui` passes (vitest for `lib/`, Playwright smoke over every route); a new route goes into `e2e/smoke.spec.ts`.
- [ ] New keys are in the help overlay.
- [ ] Looks right at 1280 px and 2560 px, light and dark; no console errors.
- [ ] Ran the privacy grep before committing.

---

## 12. Known debt

Inconsistencies we know about and accept for now. Remove an item when you fix it; don't add new ones.

- Lens and tab state is in the URL on some pages only; others keep it in React state.
- Pages import from other pages: `LlmCallPanel` lives in `pages/Ai.tsx`; Pulse imports from Rum;
  Entity imports from Problem and Rum. These should move to `components/`.
- Logs has its own toolbar layout without `PageHeader`.
- Rows on Experience → Pages do nothing on click (no page detail view yet).
- Some `Segmented` lenses have no counts.
- `FilterChip` exists in Ai and Rum as near-duplicates of the facet chips.
- The session stat tiles in Experience are hand-rolled instead of `Kpi`.
- The trace span detail (`AttrSections`) duplicates parts of `Inspector`.
- The selected row is in the URL only on Security and Trace.
