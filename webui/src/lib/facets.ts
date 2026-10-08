import type { ReactNode } from 'react'

// Client-side faceted filtering for lists that are fully loaded in the browser
// (services, hosts, pods, changes, problems, vulnerabilities, …). Counts and
// filtering are instant because nothing goes back to Grail.
//
// Semantics (the usual faceted-search contract):
//   * values within one facet are OR-ed ("namespace is a or b")
//   * facets are AND-ed with each other and with the free text
//   * a facet's own counts ignore its own selection, so you can keep adding values

/** Rows whose value is missing are grouped under this key and shown as "not set". */
export const NONE = '∅'

export interface Facet<T = any> {
  /** Short, URL-safe key, also usable as `key:value` in the filter field. */
  key: string
  label: string
  value: (r: T) => string | number | boolean | (string | number)[] | null | undefined
  /** Pretty value for menus and chips. */
  display?: (v: string) => ReactNode
  /** Fixed order (e.g. buckets "hot, warm, ok"); default is by count. */
  order?: string[]
  /** Short words that also find this facet when typed, e.g. ['ns'] for Namespace. */
  aliases?: string[]
}

export interface FacetFilter {
  key: string
  value: string
  neg?: boolean
}

export interface FacetCount {
  value: string
  count: number
}

export function valuesOf<T>(f: Facet<T>, r: T): string[] {
  const v = f.value(r)
  if (Array.isArray(v)) {
    const out = v.filter((x) => x != null && x !== '').map(String)
    return out.length ? out : [NONE]
  }
  if (v == null || v === '' || (typeof v === 'number' && isNaN(v))) return [NONE]
  return [String(v)]
}

/** Parse `?f=ns:prod&f=-phase:Running` into filters. Values may contain ':'. */
export function parseFilters(params: URLSearchParams, name = 'f'): FacetFilter[] {
  return params.getAll(name).flatMap((s) => {
    const neg = s.startsWith('-')
    const body = neg ? s.slice(1) : s
    const i = body.indexOf(':')
    if (i <= 0) return []
    return [{ key: body.slice(0, i), value: body.slice(i + 1), neg }]
  })
}

export function serializeFilter(f: FacetFilter) {
  return `${f.neg ? '-' : ''}${f.key}:${f.value}`
}

export const sameFilter = (a: FacetFilter, b: FacetFilter) => a.key === b.key && a.value === b.value && !!a.neg === !!b.neg

/** Free text: whitespace-separated terms, all must match; "-term" excludes. */
export function textMatcher(text: string) {
  const terms = text
    .toLowerCase()
    .split(/\s+/)
    .filter((t) => t && t !== '-')
  if (!terms.length) return null
  const pos = terms.filter((t) => !t.startsWith('-'))
  const neg = terms.filter((t) => t.startsWith('-')).map((t) => t.slice(1))
  return (hay: string) => pos.every((t) => hay.includes(t)) && !neg.some((t) => hay.includes(t))
}

function passes<T>(r: T, byKey: Map<string, { inc: Set<string>; exc: Set<string>; facet: Facet<T> }>, skip?: string) {
  for (const [key, g] of byKey) {
    if (key === skip) continue
    const vs = valuesOf(g.facet, r)
    if (g.inc.size && !vs.some((v) => g.inc.has(v))) return false
    if (g.exc.size && vs.some((v) => g.exc.has(v))) return false
  }
  return true
}

export function groupFilters<T>(filters: FacetFilter[], facets: Facet<T>[]) {
  const byKey = new Map<string, { inc: Set<string>; exc: Set<string>; facet: Facet<T> }>()
  for (const f of filters) {
    const facet = facets.find((x) => x.key === f.key)
    if (!facet) continue
    const g = byKey.get(f.key) ?? { inc: new Set(), exc: new Set(), facet }
    ;(f.neg ? g.exc : g.inc).add(f.value)
    byKey.set(f.key, g)
  }
  return byKey
}

export function applyFacets<T>(rows: T[], facets: Facet<T>[], filters: FacetFilter[], match: ((r: T) => boolean) | null) {
  const byKey = groupFilters(filters, facets)
  return rows.filter((r) => (!match || match(r)) && passes(r, byKey))
}

/** Value counts for one facet over rows that pass every *other* filter. */
export function countFacet<T>(rows: T[], facets: Facet<T>[], filters: FacetFilter[], match: ((r: T) => boolean) | null, key: string): FacetCount[] {
  const facet = facets.find((f) => f.key === key)
  if (!facet) return []
  const byKey = groupFilters(filters, facets)
  const counts = new Map<string, number>()
  for (const r of rows) {
    if (match && !match(r)) continue
    if (!passes(r, byKey, key)) continue
    for (const v of new Set(valuesOf(facet, r))) counts.set(v, (counts.get(v) ?? 0) + 1)
  }
  // Selected values stay listed even when the other filters leave them at 0.
  for (const f of filters) if (f.key === key && !counts.has(f.value)) counts.set(f.value, 0)
  const out = [...counts].map(([value, count]) => ({ value, count }))
  if (facet.order) {
    const idx = (v: string) => {
      const i = facet.order!.indexOf(v)
      return i < 0 ? facet.order!.length + (v === NONE ? 1 : 0) : i
    }
    return out.sort((a, b) => idx(a.value) - idx(b.value) || b.count - a.count)
  }
  return out.sort((a, b) => (a.value === NONE ? 1 : 0) - (b.value === NONE ? 1 : 0) || b.count - a.count || a.value.localeCompare(b.value))
}

/** Bucket a number into ordered labels: bucket(v, [[90,'≥ 90%'],[75,'75–90%']], '< 75%'). */
export function bucket(v: number | null | undefined, steps: [number, string][], rest: string): string | null {
  if (v == null || !Number.isFinite(v)) return null
  for (const [min, label] of steps) if (v >= min) return label
  return rest
}
