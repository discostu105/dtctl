import clsx from 'clsx'
import { AlertTriangle, ArrowLeft, Bot, Brain, CheckCircle2, ChevronDown, ChevronRight, CircleDashed, MessagesSquare, User, Wrench, XCircle } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Link, useSearch } from 'wouter'
import { parseMessages } from '../components/Conversation'
import { EntityLink } from '../components/Entity'
import { Markdown } from '../components/Markdown'
import { QueryInfo } from '../components/Panel'
import { Spark } from '../components/Spark'
import { Badge, CopyButton, Empty, ErrorBox, Skeleton, Tip } from '../components/ui'
import { num, useDql, type Rec } from '../lib/api'
import { conversationPromptQuery, conversationStepsQuery, fmtTokens } from '../lib/ai'
import { fmtDateTime, fmtMs, fmtPct } from '../lib/format'
import { traceHref } from '../lib/links'
import { pushRecent, useTitle } from '../lib/store'
import { LlmCallPanel, secs } from './Ai'
import { argsText, ToolCallPanel, type ToolUse } from '../components/ToolCallPanel'

// ── model ─────────────────────────────────────────────────────────────────
// A conversation is a sequence of TURNS. Each turn is one LLM call: what the
// model thought, what it said, and the tools it asked for, each paired with
// the execution that answered it. Child spans (the HTTP call to the model
// provider, the tool proxy) carry no gen_ai operation and are left out.

interface Part {
  type?: string
  content?: unknown
  name?: string
  id?: string
  arguments?: unknown
}

interface Turn {
  n: number
  call: Rec
  start: number
  end: number
  reasoning: string
  text: string
  tools: ToolUse[]
}

const t = (s: unknown) => Date.parse(String(s))
const str = (v: unknown) => (typeof v === 'string' ? v : JSON.stringify(v))
const partsOf = (raw: unknown): Part[] => parseMessages(raw).flatMap((m: any) => (m.parts ?? []) as Part[])
const failed = (s?: Rec) => s?.['span.status_code'] === 'error'

/** Strip harness markers but keep the text's own line breaks. */
function tidy(s: string) {
  return s
    .replace(/\[sent:[^\]]*\]/g, '')
    .replace(/\[(BEGIN|END) UNTRUSTED[^\]]*\]/g, '')
    .replace(/<\/?user_content>/g, '')
    .replace(/\n{3,}/g, '\n\n')
    .trim()
}

function buildTurns(steps: Rec[]) {
  const llm = steps.filter((s) => s.op === 'chat' && (s.model || s.input != null))
  const execs = steps.filter((s) => s.op === 'execute_tool')
  const used = new Set<Rec>()
  const turns: Turn[] = llm.map((call, i) => {
    const start = t(call.start_time)
    const nextStart = i + 1 < llm.length ? t(llm[i + 1].start_time) : Infinity
    const parts = partsOf(call.out)
    const window = execs.filter((x) => t(x.start_time) >= start && t(x.start_time) < nextStart)
    // Pair each requested tool call with the next unused execution of that tool.
    const tools: ToolUse[] = parts
      .filter((p) => p.type === 'tool_call')
      .map((p) => {
        const exec = window.find((x) => !used.has(x) && (x.tool === p.name || String(x['span.name'] ?? '').includes(` ${p.name}`)))
        if (exec) used.add(exec)
        return { name: p.name ?? 'tool', args: p.arguments, callId: p.id, exec }
      })
    for (const x of window) if (!used.has(x)) (used.add(x), tools.push({ name: x.tool ?? 'tool', exec: x }))
    return {
      n: i + 1,
      call,
      start,
      end: t(call.end_time),
      reasoning: parts.filter((p) => p.type === 'reasoning').map((p) => str(p.content)).join('\n'),
      text: tidy(parts.filter((p) => p.type === 'text').map((p) => str(p.content)).join('\n')),
      tools,
    }
  })
  const setup = execs.filter((x) => !used.has(x)) // e.g. an init tool before the first call
  return { turns, setup, llm, execs }
}

/**
 * The run's real answer. Agents often deliver it as a terminal tool call
 * (complete_run, submit, final_answer…) whose argument carries the text;
 * otherwise it's the last thing the model said.
 */
function finalAnswer(turns: Turn[]): { text: string; via?: string } | null {
  const KEYS = ['summary', 'answer', 'final_answer', 'result', 'report', 'response', 'output', 'message', 'content']
  for (const tr of turns.slice(-3).reverse())
    for (const u of [...tr.tools].reverse()) {
      let a: any = u.args
      if (typeof a === 'string') {
        try {
          a = JSON.parse(a)
        } catch {
          continue
        }
      }
      if (!a || typeof a !== 'object') continue
      for (const k of KEYS) if (typeof a[k] === 'string' && a[k].length > 60) return { text: tidy(a[k]), via: `${u.name}.${k}` }
    }
  const last = [...turns].reverse().find((x) => x.text)
  return last ? { text: last.text } : null
}

// ── page ──────────────────────────────────────────────────────────────────

export default function AiConversation({ id }: { id: string }) {
  // links carry the start time (?t=): read a narrow window, fall back to 7 days if it isn't there
  const hint = new URLSearchParams(useSearch()).get('t')
  const [wide, setWide] = useState(!hint)
  const at = wide ? null : hint
  const stepsSpec = { query: conversationStepsQuery(id, at), ttl: 120, maxRecords: 3000 }
  const res = useDql(stepsSpec)
  useEffect(() => {
    if (!wide && res.data && res.data.records.length === 0) setWide(true)
  }, [wide, res.data])
  const promptRes = useDql({ query: conversationPromptQuery(id, at), ttl: 600 })
  const steps = useMemo(() => (res.data?.records ?? []).filter((s) => ['chat', 'execute_tool', 'invoke_agent'].includes(s.op)), [res.data])
  const { turns, setup, llm, execs } = useMemo(() => buildTurns(steps), [steps])
  const [sel, setSel] = useState<{ kind: 'llm'; rec: Rec } | { kind: 'tool'; use: ToolUse; next?: Rec } | null>(null)
  const [focus, setFocus] = useState<number | null>(null)

  const prompt = useMemo(() => {
    const msgs = parseMessages(promptRes.data?.records[0]?.['gen_ai.input.messages'])
    const users = msgs.filter((m: any) => m.role === 'user')
    const last = users[users.length - 1] as any
    return last ? tidy((last.parts ?? []).filter((p: Part) => p.type === 'text').map((p: Part) => str(p.content)).join('\n')) : ''
  }, [promptRes.data])
  const final = useMemo(() => finalAnswer(turns), [turns])

  const t0 = steps.length ? Math.min(...steps.map((s) => t(s.start_time))) : 0
  const t1 = steps.length ? Math.max(...steps.map((s) => t(s.end_time) || 0)) : 0
  const total = Math.max(1, t1 - t0)
  const llmMs = llm.reduce((a, s) => a + num(s.duration) / 1e6, 0)
  const toolMs = execs.reduce((a, s) => a + num(s.duration) / 1e6, 0)
  const inTok = llm.reduce((a, s) => a + num(s.input), 0)
  const cached = llm.reduce((a, s) => a + num(s.cached), 0)
  const outTok = llm.reduce((a, s) => a + num(s.output), 0)
  const failedTools = execs.filter(failed)
  const traces = [...new Set(steps.map((s) => s['trace.id']).filter(Boolean))]
  const agents = [...new Set(steps.map((s) => s.agent).filter(Boolean))]
  const models = [...new Set(llm.map((s) => s.model).filter(Boolean))]
  const services = [...new Map(steps.filter((s) => s.service_id).map((s) => [s.service_id, s.service])).entries()]
  const firstLine = prompt.split('\n').find((l) => l.trim()) ?? ''

  useTitle(firstLine ? `Conversation · ${firstLine.slice(0, 60)}` : 'Conversation')
  useEffect(() => {
    if (steps.length) pushRecent({ href: `/ai/conversations/${id}`, label: firstLine.slice(0, 80) || id, kind: 'Conversation' })
  }, [steps.length, id, firstLine])

  const jump = (n: number) => {
    setFocus(n)
    document.getElementById(`turn-${n}`)?.scrollIntoView({ block: 'start', behavior: 'smooth' })
  }

  if (res.error) return <ErrorBox error={res.error} />
  if (res.isLoading)
    return (
      <div className="p-5">
        <Skeleton className="mb-3 h-7 w-[520px]" />
        <Skeleton className="mb-4 h-32" />
        <Skeleton className="h-96" />
      </div>
    )
  if (!steps.length) return <Empty icon={<MessagesSquare className="size-5" />} title="Conversation not found" hint="No GenAI spans with this conversation ID in the last 7 days." />

  return (
    <div className="flex h-full">
      <div className="min-w-0 flex-1 overflow-auto p-5">
        <Link href="/ai" className="mb-3 inline-flex items-center gap-1 text-xs text-ink-3 hover:text-ink-2">
          <ArrowLeft className="size-3.5" /> AI
        </Link>

        {/* header */}
        <div className="mb-4 flex flex-wrap items-start gap-4">
          <div className={clsx('flex size-10 items-center justify-center rounded-xl', failedTools.length ? 'bg-warn-wash text-warn' : 'bg-accent-wash text-accent-ink')}>
            <MessagesSquare className="size-5" />
          </div>
          <div className="min-w-0 flex-1">
            <h1 className="line-clamp-2 text-xl font-semibold tracking-tight">{firstLine || 'Conversation'}</h1>
            <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-ink-3">
              <span>{fmtDateTime(t0)}</span>
              {agents.map((a) => (
                <Badge key={a} tone="accent">
                  <Bot className="size-3" /> {a}
                </Badge>
              ))}
              {services.map(([sid, name]) => (
                <EntityLink key={sid} id={sid} name={name} type="SERVICE" />
              ))}
              <span className="font-mono text-xs">{models.join(', ')}</span>
              <span className="inline-flex items-center gap-1 font-mono text-2xs text-ink-4">
                {id}
                <CopyButton value={id} label="conversation ID" />
              </span>
            </div>
          </div>
          <div className="flex flex-wrap gap-1.5">
            {traces.slice(0, 3).map((tr, i) => (
              <Link key={tr} href={traceHref(tr, steps.find((x) => x['trace.id'] === tr)?.start_time)} className="inline-flex h-8 items-center rounded-lg border border-line bg-sunken px-3 text-sm text-ink-2 hover:border-line-strong hover:text-ink">
                {traces.length > 1 ? `Trace ${i + 1}` : 'Open trace'} →
              </Link>
            ))}
          </div>
        </div>

        {/* outcome first: what was asked, what came back */}
        <div className="mb-4 grid grid-cols-2 gap-3 max-xl:grid-cols-1">
          <Card icon={<User className="size-4 text-[var(--s1)]" />} title="Asked">
            {prompt ? <Clamp text={prompt} lines={8} /> : <span className="text-sm text-ink-4">No user prompt captured.</span>}
          </Card>
          <Card
            icon={<Bot className="size-4 text-accent-ink" />}
            title={
              <>
                {final?.via ? 'Final answer' : 'Last message'} · after {turns.length} turn{turns.length === 1 ? '' : 's'} · {fmtMs(total)}
                {final?.via && <span className="font-mono font-normal text-ink-4">via {final.via}</span>}
              </>
            }
          >
            {final ? <Clamp text={final.text} lines={8} markdown /> : <span className="text-sm text-ink-4">No answer text captured.</span>}
          </Card>
        </div>

        {/* where the time and tokens went */}
        <div className="mb-4 rounded-xl border border-line bg-panel p-3">
          <div className="mb-2 flex flex-wrap items-center gap-x-5 gap-y-1 text-xs text-ink-3">
            <span className="text-sm font-medium text-ink">Run timeline</span>
            <Legend color="bg-[var(--s7)]" label={`thinking (LLM) ${fmtMs(llmMs)} · ${fmtPct((100 * llmMs) / total, 0)}`} />
            <Legend color="bg-[var(--s3)]" label={`tools ${fmtMs(toolMs)} · ${fmtPct((100 * toolMs) / total, 0)}`} />
            {failedTools.length > 0 && <Legend color="bg-crit" label={`${failedTools.length} failed tool call${failedTools.length === 1 ? '' : 's'}`} />}
            <span className="ml-auto flex items-center gap-4">
              <span className="tnum">
                {fmtTokens(inTok)} in · {inTok ? fmtPct((100 * cached) / inTok, 0) : '—'} cached · {fmtTokens(outTok)} out
              </span>
              {llm.length > 1 && (
                <Tip content="Input tokens per turn: the context the model re-reads each time">
                  <span className="flex items-center gap-1.5">
                    context <Spark values={llm.map((s) => num(s.input))} width={90} height={18} color="var(--s7)" />
                    {fmtTokens(num(llm[0].input))} → {fmtTokens(num(llm[llm.length - 1].input))}
                  </span>
                </Tip>
              )}
            </span>
          </div>
          <Gantt turns={turns} setup={setup} t0={t0} total={total} focus={focus} onPick={jump} />
        </div>

        {/* the turns */}
        <div className="flex items-center gap-3 pb-2">
          <span className="text-sm font-medium">How it got there</span>
          <span className="text-xs text-ink-3">
            {turns.length} turns · {execs.length} tool runs{failedTools.length ? ` · ${failedTools.length} failed` : ''}
          </span>
          <span className="ml-auto" />
          <QueryInfo spec={stepsSpec} result={res} />
        </div>
        <div className="flex flex-col gap-2">
          {setup.length > 0 && (
            <div className="flex flex-wrap items-center gap-2 rounded-xl border border-dashed border-line px-3 py-2 text-xs text-ink-3">
              Setup
              {setup.map((x) => (
                <ToolRow key={x['span.id']} use={{ name: x.tool ?? 'tool', exec: x }} compact onOpen={() => setSel({ kind: 'tool', use: { name: x.tool ?? 'tool', exec: x } })} />
              ))}
            </div>
          )}
          {turns.map((turn) => (
            <TurnCard
              key={turn.n}
              turn={turn}
              t0={t0}
              total={turns.length}
              focused={focus === turn.n}
              onOpenCall={() => setSel({ kind: 'llm', rec: turn.call })}
              onOpenTool={(use) => setSel({ kind: 'tool', use, next: turns[turn.n]?.call })}
            />
          ))}
        </div>
      </div>

      {sel?.kind === 'llm' && <LlmCallPanel traceId={sel.rec['trace.id']} spanId={sel.rec['span.id']} title={sel.rec.model} at={sel.rec.start_time} onClose={() => setSel(null)} />}
      {sel?.kind === 'tool' && <ToolCallPanel key={`${sel.use.callId}-${sel.use.exec?.['span.id']}`} use={sel.use} next={sel.next} onClose={() => setSel(null)} />}
    </div>
  )
}

// ── pieces ────────────────────────────────────────────────────────────────

function Card({ icon, title, children }: { icon: ReactNode; title: ReactNode; children: ReactNode }) {
  return (
    <div className="rounded-xl border border-line bg-panel p-3.5">
      <div className="mb-1.5 flex items-center gap-2 text-xs font-medium text-ink-2">
        {icon}
        {title}
      </div>
      {children}
    </div>
  )
}

function Legend({ color, label }: { color: string; label: string }) {
  return (
    <span className="flex items-center gap-1.5">
      <i className={clsx('inline-block h-2 w-3 rounded-[2px]', color)} />
      {label}
    </span>
  )
}

function Clamp({ text, lines, markdown }: { text: string; lines: number; markdown?: boolean }) {
  const [open, setOpen] = useState(false)
  const long = text.split('\n').length > lines || text.length > lines * 110
  return (
    <div>
      <div className={clsx(!open && long && 'overflow-hidden [mask-image:linear-gradient(black_65%,transparent)]')} style={!open && long ? { maxHeight: `${lines * 1.5}rem` } : undefined}>
        {markdown ? <Markdown text={text} className="text-ink" /> : <div className="text-sm leading-relaxed break-words whitespace-pre-wrap text-ink">{text}</div>}
      </div>
      {long && (
        <button type="button" onClick={() => setOpen(!open)} className="mt-1 text-xs text-accent-ink hover:underline">
          {open ? 'Show less' : 'Show all'}
        </button>
      )}
    </div>
  )
}

/** Time axis of the whole run: LLM turns on one lane, tool runs on the other. */
function Gantt({ turns, setup, t0, total, focus, onPick }: { turns: Turn[]; setup: Rec[]; t0: number; total: number; focus: number | null; onPick: (n: number) => void }) {
  const [hover, setHover] = useState<{ x: number; label: string } | null>(null)
  const ref = useRef<HTMLDivElement>(null)
  const pos = (start: number, dur: number) => ({ left: `${((start - t0) / total) * 100}%`, width: `max(2px, ${(dur / total) * 100}%)` })
  const show = (e: React.MouseEvent, label: string) => {
    const r = ref.current?.getBoundingClientRect()
    if (r) setHover({ x: e.clientX - r.left, label })
  }
  return (
    <div ref={ref} className="relative select-none" onMouseLeave={() => setHover(null)}>
      <div className="relative h-5 rounded bg-sunken">
        {turns.map((tr) => (
          <button
            type="button"
            key={tr.n}
            onClick={() => onPick(tr.n)}
            onMouseMove={(e) => show(e, `Turn ${tr.n} · ${tr.call.model} · ${fmtMs(tr.end - tr.start)}`)}
            className={clsx('absolute top-0.5 bottom-0.5 rounded-[3px] bg-[var(--s7)] hover:brightness-125', focus === tr.n && 'ring-2 ring-accent ring-offset-1 ring-offset-panel')}
            style={pos(tr.start, tr.end - tr.start)}
          />
        ))}
      </div>
      <div className="relative mt-1 h-4 rounded bg-sunken">
        {[...setup, ...turns.flatMap((tr) => tr.tools.map((u) => u.exec).filter(Boolean) as Rec[])].map((x) => {
          const turn = turns.find((tr) => tr.tools.some((u) => u.exec === x))
          return (
            <button
              type="button"
              key={x['span.id']}
              onClick={() => turn && onPick(turn.n)}
              onMouseMove={(e) => show(e, `${String(x['span.name'] ?? x.tool).replace(/^execute_tool\s+/, '')} · ${fmtMs(num(x.duration) / 1e6)}${failed(x) ? ' · failed' : ''}`)}
              className={clsx('absolute top-0.5 bottom-0.5 rounded-[2px] hover:brightness-125', failed(x) ? 'bg-crit' : 'bg-[var(--s3)]')}
              style={pos(t(x.start_time), num(x.duration) / 1e6)}
            />
          )
        })}
      </div>
      <div className="mt-1 flex justify-between text-2xs text-ink-4">
        <span>0</span>
        <span>{fmtMs(total / 2)}</span>
        <span>{fmtMs(total)}</span>
      </div>
      {hover && (
        <div className="pointer-events-none absolute -top-2 z-10 -translate-y-full rounded-md bg-raised px-2 py-1 text-xs whitespace-nowrap shadow-pop" style={{ left: Math.min(hover.x, (ref.current?.clientWidth ?? 400) - 260) }}>
          {hover.label}
        </div>
      )}
    </div>
  )
}

function TurnCard({ turn, t0, total, focused, onOpenCall, onOpenTool }: { turn: Turn; t0: number; total: number; focused: boolean; onOpenCall: () => void; onOpenTool: (u: ToolUse) => void }) {
  const [thoughts, setThoughts] = useState(false)
  const c = turn.call
  const inTok = num(c.input)
  const bad = failed(c) || turn.tools.some((u) => failed(u.exec))
  const last = turn.n === total
  return (
    <div id={`turn-${turn.n}`} className={clsx('scroll-mt-4 rounded-xl border bg-panel transition-colors', focused ? 'border-accent/60' : 'border-line', bad && !focused && 'border-crit/30')}>
      {/* turn header: click for the full message exchange */}
      <button type="button" onClick={onOpenCall} className="flex w-full items-center gap-2 rounded-t-xl px-3 py-2 text-left text-xs hover:bg-panel-hover">
        <span className={clsx('flex size-6 shrink-0 items-center justify-center rounded-full text-2xs font-semibold', last ? 'bg-accent text-white' : 'bg-[var(--s7)]/15 text-[var(--s7)]')}>{turn.n}</span>
        <Brain className="size-3.5 text-[var(--s7)]" />
        <span className="font-mono text-ink-2">{c.model}</span>
        <span className="tnum text-ink-3">
          thought {fmtMs(turn.end - turn.start)} · first token {secs(num(c.ttft))} · {fmtTokens(inTok)} in{inTok ? ` (${fmtPct((100 * num(c.cached)) / inTok, 0)} cached)` : ''} · {fmtTokens(num(c.output))} out
        </span>
        {failed(c) && (
          <Badge tone="crit">
            <AlertTriangle className="size-3" /> LLM call failed
          </Badge>
        )}
        <span className="tnum ml-auto font-mono text-2xs text-ink-4">+{fmtMs(turn.start - t0)}</span>
        <ChevronRight className="size-3.5 text-ink-4" />
      </button>

      <div className="flex flex-col gap-2 border-t border-line px-3 py-2.5 pl-11">
        {turn.reasoning && (
          <button type="button" onClick={() => setThoughts(!thoughts)} className="flex items-start gap-1.5 text-left text-xs text-ink-3 hover:text-ink-2">
            {thoughts ? <ChevronDown className="mt-0.5 size-3 shrink-0" /> : <ChevronRight className="mt-0.5 size-3 shrink-0" />}
            <span className={clsx('italic', !thoughts && 'line-clamp-1')}>
              <span className="not-italic text-ink-4">Thinking: </span>
              {turn.reasoning}
            </span>
          </button>
        )}
        {turn.text && (
          <div className={clsx(!last && 'max-h-40 overflow-hidden [mask-image:linear-gradient(black_70%,transparent)]')}>
            <Markdown text={turn.text} className="text-ink" />
          </div>
        )}
        {turn.tools.length > 0 && (
          <div className="flex flex-col gap-0.5">
            {groupTools(turn.tools).map((g, i) =>
              g.length > 1 ? (
                <ToolGroup key={i} uses={g} onOpen={onOpenTool} />
              ) : (
                <ToolRow key={i} use={g[0]} onOpen={() => onOpenTool(g[0])} />
              ),
            )}
          </div>
        )}
        {!turn.text && !turn.tools.length && !turn.reasoning && <span className="text-xs text-ink-4">No message content captured for this call.</span>}
      </div>
    </div>
  )
}

/** Consecutive calls of the same tool that left no span collapse into one row. */
function groupTools(tools: ToolUse[]): ToolUse[][] {
  const out: ToolUse[][] = []
  for (const u of tools) {
    const g = out[out.length - 1]
    if (g && !u.exec && !g[0].exec && g[0].name === u.name) g.push(u)
    else out.push([u])
  }
  return out
}

function ToolGroup({ uses, onOpen }: { uses: ToolUse[]; onOpen: (u: ToolUse) => void }) {
  const [open, setOpen] = useState(false)
  return (
    <div>
      <button type="button" onClick={() => setOpen(!open)} className="flex w-full items-center gap-2 rounded-md px-2 py-1 text-left text-xs hover:bg-panel-hover">
        <Tip content="Requested by the model; no execution spans were recorded (handled in-process)">
          <CircleDashed className="size-3.5 shrink-0 text-ink-4" />
        </Tip>
        <Wrench className="size-3 shrink-0 text-[var(--s3)]" />
        <span className="shrink-0 font-mono font-medium text-ink">
          {uses[0].name} <span className="text-ink-3">×{uses.length}</span>
        </span>
        <span className="min-w-0 flex-1 truncate font-mono text-ink-3" title={uses.map((u) => argsText(u.args)).join('\n')}>
          {uses.map((u) => argsText(u.args).replace(/^title: /, '')).join(' · ')}
        </span>
        {open ? <ChevronDown className="size-3.5 shrink-0 text-ink-4" /> : <ChevronRight className="size-3.5 shrink-0 text-ink-4" />}
      </button>
      {open && (
        <div className="ml-5 border-l border-line pl-1">
          {uses.map((u, i) => (
            <ToolRow key={i} use={u} onOpen={() => onOpen(u)} />
          ))}
        </div>
      )}
    </div>
  )
}

/** A requested tool call joined with its execution: status, command, duration. */
function ToolRow({ use, onOpen, compact }: { use: ToolUse; onOpen: () => void; compact?: boolean }) {
  const x = use.exec
  const args = use.args != null ? argsText(use.args) : String(x?.['span.name'] ?? '').replace(/^execute_tool\s+[^:]*:?/, '')
  const icon = !x ? (
    <Tip content="Requested by the model; no execution span was recorded (handled in-process)">
      <CircleDashed className="size-3.5 shrink-0 text-ink-4" />
    </Tip>
  ) : failed(x) ? (
    <XCircle className="size-3.5 shrink-0 text-crit" />
  ) : (
    <CheckCircle2 className="size-3.5 shrink-0 text-ok" />
  )
  if (compact)
    return (
      <button type="button" onClick={onOpen} className="inline-flex items-center gap-1.5 rounded-md bg-sunken px-2 py-0.5 font-mono text-2xs text-ink-2 hover:text-ink">
        {icon}
        {use.name}
        {x && <span className="text-ink-4">{fmtMs(num(x.duration) / 1e6)}</span>}
      </button>
    )
  return (
    <button
      type="button"
      onClick={onOpen}
      className={clsx('group flex w-full items-center gap-2 rounded-md px-2 py-1 text-left text-xs hover:bg-panel-hover', failed(x) && 'bg-crit-wash/50')}
    >
      {icon}
      <Wrench className="size-3 shrink-0 text-[var(--s3)]" />
      <span className="shrink-0 font-mono font-medium text-ink">{use.name}</span>
      <span className="min-w-0 flex-1 truncate font-mono text-ink-3" title={args}>
        {args}
      </span>
      {x ? <span className="tnum shrink-0 font-mono text-ink-3">{fmtMs(num(x.duration) / 1e6)}</span> : <span className="shrink-0 text-2xs text-ink-4">no span</span>}
    </button>
  )
}

