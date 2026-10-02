// Pure logic: file → DQL, dtctl envelope → rows, rows → words. No `$` here,
// so the tests exercise it directly.

import type { FileSignal, SignalRow } from '../types'

const SOURCE_EXT = /\.(go|py|java|kt|scala|ts|tsx|js|jsx|mjs|cjs|rb|cs|php|rs)$/
const SKIP_PATH = /(^|\/)(node_modules|vendor|third_party|dist|build|testdata|__pycache__)\//
const TEST_FILE = /(_test\.go|\.(test|spec)\.[jt]sx?|(^|\/)test_[^/]+\.py|_test\.py|Test\.(java|kt))$/

/** How one file is recognised in span data. */
export type FileTarget = {
  rel: string
  /** Suffix of `otel.scope.name` (a Go package import path ends with its directory). */
  scopeSuffix: string | null
  /** Exact `code.namespace` (a JVM class). */
  namespace: string | null
  /** Suffix of `code.filepath`, and text a stack frame of the file contains. */
  pathTail: string
  /** Text a stack frame contains, when it differs from `pathTail` (JVM: `com.x.Cls.`). */
  frameNeedle: string
  matchedBy: string
}

/**
 * Normalises a git remote to `host/org/repo`, lower-case: https, ssh and
 * scp-style remotes, credentials, ports and `.git` removed.
 */
export function repoKey(remote: string): string | null {
  let s = remote.trim()
  if (s === '') return null
  s = s.replace(/^[a-z+]+:\/\//i, '')
  s = s.replace(/^[^@/]+@/, '')
  s = s.replace(/^([^/:]+):(?!\d+\/)/, '$1/')
  s = s.replace(/^([^/:]+):\d+\//, '$1/')
  s = s.replace(/\.git$/, '').replace(/\/+$/, '')

  return s.includes('/') ? s.toLowerCase() : null
}

/** The target for a repo-relative path, or null when it is not production source. */
export function targetFor(rel: string): FileTarget | null {
  if (!SOURCE_EXT.test(rel) || SKIP_PATH.test(rel) || TEST_FILE.test(rel)) return null

  const parts = rel.split('/')
  const pathTail = parts.slice(-2).join('/')
  const dir = parts.slice(0, -1).join('/')
  const ext = rel.slice(rel.lastIndexOf('.') + 1)

  if (ext === 'go') {
    return {
      rel,
      scopeSuffix: dir === '' ? null : `/${dir}`,
      namespace: null,
      pathTail,
      frameNeedle: pathTail,
      matchedBy:
        dir === ''
          ? `code.filepath or stack frames ending ${pathTail}`
          : `Go package …/${dir} (instrumentation scope), code.filepath or stack frames ending ${pathTail}`,
    }
  }

  const jvm = /(?:^|\/)src\/main\/(?:java|kotlin|scala)\/(.+)\.(?:java|kt|scala)$/.exec(rel)
  if (jvm) {
    const cls = (jvm[1] ?? '').split('/').join('.')

    return {
      rel,
      scopeSuffix: null,
      namespace: cls,
      pathTail,
      frameNeedle: `${cls}.`,
      matchedBy: `class ${cls} (code.namespace or stack frames)`,
    }
  }

  return {
    rel,
    scopeSuffix: null,
    namespace: null,
    pathTail,
    frameNeedle: pathTail,
    matchedBy: `code.filepath or stack frames ending ${pathTail}`,
  }
}

/** A DQL string literal. */
export function lit(s: string): string {
  return `"${s.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`
}

/** One query per file: matching spans summarised per operation and service. */
export function buildDql(t: FileTarget, repo: string | null, timeframe: string): string {
  const match = [
    t.scopeSuffix && `endsWith(otel.scope.name, ${lit(t.scopeSuffix)})`,
    t.namespace && `code.namespace == ${lit(t.namespace)}`,
    `endsWith(code.filepath, ${lit(t.pathTail)})`,
    `iAny(contains(coalesce(span.events[][exception.stack_trace], span.events[][exception.stacktrace]), ${lit(t.frameNeedle)}))`,
  ].filter(Boolean)

  return [
    `fetch spans, from:now()-${timeframe}`,
    repo &&
      `| filter endsWith(lower(vcs.repository.url.full), ${lit(repo)}) or endsWith(lower(vcs.repository.url.full), ${lit(`${repo}.git`)})`,
    `| filter ${match.join('\n    or ')}`,
    `| fieldsAdd exc = iAny(span.events[][span_event.name] == "exception"), err = span.status_code == "error"`,
    `| summarize spans = count(), errors = countIf(err), exceptions = countIf(exc),`,
    `    p50 = percentile(duration, 50), p95 = percentile(duration, 95),`,
    `    msg = takeAny(if(err, span.status_message)),`,
    `    by:{span.name, dt.service.name}`,
    `| sort errors desc, exceptions desc, spans desc`,
    `| limit 8`,
  ]
    .filter(Boolean)
    .join('\n')
}

const num = (v: unknown): number => {
  const n = typeof v === 'number' ? v : Number(v)

  return Number.isFinite(n) ? n : 0
}

const str = (v: unknown): string | null => (typeof v === 'string' && v !== '' ? v : null)

export type Parsed =
  | { ok: true; rows: SignalRow[]; scannedBytes: number | null }
  | { ok: false; error: string }

/** Reads `dtctl query --agent -o json` output. */
export function parseEnvelope(stdout: string, stderr: string, exitCode: number): Parsed {
  let env: any
  try {
    env = JSON.parse(stdout)
  } catch {
    const why = (stderr || stdout).trim().split('\n').slice(-1)[0] || `dtctl exited ${exitCode}`

    return { ok: false, error: why.slice(0, 300) }
  }

  if (env?.ok !== true) {
    const message = env?.error?.message ?? env?.error?.code ?? `dtctl exited ${exitCode}`

    return { ok: false, error: String(message).slice(0, 300) }
  }

  // Agent mode compacts columns that hold one value in every row into
  // `result.constant`: a row is constant + record.
  const constant = env.result?.constant && typeof env.result.constant === 'object' ? env.result.constant : {}
  const records: unknown[] = Array.isArray(env.result?.records) ? env.result.records : []
  const rows = records.map((rec: any): SignalRow => {
    const r = { ...constant, ...rec }

    return {
      op: str(r['span.name']) ?? '(unnamed span)',
      service: str(r['dt.service.name']) ?? '(no service)',
      spans: num(r.spans),
      errors: num(r.errors),
      exceptions: num(r.exceptions),
      p50: num(r.p50),
      p95: num(r.p95),
      message: str(r.msg),
    }
  })
  const scanned = env.metadata?.scannedBytes

  return { ok: true, rows, scannedBytes: scanned === undefined ? null : num(scanned) }
}

export type Totals = { spans: number; errors: number; exceptions: number; p95: number }

/** Sums over the rows; p95 is the worst operation's (percentiles do not add). */
export function totals(rows: readonly SignalRow[]): Totals {
  return rows.reduce<Totals>(
    (t, r) => ({
      spans: t.spans + r.spans,
      errors: t.errors + r.errors,
      exceptions: t.exceptions + r.exceptions,
      p95: Math.max(t.p95, r.p95),
    }),
    { spans: 0, errors: 0, exceptions: 0, p95: 0 },
  )
}

export function compact(n: number): string {
  if (n < 1000) return String(Math.round(n))
  if (n < 1e6) return `${trim(n / 1e3)}k`
  if (n < 1e9) return `${trim(n / 1e6)}M`

  return `${trim(n / 1e9)}B`
}

const trim = (x: number): string => (x >= 100 ? x.toFixed(0) : x.toFixed(1).replace(/\.0$/, ''))

/** Nanoseconds as ms or s. */
export function duration(ns: number): string {
  const ms = ns / 1e6
  if (ms < 1) return `${ms.toFixed(2)}ms`
  if (ms < 1000) return `${Math.round(ms)}ms`

  return `${(ms / 1000).toFixed(1)}s`
}

export function bytes(n: number): string {
  if (n < 1e6) return `${Math.round(n / 1e3)} KB`
  if (n < 1e9) return `${Math.round(n / 1e6)} MB`

  return `${(n / 1e9).toFixed(1)} GB`
}

export function percent(part: number, whole: number): string {
  if (whole === 0) return '0%'
  const p = (100 * part) / whole

  return p >= 10 ? `${Math.round(p)}%` : `${p.toFixed(1)}%`
}

/** The one-line verdict the band and the tool lead with. */
export function headline(s: FileSignal): string {
  switch (s.status) {
    case 'loading':
      return 'querying Dynatrace…'
    case 'error':
      return `lookup failed: ${s.error ?? 'unknown error'}`
    case 'empty':
      return `no production spans matched in the last ${s.timeframe}`
    case 'ok': {
      const t = totals(s.rows)

      return [
        `${compact(t.spans)} spans`,
        `${compact(t.errors)} errors (${percent(t.errors, t.spans)})`,
        `${compact(t.exceptions)} exceptions`,
        `p95 ${duration(t.p95)}`,
        `last ${s.timeframe}`,
      ].join(' · ')
    }
  }
}

/** The worst operation: the most errors, then exceptions. */
export function worst(rows: readonly SignalRow[]): SignalRow | null {
  const bad = rows.filter(r => r.errors > 0 || r.exceptions > 0)

  return bad.length === 0
    ? null
    : bad.reduce((a, b) => (b.errors > a.errors || (b.errors === a.errors && b.exceptions > a.exceptions) ? b : a))
}

/** What the model reads from the lookup tool. */
export function forModel(s: FileSignal): string {
  const lines = [`Dynatrace production signals for ${s.rel}: ${headline(s)}.`]
  if (s.status === 'ok') {
    lines.push(`Matched by ${s.matchedBy}. Per operation (span name · service):`)
    for (const r of s.rows) {
      lines.push(
        `- ${r.op} · ${r.service}: ${r.spans} spans, ${r.errors} errors, ${r.exceptions} exceptions, p50 ${duration(r.p50)}, p95 ${duration(r.p95)}${r.message ? `; sample error: ${r.message}` : ''}`,
      )
    }
    if (s.scannedBytes !== null) lines.push(`(Query scanned ${bytes(s.scannedBytes)}.)`)
  } else if (s.status === 'empty') {
    lines.push(
      `Matched by ${s.matchedBy}. No match means no telemetry was attributed to this file, not that it is unused.`,
    )
  }
  lines.push(`DQL used:\n${s.dql}`)

  return lines.join('\n')
}
