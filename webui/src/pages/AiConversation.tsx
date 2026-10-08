import clsx from 'clsx'
import { AlertTriangle, ArrowLeft, Bot, Brain, CheckCircle2, ChevronRight, MessagesSquare, User, Wrench, XCircle } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'wouter'
import { parseMessages } from '../components/Conversation'
import { EntityLink } from '../components/Entity'
import { QueryInfo } from '../components/Panel'
import { Inspector, SidePanel } from '../components/signals'
import { Badge, CopyButton, Empty, ErrorBox, Segmented, Skeleton, Tip } from '../components/ui'
import { arr, num, useDql, type Rec } from '../lib/api'
import { conversationPromptQuery, conversationStepsQuery, fmtTokens } from '../lib/ai'
import { fmtDateTime, fmtMs, fmtPct } from '../lib/format'
import { traceHref } from '../lib/links'
import { pushRecent, useTitle } from '../lib/store'
import { LlmCallPanel, secs } from './Ai'

/** Strip harness markers but keep the text's own line breaks. */
function tidy(s: string) {
  return s
    .replace(/\[sent:[^\]]*\]/g, '')
    .replace(/\[(BEGIN|END) UNTRUSTED[^\]]*\]/g, '')
    .replace(/<\/?user_content>/g, '')
    .replace(/\n{3,}/g, '\n\n')
    .trim()
}

interface Part {
  type?: string
  content?: unknown
  name?: string
  arguments?: unknown
}
const partsOf = (raw: unknown): Part[] => parseMessages(raw).flatMap((m: any) => (m.parts ?? []) as Part[])
const str = (v: unknown) => (typeof v === 'string' ? v : JSON.stringify(v))

export default function AiConversation({ id }: { id: string }) {
  const stepsSpec = { query: conversationStepsQuery(id), ttl: 120, maxRecords: 3000 }
  const res = useDql(stepsSpec)
  const promptRes = useDql({ query: conversationPromptQuery(id), ttl: 600 })
  // Framework wrapper spans tagged "chat" carry no model and no tokens: noise.
  const steps = (res.data?.records ?? []).filter((s) => s.op !== 'chat' || s.model || s.input != null)
  const [sel, setSel] = useState<Rec | null>(null)
  const [lens, setLens] = useState<'story' | 'llm'>('story')
  const listRef = useRef<HTMLDivElement>(null)

  const prompt = useMemo(() => {
    const msgs = parseMessages(promptRes.data?.records[0]?.['gen_ai.input.messages'])
    const users = msgs.filter((m: any) => m.role === 'user')
    const last = users[users.length - 1] as any
    return last ? tidy((last.parts ?? []).filter((p: Part) => p.type === 'text').map((p: Part) => str(p.content)).join('\n')) : ''
  }, [promptRes.data])

  const llm = steps.filter((s) => s.op === 'chat')
  const tools = steps.filter((s) => s.op === 'execute_tool')
  const failedTools = tools.filter((s) => s['span.status_code'] === 'error')
  const t0 = steps.length ? Date.parse(steps[0].start_time) : 0
  const t1 = steps.length ? Math.max(...steps.map((s) => Date.parse(s.end_time) || 0)) : 0
  const inTok = llm.reduce((a, s) => a + num(s.input), 0)
  const cached = llm.reduce((a, s) => a + num(s.cached), 0)
  const outTok = llm.reduce((a, s) => a + num(s.output), 0)
  const traces = [...new Set(steps.map((s) => s['trace.id']).filter(Boolean))]
  const agents = [...new Set(steps.map((s) => s.agent).filter(Boolean))]
  const models = [...new Set(llm.map((s) => s.model).filter(Boolean))]
  const services = [...new Map(steps.filter((s) => s.service_id).map((s) => [s.service_id, s.service])).entries()]
  const answer = useMemo(() => {
    for (let i = llm.length - 1; i >= 0; i--) {
      const t = partsOf(llm[i].out).filter((p) => p.type === 'text')
      if (t.length) return tidy(t.map((p) => str(p.content)).join('\n'))
    }
    return ''
  }, [llm])

  useTitle(prompt ? `Conversation · ${prompt.slice(0, 60)}` : 'Conversation')
  useEffect(() => {
    if (steps.length) pushRecent({ href: `/ai/conversations/${id}`, label: prompt.slice(0, 80) || id, kind: 'Conversation' })
  }, [steps.length, id, prompt])

  const select = (s: Rec) => {
    setSel(s)
    document.getElementById(`step-${s['span.id']}`)?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }

  if (res.error) return <ErrorBox error={res.error} />
  if (res.isLoading)
    return (
      <div className="p-5">
        <Skeleton className="mb-3 h-7 w-[520px]" />
        <Skeleton className="mb-4 h-24" />
        <Skeleton className="h-96" />
      </div>
    )
  if (!steps.length) return <Empty icon={<MessagesSquare className="size-5" />} title="Conversation not found" hint="No GenAI spans with this conversation ID in the last 7 days." />

  const shown = lens === 'story' ? steps : llm

  return (
    <div className="flex h-full">
      <div ref={listRef} className="min-w-0 flex-1 overflow-auto p-5">
        <Link href="/ai" className="mb-3 inline-flex items-center gap-1 text-xs text-ink-3 hover:text-ink-2">
          <ArrowLeft className="size-3.5" /> AI
        </Link>

        <div className="mb-4 flex flex-wrap items-start gap-4">
          <div className={clsx('flex size-10 items-center justify-center rounded-xl', failedTools.length || steps.some((s) => s['span.status_code'] === 'error') ? 'bg-warn-wash text-warn' : 'bg-accent-wash text-accent-ink')}>
            <MessagesSquare className="size-5" />
          </div>
          <div className="min-w-0 flex-1">
            <h1 className="line-clamp-2 text-xl font-semibold tracking-tight">{prompt.split('\n').find((l) => l.trim()) || 'Conversation'}</h1>
            <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-ink-3">
              <span>{fmtDateTime(steps[0].start_time)}</span>
              <span className="tnum text-ink">{fmtMs(t1 - t0)}</span>
              {agents.map((a) => (
                <Badge key={a} tone="accent">
                  <Bot className="size-3" /> {a}
                </Badge>
              ))}
              {services.map(([sid, name]) => (
                <EntityLink key={sid} id={sid} name={name} type="SERVICE" />
              ))}
              <span className="inline-flex items-center gap-1 font-mono text-2xs text-ink-4">
                {id}
                <CopyButton value={id} label="conversation ID" />
              </span>
            </div>
          </div>
          <div className="flex flex-wrap gap-1.5">
            {traces.slice(0, 4).map((t, i) => (
              <Link key={t} href={traceHref(t)} className="inline-flex h-8 items-center rounded-lg border border-line bg-sunken px-3 text-sm text-ink-2 hover:border-line-strong hover:text-ink">
                {traces.length > 1 ? `Trace ${i + 1}` : 'Open trace'} →
              </Link>
            ))}
          </div>
        </div>

        <div className="mb-4 grid grid-cols-5 gap-3 max-xl:grid-cols-3">
          <Mini label="LLM calls" value={llm.length} sub={models.join(', ')} />
          <Mini label="Tool calls" value={tools.length} sub={failedTools.length ? <span className="text-warn">{failedTools.length} failed</span> : 'none failed'} />
          <Mini label="Input tokens" value={fmtTokens(inTok)} sub={inTok ? `${fmtPct((100 * cached) / inTok, 0)} from prompt cache` : ''} />
          <Mini label="Output tokens" value={fmtTokens(outTok)} />
          <Mini label="Model time" value={fmtMs(llm.reduce((a, s) => a + num(s.duration) / 1e6, 0))} sub={`tools ${fmtMs(tools.reduce((a, s) => a + num(s.duration) / 1e6, 0))}`} />
        </div>

        {llm.length > 1 && <ContextGrowth llm={llm} selected={sel} onPick={select} />}

        <div className="rounded-xl border border-line bg-panel">
          <div className="flex items-center gap-3 border-b border-line px-3 py-2">
            <span className="text-sm font-medium">What happened</span>
            <Segmented
              value={lens}
              onChange={setLens}
              options={[
                { value: 'story', label: 'Every step', count: steps.length },
                { value: 'llm', label: 'LLM calls only', count: llm.length },
              ]}
            />
            <span className="ml-auto" />
            <QueryInfo spec={stepsSpec} result={res} />
          </div>

          <div className="flex flex-col gap-0 p-3">
            {prompt && (
              <Bubble icon={<User className="size-4 text-[var(--s1)]" />} who="User" tone="user">
                <Expandable text={prompt} lines={6} />
              </Bubble>
            )}
            {shown.map((s) => (
              <Step key={s['span.id']} s={s} t0={t0} selected={sel === s} onSelect={() => select(s)} />
            ))}
            {answer && (
              <Bubble icon={<Bot className="size-4 text-accent-ink" />} who="Final answer" tone="answer">
                <Expandable text={answer} lines={10} />
              </Bubble>
            )}
          </div>
        </div>
      </div>
      {sel &&
        (sel.op === 'chat' ? (
          <LlmCallPanel traceId={sel['trace.id']} spanId={sel['span.id']} title={sel.model} onClose={() => setSel(null)} />
        ) : (
          <SidePanel
            title={<span className="font-mono text-xs">{sel['span.name']}</span>}
            onClose={() => setSel(null)}
            actions={
              <Link href={traceHref(sel['trace.id'])} className="rounded px-1.5 py-1 text-xs text-accent-ink hover:bg-accent-wash">
                Trace →
              </Link>
            }
          >
            <Inspector rec={sel} />
          </SidePanel>
        ))}
    </div>
  )
}

function Mini({ label, value, sub }: { label: string; value: React.ReactNode; sub?: React.ReactNode }) {
  return (
    <div className="rounded-xl border border-line bg-panel px-3 py-2.5">
      <div className="text-2xs font-medium tracking-wide text-ink-3 uppercase">{label}</div>
      <div className="tnum mt-0.5 text-lg font-semibold">{value}</div>
      {sub && <div className="truncate text-xs text-ink-3">{sub}</div>}
    </div>
  )
}

function Bubble({ icon, who, tone, children }: { icon: React.ReactNode; who: string; tone: 'user' | 'answer'; children: React.ReactNode }) {
  return (
    <div className={clsx('my-2 flex gap-3 rounded-xl border p-3', tone === 'user' ? 'border-[var(--s1)]/25 bg-[var(--s1)]/5' : 'border-accent/25 bg-accent-wash')}>
      <span className="mt-0.5">{icon}</span>
      <div className="min-w-0 flex-1">
        <div className="mb-1 text-xs font-medium text-ink-2">{who}</div>
        {children}
      </div>
    </div>
  )
}

function Expandable({ text, lines }: { text: string; lines: number }) {
  const [open, setOpen] = useState(false)
  const long = text.split('\n').length > lines || text.length > lines * 120
  return (
    <div>
      <div className={clsx('text-sm leading-relaxed break-words whitespace-pre-wrap text-ink', !open && long && 'max-h-[9.5rem] overflow-hidden [mask-image:linear-gradient(black_70%,transparent)]')}>{text}</div>
      {long && (
        <button type="button" onClick={() => setOpen(!open)} className="mt-1 text-xs text-accent-ink hover:underline">
          {open ? 'Show less' : 'Show all'}
        </button>
      )}
    </div>
  )
}

/** One step of the story: an LLM call (what it thought, said and asked for) or a tool run. */
function Step({ s, t0, selected, onSelect }: { s: Rec; t0: number; selected: boolean; onSelect: () => void }) {
  const at = `+${fmtMs(Date.parse(s.start_time) - t0)}`
  const failed = s['span.status_code'] === 'error'
  const base = clsx('group flex w-full cursor-pointer gap-3 rounded-lg px-2 py-2 text-left hover:bg-panel-hover', selected && 'bg-accent-wash shadow-[inset_2px_0_0_var(--accent)]')

  if (s.op === 'invoke_agent')
    return (
      <div id={`step-${s['span.id']}`} onClick={onSelect} className={clsx(base, 'mt-2 items-center border-t border-line pt-3')}>
        <span className="tnum w-16 shrink-0 font-mono text-2xs text-ink-4">{at}</span>
        <Bot className="size-4 text-accent-ink" />
        <span className="text-sm font-medium">Agent run · {s.agent ?? 'agent'}</span>
        <span className="text-xs text-ink-3">{fmtMs(num(s.duration) / 1e6)}</span>
        {failed && <Badge tone="crit">failed</Badge>}
      </div>
    )

  if (s.op === 'execute_tool')
    return (
      <div id={`step-${s['span.id']}`} onClick={onSelect} className={clsx(base, 'items-center py-1')}>
        <span className="tnum w-16 shrink-0 font-mono text-2xs text-ink-4">{at}</span>
        <span className="ml-6 flex min-w-0 flex-1 items-center gap-2 text-xs">
          <Wrench className={clsx('size-3.5 shrink-0', failed ? 'text-crit' : 'text-[var(--s3)]')} />
          <span className="truncate font-mono text-ink-2">{String(s['span.name'] ?? s.tool).replace(/^execute_tool\s+/, '')}</span>
          {failed ? <XCircle className="size-3.5 shrink-0 text-crit" /> : <CheckCircle2 className="size-3.5 shrink-0 text-ok/70" />}
        </span>
        <span className="tnum shrink-0 font-mono text-xs text-ink-3">{fmtMs(num(s.duration) / 1e6)}</span>
      </div>
    )

  // LLM call
  const parts = partsOf(s.out)
  const reasoning = parts.filter((p) => p.type === 'reasoning').map((p) => str(p.content)).join('\n')
  const text = parts.filter((p) => p.type === 'text').map((p) => str(p.content)).join('\n')
  const calls = parts.filter((p) => p.type === 'tool_call')
  const inTok = num(s.input)
  return (
    <div id={`step-${s['span.id']}`} onClick={onSelect} className={clsx(base, 'items-start')}>
      <span className="tnum mt-0.5 w-16 shrink-0 font-mono text-2xs text-ink-4">{at}</span>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2 text-xs">
          <Brain className={clsx('size-4', failed ? 'text-crit' : 'text-[var(--s7)]')} />
          <span className="font-mono text-ink-2">{s.model}</span>
          <span className="tnum text-ink-3">
            {fmtMs(num(s.duration) / 1e6)} · TTFT {secs(num(s.ttft))} · {fmtTokens(inTok)} in{inTok ? ` (${fmtPct((100 * num(s.cached)) / inTok, 0)} cached)` : ''} · {fmtTokens(num(s.output))} out
          </span>
          {failed && (
            <Badge tone="crit">
              <AlertTriangle className="size-3" /> failed
            </Badge>
          )}
          {arr(s.finish).length > 0 && <span className="text-ink-4">→ {arr(s.finish).join(', ')}</span>}
        </div>
        {reasoning && (
          <Tip content="The model's reasoning (open the step for the full text)">
            <div className="mt-1 flex items-start gap-1.5 text-xs text-ink-3 italic">
              <ChevronRight className="mt-0.5 size-3 shrink-0" />
              <span className="line-clamp-2">{reasoning}</span>
            </div>
          </Tip>
        )}
        {text && <div className="mt-1 line-clamp-3 text-sm whitespace-pre-wrap text-ink">{tidy(text)}</div>}
        {calls.length > 0 && (
          <div className="mt-1.5 flex flex-wrap gap-1.5">
            {calls.map((c, i) => (
              <span key={i} className="inline-flex max-w-full items-center gap-1 rounded-md border border-line bg-sunken px-1.5 py-0.5 font-mono text-2xs text-ink-2">
                <Wrench className="size-3 shrink-0 text-[var(--s3)]" />
                <b className="font-medium">{c.name}</b>
                <span className="truncate text-ink-3">{argPreview(c.arguments)}</span>
              </span>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

function argPreview(a: unknown) {
  const s = typeof a === 'string' ? a : JSON.stringify(a)
  return (s ?? '').replace(/\s+/g, ' ').slice(0, 90)
}

/** Input tokens per LLM call (cached vs fresh): where context, cost and latency grow. */
function ContextGrowth({ llm, selected, onPick }: { llm: Rec[]; selected: Rec | null; onPick: (s: Rec) => void }) {
  const [hover, setHover] = useState<number | null>(null)
  const max = Math.max(1, ...llm.map((s) => num(s.input)))
  const h = 90
  return (
    <div className="mb-4 rounded-xl border border-line bg-panel p-3">
      <div className="mb-2 flex items-center gap-3 text-sm">
        <span className="font-medium">Context per LLM call</span>
        <span className="text-xs text-ink-3">input tokens sent with each call · click a bar to jump to the step</span>
        <span className="ml-auto flex items-center gap-3 text-xs text-ink-3">
          <span className="flex items-center gap-1.5">
            <i className="inline-block size-2 rounded-[2px] bg-accent/35" /> from prompt cache
          </span>
          <span className="flex items-center gap-1.5">
            <i className="inline-block size-2 rounded-[2px] bg-accent" /> fresh
          </span>
        </span>
      </div>
      <div className="relative flex items-end gap-[2px]" style={{ height: h }} onMouseLeave={() => setHover(null)}>
        {llm.map((s, i) => {
          const inT = num(s.input)
          const c = Math.min(num(s.cached), inT)
          const total = (inT / max) * h
          const cachedH = inT ? (c / inT) * total : 0
          return (
            <button
              type="button"
              key={s['span.id']}
              onClick={() => onPick(s)}
              onMouseEnter={() => setHover(i)}
              className={clsx('flex h-full max-w-6 min-w-[3px] flex-1 flex-col justify-end', selected === s && 'outline outline-1 outline-offset-1 outline-accent')}
            >
              <span className="block rounded-t-[3px] bg-accent" style={{ height: Math.max(1, total - cachedH) }} />
              <span className="block bg-accent/35" style={{ height: cachedH }} />
            </button>
          )
        })}
        {hover != null && (
          <div className="pointer-events-none absolute -top-2 z-10 -translate-y-full rounded-md bg-raised px-2.5 py-1.5 text-xs shadow-pop" style={{ left: `${(hover / llm.length) * 100}%` }}>
            <div className="text-ink-3">LLM call {hover + 1}</div>
            <div className="tnum">
              {fmtTokens(num(llm[hover].input))} in · {fmtTokens(num(llm[hover].cached))} cached · {fmtTokens(num(llm[hover].output))} out
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
