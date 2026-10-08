import { useEffect, useMemo, useSyncExternalStore } from 'react'
import { useDql, type DqlResult, type DqlSpec } from './api'

// Adaptive sampling for data-heavy charts (log histograms, facet counts, error
// trends). Large tenants scan hundreds of GB per hour of logs, so an exact
// count over a few hours runs into Grail's scan limit (500 GB by default) and
// comes back partial and slow. Instead:
//
//   1. probe   – a 1:10,000 sampled count over the same timeframe (~0.1 s)
//                estimates how many bytes the real query would scan
//   2. choose  – the smallest samplingRatio (1, 10, … 100,000) that keeps the
//                scan under SCAN_BUDGET
//   3. scale   – counts from a sampled query are multiplied back by the ratio
//                and shown as approximate (≈)
//   4. escalate – if a response still reports the scan limit, the ratio goes
//                up 10× for that table and timeframe, automatically
//
// Small tenants pay one tiny probe and run exact queries (ratio 1).

export type SampledTable = 'logs' | 'spans'
export const RATIOS = [1, 10, 100, 1000, 10000, 100000]
const PROBE = 10000
/**
 * Bytes a chart query may scan: fast, and far below the 500 GB default limit.
 * Override per browser with localStorage['dtctl-web:scan-budget-gb'].
 */
export const SCAN_BUDGET = (() => {
  try {
    const gb = Number(localStorage.getItem('dtctl-web:scan-budget-gb'))
    if (gb > 0) return gb * 1e9
  } catch {
    /* ignore */
  }
  return 50e9
})()

// ── escalation store (shared by every chart on the same table + timeframe) ──
const bumps = new Map<string, number>()
const listeners = new Set<() => void>()
const subscribe = (cb: () => void) => (listeners.add(cb), () => listeners.delete(cb))
function bump(key: string) {
  bumps.set(key, (bumps.get(key) ?? 0) + 1)
  listeners.forEach((l) => l())
}

export function hitScanLimit(d: DqlResult | undefined) {
  return !!d?.meta?.notifications?.some((n) => /stopped after|scanLimitGBytes|scan limit/i.test(n))
}

/** Insert `samplingRatio` into the query's fetch command. */
export function withSampling(query: string, ratio: number) {
  if (ratio <= 1) return query
  return query.replace(/^(\s*fetch\s+(?:logs|spans))(?=[\s,|]|$)/m, `$1, samplingRatio:${ratio}`)
}

export interface Sampling {
  /** null while the probe runs */
  ratio: number | null
  estBytes: number
  estRecords: number
}

/** The sampling ratio for a table over a timeframe (from/to as in DqlSpec). */
export function useSamplingRatio(table: SampledTable, scope: { from?: string; to?: string }): Sampling {
  const key = `${table}|${scope.from ?? ''}|${scope.to ?? ''}`
  const extra = useSyncExternalStore(subscribe, () => bumps.get(key) ?? 0)
  const probe = useDql({ query: `fetch ${table}, samplingRatio:${PROBE}\n| summarize n = count()`, from: scope.from, to: scope.to, ttl: 300 })
  if (probe.isLoading) return { ratio: null, estBytes: 0, estRecords: 0 }
  if (probe.error || !probe.data) return { ratio: Math.min(RATIOS[RATIOS.length - 1], 10 ** extra), estBytes: 0, estRecords: 0 }
  const estBytes = (probe.data.meta?.scannedBytes ?? 0) * PROBE
  const estRecords = Number(probe.data.records[0]?.n ?? 0) * PROBE
  const base = RATIOS.find((r) => estBytes / r <= SCAN_BUDGET) ?? RATIOS[RATIOS.length - 1]
  return { ratio: Math.min(RATIOS[RATIOS.length - 1], base * 10 ** extra), estBytes, estRecords }
}

/**
 * useDql for a heavy aggregate over logs/spans: the spec is rewritten with the
 * adaptive samplingRatio, and the named count fields (numbers or timeseries
 * arrays) come back already scaled up by the ratio.
 */
export function useAdaptiveDql(table: SampledTable, spec: DqlSpec | null, scaleFields: string[] = []) {
  const s = useSamplingRatio(table, { from: spec?.from, to: spec?.to })
  const sampled = spec && s.ratio != null ? { ...spec, query: withSampling(spec.query, s.ratio) } : null
  const raw = useDql(sampled)
  const key = `${table}|${spec?.from ?? ''}|${spec?.to ?? ''}`
  const limited = hitScanLimit(raw.data)
  useEffect(() => {
    if (limited && (s.ratio ?? 1) < RATIOS[RATIOS.length - 1]) bump(key)
  }, [limited, key, s.ratio])
  const ratio = s.ratio ?? 1
  const fields = scaleFields.join(',')
  const data = useMemo(() => {
    if (!raw.data || ratio <= 1 || !fields) return raw.data
    const fs = fields.split(',')
    const mul = (v: unknown) => (typeof v === 'number' ? v * ratio : typeof v === 'string' && v !== '' && !isNaN(+v) ? +v * ratio : v)
    return {
      ...raw.data,
      records: raw.data.records.map((r) => {
        const o = { ...r }
        for (const f of fs) if (f in o) o[f] = Array.isArray(o[f]) ? o[f].map(mul) : mul(o[f])
        return o
      }),
    }
  }, [raw.data, ratio, fields])
  const res = useMemo(() => ({ ...raw, data }), [raw, data]) as typeof raw
  return { res, spec: sampled, ratio, sampling: s, waiting: s.ratio == null }
}
