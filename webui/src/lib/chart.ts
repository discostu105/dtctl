import { num, type Rec } from './api'
import { canonicalUnit } from './format'

// How a query result becomes a chart: which fields are values, which name a
// series, and in which unit each value field is. Pure, so every rule below is
// pinned by a test rather than rediscovered from a screenshot.
//
// The rules, each one a bug it replaces:
//  - A series is named by its dimensions only: string fields that tell
//    records apart. Numbers (a `total` added after the timeseries), constants
//    and an id that merely duplicates a name never make it into the label.
//  - One value field is plotted at a time. Requests, failures and a response
//    time on one axis share no unit, so the smaller ones flatten to zero.
//  - The series shown are the largest by the plotted field, not the first
//    records the query happened to return.

export interface ChartField {
  field: string
  /** Grail's display name, when the field is a plain metric */
  name?: string
  /** fmtUnit spelling ('' = plain number) */
  unit: string
}

export interface ChartSeries {
  key: string
  label: string
  values: (number | null)[]
  /** what the series is ranked by and the legend shows: the sum for counts, else the mean */
  stat: number
}

export interface SeriesSet {
  series: ChartSeries[]
  /** 'total' or 'avg': what `stat` is */
  stat: 'total' | 'avg'
}

export interface MetricMeta {
  field: string
  unit?: string
  name?: string
}

const NOT_DIM_TYPES = /^(long|double|number|boolean|timestamp|duration|timeframe|array|record)/

/** Field types per name, from the result's `types` (first type wins). */
type Types = Record<string, string> | undefined

function isNumArray(v: unknown): v is (number | string | null)[] {
  // Grail sends longs beyond 2^53 as strings
  return Array.isArray(v) && v.every((x) => x == null || typeof x === 'number' || (typeof x === 'string' && x !== '' && !isNaN(Number(x))))
}

/** Value fields of a timeseries record set: numeric arrays as long as the time axis. */
function valueFields(records: Rec[], n: number): string[] {
  const keys: string[] = []
  for (const r of records)
    for (const k of Object.keys(r))
      if (!keys.includes(k) && k !== 'timeframe' && isNumArray(r[k]) && r[k].length === n) keys.push(k)
  // a field that is something other than a numeric array in any record is not a value field
  return keys.filter((k) => records.every((r) => r[k] == null || (isNumArray(r[k]) && r[k].length === n)))
}

/** A field that names series; `alias` is a readable field it determines (a name beside an id). */
export interface Dim {
  field: string
  alias?: string
}

/**
 * The fields that name a series: string-like (or grouped by), varying across
 * records, and not already determined by the fields kept before them. A
 * service's type or its id adds nothing to a label that has the service; a
 * name the kept id determines is shown in its place.
 */
export function dimensionFields(records: Rec[], exclude: string[], types?: Types, query = ''): Dim[] {
  const keys: string[] = []
  for (const r of records) for (const k of Object.keys(r)) if (!keys.includes(k) && !exclude.includes(k) && k !== 'timeframe' && k !== 'interval') keys.push(k)
  // what the query groups by is a dimension whatever its type (a status code)
  const by = byFields(query)
  const cands = keys.filter((k) => {
    if (by.includes(k)) return true
    if (types?.[k] && NOT_DIM_TYPES.test(types[k])) return false
    // a long arrives as a string; without types, a field of numbers in strings is still a number
    return records.every((r) => r[k] == null || typeof r[k] === 'string') && !records.every((r) => r[k] == null || (r[k] !== '' && !isNaN(Number(r[k]))))
  })
  const distinct = (k: string) => new Set(records.map((r) => r[k] ?? null)).size
  // a constant names nothing; a field that is always null neither
  const varying = cands.filter((k) => records.length === 1 || distinct(k) > 1)
  // the grouping first, then the finest fields, the readable one of a tie first
  const order = [...varying].sort(
    (a, b) =>
      Number(by.includes(b)) - Number(by.includes(a)) ||
      (by.includes(a) ? by.indexOf(a) - by.indexOf(b) : 0) ||
      distinct(b) - distinct(a) ||
      readability(b, records) - readability(a, records),
  )
  const kept: Dim[] = []
  for (const k of order) {
    const owner = kept.find((d) => determines(records, [d.field], k))
    if (!kept.length || !determines(records, kept.map((d) => d.field), k)) kept.push({ field: k })
    else if (owner && readability(k, records) > readability(owner.alias ?? owner.field, records)) owner.alias = k
  }
  return keys.flatMap((k) => kept.filter((d) => d.field === k))
}

/** Whether the values of `by` fix the value of `k` in every record. */
function determines(records: Rec[], by: string[], k: string) {
  const seen = new Map<string, unknown>()
  for (const r of records) {
    const key = JSON.stringify(by.map((f) => r[f] ?? null))
    const v = r[k] ?? null
    if (seen.has(key) && seen.get(key) !== v) return false
    seen.set(key, v)
  }
  return true
}

const ENTITY_ID = /^[A-Z][A-Z0-9_]*-[0-9A-F]{8,}$/
function readability(k: string, records: Rec[]) {
  if (/(^|[._])name$/i.test(k)) return 3
  if (/(^|[._])(id|uid)$/i.test(k) || records.every((r) => r[k] == null || ENTITY_ID.test(String(r[k])))) return 0
  return 1
}

const shown = (v: unknown) => (v == null || v === '' ? null : String(v))

/**
 * Series labels, one per record: each dimension's alias, else its value; a
 * missing one reads "(none)". Two series a name alone cannot tell apart
 * (two services called "api") get their id in parentheses.
 */
export function labelsOf(records: Rec[], dims: Dim[], fallback: string): string[] {
  if (!dims.length) return records.map(() => fallback)
  const short = records.map((r) => dims.map((d) => (d.alias && shown(r[d.alias])) || shown(r[d.field]) || '(none)').join(' · '))
  const count = new Map<string, number>()
  for (const l of short) count.set(l, (count.get(l) ?? 0) + 1)
  return short.map((l, i) => {
    if (count.get(l)! < 2) return l
    const ids = dims.filter((d) => d.alias && shown(records[i][d.alias])).map((d) => shown(records[i][d.field]))
    return ids.length ? `${l} (${ids.join(', ')})` : l
  })
}

/**
 * Counts add up over time, everything else (durations, percentages, bytes in
 * use) does not. A field without a unit is a count when all its values are
 * whole numbers: `makeTimeseries count()` is the common case.
 */
export function additive(unit: string, values: Iterable<number | null>): boolean {
  if (unit === 'count') return true
  if (unit !== '') return false
  for (const v of values) if (v != null && !Number.isInteger(v)) return false
  return true
}

function statOf(values: (number | null)[], sum: boolean): number {
  let total = 0
  let n = 0
  for (const v of values)
    if (v != null && Number.isFinite(v)) {
      total += v
      n++
    }
  if (!n) return NaN
  return sum ? total : total / n
}

export interface TimeseriesModel {
  kind: 'timeseries'
  x: number[]
  fields: ChartField[]
  dims: Dim[]
  /** every non-empty series of one field, largest first */
  series: (field: string) => SeriesSet
}

/** The chart model of a timeseries/makeTimeseries result, or null if it is none. */
export function timeseriesModel(records: Rec[] | undefined, opts: { types?: Types; metrics?: MetricMeta[]; query?: string } = {}): TimeseriesModel | null {
  const r0 = records?.find((r) => r.timeframe && r.interval != null)
  if (!records?.length || !r0) return null
  const start = Date.parse(r0.timeframe?.start) / 1000
  const step = num(r0.interval) / 1e9
  if (!Number.isFinite(start) || !(step > 0)) return null
  const n = Math.max(0, ...records.map((r) => Math.max(0, ...Object.values(r).map((v) => (isNumArray(v) ? v.length : 0)))))
  const vf = inQueryOrder(valueFields(records, n), opts.query ?? '')
  if (!vf.length) return null
  const x = Array.from({ length: n }, (_, i) => start + i * step)
  const units = fieldUnits(vf, opts.metrics, opts.query)
  const fields: ChartField[] = vf.map((f) => ({ field: f, name: displayName(opts.metrics, f), unit: units[f] ?? '' }))
  const dims = dimensionFields(records, vf, opts.types, opts.query)
  const labels = labelsOf(records, dims, '')
  const cache = new Map<string, SeriesSet>()
  return {
    kind: 'timeseries',
    x,
    fields,
    dims,
    series: (field) => {
      if (!cache.has(field)) {
        const rows = records.map((r, i) => {
          const raw = Array.isArray(r[field]) ? (r[field] as unknown[]) : []
          return { key: String(i), label: labels[i] || field, values: Array.from({ length: n }, (_, j) => (raw[j] == null ? null : num(raw[j]))) }
        })
        const sum = additive(units[field] ?? '', rows.flatMap((r) => r.values))
        const series = rows
          .map((r) => ({ ...r, stat: statOf(r.values, sum) }))
          .filter((s) => Number.isFinite(s.stat))
          .sort((a, b) => b.stat - a.stat)
        cache.set(field, { series, stat: sum ? 'total' : 'avg' })
      }
      return cache.get(field)!
    },
  }
}

export interface BarsModel {
  kind: 'bars'
  fields: ChartField[]
  dims: Dim[]
  /** every row with a value for the field, largest first */
  bars: (field: string) => { key: string; label: string; value: number }[]
}

/** A bar chart of a summarize-style result: string dimensions and numeric columns. */
export function barsModel(records: Rec[] | undefined, opts: { types?: Types; query?: string } = {}): BarsModel | null {
  if (!records?.length || records.length > 1000) return null
  const keys: string[] = []
  for (const r of records) for (const k of Object.keys(r)) if (!keys.includes(k)) keys.push(k)
  const by = byFields(opts.query ?? '')
  const numeric = keys.filter((k) => {
    if (by.includes(k)) return false
    if (opts.types?.[k] && !/^(long|double|number)/.test(opts.types[k])) return false
    return records.some((r) => r[k] != null) && records.every((r) => r[k] == null || typeof r[k] === 'number' || (typeof r[k] === 'string' && !isNaN(Number(r[k])) && r[k] !== ''))
  })
  if (!numeric.length) return null
  numeric.splice(0, numeric.length, ...inQueryOrder(numeric, opts.query ?? ''))
  const dims = dimensionFields(records, numeric, opts.types, opts.query)
  if (!dims.length && records.length > 1) return null
  const labels = labelsOf(records, dims, '')
  return {
    kind: 'bars',
    fields: numeric.map((f) => ({ field: f, unit: unitFromName(f) })),
    dims,
    bars: (field) =>
      records
        .map((r, i) => ({ key: String(i), label: labels[i] || field, value: r[field] == null ? NaN : num(r[field]) }))
        .filter((b) => Number.isFinite(b.value))
        .sort((a, b) => b.value - a.value),
  }
}

function displayName(metrics: MetricMeta[] | undefined, field: string) {
  const n = metrics?.find((m) => m.field === field)?.name
  return n ? n.replace(/^"(.*)"$/, '$1') : undefined
}

/** A unit only a field's name can tell (nothing in a summarize result carries one). */
function unitFromName(f: string): string {
  if (/(_|\.|^)(percent|pct)$|_rate$|^rate$|percentage/i.test(f)) return '%'
  return ''
}

/**
 * Each value field's unit: Grail's, for a field that is a metric; for one the
 * query derived (`req = coalesce(r[], s[])`), the unit every metric in its
 * expression shares, unless the expression multiplies or divides.
 */
export function fieldUnits(fields: string[], metrics: MetricMeta[] | undefined, query = ''): Record<string, string> {
  const units: Record<string, string> = {}
  for (const m of metrics ?? []) if (m.unit != null) units[m.field] = canonicalUnit(m.unit)
  for (const { name, expr } of assignments(query)) {
    if (name in units && metrics?.some((m) => m.field === name)) continue
    if (/[*/]/.test(expr)) continue
    const refs = (expr.match(/[A-Za-z_][\w.]*/g) ?? []).filter((id) => id in units)
    const us = new Set(refs.map((id) => units[id]))
    if (refs.length && us.size === 1) units[name] = [...us][0]
  }
  const out: Record<string, string> = {}
  for (const f of fields) if (f in units) out[f] = units[f]
  return out
}

/**
 * Fields in the order the query names them: the server sends records as maps
 * with sorted keys, so record order is alphabetical, not the author's.
 */
export function inQueryOrder(fields: string[], query: string): string[] {
  const at = (f: string) => {
    const esc = f.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    const m = new RegExp(`(?<![\\w.])${esc}\\s*=(?!=)`).exec(query) ?? new RegExp(`(?<![\\w.])${esc}(?![\\w.])`).exec(query)
    return m ? m.index : Infinity
  }
  return fields.map((f, i) => ({ f, i, at: at(f) })).sort((a, b) => a.at - b.at || a.i - b.i).map((x) => x.f)
}

/** The field names a query groups by: `by:{a, b = f(c)}` gives a and b. */
export function byFields(query: string): string[] {
  const out: string[] = []
  for (const m of query.matchAll(/\bby\s*:\s*(\{[^}]*\}|[\w.]+)/g)) {
    const body = m[1].startsWith('{') ? m[1].slice(1, -1) : m[1]
    for (const part of splitTop(body, ',')) {
      const p = part.trim()
      if (!p) continue
      const a = /^([A-Za-z_][\w.]*)\s*=(?!=)/.exec(p)
      out.push(a ? a[1] : p.replace(/^`(.*)`$/, '$1'))
    }
  }
  return out
}

/** `name = expr` pairs of every fieldsAdd/fields command, in order. */
export function assignments(query: string): { name: string; expr: string }[] {
  const out: { name: string; expr: string }[] = []
  for (const cmd of splitTop(query, '|')) {
    const m = /^\s*(fieldsAdd|fields)\s+([\s\S]*)$/.exec(cmd)
    if (!m) continue
    for (const part of splitTop(m[2], ',')) {
      const a = /^\s*([A-Za-z_][\w.]*)\s*=(?!=)\s*([\s\S]+?)\s*$/.exec(part)
      if (a) out.push({ name: a[1], expr: a[2] })
    }
  }
  return out
}

/** Split at a separator outside parentheses, brackets, braces and strings. */
function splitTop(s: string, sep: string): string[] {
  const out: string[] = []
  let depth = 0
  let quote = ''
  let cur = ''
  for (let i = 0; i < s.length; i++) {
    const c = s[i]
    if (quote) {
      if (c === '\\') cur += c + (s[++i] ?? '')
      else {
        if (c === quote) quote = ''
        cur += c
      }
      continue
    }
    if (c === '"' || c === '`') quote = c
    else if ('([{'.includes(c)) depth++
    else if (')]}'.includes(c)) depth--
    else if (c === sep && depth === 0) {
      out.push(cur)
      cur = ''
      continue
    }
    cur += c
  }
  out.push(cur)
  return out
}
