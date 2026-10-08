import type { Page, Route } from '@playwright/test'

// A stand-in for `dtctl serve web`: answers every /api endpoint the SPA calls
// with synthetic data (RFC 2606 names only). Queries are matched on their text;
// anything unrecognized gets an empty result, which every page must handle.

type Rec = Record<string, unknown>

interface Spec {
  id: string
  query: string
  from?: string
  to?: string
}

const META = {
  context: 'test',
  environment: 'https://tenant.example.invalid/',
  safetyLevel: 'readonly',
  version: '0.0.0-test',
  userName: 'Test User',
  userEmail: 'user@example.invalid',
  contexts: ['test', 'other'],
  tenants: [
    { name: 'test', environment: 'https://tenant.example.invalid/', safetyLevel: 'readonly' },
    { name: 'other', environment: 'https://other.example.invalid/', safetyLevel: 'readonly' },
  ],
}

const ACTIVITY = {
  maxConcurrent: 8,
  running: [],
  queued: [],
  recent: [],
  totals: { requested: 0, executed: 0, cached: 0, shared: 0, errors: 0, cancelled: 0, scannedBytes: 0 },
}

// one owner IAM resolved, one it could not (no iam:users:read, or a service user)
const DOCS = [
  { id: 'doc-1', name: 'Checkout overview', type: 'dashboard', owner: '00000000-0000-0000-0000-000000000001', ownerName: 'Ada Example', modified: '2026-01-10T12:00:00Z', isPrivate: false },
  { id: 'doc-2', name: 'Capacity review', type: 'dashboard', owner: '00000000-0000-0000-0000-000000000002', modified: '2026-01-09T12:00:00Z', isPrivate: true },
]

const hosts = (n: number): Rec[] => Array.from({ length: n }, (_, i) => ({
  id: `HOST-${String(i + 1).padStart(16, '0')}`,
  name: `host-${i + 1}.example.invalid`,
  'os.type': i % 2 ? 'WINDOWS' : 'LINUX',
  'os.version': i % 2 ? 'Windows Server 2022' : 'Ubuntu 24.04 LTS',
  logical_cores: 4,
  memory: 16 * 1024 ** 3,
  ip: [`10.0.0.${i + 1}`],
  'host.type': 't3.large',
  'cloud.provider': 'aws',
  'tags:aws': { team: i < 3 ? 'team-a' : 'team-b', env: 'test' },
}))

/** Records for one query, by what the query is asking for. */
export function recordsFor(q: string, HOSTS: Rec[] = hosts(6)): Rec[] {
  // filter popup: attribute discovery sample and value counts
  // the popup's source head quotes the type; the Hosts page's own query doesn't
  if (!/^smartscapeNodes "?HOST"?\n/.test(q)) return []
  if (q.includes('| fieldsRemove k8s.object')) return HOSTS
  if (q.includes('| fieldsSummary host.type')) return [{ count: HOSTS.length, values: [{ value: 't3.large', count: HOSTS.length }] }]
  if (q.includes('| fieldsSummary')) {
    return [
      {
        count: HOSTS.length,
        values: [
          { value: 'team-a', count: 3 },
          { value: 'team-b', count: 3 },
        ],
      },
    ]
  }
  // Hosts list (not its timeseries)
  if (q.includes('| fields id, name, os.type')) return HOSTS
  return []
}

interface Notice {
  type?: string
  severity?: string
  message: string
}

function result(spec: Spec, HOSTS: Rec[], notices?: Notice[]) {
  const records = recordsFor(spec.query, HOSTS)
  // notices ride on the Hosts list query only
  const list = spec.query.includes('| fields id, name, os.type')
  return {
    id: spec.id,
    ok: true,
    records,
    types: {},
    meta: { executionMs: 1, scannedRecords: 0, scannedBytes: 0, from: spec.from, to: spec.to, ...(list && notices ? { notices } : {}) },
    elapsedMs: 1,
  }
}

const json = (route: Route, body: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })

/**
 * Install the mock; returns the queries the page sent, for assertions. `hosts`
 * sizes the Hosts list (1000 caps it); `notices` are Grail notifications on it.
 */
export async function mockApi(page: Page, opts: { hosts?: number; notices?: Notice[] } = {}) {
  const HOSTS = hosts(opts.hosts ?? 6)
  const queries: string[] = []
  await page.route('**/api/**', async (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname
    if (req.headers()['x-dtctl-web'] !== '1') return json(route, { error: 'missing X-Dtctl-Web header' }, 403)
    if (path === '/api/meta') return json(route, META)
    if (path === '/api/activity') return json(route, ACTIVITY)
    if (path === '/api/activity/cancel' || path === '/api/context') return json(route, {})
    if (path === '/api/documents') return json(route, new URL(req.url()).searchParams.get('type') === 'dashboard' ? DOCS : [])
    if (path.startsWith('/api/dql/')) return json(route, path.endsWith('verify') ? { valid: true, notifications: [] } : { suggestions: [] })
    if (path === '/api/batch') {
      const specs = req.postDataJSON() as Spec[]
      for (const s of specs) queries.push(s.query)
      return route.fulfill({ status: 200, contentType: 'application/x-ndjson', body: specs.map((s) => JSON.stringify(result(s, HOSTS, opts.notices))).join('\n') + '\n' })
    }
    return json(route, { error: `unmocked ${path}` }, 404)
  })
  return queries
}
