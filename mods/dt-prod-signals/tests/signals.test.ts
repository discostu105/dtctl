import { describe, expect, test } from 'claude-code/testing'

import { buildDql, forModel, headline, parseEnvelope, repoKey, targetFor, totals, worst } from '../hooks/signals'

describe('repoKey', () => {
  test('normalises https, scp and ssh remotes', () => {
    expect(repoKey('https://github.com/Acme/Shop.git\n')).toBe('github.com/acme/shop')
    expect(repoKey('git@github.com:Acme/Shop.git')).toBe('github.com/acme/shop')
    expect(repoKey('ssh://git@github.com:22/acme/shop.git')).toBe('github.com/acme/shop')
    expect(repoKey('https://user:tok@gitlab.example.invalid/a/b/c')).toBe('gitlab.example.invalid/a/b/c')
    expect(repoKey('')).toBeNull()
  })
})

describe('targetFor', () => {
  test('a Go file matches by package scope', () => {
    const t = targetFor('internal/shared/dql/client.go')
    expect(t?.scopeSuffix).toBe('/internal/shared/dql')
    expect(t?.pathTail).toBe('dql/client.go')
  })
  test('a JVM file matches by class', () => {
    const t = targetFor('svc/src/main/java/com/acme/cart/CartService.java')
    expect(t?.namespace).toBe('com.acme.cart.CartService')
    expect(t?.frameNeedle).toBe('com.acme.cart.CartService.')
  })
  test('tests, vendored code and non-source files are skipped', () => {
    expect(targetFor('internal/api/handler_test.go')).toBeNull()
    expect(targetFor('web/src/cart.test.ts')).toBeNull()
    expect(targetFor('tests/test_proxy.py')).toBeNull()
    expect(targetFor('node_modules/x/index.js')).toBeNull()
    expect(targetFor('README.md')).toBeNull()
  })
})

describe('buildDql', () => {
  test('scopes to the repo and matches every code location', () => {
    const dql = buildDql(targetFor('internal/shared/dql/client.go')!, 'github.com/acme/shop', '2h')
    expect(dql).toContain('fetch spans, from:now()-2h')
    expect(dql).toContain('endsWith(lower(vcs.repository.url.full), "github.com/acme/shop")')
    expect(dql).toContain('endsWith(otel.scope.name, "/internal/shared/dql")')
    expect(dql).toContain('endsWith(code.filepath, "dql/client.go")')
    expect(dql).toContain('span.events[][exception.stack_trace]')
  })
  test('without a repo there is no repo filter, and quotes are escaped', () => {
    const dql = buildDql(targetFor('we"ird/x.py')!, null, '30m')
    expect(dql).not.toContain('vcs.repository.url.full')
    expect(dql).toContain('"we\\"ird/x.py"')
  })
})

const ENVELOPE = JSON.stringify({
  ok: true,
  result: {
    kind: 'records',
    records: [
      { 'span.name': 'dql.execute', 'dt.service.name': 'shop (prod)', spans: '9000', errors: '120', exceptions: '40', p50: '1000000', p95: '294000000', msg: 'request failed with status 403' },
      { 'span.name': 'dql.parse', 'dt.service.name': 'shop (prod)', spans: '1000', errors: '0', exceptions: '0', p50: '200000', p95: '900000' },
    ],
  },
  metadata: { scannedBytes: 2999673523 },
})

describe('parseEnvelope', () => {
  test('reads rows, numbers given as strings, and the scan size', () => {
    const parsed = parseEnvelope(ENVELOPE, '', 0)
    expect(parsed.ok).toBe(true)
    if (!parsed.ok) return
    expect(parsed.rows).toHaveLength(2)
    expect(totals(parsed.rows)).toEqual({ spans: 10000, errors: 120, exceptions: 40, p95: 294000000 })
    expect(worst(parsed.rows)?.op).toBe('dql.execute')
    expect(parsed.scannedBytes).toBe(2999673523)
  })
  test('columns compacted into result.constant are read back into every row', () => {
    const env = JSON.stringify({ ok: true, result: { constant: { 'span.name': 'dql.execute', msg: 'status 403' }, records: [{ 'dt.service.name': 'a', spans: '2', errors: '1' }, { 'dt.service.name': 'b', spans: '3', errors: '1' }] } })
    const parsed = parseEnvelope(env, '', 0)
    expect(parsed.ok && parsed.rows.map(r => `${r.op}/${r.service}/${r.message}`)).toEqual(['dql.execute/a/status 403', 'dql.execute/b/status 403'])
  })
  test('an error envelope and garbage both become errors', () => {
    expect(parseEnvelope(JSON.stringify({ ok: false, error: { code: 'auth', message: 'token expired' } }), '', 1)).toEqual({ ok: false, error: 'token expired' })
    expect(parseEnvelope('', 'Error: no context configured\n', 1)).toEqual({ ok: false, error: 'Error: no context configured' })
  })
})

test('headline and model text', () => {
  const parsed = parseEnvelope(ENVELOPE, '', 0)
  if (!parsed.ok) throw new Error('parse')
  const s = { rel: 'a/b.go', status: 'ok' as const, matchedBy: 'x', timeframe: '2h', dql: 'fetch spans', rows: parsed.rows, scannedBytes: parsed.scannedBytes, error: null, fetchedAt: 0 }
  expect(headline(s)).toBe('10k spans · 120 errors (1.2%) · 40 exceptions · p95 294ms · last 2h')
  expect(forModel(s)).toContain('sample error: request failed with status 403')
  expect(forModel(s)).toContain('scanned 3.0 GB')
})
