import { useVirtualizer } from '@tanstack/react-virtual'
import { BackLink } from '../components/BackLink'
import { DetailHeader, IdCopy, OpenInDynatrace } from '../components/DetailHeader'
import clsx from 'clsx'
import {
  AlertTriangle,
  Bot,
  ChevronDown,
  ChevronRight,
  ChevronsDownUp,
  ChevronsUpDown,
  Route,
  Search,
  Sparkles,
  Waypoints,
  Wrench,
  X,
  ZoomIn,
  ZoomOut,
} from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Link, useLocation, useSearch } from 'wouter'
import { cssVar, useThemeVersion } from '../components/Chart'
import { DataTable, type Column } from '../components/DataTable'
import { DataTabs } from '../components/DataTabs'
import { EntityLink } from '../components/Entity'
import { Inspector, LogStream, SidePanel, Value } from '../components/signals'
import { Badge, CopyButton, Empty, ErrorBox, Kbd, Skeleton, TimeAgo, Tip } from '../components/ui'
import { fmtTokens } from '../lib/ai'
import { num, useDql, useMeta, type Rec } from '../lib/api'
import { q } from '../lib/dql'
import { fmtCompact, fmtInt, fmtMs, fmtPct } from '../lib/format'
import { dtLinks, convHref, traceHref } from '../lib/links'
import { hitScanLimit } from '../lib/sampling'
import { pushRecent, useTitle } from '../lib/store'
import { useTimeframe } from '../lib/timeframe'
import {
  TRACE_LIMIT,
  buildTrace,
  defaultCollapsed,
  operations,
  revealed,
  serviceColor,
  serviceSlot,
  locateWindows,
  spanDetailQuery,
  spanLogsQuery,
  tickStep,
  traceLocateQuery,
  traceSkeletonQuery,
  traceWindow,
  visibleRows,
  type OpStat,
  type TNode,
  type TraceModel,
} from '../lib/trace'
import { LlmCallPanel } from './Ai'

type View = 'waterfall' | 'flame' | 'summary' | 'logs'
type Win = [number, number]

const ROW_H = 26

/**
 * Trace view, modelled on what the good tracing tools converged on (Jaeger,
 * Tempo, Honeycomb, Datadog): a minimap to zoom the time window, a collapsible
 * waterfall coloured by service with durations on the bars, the critical path,
 * in-trace search, self time, a flame graph, and a summary that folds repeated
 * operations together. The full trace skeleton loads at once (10k+ spans);
 * a span's attributes and logs load when it is opened.
 */
export function Trace({ id }: { id: string }) {
  const { data: meta } = useMeta()
  const search = new URLSearchParams(useSearch())
  const [, navigate] = useLocation()
  // locate first (cheap, progressively wider windows), then load from the trace's exact window
  const tfNow = useTimeframe()
  const [windows] = useState(() => locateWindows(search.get('t'), tfNow))
  const [wi, setWi] = useState(0)
  const loc = useDql({ query: traceLocateQuery(id), from: windows[wi].from, to: windows[wi].to, ttl: 300 })
  const hit = loc.data?.records[0]
  const found = hit && num(hit.n) > 0 ? hit : null
  const [limitedAt, setLimitedAt] = useState<string[]>([])
  useEffect(() => {
    if (!loc.data || found) return
    if (hitScanLimit(loc.data)) setLimitedAt((l) => [...l, windows[wi].label])
    if (wi < windows.length - 1) setWi(wi + 1)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loc.data])
  const tw = found ? traceWindow(found.s, found.e) : null
  const spec = tw ? { query: traceSkeletonQuery(id), from: tw.from, to: tw.to, ttl: 300, maxRecords: TRACE_LIMIT } : null
  const res = useDql(spec)
  const locating = !found && !loc.error && !(loc.data && wi === windows.length - 1)
  const m = useMemo(() => buildTrace(res.data?.records ?? []), [res.data])
  const truncated = (res.data?.records.length ?? 0) >= TRACE_LIMIT

  const view = (search.get('view') as View) || 'waterfall'
  const setView = (v: View) => setParam('view', v === 'waterfall' ? null : v)
  const setParam = useCallback(
    (k: string, v: string | null) => {
      const p = new URLSearchParams(window.location.search)
      if (v == null) p.delete(k)
      else p.set(k, v)
      navigate(`${window.location.pathname}?${p}`, { replace: true })
    },
    [navigate],
  )

  // selection lives in the URL (?span=…) so a span is linkable
  const spanParam = search.get('span')
  const selIdx = useMemo(() => (spanParam ? m.nodes.findIndex((n) => n.id === spanParam) : -1), [m, spanParam])
  const sel = selIdx >= 0 ? m.nodes[selIdx] : null
  const select = useCallback((i: number | null) => setParam('span', i == null ? null : m.nodes[i].id), [m, setParam])

  const [collapsed, setCollapsed] = useState<Set<number>>(new Set())
  useEffect(() => {
    const c = defaultCollapsed(m)
    setCollapsed(selIdx >= 0 ? revealed(m, c, selIdx) : c)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [m])
  const [win, setWin] = useState<Win | null>(null)
  const w: Win = win ?? [m.t0, m.t1]
  const [crit, setCrit] = useState(false)
  const [query, setQuery] = useState('')
  const [matchPos, setMatchPos] = useState(0)

  const rows = useMemo(() => visibleRows(m, collapsed), [m, collapsed])
  const matches = useMemo(() => {
    const ql = query.trim().toLowerCase()
    if (!ql) return null
    const out: number[] = []
    for (const n of m.nodes) if (n.name.toLowerCase().includes(ql) || n.service.toLowerCase().includes(ql) || n.id === ql) out.push(n.i)
    return out
  }, [m, query])
  const matchSet = useMemo(() => (matches ? new Set(matches) : null), [matches])
  const errors = useMemo(() => m.nodes.filter((n) => n.failed).map((n) => n.i), [m])

  const focus = useCallback(
    (i: number) => {
      setCollapsed((c) => revealed(m, c, i))
      select(i)
    },
    [m, select],
  )
  const step = (list: number[] | null, dir: 1 | -1) => {
    if (!list?.length) return
    const cur = selIdx
    const next = dir === 1 ? (list.find((i) => i > cur) ?? list[0]) : ([...list].reverse().find((i) => i < cur) ?? list[list.length - 1])
    setMatchPos(list.indexOf(next))
    focus(next)
  }
  const zoomTo = (n: TNode) => {
    const pad = Math.max(n.dur * 0.04, 0.05)
    setWin([n.start - pad, n.end + pad])
  }

  const root = m.nodes[m.roots[0]]
  const title = root ? String(root.rec['endpoint.name'] ?? root.name) : 'Trace'
  useTitle(root ? `Trace · ${title}` : 'Trace')
  useEffect(() => {
    if (root) pushRecent({ href: traceHref(id, root.rec.start_time), label: title, kind: 'Trace' })
  }, [root, id, title])

  const ai = useMemo(() => {
    const spans = m.nodes.map((n) => n.rec)
    const llm = spans.filter((s) => s['gen_ai.operation.name'] === 'chat' && (s['gen_ai.request.model'] || s['gen_ai.usage.input_tokens'] != null))
    const tools = spans.filter((s) => s['gen_ai.operation.name'] === 'execute_tool')
    const convs = [...new Set(spans.map((s) => s['gen_ai.conversation.id']).filter(Boolean))] as string[]
    const tokens = llm.reduce((a, s) => a + num(s['gen_ai.usage.input_tokens']) + num(s['gen_ai.usage.output_tokens']), 0)
    return { llm: llm.length, tools: tools.length, convs, tokens }
  }, [m])
  const serviceIds = useMemo(() => {
    const mm = new Map<string, string>()
    for (const n of m.nodes) if (n.rec['dt.smartscape.service']) mm.set(n.service, n.rec['dt.smartscape.service'])
    return mm
  }, [m])

  const logsSpec = tw ? { query: `fetch logs\n| filter trace_id == ${q(id)}\n| sort timestamp asc\n| limit 500`, from: tw.from, to: tw.to, ttl: 120 } : null
  const logs = useDql(logsSpec)
  const total = m.t1 - m.t0

  return (
    <div className="flex h-full">
      <div className="flex min-w-0 flex-1 flex-col p-5">
        <BackLink fallback="/traces" label="Traces" />
        {res.error || loc.error ? (
          <ErrorBox error={res.error ?? loc.error} />
        ) : locating || res.isLoading ? (
          <>
            <div className="mb-2 flex items-center gap-2 text-xs text-ink-3">
              <span className="size-1.5 animate-pulse rounded-full bg-accent" />
              {locating ? `Locating trace · ${windows[wi].label}…` : `Loading ${found ? fmtInt(num(found.n)) : ''} spans…`}
            </div>
            <Skeleton className="mb-3 h-7 w-96" />
            <Skeleton className="h-96" />
          </>
        ) : !m.nodes.length ? (
          <Empty
            title="Trace not found"
            hint={
              limitedAt.length
                ? `Searched ${windows.map((w) => w.label).join(', ')}. The ${limitedAt[limitedAt.length - 1]} search stopped at Grail's scan limit before it found this trace. Open it from a list, log or span (those links carry its time), or set the timeframe to when it ran.`
                : `No spans with this trace ID in ${windows.map((w) => w.label).join(', ')}.`
            }
          />
        ) : (
          <>
            <DetailHeader
              icon={<Waypoints />}
              tone={m.failed ? 'crit' : 'accent'}
              title={<span title={title}>{title}</span>}
              meta={
                <>
                  <IdCopy id={id} label="trace ID" />
                  <span className="tnum font-medium text-ink">{fmtMs(total)}</span>
                  <span>
                    {fmtInt(m.nodes.length)} spans{truncated && <span className="text-warn"> (first {fmtInt(TRACE_LIMIT)})</span>}
                  </span>
                  <span>{m.services.length === 1 ? '1 service' : `${fmtInt(m.services.length)} services`}</span>
                  <span>depth {m.maxDepth + 1}</span>
                  {m.failed > 0 && (
                    <button type="button" onClick={() => step(errors, 1)} className="inline-flex items-center gap-1 text-crit hover:underline">
                      <AlertTriangle className="size-3.5" /> {fmtInt(m.failed)} failed
                    </button>
                  )}
                  <TimeAgo value={root.rec.start_time} />
                  {ai.llm + ai.tools > 0 && (
                    <span className="inline-flex items-center gap-2 rounded-md bg-[var(--genai-llm)]/10 px-2 py-0.5 text-xs text-[var(--genai-llm)]">
                      <Sparkles className="size-3" /> {fmtInt(ai.llm)} LLM calls · <Wrench className="size-3" /> {fmtInt(ai.tools)} tool calls · {fmtTokens(ai.tokens)} tokens
                      {ai.convs.slice(0, 2).map((c) => (
                        <Link key={c} href={convHref(c, root.rec.start_time)} className="font-medium underline-offset-2 hover:underline">
                          replay conversation →
                        </Link>
                      ))}
                    </span>
                  )}
                </>
              }
              actions={<OpenInDynatrace href={meta?.environment && dtLinks.trace(meta.environment, id)} />}
            />
            <ServiceBreakdown m={m} ids={serviceIds} onPick={(s) => setQuery(s)} />

            {/* ── main ── */}
            <div className="flex min-h-0 flex-1 flex-col rounded-xl border border-line bg-panel">
              <DataTabs
                value={view}
                onChange={setView}
                tabs={[
                  { value: 'waterfall', label: 'Waterfall', spec, result: res, count: m.nodes.length, limit: TRACE_LIMIT },
                  { value: 'flame', label: 'Flame graph', count: null },
                  { value: 'summary', label: 'Summary', count: null },
                  { value: 'logs', label: 'Logs', spec: logsSpec, result: logs, limit: 500 },
                ]}
              />
              {(view === 'waterfall' || view === 'flame') && (
                <>
                  <Toolbar
                    query={query}
                    setQuery={(s) => {
                      setQuery(s)
                      setMatchPos(0)
                    }}
                    matches={matches}
                    matchPos={matchPos}
                    onStep={(d) => step(matches, d)}
                    errors={errors.length}
                    onError={() => step(errors, 1)}
                    crit={crit}
                    setCrit={setCrit}
                    zoomed={!!win}
                    win={w}
                    onResetZoom={() => setWin(null)}
                    onZoomSel={sel ? () => zoomTo(sel) : undefined}
                    onExpandAll={view === 'waterfall' ? () => setCollapsed(new Set()) : undefined}
                    onCollapseAll={view === 'waterfall' ? () => setCollapsed(defaultCollapsed(m, true)) : undefined}
                  />
                  <Minimap m={m} win={w} setWin={setWin} />
                </>
              )}
              {view === 'waterfall' && (
                <Waterfall
                  m={m}
                  rows={rows}
                  sel={selIdx}
                  onSelect={select}
                  collapsed={collapsed}
                  setCollapsed={setCollapsed}
                  win={w}
                  onZoom={zoomTo}
                  matchSet={matchSet}
                  query={query}
                  crit={crit}
                  keys={{
                    nextMatch: () => step(matches, 1),
                    prevMatch: () => step(matches, -1),
                    nextError: () => step(errors, 1),
                    toggleCrit: () => setCrit((c) => !c),
                    resetZoom: () => setWin(null),
                  }}
                />
              )}
              {view === 'flame' && <FlameGraph m={m} win={w} sel={selIdx} onSelect={select} onZoom={zoomTo} matchSet={matchSet} crit={crit} />}
              {view === 'summary' && (
                <Summary
                  m={m}
                  onPick={(op) => {
                    setQuery(op.op)
                    setMatchPos(0)
                    setView('waterfall')
                    focus(op.first)
                  }}
                />
              )}
              {view === 'logs' &&
                (logs.error ? <ErrorBox error={logs.error} /> : <LogStream records={logs.data?.records} loading={logs.isLoading} className="min-h-0 flex-1" />)}
            </div>
          </>
        )}
      </div>
      {sel && sel.rec['gen_ai.operation.name'] === 'chat' ? (
        <LlmCallPanel traceId={id} spanId={sel.id} at={sel.rec.start_time} onClose={() => select(null)} />
      ) : (
        sel && tw && <SpanPanel m={m} n={sel} traceId={id} win={tw} onClose={() => select(null)} onSelect={focus} onZoom={() => zoomTo(sel)} />
      )}
    </div>
  )
}

// ── header pieces ───────────────────────────────────────────────────────────

/** Where the time went, by service (self time), as one stacked bar. */
function ServiceBreakdown({ m, ids, onPick }: { m: TraceModel; ids: Map<string, string>; onPick: (s: string) => void }) {
  const total = m.services.reduce((a, s) => a + s.self, 0) || 1
  return (
    <div className="mb-3">
      {m.services.length > 1 && (
        <div className="mb-1.5 flex h-1.5 overflow-hidden rounded-full bg-line">
          {m.services.map((s) => (
            <span key={s.name} style={{ width: `${(100 * s.self) / total}%`, background: s.color }} title={`${s.name}: ${fmtMs(s.self)} self time`} />
          ))}
        </div>
      )}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
        {m.services.slice(0, 10).map((s) => (
          <span key={s.name} className="inline-flex items-center gap-1.5 text-ink-2">
            <i className="inline-block size-2 rounded-[2px]" style={{ background: s.color }} />
            {ids.get(s.name) ? <EntityLink id={ids.get(s.name)!} name={s.name} type="SERVICE" /> : s.name}
            <button type="button" onClick={() => onPick(s.name)} className="tnum text-ink-3 hover:text-accent-ink" title="Highlight this service's spans">
              {m.services.length > 1 ? `${fmtPct((100 * s.self) / total, 0)} · ` : ''}
              {fmtCompact(s.spans)} spans
            </button>
            {s.errors > 0 && <span className="text-crit">· {s.errors} failed</span>}
          </span>
        ))}
      </div>
    </div>
  )
}

function Toolbar(p: {
  query: string
  setQuery: (s: string) => void
  matches: number[] | null
  matchPos: number
  onStep: (d: 1 | -1) => void
  errors: number
  onError: () => void
  crit: boolean
  setCrit: (b: boolean) => void
  zoomed: boolean
  win: Win
  onResetZoom: () => void
  onZoomSel?: () => void
  onExpandAll?: () => void
  onCollapseAll?: () => void
}) {
  const btn = 'inline-flex h-7 items-center gap-1.5 rounded-md px-2 text-xs text-ink-2 hover:bg-line hover:text-ink disabled:opacity-40'
  return (
    <div className="flex flex-wrap items-center gap-1.5 border-b border-line px-3 py-1.5">
      <div className="relative w-72">
        <Search className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-ink-4" />
        <input
          data-filter
          value={p.query}
          onChange={(e) => p.setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              p.onStep(e.shiftKey ? -1 : 1)
            } else if (e.key === 'Escape') {
              p.setQuery('')
              ;(e.target as HTMLInputElement).blur()
            }
          }}
          placeholder="Find spans by name or service…"
          spellCheck={false}
          className="h-7 w-full rounded-md border border-line bg-sunken pr-16 pl-7 text-xs outline-none placeholder:text-ink-4 focus:border-accent/60"
        />
        {p.matches && (
          <span className="tnum absolute top-1/2 right-1 flex -translate-y-1/2 items-center gap-0.5 text-2xs text-ink-3">
            {p.matches.length ? `${p.matchPos + 1}/${p.matches.length}` : '0'}
            <button type="button" onClick={() => p.setQuery('')} className="grid size-5 place-items-center rounded hover:bg-line" aria-label="Clear search">
              <X className="size-3" />
            </button>
          </span>
        )}
      </div>
      {p.matches && p.matches.length > 0 && (
        <>
          <button type="button" className={btn} onClick={() => p.onStep(-1)} title="Previous match (⇧↵, N)">
            ↑
          </button>
          <button type="button" className={btn} onClick={() => p.onStep(1)} title="Next match (↵, n)">
            ↓
          </button>
        </>
      )}
      <span className="mx-1 h-4 w-px bg-line" />
      {p.errors > 0 && (
        <button type="button" className={clsx(btn, 'text-crit hover:text-crit')} onClick={p.onError} title="Jump to the next failed span (e)">
          <AlertTriangle className="size-3.5" /> Next error
        </button>
      )}
      <button type="button" className={clsx(btn, p.crit && 'bg-accent-wash text-accent-ink')} onClick={() => p.setCrit(!p.crit)} title="Highlight the critical path (c)">
        <Route className="size-3.5" /> Critical path
      </button>
      {p.onExpandAll && (
        <>
          <button type="button" className={btn} onClick={p.onExpandAll} title="Expand all">
            <ChevronsUpDown className="size-3.5" />
          </button>
          <button type="button" className={btn} onClick={p.onCollapseAll} title="Collapse to top level">
            <ChevronsDownUp className="size-3.5" />
          </button>
        </>
      )}
      <span className="ml-auto flex items-center gap-1">
        {p.onZoomSel && (
          <button type="button" className={btn} onClick={p.onZoomSel} title="Zoom to the selected span (z)">
            <ZoomIn className="size-3.5" /> Zoom to span
          </button>
        )}
        {p.zoomed && (
          <button type="button" className={clsx(btn, 'text-accent-ink')} onClick={p.onResetZoom} title="Show the whole trace (0)">
            <ZoomOut className="size-3.5" /> {fmtMs(p.win[1] - p.win[0])} window · reset
          </button>
        )}
      </span>
    </div>
  )
}

// ── minimap ─────────────────────────────────────────────────────────────────

/** The whole trace in 52px; drag to zoom the views below, double-click to reset. */
function Minimap({ m, win, setWin }: { m: TraceModel; win: Win; setWin: (w: Win | null) => void }) {
  const wrap = useRef<HTMLDivElement>(null)
  const canvas = useRef<HTMLCanvasElement>(null)
  const [width, setWidth] = useState(0)
  const [drag, setDrag] = useState<[number, number] | null>(null)
  const theme = useThemeVersion()
  const H = 52
  useEffect(() => {
    const ro = new ResizeObserver(([e]) => setWidth(Math.floor(e.contentRect.width)))
    if (wrap.current) ro.observe(wrap.current)
    return () => ro.disconnect()
  }, [])
  useEffect(() => {
    const c = canvas.current
    if (!c || !width) return
    const dpr = window.devicePixelRatio || 1
    c.width = width * dpr
    c.height = H * dpr
    const ctx = c.getContext('2d')!
    ctx.scale(dpr, dpr)
    ctx.clearRect(0, 0, width, H)
    const span = m.t1 - m.t0 || 1
    const n = m.nodes.length
    const rh = Math.max(1, H / Math.max(n, 1))
    const colors = new Map<number, string>()
    const crit = cssVar('--crit')
    for (const node of m.nodes) {
      let col = colors.get(node.svc)
      if (!col) {
        col = cssVar(serviceSlot(node.svc))
        colors.set(node.svc, col)
      }
      ctx.fillStyle = node.failed ? crit : col
      const x = ((node.start - m.t0) / span) * width
      const wd = Math.max(1, (node.dur / span) * width)
      ctx.fillRect(x, (node.i / n) * H, wd, rh)
    }
  }, [m, width, theme])

  const span = m.t1 - m.t0 || 1
  const toT = (x: number) => m.t0 + (Math.min(Math.max(x, 0), width) / width) * span
  const toX = (t: number) => ((t - m.t0) / span) * width
  const local = (e: React.MouseEvent) => e.clientX - (wrap.current?.getBoundingClientRect().left ?? 0)
  const zoomed = win[0] > m.t0 + 1e-6 || win[1] < m.t1 - 1e-6
  const sel = drag ? [Math.min(...drag), Math.max(...drag)] : null

  return (
    <div className="border-b border-line px-3 pt-2 pb-1">
      <div
        ref={wrap}
        className="relative cursor-crosshair select-none"
        style={{ height: H }}
        onMouseDown={(e) => {
          const x = local(e)
          setDrag([x, x])
          const move = (ev: MouseEvent) => setDrag((d) => [d ? d[0] : x, ev.clientX - (wrap.current?.getBoundingClientRect().left ?? 0)])
          const up = (ev: MouseEvent) => {
            window.removeEventListener('mousemove', move)
            window.removeEventListener('mouseup', up)
            const b = ev.clientX - (wrap.current?.getBoundingClientRect().left ?? 0)
            setDrag(null)
            if (Math.abs(b - x) > 4) setWin([toT(Math.min(x, b)), toT(Math.max(x, b))])
          }
          window.addEventListener('mousemove', move)
          window.addEventListener('mouseup', up)
        }}
        onDoubleClick={() => setWin(null)}
        title="Drag to zoom · double-click to reset"
      >
        <canvas ref={canvas} style={{ width: '100%', height: H }} className="rounded-sm opacity-90" />
        {zoomed && !sel && (
          <>
            <div className="absolute inset-y-0 left-0 bg-panel/75" style={{ width: toX(win[0]) }} />
            <div className="absolute inset-y-0 right-0 bg-panel/75" style={{ left: toX(win[1]) }} />
            <div className="absolute inset-y-0 border-x-2 border-accent" style={{ left: toX(win[0]), width: Math.max(2, toX(win[1]) - toX(win[0])) }} />
          </>
        )}
        {sel && <div className="absolute inset-y-0 border-x border-accent bg-accent/15" style={{ left: sel[0], width: sel[1] - sel[0] }} />}
      </div>
      <Ticks from={0} to={span} className="mt-0.5" />
    </div>
  )
}

/** Offset labels under a time range (relative to the trace start). */
function Ticks({ from, to, className }: { from: number; to: number; className?: string }) {
  const stepMs = tickStep(to - from, 6)
  const first = Math.ceil(from / stepMs) * stepMs
  const out: number[] = []
  for (let t = first; t <= to + 1e-9 && out.length < 20; t += stepMs) out.push(t)
  return (
    <div className={clsx('relative h-3.5 text-2xs text-ink-4', className)}>
      {out.map((t) => {
        const pct = ((t - from) / (to - from || 1)) * 100
        return (
          <span key={t} className="tnum absolute -translate-x-1/2 whitespace-nowrap" style={{ left: `${pct}%`, transform: pct < 3 ? 'none' : pct > 97 ? 'translateX(-100%)' : undefined }}>
            {t === 0 ? '0' : fmtMs(t)}
          </span>
        )
      })}
    </div>
  )
}

// ── waterfall ───────────────────────────────────────────────────────────────

const NAME_W_KEY = 'dtctl-web:trace-name-w'

function Waterfall({
  m,
  rows,
  sel,
  onSelect,
  collapsed,
  setCollapsed,
  win,
  onZoom,
  matchSet,
  query,
  crit,
  keys,
}: {
  m: TraceModel
  rows: number[]
  sel: number
  onSelect: (i: number | null) => void
  collapsed: Set<number>
  setCollapsed: (f: (c: Set<number>) => Set<number>) => void
  win: Win
  onZoom: (n: TNode) => void
  matchSet: Set<number> | null
  query: string
  crit: boolean
  keys: { nextMatch: () => void; prevMatch: () => void; nextError: () => void; toggleCrit: () => void; resetZoom: () => void }
}) {
  const scroller = useRef<HTMLDivElement>(null)
  // the waterfall is the page's list: keys work without clicking it first
  useEffect(() => scroller.current?.focus({ preventScroll: true }), [])
  const [nameW, setNameW] = useState(() => {
    try {
      return Number(localStorage.getItem(NAME_W_KEY)) || 420
    } catch {
      return 420
    }
  })
  const v = useVirtualizer({ count: rows.length, getScrollElement: () => scroller.current, estimateSize: () => ROW_H, overscan: 20 })
  const rowOf = useMemo(() => {
    const mm = new Map<number, number>()
    rows.forEach((r, i) => mm.set(r, i))
    return mm
  }, [rows])

  useEffect(() => {
    const r = rowOf.get(sel)
    if (r != null) v.scrollToIndex(r, { align: 'auto' })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sel])

  const toggle = (i: number) =>
    setCollapsed((c) => {
      const n = new Set(c)
      if (n.has(i)) n.delete(i)
      else n.add(i)
      return n
    })

  const [w0, w1] = win
  const ws = w1 - w0 || 1
  const pct = (t: number) => ((t - w0) / ws) * 100
  const stepMs = tickStep(ws, 8)
  const grid: number[] = []
  for (let t = Math.ceil((w0 - m.t0) / stepMs) * stepMs; t <= w1 - m.t0 && grid.length < 30; t += stepMs) grid.push(t)

  const onKey = (e: React.KeyboardEvent) => {
    if (e.metaKey || e.ctrlKey || e.altKey) return
    const cur = rowOf.get(sel) ?? -1
    const n = sel >= 0 ? m.nodes[sel] : null
    const go = (r: number) => {
      e.preventDefault()
      const i = rows[Math.max(0, Math.min(rows.length - 1, r))]
      if (i != null) onSelect(i)
    }
    if (e.key === 'j' || e.key === 'ArrowDown') go(cur + 1)
    else if (e.key === 'k' || e.key === 'ArrowUp') go(cur - 1)
    else if (e.key === 'ArrowRight' && n) {
      e.preventDefault()
      if (collapsed.has(n.i)) toggle(n.i)
      else if (n.children.length) onSelect(n.children[0])
    } else if (e.key === 'ArrowLeft' && n) {
      e.preventDefault()
      if (n.children.length && !collapsed.has(n.i)) toggle(n.i)
      else if (n.parent >= 0) onSelect(n.parent)
    } else if (e.key === 'n') keys.nextMatch()
    else if (e.key === 'N') keys.prevMatch()
    else if (e.key === 'e') keys.nextError()
    else if (e.key === 'c') keys.toggleCrit()
    else if (e.key === 'z' && n) onZoom(n)
    else if (e.key === '0') keys.resetZoom()
    else if (e.key === 'Home') go(0)
    else if (e.key === 'End') go(rows.length - 1)
  }

  const ql = query.trim().toLowerCase()

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* ruler */}
      <div className="relative flex h-7 shrink-0 items-center border-b border-line text-2xs text-ink-3">
        <div className="flex shrink-0 items-center justify-between px-3 font-medium tracking-wide uppercase" style={{ width: nameW }}>
          <span>Span</span>
          <span className="tnum normal-case">{fmtInt(rows.length)} shown</span>
        </div>
        <div
          className="absolute inset-y-0 z-[2] w-2 -translate-x-1 cursor-col-resize hover:bg-accent/30"
          style={{ left: nameW }}
          onMouseDown={(e) => {
            e.preventDefault()
            const startX = e.clientX
            const start = nameW
            const move = (ev: MouseEvent) => setNameW(Math.max(220, Math.min(start + ev.clientX - startX, (scroller.current?.clientWidth ?? 1200) - 200)))
            const up = () => {
              window.removeEventListener('mousemove', move)
              window.removeEventListener('mouseup', up)
              setNameW((w) => {
                try {
                  localStorage.setItem(NAME_W_KEY, String(w))
                } catch {
                  /* ignore */
                }
                return w
              })
            }
            window.addEventListener('mousemove', move)
            window.addEventListener('mouseup', up)
          }}
          title="Drag to resize"
        />
        <div className="relative h-full flex-1 border-l border-line">
          {grid.map((t) => (
            <span key={t} className="tnum absolute top-1/2 -translate-y-1/2 pl-1 whitespace-nowrap" style={{ left: `${pct(m.t0 + t)}%` }}>
              {t === 0 ? '0' : fmtMs(t)}
            </span>
          ))}
        </div>
      </div>
      <div ref={scroller} tabIndex={0} onKeyDown={onKey} className="relative min-h-0 flex-1 overflow-auto outline-none" data-table>
        <div style={{ height: v.getTotalSize(), position: 'relative' }}>
          {/* grid lines through all rows */}
          <div className="pointer-events-none absolute inset-y-0 right-0" style={{ left: nameW }}>
            {grid.map((t) => (
              <span key={t} className="absolute inset-y-0 w-px bg-line" style={{ left: `${pct(m.t0 + t)}%` }} />
            ))}
          </div>
          {v.getVirtualItems().map((vi) => {
            const n = m.nodes[rows[vi.index]]
            const isSel = n.i === sel
            const isCol = collapsed.has(n.i)
            const dim = (matchSet && !matchSet.has(n.i)) || (crit && !n.critical)
            const parentSvc = n.parent >= 0 ? m.nodes[n.parent].service : null
            const left = pct(n.start)
            const right = pct(n.end)
            const visible = right >= 0 && left <= 100
            const l = Math.max(0, left)
            const wd = Math.max(0.15, Math.min(100, right) - l)
            // label right of the bar; inside it when the bar is wide; else left of it
            const labelPos = Math.min(100, right) < 82 ? 'right' : wd > 14 ? 'inside' : 'left'
            return (
              <div
                key={n.i}
                onClick={() => onSelect(isSel ? null : n.i)}
                onDoubleClick={() => onZoom(n)}
                className={clsx(
                  'absolute inset-x-0 flex cursor-pointer items-center border-b border-line/60 text-sm hover:bg-panel-hover',
                  isSel && 'bg-accent-wash! shadow-[inset_2px_0_0_var(--accent)]',
                )}
                style={{ height: vi.size, transform: `translateY(${vi.start}px)` }}
              >
                <div className={clsx('flex h-full shrink-0 items-center gap-1 pr-2 pl-2', dim && 'opacity-40')} style={{ width: nameW }}>
                  <span className="relative flex h-full shrink-0" style={{ width: n.depth * 12 }}>
                    {Array.from({ length: Math.min(n.depth, 30) }, (_, d) => (
                      <span key={d} className="absolute inset-y-0 w-px bg-line" style={{ left: d * 12 + 5 }} />
                    ))}
                  </span>
                  {n.children.length ? (
                    <button
                      type="button"
                      onClick={(e) => {
                        e.stopPropagation()
                        toggle(n.i)
                      }}
                      className="grid size-4 shrink-0 place-items-center rounded text-ink-3 hover:bg-line hover:text-ink"
                      aria-label={isCol ? 'Expand' : 'Collapse'}
                    >
                      {isCol ? <ChevronRight className="size-3.5" /> : <ChevronDown className="size-3.5" />}
                    </button>
                  ) : (
                    <span className="size-4 shrink-0" />
                  )}
                  <span className="h-3.5 w-[3px] shrink-0 rounded-full" style={{ background: serviceColor(n.svc) }} />
                  {m.services.length > 1 && n.service !== parentSvc && <span className="shrink-0 text-2xs font-medium text-ink-3">{n.service}</span>}
                  <span className={clsx('min-w-0 truncate', n.failed && 'text-crit')} title={n.name}>
                    <Highlight text={n.name} q={ql} />
                  </span>
                  <GenAiBadge r={n.rec} />
                  {n.failed && <AlertTriangle className="size-3 shrink-0 text-crit" />}
                  {isCol && (
                    <span className="ml-auto flex shrink-0 items-center gap-1 pl-1 text-2xs">
                      <span className="tnum rounded bg-line px-1 text-ink-3">+{fmtCompact(n.desc)}</span>
                      {n.errorsBelow > 0 && <span className="tnum rounded bg-crit-wash px-1 text-crit">{n.errorsBelow} failed</span>}
                    </span>
                  )}
                </div>
                <div className={clsx('relative h-full flex-1 border-l border-line', dim && 'opacity-30')}>
                  {visible && (
                    <>
                      <span
                        className={clsx('absolute top-1/2 h-3 -translate-y-1/2 rounded-[3px]', isSel && 'ring-2 ring-accent/50')}
                        style={{ left: `${l}%`, width: `${wd}%`, minWidth: 2, background: n.failed ? 'var(--crit)' : serviceColor(n.svc) }}
                      >
                        {n.self < n.dur * 0.98 && n.children.length > 0 && !isCol && (
                          <span className="absolute inset-0 rounded-[3px] bg-panel/45" title="Lighter: time spent in children" />
                        )}
                      </span>
                      <span
                        className={clsx('tnum absolute top-1/2 -translate-y-1/2 text-2xs whitespace-nowrap', labelPos === 'inside' ? 'font-medium text-white' : 'text-ink-2')}
                        style={
                          labelPos === 'right'
                            ? { left: `calc(${Math.min(100, right)}% + 6px)` }
                            : labelPos === 'inside'
                              ? { right: `calc(${100 - Math.min(100, right)}% + 6px)` }
                              : { right: `calc(${100 - l}% + 6px)` }
                        }
                      >
                        {fmtMs(n.dur)}
                      </span>
                    </>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      </div>
      <div className="flex shrink-0 items-center gap-3 border-t border-line px-3 py-1 text-2xs text-ink-4">
        <span>
          <Kbd>j</Kbd>/<Kbd>k</Kbd> move
        </span>
        <span>
          <Kbd>←</Kbd>/<Kbd>→</Kbd> fold
        </span>
        <span>
          <Kbd>e</Kbd> next error
        </span>
        <span>
          <Kbd>c</Kbd> critical path
        </span>
        <span>
          <Kbd>z</Kbd> zoom · <Kbd>0</Kbd> reset
        </span>
        <span>double-click a span to zoom · drag the minimap</span>
      </div>
    </div>
  )
}

function Highlight({ text, q: ql }: { text: string; q: string }) {
  if (!ql) return <>{text}</>
  const i = text.toLowerCase().indexOf(ql)
  if (i < 0) return <>{text}</>
  return (
    <>
      {text.slice(0, i)}
      <mark className="rounded-[2px] bg-warn/30 text-inherit">{text.slice(i, i + ql.length)}</mark>
      {text.slice(i + ql.length)}
    </>
  )
}

/** GenAI-aware rows: LLM calls show their tokens, tools and agents are labelled. */
function GenAiBadge({ r }: { r: Rec }) {
  const op = r['gen_ai.operation.name']
  if (!op) return null
  if (op === 'chat')
    return (
      <span className="inline-flex shrink-0 items-center gap-1 rounded bg-[var(--genai-llm)]/15 px-1 text-2xs text-[var(--genai-llm)]">
        <Sparkles className="size-2.5" /> LLM {fmtTokens(num(r['gen_ai.usage.input_tokens']))}→{fmtTokens(num(r['gen_ai.usage.output_tokens']))}
      </span>
    )
  if (op === 'execute_tool') return <span className="shrink-0 rounded bg-[var(--genai-tool)]/15 px-1 text-2xs text-[var(--genai-tool)]">
        <Wrench className="mr-0.5 inline size-2.5" />
        {r['gen_ai.tool.name'] ?? 'tool'}
      </span>
  if (op === 'invoke_agent') return <span className="shrink-0 rounded bg-accent-wash px-1 text-2xs text-accent-ink">
        <Bot className="mr-0.5 inline size-2.5" />
        {r['gen_ai.agent.name'] ?? 'agent'}
      </span>
  return <span className="shrink-0 rounded bg-line px-1 text-2xs text-ink-3">{op}</span>
}

// ── flame graph ─────────────────────────────────────────────────────────────

const FL_H = 20

/** Icicle view: depth down, time across. Shows where nesting and time pile up. */
function FlameGraph({
  m,
  win,
  sel,
  onSelect,
  onZoom,
  matchSet,
  crit,
}: {
  m: TraceModel
  win: Win
  sel: number
  onSelect: (i: number | null) => void
  onZoom: (n: TNode) => void
  matchSet: Set<number> | null
  crit: boolean
}) {
  const wrap = useRef<HTMLDivElement>(null)
  const canvas = useRef<HTMLCanvasElement>(null)
  const [width, setWidth] = useState(0)
  const [hover, setHover] = useState<{ n: TNode; x: number; y: number } | null>(null)
  const theme = useThemeVersion()
  const H = (m.maxDepth + 1) * FL_H
  const byDepth = useMemo(() => {
    const d: TNode[][] = Array.from({ length: m.maxDepth + 1 }, () => [])
    for (const n of m.nodes) d[n.depth].push(n)
    d.forEach((a) => a.sort((x, y) => x.start - y.start))
    return d
  }, [m])
  useEffect(() => {
    const ro = new ResizeObserver(([e]) => setWidth(Math.floor(e.contentRect.width)))
    if (wrap.current) ro.observe(wrap.current)
    return () => ro.disconnect()
  }, [])
  const [w0, w1] = win
  const ws = w1 - w0 || 1
  useEffect(() => {
    const c = canvas.current
    if (!c || !width) return
    const dpr = window.devicePixelRatio || 1
    c.width = width * dpr
    c.height = H * dpr
    const ctx = c.getContext('2d')!
    ctx.scale(dpr, dpr)
    ctx.clearRect(0, 0, width, H)
    ctx.font = '11px Inter, system-ui, sans-serif'
    ctx.textBaseline = 'middle'
    const crit_ = cssVar('--crit')
    const ink = '#ffffffee'
    const accent = cssVar('--accent')
    const cols = new Map<number, string>()
    for (const n of m.nodes) {
      const x0 = ((n.start - w0) / ws) * width
      const x1 = ((n.end - w0) / ws) * width
      if (x1 < 0 || x0 > width) continue
      const x = Math.max(0, x0)
      const wd = Math.max(1, Math.min(width, x1) - x)
      const y = n.depth * FL_H
      let col = cols.get(n.svc)
      if (!col) {
        col = cssVar(serviceSlot(n.svc))
        cols.set(n.svc, col)
      }
      ctx.globalAlpha = (matchSet && !matchSet.has(n.i)) || (crit && !n.critical) ? 0.22 : 1
      ctx.fillStyle = n.failed ? crit_ : col
      ctx.fillRect(x, y + 1, wd - (wd > 3 ? 1 : 0), FL_H - 2)
      if (wd > 70) {
        ctx.save()
        ctx.beginPath()
        ctx.rect(x + 4, y, wd - 8, FL_H)
        ctx.clip()
        ctx.fillStyle = ink
        ctx.fillText(n.name, x + 4, y + FL_H / 2)
        ctx.restore()
      }
      if (n.i === sel) {
        ctx.globalAlpha = 1
        ctx.strokeStyle = accent
        ctx.lineWidth = 2
        ctx.strokeRect(x + 1, y + 2, wd - 2, FL_H - 4)
      }
    }
    ctx.globalAlpha = 1
  }, [m, width, H, w0, ws, sel, matchSet, crit, theme])

  const hit = (e: React.MouseEvent) => {
    const r = canvas.current!.getBoundingClientRect()
    const x = e.clientX - r.left
    const y = e.clientY - r.top
    const d = Math.floor(y / FL_H)
    const t = w0 + (x / width) * ws
    const row = byDepth[d]
    if (!row) return null
    // last span starting before t that is still running at t
    let lo = 0
    let hi = row.length - 1
    let k = -1
    while (lo <= hi) {
      const mid = (lo + hi) >> 1
      if (row[mid].start <= t) {
        k = mid
        lo = mid + 1
      } else hi = mid - 1
    }
    for (let j = k; j >= 0 && j > k - 50; j--) if (row[j].end >= t) return { n: row[j], x, y }
    return null
  }

  return (
    <div className="min-h-0 flex-1 overflow-auto p-3">
      <div ref={wrap} className="relative" style={{ height: H }}>
        <canvas
          ref={canvas}
          style={{ width: '100%', height: H }}
          className="cursor-pointer"
          onMouseMove={(e) => setHover(hit(e))}
          onMouseLeave={() => setHover(null)}
          onClick={(e) => {
            const h = hit(e)
            onSelect(h ? (h.n.i === sel ? null : h.n.i) : null)
          }}
          onDoubleClick={(e) => {
            const h = hit(e)
            if (h) onZoom(h.n)
          }}
        />
        {hover && (
          <div
            className="pointer-events-none absolute z-10 max-w-md rounded-md bg-raised px-2.5 py-1.5 text-xs shadow-pop"
            style={{ left: Math.min(hover.x + 12, width - 320), top: hover.y + 14 }}
          >
            <div className="truncate font-medium">{hover.n.name}</div>
            <div className="tnum mt-0.5 text-ink-3">
              {hover.n.service} · {fmtMs(hover.n.dur)} · self {fmtMs(hover.n.self)} · +{fmtMs(hover.n.start - m.t0)}
            </div>
          </div>
        )}
      </div>
      <div className="mt-2 text-2xs text-ink-4">Click to inspect · double-click to zoom · drag the minimap to pick a window</div>
    </div>
  )
}

// ── summary ─────────────────────────────────────────────────────────────────

/** Repeated work folded into operations, ranked by self time: where the time actually went. */
function Summary({ m, onPick }: { m: TraceModel; onPick: (op: OpStat) => void }) {
  const ops = useMemo(() => operations(m), [m])
  const totalSelf = ops.reduce((a, o) => a + o.self, 0) || 1
  const cols: Column<OpStat>[] = [
    {
      key: 'op',
      header: 'Operation',
      width: 'minmax(260px,2.4fr)',
      render: (o) => (
        <span className="flex min-w-0 items-center gap-2">
          <span className="h-3.5 w-[3px] shrink-0 rounded-full" style={{ background: serviceColor(o.svc) }} />
          <span className="truncate font-medium" title={o.op}>
            {o.op}
          </span>
        </span>
      ),
      sort: (o) => o.op,
    },
    { key: 'svc', header: 'Service', width: 'minmax(110px,1fr)', render: (o) => <span className="text-ink-3">{o.service}</span>, sort: (o) => o.service },
    { key: 'count', header: 'Spans', width: '70px', align: 'right', render: (o) => fmtInt(o.count), sort: (o) => o.count },
    {
      key: 'self',
      header: 'Self time',
      width: '210px',
      align: 'right',
      render: (o) => (
        <span className="flex items-center justify-end gap-2">
          <span className="h-1.5 w-20 overflow-hidden rounded-full bg-line">
            <span className="block h-full rounded-full" style={{ width: `${(100 * o.self) / totalSelf}%`, background: serviceColor(o.svc) }} />
          </span>
          <span className="w-16">{fmtMs(o.self)}</span>
          <span className="w-10 text-ink-3">{fmtPct((100 * o.self) / totalSelf)}</span>
        </span>
      ),
      sort: (o) => o.self,
    },
    { key: 'total', header: 'Total', width: '90px', align: 'right', render: (o) => <span className="text-ink-2">{fmtMs(o.total)}</span>, sort: (o) => o.total },
    { key: 'avg', header: 'Avg', width: '80px', align: 'right', render: (o) => <span className="text-ink-2">{fmtMs(o.total / o.count)}</span>, sort: (o) => o.total / o.count },
    { key: 'max', header: 'Max', width: '80px', align: 'right', render: (o) => <span className="text-ink-2">{fmtMs(o.max)}</span>, sort: (o) => o.max },
    {
      key: 'err',
      header: 'Failed',
      width: '64px',
      align: 'right',
      render: (o) => (o.errors ? <span className="text-crit">{fmtInt(o.errors)}</span> : <span className="text-ink-4">—</span>),
      sort: (o) => o.errors,
    },
  ]
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="border-b border-line px-3 py-2 text-xs text-ink-3">
        {fmtInt(m.nodes.length)} spans fold into <b className="text-ink">{fmtInt(ops.length)}</b> operations. Self time excludes time spent in child spans, so
        it adds up to the work actually done. Click an operation to find its spans in the waterfall.
      </div>
      <DataTable rows={ops} columns={cols} rowKey={(o) => o.key} onOpen={onPick} initialSort={{ key: 'self', dir: 'desc' }} className="flex-1" autoFocus />
    </div>
  )
}

// ── span panel ──────────────────────────────────────────────────────────────

const RESOURCE = /^(service|host|k8s|process|container|cloud|aws|gcp|azure|os|telemetry|otel|deployment|faas|device|browser)\./
const CORE = /^(span\.|trace\.|start_time$|end_time$|duration$|timestamp$|request\.|transaction\.|supportability|primary_tags|endpoint\.)/

function SpanPanel({
  m,
  n,
  traceId,
  win,
  onClose,
  onSelect,
  onZoom,
}: {
  m: TraceModel
  n: TNode
  traceId: string
  win: { from: string; to: string }
  onClose: () => void
  onSelect: (i: number) => void
  onZoom: () => void
}) {
  const detail = useDql({ query: spanDetailQuery(traceId, n.id), ...win, ttl: 600 })
  const logs = useDql({ query: spanLogsQuery(traceId, n.id), ...win, ttl: 120 })
  const rec: Rec = detail.data?.records[0] ?? n.rec
  const total = m.t1 - m.t0 || 1
  const chain: TNode[] = []
  for (let p = n.parent; p >= 0 && chain.length < 6; p = m.nodes[p].parent) chain.unshift(m.nodes[p])
  const fullName = String(rec['span.name'] ?? n.name)
  const svcId = rec['dt.smartscape.service']

  const groups = useMemo(() => {
    const attrs: [string, unknown][] = []
    const core: [string, unknown][] = []
    const resource: [string, unknown][] = []
    const dt: [string, unknown][] = []
    for (const [k, v] of Object.entries(rec)) {
      if (k === 'span.events' || v == null || v === '') continue
      if (k.startsWith('dt.')) dt.push([k, v])
      else if (RESOURCE.test(k)) resource.push([k, v])
      else if (CORE.test(k)) core.push([k, v])
      else attrs.push([k, v])
    }
    const by = (a: [string, unknown], b: [string, unknown]) => a[0].localeCompare(b[0])
    return [
      { title: 'Attributes', rows: attrs.sort(by), open: true },
      { title: 'Span', rows: core.sort(by), open: true },
      { title: 'Resource', rows: resource.sort(by), open: false },
      { title: 'Dynatrace', rows: dt.sort(by), open: false },
    ].filter((g) => g.rows.length)
  }, [rec])

  return (
    <SidePanel
      title={<span className="truncate">{n.name}</span>}
      onClose={onClose}
      actions={<Badge tone={n.failed ? 'crit' : 'muted'}>{n.failed ? 'failed' : String(rec['span.kind'] ?? 'span')}</Badge>}
    >
      {/* where it sits */}
      {chain.length > 0 && (
        <div className="mb-3 flex flex-wrap items-center gap-1 text-xs text-ink-3">
          {chain.map((c) => (
            <span key={c.i} className="flex min-w-0 items-center gap-1">
              <button type="button" onClick={() => onSelect(c.i)} className="max-w-48 truncate hover:text-accent-ink" title={c.name}>
                {c.name}
              </button>
              <ChevronRight className="size-3 shrink-0 text-ink-4" />
            </span>
          ))}
        </div>
      )}
      {fullName.length > 60 && (
        <div className="group relative mb-3 rounded-lg bg-sunken p-2.5 pr-8 font-mono text-xs leading-relaxed break-all text-ink-2">
          {fullName}
          <CopyButton value={fullName} label="span name" className="absolute top-1.5 right-1.5" />
        </div>
      )}

      {/* timing */}
      <div className="mb-3 rounded-lg border border-line p-3">
        <div className="relative mb-2 h-2 rounded-full bg-line">
          <span
            className="absolute inset-y-0 rounded-full"
            style={{ left: `${((n.start - m.t0) / total) * 100}%`, width: `${Math.max(0.5, (n.dur / total) * 100)}%`, background: n.failed ? 'var(--crit)' : serviceColor(n.svc) }}
          />
        </div>
        <div className="grid grid-cols-4 gap-2 text-xs">
          <Fact label="Duration" value={fmtMs(n.dur)} />
          <Fact label="Self time" value={fmtMs(n.self)} hint={n.children.length ? `${fmtPct((100 * n.self) / (n.dur || 1), 0)} of span` : 'no children'} />
          <Fact label="Starts at" value={`+${fmtMs(n.start - m.t0)}`} />
          <Fact label="Of trace" value={fmtPct((100 * n.dur) / total)} hint={n.critical ? 'on critical path' : undefined} />
        </div>
        <div className="mt-2.5 flex flex-wrap items-center gap-2 text-xs">
          <span className="inline-flex items-center gap-1.5">
            <i className="inline-block size-2 rounded-[2px]" style={{ background: serviceColor(n.svc) }} />
            {svcId ? <EntityLink id={svcId} name={n.service} type="SERVICE" /> : n.service}
          </span>
          {n.children.length > 0 && <span className="text-ink-3">· {fmtInt(n.children.length)} children · {fmtInt(n.desc)} below</span>}
          <button type="button" onClick={onZoom} className="ml-auto inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-accent-ink hover:bg-accent-wash">
            <ZoomIn className="size-3" /> zoom to span
          </button>
        </div>
      </div>

      <SpanEvents rec={rec} />

      {(logs.data?.records.length ?? 0) > 0 && (
        <Section title="Logs" count={logs.data!.records.length} open>
          <div className="max-h-64 overflow-auto rounded-md border border-line">
            <LogStream records={logs.data?.records} loading={false} />
          </div>
        </Section>
      )}

      {detail.isLoading ? (
        <Skeleton className="h-40" />
      ) : detail.error ? (
        <>
          <ErrorBox error={detail.error} />
          <Inspector rec={n.rec} />
        </>
      ) : (
        <AttrSections groups={groups} />
      )}
    </SidePanel>
  )
}

function Fact({ label, value, hint }: { label: string; value: ReactNode; hint?: string }) {
  return (
    <div>
      <div className="text-2xs text-ink-3">{label}</div>
      <div className="tnum font-medium">{value}</div>
      {hint && <div className="text-2xs text-ink-4">{hint}</div>}
    </div>
  )
}

function Section({ title, count, open, children }: { title: string; count: number; open?: boolean; children: ReactNode }) {
  return (
    <details open={open} className="group/s mb-2 rounded-lg border border-line">
      <summary className="flex cursor-pointer list-none items-center gap-1.5 px-3 py-2 text-xs font-medium text-ink-2 select-none">
        <ChevronRight className="size-3.5 text-ink-4 transition-transform group-open/s:rotate-90" />
        {title}
        <span className="tnum font-normal text-ink-4">{count}</span>
      </summary>
      <div className="border-t border-line px-3 py-1.5">{children}</div>
    </details>
  )
}

function AttrSections({ groups }: { groups: { title: string; rows: [string, unknown][]; open: boolean }[] }) {
  const [filter, setFilter] = useState('')
  const f = filter.toLowerCase()
  const match = ([k, v]: [string, unknown]) => !f || k.toLowerCase().includes(f) || String(typeof v === 'string' ? v : JSON.stringify(v)).toLowerCase().includes(f)
  return (
    <div>
      <input
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
        placeholder={`Filter ${groups.reduce((a, g) => a + g.rows.length, 0)} attributes…`}
        className="mb-2 h-7 w-full rounded-md border border-line bg-sunken px-2 text-xs outline-none placeholder:text-ink-4 focus:border-accent/60"
      />
      {groups.map((g) => {
        const rows = g.rows.filter(match)
        if (!rows.length) return null
        return (
          <Section key={g.title} title={g.title} count={rows.length} open={g.open || !!f}>
            <div className="divide-y divide-line">
              {rows.map(([k, v]) => (
                <div key={k} className="group grid grid-cols-[minmax(120px,40%)_1fr_auto] items-start gap-3 py-1.5 text-xs">
                  <div className="truncate font-mono text-ink-3" title={k}>
                    {k}
                  </div>
                  <div className="min-w-0 break-words text-ink">
                    <Value k={k} v={v} />
                  </div>
                  <CopyButton value={typeof v === 'string' ? v : JSON.stringify(v)} label={k} className="opacity-0 group-hover:opacity-100" />
                </div>
              ))}
            </div>
          </Section>
        )
      })}
    </div>
  )
}

function SpanEvents({ rec }: { rec: Rec }) {
  const events: Rec[] = Array.isArray(rec['span.events']) ? rec['span.events'] : []
  const ex = events.filter((e) => e['span_event.name'] === 'exception')
  const other = events.filter((e) => e['span_event.name'] !== 'exception')
  if (!events.length) return null
  return (
    <>
      {ex.length > 0 && (
        <div className="mb-3 rounded-lg border border-crit/30 bg-crit-wash p-3">
          {ex.map((e, i) => (
            <div key={i} className="mb-2 last:mb-0">
              <div className="text-sm font-medium text-crit">{e['exception.type'] ?? 'Exception'}</div>
              <div className="text-xs text-ink-2">{e['exception.message']}</div>
              {e['exception.stack_trace'] && (
                <details className="mt-1">
                  <summary className="cursor-pointer text-xs text-ink-3">stack trace</summary>
                  <pre className="mt-1 max-h-64 overflow-auto font-mono text-2xs whitespace-pre-wrap text-ink-2">{e['exception.stack_trace']}</pre>
                </details>
              )}
            </div>
          ))}
        </div>
      )}
      {other.length > 0 && (
        <Section title="Events" count={other.length}>
          {other.map((e, i) => (
            <div key={i} className="py-1 text-xs">
              <span className="font-medium">{e['span_event.name']}</span>{' '}
              <Tip content={<pre className="text-2xs">{JSON.stringify(e, null, 2)}</pre>}>
                <span className="cursor-help text-ink-3">{Object.keys(e).length} fields</span>
              </Tip>
            </div>
          ))}
        </Section>
      )}
    </>
  )
}

