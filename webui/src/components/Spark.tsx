import clsx from 'clsx'
import { memo } from 'react'

/**
 * Inline SVG sparkline — cheap enough for hundreds of table rows.
 * Always paired with a numeric value by the caller: a sparkline is scaled to
 * its own range, so on its own it hides the absolute level.
 */
export const Spark = memo(function Spark({
  values,
  color = 'var(--s1)',
  className,
  width = 96,
  height = 22,
  kind = 'line',
  zeroBased = true,
  max,
}: {
  values: (number | null | undefined)[] | undefined
  color?: string
  className?: string
  width?: number
  height?: number
  kind?: 'line' | 'bars'
  zeroBased?: boolean
  max?: number
}) {
  const v = (values ?? []).map((x) => (x == null || !Number.isFinite(Number(x)) ? null : Number(x)))
  const nums = v.filter((x): x is number => x != null)
  if (nums.length < 2) return <svg width={width} height={height} className={className} aria-hidden />
  const hi = max ?? Math.max(...nums)
  const lo = zeroBased ? 0 : Math.min(...nums)
  const range = hi - lo || 1
  const pad = 2
  const h = height - pad * 2
  const x = (i: number) => (i / (v.length - 1)) * width
  const y = (n: number) => pad + h - ((n - lo) / range) * h

  if (kind === 'bars') {
    const bw = Math.max(1, width / v.length - 1)
    return (
      <svg width={width} height={height} className={className} aria-hidden>
        {v.map((n, i) =>
          n == null || n === 0 ? null : (
            <rect key={i} x={(i / v.length) * width} y={y(n)} width={bw} height={Math.max(1, height - pad - y(n))} rx={0.75} fill={color} />
          ),
        )}
      </svg>
    )
  }

  let d = ''
  let started = false
  v.forEach((n, i) => {
    if (n == null) {
      started = false
      return
    }
    d += `${started ? 'L' : 'M'}${x(i).toFixed(1)},${y(n).toFixed(1)}`
    started = true
  })
  const firstI = v.findIndex((n) => n != null)
  let lastI = v.length - 1
  while (lastI > 0 && v[lastI] == null) lastI--
  const area = `${d}L${x(lastI).toFixed(1)},${height}L${x(firstI).toFixed(1)},${height}Z`
  return (
    <svg width={width} height={height} className={clsx('overflow-visible', className)} aria-hidden>
      <path d={area} fill={color} opacity={0.12} />
      <path d={d} fill="none" stroke={color} strokeWidth={1.5} strokeLinejoin="round" strokeLinecap="round" />
      <circle cx={x(lastI)} cy={y(v[lastI]!)} r={2} fill={color} />
    </svg>
  )
})

/** Thin meter (percent). Fill carries severity; track is a lighter step. */
export function Meter({ pct, className }: { pct: number; className?: string }) {
  const p = Math.max(0, Math.min(100, pct))
  const color = p >= 90 ? 'var(--crit)' : p >= 75 ? 'var(--warn)' : 'var(--accent)'
  return (
    <div className={clsx('h-1.5 w-16 overflow-hidden rounded-full bg-line', className)}>
      <div className="h-full rounded-full" style={{ width: `${p}%`, background: color }} />
    </div>
  )
}
