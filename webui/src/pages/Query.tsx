import { useQueryClient } from '@tanstack/react-query'
import clsx from 'clsx'
import { AlertTriangle, BarChart3, CheckCircle2, Clock, ExternalLink, Play, Table2, Terminal, Trash2 } from 'lucide-react'
import { lazy, Suspense, useEffect, useMemo, useState } from 'react'
import { useSearch } from 'wouter'
import { TimeChart, tsAxis, SERIES } from '../components/Chart'
import { DataTable, type Column } from '../components/DataTable'
import { PageHeader, QueryInfo } from '../components/Panel'
import { Inspector, SidePanel } from '../components/signals'
import { CopyButton, Empty, ErrorBox, Kbd, Segmented, SkeletonRows, Tip } from '../components/ui'
import { dqlKey, forceFresh, num, useDql, useMeta, type DqlSpec, type Rec } from '../lib/api'
import { fmtAbs, fmtCompact, fmtTime } from '../lib/format'
import { highlightDql } from '../lib/highlight'
import { dtLinks } from '../lib/links'
import { useTitle } from '../lib/store'
import { useTimeframe } from '../lib/timeframe'
import { EntityLink } from '../components/Entity'

const DqlEditor = lazy(() => import('../components/DqlEditor'))

function VerifyStatus({ v }: { v: { valid: boolean; messages: string[] } | null }) {
  if (!v) return null
  if (v.valid && !v.messages.length)
    return (
      <span className="inline-flex items-center gap-1 text-xs text-ok">
        <CheckCircle2 className="size-3.5" /> valid
      </span>
    )
  return (
    <Tip content={v.messages.join(' · ')}>
      <span className={clsx('inline-flex max-w-96 items-center gap-1 truncate text-xs', v.valid ? 'text-warn' : 'text-crit')}>
        <AlertTriangle className="size-3.5 shrink-0" /> <span className="truncate">{v.messages[0] ?? 'invalid'}</span>
      </span>
    </Tip>
  )
}

const HIST_KEY = 'dtctl-web:query-history'
const EXAMPLES = [
  'fetch logs\n| filter loglevel == "ERROR"\n| summarize count = count(), by:{k8s.namespace.name}\n| sort count desc',
  'timeseries avg(dt.host.cpu.usage), by:{host.name}',
  'fetch spans\n| filter request.is_failed == true\n| summarize failures = count(), by:{service.name, endpoint.name}\n| sort failures desc\n| limit 20',
  'smartscapeNodes "*"\n| summarize count = count(), by:{type}\n| sort count desc',
]

function loadHistory(): string[] {
  try {
    return JSON.parse(localStorage.getItem(HIST_KEY) || '[]')
  } catch {
    return []
  }
}

export default function Query() {
  useTitle('Query')
  const params = new URLSearchParams(useSearch())
  const initial = params.get('dql') ?? loadHistory()[0] ?? EXAMPLES[0]
  const [text, setText] = useState(initial)
  const [spec, setSpec] = useState<DqlSpec | null>(null)
  const [history, setHistory] = useState<string[]>(loadHistory)
  const [hIdx, setHIdx] = useState(-1)
  const [view, setView] = useState<'auto' | 'table' | 'chart'>('auto')
  const [sel, setSel] = useState<Rec | null>(null)
  const tf = useTimeframe()
  const { data: meta } = useMeta()
  const qc = useQueryClient()
  const [verify, setVerify] = useState<{ valid: boolean; messages: string[] } | null>(null)

  useEffect(() => {
    const d = params.get('dql')
    if (d) {
      setText(d)
      if (params.get('run') || params.get('dql')) run(d)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const run = (q = text) => {
    const query = q.trim()
    if (!query) return
    const s: DqlSpec = { query, from: tf.from, to: tf.to, maxRecords: 5000, ttl: 5 }
    forceFresh()
    qc.removeQueries({ queryKey: dqlKey(s) })
    setSpec(s)
    setSel(null)
    const h = [query, ...history.filter((x) => x !== query)].slice(0, 50)
    setHistory(h)
    setHIdx(-1)
    try {
      localStorage.setItem(HIST_KEY, JSON.stringify(h))
    } catch {
      /* ignore */
    }
  }

  const res = useDql(spec)
  const records = res.data?.records

  const ts = useMemo(() => detectTimeseries(records), [records])
  const effective = view === 'auto' ? (ts ? 'chart' : 'table') : view

  // ⌘↑ / ⌘↓ walk the query history (newest first).
  const walkHistory = (dir: 1 | -1) => {
    const n = Math.max(-1, Math.min(history.length - 1, hIdx + dir))
    setHIdx(n)
    return n >= 0 ? history[n] : null
  }

  return (
    <div className="flex h-full">
      <div className="flex min-w-0 flex-1 flex-col p-5">
        <PageHeader title="Query" icon={<Terminal className="size-5" />} sub={`DQL workbench · ${tf.label.toLowerCase()} unless the query sets its own`} />

        <div className="rounded-xl border border-line bg-panel focus-within:border-accent/50">
          <div className="max-h-[42vh] overflow-auto">
            <Suspense fallback={<pre className="m-0 min-h-[120px] p-3 pl-12 font-mono text-[13px] leading-[21px] whitespace-pre-wrap text-ink">{highlightDql(text)}</pre>}>
              <DqlEditor value={text} onChange={setText} onRun={(doc) => run(doc)} onHistory={walkHistory} onVerify={setVerify} timeframe={{ from: tf.from, to: tf.to }} autoFocus />
            </Suspense>
          </div>
          <div className="flex items-center gap-2 border-t border-line px-2 py-1.5">
            <button
              type="button"
              onClick={() => run()}
              className="inline-flex h-7 items-center gap-1.5 rounded-md bg-accent px-3 text-sm font-medium text-white hover:brightness-110"
            >
              <Play className="size-3.5 fill-current" /> Run
              <span className="ml-1 text-2xs opacity-70">⌘↵</span>
            </button>
            <VerifyStatus v={verify} />
            <span className="hidden text-xs text-ink-3 xl:inline">
              <Kbd>⌘</Kbd> <Kbd>Space</Kbd> complete · <Kbd>⌘</Kbd> <Kbd>↑</Kbd> history · <Kbd>⌘</Kbd> <Kbd>/</Kbd> comment
            </span>
            <span className="ml-auto" />
            <CopyButton value={`dtctl query '${text.replace(/'/g, "'\\''")}'`} label="dtctl command" />
            <span className="text-xs text-ink-3">copy as dtctl</span>
            {meta?.environment && (
              <a
                href={dtLinks.notebook(meta.environment, text)}
                target="_blank"
                rel="noreferrer"
                className="inline-flex h-7 items-center gap-1 rounded-md px-2 text-xs text-ink-2 hover:bg-line hover:text-ink"
              >
                Open in Notebooks <ExternalLink className="size-3" />
              </a>
            )}
          </div>
        </div>

        <div className="mt-4 flex min-h-0 flex-1 flex-col rounded-xl border border-line bg-panel">
          <div className="flex h-10 shrink-0 items-center gap-3 border-b border-line px-3">
            <span className="text-sm font-medium">Result</span>
            {records && (
              <span className="tnum text-xs text-ink-3">
                {fmtCompact(records.length)} records{records.length >= 5000 ? ' (limit reached)' : ''}
              </span>
            )}
            <span className="ml-auto" />
            {records && (
              <Segmented
                value={view}
                onChange={setView}
                options={[
                  { value: 'auto', label: 'Auto' },
                  { value: 'table', label: <Table2 className="size-3.5" /> },
                  { value: 'chart', label: <BarChart3 className="size-3.5" /> },
                ]}
              />
            )}
            <QueryInfo spec={spec} result={res} />
          </div>
          <div className="flex min-h-0 flex-1 flex-col">
            {!spec ? (
              <Examples history={history} onPick={(q) => (setText(q), run(q))} onClear={() => (setHistory([]), localStorage.removeItem(HIST_KEY))} />
            ) : res.error ? (
              <ErrorBox error={res.error} />
            ) : res.isLoading ? (
              <SkeletonRows rows={8} />
            ) : !records?.length ? (
              <Empty title="Query returned no records" />
            ) : effective === 'chart' ? (
              ts ? (
                <div className="p-3 pl-1">
                  <TimeChart x={ts.x} series={ts.series} height={320} format={fmtCompact} />
                  {ts.series.length > 1 && (
                    <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 pl-3 text-xs text-ink-2">
                      {ts.series.map((s) => (
                        <span key={s.label} className="flex items-center gap-1.5">
                          <i className="inline-block h-0.5 w-3 rounded" style={{ background: `var(${s.color})` }} />
                          {s.label}
                        </span>
                      ))}
                      {ts.more > 0 && <span className="text-ink-3">+{ts.more} more series (see table)</span>}
                    </div>
                  )}
                </div>
              ) : (
                <Empty title="Not a timeseries" hint="Charts need a timeseries/makeTimeseries result. Switch to table." />
              )
            ) : (
              <ResultTable records={records} types={res.data?.types} onOpen={setSel} />
            )}
          </div>
        </div>
      </div>
      {sel && (
        <SidePanel title="Record" onClose={() => setSel(null)}>
          <Inspector rec={sel} />
        </SidePanel>
      )}
    </div>
  )
}

function Examples({ history, onPick, onClear }: { history: string[]; onPick: (q: string) => void; onClear: () => void }) {
  return (
    <div className="grid grid-cols-2 gap-6 overflow-auto p-4 max-xl:grid-cols-1">
      <div>
        <div className="mb-2 flex items-center gap-2 text-2xs font-medium tracking-wide text-ink-3 uppercase">
          <Clock className="size-3" /> Recent queries
          {history.length > 0 && (
            <Tip content="Clear history">
              <button type="button" onClick={onClear} className="ml-auto rounded p-0.5 hover:bg-line">
                <Trash2 className="size-3" />
              </button>
            </Tip>
          )}
        </div>
        {history.length === 0 && <div className="text-sm text-ink-3">Queries you run are remembered here (in this browser).</div>}
        {history.slice(0, 8).map((q) => (
          <button key={q} type="button" onClick={() => onPick(q)} className="mb-1.5 block w-full rounded-lg border border-line p-2 text-left hover:border-line-strong hover:bg-panel-hover">
            <pre className="line-clamp-3 font-mono text-xs whitespace-pre-wrap text-ink-2">{highlightDql(q)}</pre>
          </button>
        ))}
      </div>
      <div>
        <div className="mb-2 text-2xs font-medium tracking-wide text-ink-3 uppercase">Examples</div>
        {EXAMPLES.map((q) => (
          <button key={q} type="button" onClick={() => onPick(q)} className="mb-1.5 block w-full rounded-lg border border-line p-2 text-left hover:border-line-strong hover:bg-panel-hover">
            <pre className="font-mono text-xs whitespace-pre-wrap text-ink-2">{highlightDql(q)}</pre>
          </button>
        ))}
      </div>
    </div>
  )
}

function detectTimeseries(records: Rec[] | undefined) {
  if (!records?.length) return null
  const r0 = records[0]
  if (!r0.timeframe || r0.interval == null) return null
  const arrayKeys = Object.keys(r0).filter((k) => Array.isArray(r0[k]) && r0[k].every((v: unknown) => v == null || typeof v === 'number'))
  if (!arrayKeys.length) return null
  const dimKeys = Object.keys(r0).filter((k) => !arrayKeys.includes(k) && k !== 'timeframe' && k !== 'interval')
  const x = tsAxis(r0, arrayKeys[0])
  const all = records.flatMap((r) =>
    arrayKeys.map((k) => ({
      label: [arrayKeys.length > 1 ? k : '', ...dimKeys.map((d) => String(r[d] ?? 'null'))].filter(Boolean).join(' · ') || k,
      values: (r[k] as (number | null)[]).map((v) => (v == null ? null : num(v))),
    })),
  )
  // ≤ 8 series, colors in fixed order; the rest stays in the table view.
  const series = all.slice(0, 8).map((s, i) => ({ ...s, color: SERIES[i] }))
  return { x, series, more: all.length - series.length }
}

function ResultTable({ records, types, onOpen }: { records: Rec[]; types?: Record<string, string>; onOpen: (r: Rec) => void }) {
  const columns: Column[] = useMemo(() => {
    const keys: string[] = []
    const seen = new Set<string>()
    for (const r of records.slice(0, 200))
      for (const k of Object.keys(r))
        if (!seen.has(k)) {
          seen.add(k)
          keys.push(k)
        }
    return keys.slice(0, 40).map((k) => {
      const t = types?.[k]
      const numeric = t === 'long' || t === 'double'
      return {
        key: k,
        header: k,
        width: k === 'content' ? 'minmax(320px,4fr)' : numeric ? 'minmax(80px,0.6fr)' : 'minmax(120px,1fr)',
        align: numeric ? 'right' : 'left',
        render: (r: Rec) => <Cell v={r[k]} type={t} />,
        sort: (r: Rec) => (numeric ? num(r[k]) : r[k] == null ? null : typeof r[k] === 'object' ? JSON.stringify(r[k]) : String(r[k])),
      } as Column
    })
  }, [records, types])
  return <DataTable rows={records} columns={columns} rowKey={(_, i) => String(i)} onOpen={onOpen} className="flex-1" />
}

function Cell({ v, type }: { v: unknown; type?: string }) {
  if (v == null) return <span className="text-ink-4">null</span>
  if (type === 'timestamp') return <span className="tnum font-mono text-xs text-ink-2" title={fmtAbs(v)}>{fmtTime(v)}</span>
  if (type === 'long' || type === 'double') return <span className="font-mono text-xs">{fmtCompact(num(v))}</span>
  if (Array.isArray(v)) return <span className="font-mono text-xs text-ink-3">[{v.length}] {JSON.stringify(v).slice(0, 80)}</span>
  if (typeof v === 'object') return <span className="font-mono text-xs text-ink-3">{JSON.stringify(v).slice(0, 120)}</span>
  const s = String(v)
  if (/^[A-Z][A-Z0-9_]+-[0-9A-F]{16}$/.test(s)) return <EntityLink id={s} className="text-xs" />
  if (typeof v === 'boolean') return <span className={clsx('font-mono text-xs', v ? 'text-ok' : 'text-ink-3')}>{s}</span>
  return <span className={clsx(type === 'string' && s.length > 40 && 'font-mono text-xs')}>{s}</span>
}
