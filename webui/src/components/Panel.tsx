import * as Popover from '@radix-ui/react-popover'
import type { UseQueryResult } from '@tanstack/react-query'
import clsx from 'clsx'
import { Code2, ExternalLink, Zap } from 'lucide-react'
import type { ReactNode } from 'react'
import { useLocation } from 'wouter'
import type { DqlResult, DqlSpec } from '../lib/api'
import { fmtBytes, fmtCompact, fmtMs } from '../lib/format'
import { highlightDql } from '../lib/highlight'
import { CopyButton, Kbd, Tip } from './ui'

/** Link target that opens a query in the workbench. */
export function queryHref(spec: DqlSpec) {
  const p = new URLSearchParams({ dql: spec.query })
  return `/query?${p.toString()}`
}

/** The honesty layer: what ran, how long it took, whether it came from cache. */
export function QueryInfo({ spec, result, className }: { spec: DqlSpec | null | undefined; result?: UseQueryResult<DqlResult, Error>; className?: string }) {
  const [, navigate] = useLocation()
  if (!spec) return null
  const d = result?.data
  const fetching = result?.isFetching
  return (
    <Popover.Root>
      <Tip content="Show query">
        <Popover.Trigger asChild>
          <button
            type="button"
            className={clsx(
              'inline-flex h-6 items-center gap-1 rounded-md px-1.5 text-2xs text-ink-3 transition-colors hover:bg-line hover:text-ink-2',
              className,
            )}
          >
            {fetching ? (
              <span className="size-1.5 animate-pulse rounded-full bg-accent" />
            ) : d?.cached ? (
              <Zap className="size-3" />
            ) : (
              <Code2 className="size-3.5" />
            )}
            {d && !fetching && <span className="tnum">{d.cached ? 'cached' : fmtMs(d.meta?.executionMs ?? d.elapsedMs)}</span>}
          </button>
        </Popover.Trigger>
      </Tip>
      <Popover.Portal>
        <Popover.Content align="end" sideOffset={6} className="anim-pop z-50 w-[min(560px,90vw)] rounded-lg bg-raised p-0 shadow-pop">
          <div className="flex items-center justify-between border-b border-line px-3 py-2">
            <div className="text-xs font-medium text-ink-2">DQL</div>
            <div className="flex items-center gap-1">
              <CopyButton value={spec.query} label="query" />
              <button
                type="button"
                onClick={() => navigate(queryHref(spec))}
                className="inline-flex h-6 items-center gap-1 rounded-md px-2 text-xs text-accent-ink hover:bg-accent-wash"
              >
                Open in Query <ExternalLink className="size-3" />
              </button>
            </div>
          </div>
          <pre className="max-h-72 overflow-auto px-3 py-2 font-mono text-xs leading-relaxed whitespace-pre-wrap text-ink-2">
            {highlightDql(spec.query)}
          </pre>
          {d && (
            <div className="tnum flex flex-wrap gap-x-4 gap-y-1 border-t border-line px-3 py-2 text-2xs text-ink-3">
              <span>{fmtCompact(d.records.length)} records</span>
              {d.meta && <span>Grail {fmtMs(d.meta.executionMs)}</span>}
              {d.meta && d.meta.scannedBytes > 0 && <span>scanned {fmtBytes(d.meta.scannedBytes)}</span>}
              {d.meta && d.meta.scannedRecords > 0 && <span>{fmtCompact(d.meta.scannedRecords)} rec</span>}
              <span>{d.cached ? 'served from cache' : `round-trip ${fmtMs(d.elapsedMs)}`}</span>
              {d.meta?.sampled && <span className="text-warn">sampled</span>}
            </div>
          )}
          {d?.meta?.notifications?.length ? (
            <div className="border-t border-line px-3 py-2 text-2xs text-warn">{d.meta.notifications.join(' · ')}</div>
          ) : null}
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  )
}

export function Panel({
  title,
  icon,
  actions,
  spec,
  result,
  children,
  className,
  bodyClassName,
  hint,
}: {
  title?: ReactNode
  icon?: ReactNode
  actions?: ReactNode
  spec?: DqlSpec | null
  result?: UseQueryResult<DqlResult, Error>
  children: ReactNode
  className?: string
  bodyClassName?: string
  hint?: ReactNode
}) {
  const stale = result?.isPlaceholderData
  return (
    <section className={clsx('flex min-w-0 flex-col rounded-xl border border-line bg-panel', className)}>
      {(title || actions || spec) && (
        <header className="flex h-10 shrink-0 items-center gap-2 border-b border-line pr-1.5 pl-3">
          {icon && <span className="text-ink-3">{icon}</span>}
          <h2 className="truncate text-sm font-medium">{title}</h2>
          {hint && <span className="truncate text-xs text-ink-3">{hint}</span>}
          <div className="ml-auto flex items-center gap-1">
            {actions}
            <QueryInfo spec={spec} result={result} />
          </div>
        </header>
      )}
      <div className={clsx('min-h-0 flex-1 transition-opacity', stale && 'opacity-60', bodyClassName)}>{children}</div>
    </section>
  )
}

export function PageHeader({ title, sub, actions, icon }: { title: ReactNode; sub?: ReactNode; actions?: ReactNode; icon?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-end gap-3 pb-4">
      <div className="min-w-0">
        <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
          {icon && <span className="text-ink-3">{icon}</span>}
          {title}
        </h1>
        {sub && <div className="mt-0.5 text-sm text-ink-3">{sub}</div>}
      </div>
      {actions && <div className="ml-auto flex items-center gap-2">{actions}</div>}
    </div>
  )
}

export function FilterInput({
  value,
  onChange,
  placeholder = 'Filter…',
  className,
  inputRef,
}: {
  value: string
  onChange: (v: string) => void
  placeholder?: string
  className?: string
  inputRef?: React.Ref<HTMLInputElement>
}) {
  return (
    <div className={clsx('relative', className)}>
      <input
        ref={inputRef}
        data-filter
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            onChange('')
            ;(e.target as HTMLInputElement).blur()
          }
        }}
        placeholder={placeholder}
        className="h-8 w-full rounded-lg border border-line bg-sunken pr-8 pl-2.5 text-sm text-ink outline-none placeholder:text-ink-4 focus:border-accent/60"
      />
      <Kbd className="absolute top-1/2 right-2 -translate-y-1/2">/</Kbd>
    </div>
  )
}
