// RUM (real user monitoring) queries and Core Web Vitals ratings.
// Shapes validated live; session/event filters follow dynatui's catalog/rum.go.
import { num, type Rec } from './api'
import { q } from './dql'
import type { Tone } from '../components/ui'

/** Synthetic monitors emit RUM events too; most views want real users only. */
export const REAL_USERS = 'dt.rum.user_type == "real_user"'

/** Sessions carry dt.smartscape.frontend as an array. */
export const sessionFrontendFilter = (id: string) => `matchesPhrase(arrayToString(dt.smartscape.frontend, delimiter:","), ${q(id)})`
export const eventFrontendFilter = (id: string) => `dt.smartscape.frontend == toSmartscapeId(${q(id)})`

function where(filters: (string | false | null | undefined)[]) {
  return filters
    .filter(Boolean)
    .map((f) => `| filter ${f}`)
    .join('\n')
}

/** One row per frontend: traffic + p75 Core Web Vitals over view summaries. */
export function frontendsQuery(realOnly: boolean) {
  return `fetch user.events
| filter characteristics.classifier == "view_summary"
${where([realOnly && REAL_USERS])}
| summarize { views = count(), sessions = countDistinct(dt.rum.session.id), lcp = percentile(web_vitals.largest_contentful_paint, 75), inp = percentile(web_vitals.interaction_to_next_paint, 75), cls = percentile(web_vitals.cumulative_layout_shift, 75), ttfb = percentile(web_vitals.time_to_first_byte, 75), errors = sum(toLong(error.exception_count)) + sum(toLong(error.http_4xx_count)) + sum(toLong(error.http_5xx_count)), name = takeFirst(frontend.name) }, by:{dt.smartscape.frontend}
| sort views desc
| limit 100`
}

export function frontendTrafficQuery(interval: string) {
  return `timeseries { req = sum(dt.frontend.request.count, default:0), err = sum(dt.frontend.error.count, default:0) }, by:{dt.smartscape.frontend}, interval:${interval}`
}

export type SessionLens = 'all' | 'errors' | 'bounced'

export function sessionsQuery(opts: { realOnly: boolean; frontend?: string | null; lens: SessionLens }) {
  return `fetch user.sessions
${where([
  opts.realOnly && REAL_USERS,
  opts.frontend && sessionFrontendFilter(opts.frontend),
  opts.lens === 'errors' && 'toLong(error.count) > 0',
  opts.lens === 'bounced' && 'characteristics.is_bounce == true',
])}
| fields start_time, end_time, duration, dt.rum.session.id, frontend.name, dt.smartscape.frontend, user.identifier, view_summary_count, user_action_count, request_count, error.count, browser.name, browser.version, os.name, device.type, geo.country.iso_code, geo.city.name, end_reason, characteristics.has_replay, characteristics.is_bounce, dt.rum.user_type
| sort start_time desc
| limit 500`
}

export function sessionQuery(id: string) {
  return `fetch user.sessions, from:now()-7d
| filter dt.rum.session.id == ${q(id)}
| sort end_time desc
| limit 1`
}

export function sessionEventsQuery(id: string) {
  return `fetch user.events
| filter dt.rum.session.id == ${q(id)}
| filter not(in(characteristics.classifier, {"visibility_change", "invalid"}))
| sort start_time asc
| limit 2000`
}

export function errorGroupsQuery(opts: { realOnly: boolean; frontend?: string | null }) {
  return `fetch user.events
| filter characteristics.classifier == "error"
${where([opts.realOnly && REAL_USERS, opts.frontend && eventFrontendFilter(opts.frontend)])}
| summarize { count = count(), sessions = countDistinct(dt.rum.session.id), app = takeFirst(frontend.name), frontend = takeFirst(dt.smartscape.frontend), type = takeFirst(error.type), first = min(start_time), last = max(start_time), view = takeLast(view.detected_name), sample_session = takeLast(dt.rum.session.id) }, by:{error = error.display_name}
| sort count desc
| limit 300`
}

export function errorOccurrencesQuery(display: string, opts: { realOnly: boolean; frontend?: string | null }) {
  return `fetch user.events
| filter characteristics.classifier == "error" and error.display_name == ${q(display)}
${where([opts.realOnly && REAL_USERS, opts.frontend && eventFrontendFilter(opts.frontend)])}
| sort start_time desc
| limit 50`
}

export function pagesQuery(opts: { realOnly: boolean; frontend?: string | null }) {
  return `fetch user.events
| filter characteristics.classifier == "view_summary"
${where([opts.realOnly && REAL_USERS, opts.frontend && eventFrontendFilter(opts.frontend)])}
| summarize { views = count(), sessions = countDistinct(dt.rum.session.id), lcp = percentile(web_vitals.largest_contentful_paint, 75), inp = percentile(web_vitals.interaction_to_next_paint, 75), cls = percentile(web_vitals.cumulative_layout_shift, 75), errors = sum(toLong(error.exception_count)), app = takeFirst(frontend.name) }, by:{page = coalesce(view.detected_name, view.name)}
| sort views desc
| limit 300`
}

// ── Core Web Vitals (thresholds per web.dev) ─────────────────────────────────

export type Vital = 'lcp' | 'inp' | 'cls' | 'ttfb'

const THRESHOLDS: Record<Vital, [number, number]> = {
  lcp: [2500, 4000], // ms
  inp: [200, 500], // ms
  cls: [0.1, 0.25],
  ttfb: [800, 1800], // ms
}

export const VITAL_LABEL: Record<Vital, string> = {
  lcp: 'Largest contentful paint',
  inp: 'Interaction to next paint',
  cls: 'Cumulative layout shift',
  ttfb: 'Time to first byte',
}

/** Vitals arrive in ns (CLS is unitless). Returns ms / raw, or NaN if unmeasured. */
export function vitalValue(r: Rec, v: Vital): number {
  const raw = num(r[v])
  if (!Number.isFinite(raw)) return NaN
  if (v === 'cls') return raw
  // INP is 0 on pages nobody interacted with: that's "not measured", not "instant".
  if (raw === 0 && v === 'inp') return NaN
  return raw / 1e6
}

export function vitalRating(v: Vital, value: number): { tone: Tone; label: string } | null {
  if (!Number.isFinite(value)) return null
  const [good, poor] = THRESHOLDS[v]
  if (value <= good) return { tone: 'ok', label: 'good' }
  if (value <= poor) return { tone: 'warn', label: 'needs improvement' }
  return { tone: 'crit', label: 'poor' }
}

export function fmtVital(v: Vital, value: number) {
  if (!Number.isFinite(value)) return '—'
  if (v === 'cls') return value.toFixed(value < 0.01 ? 3 : 2)
  return value >= 1000 ? `${(value / 1000).toFixed(2)} s` : `${Math.round(value)} ms`
}

/** ISO country code → flag emoji. */
export function flag(iso: unknown) {
  const s = String(iso ?? '')
  if (!/^[A-Za-z]{2}$/.test(s)) return ''
  return String.fromCodePoint(...[...s.toUpperCase()].map((c) => 0x1f1e6 + c.charCodeAt(0) - 65))
}
