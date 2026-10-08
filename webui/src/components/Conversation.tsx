import clsx from 'clsx'
import { Bot, Brain, ChevronRight, Cog, Reply, User, Wrench } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { CopyButton } from './ui'

// Renders OpenTelemetry GenAI messages (gen_ai.input.messages /
// gen_ai.output.messages): role-tagged turns whose parts are text,
// reasoning, tool_call or tool_call_response.

interface Part {
  type?: string
  content?: unknown
  name?: string
  id?: string
  arguments?: unknown
  response?: unknown
  result?: unknown
}
interface Message {
  role?: string
  parts?: Part[]
  finish_reason?: string
}

export function parseMessages(v: unknown): Message[] {
  if (Array.isArray(v)) return v as Message[]
  if (typeof v !== 'string' || !v.trim()) return []
  try {
    const m = JSON.parse(v)
    return Array.isArray(m) ? m : [m]
  } catch {
    return [{ role: 'unknown', parts: [{ type: 'text', content: v }] }]
  }
}

const ROLE: Record<string, { icon: typeof User; label: string; cls: string }> = {
  system: { icon: Cog, label: 'System', cls: 'text-ink-3' },
  user: { icon: User, label: 'User', cls: 'text-[var(--s1)]' },
  assistant: { icon: Bot, label: 'Assistant', cls: 'text-accent-ink' },
  tool: { icon: Wrench, label: 'Tool', cls: 'text-[var(--s3)]' },
}

const str = (v: unknown) => (typeof v === 'string' ? v : JSON.stringify(v, null, 2))

function Collapsible({ title, body, icon, defaultOpen, tone }: { title: ReactNode; body: string; icon?: ReactNode; defaultOpen?: boolean; tone?: string }) {
  const long = body.length > 600 || body.split('\n').length > 12
  const [open, setOpen] = useState(defaultOpen ?? !long)
  return (
    <div className={clsx('rounded-lg border border-line bg-sunken', tone)}>
      <button type="button" onClick={() => setOpen(!open)} className="flex w-full items-center gap-1.5 px-2.5 py-1.5 text-left text-xs text-ink-2">
        <ChevronRight className={clsx('size-3.5 shrink-0 transition-transform', open && 'rotate-90')} />
        {icon}
        <span className="min-w-0 flex-1 truncate">{title}</span>
        {!open && <span className="shrink-0 text-ink-4">{body.length.toLocaleString()} chars</span>}
      </button>
      {open && (
        <div className="relative border-t border-line">
          <CopyButton value={body} className="absolute top-1 right-1" />
          <pre className="max-h-96 overflow-auto p-2.5 pr-8 font-mono text-xs leading-relaxed break-words whitespace-pre-wrap text-ink-2">{body}</pre>
        </div>
      )}
    </div>
  )
}

function PartView({ p, collapsed }: { p: Part; collapsed?: boolean }) {
  switch (p.type) {
    case 'text':
      if (collapsed) return <Collapsible title={firstLine(str(p.content))} body={str(p.content)} defaultOpen={false} />
      return <div className="text-sm leading-relaxed break-words whitespace-pre-wrap text-ink">{str(p.content)}</div>
    case 'reasoning':
      return <Collapsible title="Reasoning" icon={<Brain className="size-3.5 text-[var(--s7)]" />} body={str(p.content)} defaultOpen={false} />
    case 'tool_call':
      return (
        <Collapsible
          title={
            <>
              Calls <b className="font-mono font-medium text-ink">{p.name}</b>
            </>
          }
          icon={<Wrench className="size-3.5 text-[var(--s3)]" />}
          body={prettyArgs(p.arguments)}
        />
      )
    case 'tool_call_response':
      return <Collapsible title={<>Tool result {p.id && <span className="font-mono text-ink-4">{p.id.slice(-8)}</span>}</>} icon={<Reply className="size-3.5 text-[var(--s3)]" />} body={str(p.response ?? p.result ?? p.content)} />
  }
  return <Collapsible title={p.type ?? 'part'} body={str(p)} />
}

function firstLine(s: string) {
  const l = s.split('\n').find((x) => x.trim()) ?? ''
  return l.length > 90 ? l.slice(0, 90) + '…' : l
}

function prettyArgs(a: unknown) {
  if (typeof a !== 'string') return JSON.stringify(a, null, 2)
  try {
    return JSON.stringify(JSON.parse(a), null, 2)
  } catch {
    return a
  }
}

export function Conversation({ messages, label }: { messages: Message[]; label?: string }) {
  if (!messages.length) return null
  return (
    <div className="flex flex-col gap-3">
      {label && <div className="text-2xs font-medium tracking-wide text-ink-3 uppercase">{label}</div>}
      {messages.map((m, i) => {
        const r = ROLE[m.role ?? ''] ?? { icon: Bot, label: m.role ?? 'message', cls: 'text-ink-3' }
        return (
          <div key={i} className="flex gap-2.5">
            <r.icon className={clsx('mt-0.5 size-4 shrink-0', r.cls)} />
            <div className="flex min-w-0 flex-1 flex-col gap-1.5">
              <div className="flex items-center gap-2 text-xs">
                <span className={clsx('font-medium', r.cls)}>{r.label}</span>
                {m.finish_reason && <span className="text-ink-4">finish: {m.finish_reason}</span>}
              </div>
              {(m.parts ?? []).map((p, j) => (
                <PartView key={j} p={p} collapsed={m.role === 'system'} />
              ))}
            </div>
          </div>
        )
      })}
    </div>
  )
}
