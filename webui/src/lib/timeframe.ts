import { useSyncExternalStore } from 'react'

export interface Timeframe {
  /** preset key ("2h") or "custom" */
  key: string
  label: string
  from: string // "now-2h" or RFC3339
  to: string // "now" or RFC3339
  ms: number // duration in ms
}

export const PRESETS: { key: string; label: string; ms: number }[] = [
  { key: '15m', label: 'Last 15 minutes', ms: 15 * 60e3 },
  { key: '1h', label: 'Last hour', ms: 3600e3 },
  { key: '2h', label: 'Last 2 hours', ms: 2 * 3600e3 },
  { key: '6h', label: 'Last 6 hours', ms: 6 * 3600e3 },
  { key: '24h', label: 'Last 24 hours', ms: 24 * 3600e3 },
  { key: '3d', label: 'Last 3 days', ms: 3 * 86400e3 },
  { key: '7d', label: 'Last 7 days', ms: 7 * 86400e3 },
]

export function parseRel(key: string): Timeframe | null {
  const m = /^(\d+)([smhdw])$/.exec(key.trim())
  if (!m) return null
  const n = Number(m[1])
  const unit = { s: 1e3, m: 60e3, h: 3600e3, d: 86400e3, w: 7 * 86400e3 }[m[2] as 's']
  const ms = n * unit
  const preset = PRESETS.find((p) => p.key === key)
  const words = { s: 'second', m: 'minute', h: 'hour', d: 'day', w: 'week' }[m[2] as 's']
  return {
    key,
    label: preset?.label ?? `Last ${n} ${words}${n === 1 ? '' : 's'}`,
    from: `now-${key}`,
    to: 'now',
    ms,
  }
}

export function absolute(from: Date, to: Date): Timeframe {
  const fmt = (d: Date) => d.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })
  return { key: 'custom', label: `${fmt(from)} – ${fmt(to)}`, from: from.toISOString(), to: to.toISOString(), ms: to.getTime() - from.getTime() }
}

function fromUrl(): Timeframe {
  try {
    const p = new URLSearchParams(location.search)
    const tf = p.get('tf')
    if (tf) {
      const [a, b] = tf.split('~')
      if (b) {
        const f = new Date(a)
        const t = new Date(b)
        if (!isNaN(f.getTime()) && !isNaN(t.getTime())) return absolute(f, t)
      }
      const rel = parseRel(tf)
      if (rel) return rel
    }
    const stored = localStorage.getItem('dtctl-web:tf')
    const rel = stored && parseRel(stored)
    if (rel) return rel
  } catch {
    /* storage unavailable */
  }
  return parseRel('2h')!
}

let current = fromUrl()
const listeners = new Set<() => void>()

export function setTimeframe(tf: Timeframe) {
  current = tf
  try {
    if (tf.key !== 'custom') localStorage.setItem('dtctl-web:tf', tf.key)
  } catch {
    /* ignore */
  }
  syncUrl()
  listeners.forEach((l) => l())
}

/** Keep ?tf= in the address bar so any URL is shareable as-is. */
export function syncUrl() {
  const p = new URLSearchParams(location.search)
  p.set('tf', current.key === 'custom' ? `${current.from}~${current.to}` : current.key)
  const url = `${location.pathname}?${p.toString()}${location.hash}`
  if (url !== location.pathname + location.search + location.hash) history.replaceState(history.state, '', url)
}

export function useTimeframe(): Timeframe {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb)
      return () => listeners.delete(cb)
    },
    () => current,
  )
}

export function getTimeframe() {
  return current
}

/** Timeframe ≥ floor (dynatui's floorTimeframe): e.g. problem/vuln views need ≥24h. */
export function floorTf(tf: Timeframe, floorKey: string): Timeframe {
  const f = parseRel(floorKey)!
  return tf.key !== 'custom' && tf.ms < f.ms ? f : tf
}

/** A timeseries interval giving ~60–120 points. */
export function intervalFor(ms: number) {
  const steps = [
    ['1m', 60e3],
    ['2m', 120e3],
    ['5m', 300e3],
    ['10m', 600e3],
    ['15m', 900e3],
    ['30m', 1800e3],
    ['1h', 3600e3],
    ['3h', 3 * 3600e3],
    ['6h', 6 * 3600e3],
    ['12h', 12 * 3600e3],
    ['1d', 86400e3],
  ] as const
  for (const [k, v] of steps) if (ms / v <= 120) return k
  return '1d'
}

/** Sparkline interval: ~24–40 points. */
export function sparkInterval(ms: number) {
  const steps = [
    ['1m', 60e3],
    ['5m', 300e3],
    ['10m', 600e3],
    ['30m', 1800e3],
    ['1h', 3600e3],
    ['3h', 3 * 3600e3],
    ['6h', 6 * 3600e3],
    ['1d', 86400e3],
  ] as const
  for (const [k, v] of steps) if (ms / v <= 40) return k
  return '1d'
}
