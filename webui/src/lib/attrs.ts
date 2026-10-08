// Server-side attribute filters ("attrs"): filters that narrow a list's DQL on
// the server, so they reach what the loaded rows don't carry — Smartscape tag
// maps (Kubernetes labels and annotations, AWS/Azure/GCP tags), primary tags,
// every raw entity attribute — and keep working past a list's row cap.
//
// Ported from dynatui's catalog/facets.go. The encodings below were validated
// live there and re-checked against real tenants for this UI:
//   * tag maps are bracket-indexed with both segments backtick-quoted:
//     `tags:k8s.labels`[`app.kubernetes.io/name`]; filter and fieldsSummary
//     both accept that expression straight off smartscapeNodes
//   * map filters must run before any fields/summarize stage (those drop the
//     maps), so stages go directly after the source line
//   * never wrap the field in toString() (mutes Grail's indexes); the shape of
//     the stringified value picks typed comparison legs instead
//   * exclusion uses filterOut, which keeps records where the field is unset;
//     not(…) would silently drop them too
//   * primary tags are flat dotted fields (primary_tags.team), not a map

import { NONE } from './facets'

/** Rows whose value is missing: the "not set" bucket. Same marker as client facets. */
export const UNSET = NONE

/**
 * One server-side filter. `field` is the identity: a scalar field
 * (`k8s.namespace.name`, `primary_tags.team`) or a tag-map key
 * (`tags:k8s.labels[app]`). Values of one field are OR-ed, fields are AND-ed.
 */
export interface AttrFilter {
  field: string
  value: string
  neg?: boolean
}

export interface AttrRef {
  field: string
  /** tag map field, e.g. "tags:k8s.labels" (undefined for scalars) */
  map?: string
  /** key inside the map, e.g. "app" */
  key?: string
}

const MAP_REF = /^(tags:[^[\]]+)\[(.+)\]$/
const BARE = /^[A-Za-z_][A-Za-z0-9_.]*$/

export function parseField(field: string): AttrRef {
  const m = MAP_REF.exec(field)
  return m ? { field, map: m[1], key: m[2] } : { field }
}

export const mapField = (map: string, key: string) => `${map}[${key}]`

/** The DQL reference for a field identity. */
export function fieldExpr(field: string): string {
  const r = parseField(field)
  if (r.map) return `\`${r.map}\`[\`${r.key}\`]`
  // Special-char keys backtick the WHOLE path (`primary_tags.cost-center`);
  // segment-quoting is a parse error.
  return BARE.test(field) ? field : `\`${field}\``
}

const lit = (s: string) => JSON.stringify(s)

const SMARTSCAPE_ID = /^[A-Z][A-Z0-9_]*-[0-9A-F]{16}$/
const UID_HEX = /^([0-9a-fA-F]{16}|[0-9a-fA-F]{32})$/
const UUID = /^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$/
const IPV4 = /^(\d{1,3}\.){3}\d{1,3}$/
const INTEGER = /^-?[0-9]+$/
const DECIMAL = /^-?[0-9]+\.[0-9]+$/

export const isPattern = (v: string) => v.includes('*')

/** Predicate for one value, typed by the value's shape (fieldsSummary stringifies everything). */
export function valueCond(expr: string, v: string): string {
  if (v === UNSET) return `isNull(${expr})`
  if (isPattern(v)) return `matchesValue(${expr}, ${lit(v)})`
  if (v === 'true' || v === 'false') return `(${expr} == ${v} or ${expr} == ${lit(v)})`
  if (UID_HEX.test(v)) return `(${expr} == ${lit(v)} or ${expr} == toUid(${lit(v)}) or ${expr} ~ ${lit(v)})`
  if (SMARTSCAPE_ID.test(v) || UUID.test(v) || IPV4.test(v)) return `(${expr} == ${lit(v)} or ${expr} ~ ${lit(v)})`
  if (INTEGER.test(v)) return `(${expr} == ${v} or ${expr} == ${lit(v)})`
  if (DECIMAL.test(v)) return `(${expr} == ${v} or ${expr} == ${lit(v)})`
  return `${expr} == ${lit(v)}`
}

function groupCond(field: string, values: string[]) {
  const expr = fieldExpr(field)
  // Plain strings collapse into one in(); typed values keep their legs.
  const plain = values.filter((v) => v !== UNSET && !isPattern(v) && valueCond(expr, v) === `${expr} == ${lit(v)}`)
  const rest = values.filter((v) => !plain.includes(v))
  const parts = [...(plain.length > 1 ? [`in(${expr}, {${plain.map(lit).join(', ')}})`] : plain.map((v) => valueCond(expr, v))), ...rest.map((v) => valueCond(expr, v))]
  return parts.length === 1 ? parts[0] : `(${parts.join(' or ')})`
}

/** Filters grouped by field and polarity, in first-seen order. */
export function groupAttrs(filters: AttrFilter[]) {
  const m = new Map<string, { field: string; neg: boolean; values: string[] }>()
  for (const f of filters) {
    const k = `${f.neg ? '-' : ''}${f.field}`
    const g = m.get(k) ?? { field: f.field, neg: !!f.neg, values: [] }
    g.values.push(f.value)
    m.set(k, g)
  }
  return [...m.values()]
}

/** `| filter` / `| filterOut` stages for a pipeline; `except` skips one field (exclude-self counts). */
export function attrStages(filters: AttrFilter[], except?: string): string[] {
  return groupAttrs(filters.filter((f) => f.field !== except)).map((g) => `| ${g.neg ? 'filterOut' : 'filter'} ${groupCond(g.field, g.values)}`)
}

/** One boolean expression, for places that take a condition (timeseries `filter:{}`). */
export function attrCondition(filters: AttrFilter[], except?: string): string {
  return groupAttrs(filters.filter((f) => f.field !== except))
    .map((g) => {
      const c = groupCond(g.field, g.values)
      // keep records where the field is unset, like filterOut does
      return g.neg ? `(not(${c}) or isNull(${fieldExpr(g.field)}))` : c
    })
    .join(' and ')
}

/** Inject the filter stages directly after the source command (line 1). */
export function withAttrs(query: string, filters: AttrFilter[]): string {
  if (!filters.length) return query
  const lines = query.split('\n')
  return [lines[0], ...attrStages(filters), ...lines.slice(1)].join('\n')
}

// ── URL ─────────────────────────────────────────────────────────────────────
// ?a=tags:k8s.labels[app]=checkout  &a=-primary_tags.team=payments
// The field ends at the first '=' outside [...] (map keys may contain '=').

export function parseAttr(s: string): AttrFilter | null {
  const neg = s.startsWith('-')
  const body = neg ? s.slice(1) : s
  let depth = 0
  for (let i = 0; i < body.length; i++) {
    const c = body[i]
    if (c === '[') depth++
    else if (c === ']') depth = Math.max(0, depth - 1)
    else if (c === '=' && depth === 0 && i > 0) {
      const field = body.slice(0, i)
      // DQL-injection guard: identities never carry backticks or line breaks
      if (/[`\n\r|]/.test(field)) return null
      return { field, value: body.slice(i + 1), neg }
    }
  }
  return null
}

export const serializeAttr = (f: AttrFilter) => `${f.neg ? '-' : ''}${f.field}=${f.value}`

export function parseAttrs(params: URLSearchParams, name = 'a'): AttrFilter[] {
  return params.getAll(name).flatMap((s) => {
    const f = parseAttr(s)
    return f ? [f] : []
  })
}

export const sameAttr = (a: AttrFilter, b: AttrFilter) => a.field === b.field && a.value === b.value && !!a.neg === !!b.neg

// ── naming ──────────────────────────────────────────────────────────────────

/** Tag contexts users reach for most come first; the rest follow alphabetically. */
const CONTEXT_ORDER = ['k8s.labels', 'aws', 'azure', 'google_compute_engine', 'k8s.annotations']

const CONTEXTS: Record<string, { group: string; short: string }> = {
  'k8s.labels': { group: 'Kubernetes labels', short: 'label' },
  'k8s.annotations': { group: 'Kubernetes annotations', short: 'annotation' },
  aws: { group: 'AWS tags', short: 'AWS tag' },
  azure: { group: 'Azure tags', short: 'Azure tag' },
  google_compute_engine: { group: 'GCP labels', short: 'GCP label' },
  environment: { group: 'Environment tags', short: 'tag' },
  kubernetes: { group: 'Kubernetes tags', short: 'tag' },
}

export const PRIMARY_TAGS = 'Primary tags'
export const NS_LABELS = 'Namespace labels'
export const ATTRIBUTES = 'Attributes'

export function contextOf(ctx: string) {
  return CONTEXTS[ctx] ?? { group: `${ctx} tags`, short: `${ctx} tag` }
}

/** How a field reads in chips and headers: { kind: "label", name: "app" }. */
export function describeField(field: string): { group: string; kind: string; name: string } {
  const r = parseField(field)
  if (r.map) {
    const c = contextOf(r.map.slice('tags:'.length))
    return { group: c.group, kind: c.short, name: r.key! }
  }
  if (field.startsWith('primary_tags.')) return { group: PRIMARY_TAGS, kind: 'primary tag', name: field.slice('primary_tags.'.length) }
  if (field.startsWith('k8s.namespace.label.')) return { group: NS_LABELS, kind: 'namespace label', name: field.slice('k8s.namespace.label.'.length) }
  return { group: ATTRIBUTES, kind: '', name: field }
}

export function groupRank(group: string) {
  if (group === PRIMARY_TAGS) return 0
  const i = CONTEXT_ORDER.findIndex((c) => contextOf(c).group === group)
  if (i >= 0) return 1 + i
  if (group === ATTRIBUTES) return 100
  if (group === NS_LABELS) return 90
  return 50
}

// ── sources: discovery + value exploration ─────────────────────────────────

/**
 * Where a list's attributes come from.
 *   nodes            smartscapeNodes lists: discovery samples raw entities,
 *                    values come from fieldsSummary over the whole population
 *   service-metrics  the services roster: dimensions of the four service
 *                    metrics (primary tags live there, not on SERVICE nodes);
 *                    values count distinct services
 */
export interface AttrSource {
  kind: 'nodes' | 'service-metrics'
  /** nodes: the source command, e.g. `smartscapeNodes "K8S_POD"` */
  head: string
  /** timeframe for metric-backed sources */
  from?: string
  to?: string
}

export const SERVICE_METRICS = `{"dt.service.request.count", "dt.service.faas_invoke.count", "dt.service.messaging.process.count", "dt.service.request.service_mesh.count"}`

export const nodesSource = (types: string | string[]): AttrSource => ({
  kind: 'nodes',
  head: `smartscapeNodes ${(Array.isArray(types) ? types : [types]).map(lit).join(', ')}`,
})

const SAMPLE = 300

/** A bounded raw sample: every tag map and scalar is visible, manifests stripped. */
export function discoveryQuery(src: AttrSource): string {
  if (src.kind === 'service-metrics') return `metrics\n| filter in(metric.key, ${SERVICE_METRICS})\n| limit ${SAMPLE}`
  // fieldsRemove tolerates absent fields, so every heavy manifest can be listed
  return `${src.head}\n| fieldsRemove k8s.object, aws.object, azure.object, gcp.object, references\n| limit ${SAMPLE}`
}

/** Fields that are identities, plumbing or noise as filters. */
const SKIP = new Set(['id', 'id_classic', 'metric.key', 'interval', 'lifetime', 'dt.security_context', 'k8s.cluster.uid', 'k8s.pod.uid', 'dt.process_group.id'])

export interface AttrCandidate {
  field: string
  group: string
  name: string
  /** share of sampled records that carry it (0–1) */
  coverage: number
}

export function parseDiscovery(records: Record<string, any>[]): AttrCandidate[] {
  const seen = new Map<string, number>()
  const n = records.length || 1
  const bump = (f: string) => seen.set(f, (seen.get(f) ?? 0) + 1)
  for (const r of records) {
    for (const [k, v] of Object.entries(r)) {
      if (v == null || v === '' || SKIP.has(k) || k.startsWith('dt.smartscape.')) continue
      if (k.startsWith('tags:')) {
        if (typeof v === 'object' && !Array.isArray(v)) for (const key of Object.keys(v)) bump(mapField(k, key))
        continue
      }
      if (typeof v === 'object') continue // arrays and records are not scalar facets
      bump(k)
    }
  }
  return [...seen].map(([field, c]) => ({ field, ...describeField(field), coverage: c / n }))
}

export const TOP_VALUES = 200

/** Top values of one field over the population narrowed by every other filter. */
export function valuesQuery(src: AttrSource, field: string, filters: AttrFilter[]): string {
  const expr = fieldExpr(field)
  const others = attrStages(filters, field)
  if (src.kind === 'service-metrics')
    return [
      'metrics',
      `| filter in(metric.key, ${SERVICE_METRICS})`,
      ...others,
      `| summarize n = countDistinct(dt.smartscape.service), by:{v = ${expr}}`,
      '| sort n desc',
      `| limit ${TOP_VALUES}`,
    ].join('\n')
  return [src.head, ...others, `| fieldsSummary ${expr}, topValues: ${TOP_VALUES}`].join('\n')
}

export interface AttrValues {
  values: { value: string; count: number }[]
  /** entities in the narrowed population (nodes only) */
  total?: number
}

const str = (v: unknown) => (v == null ? '' : typeof v === 'object' ? JSON.stringify(v) : String(v))

export function parseValues(src: AttrSource, records: Record<string, any>[]): AttrValues {
  if (src.kind === 'service-metrics') {
    return { values: records.map((r) => ({ value: r.v == null || r.v === '' ? UNSET : str(r.v), count: Number(r.n) || 0 })) }
  }
  const r = records[0]
  if (!r) return { values: [] }
  const values = ((r.values ?? []) as { value: unknown; count: unknown }[]).map((x) => ({
    value: x.value == null || x.value === '' ? UNSET : str(x.value),
    count: Number(x.count) || 0,
  }))
  return { values, total: Number(r.count) || 0 }
}
