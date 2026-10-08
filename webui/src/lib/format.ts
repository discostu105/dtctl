const nf0 = new Intl.NumberFormat('en-US', { maximumFractionDigits: 0 })
const nf1 = new Intl.NumberFormat('en-US', { maximumFractionDigits: 1 })
const nf2 = new Intl.NumberFormat('en-US', { maximumFractionDigits: 2 })

export function fmtInt(n: number) {
  return Number.isFinite(n) ? nf0.format(n) : '—'
}

/** 1,284 · 12.9K · 4.2M */
export function fmtCompact(n: number) {
  if (!Number.isFinite(n)) return '—'
  const a = Math.abs(n)
  if (a < 10_000) return a < 10 && a % 1 !== 0 ? nf2.format(n) : nf0.format(n)
  if (a < 1e6) return nf1.format(n / 1e3) + 'K'
  if (a < 1e9) return nf1.format(n / 1e6) + 'M'
  return nf1.format(n / 1e9) + 'B'
}

export function fmtPct(n: number, digits = 1) {
  if (!Number.isFinite(n)) return '—'
  if (n > 0 && n < 0.1) return '<0.1%'
  return n.toFixed(digits).replace(/\.0+$/, '') + '%'
}

/** Durations: input in nanoseconds. */
export function fmtNs(ns: number) {
  if (!Number.isFinite(ns)) return '—'
  return fmtMs(ns / 1e6)
}
export function fmtUs(us: number) {
  if (!Number.isFinite(us)) return '—'
  return fmtMs(us / 1e3)
}
export function fmtMs(ms: number) {
  if (!Number.isFinite(ms)) return '—'
  const a = Math.abs(ms)
  if (a < 1) return nf2.format(ms * 1000) + ' µs'
  if (a < 1000) return (a < 10 ? nf1 : nf0).format(ms) + ' ms'
  if (a < 60_000) return nf2.format(ms / 1000) + ' s'
  if (a < 3_600_000) return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`
  if (a < 86_400_000) return `${Math.floor(ms / 3_600_000)}h ${Math.round((ms % 3_600_000) / 60_000)}m`
  return `${Math.floor(ms / 86_400_000)}d ${Math.round((ms % 86_400_000) / 3_600_000)}h`
}

export function fmtBytes(b: number) {
  if (!Number.isFinite(b)) return '—'
  const u = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let i = 0
  while (Math.abs(b) >= 1024 && i < u.length - 1) {
    b /= 1024
    i++
  }
  return (i === 0 ? nf0 : nf1).format(b) + ' ' + u[i]
}

export function toDate(v: unknown): Date | null {
  if (v == null || v === '') return null
  if (v instanceof Date) return v
  const d = typeof v === 'number' ? new Date(v) : new Date(String(v))
  return Number.isNaN(d.getTime()) ? null : d
}

/** "3m ago" · "2h ago" · "5d ago" */
export function ago(v: unknown, now = Date.now()) {
  const d = toDate(v)
  if (!d) return '—'
  const s = Math.round((now - d.getTime()) / 1000)
  if (s < 0) return 'just now'
  if (s < 45) return `${s}s ago`
  if (s < 3600) return `${Math.round(s / 60)}m ago`
  if (s < 86400) return `${Math.round(s / 3600)}h ago`
  if (s < 86400 * 60) return `${Math.round(s / 86400)}d ago`
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })
}

/** Compact duration between two instants ("12m", "3h 4m"). */
export function span(from: unknown, to: unknown = Date.now()) {
  const a = toDate(from)
  const b = toDate(to)
  if (!a || !b) return '—'
  return fmtMs(b.getTime() - a.getTime()).replace(/ 0s$/, '').replace(/ 0m$/, '')
}

const dtf = new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
const tf = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })

export function fmtDateTime(v: unknown) {
  const d = toDate(v)
  return d ? dtf.format(d) : '—'
}
export function fmtTime(v: unknown) {
  const d = toDate(v)
  if (!d) return '—'
  const ms = String(d.getMilliseconds()).padStart(3, '0')
  return `${tf.format(d)}.${ms}`
}
export function fmtAbs(v: unknown) {
  const d = toDate(v)
  return d ? d.toISOString().replace('T', ' ').replace('Z', ' UTC') : ''
}

export function shortType(t: string) {
  return t
    .replace(/^K8S_/, '')
    .replace(/^AWS_/, 'AWS ')
    .replace(/_/g, ' ')
    .toLowerCase()
    .replace(/\b\w/g, (c) => c.toUpperCase())
    .replace(/^Aws /, 'AWS ')
}

export function titleCase(s: string) {
  return s
    .toLowerCase()
    .replace(/_/g, ' ')
    .replace(/\b\w/g, (c) => c.toUpperCase())
}
