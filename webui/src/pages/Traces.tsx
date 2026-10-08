import clsx from 'clsx'
import { AlertTriangle, ArrowLeft, ExternalLink, Waypoints } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { Link, useLocation, useSearch } from 'wouter'
import { DataTabs } from '../components/DataTabs'
import { fmtTokens } from '../lib/ai'
import { LlmCallPanel } from './Ai'
import { FilterInput, PageHeader, Panel } from '../components/Panel'
import { EntityLink } from '../components/Entity'
import { Inspector, LogStream, SidePanel, SpanTable, spanFailed } from '../components/signals'
import { Badge, CopyButton, Empty, ErrorBox, Segmented, Skeleton, TimeAgo } from '../components/ui'
import { num, useDql, useMeta, type Rec } from '../lib/api'
import { q, SPAN_LENSES, spansQuery, traceQuery } from '../lib/dql'
import { fmtMs, fmtNs } from '../lib/format'
import { dtLinks } from '../lib/links'
import { tfSpec } from '../lib/shared'
import { pushRecent, useTitle } from '../lib/store'
import { useTimeframe } from '../lib/timeframe'

export default function Traces() {
  useTitle('Traces')
  const tf = useTimeframe()
  const params = new URLSearchParams(useSearch())
  const [, navigate] = useLocation()
  const lens = params.get('lens') ?? 'roots'
  const [text, setText] = useState(params.get('q') ?? '')
  const [debounced, setDebounced] = useState(text)
  useEffect(() => {
    const t = setTimeout(() => setDebounced(text.trim()), 350)
    return () => clearTimeout(t)
  }, [text])
  const extra = debounced
    ? [`contains(span.name, ${q(debounced)}, caseSensitive:false) or contains(coalesce(dt.service.name, service.name, ""), ${q(debounced)}, caseSensitive:false) or contains(coalesce(endpoint.name, ""), ${q(debounced)}, caseSensitive:false)`]
    : []
  const spec = tfSpec(tf, spansQuery(lens, extra, 500))
  const res = useDql(spec)
  const rows = res.data?.records
  const failed = rows?.filter(spanFailed).length ?? 0

  return (
    <div className="flex h-full flex-col p-5">
      <PageHeader
        title="Traces"
        icon={<Waypoints className="size-5" />}
        sub={rows ? `${rows.length}${rows.length >= 500 ? '+' : ''} spans · ${failed} failed · ${tf.label.toLowerCase()}` : 'Distributed traces'}
        actions={
          <>
            <Segmented
              value={lens}
              onChange={(l) => navigate(`/traces?lens=${l}`, { replace: true })}
              options={SPAN_LENSES.map((l) => ({ value: l.key, label: l.label }))}
            />
            <FilterInput value={text} onChange={setText} placeholder="Span, service or endpoint…" className="w-64" />
          </>
        }
      />
      <Panel spec={spec} result={res} className="min-h-0 flex-1" bodyClassName="flex min-h-0 flex-col" title={SPAN_LENSES.find((l) => l.key === lens)?.label}>
        {res.error ? <ErrorBox error={res.error} /> : <SpanTable records={rows} loading={res.isLoading} className="flex-1" />}
      </Panel>
    </div>
  )
}

interface Node {
  rec: Rec
  depth: number
  start: number
  end: number
  failed: boolean
  children: number
}

function buildTree(spans: Rec[]): Node[] {
  const byId = new Map<string, Rec>()
  const kids = new Map<string, Rec[]>()
  for (const s of spans) byId.set(s['span.id'], s)
  const roots: Rec[] = []
  for (const s of spans) {
    const p = s['span.parent_id']
    if (p && byId.has(p)) {
      if (!kids.has(p)) kids.set(p, [])
      kids.get(p)!.push(s)
    } else roots.push(s)
  }
  const out: Node[] = []
  // Milliseconds as float: ns-since-epoch would overflow 2^53.
  const t = (s: Rec) => {
    const str = String(s.start_time)
    const m = /\.(\d+)Z$/.exec(str)
    const frac = m ? Number((m[1] + '000000000').slice(0, 9)) : 0
    return Date.parse(str.replace(/\.\d+Z$/, 'Z')) + frac / 1e6
  }
  const walk = (s: Rec, depth: number) => {
    const start = t(s)
    const children = (kids.get(s['span.id']) ?? []).sort((a, b) => t(a) - t(b))
    out.push({ rec: s, depth, start, end: start + num(s.duration) / 1e6, failed: spanFailed(s), children: children.length })
    for (const c of children) walk(c, depth + 1)
  }
  roots.sort((a, b) => t(a) - t(b)).forEach((r) => walk(r, 0))
  return out
}

export function Trace({ id }: { id: string }) {
  const { data: meta } = useMeta()
  const spec = { query: traceQuery(id), ttl: 300 }
  const res = useDql(spec)
  const spans = res.data?.records ?? []
  const nodes = useMemo(() => buildTree(spans), [spans])
  const [sel, setSel] = useState<Rec | null>(null)
  const [tab, setTab] = useState<'waterfall' | 'logs'>('waterfall')
  const root = nodes[0]?.rec
  useTitle(root ? `Trace · ${root['span.name']}` : 'Trace')
  useEffect(() => {
    if (root) pushRecent({ href: `/traces/${id}`, label: String(root['endpoint.name'] ?? root['span.name']), kind: 'Trace' })
  }, [root, id])

  const t0 = nodes.length ? Math.min(...nodes.map((n) => n.start)) : 0
  const t1 = nodes.length ? Math.max(...nodes.map((n) => n.end)) : 1
  const total = Math.max(1, t1 - t0)
  const nFailed = nodes.filter((n) => n.failed).length
  // GenAI traces: summarize the AI work and link to its conversation(s).
  const ai = useMemo(() => {
    const llm = spans.filter((s) => s['gen_ai.operation.name'] === 'chat' && (s['gen_ai.request.model'] || s['gen_ai.usage.input_tokens'] != null))
    const tools = spans.filter((s) => s['gen_ai.operation.name'] === 'execute_tool')
    const convs = [...new Set(spans.map((s) => s['gen_ai.conversation.id']).filter(Boolean))] as string[]
    const tokens = llm.reduce((a, s) => a + num(s['gen_ai.usage.input_tokens']) + num(s['gen_ai.usage.output_tokens']), 0)
    return { llm: llm.length, tools: tools.length, convs, tokens }
  }, [spans])
  const services = useMemo(() => {
    const m = new Map<string, string>()
    for (const s of spans) if (s['dt.smartscape.service']) m.set(s['dt.smartscape.service'], s['dt.service.name'] ?? s['service.name'])
    return [...m.entries()]
  }, [spans])

  const logsSpec = { query: `fetch logs, from:now()-7d\n| filter trace_id == ${q(id)}\n| sort timestamp asc\n| limit 500`, ttl: 120 }
  const logs = useDql(logsSpec)

  return (
    <div className="flex h-full">
      <div className="flex min-w-0 flex-1 flex-col p-5">
        <Link href="/traces" className="mb-3 inline-flex items-center gap-1 text-xs text-ink-3 hover:text-ink-2">
          <ArrowLeft className="size-3.5" /> Traces
        </Link>
        {res.error ? (
          <ErrorBox error={res.error} />
        ) : res.isLoading ? (
          <>
            <Skeleton className="mb-3 h-7 w-96" />
            <Skeleton className="h-96" />
          </>
        ) : !nodes.length ? (
          <Empty title="Trace not found" hint="No spans with this trace ID in the last 7 days." />
        ) : (
          <>
            <div className="mb-4 flex flex-wrap items-start gap-4">
              <div className={clsx('flex size-10 items-center justify-center rounded-xl', nFailed ? 'bg-crit-wash text-crit' : 'bg-accent-wash text-accent-ink')}>
                <Waypoints className="size-5" />
              </div>
              <div className="min-w-0 flex-1">
                <h1 className="truncate text-xl font-semibold tracking-tight">{root['endpoint.name'] ?? root['span.name']}</h1>
                <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-ink-3">
                  <span className="inline-flex items-center gap-1 font-mono text-xs">
                    {id}
                    <CopyButton value={id} label="trace ID" />
                  </span>
                  <span className="tnum text-ink">{fmtMs(total)}</span>
                  <span>{nodes.length} spans</span>
                  {nFailed > 0 && (
                    <span className="inline-flex items-center gap-1 text-crit">
                      <AlertTriangle className="size-3.5" /> {nFailed} failed
                    </span>
                  )}
                  <TimeAgo value={root.start_time} />
                  {ai.llm + ai.tools > 0 && (
                    <span className="inline-flex items-center gap-2 rounded-md bg-[var(--s7)]/10 px-2 py-0.5 text-xs text-[var(--s7)]">
                      ✦ {ai.llm} LLM calls · ⚙ {ai.tools} tool calls · {fmtTokens(ai.tokens)} tokens
                      {ai.convs.slice(0, 2).map((c) => (
                        <Link key={c} href={`/ai/conversations/${c}`} className="font-medium underline-offset-2 hover:underline">
                          replay conversation →
                        </Link>
                      ))}
                    </span>
                  )}
                </div>
                <div className="mt-2 flex flex-wrap gap-1.5">
                  {services.map(([sid, name]) => (
                    <EntityLink key={sid} id={sid} name={name} type="SERVICE" className="h-6 rounded-md border border-line bg-sunken px-2 text-xs hover:no-underline" />
                  ))}
                </div>
              </div>
              {meta?.environment && (
                <a
                  href={dtLinks.trace(meta.environment, id)}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-line bg-sunken px-3 text-sm text-ink-2 hover:border-line-strong hover:text-ink"
                >
                  Open in Dynatrace <ExternalLink className="size-3.5" />
                </a>
              )}
            </div>

            <div className="flex min-h-0 flex-1 flex-col rounded-xl border border-line bg-panel">
              <DataTabs
                value={tab}
                onChange={setTab}
                tabs={[
                  { value: 'waterfall', label: 'Waterfall', spec, result: res, count: nodes.length, limit: 1000 },
                  { value: 'logs', label: 'Logs', spec: logsSpec, result: logs, limit: 500 },
                ]}
                right={
                  tab === 'waterfall' && (
                  <div className="flex items-center gap-3 text-xs text-ink-3">
                    {[
                      ['server / internal', 'var(--s1)'],
                      ['client', 'var(--s7)'],
                      ['database', 'var(--s3)'],
                      ['failed', 'var(--crit)'],
                    ].map(([l, c]) => (
                      <span key={l} className="flex items-center gap-1.5">
                        <i className="inline-block h-2 w-3 rounded-[2px]" style={{ background: c }} />
                        {l}
                      </span>
                    ))}
                  </div>
                  )
                }
              />
              {tab === 'waterfall' ? (
                <div className="min-h-0 flex-1 overflow-auto">
                  <div className="sticky top-0 z-[1] grid grid-cols-[minmax(280px,38%)_1fr_80px] gap-3 border-b border-line bg-panel px-3 py-1.5 text-2xs font-medium tracking-wide text-ink-3 uppercase">
                    <span>Span</span>
                    <span className="flex justify-between">
                      <span>0</span>
                      <span>{fmtMs(total / 2)}</span>
                      <span>{fmtMs(total)}</span>
                    </span>
                    <span className="text-right">Duration</span>
                  </div>
                  {nodes.map((n) => {
                    const left = ((n.start - t0) / total) * 100
                    const width = Math.max(0.3, ((n.end - n.start) / total) * 100)
                    const on = sel === n.rec
                    return (
                      <button
                        type="button"
                        key={n.rec['span.id']}
                        onClick={() => setSel(on ? null : n.rec)}
                        className={clsx(
                          'grid w-full grid-cols-[minmax(280px,38%)_1fr_80px] items-center gap-3 border-b border-line px-3 py-1 text-left text-sm hover:bg-panel-hover',
                          on && 'bg-accent-wash! shadow-[inset_2px_0_0_var(--accent)]',
                        )}
                      >
                        <span className="flex min-w-0 items-center gap-1.5" style={{ paddingLeft: n.depth * 14 }}>
                          {n.failed ? <span className="size-2 shrink-0 rounded-full bg-crit" /> : <span className="size-2 shrink-0 rounded-full bg-ink-4" />}
                          <span className="truncate">{n.rec['span.name']}</span>
                          <GenAiBadge r={n.rec} />
                          <span className="shrink-0 truncate text-2xs text-ink-3">{n.rec['dt.service.name'] ?? n.rec['service.name']}</span>
                        </span>
                        <span className="relative h-5">
                          <span
                            className={clsx('absolute top-1 h-3 rounded-[3px]', n.failed ? 'bg-crit' : spanColor(n.rec))}
                            style={{ left: `${left}%`, width: `${width}%`, minWidth: 2 }}
                          />
                        </span>
                        <span className="tnum text-right font-mono text-xs text-ink-2">{fmtMs(n.end - n.start)}</span>
                      </button>
                    )
                  })}
                </div>
              ) : logs.error ? (
                <ErrorBox error={logs.error} />
              ) : (
                <LogStream records={logs.data?.records} loading={logs.isLoading} className="min-h-0 flex-1" />
              )}
            </div>
          </>
        )}
      </div>
      {sel && sel['gen_ai.operation.name'] === 'chat' ? (
        <LlmCallPanel traceId={sel['trace.id']} spanId={sel['span.id']} onClose={() => setSel(null)} />
      ) : sel && (
        <SidePanel
          title={sel['span.name']}
          onClose={() => setSel(null)}
          actions={
            <Badge tone={spanFailed(sel) ? 'crit' : 'muted'}>
              {sel['span.kind']} · {fmtNs(num(sel.duration))}
            </Badge>
          }
        >
          <SpanEvents rec={sel} />
          <Inspector rec={sel} hide={['span.events']} />
        </SidePanel>
      )}
    </div>
  )
}

/** GenAI-aware waterfall: LLM calls show their tokens, tools and agents are labelled. */
function GenAiBadge({ r }: { r: Rec }) {
  const op = r['gen_ai.operation.name']
  if (!op) return null
  if (op === 'chat')
    return (
      <span className="inline-flex shrink-0 items-center gap-1 rounded bg-[var(--s7)]/15 px-1 text-2xs text-[var(--s7)]">
        ✦ LLM {fmtTokens(num(r['gen_ai.usage.input_tokens']))}→{fmtTokens(num(r['gen_ai.usage.output_tokens']))}
      </span>
    )
  if (op === 'execute_tool') return <span className="shrink-0 rounded bg-[var(--s3)]/15 px-1 text-2xs text-[var(--s3)]">⚙ {r['gen_ai.tool.name'] ?? 'tool'}</span>
  if (op === 'invoke_agent') return <span className="shrink-0 rounded bg-accent-wash px-1 text-2xs text-accent-ink">◈ {r['gen_ai.agent.name'] ?? 'agent'}</span>
  return <span className="shrink-0 rounded bg-line px-1 text-2xs text-ink-3">{op}</span>
}

function spanColor(r: Rec) {
  if (r['db.system.name'] || r['db.system']) return 'bg-[var(--s3)]'
  if (r['span.kind'] === 'client') return 'bg-[var(--s7)]'
  if (r['gen_ai.operation.name'] === 'chat') return 'bg-[var(--s7)]'
  if (r['gen_ai.operation.name']) return 'bg-[var(--s5)]'
  return 'bg-[var(--s1)]'
}

function SpanEvents({ rec }: { rec: Rec }) {
  const events: Rec[] = Array.isArray(rec['span.events']) ? rec['span.events'] : []
  const ex = events.filter((e) => e['span_event.name'] === 'exception')
  if (!ex.length) return null
  return (
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
  )
}

