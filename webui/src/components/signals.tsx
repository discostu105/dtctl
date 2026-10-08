import { useVirtualizer } from '@tanstack/react-virtual'
import clsx from 'clsx'
import { AlertOctagon, ChevronRight, Maximize2, Minimize2, X } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Link } from 'wouter'
import { num, type Rec } from '../lib/api'
import type { FacetCtl } from './Facets'
import type { Facet } from '../lib/facets'
import { fmtNs, fmtTime, span, titleCase } from '../lib/format'
import { problemHref, traceHref } from '../lib/links'
import { inspectorRows } from '../lib/record'
import { DataTable, type Column } from './DataTable'
import { EntityLink } from './Entity'
import { Badge, CopyButton, Empty, Kbd, SkeletonRows, TimeAgo, Tip, type Tone, When } from './ui'

// ── log levels ───────────────────────────────────────────────────────────────

export function levelTone(level: unknown): Tone {
  const l = String(level ?? '').toUpperCase()
  if (['ERROR', 'SEVERE', 'FATAL', 'CRITICAL', 'EMERGENCY', 'ALERT'].includes(l)) return 'crit'
  if (['WARN', 'WARNING'].includes(l)) return 'warn'
  if (['INFO', 'NOTICE'].includes(l)) return 'info'
  return 'muted'
}

export function LevelBadge({ level }: { level: unknown }) {
  const l = String(level ?? 'NONE').toUpperCase()
  return (
    <Badge tone={levelTone(l)} className="w-[54px] justify-center font-mono tracking-tight">
      {l.slice(0, 5)}
    </Badge>
  )
}

export function logSource(r: Rec): string {
  return r['k8s.pod.name'] || r['dt.process_group.detected_name'] || r['service.name'] || r['log.source'] || r['host.name'] || ''
}

// ── record inspector ────────────────────────────────────────────────────────

const ENTITY_ID = /^[A-Z][A-Z0-9_]+-[0-9A-F]{16}$/
const TRACE_FIELDS = new Set(['trace_id', 'trace.id', 'dt.trace_id'])

export function Value({ k, v }: { k: string; v: unknown }): ReactNode {
  if (v == null) return <span className="text-ink-4">null</span>
  if (Array.isArray(v)) {
    if (v.length === 0) return <span className="text-ink-4">[]</span>
    if (v.every((x) => typeof x !== 'object'))
      return (
        <span className="flex flex-wrap gap-1">
          {v.map((x, i) => (
            <Value key={i} k={k} v={x} />
          ))}
        </span>
      )
    return <pre className="font-mono text-xs whitespace-pre-wrap text-ink-2">{JSON.stringify(v, null, 2)}</pre>
  }
  if (typeof v === 'object') return <pre className="font-mono text-xs whitespace-pre-wrap text-ink-2">{JSON.stringify(v, null, 2)}</pre>
  const s = String(v)
  if (ENTITY_ID.test(s)) return <EntityLink id={s} showId className="text-xs" />
  if (TRACE_FIELDS.has(k) && /^[0-9a-f]{32}$/i.test(s))
    return (
      <Link href={traceHref(s)} className="font-mono text-xs text-accent-ink hover:underline">
        {s}
      </Link>
    )
  if (/^https?:\/\//.test(s))
    return (
      <a href={s} target="_blank" rel="noreferrer" className="break-all text-accent-ink hover:underline">
        {s}
      </a>
    )
  if (/^\d{4}-\d{2}-\d{2}T/.test(s))
    return (
      <span>
        {s} <TimeAgo value={s} className="text-ink-3" />
      </span>
    )
  return <span className="break-words whitespace-pre-wrap">{s}</span>
}

/** Pretty-print JSON bodies; leave everything else as-is. */
export function prettyContent(s: string): { text: string; json: boolean } {
  const t = s.trim()
  if ((t.startsWith('{') && t.endsWith('}')) || (t.startsWith('[') && t.endsWith(']'))) {
    try {
      return { text: JSON.stringify(JSON.parse(t), null, 2), json: true }
    } catch {
      /* not json */
    }
  }
  return { text: s, json: false }
}

/**
 * All attributes of a record, sorted, filterable. Empty values and legacy
 * dt.entity.* ids that a dt.smartscape.* field already shows are held back
 * (lib/record.ts); one click shows them.
 */
export function Inspector({ rec, hide = [] }: { rec: Rec; hide?: string[] }) {
  const [filter, setFilter] = useState('')
  const [showAll, setShowAll] = useState(false)
  const { rows: entries, empty, twins } = useMemo(() => inspectorRows(rec, { hide, all: showAll, filter }), [rec, hide, showAll, filter])
  const held = empty + twins
  const heldLabel = [empty && `${empty} empty`, twins && `${twins} duplicate ${twins === 1 ? 'id' : 'ids'}`].filter(Boolean).join(', ')
  return (
    <div>
      <div className="mb-2 flex items-center gap-2">
        <input
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Escape' && filter) {
              e.stopPropagation()
              setFilter('')
            }
          }}
          placeholder={`Filter ${Object.keys(rec).filter((k) => !hide.includes(k)).length - held} attributes…`}
          className="h-7 min-w-0 flex-1 rounded-md border border-line bg-sunken px-2 text-xs outline-none placeholder:text-ink-4 focus:border-accent/60"
        />
        {held > 0 && (
          <Tip content={twins ? 'Duplicate ids: dt.entity.* fields that repeat the id of a dt.smartscape.* field' : 'null, empty text and empty lists'}>
            <button type="button" onClick={() => setShowAll(!showAll)} className="shrink-0 rounded px-1.5 py-1 text-2xs text-ink-3 hover:bg-line hover:text-ink-2">
              {showAll ? `hide ${heldLabel}` : `show ${heldLabel}`}
            </button>
          </Tip>
        )}
      </div>
      <div className="divide-y divide-line">
        {entries.map(([k, v]) => (
          <div key={k} className="group grid grid-cols-[minmax(120px,38%)_1fr_auto] items-start gap-3 py-1.5 text-xs">
            <div className="truncate font-mono text-ink-3" title={k}>
              {k}
            </div>
            <div className="min-w-0 text-ink">
              <Value k={k} v={v} />
            </div>
            <CopyButton value={typeof v === 'string' ? v : JSON.stringify(v)} label={k} className="opacity-0 group-hover:opacity-100" />
          </div>
        ))}
      </div>
    </div>
  )
}

const MAX_KEY = 'dtctl-web:panel-max'
function loadMax() {
  try {
    return localStorage.getItem(MAX_KEY) === '1'
  } catch {
    return false
  }
}

/**
 * Right-hand detail panel. Maximizable (button, double-click the header, or
 * `m`) to fill the content area; the choice is remembered across panels.
 * `Esc` restores a maximized panel first, then closes.
 */
const PANEL_WIDTH = { default: 'w-[min(640px,48vw)]', wide: 'w-[min(720px,52vw)]' }

/** Detail panel sizes: `default` for records, spans, tools; `wide` only for LLM message content. */
export function SidePanel({
  title,
  onClose,
  children,
  actions,
  size = 'default',
}: {
  title: ReactNode
  onClose: () => void
  children: ReactNode
  actions?: ReactNode
  size?: keyof typeof PANEL_WIDTH
}) {
  const width = PANEL_WIDTH[size]
  const [max, setMax] = useState(loadMax)
  const toggle = () =>
    setMax((m) => {
      try {
        localStorage.setItem(MAX_KEY, m ? '0' : '1')
      } catch {
        /* ignore */
      }
      return !m
    })
  const state = useRef({ max, toggle, onClose })
  state.current = { max, toggle, onClose }
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement).closest('input, textarea, [contenteditable=true], [cmdk-root]') || e.metaKey || e.ctrlKey || e.altKey) return
      if (e.key === 'm') {
        e.preventDefault()
        state.current.toggle()
      } else if (e.key === 'Escape') {
        if (state.current.max) state.current.toggle()
        else state.current.onClose()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
  return (
    <aside
      className={clsx(
        'flex shrink-0 flex-col border-l border-line bg-panel',
        max ? 'anim-pop fixed top-12 right-0 bottom-0 left-[var(--rail-w)] z-40 shadow-pop' : `anim-slide h-full ${width}`,
      )}
    >
      <div className="flex h-11 shrink-0 cursor-default items-center gap-2 border-b border-line px-3 select-none" onDoubleClick={toggle}>
        <div className="min-w-0 flex-1 truncate text-sm font-medium">{title}</div>
        {actions}
        <Tip content={<span className="flex items-center gap-2">{max ? 'Restore' : 'Maximize'} <Kbd>M</Kbd></span>}>
          <button type="button" onClick={toggle} className="rounded p-1 text-ink-3 hover:bg-line hover:text-ink" aria-label={max ? 'Restore panel' : 'Maximize panel'}>
            {max ? <Minimize2 className="size-4" /> : <Maximize2 className="size-4" />}
          </button>
        </Tip>
        <Tip content={<span className="flex items-center gap-2">Close <Kbd>Esc</Kbd></span>}>
          <button type="button" onClick={onClose} className="rounded p-1 text-ink-3 hover:bg-line hover:text-ink" aria-label="Close">
            <X className="size-4" />
          </button>
        </Tip>
      </div>
      <div className={clsx('min-h-0 flex-1 overflow-auto p-3', max && 'mx-auto w-full max-w-[1400px] px-6')}>{children}</div>
    </aside>
  )
}

// ── log stream ─────────────────────────────────────────────────────────────

export function LogStream({
  records,
  loading,
  onSelect,
  selected,
  highlight,
  className,
}: {
  records: Rec[] | undefined
  loading?: boolean
  onSelect?: (r: Rec | null) => void
  selected?: Rec | null
  highlight?: string
  className?: string
}) {
  const ref = useRef<HTMLDivElement>(null)
  const rows = records ?? []
  const v = useVirtualizer({ count: rows.length, getScrollElement: () => ref.current, estimateSize: () => 30, overscan: 20 })
  // j/k (or arrows) move through records and drive the detail panel, like every list
  const at = selected ? rows.indexOf(selected) : -1
  const onKey = (e: React.KeyboardEvent) => {
    if (!onSelect || e.metaKey || e.ctrlKey || e.altKey) return
    const step = e.key === 'j' || e.key === 'ArrowDown' ? 1 : e.key === 'k' || e.key === 'ArrowUp' ? -1 : 0
    if (step) {
      e.preventDefault()
      const next = Math.max(0, Math.min(rows.length - 1, at + step))
      onSelect(rows[next])
      v.scrollToIndex(next, { align: 'auto' })
    } else if (e.key === 'Home' || e.key === 'End') {
      e.preventDefault()
      const next = e.key === 'Home' ? 0 : rows.length - 1
      onSelect(rows[next])
      v.scrollToIndex(next, { align: 'auto' })
    }
  }
  if (loading && !records) return <SkeletonRows rows={12} />
  if (rows.length === 0) return <Empty title="No log records" hint="Nothing matched in this timeframe. Try widening it (T) or loosening the filters." />
  return (
    <div ref={ref} className={clsx('min-h-0 overflow-auto outline-none', className)} tabIndex={0} onKeyDown={onKey}>
      <div style={{ height: v.getTotalSize(), position: 'relative' }}>
        {v.getVirtualItems().map((vi) => {
          const r = rows[vi.index]
          const sel = selected === r
          return (
            <div
              key={vi.key}
              onClick={() => onSelect?.(sel ? null : r)}
              className={clsx(
                'absolute inset-x-0 flex h-[30px] cursor-pointer items-center gap-3 border-b border-line px-3 font-mono text-xs hover:bg-panel-hover',
                sel && 'bg-accent-wash! shadow-[inset_2px_0_0_var(--accent)]',
                levelTone(r.loglevel) === 'crit' && !sel && 'bg-crit-wash/40',
              )}
              style={{ transform: `translateY(${vi.start}px)` }}
            >
              <span className="tnum w-[92px] shrink-0 text-ink-3">{fmtTime(r.timestamp)}</span>
              <LevelBadge level={r.loglevel ?? r.status} />
              <span className="w-[180px] shrink-0 truncate font-sans text-ink-3" title={logSource(r)}>
                {logSource(r)}
              </span>
              <span className="min-w-0 flex-1 truncate text-ink">
                <Highlight text={String(r.content ?? '')} term={highlight} />
              </span>
              <ChevronRight className="size-3.5 shrink-0 text-ink-4" />
            </div>
          )
        })}
      </div>
    </div>
  )
}

export function Highlight({ text, term }: { text: string; term?: string }) {
  if (!term) return <>{text}</>
  const i = text.toLowerCase().indexOf(term.toLowerCase())
  if (i < 0) return <>{text}</>
  return (
    <>
      {text.slice(0, i)}
      <mark className="rounded-[2px] bg-warn/30 text-ink">{text.slice(i, i + term.length)}</mark>
      {text.slice(i + term.length)}
    </>
  )
}

export function LogDetail({ rec }: { rec: Rec }) {
  const { text, json } = prettyContent(String(rec.content ?? ''))
  const traceId = rec.trace_id || rec['trace.id']
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <LevelBadge level={rec.loglevel ?? rec.status} />
        <span className="tnum text-xs text-ink-2">{fmtTime(rec.timestamp)}</span>
        <TimeAgo value={rec.timestamp} className="text-xs text-ink-3" />
        {traceId && (
          <Link href={traceHref(String(traceId), rec.timestamp, rec.span_id ? String(rec.span_id) : undefined)} className="ml-auto text-xs text-accent-ink hover:underline">
            Open trace →
          </Link>
        )}
      </div>
      <div className="relative rounded-lg border border-line bg-sunken">
        <CopyButton value={String(rec.content ?? '')} label="content" className="absolute top-1.5 right-1.5" />
        <pre className={clsx('max-h-80 overflow-auto p-3 pr-8 font-mono text-xs leading-relaxed break-words whitespace-pre-wrap', json ? 'text-ink-2' : 'text-ink')}>{text}</pre>
      </div>
      <Inspector rec={rec} hide={['content']} />
    </div>
  )
}

// ── spans ──────────────────────────────────────────────────────────────────

export function spanFailed(r: Rec) {
  return r['request.is_failed'] === true || r['span.status_code'] === 'error'
}

export const spanColumns: Column[] = [
  {
    key: 'time',
    header: 'Started',
    width: '108px',
    render: (r) => <span className="tnum font-mono text-xs text-ink-3">{fmtTime(r.start_time)}</span>,
    sort: (r) => r.start_time,
  },
  {
    key: 'status',
    header: '',
    width: '14px',
    render: (r) => <span className={clsx('inline-block size-2 rounded-full', spanFailed(r) ? 'bg-crit' : 'bg-ok/70')} />,
  },
  {
    key: 'name',
    header: 'Span',
    width: 'minmax(220px,2fr)',
    render: (r) => (
      <span className="flex min-w-0 items-center gap-2">
        {r['http.request.method'] && <span className="font-mono text-2xs text-ink-3">{r['http.request.method']}</span>}
        <span className="truncate">{r['endpoint.name'] || r['span.name']}</span>
        {r['db.system'] && <Badge>{r['db.system']}</Badge>}
        {r['gen_ai.request.model'] && <Badge tone="accent">{r['gen_ai.request.model']}</Badge>}
      </span>
    ),
    sort: (r) => r['span.name'],
  },
  {
    key: 'service',
    header: 'Service',
    width: 'minmax(140px,1fr)',
    facet: 'service',
    render: (r) =>
      r['dt.smartscape.service'] ? (
        <EntityLink id={r['dt.smartscape.service']} name={r.service} type="SERVICE" />
      ) : (
        <span className="text-ink-2">{r.service}</span>
      ),
    sort: (r) => r.service,
  },
  {
    key: 'code',
    header: 'Code',
    width: '64px',
    facet: 'code',
    align: 'right',
    render: (r) => {
      const c = num(r['http.response.status_code'])
      return Number.isFinite(c) ? <span className={clsx('font-mono text-xs', c >= 500 ? 'text-crit' : c >= 400 ? 'text-warn' : 'text-ink-3')}>{c}</span> : null
    },
    sort: (r) => num(r['http.response.status_code']),
  },
  {
    key: 'dur',
    header: 'Duration',
    width: '88px',
    align: 'right',
    render: (r) => <span className="font-mono text-xs">{fmtNs(num(r.duration))}</span>,
    sort: (r) => num(r.duration),
  },
]

/** Facets for span lists (client-side over the loaded spans). */
export const SPAN_FACETS: Facet<Rec>[] = [
  { key: 'service', label: 'Service', value: (r) => r.service },
  { key: 'status', label: 'Status', value: (r) => (spanFailed(r) ? 'Failed' : 'OK'), order: ['Failed', 'OK'] },
  { key: 'code', label: 'HTTP status', value: (r) => r['http.response.status_code'], aliases: ['status_code'] },
  { key: 'method', label: 'Method', value: (r) => r['http.request.method'] },
  { key: 'kind', label: 'Span kind', value: (r) => r['span.kind'] },
  { key: 'db', label: 'Database', value: (r) => r['db.system'] },
]

export function SpanTable({
  records,
  loading,
  maxHeight,
  className,
  facets,
  autoFocus,
}: {
  records: Rec[] | undefined
  loading?: boolean
  maxHeight?: number | string
  className?: string
  facets?: FacetCtl<Rec>
  autoFocus?: boolean
}) {
  return (
    <DataTable
      rows={records}
      loading={loading}
      columns={spanColumns}
      facets={facets}
      autoFocus={autoFocus}
      rowKey={(r, i) => `${r['span.id']}-${i}`}
      href={(r) => traceHref(r['trace.id'], r.start_time ?? r.timestamp, r['span.id'])}
      maxHeight={maxHeight}
      className={className}
      empty={<Empty title="No spans" hint="No traces matched in this timeframe." />}
    />
  )
}

// ── problems ───────────────────────────────────────────────────────────────

export function ProblemStatus({ status }: { status: string }) {
  return status === 'ACTIVE' ? (
    <Badge tone="crit">
      <span className="live-dot size-1.5 rounded-full bg-crit" /> Active
    </Badge>
  ) : (
    <Badge tone="muted">Closed</Badge>
  )
}

export const problemColumns: Column[] = [
  { key: 'id', header: 'ID', width: '96px', render: (r) => <span className="font-mono text-xs text-ink-3">{r.display_id}</span>, sort: (r) => r.display_id },
  { key: 'status', header: 'Status', width: '82px', facet: 'status', render: (r) => <ProblemStatus status={r.status} />, sort: (r) => r.status },
  {
    key: 'name',
    header: 'Problem',
    width: 'minmax(220px,2fr)',
    render: (r) => (
      <span className="flex min-w-0 items-center gap-2">
        <AlertOctagon className={clsx('size-3.5 shrink-0', r.status === 'ACTIVE' ? 'text-crit' : 'text-ink-4')} />
        <span className="truncate font-medium">{r.name}</span>
      </span>
    ),
    sort: (r) => r.name,
  },
  { key: 'cat', header: 'Category', width: '130px', facet: 'category', render: (r) => <span className="text-ink-2">{titleCase(String(r.category ?? ''))}</span>, sort: (r) => r.category },
  {
    key: 'affected',
    header: 'Affected',
    width: 'minmax(160px,1.2fr)',
    facet: 'affected',
    render: (r) => {
      const names: string[] = Array.isArray(r.affected) ? r.affected : []
      return (
        <span className="flex min-w-0 items-center gap-1.5 text-ink-2">
          <span className="truncate">{names[0] ?? '—'}</span>
          {names.length > 1 && <span className="shrink-0 text-2xs text-ink-3">+{names.length - 1}</span>}
        </span>
      )
    },
  },
  {
    key: 'dur',
    header: 'Duration',
    width: '84px',
    align: 'right',
    render: (r) => <span className="text-ink-2">{span(r.start, r.status === 'ACTIVE' ? Date.now() : r.end)}</span>,
    sort: (r) => (r.status === 'ACTIVE' ? Date.now() : Date.parse(r.end)) - Date.parse(r.start),
  },
  { key: 'start', header: 'Started', width: '130px', align: 'right', render: (r) => <When value={r.start} />, sort: (r) => r.start },
]

export function ProblemsTable({
  records,
  loading,
  maxHeight,
  facets,
  autoFocus,
}: {
  records: Rec[] | undefined
  loading?: boolean
  maxHeight?: number | string
  facets?: FacetCtl<Rec>
  autoFocus?: boolean
}) {
  return (
    <DataTable
      rows={records}
      loading={loading}
      columns={problemColumns}
      facets={facets}
      autoFocus={autoFocus}
      rowKey={(r) => r.display_id}
      href={(r) => problemHref(r.display_id)}
      maxHeight={maxHeight}
      empty={<Empty title="No problems" hint="Davis found nothing in this timeframe. Enjoy the quiet." />}
    />
  )
}

/** Vulnerability risk: level abbreviation and score, fixed width so a column lines up. */
export function RiskBadge({ level, score }: { level: string; score?: unknown }) {
  const tone = level === 'CRITICAL' ? 'crit' : level === 'HIGH' ? 'warn' : level === 'MEDIUM' ? 'info' : 'muted'
  return (
    <Badge tone={tone} className="w-[64px] justify-between font-mono">
      <span>{({ CRITICAL: 'CRIT', HIGH: 'HIGH', MEDIUM: 'MED', LOW: 'LOW', NONE: 'NONE' } as Record<string, string>)[level] ?? String(level ?? '').slice(0, 4)}</span>
      <span>{Number.isFinite(num(score)) ? num(score).toFixed(1) : ''}</span>
    </Badge>
  )
}
