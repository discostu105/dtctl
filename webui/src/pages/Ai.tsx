import clsx from 'clsx'
import { AlertTriangle, Bot, CheckCircle2, Database, ExternalLink, Gauge, MessagesSquare, Search, Sparkles, Wrench, X, XCircle } from 'lucide-react'
import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Link, useLocation, useSearch } from 'wouter'
import { Legend, SERIES, TimeChart, tsAxis } from '../components/Chart'
import { Conversation, parseMessages } from '../components/Conversation'
import { DataTable, type Column } from '../components/DataTable'
import { DataTabs } from '../components/DataTabs'
import { EntityLink } from '../components/Entity'
import { FilterInput, PageHeader, Panel, QueryInfo } from '../components/Panel'
import { Inspector, SidePanel } from '../components/signals'
import { Spark } from '../components/Spark'
import { Badge, Empty, ErrorBox, Facts, Kbd, Segmented, Skeleton, SkeletonRows, TimeAgo, Tip } from '../components/ui'
import { arr, num, useDql, type DqlSpec, type Rec } from '../lib/api'
import {
  agentsQuery, aiKpiQuery, callDetailQuery, callsByModelSeries, conversationsQuery, evalRunsQuery, evalsByQuestionQuery, evalSourceQuery, evalsQuery,
  fmtTokens, lastText, modelsQuery, userPrompt, recentCallsQuery, toolExecutionsQuery, toolsQuery, ttftSeries, type ConversationFilter,
} from '../lib/ai'
import { q } from '../lib/dql'
import { fmtCompact, fmtDateTime, fmtMs, fmtPct } from '../lib/format'
import { traceHref } from '../lib/links'
import { tfSpec } from '../lib/shared'
import { useTitle } from '../lib/store'
import { absolute, floorTf, intervalFor, setTimeframe, useTimeframe, type Timeframe } from '../lib/timeframe'

type Tab = 'conversations' | 'calls' | 'models' | 'agents' | 'tools' | 'evals'

export const aiKpiSpec = (tf: Timeframe): DqlSpec => tfSpec(tf, aiKpiQuery(), { ttl: 30 })
// Evaluations typically run as nightly CI batches: look back at least 7 days.
const evalTf = (tf: Timeframe) => floorTf(tf, '7d')

export const conversationsSpec = (tf: Timeframe, f: ConversationFilter) => tfSpec(tf, conversationsQuery(f), { ttl: 30, maxRecords: 300 })
const callsSpec = (tf: Timeframe, model: string | null) => tfSpec(tf, recentCallsQuery(model ? `gen_ai.request.model == ${q(model)}` : undefined), { ttl: 20 })
const modelsSpec = (tf: Timeframe) => tfSpec(tf, modelsQuery(), { ttl: 30 })
const agentsSpec = (tf: Timeframe) => tfSpec(tf, agentsQuery(), { ttl: 30 })
const toolsSpec = (tf: Timeframe) => tfSpec(tf, toolsQuery(), { ttl: 30 })
const evalsSpec = (tf: Timeframe) => tfSpec(evalTf(tf), evalsQuery(), { ttl: 60 })

export const secs = (s: number) => (Number.isFinite(s) ? (s < 1 ? `${Math.round(s * 1000)} ms` : `${s.toFixed(s < 10 ? 2 : 1)} s`) : '—')
export const convHref = (id: string) => `/ai/conversations/${encodeURIComponent(id)}`

export default function Ai() {
  useTitle('AI')
  const tf = useTimeframe()
  const params = new URLSearchParams(useSearch())
  const [, navigate] = useLocation()
  const tab = (params.get('tab') as Tab) || 'conversations'
  const model = params.get('model')
  const agent = params.get('agent')
  const errorsOnly = params.get('errors') === '1'
  const qParam = params.get('q') ?? ''
  const [text, setText] = useState(qParam)
  useEffect(() => setText(qParam), [qParam])
  const set = (mut: (p: URLSearchParams) => void) => {
    const p = new URLSearchParams(location.search)
    mut(p)
    navigate(`/ai?${p}`, { replace: true })
  }
  // Search commits on Enter (content search scans message payloads: ~1–2 s).
  const commitSearch = (v: string) =>
    set((p) => {
      if (v.trim()) p.set('q', v.trim())
      else p.delete('q')
      p.delete('tab')
    })

  const kpi = useDql(aiKpiSpec(tf))
  const k = kpi.data?.records[0]
  const iv = intervalFor(tf.ms)
  const callsSeriesSpec = tfSpec(tf, callsByModelSeries(iv))
  const callsSeries = useDql(callsSeriesSpec)
  const ttftSpec = tfSpec(tf, ttftSeries(iv))
  const ttft = useDql(ttftSpec)

  // Every tab's query runs up front: counts, empty states, instant switching.
  const filter: ConversationFilter = { search: qParam, agent, errorsOnly }
  const convSpec = conversationsSpec(tf, filter)
  const conv = useDql(convSpec)
  const cSpec = callsSpec(tf, model)
  const calls = useDql(cSpec)
  const models = useDql(modelsSpec(tf))
  const agents = useDql(agentsSpec(tf))
  const tools = useDql(toolsSpec(tf))
  const evals = useDql(evalsSpec(tf))

  const callChart = useMemo(() => {
    const recs = [...(callsSeries.data?.records ?? [])].sort((a, b) => sum(b.calls) - sum(a.calls))
    if (!recs.length) return null
    return { x: tsAxis(recs[0], 'calls'), series: recs.slice(0, 6).map((r, i) => ({ label: String(r.model ?? 'unknown'), values: r.calls as number[], color: SERIES[i], fill: false })) }
  }, [callsSeries.data])
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
    const rs = evals.data?.records ?? []
    return rs.length ? { rate: (100 * rs.filter((r) => r.label === 'pass').length) / rs.length, n: rs.length } : null
  }, [evals.data])

  const noData = k && num(k.chats) + num(k.tools) + num(k.agents) === 0

  return (
    <div className="mx-auto flex max-w-[1600px] flex-col p-5">
      <PageHeader title="AI" icon={<Sparkles className="size-5" />} sub={`Conversations, LLM calls, agents, tools and evaluations · ${tf.label.toLowerCase()}`} />

      {/* search: the front door for "what did people ask / what happened with X" */}
      <form
        className="relative mb-4"
        onSubmit={(e) => {
          e.preventDefault()
          commitSearch(text)
        }}
      >
        <Search className="absolute top-1/2 left-3 size-4 -translate-y-1/2 text-ink-3" />
        <input
          data-filter
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => e.key === 'Escape' && (setText(''), commitSearch(''))}
          placeholder="Search conversations: what users asked, what agents answered, tool commands, a conversation or trace ID…"
          className="h-11 w-full rounded-xl border border-line bg-panel pr-28 pl-9 text-[15px] outline-none placeholder:text-ink-4 focus:border-accent/60"
        />
        <span className="absolute top-1/2 right-3 flex -translate-y-1/2 items-center gap-1.5 text-xs text-ink-4">
          {conv.isFetching && qParam ? 'searching…' : <>press <Kbd>↵</Kbd></>}
        </span>
      </form>

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
            <Kpi label="Prompt cache hits" value={k && (num(k.input) ? fmtPct((100 * num(k.cache_read)) / num(k.input)) : '—')} sub="of input tokens" />
            <Kpi label="Time to first token" value={k && secs(num(k.ttft_p50))} sub={k && `p50 · p90 ${secs(num(k.ttft_p90))}`} />
            <Kpi
              label="Tool calls"
              value={k && fmtCompact(num(k.tools))}
              sub={k && (num(k.tools) ? <span className={clsx(num(k.tool_fail) && 'text-warn')}>{fmtPct((100 * num(k.tool_fail)) / num(k.tools), 2)} failed</span> : '—')}
            />
            <Kpi
              label="Eval pass rate"
              value={evalPass ? fmtPct(evalPass.rate, 0) : evals.isLoading ? undefined : '—'}
              sub={evalPass ? `${evalPass.n} evaluations · ${evalTf(tf).label.toLowerCase()}` : 'no evaluations'}
              tone={evalPass && evalPass.rate < 50 ? 'warn' : undefined}
              onClick={() => set((p) => p.set('tab', 'evals'))}
            />
          </div>

          <div className="mb-4 grid grid-cols-2 gap-4 max-xl:grid-cols-1">
            <Panel title="LLM calls by model" spec={callsSeriesSpec} result={callsSeries} hint="drag to zoom">
              {callsSeries.error ? (
                <ErrorBox error={callsSeries.error} />
              ) : !callChart ? (
                <Skeleton className="m-3 h-[160px]" />
              ) : (
                <div className="p-3 pl-1">
                  <TimeChart x={callChart.x} series={callChart.series} height={160} format={fmtCompact} syncKey="ai" onZoom={(a, b) => setTimeframe(absolute(a, b))} />
                  <Legend className="mt-2 pl-3" items={callChart.series.map((s) => ({ label: s.label, color: s.color! }))} />
                </div>
              )}
            </Panel>
            <Panel title="Time to first token" spec={ttftSpec} result={ttft} hint="server-reported">
              {ttft.error ? (
                <ErrorBox error={ttft.error} />
              ) : !ttftChart ? (
                <Skeleton className="m-3 h-[160px]" />
              ) : (
                <div className="p-3 pl-1">
                  <TimeChart x={ttftChart.x} series={ttftChart.series} height={160} format={secs} syncKey="ai" onZoom={(a, b) => setTimeframe(absolute(a, b))} />
                  <Legend className="mt-2 pl-3" items={ttftChart.series.map((s) => ({ label: s.label, color: s.color }))} />
                </div>
              )}
            </Panel>
          </div>

          <div className="flex min-h-[620px] flex-col rounded-xl border border-line bg-panel">
            <DataTabs
              value={tab}
              onChange={(t) => set((p) => (t === 'conversations' ? p.delete('tab') : p.set('tab', t)))}
              tabs={[
                { value: 'conversations', label: 'Conversations', spec: convSpec, result: conv, limit: 300 },
                { value: 'calls', label: 'LLM calls', spec: cSpec, result: calls, limit: 300 },
                { value: 'models', label: 'Models', spec: modelsSpec(tf), result: models },
                { value: 'agents', label: 'Agents', spec: agentsSpec(tf), result: agents },
                { value: 'tools', label: 'Tools', spec: toolsSpec(tf), result: tools },
                { value: 'evals', label: 'Evaluations', spec: evalsSpec(tf), result: evals, limit: 500 },
              ]}
              right={
                <FilterChips
                  chips={[
                    tab === 'conversations' && qParam && { label: `“${qParam}”`, clear: () => commitSearch('') },
                    tab === 'conversations' && agent && { label: `agent: ${agent}`, clear: () => set((p) => p.delete('agent')) },
                    tab === 'calls' && model && { label: model, clear: () => set((p) => p.delete('model')) },
                  ]}
                />
              }
            />
            <div className="flex min-h-0 flex-1 flex-col">
              {tab === 'conversations' && (
                <ConversationsView result={conv} search={qParam} errorsOnly={errorsOnly} onErrorsOnly={(v) => set((p) => (v ? p.set('errors', '1') : p.delete('errors')))} />
              )}
              {tab === 'calls' && <CallsView result={calls} />}
              {tab === 'models' &&
                <ModelsView
                  result={models}
                  onPick={(m) =>
                    set((p) => {
                      p.set('tab', 'calls')
                      p.set('model', m)
                    })
                  }
                />}
              {tab === 'agents' &&
                <AgentsView
                  result={agents}
                  onPick={(a) =>
                    set((p) => {
                      p.delete('tab')
                      p.set('agent', a)
                    })
                  }
                />}
              {tab === 'tools' && <ToolsView tf={tf} result={tools} />}
              {tab === 'evals' && <EvalsView tf={tf} result={evals} />}
            </div>
          </div>
        </>
      )}
    </div>
  )
}

const sum = (a: unknown) => arr(a).reduce((x: number, y: unknown) => x + (num(y) || 0), 0)

function FilterChips({ chips }: { chips: (false | '' | null | undefined | { label: string; clear: () => void })[] }) {
  return (
    <>
      {chips.filter(Boolean).map((c) => {
        const chip = c as { label: string; clear: () => void }
        return (
          <button
            key={chip.label}
            type="button"
            onClick={chip.clear}
            className="inline-flex h-6 max-w-72 items-center gap-1 rounded-md bg-accent-wash px-2 text-xs text-accent-ink hover:brightness-110"
          >
            <span className="truncate">{chip.label}</span>
            <X className="size-3 shrink-0" />
          </button>
        )
      })}
    </>
  )
}

function Kpi({ label, value, sub, tone, onClick }: { label: string; value: ReactNode; sub?: ReactNode; tone?: 'warn'; onClick?: () => void }) {
  return (
    <div onClick={onClick} className={clsx('rounded-xl border border-line bg-panel p-3.5', onClick && 'cursor-pointer hover:border-line-strong hover:bg-panel-hover')}>
      <div className="text-xs text-ink-3">{label}</div>
      {value == null ? <Skeleton className="mt-1.5 h-7 w-20" /> : <div className={clsx('tnum mt-1 text-[24px] leading-none font-semibold tracking-tight', tone === 'warn' && 'text-warn')}>{value}</div>}
      <div className="mt-1.5 truncate text-xs text-ink-3">{sub}</div>
    </div>
  )
}

type Result = ReturnType<typeof useDql>

// ── conversations ───────────────────────────────────────────────────────

function ConversationsView({ result, search, errorsOnly, onErrorsOnly }: { result: Result; search: string; errorsOnly: boolean; onErrorsOnly: (v: boolean) => void }) {
  const cols: Column[] = [
    {
      key: 'start',
      header: 'Started',
      width: '96px',
      render: (r) => (
        <span className="flex flex-col leading-tight">
          <TimeAgo value={r.start} className="text-ink-2" />
          <span className="tnum text-2xs text-ink-4">{fmtDateTime(r.start).slice(-8)}</span>
        </span>
      ),
      sort: (r) => r.start,
    },
    {
      key: 'conv',
      header: 'Conversation',
      width: 'minmax(360px,4fr)',
      render: (r) => {
        const prompt = userPrompt(r.prompt)
        const answer = lastText(r.answer)
        return (
          <span className="flex min-w-0 flex-col leading-snug">
            <span className="truncate font-medium text-ink">{prompt || <span className="font-normal text-ink-4">(no user prompt captured)</span>}</span>
            <span className="truncate text-xs text-ink-3">{answer ? <>↳ {answer}</> : <span className="text-ink-4">no final answer text</span>}</span>
          </span>
        )
      },
    },
    {
      key: 'who',
      header: 'Agent · service',
      width: 'minmax(150px,1.2fr)',
      render: (r) => (
        <span className="flex min-w-0 flex-col leading-tight">
          <span className="truncate text-ink-2">{arr(r.agents).join(', ') || '—'}</span>
          <span className="truncate text-2xs text-ink-4">{r.service}</span>
        </span>
      ),
      sort: (r) => arr(r.agents)[0] ?? '',
    },
    {
      key: 'steps',
      header: 'Steps',
      width: '110px',
      align: 'right',
      render: (r) => (
        <span className="flex flex-col items-end leading-tight">
          <span>
            {num(r.llm)} LLM · {num(r.tools)} tools
          </span>
          {num(r.tool_fail) > 0 && <span className="text-2xs text-warn">{num(r.tool_fail)} tool failures</span>}
        </span>
      ),
      sort: (r) => num(r.llm) + num(r.tools),
    },
    {
      key: 'tokens',
      header: 'Tokens',
      width: '96px',
      align: 'right',
      render: (r) => (
        <span className="flex flex-col items-end leading-tight">
          <span>{fmtTokens(num(r.input) + num(r.output))}</span>
          <span className="text-2xs text-ink-4">{num(r.input) ? `${fmtPct((100 * num(r.cached)) / num(r.input), 0)} cached` : ''}</span>
        </span>
      ),
      sort: (r) => num(r.input) + num(r.output),
    },
    { key: 'dur', header: 'Duration', width: '80px', align: 'right', render: (r) => span(r.start, r.end), sort: (r) => Date.parse(r.end) - Date.parse(r.start) },
    {
      key: 'status',
      header: '',
      width: '28px',
      render: (r) =>
        num(r.errors) > 0 ? (
          <Tip content={`${num(r.errors)} failed span${num(r.errors) === 1 ? '' : 's'}`}>
            <AlertTriangle className="size-4 text-crit" />
          </Tip>
        ) : (
          <CheckCircle2 className="size-4 text-ok/70" />
        ),
      sort: (r) => num(r.errors),
    },
    ...(search ? [{ key: 'hits', header: 'Hits', width: '52px', align: 'right' as const, render: (r: Rec) => <Badge tone="accent">{num(r.hits)}</Badge>, sort: (r: Rec) => num(r.hits) }] : []),
  ]
  return (
    <>
      <div className="flex items-center gap-3 border-b border-line px-3 py-2">
        <Segmented
          value={errorsOnly ? 'errors' : 'all'}
          onChange={(v) => onErrorsOnly(v === 'errors')}
          options={[
            { value: 'all', label: 'All' },
            { value: 'errors', label: 'With errors' },
          ]}
        />
        <span className="text-xs text-ink-3">{search ? `Matching “${search}” in prompts, answers and tool calls` : 'Newest first · click to replay a conversation step by step'}</span>
      </div>
      {result.error ? (
        <ErrorBox error={result.error} />
      ) : (
        <DataTable
          rows={result.data?.records}
          loading={result.isLoading}
          columns={cols}
          rowKey={(r) => r.conversation}
          href={(r) => convHref(r.conversation)}
          initialSort={{ key: 'start', dir: 'desc' }}
          rowHeight={52}
          className="flex-1"
          maxHeight={720}
          empty={<Empty icon={<MessagesSquare className="size-5" />} title={search ? 'No conversation matches' : 'No conversations'} hint={search ? 'Try another word, or widen the timeframe.' : 'No spans carry gen_ai.conversation.id in this timeframe.'} />}
        />
      )}
    </>
  )
}

function span(a: unknown, b: unknown) {
  const ms = Date.parse(String(b)) - Date.parse(String(a))
  return Number.isFinite(ms) ? fmtMs(ms) : '—'
}

// ── LLM calls ───────────────────────────────────────────────────────────

function CallsView({ result }: { result: Result }) {
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
        {result.error ? (
          <ErrorBox error={result.error} />
        ) : (
          <DataTable
            rows={result.data?.records}
            loading={result.isLoading}
            columns={cols}
            rowKey={(r) => `${r['trace.id']}${r['span.id']}`}
            onOpen={setSel}
            selectedKey={sel ? `${sel['trace.id']}${sel['span.id']}` : null}
            initialSort={{ key: 'time', dir: 'desc' }}
            className="flex-1"
            maxHeight={720}
          />
        )}
      </div>
      {sel && <LlmCallPanel traceId={sel['trace.id']} spanId={sel['span.id']} title={sel.model} onClose={() => setSel(null)} />}
    </div>
  )
}

/** Full LLM call: facts + conversation view (shared by AI, conversations and the trace waterfall). */
export function LlmCallPanel({ traceId, spanId, title, onClose }: { traceId: string; spanId: string; title?: string; onClose: () => void }) {
  const res = useDql({ query: callDetailQuery(traceId, spanId), ttl: 600 })
  const d = res.data?.records[0]
  return (
    <SidePanel
      title={<span className="font-mono text-xs">{title ?? d?.['gen_ai.request.model'] ?? 'LLM call'}</span>}
      onClose={onClose}
      width="w-[min(680px,50vw)]"
      actions={
        <Link href={traceHref(traceId)} className="inline-flex items-center gap-1 rounded px-1.5 py-1 text-xs text-accent-ink hover:bg-accent-wash">
          Trace <ExternalLink className="size-3" />
        </Link>
      }
    >
      {res.isLoading ? <SkeletonRows rows={8} /> : !d ? <Empty title="Span not found" /> : <LlmCallBody d={d} />}
    </SidePanel>
  )
}

function LlmCallBody({ d }: { d: Rec }) {
  const [view, setView] = useState<'conversation' | 'raw'>('conversation')
  const input = parseMessages(d['gen_ai.input.messages'])
  const output = parseMessages(d['gen_ai.output.messages'])
  const system = d['gen_ai.system_instructions']
  const conv = d['gen_ai.conversation.id']
  const inTok = num(d['gen_ai.usage.input_tokens'])
  return (
    <>
      <Facts
        className="mb-4"
        items={[
          ['When', <>{fmtDateTime(d.start_time)} · <TimeAgo value={d.start_time} /></>],
          ['Model', <span className="font-mono text-xs">{d['gen_ai.request.model']}</span>],
          ['Provider', d['gen_ai.provider.name']],
          ['Tokens', `${fmtTokens(inTok)} in (${fmtTokens(num(d['gen_ai.usage.cache_read.input_tokens']))} cached) · ${fmtTokens(num(d['gen_ai.usage.output_tokens']))} out`],
          ['Time to first token', secs(num(d['gen_ai.server.time_to_first_token']))],
          ['Duration', fmtMs(num(d.duration) / 1e6)],
          ['Finish', arr(d['gen_ai.response.finish_reasons']).join(', ')],
          [
            'Conversation',
            conv && (
              <Link href={convHref(conv)} className="font-mono text-xs text-accent-ink hover:underline">
                {conv} →
              </Link>
            ),
          ],
        ]}
      />
      <div className="mb-3 flex gap-3 border-b border-line text-sm">
        {(['conversation', 'raw'] as const).map((v) => (
          <button key={v} type="button" onClick={() => setView(v)} className={clsx('-mb-px border-b-2 pb-1.5', view === v ? 'border-accent text-ink' : 'border-transparent text-ink-3 hover:text-ink-2')}>
            {v === 'conversation' ? 'Messages' : 'All attributes'}
          </button>
        ))}
      </div>
      {view === 'raw' ? (
        <Inspector rec={d} />
      ) : (
        <div className="flex flex-col gap-5">
          {system && <Conversation label="System instructions" messages={[{ role: 'system', parts: parseMessages(system).flatMap((m: any) => (m.parts ? m.parts : [m])) }]} />}
          <Conversation label={`Input · ${input.length} message${input.length === 1 ? '' : 's'}`} messages={input} />
          <Conversation label="Output" messages={output} />
          {!input.length && !output.length && <Empty title="No message content captured" hint="The instrumentation didn't record gen_ai.input/output.messages for this call." />}
        </div>
      )}
    </>
  )
}

// ── models / agents / tools ─────────────────────────────────────────────

function ModelsView({ result, onPick }: { result: Result; onPick: (model: string) => void }) {
  const cols: Column[] = [
    {
      key: 'model',
      header: 'Model',
      width: 'minmax(240px,2fr)',
      render: (r) => (r.model_id ? <EntityLink id={r.model_id} name={r.model} type="GENAI_MODEL" className="font-mono text-xs" /> : <span className="font-mono text-xs">{r.model}</span>),
      sort: (r) => r.model,
    },
    { key: 'provider', header: 'Provider', width: '100px', render: (r) => <Badge>{r.provider ?? '—'}</Badge>, sort: (r) => r.provider },
    { key: 'calls', header: 'Calls', width: '72px', align: 'right', render: (r) => fmtCompact(num(r.calls)), sort: (r) => num(r.calls) },
    { key: 'in', header: 'Input tok', width: '84px', align: 'right', render: (r) => fmtTokens(num(r.input)), sort: (r) => num(r.input) },
    { key: 'out', header: 'Output tok', width: '84px', align: 'right', render: (r) => fmtTokens(num(r.output)), sort: (r) => num(r.output) },
    { key: 'cache', header: 'Cache hits', width: '84px', align: 'right', render: (r) => (num(r.input) ? fmtPct((100 * num(r.cache_read)) / num(r.input)) : '—'), sort: (r) => num(r.cache_read) / (num(r.input) || 1) },
    { key: 'ttft50', header: 'TTFT p50', width: '80px', align: 'right', render: (r) => secs(num(r.ttft_p50)), sort: (r) => num(r.ttft_p50) },
    { key: 'ttft90', header: 'TTFT p90', width: '80px', align: 'right', render: (r) => secs(num(r.ttft_p90)), sort: (r) => num(r.ttft_p90) },
    { key: 'dur90', header: 'Duration p90', width: '96px', align: 'right', render: (r) => fmtMs(num(r.dur_p90) / 1e6), sort: (r) => num(r.dur_p90) },
    { key: 'failed', header: 'Failed', width: '64px', align: 'right', render: (r) => <span className={clsx(num(r.failed) ? 'text-crit' : 'text-ink-4')}>{num(r.failed)}</span>, sort: (r) => num(r.failed) },
  ]
  if (result.error) return <ErrorBox error={result.error} />
  return (
    <>
      <Hint>Click a model to see its LLM calls.</Hint>
      <DataTable rows={result.data?.records} loading={result.isLoading} columns={cols} rowKey={(r) => `${r.model}|${r.provider}`} onOpen={(r) => onPick(String(r.model))} initialSort={{ key: 'calls', dir: 'desc' }} className="flex-1" />
    </>
  )
}

function AgentsView({ result, onPick }: { result: Result; onPick: (agent: string) => void }) {
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
    { key: 'failed', header: 'Failed', width: '64px', align: 'right', render: (r) => <span className={clsx(num(r.failed) ? 'text-crit' : 'text-ink-4')}>{num(r.failed)}</span>, sort: (r) => num(r.failed) },
    { key: 'last', header: 'Last run', width: '90px', align: 'right', render: (r) => <TimeAgo value={r.last} className="text-ink-2" />, sort: (r) => r.last },
  ]
  if (result.error) return <ErrorBox error={result.error} />
  return (
    <>
      <Hint>Click an agent to see its conversations; the name opens the agent's entity page.</Hint>
      <DataTable rows={result.data?.records} loading={result.isLoading} columns={cols} rowKey={(r) => `${r.agent}|${r.agent_id}`} onOpen={(r) => r.agent && onPick(String(r.agent))} initialSort={{ key: 'runs', dir: 'desc' }} className="flex-1" />
    </>
  )
}

function ToolsView({ tf, result }: { tf: Timeframe; result: Result }) {
  const [filter, setFilter] = useState('')
  const [sel, setSel] = useState<Rec | null>(null)
  const f = filter.toLowerCase()
  const rows = useMemo(() => result.data?.records.filter((r) => !f || String(r.tool).toLowerCase().includes(f)), [result.data, f])
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
    <div className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex items-center border-b border-line px-3 py-2">
          <span className="text-xs text-ink-3">Click a tool to see its executions, failures first.</span>
          <FilterInput value={filter} onChange={setFilter} placeholder="Filter tools…" className="ml-auto w-64" />
        </div>
        {result.error ? (
          <ErrorBox error={result.error} />
        ) : (
          <DataTable rows={rows} loading={result.isLoading} columns={cols} rowKey={(r) => String(r.tool)} onOpen={setSel} selectedKey={sel ? String(sel.tool) : null} initialSort={{ key: 'calls', dir: 'desc' }} className="flex-1" />
        )}
      </div>
      {sel && <ToolPanel tf={tf} tool={sel} onClose={() => setSel(null)} />}
    </div>
  )
}

function ToolPanel({ tf, tool, onClose }: { tf: Timeframe; tool: Rec; onClose: () => void }) {
  const failed = num(tool.failed)
  const [failedOnly, setFailedOnly] = useState(failed > 0)
  useEffect(() => setFailedOnly(num(tool.failed) > 0), [tool])
  const spec = tfSpec(tf, toolExecutionsQuery(String(tool.tool), failedOnly), { ttl: 20 })
  const res = useDql(spec)
  const [open, setOpen] = useState<Rec | null>(null)
  return (
    <SidePanel title={<span className="font-mono text-xs">{tool.tool}</span>} onClose={onClose} width="w-[min(620px,46vw)]" actions={<QueryInfo spec={spec} result={res} />}>
      <div className="mb-3 flex flex-wrap gap-x-4 gap-y-1 text-sm text-ink-2">
        <span>
          <b className="tnum text-ink">{fmtCompact(num(tool.calls))}</b> calls
        </span>
        <span className={clsx(failed && 'text-crit')}>
          <b className="tnum">{failed}</b> failed
        </span>
        <span>
          p50 {fmtMs(num(tool.dur_p50) / 1e6)} · p90 {fmtMs(num(tool.dur_p90) / 1e6)}
        </span>
      </div>
      <Segmented
        className="mb-3"
        value={failedOnly ? 'failed' : 'all'}
        onChange={(v) => setFailedOnly(v === 'failed')}
        options={[
          { value: 'failed', label: 'Failures', count: failed },
          { value: 'all', label: 'All executions' },
        ]}
      />
      {res.isLoading ? (
        <SkeletonRows rows={6} />
      ) : !res.data?.records.length ? (
        <Empty title={failedOnly ? 'No failures' : 'No executions'} className="py-6" />
      ) : (
        <ul className="mb-4 divide-y divide-line rounded-lg border border-line">
          {res.data.records.map((r, i) => (
            <li key={i}>
              <button type="button" onClick={() => setOpen(open === r ? null : r)} className={clsx('flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-xs hover:bg-panel-hover', open === r && 'bg-accent-wash')}>
                <span className={clsx('size-2 shrink-0 rounded-full', r['span.status_code'] === 'error' ? 'bg-crit' : 'bg-ok/70')} />
                <span className="w-16 shrink-0 text-ink-3">
                  <TimeAgo value={r.start_time} />
                </span>
                <span className="min-w-0 flex-1 truncate font-mono text-ink-2">{String(r['span.name'] ?? '').replace(/^execute_tool\s+/, '')}</span>
                <span className="tnum shrink-0 text-ink-3">{fmtMs(num(r.duration) / 1e6)}</span>
                {r.conversation ? (
                  <Link href={convHref(r.conversation)} onClick={(e) => e.stopPropagation()} className="shrink-0 text-accent-ink hover:underline">
                    conversation →
                  </Link>
                ) : (
                  <Link href={traceHref(r['trace.id'])} onClick={(e) => e.stopPropagation()} className="shrink-0 text-accent-ink hover:underline">
                    trace →
                  </Link>
                )}
              </button>
              {open === r && (
                <div className="border-t border-line bg-sunken p-2.5">
                  <Inspector rec={r} />
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
      <div className="text-2xs text-ink-4">Tool spans in this data carry the command in the span name; arguments and results are not captured.</div>
    </SidePanel>
  )
}

function Hint({ children }: { children: ReactNode }) {
  return <div className="border-b border-line px-3 py-2 text-xs text-ink-3">{children}</div>
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

type EvalLens = 'results' | 'questions' | 'runs' | 'criteria'

function EvalsView({ tf, result }: { tf: Timeframe; result: Result }) {
  const etf = evalTf(tf)
  const srcSpec = tfSpec(etf, evalSourceQuery(), { ttl: 300 })
  const src = useDql(srcSpec)
  const s = src.data?.records[0]
  const [lens, setLens] = useState<EvalLens>('results')
  const [focus, setFocus] = useState<{ kind: 'question' | 'run' | 'criterion'; value: string } | null>(null)
  const pick = (kind: 'question' | 'run' | 'criterion', value: string) => {
    setFocus({ kind, value })
    setLens('results')
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* provenance: where these numbers come from, and how to check them */}
      <div className="flex items-start gap-2.5 border-b border-line bg-sunken/60 px-3 py-2.5 text-xs text-ink-2">
        <Database className="mt-0.5 size-3.5 shrink-0 text-ink-3" />
        {s ? (
          <div className="min-w-0 flex-1 leading-relaxed">
            <b className="font-medium text-ink">{num(s.n)}</b> results from business events of type <code className="rounded bg-line px-1 font-mono">gen_ai.evaluation.result</code>
            {s.provider && (
              <>
                , emitted by <b className="font-medium text-ink">{s.provider}</b>
              </>
            )}
            {arr(s.pipeline).length > 0 && (
              <>
                {' '}
                and ingested through OpenPipeline (<span className="font-mono">{arr(s.pipeline).join(' → ')}</span>
                {s.source && <>, source {s.source}</>})
              </>
            )}
            . {etf.label}, from <TimeAgo value={s.first} /> to <TimeAgo value={s.last} />. Each result is an LLM-as-judge verdict on one agent answer.
          </div>
        ) : (
          <div className="flex-1">{src.isLoading ? 'Looking up the evaluation source…' : 'No evaluation events found.'}</div>
        )}
        <QueryInfo spec={srcSpec} result={src} />
      </div>
      <div className="flex items-center gap-3 border-b border-line px-3 py-2">
        <Segmented
          value={lens}
          onChange={setLens}
          options={[
            { value: 'results', label: 'Results' },
            { value: 'questions', label: 'By question' },
            { value: 'runs', label: 'By run' },
            { value: 'criteria', label: 'Failing criteria' },
          ]}
        />
        {focus && lens === 'results' && (
          <button
            type="button"
            onClick={() => setFocus(null)}
            className="inline-flex h-6 max-w-[420px] items-center gap-1 rounded-md bg-accent-wash px-2 text-xs text-accent-ink hover:brightness-110"
          >
            <span className="truncate">
              {focus.kind}: {focus.value}
            </span>
            <X className="size-3 shrink-0" />
          </button>
        )}
      </div>
      {lens === 'results' && <EvalResults result={result} focus={focus} />}
      {lens === 'questions' && <EvalQuestions tf={etf} onPick={(q) => pick('question', q)} />}
      {lens === 'runs' && <EvalRuns tf={etf} onPick={(r) => pick('run', r)} />}
      {lens === 'criteria' && <EvalCriteria result={result} onPick={(c) => pick('criterion', c)} />}
    </div>
  )
}

function ScoreBar({ s }: { s: number }) {
  return (
    <span className="flex items-center justify-end gap-2">
      <span className="h-1.5 w-12 overflow-hidden rounded-full bg-line">
        <span className={clsx('block h-full rounded-full', s >= 0.8 ? 'bg-ok' : s >= 0.5 ? 'bg-warn' : 'bg-crit')} style={{ width: `${Math.max(0, Math.min(1, s)) * 100}%` }} />
      </span>
      <span className="w-9">{Number.isFinite(s) ? s.toFixed(2) : '—'}</span>
    </span>
  )
}

function EvalResults({ result, focus }: { result: Result; focus: { kind: 'question' | 'run' | 'criterion'; value: string } | null }) {
  const [sel, setSel] = useState<Rec | null>(null)
  const [label, setLabel] = useState<'all' | 'pass' | 'fail'>('all')
  const rows = useMemo(
    () =>
      result.data?.records
        .filter((r) => label === 'all' || r.label === label)
        .filter((r) => {
          if (!focus) return true
          if (focus.kind === 'question') return r.question === focus.value
          if (focus.kind === 'run') return r.run === focus.value
          return parseCriteria(r.criteria).some((c) => c.criterion === focus.value && !c.fulfilled)
        }),
    [result.data, label, focus],
  )
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
    { key: 'score', header: 'Score', width: '110px', align: 'right', render: (r) => <ScoreBar s={num(r.score)} />, sort: (r) => num(r.score) },
    { key: 'question', header: 'Question', width: 'minmax(260px,3fr)', render: (r) => <span className="truncate">{r.question ?? '—'}</span>, sort: (r) => r.question },
    { key: 'run', header: 'Run', width: '140px', render: (r) => <span className="truncate font-mono text-xs text-ink-3">{r.run}</span>, sort: (r) => r.run },
    { key: 'judge', header: 'Judge', width: 'minmax(120px,1fr)', render: (r) => <span className="truncate font-mono text-xs text-ink-3">{r.judge}</span>, sort: (r) => r.judge },
  ]
  return (
    <div className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex items-center gap-1 border-b border-line px-3 py-1.5 text-xs">
          <span className="mr-2 text-ink-3">{rows ? `${rows.length} results` : ''}</span>
          {(['all', 'fail', 'pass'] as const).map((l) => (
            <button key={l} type="button" onClick={() => setLabel(l)} className={clsx('rounded-md px-2 py-1', label === l ? 'bg-raised text-ink shadow-[0_0_0_1px_var(--line-strong)]' : 'text-ink-3 hover:text-ink-2')}>
              {l === 'all' ? 'All' : l === 'fail' ? 'Failed' : 'Passed'}
            </button>
          ))}
        </div>
        {result.error ? (
          <ErrorBox error={result.error} />
        ) : (
          <DataTable
            rows={rows}
            loading={result.isLoading}
            columns={cols}
            rowKey={(r, i) => `${r.timestamp}${i}`}
            onOpen={setSel}
            initialSort={{ key: 'time', dir: 'desc' }}
            className="flex-1"
            maxHeight={620}
            empty={<Empty icon={<Gauge className="size-5" />} title="No evaluations" hint="No gen_ai.evaluation.result events match." />}
          />
        )}
      </div>
      {sel && (
        <SidePanel title={sel.question ?? 'Evaluation'} onClose={() => setSel(null)} width="w-[min(620px,46vw)]">
          <div className="mb-3 flex items-center gap-2">
            {sel.label === 'pass' ? <Badge tone="ok">pass</Badge> : <Badge tone="crit">{sel.label ?? 'fail'}</Badge>}
            <span className="tnum text-sm">score {num(sel.score).toFixed(2)}</span>
            <span className="text-xs text-ink-3">· judged by {sel.judge} · run {sel.run}</span>
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

function EvalQuestions({ tf, onPick }: { tf: Timeframe; onPick: (q: string) => void }) {
  const spec = tfSpec(tf, evalsByQuestionQuery(), { ttl: 120 })
  const res = useDql(spec)
  const cols: Column[] = [
    { key: 'question', header: 'Question', width: 'minmax(320px,4fr)', render: (r) => <span className="truncate">{r.question ?? '—'}</span>, sort: (r) => r.question },
    { key: 'n', header: 'Runs', width: '60px', align: 'right', render: (r) => num(r.n), sort: (r) => num(r.n) },
    {
      key: 'rate',
      header: 'Pass rate',
      width: '120px',
      align: 'right',
      render: (r) => <span className={clsx(num(r.rate) < 50 ? 'text-crit' : num(r.rate) < 80 ? 'text-warn' : 'text-ok')}>{fmtPct(num(r.rate), 0)}</span>,
      sort: (r) => num(r.rate),
    },
    { key: 'avg', header: 'Avg score', width: '110px', align: 'right', render: (r) => <ScoreBar s={num(r.avg)} />, sort: (r) => num(r.avg) },
    { key: 'trend', header: 'Scores over time', width: '120px', render: (r) => <Spark values={arr(r.scores).map(num)} width={110} max={1} color="var(--s1)" /> },
    { key: 'last', header: 'Last', width: '70px', render: (r) => (r.last_label === 'pass' ? <Badge tone="ok">pass</Badge> : <Badge tone="crit">{r.last_label ?? '—'}</Badge>), sort: (r) => r.last_label },
  ]
  return (
    <>
      <Hint>Questions the agent fails most often come first. Click one to see every result for it.</Hint>
      {res.error ? <ErrorBox error={res.error} /> : <DataTable rows={res.data?.records} loading={res.isLoading} columns={cols} rowKey={(r) => String(r.question)} onOpen={(r) => onPick(String(r.question))} initialSort={{ key: 'rate', dir: 'asc' }} className="flex-1" maxHeight={620} />}
    </>
  )
}

function EvalRuns({ tf, onPick }: { tf: Timeframe; onPick: (r: string) => void }) {
  const spec = tfSpec(tf, evalRunsQuery(), { ttl: 120 })
  const res = useDql(spec)
  const rows = res.data?.records
  const cols: Column[] = [
    { key: 'start', header: 'Run started', width: '150px', render: (r) => <span className="tnum text-ink-2">{fmtDateTime(r.start)}</span>, sort: (r) => r.start },
    { key: 'run', header: 'Run', width: 'minmax(160px,1fr)', render: (r) => <span className="font-mono text-xs">{r.run ?? '(no run id)'}</span>, sort: (r) => r.run },
    { key: 'n', header: 'Results', width: '72px', align: 'right', render: (r) => num(r.n), sort: (r) => num(r.n) },
    {
      key: 'rate',
      header: 'Pass rate',
      width: '160px',
      align: 'right',
      render: (r) => (
        <span className="flex items-center justify-end gap-2">
          <span className="h-1.5 w-20 overflow-hidden rounded-full bg-line">
            <span className={clsx('block h-full rounded-full', num(r.rate) >= 80 ? 'bg-ok' : num(r.rate) >= 50 ? 'bg-warn' : 'bg-crit')} style={{ width: `${num(r.rate)}%` }} />
          </span>
          <span className="w-10">{fmtPct(num(r.rate), 0)}</span>
        </span>
      ),
      sort: (r) => num(r.rate),
    },
    { key: 'avg', header: 'Avg score', width: '110px', align: 'right', render: (r) => <ScoreBar s={num(r.avg)} />, sort: (r) => num(r.avg) },
    { key: 'judge', header: 'Judge', width: 'minmax(140px,1fr)', render: (r) => <span className="truncate font-mono text-xs text-ink-3">{r.judge}</span> },
  ]
  return (
    <>
      <div className="flex items-center gap-3 border-b border-line px-3 py-2 text-xs text-ink-3">
        <span>Each run is one evaluation batch: compare pass rates across runs to spot regressions.</span>
        {rows && rows.length > 1 && (
          <span className="ml-auto flex items-center gap-2">
            pass rate per run, oldest → newest
            <Spark values={[...rows].reverse().map((r) => num(r.rate))} width={140} max={100} color="var(--s1)" />
          </span>
        )}
      </div>
      {res.error ? <ErrorBox error={res.error} /> : <DataTable rows={rows} loading={res.isLoading} columns={cols} rowKey={(r) => String(r.run)} onOpen={(r) => onPick(String(r.run))} initialSort={{ key: 'start', dir: 'desc' }} className="flex-1" maxHeight={620} />}
    </>
  )
}

function EvalCriteria({ result, onPick }: { result: Result; onPick: (c: string) => void }) {
  const rows = useMemo(() => {
    const m = new Map<string, { criterion: string; n: number; failed: number }>()
    for (const r of result.data?.records ?? [])
      for (const c of parseCriteria(r.criteria)) {
        if (!c.criterion) continue
        const e = m.get(c.criterion) ?? { criterion: c.criterion, n: 0, failed: 0 }
        e.n++
        if (!c.fulfilled) e.failed++
        m.set(c.criterion, e)
      }
    return [...m.values()].filter((e) => e.failed > 0)
  }, [result.data])
  const cols: Column<{ criterion: string; n: number; failed: number }>[] = [
    { key: 'criterion', header: 'Criterion', width: 'minmax(360px,5fr)', render: (r) => <span className="truncate">{r.criterion}</span>, sort: (r) => r.criterion },
    { key: 'failed', header: 'Not met', width: '80px', align: 'right', render: (r) => <span className="text-crit">{r.failed}</span>, sort: (r) => r.failed },
    { key: 'n', header: 'Judged', width: '72px', align: 'right', render: (r) => r.n, sort: (r) => r.n },
    { key: 'rate', header: 'Miss rate', width: '84px', align: 'right', render: (r) => fmtPct((100 * r.failed) / r.n, 0), sort: (r) => r.failed / r.n },
  ]
  return (
    <>
      <Hint>The expectations agent answers miss most often (across the {result.data?.records.length ?? '…'} most recent results). Click one to see the failing results.</Hint>
      <DataTable rows={result.data ? rows : undefined} loading={result.isLoading} columns={cols} rowKey={(r) => r.criterion} onOpen={(r) => onPick(r.criterion)} initialSort={{ key: 'failed', dir: 'desc' }} className="flex-1" maxHeight={620} />
    </>
  )
}
