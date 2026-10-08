import * as Popover from '@radix-ui/react-popover'
import { useIsFetching, useQuery } from '@tanstack/react-query'
import clsx from 'clsx'
import { Activity as ActivityIcon, X } from 'lucide-react'
import { useState } from 'react'
import { useLocation } from 'wouter'
import { getJSON } from '../lib/api'
import { fmtBytes, fmtCompact, fmtMs } from '../lib/format'
import { queryHref } from './Panel'
import { Tip } from './ui'

interface ActiveQuery {
  key: string
  query: string
  state: 'queued' | 'running'
  queuedAt: string
  startedAt?: string
  waiters: number
}
interface FinishedQuery {
  query: string
  at: string
  elapsedMs: number
  queuedMs?: number
  executionMs?: number
  scannedBytes?: number
  records: number
  outcome: 'ok' | 'shared' | 'error' | 'cancelled'
  error?: string
}
interface ActivityData {
  maxConcurrent: number
  running: ActiveQuery[]
  queued: ActiveQuery[]
  recent: FinishedQuery[]
  totals: { requested: number; executed: number; cached: number; shared: number; errors: number; cancelled: number; scannedBytes: number }
}

const H = { 'X-Dtctl-Web': '1', 'Content-Type': 'application/json' }
const firstLine = (q: string) => q.replace(/\s+/g, ' ').trim()
const since = (t?: string) => (t ? Date.now() - Date.parse(t) : 0)

/**
 * Top-bar indicator of what the server is doing with Grail: queries running
 * (out of the concurrency slots), queued behind them, and recent executions,
 * so "everything is stuck" has a visible cause and a cancel button.
 */
export function QueryActivity() {
  const [open, setOpen] = useState(false)
  const clientBusy = useIsFetching({ queryKey: ['dql'] }) > 0
  const { data: a } = useQuery({
    queryKey: ['activity'],
    queryFn: () => getJSON<ActivityData>('/api/activity'),
    refetchInterval: open || clientBusy ? 1000 : 5000,
    refetchIntervalInBackground: false,
  })
  const running = a?.running.length ?? 0
  const queued = a?.queued.length ?? 0
  const busy = running + queued > 0
  const full = !!a && running >= a.maxConcurrent && queued > 0

  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Tip content={busy ? `${running} running · ${queued} queued` : 'Query activity'}>
        <Popover.Trigger asChild>
          <button
            type="button"
            className={clsx(
              'inline-flex h-8 items-center gap-1.5 rounded-lg border px-2 text-xs tabular-nums',
              full ? 'border-warn/40 bg-warn-wash text-warn' : busy ? 'border-accent/30 bg-accent-wash text-accent-ink' : 'border-line bg-sunken text-ink-3 hover:text-ink-2',
            )}
            aria-label="Query activity"
          >
            <ActivityIcon className={clsx('size-3.5', busy && 'animate-pulse')} />
            {busy && (
              <span>
                {running}
                {queued > 0 && <span className="opacity-80"> +{queued} queued</span>}
              </span>
            )}
          </button>
        </Popover.Trigger>
      </Tip>
      <Popover.Portal>
        <Popover.Content align="end" sideOffset={6} className="anim-pop z-50 w-[min(620px,92vw)] overflow-hidden rounded-lg bg-raised text-sm shadow-pop">
          {a ? <Panel a={a} /> : <div className="p-4 text-ink-3">Loading…</div>}
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  )
}

function Panel({ a }: { a: ActivityData }) {
  const [, navigate] = useLocation()
  const t = a.totals
  const served = t.cached + t.shared
  const oldest = a.running[0]
  const cancel = (key: string) => fetch('/api/activity/cancel', { method: 'POST', headers: H, body: JSON.stringify({ key }) })
  return (
    <div>
      <div className="border-b border-line px-4 py-3">
        <div className="flex items-baseline justify-between">
          <div className="font-medium">Queries</div>
          <div className="text-2xs text-ink-3">since start · this tenant</div>
        </div>
        <div className="tnum mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-xs text-ink-3">
          <span>
            <b className="text-ink">{fmtCompact(t.requested)}</b> requested
          </span>
          <span>
            <b className="text-ink">{fmtCompact(t.executed)}</b> ran on Grail
          </span>
          <span>
            <b className="text-ink">{t.requested ? Math.round((100 * served) / t.requested) : 0}%</b> from cache or shared
          </span>
          {t.cancelled > 0 && <span>{fmtCompact(t.cancelled)} cancelled</span>}
          {t.errors > 0 && <span className="text-crit">{fmtCompact(t.errors)} failed</span>}
          <span>{fmtBytes(t.scannedBytes)} scanned</span>
        </div>
        {/* slots */}
        <div className="mt-2.5 flex items-center gap-2">
          <div className="flex gap-1">
            {Array.from({ length: a.maxConcurrent }, (_, i) => (
              <span key={i} className={clsx('h-2 w-5 rounded-sm', i < a.running.length ? 'bg-accent' : 'bg-line')} />
            ))}
          </div>
          <span className="tnum text-2xs text-ink-3">
            {a.running.length}/{a.maxConcurrent} slots{a.queued.length ? ` · ${a.queued.length} waiting` : ''}
          </span>
        </div>
        {a.queued.length > 0 && a.running.length >= a.maxConcurrent && oldest && (
          <div className="mt-2 rounded-md bg-warn-wash px-2.5 py-1.5 text-xs text-ink-2">
            Every slot is busy and {a.queued.length} {a.queued.length === 1 ? 'query waits' : 'queries wait'}. The oldest running query has taken{' '}
            {fmtMs(since(oldest.startedAt))}. Queries nobody is waiting for any more are cancelled automatically; cancel a slow one to free a slot now.
          </div>
        )}
      </div>

      <div className="max-h-[60vh] overflow-y-auto">
        {[...a.running, ...a.queued].length > 0 && (
          <Section title="In flight">
            {[...a.running, ...a.queued].map((q) => (
              <div key={q.key} className="group flex items-center gap-2 px-4 py-1.5 text-xs">
                <span className={clsx('size-1.5 shrink-0 rounded-full', q.state === 'running' ? 'animate-pulse bg-accent' : 'bg-ink-4')} />
                <span className="tnum w-16 shrink-0 text-ink-2">{fmtMs(since(q.state === 'running' ? q.startedAt : q.queuedAt))}</span>
                <span className="w-14 shrink-0 text-2xs text-ink-4">{q.state === 'running' ? 'running' : 'waiting'}</span>
                <span className="min-w-0 flex-1 truncate font-mono text-ink-2" title={q.query}>
                  {firstLine(q.query)}
                </span>
                {q.waiters > 1 && <span className="shrink-0 text-2xs text-ink-3">×{q.waiters}</span>}
                <button
                  type="button"
                  onClick={() => cancel(q.key)}
                  className="grid size-5 shrink-0 place-items-center rounded text-ink-4 hover:bg-crit-wash hover:text-crit"
                  title="Cancel this query"
                >
                  <X className="size-3" />
                </button>
              </div>
            ))}
          </Section>
        )}
        <Section title="Recent">
          {a.recent.length === 0 && <div className="px-4 py-2 text-xs text-ink-3">Nothing yet.</div>}
          {a.recent.slice(0, 25).map((q, i) => (
            <button
              key={i}
              type="button"
              onClick={() => navigate(queryHref({ query: q.query }))}
              title={q.error ? `${q.error}\n\n${q.query}` : q.query}
              className="flex w-full items-center gap-2 px-4 py-1.5 text-left text-xs hover:bg-panel-hover"
            >
              <span
                className={clsx(
                  'size-1.5 shrink-0 rounded-full',
                  q.outcome === 'ok' ? 'bg-ok' : q.outcome === 'shared' ? 'bg-accent' : q.outcome === 'cancelled' ? 'bg-ink-4' : 'bg-crit',
                )}
              />
              <span className={clsx('tnum w-16 shrink-0', q.elapsedMs > 5000 ? 'text-warn' : 'text-ink-2')}>{fmtMs(q.elapsedMs)}</span>
              <span className="w-14 shrink-0 text-2xs text-ink-4">{q.outcome === 'ok' ? (q.scannedBytes ? fmtBytes(q.scannedBytes) : `${q.records} rec`) : q.outcome}</span>
              <span className="min-w-0 flex-1 truncate font-mono text-ink-3">{firstLine(q.query)}</span>
              {(q.queuedMs ?? 0) > 300 && <span className="tnum shrink-0 text-2xs text-warn">waited {fmtMs(q.queuedMs!)}</span>}
            </button>
          ))}
        </Section>
      </div>
    </div>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="py-1.5">
      <div className="px-4 pt-1 pb-1 text-2xs font-medium tracking-wide text-ink-3 uppercase">{title}</div>
      {children}
    </div>
  )
}
