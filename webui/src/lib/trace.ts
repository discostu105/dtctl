import { num, type Rec } from './api'
import { q } from './dql'

// The trace model behind the waterfall, flame graph and summary. Built once
// per trace in a single pass; everything the views need (tree order, self
// time, critical path, per-service totals) is precomputed here so rendering
// 10k+ spans stays cheap.

/** Lean skeleton for every span (full attributes are fetched per span on demand). */
export function traceSkeletonQuery(traceId: string, limit = TRACE_LIMIT) {
  return `fetch spans, from:now()-7d
| filter trace.id == toUid(${q(traceId)})
| fields trace.id, span.id, span.parent_id, span.name = substring(span.name, from:0, to:240), span.kind, start_time, duration, span.status_code, request.is_failed, service.name, dt.service.name, dt.smartscape.service, endpoint.name, db.system, db.system.name, http.response.status_code, gen_ai.operation.name, gen_ai.request.model, gen_ai.usage.input_tokens, gen_ai.usage.output_tokens, gen_ai.tool.name, gen_ai.agent.name, gen_ai.conversation.id
| limit ${limit}`
}

export const TRACE_LIMIT = 30000

export function spanDetailQuery(traceId: string, spanId: string) {
  return `fetch spans, from:now()-7d
| filter trace.id == toUid(${q(traceId)}) and span.id == toUid(${q(spanId)})
| limit 1`
}

export function spanLogsQuery(traceId: string, spanId: string) {
  return `fetch logs, from:now()-7d
| filter trace_id == ${q(traceId)} and span_id == ${q(spanId)}
| sort timestamp asc
| limit 200`
}

export interface TNode {
  i: number
  rec: Rec
  id: string
  name: string
  service: string
  svc: number
  parent: number
  children: number[]
  depth: number
  /** ms (float) since epoch */
  start: number
  end: number
  dur: number
  self: number
  failed: boolean
  /** subtree size, excluding the node */
  desc: number
  /** failed spans in the subtree, excluding the node */
  errorsBelow: number
  critical: boolean
}

export interface ServiceStat {
  name: string
  color: string
  spans: number
  self: number
  errors: number
}

export interface TraceModel {
  nodes: TNode[]
  roots: number[]
  t0: number
  t1: number
  services: ServiceStat[]
  maxDepth: number
  failed: number
}

// Service colors: categorical slots, red is kept for errors.
const SLOTS = ['--s1', '--s3', '--s4', '--s7', '--s5', '--s2', '--s6']
export const serviceColor = (i: number) => `var(${SLOTS[i % SLOTS.length]})`
export const serviceSlot = (i: number) => SLOTS[i % SLOTS.length]

/** Milliseconds as float: ns-since-epoch would overflow 2^53. */
export function spanStartMs(s: Rec) {
  const str = String(s.start_time)
  const m = /\.(\d+)Z$/.exec(str)
  const frac = m ? Number((m[1] + '000000000').slice(0, 9)) : 0
  return Date.parse(str.replace(/\.\d+Z$/, 'Z')) + frac / 1e6
}

export const isFailed = (r: Rec) => r['request.is_failed'] === true || r['span.status_code'] === 'error'

export function buildTrace(spans: Rec[]): TraceModel {
  const idx = new Map<string, number>()
  const tmp = spans.map((rec, k) => {
    const start = spanStartMs(rec)
    const dur = num(rec.duration) / 1e6
    idx.set(rec['span.id'], k)
    return { rec, start, end: start + dur, dur }
  })
  const kids: number[][] = spans.map(() => [])
  const rootsRaw: number[] = []
  tmp.forEach((t, k) => {
    const p = idx.get(t.rec['span.parent_id'])
    if (p != null && p !== k) kids[p].push(k)
    else rootsRaw.push(k)
  })
  const byStart = (a: number, b: number) => tmp[a].start - tmp[b].start
  kids.forEach((c) => c.sort(byStart))
  rootsRaw.sort(byStart)

  // services in order of first appearance (root service gets the first color)
  const svcIdx = new Map<string, number>()
  const svcName = (r: Rec) => String(r['dt.service.name'] ?? r['service.name'] ?? 'unknown')

  // iterative DFS → tree order
  const order: number[] = []
  const depthOf = new Array<number>(spans.length).fill(0)
  const stack = [...rootsRaw].reverse()
  const seen = new Uint8Array(spans.length)
  while (stack.length) {
    const k = stack.pop()!
    if (seen[k]) continue
    seen[k] = 1
    order.push(k)
    for (let j = kids[k].length - 1; j >= 0; j--) {
      depthOf[kids[k][j]] = depthOf[k] + 1
      stack.push(kids[k][j])
    }
  }
  const pos = new Array<number>(spans.length)
  order.forEach((k, i) => (pos[k] = i))

  const nodes: TNode[] = order.map((k, i) => {
    const t = tmp[k]
    const service = svcName(t.rec)
    if (!svcIdx.has(service)) svcIdx.set(service, svcIdx.size)
    const p = idx.get(t.rec['span.parent_id'])
    return {
      i,
      rec: t.rec,
      id: t.rec['span.id'],
      name: String(t.rec['span.name'] ?? ''),
      service,
      svc: svcIdx.get(service)!,
      parent: p != null && p !== k ? pos[p] : -1,
      children: kids[k].map((c) => pos[c]),
      depth: depthOf[k],
      start: t.start,
      end: t.end,
      dur: t.dur,
      self: t.dur,
      failed: isFailed(t.rec),
      desc: 0,
      errorsBelow: 0,
      critical: false,
    }
  })

  // subtree sizes / errors (reverse tree order = children before parents)
  for (let i = nodes.length - 1; i >= 0; i--) {
    const n = nodes[i]
    if (n.parent >= 0) {
      const p = nodes[n.parent]
      p.desc += n.desc + 1
      p.errorsBelow += n.errorsBelow + (n.failed ? 1 : 0)
    }
  }

  // self time = duration minus the union of child intervals (clipped to the span)
  for (const n of nodes) {
    if (!n.children.length) continue
    let covered = 0
    let curS = -Infinity
    let curE = -Infinity
    const iv = n.children.map((c) => [Math.max(n.start, nodes[c].start), Math.min(n.end, nodes[c].end)]).filter(([a, b]) => b > a)
    iv.sort((a, b) => a[0] - b[0])
    for (const [a, b] of iv) {
      if (a > curE) {
        if (curE > curS) covered += curE - curS
        curS = a
        curE = b
      } else curE = Math.max(curE, b)
    }
    if (curE > curS) covered += curE - curS
    n.self = Math.max(0, n.dur - covered)
  }

  // critical path (Jaeger's walk): from each root, repeatedly take the child
  // that finishes last before the cursor, then continue before its start.
  const crit = (k: number, until: number) => {
    const n = nodes[k]
    n.critical = true
    let cursor = Math.min(n.end, until)
    const cs = [...n.children].sort((a, b) => nodes[b].end - nodes[a].end)
    for (const c of cs) {
      if (nodes[c].start >= cursor) continue
      crit(c, cursor)
      cursor = nodes[c].start
      if (cursor <= n.start) break
    }
  }
  const roots = nodes.filter((n) => n.parent < 0).map((n) => n.i)
  for (const r of roots) crit(r, Infinity)

  const t0 = nodes.length ? Math.min(...roots.map((r) => nodes[r].start), ...nodes.slice(0, 1).map((n) => n.start)) : 0
  let t1 = t0 + 1
  let maxDepth = 0
  let failed = 0
  const stats = new Map<string, ServiceStat>()
  for (const n of nodes) {
    if (n.end > t1) t1 = n.end
    if (n.depth > maxDepth) maxDepth = n.depth
    if (n.failed) failed++
    const s = stats.get(n.service) ?? { name: n.service, color: serviceColor(n.svc), spans: 0, self: 0, errors: 0 }
    s.spans++
    s.self += n.self
    if (n.failed) s.errors++
    stats.set(n.service, s)
  }
  const tMin = nodes.reduce((m, n) => Math.min(m, n.start), Infinity)
  return {
    nodes,
    roots,
    t0: Number.isFinite(tMin) ? tMin : t0,
    t1,
    services: [...stats.values()].sort((a, b) => b.self - a.self),
    maxDepth,
    failed,
  }
}

/** Default expansion for big traces: show the shape, not every leaf. */
export function defaultCollapsed(m: TraceModel, force = false): Set<number> {
  const out = new Set<number>()
  if (!force && m.nodes.length <= 300) return out
  for (const n of m.nodes) {
    if (n.depth === 0 && m.roots.length <= 3) continue
    if (force ? n.children.length > 0 : n.children.length > 12 || n.desc > 60) out.add(n.i)
  }
  return out
}

/** Tree-order rows with collapsed subtrees skipped. */
export function visibleRows(m: TraceModel, collapsed: Set<number>): number[] {
  const out: number[] = []
  for (let i = 0; i < m.nodes.length; i++) {
    out.push(i)
    if (collapsed.has(i)) i += m.nodes[i].desc
  }
  return out
}

/** Expand every ancestor of a node so it becomes visible. */
export function revealed(m: TraceModel, collapsed: Set<number>, i: number): Set<number> {
  const next = new Set(collapsed)
  let p = m.nodes[i]?.parent ?? -1
  while (p >= 0) {
    next.delete(p)
    p = m.nodes[p].parent
  }
  return next
}

/**
 * Operation key for the summary: "/usr/bin/git config --local x" → "git config",
 * "GET /api/users/123" → "GET /api/users/N". Repeated work collapses into one row.
 */
export function operationOf(name: string) {
  const toks = name.trim().split(/\s+/)
  if (toks.length > 2 && (toks[0].includes('/') || toks[1].startsWith('-') || name.length > 60)) {
    const head = toks[0].split('/').pop() || toks[0]
    // a subcommand looks like a lowercase word ("config", "build"), not an id or a path
    const sub = toks.slice(1).find((t) => /^[a-z][a-z._-]{1,20}$/.test(t))
    return sub ? `${head} ${sub}` : head
  }
  return name
    .replace(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi, '{uuid}')
    .replace(/\b[0-9a-f]{16,}\b/gi, '{id}')
    .replace(/\d+/g, 'N')
}

export interface OpStat {
  key: string
  op: string
  service: string
  svc: number
  count: number
  total: number
  self: number
  max: number
  errors: number
  first: number
}

export function operations(m: TraceModel): OpStat[] {
  const map = new Map<string, OpStat>()
  for (const n of m.nodes) {
    const op = operationOf(n.name)
    const key = `${n.service}\u0000${op}`
    const s = map.get(key) ?? { key, op, service: n.service, svc: n.svc, count: 0, total: 0, self: 0, max: 0, errors: 0, first: n.i }
    s.count++
    s.total += n.dur
    s.self += n.self
    if (n.dur > s.max) s.max = n.dur
    if (n.failed) s.errors++
    map.set(key, s)
  }
  return [...map.values()].sort((a, b) => b.self - a.self)
}

/** "Nice" tick step for a time span in ms. */
export function tickStep(span: number, target = 6) {
  const raw = span / target
  const steps = [1, 2, 5, 10, 20, 50, 100, 200, 500, 1e3, 2e3, 5e3, 10e3, 15e3, 30e3, 60e3, 120e3, 300e3, 600e3, 900e3, 1800e3, 3600e3]
  const sub = [0.001, 0.002, 0.005, 0.01, 0.02, 0.05, 0.1, 0.2, 0.5]
  for (const s of [...sub, ...steps]) if (s >= raw) return s
  return steps[steps.length - 1]
}
