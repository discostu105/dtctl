import clsx from 'clsx'
import { CheckCircle2, ChevronRight, CircleDashed, ExternalLink, Wrench, XCircle } from 'lucide-react'
import { useMemo, type ReactNode } from 'react'
import { Link, useLocation } from 'wouter'
import { num, useDql, type Rec } from '../lib/api'
import { callDetailQuery } from '../lib/ai'
import { fmtMs } from '../lib/format'
import { highlightDql } from '../lib/highlight'
import { traceHref } from '../lib/links'
import { parseMessages } from './Conversation'
import { queryHref } from './Panel'
import { Inspector, SidePanel } from './signals'
import { Badge, CopyButton, Skeleton } from './ui'

// A tool call has three parts that live in three places:
//   the call      → the model's output message (tool_call part: name, id, arguments)
//   the execution → the execute_tool span (status, duration; usually no payload)
//   the result    → the NEXT model call's input (tool_call_response part with the same id)
// This panel joins them so "what did the agent run, and what came back" is one view.

export interface ToolUse {
  name: string
  args?: unknown
  callId?: string
  exec?: Rec
}

/** Human-readable tool arguments: `dtctl query "fetch …"` rather than raw JSON. */
export function argsText(a: unknown): string {
  const v = parseArgs(a)
  if (typeof v === 'string') return v
  if (v && typeof v === 'object' && !Array.isArray(v)) {
    if (typeof v.command === 'string' && Array.isArray(v.args))
      return [v.command, ...v.args.map((x: unknown) => (typeof x === 'string' && /\s/.test(x) ? JSON.stringify(x) : String(x)))].join(' ')
    const vals = Object.entries(v)
    if (vals.length <= 3 && vals.every(([, x]) => typeof x !== 'object'))
      return vals.map(([k, x]) => `${k}: ${typeof x === 'string' ? x : JSON.stringify(x)}`).join(' · ')
  }
  return JSON.stringify(v)
}

function parseArgs(a: unknown): any {
  if (typeof a !== 'string') return a
  try {
    return JSON.parse(a)
  } catch {
    return a
  }
}

/** The DQL inside a `dtctl query "…"` call, if that's what this is. */
function dqlOf(a: unknown): string | null {
  const v = parseArgs(a)
  if (v && typeof v === 'object' && Array.isArray(v.args)) {
    const args = v.args.map(String)
    const cmd = [String(v.command ?? ''), ...args]
    const qi = cmd.findIndex((x) => x === 'query')
    if (/dtctl$/.test(cmd[0]) && qi >= 0 && cmd[qi + 1]) return cmd[qi + 1]
  }
  const s = typeof v === 'string' ? v : ''
  const m = /dtctl\s+query\s+(["'])([\s\S]*)\1/.exec(s)
  return m ? m[2].replace(/\\"/g, '"') : null
}

export function ToolCallPanel({ use, next, onClose }: { use: ToolUse; next?: Rec; onClose: () => void }) {
  const [, navigate] = useLocation()
  const x = use.exec
  const failed = x?.['span.status_code'] === 'error'
  const dql = dqlOf(use.args)
  const cmd = use.args != null ? argsText(use.args) : ''
  const traceId = x?.['trace.id'] ?? next?.['trace.id']

  // the result: the next model call's input carries it, keyed by the call id
  const nextSpec = use.callId && next ? { query: callDetailQuery(next['trace.id'], next['span.id']), ttl: 600 } : null
  const nextRes = useDql(nextSpec)
  const result = useMemo(() => {
    const rec = nextRes.data?.records[0]
    if (!rec) return null
    for (const m of parseMessages(rec['gen_ai.input.messages']) as any[])
      for (const p of m.parts ?? []) if (p.type === 'tool_call_response' && p.id === use.callId) return String(typeof p.response === 'string' ? p.response : JSON.stringify(p.response ?? p.content, null, 2))
    return ''
  }, [nextRes.data, use.callId])

  return (
    <SidePanel
      title={
        <span className="flex items-center gap-2">
          <Wrench className="size-3.5 text-[var(--s3)]" />
          <span className="font-mono text-sm">{use.name}</span>
        </span>
      }
      onClose={onClose}
      actions={
        traceId && (
          <Link href={traceHref(traceId) + (x ? `?span=${x['span.id']}` : '')} className="rounded px-1.5 py-1 text-xs text-accent-ink hover:bg-accent-wash">
            Trace →
          </Link>
        )
      }
    >
      <div className="mb-3 flex flex-wrap items-center gap-2 text-xs">
        {!x ? (
          <Badge>
            <CircleDashed className="size-3" /> no execution span
          </Badge>
        ) : failed ? (
          <Badge tone="crit">
            <XCircle className="size-3" /> failed
          </Badge>
        ) : (
          <Badge tone="ok">
            <CheckCircle2 className="size-3" /> ok
          </Badge>
        )}
        {x && <span className="tnum text-ink-2">ran {fmtMs(num(x.duration) / 1e6)}</span>}
        {use.callId && <span className="font-mono text-2xs text-ink-4">{use.callId}</span>}
      </div>

      <Block title="Call" copy={cmd || undefined}>
        {dql ? (
          <>
            <div className="mb-1.5 font-mono text-2xs text-ink-3">dtctl query</div>
            <pre className="max-h-80 overflow-auto rounded-md bg-sunken p-2.5 font-mono text-xs leading-relaxed whitespace-pre-wrap">{highlightDql(dql)}</pre>
            <button
              type="button"
              onClick={() => navigate(queryHref({ query: dql }))}
              className="mt-2 inline-flex h-7 items-center gap-1.5 rounded-md bg-accent-wash px-2.5 text-xs font-medium text-accent-ink hover:brightness-110"
            >
              Run it in Query <ExternalLink className="size-3" />
            </button>
          </>
        ) : cmd ? (
          <pre className="max-h-80 overflow-auto rounded-md bg-sunken p-2.5 font-mono text-xs leading-relaxed whitespace-pre-wrap text-ink-2">{prettyArgs(use.args)}</pre>
        ) : (
          <div className="text-xs text-ink-3">The model's request wasn't captured; only the execution span exists.</div>
        )}
      </Block>

      <Block title="Result" copy={result || undefined}>
        {!nextSpec ? (
          <div className="text-xs text-ink-3">
            {use.callId ? 'This was the last model call, so no later message carries the result.' : 'No call id to match the result by.'}
          </div>
        ) : nextRes.isLoading ? (
          <Skeleton className="h-24" />
        ) : result ? (
          <ToolResult text={result} />
        ) : (
          <div className="text-xs text-ink-3">The next model call's input doesn't include this result (its messages weren't captured, or were compacted).</div>
        )}
      </Block>

      {x && (
        <details className="group/s rounded-lg border border-line">
          <summary className="flex cursor-pointer list-none items-center gap-1.5 px-3 py-2 text-xs font-medium text-ink-2 select-none">
            <ChevronRight className="size-3.5 text-ink-4 transition-transform group-open/s:rotate-90" />
            Execution span
          </summary>
          <div className="border-t border-line px-3 py-2">
            <Inspector rec={Object.fromEntries(Object.entries(x).filter(([, v]) => v != null && v !== ''))} />
          </div>
        </details>
      )}
    </SidePanel>
  )
}

function Block({ title, copy, children }: { title: string; copy?: string; children: ReactNode }) {
  return (
    <section className="mb-3 rounded-lg border border-line">
      <header className="flex items-center justify-between border-b border-line px-3 py-1.5">
        <span className="text-xs font-medium text-ink-2">{title}</span>
        {copy && <CopyButton value={copy} label={title.toLowerCase()} />}
      </header>
      <div className="p-3">{children}</div>
    </section>
  )
}

function prettyArgs(a: unknown) {
  const v = parseArgs(a)
  if (v && typeof v === 'object' && !(typeof v.command === 'string' && Array.isArray(v.args))) return JSON.stringify(v, null, 2)
  return argsText(a)
}

/**
 * Tool output as the agent saw it, minus the harness wrapper. dtctl's agent
 * envelope with CSV records becomes a table; other JSON is pretty-printed.
 */
function ToolResult({ text }: { text: string }) {
  const body = text
    .replace(/^Tool `[^`]+` returned the result below\.[^\n]*\n?/, '')
    .replace(/\[(BEGIN|END) UNTRUSTED[^\]]*\]\n?/g, '')
    .replace(/<\/?user_content>\n?/g, '')
    .trim()
  const lines = body.split('\n')
  const status = /^(exit=\S+.*)$/.exec(lines[0] ?? '')?.[1]
  const rest = status ? lines.slice(1).join('\n') : body
  let json: any = null
  try {
    json = JSON.parse(rest)
  } catch {
    /* not JSON */
  }
  const kv = status ? Object.fromEntries(status.split(/\s+/).map((p) => p.split('='))) : null
  const csv = json?.result?.encoding === 'csv' && typeof json.result.records === 'string' ? parseCsv(json.result.records) : null
  return (
    <div>
      {kv && (
        <div className="mb-2 flex flex-wrap gap-1.5 text-2xs">
          {Object.entries(kv).map(([k, v]) => (
            <span key={k} className={clsx('rounded px-1.5 py-0.5 font-mono', k === 'exit' && v !== '0' ? 'bg-crit-wash text-crit' : 'bg-line text-ink-2')}>
              {k} {String(v)}
            </span>
          ))}
          {json?.ok === false && <span className="rounded bg-crit-wash px-1.5 py-0.5 text-crit">ok: false</span>}
        </div>
      )}
      {csv ? (
        <div className="max-h-96 overflow-auto rounded-md border border-line">
          <table className="w-full text-left text-xs">
            <thead className="sticky top-0 bg-panel">
              <tr>
                {csv[0].map((h, i) => (
                  <th key={i} className="border-b border-line px-2 py-1 font-mono text-2xs font-medium whitespace-nowrap text-ink-3">
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {csv.slice(1).map((r, i) => (
                <tr key={i} className="border-b border-line/60 last:border-0">
                  {r.map((c, j) => (
                    <td key={j} className="tnum px-2 py-1 align-top font-mono whitespace-nowrap text-ink-2">
                      {c}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <pre className="max-h-[480px] overflow-auto rounded-md bg-sunken p-2.5 font-mono text-xs leading-relaxed whitespace-pre-wrap text-ink-2">
          {json ? JSON.stringify(json, null, 2) : rest}
        </pre>
      )}
      {csv && json?.result && Object.keys(json.result).some((k) => !['kind', 'encoding', 'records'].includes(k)) && (
        <pre className="mt-2 max-h-40 overflow-auto rounded-md bg-sunken p-2 font-mono text-2xs text-ink-3">
          {JSON.stringify(Object.fromEntries(Object.entries(json.result).filter(([k]) => !['kind', 'encoding', 'records'].includes(k))), null, 2)}
        </pre>
      )}
    </div>
  )
}

/** Small RFC-4180-ish CSV parser (quoted fields, escaped quotes). */
function parseCsv(s: string): string[][] {
  const rows: string[][] = []
  let row: string[] = []
  let cell = ''
  let quoted = false
  for (let i = 0; i < s.length; i++) {
    const c = s[i]
    if (quoted) {
      if (c === '"' && s[i + 1] === '"') {
        cell += '"'
        i++
      } else if (c === '"') quoted = false
      else cell += c
    } else if (c === '"') quoted = true
    else if (c === ',') {
      row.push(cell)
      cell = ''
    } else if (c === '\n') {
      row.push(cell)
      rows.push(row)
      row = []
      cell = ''
    } else cell += c
  }
  if (cell || row.length) {
    row.push(cell)
    rows.push(row)
  }
  return rows.filter((r) => r.length > 1 || r[0])
}
