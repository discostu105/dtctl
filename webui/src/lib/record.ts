import type { Rec } from './api'

// What the record inspector shows by default. Records carry a lot that says
// nothing: OTel attributes are sparse (null, "", []), and most entities appear
// twice, as dt.smartscape.<type> and as the legacy dt.entity.<type> with the
// same id. Both are hidden by default, counted, and one click away.

export const isEmpty = (v: unknown) => v == null || v === '' || (Array.isArray(v) && v.length === 0)

/** dt.entity.* fields whose id a dt.smartscape.* field of the same record already shows. */
export function legacyTwins(entries: [string, unknown][]): Set<string> {
  const ids = new Set(entries.filter(([k, v]) => k.startsWith('dt.smartscape.') && typeof v === 'string').map(([, v]) => v as string))
  return new Set(entries.filter(([k, v]) => k.startsWith('dt.entity.') && typeof v === 'string' && ids.has(v)).map(([k]) => k))
}

export interface InspectorRows {
  /** sorted by key */
  rows: [string, unknown][]
  empty: number
  twins: number
}

/** The rows to show, and how many were held back (unless `all`). */
export function inspectorRows(rec: Rec, opts: { hide?: string[]; all?: boolean; filter?: string } = {}): InspectorRows {
  const entries = Object.entries(rec).filter(([k]) => !opts.hide?.includes(k))
  const twins = legacyTwins(entries)
  const nEmpty = entries.filter(([, v]) => isEmpty(v)).length
  const f = opts.filter?.toLowerCase()
  const rows = entries
    .filter(([k, v]) => opts.all || (!isEmpty(v) && !twins.has(k)))
    .filter(([k, v]) => !f || k.toLowerCase().includes(f) || String(JSON.stringify(v)).toLowerCase().includes(f))
    .sort(([a], [b]) => a.localeCompare(b))
  return { rows, empty: nEmpty, twins: twins.size }
}
