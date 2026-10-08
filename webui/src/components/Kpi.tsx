import clsx from 'clsx'
import { ArrowUpRight } from 'lucide-react'
import type { ReactNode } from 'react'
import { Link } from 'wouter'
import { fmtCompact } from '../lib/format'
import { Spark } from './Spark'
import { Skeleton } from './ui'

/**
 * The one headline-number tile (Pulse, AI, …). A number value is formatted
 * with fmtCompact; pass a string for anything else (%, durations). `tone`
 * colours the value, but never a zero. A tile with `href` or `onClick` is a
 * way in: it shows the hover state and the arrow.
 */
export function Kpi({
  label,
  value,
  sub,
  icon,
  tone,
  href,
  onClick,
  spark,
  sparkColor,
  loading,
}: {
  label: string
  value: number | string | null | undefined
  sub?: ReactNode
  icon?: ReactNode
  tone?: 'crit' | 'warn' | 'ok'
  href?: string
  onClick?: () => void
  spark?: number[]
  sparkColor?: string
  loading?: boolean
}) {
  const zero = value === 0
  const toneCls = !zero && tone ? { crit: 'text-crit', warn: 'text-warn', ok: 'text-ok' }[tone] : undefined
  const body = (
    <>
      <div className="flex items-center gap-2 text-xs text-ink-3">
        {icon && <span className={toneCls ?? 'text-ink-3'}>{icon}</span>}
        {label}
        {(href || onClick) && <ArrowUpRight className="ml-auto size-3.5 opacity-0 transition-opacity group-hover:opacity-100" />}
      </div>
      <div className="flex items-end justify-between gap-2">
        {loading || value == null ? (
          <Skeleton className="h-7 w-16" />
        ) : (
          <div className={clsx('tnum text-[26px] leading-none font-semibold tracking-tight', toneCls)}>{typeof value === 'number' ? fmtCompact(value) : value}</div>
        )}
        {spark && <Spark values={spark} color={sparkColor} width={88} height={28} kind="bars" />}
      </div>
      <div className="truncate text-xs text-ink-3">{sub}</div>
    </>
  )
  const cls = clsx(
    'group relative flex flex-col gap-2 overflow-hidden rounded-xl border border-line bg-panel p-3.5 text-left transition-colors',
    (href || onClick) && 'hover:border-line-strong hover:bg-panel-hover',
  )
  if (href)
    return (
      <Link href={href} className={cls}>
        {body}
      </Link>
    )
  if (onClick)
    return (
      <button type="button" onClick={onClick} className={cls}>
        {body}
      </button>
    )
  return <div className={cls}>{body}</div>
}
