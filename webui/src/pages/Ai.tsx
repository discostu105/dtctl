import clsx from 'clsx'
import { Bot, CheckCircle2, ExternalLink, Gauge, Sparkles, Wrench, X, XCircle } from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'
import { Link, useLocation, useSearch } from 'wouter'
import { Legend, SERIES, TimeChart, tsAxis } from '../components/Chart'
import { Conversation, parseMessages } from '../components/Conversation'
import { DataTable, type Column } from '../components/DataTable'
import { EntityLink } from '../components/Entity'
import { FilterInput, PageHeader, Panel } from '../components/Panel'
import { Inspector, SidePanel } from '../components/signals'
import { Badge, Empty, ErrorBox, Facts, Skeleton, SkeletonRows, Tabs, TimeAgo, Tip } from '../components/ui'
import { arr, num, useDql, type DqlSpec, type Rec } from '../lib/api'
import {
  agentsQuery, aiKpiQuery, callDetailQuery, callsByModelSeries, evalSummaryQuery, evalsQuery, fmtTokens, modelsQuery, recentCallsQuery, toolsQuery,
  ttftSeries,
} from '../lib/ai'
import { q } from '../lib/dql'
import { fmtCompact, fmtDateTime, fmtMs, fmtPct } from '../lib/format'
import { traceHref } from '../lib/links'
import { tfSpec } from '../lib/shared'
import { useTitle } from '../lib/store'
import { absolute, floorTf, intervalFor, setTimeframe, useTimeframe, type Timeframe } from '../lib/timeframe'

type Tab = 'calls' | 'models' | 'agents' | 'tools' | 'evals'

export const aiKpiSpec = (tf: Timeframe): DqlSpec => tfSpec(tf, aiKpiQuery(), { ttl: 30 })
// Evaluations typically run as nightly CI batches: look back at least 7 days.
const evalTf = (tf: Timeframe) => floorTf(tf, '7d')

const secs = (s: number) => (Number.isFinite(s) ? (s < 1 ? `${Math.round(s * 1000)} ms` : `${s.toFixed(s < 10 ? 2 : 1)} s`) : '—')

export default function Ai() {
  useTitle('AI')
  const tf = useTimeframe()
  const params = new URLSearchParams(useSearch())
  const [, navigate] = useLocation()
  const tab = (params.get('tab') as Tab) || 'calls'
  const model = params.get('model')
  const set = (mut: (p: URLSearchParams) => void) => {
    const p = new URLSearchParams(location.search)
    mut(p)
    navigate(`/ai?${p}`, { replace: true })
  }

  const kpi = useDql(aiKpiSpec(tf))
  const k = kpi.data?.records[0]
  const iv = intervalFor(tf.ms)
  const callsSpec = tfSpec(tf, callsByModelSeries(iv))
  const calls = useDql(callsSpec)
  const ttftSpec = tfSpec(tf, ttftSeries(iv))
  const ttft = useDql(ttftSpec)
  const evalSum = useDql(tfSpec(evalTf(tf), evalSummaryQuery(), { ttl: 120 }))

  const callChart = useMemo(() => {
    const recs = [...(calls.data?.records ?? [])].sort((a, b) => sum(b.calls) - sum(a.calls))
    if (!recs.length) return null
    const x = tsAxis(recs[0], 'calls')
    return { x, series: recs.slice(0, 6).map((r, i) => ({ label: String(r.model ?? 'unknown'), values: r.calls as number[], color: SERIES[i], fill: false })) }
  }, [calls.data])
  const ttftChart = useMemo(() => {
    const r = ttft.data?.records[0]
    if (!r) return null
    return {
      x: tsAxis(r, 'p50'),
      series: [
        { label: 'p50', values: r.p50 as number[], color: '--s1' },
        { label: 'p90', values: r.p90 as number[], color: '--s2', fill: false },
      ],
    }
  }, [ttft.data])

  const evalPass = useMemo(() => {
    const rs = evalSum.data?.records ?? []
    const n = rs.reduce((a, r) => a + num(r.n), 0)
    const p = rs.reduce((a, r) => a + num(r.passed), 0)
    return n ? { rate: (100 * p) / n, n } : null
  }, [evalSum.data])

  const noData = k && num(k.chats) + num(k.tools) + num(k.agents) === 0

  return (
    <div className="mx-auto flex max-w-[1600px] flex-col p-5">
      <PageHeader title="AI" icon={<Sparkles className="size-5" />} sub={`LLM calls, agents, tools and evaluations · ${tf.label.toLowerCase()}`} />

      {noData ? (
        <Empty
          icon={<Sparkles className="size-5" />}
          title="No GenAI telemetry in this timeframe"
          hint="This view reads OpenTelemetry GenAI spans (gen_ai.operation.name) and gen_ai.evaluation.result events. Try a wider timeframe."
        />
      ) : (
        <>
          <div className="mb-4 grid grid-cols-6 gap-3 max-xl:grid-cols-3">
            <Kpi label="LLM calls" value={k && fmtCompact(num(k.chats))} sub={k && (num(k.chat_fail) ? <span className="text-crit">{num(k.chat_fail)} failed</span> : 'none failed')} />
            <Kpi label="Tokens" value={k && fmtTokens(num(k.input) + num(k.output))} sub={k && `${fmtTokens(num(k.input))} in · ${fmtTokens(num(k.output))} out`} />
            <Kpi
              label="Prompt cache hits"
              value={k && (num(k.input) ? fmtPct((100 * num(k.cache_read)) / num(k.input)) : '—')}
              sub="of input tokens"
            />
            <Kpi label="Time to first token" value={k && secs(num(k.ttft_p50))} sub={k && `p50 · p90 ${secs(num(k.ttft_p90))}`} />
            <Kpi
              label="Tool calls"
              value={k && fmtCompact(num(k.tools))}
              sub={k && (num(k.tools) ? <span className={clsx(num(k.tool_fail) && 'text-warn')}>{fmtPct((100 * num(k.tool_fail)) / num(k.tools), 2)} failed</span> : '—')}
            />
            <Kpi
              label="Eval pass rate"
              value={evalPass ? fmtPct(evalPass.rate, 0) : evalSum.isLoading ? undefined : '—'}
              sub={evalPass ? `${evalPass.n} evaluations · ${evalTf(tf).label.toLowerCase()}` : 'no evaluations'}
              tone={evalPass && evalPass.rate < 50 ? 'warn' : undefined}
            />
          </div>

          <div className="mb-4 grid grid-cols-2 gap-4 max-xl:grid-cols-1">
            <Panel title="LLM calls by model" spec={callsSpec} result={calls} hint="drag to zoom">
              {calls.error ? (
                <ErrorBox error={calls.error} />
              ) : !callChart ? (
                <Skeleton className="m-3 h-[170px]" />
              ) : (
                <div className="p-3 pl-1">
                  <TimeChart x={callChart.x} series={callChart.series} height={170} format={fmtCompact} syncKey="ai" onZoom={(a, b) => setTimeframe(absolute(a, b))} />
                  <Legend className="mt-2 pl-3" items={callChart.series.map((s) => ({ label: s.label, color: s.color! }))} />
                </div>
              )}
            </Panel>
            <Panel title="Time to first token" spec={ttftSpec} result={ttft} hint="server-reported">
              {ttft.error ? (
                <ErrorBox error={ttft.error} />
              ) : !ttftChart ? (
                <Skeleton className="m-3 h-[170px]" />
              ) : (
                <div className="p-3 pl-1">
                  <TimeChart x={ttftChart.x} series={ttftChart.series} height={170} format={secs} syncKey="ai" onZoom={(a, b) => setTimeframe(absolute(a, b))} />
                  <Legend className="mt-2 pl-3" items={ttftChart.series.map((s) => ({ label: s.label, color: s.color }))} />
                </div>
              )}
            </Panel>
          </div>

          <div className="flex min-h-[560px] flex-col rounded-xl border border-line bg-panel">
            <div className="flex items-center border-b border-line pr-3">
              <Tabs
                className="border-b-0 px-2"
                value={tab}
                onChange={(t) => set((p) => (t === 'calls' ? p.delete('tab') : p.set('tab', t)))}
                tabs={[
                  { value: 'calls', label: 'LLM calls' },
                  { value: 'models', label: 'Models' },
                  { value: 'agents', label: 'Agents' },
                  { value: 'tools', label: 'Tools' },
                  { value: 'evals', label: 'Evaluations' },
                ]}
              />
              {model && tab === 'calls' && (
                <button
                  type="button"
                  onClick={() => set((p) => p.delete('model'))}
                  className="ml-auto inline-flex h-6 max-w-80 items-center gap-1 rounded-md bg-accent-wash px-2 text-xs text-accent-ink hover:brightness-110"
                >
                  <span className="truncate font-mono">{model}</span>
                  <X className="size-3" />
                </button>
              )}
            </div>
            <div className="flex min-h-0 flex-1 flex-col">
              {tab === 'calls' && <CallsView tf={tf} model={model} />}
              {tab === 'models' &&
                <ModelsView
                  tf={tf}
                  onPick={(m) =>
                    set((p) => {
                      p.delete('tab')
                      p.set('model', m)
                    })
                  }
                />}
              {tab === 'agents' && <AgentsView tf={tf} />}
              {tab === 'tools' && <ToolsView tf={tf} />}
              {tab === 'evals' && <EvalsView tf={tf} />}
            </div>
          </div>
        </>
      )}
    </div>
  )
}

const sum = (a: unknown) => arr(a).reduce((x: number, y: unknown) => x + (num(y) || 0), 0)

function Kpi({ label, value, sub, tone }: { label: string; value: ReactNode; sub?: ReactNode; tone?: 'warn' }) {
  return (
    <div className="rounded-xl border border-line bg-panel p-3.5">
      <div className="text-xs text-ink-3">{label}</div>
      {value == null ? <Skeleton className="mt-1.5 h-7 w-20" /> : <div className={clsx('tnum mt-1 text-[24px] leading-none font-semibold tracking-tight', tone === 'warn' && 'text-warn')}>{value}</div>}
      <div className="mt-1.5 truncate text-xs text-ink-3">{sub}</div>
    </div>
  )
}

// ── LLM calls ───────────────────────────────────────────────────────────

function CallsView({ tf, model }: { tf: Timeframe; model: string | null }) {
  const res = useDql(tfSpec(tf, recentCallsQuery(model ? `gen_ai.request.model == ${q(model)}` : undefined), { ttl: 20 }))
  const [sel, setSel] = useState<Rec | null>(null)
  const cols: Column[] = [
    { key: 'time', header: 'Time', width: '130px', render: (r) => <span className="tnum text-xs text-ink-2">{fmtDateTime(r.start_time)}</span>, sort: (r) => r.start_time },
    {
      key: 'model',
      header: 'Model',
      width: 'minmax(200px,1.6fr)',
      render: (r) => (
        <span className="flex min-w-0 items-center gap-2">
          <span className={clsx('size-2 shrink-0 rounded-full', r['span.status_code'] === 'error' ? 'bg-crit' : 'bg-ok/70')} />
          <span className="truncate font-mono text-xs">{r.model}</span>
        </span>
      ),
      sort: (r) => r.model,
    },
    { key: 'service', header: 'Service', width: 'minmax(140px,1fr)', render: (r) => (r.service_id ? <EntityLink id={r.service_id} name={r.service} type="SERVICE" /> : <span className="text-ink-2">{r.service}</span>), sort: (r) => r.service },
    { key: 'in', header: 'Input', width: '72px', align: 'right', render: (r) => fmtTokens(num(r.input)), sort: (r) => num(r.input) },
    {
      key: 'cache',
      header: 'Cached',
      width: '64px',
      align: 'right',
      render: (r) => <span className="text-ink-3">{num(r.input) ? fmtPct((100 * num(r.cached)) / num(r.input), 0) : '—'}</span>,
      sort: (r) => num(r.cached) / (num(r.input) || 1),
    },
    { key: 'out', header: 'Output', width: '68px', align: 'right', render: (r) => fmtTokens(num(r.output)), sort: (r) => num(r.output) },
    { key: 'ttft', header: 'TTFT', width: '72px', align: 'right', render: (r) => secs(num(r.ttft)), sort: (r) => num(r.ttft) },
    { key: 'dur', header: 'Duration', width: '80px', align: 'right', render: (r) => fmtMs(num(r.duration) / 1e6), sort: (r) => num(r.duration) },
    { key: 'finish', header: 'Finish', width: '96px', render: (r) => <span className="text-xs text-ink-3">{arr(r.finish).join(', ')}</span>, sort: (r) => arr(r.finish)[0] },
  ]
  return (
    <div className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">
        {res.error ? (
          <ErrorBox error={res.error} />
        ) : (
          <DataTable
            rows={res.data?.records}
            loading={res.isLoading}
            columns={cols}
            rowKey={(r) => `${r['trace.id']}${r['span.id']}`}
            onOpen={setSel}
            selectedKey={sel ? `${sel['trace.id']}${sel['span.id']}` : null}
            initialSort={{ key: 'time', dir: 'desc' }}
            className="flex-1"
            maxHeight={620}
          />
        )}
      </div>
      {sel && <CallPanel call={sel} onClose={() => setSel(null)} />}
    </div>
  )
}

function CallPanel({ call, onClose }: { call: Rec; onClose: () => void }) {
  const res = useDql({ query: callDetailQuery(call['trace.id'], call['span.id']), ttl: 600 })
  const d = res.data?.records[0]
  const [view, setView] = useState<'conversation' | 'raw'>('conversation')
  const input = parseMessages(d?.['gen_ai.input.messages'])
  const output = parseMessages(d?.['gen_ai.output.messages'])
  const system = d?.['gen_ai.system_instructions']
  return (
    <SidePanel
      title={<span className="font-mono text-xs">{call.model}</span>}
      onClose={onClose}
      width="w-[min(680px,50vw)]"
      actions={
        <Link href={traceHref(call['trace.id'])} className="inline-flex items-center gap-1 rounded px-1.5 py-1 text-xs text-accent-ink hover:bg-accent-wash">
          Trace <ExternalLink className="size-3" />
        </Link>
      }
    >
      <Facts
        className="mb-4"
        items={[
          ['When', <>{fmtDateTime(call.start_time)} · <TimeAgo value={call.start_time} /></>],
          ['Provider', call.provider],
          ['Tokens', `${fmtTokens(num(call.input))} in (${fmtTokens(num(call.cached))} cached) · ${fmtTokens(num(call.output))} out`],
          ['Time to first token', secs(num(call.ttft))],
          ['Duration', fmtMs(num(call.duration) / 1e6)],
          ['Finish', arr(call.finish).join(', ')],
          ['Conversation', call.conversation && <span className="font-mono text-xs">{call.conversation}</span>],
        ]}
      />
      <div className="mb-3 flex gap-3 border-b border-line text-sm">
        {(['conversation', 'raw'] as const).map((v) => (
          <button key={v} type="button" onClick={() => setView(v)} className={clsx('-mb-px border-b-2 pb-1.5', view === v ? 'border-accent text-ink' : 'border-transparent text-ink-3 hover:text-ink-2')}>
            {v === 'conversation' ? 'Conversation' : 'All attributes'}
          </button>
        ))}
      </div>
      {res.isLoading ? (
        <SkeletonRows rows={6} />
      ) : !d ? (
        <Empty title="Span not found" />
      ) : view === 'raw' ? (
        <Inspector rec={d} />
      ) : (
        <div className="flex flex-col gap-5">
          {system && <Conversation label="System instructions" messages={[{ role: 'system', parts: parseMessages(system).flatMap((m: any) => (m.parts ? m.parts : [m])) }]} />}
          <Conversation label={`Input · ${input.length} message${input.length === 1 ? '' : 's'}`} messages={input} />
          <Conversation label="Output" messages={output} />
          {!input.length && !output.length && <Empty title="No message content captured" hint="The instrumentation didn't record gen_ai.input/output.messages for this call." />}
        </div>
      )}
    </SidePanel>
  )
}

// ── models / agents / tools ─────────────────────────────────────────────

function ModelsView({ tf, onPick }: { tf: Timeframe; onPick: (model: string) => void }) {
  const res = useDql(tfSpec(tf, modelsQuery(), { ttl: 30 }))
  const cols: Column[] = [
    {
      key: 'model',
      header: 'Model',
      width: 'minmax(240px,2fr)',
      render: (r) => (
        <span className="flex min-w-0 items-center gap-2">
          {r.model_id ? <EntityLink id={r.model_id} name={r.model} type="GENAI_MODEL" className="font-mono text-xs" /> : <span className="font-mono text-xs">{r.model}</span>}
        </span>
      ),
      sort: (r) => r.model,
    },
    { key: 'provider', header: 'Provider', width: '100px', render: (r) => <Badge>{r.provider ?? '—'}</Badge>, sort: (r) => r.provider },
    { key: 'calls', header: 'Calls', width: '72px', align: 'right', render: (r) => fmtCompact(num(r.calls)), sort: (r) => num(r.calls) },
    { key: 'in', header: 'Input tok', width: '84px', align: 'right', render: (r) => fmtTokens(num(r.input)), sort: (r) => num(r.input) },
    { key: 'out', header: 'Output tok', width: '84px', align: 'right', render: (r) => fmtTokens(num(r.output)), sort: (r) => num(r.output) },
    {
      key: 'cache',
      header: 'Cache hits',
      width: '84px',
      align: 'right',
      render: (r) => (num(r.input) ? fmtPct((100 * num(r.cache_read)) / num(r.input)) : '—'),
      sort: (r) => num(r.cache_read) / (num(r.input) || 1),
    },
    { key: 'ttft50', header: 'TTFT p50', width: '80px', align: 'right', render: (r) => secs(num(r.ttft_p50)), sort: (r) => num(r.ttft_p50) },
    { key: 'ttft90', header: 'TTFT p90', width: '80px', align: 'right', render: (r) => secs(num(r.ttft_p90)), sort: (r) => num(r.ttft_p90) },
    { key: 'dur90', header: 'Duration p90', width: '96px', align: 'right', render: (r) => fmtMs(num(r.dur_p90) / 1e6), sort: (r) => num(r.dur_p90) },
    {
      key: 'failed',
      header: 'Failed',
      width: '64px',
      align: 'right',
      render: (r) => <span className={clsx(num(r.failed) ? 'text-crit' : 'text-ink-4')}>{num(r.failed)}</span>,
      sort: (r) => num(r.failed),
    },
  ]
  if (res.error) return <ErrorBox error={res.error} />
  return (
    <>
      <div className="border-b border-line px-3 py-2 text-xs text-ink-3">Click a row to see that model's calls.</div>
      <DataTable rows={res.data?.records} loading={res.isLoading} columns={cols} rowKey={(r) => `${r.model}|${r.provider}`} onOpen={(r) => onPick(String(r.model))} initialSort={{ key: 'calls', dir: 'desc' }} className="flex-1" />
    </>
  )
}

function AgentsView({ tf }: { tf: Timeframe }) {
  const res = useDql(tfSpec(tf, agentsQuery(), { ttl: 30 }))
  const cols: Column[] = [
    {
      key: 'agent',
      header: 'Agent',
      width: 'minmax(200px,1.5fr)',
      render: (r) =>
        r.agent_id ? (
          <EntityLink id={r.agent_id} name={r.agent} type="GENAI_AGENT" />
        ) : (
          <span className="flex items-center gap-1.5">
            <Bot className="size-3.5 text-ink-3" />
            {r.agent ?? '(unnamed)'}
          </span>
        ),
      sort: (r) => r.agent,
    },
    { key: 'svc', header: 'Runs in', width: 'minmax(160px,1.5fr)', render: (r) => <span className="truncate text-ink-2">{arr(r.services).join(', ')}</span> },
    { key: 'runs', header: 'Runs', width: '72px', align: 'right', render: (r) => fmtCompact(num(r.runs)), sort: (r) => num(r.runs) },
    { key: 'p50', header: 'Duration p50', width: '100px', align: 'right', render: (r) => fmtMs(num(r.dur_p50) / 1e6), sort: (r) => num(r.dur_p50) },
    { key: 'p90', header: 'Duration p90', width: '100px', align: 'right', render: (r) => fmtMs(num(r.dur_p90) / 1e6), sort: (r) => num(r.dur_p90) },
    {
      key: 'failed',
      header: 'Failed',
      width: '64px',
      align: 'right',
      render: (r) => <span className={clsx(num(r.failed) ? 'text-crit' : 'text-ink-4')}>{num(r.failed)}</span>,
      sort: (r) => num(r.failed),
    },
    { key: 'last', header: 'Last run', width: '90px', align: 'right', render: (r) => <TimeAgo value={r.last} className="text-ink-2" />, sort: (r) => r.last },
  ]
  if (res.error) return <ErrorBox error={res.error} />
  return <DataTable rows={res.data?.records} loading={res.isLoading} columns={cols} rowKey={(r) => `${r.agent}|${r.agent_id}`} href={(r) => (r.agent_id ? `/e/${r.agent_id}?tab=traces` : '/ai?tab=agents')} initialSort={{ key: 'runs', dir: 'desc' }} className="flex-1" />
}

function ToolsView({ tf }: { tf: Timeframe }) {
  const res = useDql(tfSpec(tf, toolsQuery(), { ttl: 30 }))
  const [filter, setFilter] = useState('')
  const f = filter.toLowerCase()
  const rows = useMemo(() => res.data?.records.filter((r) => !f || String(r.tool).toLowerCase().includes(f)), [res.data, f])
  const cols: Column[] = [
    {
      key: 'tool',
      header: 'Tool',
      width: 'minmax(200px,2fr)',
      render: (r) => (
        <span className="flex items-center gap-1.5">
          <Wrench className="size-3.5 text-ink-3" />
          <span className="font-mono text-xs">{r.tool ?? '(unnamed)'}</span>
        </span>
      ),
      sort: (r) => r.tool,
    },
    { key: 'svc', header: 'Called from', width: 'minmax(160px,1.5fr)', render: (r) => <span className="truncate text-ink-2">{arr(r.services).join(', ')}</span> },
    { key: 'calls', header: 'Calls', width: '80px', align: 'right', render: (r) => fmtCompact(num(r.calls)), sort: (r) => num(r.calls) },
    {
      key: 'rate',
      header: 'Failure rate',
      width: '96px',
      align: 'right',
      render: (r) => {
        const rate = (100 * num(r.failed)) / (num(r.calls) || 1)
        return <span className={clsx(rate >= 5 ? 'text-crit' : rate > 0 ? 'text-warn' : 'text-ink-4')}>{fmtPct(rate, 2)}</span>
      },
      sort: (r) => num(r.failed) / (num(r.calls) || 1),
    },
    { key: 'p50', header: 'p50', width: '80px', align: 'right', render: (r) => fmtMs(num(r.dur_p50) / 1e6), sort: (r) => num(r.dur_p50) },
    { key: 'p90', header: 'p90', width: '80px', align: 'right', render: (r) => fmtMs(num(r.dur_p90) / 1e6), sort: (r) => num(r.dur_p90) },
  ]
  return (
    <>
      <div className="flex items-center border-b border-line px-3 py-2">
        <span className="text-xs text-ink-3">{rows ? `${rows.length} tools` : ''}</span>
        <FilterInput value={filter} onChange={setFilter} placeholder="Filter tools…" className="ml-auto w-64" />
      </div>
      {res.error ? <ErrorBox error={res.error} /> : <DataTable rows={rows} loading={res.isLoading} columns={cols} rowKey={(r) => String(r.tool)} initialSort={{ key: 'calls', dir: 'desc' }} className="flex-1" />}
    </>
  )
}

// ── evaluations ─────────────────────────────────────────────────────────

interface Criterion {
  criterion?: string
  fulfilled?: boolean
  reason?: string
}

function parseCriteria(v: unknown): Criterion[] {
  if (Array.isArray(v)) return v
  try {
    const c = JSON.parse(String(v ?? '[]'))
    return Array.isArray(c) ? c : []
  } catch {
    return []
  }
}

function EvalsView({ tf }: { tf: Timeframe }) {
  const etf = evalTf(tf)
  const res = useDql(tfSpec(etf, evalsQuery(), { ttl: 60 }))
  const sumRes = useDql(tfSpec(etf, evalSummaryQuery(), { ttl: 120 }))
  const [sel, setSel] = useState<Rec | null>(null)
  const [label, setLabel] = useState<'all' | 'pass' | 'fail'>('all')
  const rows = useMemo(() => res.data?.records.filter((r) => label === 'all' || r.label === label), [res.data, label])
  const cols: Column[] = [
    { key: 'time', header: 'Time', width: '130px', render: (r) => <span className="tnum text-xs text-ink-2">{fmtDateTime(r.timestamp)}</span>, sort: (r) => r.timestamp },
    {
      key: 'label',
      header: 'Result',
      width: '70px',
      render: (r) =>
        r.label === 'pass' ? (
          <Badge tone="ok">
            <CheckCircle2 className="size-3" /> pass
          </Badge>
        ) : (
          <Badge tone="crit">
            <XCircle className="size-3" /> {r.label ?? 'fail'}
          </Badge>
        ),
      sort: (r) => r.label,
    },
    {
      key: 'score',
      header: 'Score',
      width: '110px',
      align: 'right',
      render: (r) => {
        const s = num(r.score)
        return (
          <span className="flex items-center justify-end gap-2">
            <span className="h-1.5 w-12 overflow-hidden rounded-full bg-line">
              <span className={clsx('block h-full rounded-full', s >= 0.8 ? 'bg-ok' : s >= 0.5 ? 'bg-warn' : 'bg-crit')} style={{ width: `${Math.max(0, Math.min(1, s)) * 100}%` }} />
            </span>
            <span className="w-9">{Number.isFinite(s) ? s.toFixed(2) : '—'}</span>
          </span>
        )
      },
      sort: (r) => num(r.score),
    },
    { key: 'question', header: 'Question', width: 'minmax(260px,3fr)', render: (r) => <span className="truncate">{r.question ?? '—'}</span>, sort: (r) => r.question },
    { key: 'name', header: 'Evaluation', width: '150px', render: (r) => <span className="text-ink-2">{String(r.name ?? '').replace(/_/g, ' ')}</span>, sort: (r) => r.name },
    { key: 'judge', header: 'Judge', width: 'minmax(120px,1fr)', render: (r) => <span className="truncate font-mono text-xs text-ink-3">{r.judge}</span>, sort: (r) => r.judge },
  ]
  return (
    <div className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex flex-wrap items-center gap-3 border-b border-line px-3 py-2">
          {(sumRes.data?.records ?? []).map((s) => {
            const rate = (100 * num(s.passed)) / (num(s.n) || 1)
            return (
              <Tip key={s.name} content={`${s.method ?? ''} · avg score ${num(s.avg).toFixed(2)} · last ${fmtDateTime(s.last)}`}>
                <span className="inline-flex items-center gap-2 rounded-lg border border-line bg-sunken px-2.5 py-1 text-xs">
                  <Gauge className="size-3.5 text-ink-3" />
                  <span className="text-ink-2">{String(s.name).replace(/_/g, ' ')}</span>
                  <b className={clsx('tnum font-semibold', rate < 50 ? 'text-warn' : 'text-ok')}>{fmtPct(rate, 0)} pass</b>
                  <span className="tnum text-ink-4">n={num(s.n)}</span>
                </span>
              </Tip>
            )
          })}
          <span className="text-xs text-ink-3">{etf.label.toLowerCase()}</span>
          <span className="ml-auto flex gap-1 text-xs">
            {(['all', 'fail', 'pass'] as const).map((l) => (
              <button key={l} type="button" onClick={() => setLabel(l)} className={clsx('rounded-md px-2 py-1', label === l ? 'bg-raised text-ink shadow-[0_0_0_1px_var(--line-strong)]' : 'text-ink-3 hover:text-ink-2')}>
                {l === 'all' ? 'All' : l === 'fail' ? 'Failed' : 'Passed'}
              </button>
            ))}
          </span>
        </div>
        {res.error ? (
          <ErrorBox error={res.error} />
        ) : (
          <DataTable
            rows={rows}
            loading={res.isLoading}
            columns={cols}
            rowKey={(r, i) => `${r.timestamp}${i}`}
            onOpen={setSel}
            initialSort={{ key: 'time', dir: 'desc' }}
            className="flex-1"
            maxHeight={620}
            empty={<Empty icon={<Gauge className="size-5" />} title="No evaluations" hint="No gen_ai.evaluation.result events in this timeframe." />}
          />
        )}
      </div>
      {sel && (
        <SidePanel title={sel.question ?? 'Evaluation'} onClose={() => setSel(null)} width="w-[min(620px,46vw)]">
          <div className="mb-3 flex items-center gap-2">
            {sel.label === 'pass' ? <Badge tone="ok">pass</Badge> : <Badge tone="crit">{sel.label ?? 'fail'}</Badge>}
            <span className="tnum text-sm">score {num(sel.score).toFixed(2)}</span>
            <span className="text-xs text-ink-3">· judged by {sel.judge}</span>
          </div>
          {sel.explanation && <p className="mb-4 text-sm text-ink-2">{sel.explanation}</p>}
          <div className="mb-2 text-2xs font-medium tracking-wide text-ink-3 uppercase">Criteria</div>
          <ul className="mb-4 flex flex-col gap-2">
            {parseCriteria(sel.criteria).map((c, i) => (
              <li key={i} className={clsx('rounded-lg border p-2.5', c.fulfilled ? 'border-ok/25 bg-ok-wash' : 'border-crit/25 bg-crit-wash')}>
                <div className="flex items-start gap-2 text-sm">
                  {c.fulfilled ? <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-ok" /> : <XCircle className="mt-0.5 size-4 shrink-0 text-crit" />}
                  <span className="font-medium">{c.criterion}</span>
                </div>
                {c.reason && <div className="mt-1 pl-6 text-xs text-ink-2">{c.reason}</div>}
              </li>
            ))}
          </ul>
          {sel.answer && <Conversation label="Agent answer" messages={parseMessages(sel.answer)} />}
          <details className="mt-4">
            <summary className="cursor-pointer text-xs text-ink-3">All attributes</summary>
            <div className="mt-2">
              <Inspector rec={sel} />
            </div>
          </details>
        </SidePanel>
      )}
    </div>
  )
}
