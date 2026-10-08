import type { DqlResult } from './api'

// Grail answers a scan-limit hit, a truncated result or a timeout with exit
// code 0 and a notification, so a partial result looks exactly like a complete
// one. These turn the notifications into one plain-language line that says
// what happened and the one thing that helps.

export type NoticeKind = 'scan' | 'timeout' | 'rows' | 'sampled'

export interface Notice {
  type?: string
  severity?: string
  message: string
}

export interface QueryNotice {
  kind: NoticeKind
  /** the result may be incomplete (everything but sampling) */
  warn: boolean
  text: string
  /** Grail's own wording, for the tooltip */
  raw: string
}

/** Most important first: the banner shows only the first one. */
const ORDER: NoticeKind[] = ['scan', 'timeout', 'rows', 'sampled']

/**
 * The category of one notification, by its type, then by its wording: not
 * every deployment stamps a type (and record truncation has two spellings).
 */
export function classify(n: Notice): NoticeKind | null {
  switch (n.type) {
    case 'SCAN_LIMIT_GBYTES':
      return 'scan'
    case 'FETCH_TIMEOUT':
      return 'timeout'
    case 'API_RECORDS_LIMIT_ADDED':
    case 'RESULT_LIMIT_RECORDS':
    case 'RESULT_LIMIT_BYTES':
      return 'rows'
    case 'SAMPLING_APPLIED':
      return 'sampled'
  }
  const m = n.message.toLowerCase()
  if (/stopped after .* scanned|scanlimitgbytes|scan limit/.test(m)) return 'scan'
  if (/timed out|timeout/.test(m)) return 'timeout'
  if (/result has been limited|limited to \d/.test(m)) return 'rows'
  if (/sampl/.test(m)) return 'sampled'
  return null
}

function describe(kind: NoticeKind, raw: string): string {
  const num = (re: RegExp) => re.exec(raw)?.[1]
  switch (kind) {
    case 'scan': {
      const gb = num(/after (\d+(?:\.\d+)?) gigabytes?/i)
      return `Partial result: Grail stopped after scanning ${gb ? `${gb} GB` : 'its limit'}. Narrow the timeframe or add a filter.`
    }
    case 'timeout':
      return 'The query timed out, so the result may be incomplete. Narrow the timeframe or add a filter.'
    case 'rows': {
      const n = num(/limited to (\d+)/i)
      return `${n ? `Only the first ${Number(n).toLocaleString('en-US')} rows came back` : 'Only part of the result came back'}; more matched. Add a filter to see the rest.`
    }
    case 'sampled':
      return 'Results are sampled, so counts are approximate.'
  }
}

/** Every recognized notice of a result, most important first. */
export function noticesOf(meta: DqlResult['meta'] | undefined): QueryNotice[] {
  const ns: Notice[] = meta?.notices ?? (meta?.notifications ?? []).map((message) => ({ message }))
  const out: QueryNotice[] = []
  for (const n of ns) {
    const kind = classify(n)
    if (!kind || out.some((o) => o.kind === kind)) continue
    out.push({ kind, warn: kind !== 'sampled', text: describe(kind, n.message), raw: n.message })
  }
  return out.sort((a, b) => ORDER.indexOf(a.kind) - ORDER.indexOf(b.kind))
}

/** The one notice worth a banner: a warning that the result may be incomplete. */
export function warningOf(meta: DqlResult['meta'] | undefined): QueryNotice | null {
  return noticesOf(meta).find((n) => n.warn) ?? null
}
