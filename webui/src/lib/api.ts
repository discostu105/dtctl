import { keepPreviousData, QueryClient, useQuery, type UseQueryResult } from '@tanstack/react-query'

export type Rec = Record<string, any>

export interface DqlMeta {
  executionMs: number
  scannedRecords: number
  scannedBytes: number
  sampled?: boolean
  from?: string
  to?: string
  notifications?: string[]
}

export interface DqlResult {
  records: Rec[]
  types?: Record<string, string>
  meta?: DqlMeta
  cached?: boolean
  elapsedMs: number
}

export interface DqlSpec {
  query: string
  /** "now-2h" | RFC3339; omitted → the query's own timeframe */
  from?: string
  to?: string
  maxRecords?: number
  /** seconds a server-cached result may be reused */
  ttl?: number
}

export class DqlError extends Error {}

const HEADERS = { 'X-Dtctl-Web': '1' }

// ── batcher ────────────────────────────────────────────────────────────────
// Every query issued within one animation tick goes out as ONE request.
// The server streams NDJSON lines back as each query completes, so the UI
// paints panels progressively — and never queues behind the browser's
// six-connections-per-host limit.

interface Pending {
  id: string
  spec: DqlSpec
  fresh: boolean
  resolve: (r: DqlResult) => void
  reject: (e: Error) => void
}

let queue: Pending[] = []
let scheduled = false
let seq = 0
let freshUntil = 0

/** Mark the next wave of queries as cache-bypassing (manual refresh). */
export function forceFresh() {
  freshUntil = Date.now() + 1500
}

function flush() {
  scheduled = false
  const batch = queue
  queue = []
  // The server accepts ≤32 per batch.
  for (let i = 0; i < batch.length; i += 32) sendBatch(batch.slice(i, i + 32))
}

async function sendBatch(batch: Pending[]) {
  const byId = new Map(batch.map((p) => [p.id, p]))
  try {
    const res = await fetch('/api/batch', {
      method: 'POST',
      headers: { ...HEADERS, 'Content-Type': 'application/json' },
      body: JSON.stringify(batch.map((p) => ({ id: p.id, ...p.spec, fresh: p.fresh || undefined }))),
    })
    if (!res.ok || !res.body) throw new DqlError(`batch failed: HTTP ${res.status}`)
    const reader = res.body.getReader()
    const decoder = new TextDecoder()
    let buf = ''
    for (;;) {
      const { value, done } = await reader.read()
      if (done) break
      buf += decoder.decode(value, { stream: true })
      let nl: number
      while ((nl = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, nl)
        buf = buf.slice(nl + 1)
        if (!line.trim()) continue
        const msg = JSON.parse(line)
        const p = byId.get(msg.id)
        if (!p) continue
        byId.delete(msg.id)
        if (msg.ok) p.resolve({ records: msg.records ?? [], types: msg.types, meta: msg.meta, cached: msg.cached, elapsedMs: msg.elapsedMs })
        else p.reject(new DqlError(cleanError(msg.error)))
      }
    }
    for (const p of byId.values()) p.reject(new DqlError('no result returned'))
  } catch (e) {
    for (const p of byId.values()) p.reject(e instanceof Error ? e : new Error(String(e)))
  }
}

function cleanError(msg: string | undefined) {
  if (!msg) return 'query failed'
  return msg.replace(/^query failed \(([A-Z_]+)\):\s*/, '$1: ')
}

export function runDql(spec: DqlSpec): Promise<DqlResult> {
  return new Promise((resolve, reject) => {
    queue.push({ id: String(++seq), spec, fresh: Date.now() < freshUntil, resolve, reject })
    if (!scheduled) {
      scheduled = true
      // A macrotask, not a microtask: lets every component in the same
      // render pass enqueue before we send.
      setTimeout(flush, 0)
    }
  })
}

// ── react-query integration ───────────────────────────────────────────────

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 20_000,
      gcTime: 10 * 60_000,
      retry: 0,
      refetchOnWindowFocus: false,
    },
  },
})

export const dqlKey = (s: DqlSpec) => ['dql', s.query, s.from ?? '', s.to ?? '', s.maxRecords ?? 0] as const

export function useDql(spec: DqlSpec | null | undefined, opts?: { refetchInterval?: number }): UseQueryResult<DqlResult, Error> {
  return useQuery({
    queryKey: spec ? dqlKey(spec) : ['dql-disabled'],
    queryFn: () => runDql(spec!),
    enabled: !!spec,
    // Changing the timeframe keeps the old data on screen (dimmed) instead
    // of flashing back to placeholders.
    placeholderData: keepPreviousData,
    refetchInterval: opts?.refetchInterval,
  })
}

/** Warm the cache on hover/focus so the click lands on data. */
export function prefetchDql(spec: DqlSpec) {
  return queryClient.prefetchQuery({ queryKey: dqlKey(spec), queryFn: () => runDql(spec), staleTime: 20_000 })
}

export async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url, { headers: HEADERS })
  if (!res.ok) {
    let msg = `HTTP ${res.status}`
    try {
      msg = (await res.json()).error ?? msg
    } catch {
      /* not json */
    }
    throw new Error(msg)
  }
  return res.json()
}

export interface Meta {
  context: string
  environment: string
  safetyLevel: string
  version: string
  userName?: string
  userEmail?: string
  contexts?: string[]
}

export function useMeta() {
  return useQuery({ queryKey: ['meta'], queryFn: () => getJSON<Meta>('/api/meta'), staleTime: Infinity })
}

// ── record helpers ─────────────────────────────────────────────────────────

/** DQL longs arrive as strings; coerce anything numeric-ish. */
export function num(v: unknown): number {
  if (typeof v === 'number') return v
  if (typeof v === 'string' && v !== '') return Number(v)
  return NaN
}

export function arr(v: unknown): any[] {
  return Array.isArray(v) ? v : v == null ? [] : [v]
}
