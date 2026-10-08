import type { UseQueryResult } from '@tanstack/react-query'
import clsx from 'clsx'
import { useEffect, useRef, type ReactNode } from 'react'
import type { DqlResult, DqlSpec } from '../lib/api'
import { fmtInt } from '../lib/format'
import { QueryInfo } from './Panel'
import { Tip } from './ui'

export interface DataTab<T extends string> {
  value: T
  label: ReactNode
  hidden?: boolean
  /** the tab's main query: drives the count, the empty state and "show query" */
  spec?: DqlSpec | null
  result?: UseQueryResult<DqlResult, Error>
  /** override the count (default: number of records) */
  count?: number | null
  /** the query's limit, so a full page reads "500+" */
  limit?: number
}

/**
 * Tabs that know their data before you click them: every tab's query runs up
 * front (in the same streamed batch), so each tab shows its record count, an
 * empty tab is visibly dimmed, and switching tabs is instant. The active
 * tab's query is one click away on the right.
 */
export function DataTabs<T extends string>({
  value,
  onChange,
  tabs,
  className,
  right,
}: {
  value: T
  onChange: (v: T) => void
  tabs: DataTab<T>[]
  className?: string
  right?: ReactNode
}) {
  const shown = tabs.filter((t) => !t.hidden)
  const active = shown.find((t) => t.value === value)

  // Never open on an empty tab: until the user picks one, move from an empty
  // default to the first tab that has data (counts arrive progressively).
  const picked = useRef(false)
  const countOf = (t: DataTab<T>) => (t.count !== undefined ? t.count : t.result?.data ? t.result.data.records.length : undefined)
  const activeCount = active ? countOf(active) : undefined
  const firstWithData = shown.find((t) => {
    const n = countOf(t)
    return n == null ? false : n > 0
  })
  useEffect(() => {
    if (!picked.current && activeCount === 0 && firstWithData && firstWithData.value !== value) onChange(firstWithData.value)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeCount, firstWithData?.value, value])
  return (
    <div className={clsx('flex items-center border-b border-line pr-2 pl-2', className)} role="tablist">
      {shown.map((t) => {
        const loading = !!t.spec && !!t.result?.isLoading
        const error = !!t.result?.error
        const n = t.count !== undefined ? t.count : t.result?.data ? t.result.data.records.length : undefined
        const empty = !loading && !error && n === 0
        const on = t.value === value
        const badge = (
          <span
            className={clsx(
              'tnum inline-flex h-4 min-w-4 items-center justify-center rounded px-1 text-2xs',
              empty ? 'bg-transparent text-ink-4' : 'bg-line text-ink-3',
            )}
          >
            {n != null && fmtInt(n)}
            {t.limit && n != null && n >= t.limit ? '+' : ''}
          </span>
        )
        return (
          <button
            key={t.value}
            role="tab"
            aria-selected={on}
            onClick={() => {
              picked.current = true
              onChange(t.value)
            }}
            className={clsx(
              'relative -mb-px inline-flex h-9 items-center gap-1.5 px-2.5 text-sm transition-colors',
              on ? 'text-ink' : empty ? 'text-ink-4 hover:text-ink-3' : 'text-ink-3 hover:text-ink-2',
            )}
          >
            <span className={clsx(empty && !on && 'line-through decoration-ink-4/60')}>{t.label}</span>
            {loading ? (
              <span className="size-1.5 animate-pulse rounded-full bg-accent/70" />
            ) : error ? (
              <Tip content="Query failed">
                <span className="size-1.5 rounded-full bg-crit" />
              </Tip>
            ) : n != null ? (
              empty ? <Tip content="No data in this timeframe">{badge}</Tip> : badge
            ) : null}
            {on && <span className="absolute inset-x-1.5 bottom-0 h-0.5 rounded-full bg-accent" />}
          </button>
        )
      })}
      <div className="ml-auto flex items-center gap-2">
        {right}
        {active?.spec && <QueryInfo spec={active.spec} result={active.result} />}
      </div>
    </div>
  )
}
