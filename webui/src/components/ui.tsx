import * as Tooltip from '@radix-ui/react-tooltip'
import clsx from 'clsx'
import { AlertTriangle, Check, Copy, Inbox } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import { toast } from 'sonner'
import { ago, fmtAbs } from '../lib/format'

export function Tip({ content, children, side = 'top' }: { content: ReactNode; children: ReactNode; side?: 'top' | 'bottom' | 'left' | 'right' }) {
  if (!content) return <>{children}</>
  return (
    <Tooltip.Root delayDuration={350}>
      <Tooltip.Trigger asChild>{children}</Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content
          side={side}
          sideOffset={6}
          className="anim-pop z-50 max-w-sm rounded-md bg-raised px-2 py-1 text-xs text-ink-2 shadow-pop"
        >
          {content}
        </Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip.Root>
  )
}

export function Kbd({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <kbd
      className={clsx(
        'inline-flex h-[18px] min-w-[18px] items-center justify-center rounded border border-line-strong bg-sunken px-1 font-sans text-2xs font-medium text-ink-3',
        className,
      )}
    >
      {children}
    </kbd>
  )
}

export type Tone = 'crit' | 'warn' | 'ok' | 'info' | 'muted' | 'accent'

const toneCls: Record<Tone, string> = {
  crit: 'bg-crit-wash text-crit',
  warn: 'bg-warn-wash text-warn',
  ok: 'bg-ok-wash text-ok',
  info: 'bg-accent-wash text-accent-ink',
  accent: 'bg-accent-wash text-accent-ink',
  muted: 'bg-line text-ink-2',
}

export function Badge({ tone = 'muted', children, className, mono }: { tone?: Tone; children: ReactNode; className?: string; mono?: boolean }) {
  return (
    <span
      className={clsx(
        'inline-flex h-[20px] shrink-0 items-center gap-1 rounded-[5px] px-1.5 text-2xs font-medium whitespace-nowrap',
        mono && 'font-mono',
        toneCls[tone],
        className,
      )}
    >
      {children}
    </span>
  )
}

const dotCls: Record<Tone, string> = {
  crit: 'bg-crit',
  warn: 'bg-warn',
  ok: 'bg-ok',
  info: 'bg-accent',
  accent: 'bg-accent',
  muted: 'bg-ink-4',
}

export function Dot({ tone = 'muted', live, className }: { tone?: Tone; live?: boolean; className?: string }) {
  return <span className={clsx('inline-block size-2 shrink-0 rounded-full', dotCls[tone], live && 'live-dot', className)} />
}

/** Click-to-copy text with a tick confirmation. */
export function CopyButton({ value, label, className }: { value: string; label?: string; className?: string }) {
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      className={clsx('inline-flex items-center gap-1 rounded p-0.5 text-ink-3 transition-colors hover:bg-line hover:text-ink', className)}
      onClick={(e) => {
        e.stopPropagation()
        e.preventDefault()
        copy(value, label)
        setDone(true)
        setTimeout(() => setDone(false), 1200)
      }}
      aria-label={`Copy ${label ?? value}`}
    >
      {done ? <Check className="size-3.5 text-ok" /> : <Copy className="size-3.5" />}
    </button>
  )
}

export function copy(value: string, label?: string) {
  navigator.clipboard?.writeText(value).then(
    () => toast.success(`Copied ${label ?? ''}`.trim(), { description: value.length > 80 ? value.slice(0, 80) + '…' : value, duration: 1600 }),
    () => toast.error('Clipboard unavailable'),
  )
}

/** Monospace ID that copies on click. */
export function IdChip({ id, className }: { id: string; className?: string }) {
  return (
    <Tip content="Click to copy">
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation()
          copy(id, 'ID')
        }}
        className={clsx('truncate rounded px-1 font-mono text-2xs text-ink-3 hover:bg-line hover:text-ink-2', className)}
      >
        {id}
      </button>
    </Tip>
  )
}

/** Relative time that ticks, with the absolute timestamp on hover. */
export function TimeAgo({ value, className }: { value: unknown; className?: string }) {
  const now = useNow(15_000)
  if (value == null) return <span className="text-ink-4">—</span>
  return (
    <Tip content={fmtAbs(value)}>
      <span className={clsx('tnum whitespace-nowrap', className)}>{ago(value, now)}</span>
    </Tip>
  )
}

export function useNow(every = 1000) {
  const [now, setNow] = useState(Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), every)
    return () => clearInterval(t)
  }, [every])
  return now
}

export function Skeleton({ className }: { className?: string }) {
  return <div className={clsx('shimmer', className)} />
}

export function SkeletonRows({ rows = 6, className }: { rows?: number; className?: string }) {
  return (
    <div className={clsx('flex flex-col gap-2.5 p-3', className)}>
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="h-4" />
      ))}
    </div>
  )
}

export function Empty({ title = 'Nothing here', hint, icon, className }: { title?: string; hint?: ReactNode; icon?: ReactNode; className?: string }) {
  return (
    <div className={clsx('flex flex-col items-center justify-center gap-1.5 px-4 py-10 text-center', className)}>
      <div className="mb-1 text-ink-4">{icon ?? <Inbox className="size-5" />}</div>
      <div className="text-sm font-medium text-ink-2">{title}</div>
      {hint && <div className="max-w-sm text-xs text-ink-3">{hint}</div>}
    </div>
  )
}

export function ErrorBox({ error, className }: { error: unknown; className?: string }) {
  const msg = error instanceof Error ? error.message : String(error)
  return (
    <div className={clsx('m-3 flex items-start gap-2 rounded-md bg-crit-wash p-3 text-xs text-crit', className)}>
      <AlertTriangle className="mt-px size-4 shrink-0" />
      <div className="min-w-0">
        <div className="font-medium">Query failed</div>
        <div className="mt-0.5 font-mono break-words whitespace-pre-wrap text-ink-2">{msg}</div>
      </div>
    </div>
  )
}

export function Segmented<T extends string>({
  value,
  onChange,
  options,
  className,
}: {
  value: T
  onChange: (v: T) => void
  options: { value: T; label: ReactNode; count?: number }[]
  className?: string
}) {
  return (
    <div className={clsx('inline-flex items-center gap-0.5 rounded-lg bg-sunken p-0.5', className)}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          onClick={() => onChange(o.value)}
          className={clsx(
            'inline-flex h-6 items-center gap-1.5 rounded-md px-2.5 text-xs font-medium transition-colors',
            o.value === value ? 'bg-raised text-ink shadow-[0_0_0_1px_var(--line-strong)]' : 'text-ink-3 hover:text-ink-2',
          )}
        >
          {o.label}
          {o.count != null && <span className="tnum text-2xs text-ink-3">{o.count}</span>}
        </button>
      ))}
    </div>
  )
}

export function Tabs<T extends string>({
  value,
  onChange,
  tabs,
  className,
}: {
  value: T
  onChange: (v: T) => void
  tabs: { value: T; label: ReactNode; count?: number | null; hidden?: boolean }[]
  className?: string
}) {
  return (
    <div className={clsx('flex items-center gap-1 border-b border-line', className)} role="tablist">
      {tabs
        .filter((t) => !t.hidden)
        .map((t, i) => (
          <button
            key={t.value}
            role="tab"
            aria-selected={t.value === value}
            onClick={() => onChange(t.value)}
            className={clsx(
              'relative -mb-px inline-flex h-9 items-center gap-1.5 px-2.5 text-sm transition-colors',
              t.value === value ? 'text-ink' : 'text-ink-3 hover:text-ink-2',
            )}
          >
            <span>{t.label}</span>
            {t.count != null && <span className="tnum rounded bg-line px-1 text-2xs text-ink-3">{t.count}</span>}
            {i < 9 && <span className="sr-only">(press {i + 1})</span>}
            {t.value === value && <span className="absolute inset-x-1.5 bottom-0 h-0.5 rounded-full bg-accent" />}
          </button>
        ))}
    </div>
  )
}

export function Stat({ label, value, sub, tone, className }: { label: ReactNode; value: ReactNode; sub?: ReactNode; tone?: Tone; className?: string }) {
  return (
    <div className={clsx('min-w-0', className)}>
      <div className="text-2xs font-medium tracking-wide text-ink-3 uppercase">{label}</div>
      <div
        className={clsx(
          'tnum mt-0.5 text-lg font-semibold',
          tone === 'crit' && 'text-crit',
          tone === 'warn' && 'text-warn',
          tone === 'ok' && 'text-ok',
        )}
      >
        {value}
      </div>
      {sub && <div className="truncate text-xs text-ink-3">{sub}</div>}
    </div>
  )
}

/** Key/value facts grid. */
export function Facts({ items, className }: { items: [ReactNode, ReactNode][]; className?: string }) {
  const shown = items.filter(([, v]) => v != null && v !== '' && v !== false)
  return (
    <dl className={clsx('grid grid-cols-[auto_1fr] gap-x-6 gap-y-1.5 text-sm', className)}>
      {shown.map(([k, v], i) => (
        <div key={i} className="contents">
          <dt className="whitespace-nowrap text-ink-3">{k}</dt>
          <dd className="min-w-0 truncate text-ink">{v}</dd>
        </div>
      ))}
    </dl>
  )
}
