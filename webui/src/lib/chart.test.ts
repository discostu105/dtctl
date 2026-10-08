import { describe, expect, it } from 'vitest'
import { additive, assignments, barsModel, byFields, dimensionFields, inQueryOrder, labelsOf, fieldUnits, timeseriesModel } from './chart'

const tf = { start: '2026-10-01T00:00:00Z', end: '2026-10-01T03:00:00Z' }
const interval = String(3600e9)

// The shape of a services query: one record per service, three value arrays,
// then scalars derived from them, a looked-up id and name, and a constant.
const SERVICES = [
  { timeframe: tf, interval, 'dt.smartscape.service': null, req: [1, 2, 3], fail: [0, 0, 0], rt: [null, null, null], total: '6', failed: '0', latency: 0.5, 's.id': null, 's.name': null, 's.type': 'WebRequest' },
  { timeframe: tf, interval, 'dt.smartscape.service': 'SERVICE-00000000000000A1', req: [10, 20, 30], fail: [1, 0, 2], rt: [1500, 2500, null], total: '60', failed: '3', latency: 2000, 's.id': 'SERVICE-00000000000000A1', 's.name': 'checkout', 's.type': 'WebRequest' },
  { timeframe: tf, interval, 'dt.smartscape.service': 'SERVICE-00000000000000B2', req: [100, 200, 300], fail: [5, 5, 5], rt: [800, 900, 1000], total: '600', failed: '15', latency: 900, 's.id': 'SERVICE-00000000000000B2', 's.name': 'cart', 's.type': 'WebRequest' },
]
const QUERY = `timeseries { r = sum(dt.service.request.count), s = sum(dt.service.request.service_mesh.count), rrt = avg(dt.service.request.response_time) }, union:true, by:{dt.smartscape.service}
| fieldsAdd req = coalesce(r[], s[], 0), rt = coalesce(rrt[]), fail = coalesce(rf[], 0)
| fieldsAdd total = arraySum(req), failure_rate = if(total > 0, 100.0 * failed / total, else: 0.0)`
const METRICS = [
  { field: 'r', unit: 'count' },
  { field: 's', unit: 'count' },
  { field: 'rrt', unit: 'us', name: '"Service request response time"' },
  { field: 'rf', unit: 'count' },
]

describe('timeseriesModel', () => {
  const m = timeseriesModel(SERVICES, { metrics: METRICS, query: QUERY })!

  it('plots the value arrays, one field at a time, on an hourly axis', () => {
    // in the order the query defines them, not the server's alphabetical keys
    expect(m.fields.map((f) => f.field)).toEqual(['req', 'rt', 'fail'])
    expect(m.x).toEqual([0, 1, 2].map((i) => Date.parse(tf.start) / 1000 + i * 3600))
  })

  it('names series by their name only: no scalars, ids, types or constants', () => {
    expect(m.dims).toEqual([{ field: 'dt.smartscape.service', alias: 's.name' }])
    expect(m.series('req').series.map((s) => s.label)).toEqual(['cart', 'checkout', '(none)'])
  })

  it('ranks by the plotted field and drops series without a value', () => {
    const rt = m.series('rt')
    expect(rt.series.map((s) => s.label)).toEqual(['checkout', 'cart'])
    expect(rt.stat).toBe('avg')
    expect(rt.series[0].stat).toBe(2000)
    expect(m.series('req').stat).toBe('total')
    expect(m.series('req').series[0].stat).toBe(600)
  })

  it('carries units into fields the query derived', () => {
    expect(Object.fromEntries(m.fields.map((f) => [f.field, f.unit]))).toEqual({ req: 'count', rt: 'µs', fail: 'count' })
  })

  it('is not a timeseries without a time axis or value arrays', () => {
    expect(timeseriesModel([{ a: 1 }])).toBeNull()
    expect(timeseriesModel([{ timeframe: tf, interval, tags: ['a', 'b'] }])).toBeNull()
    expect(timeseriesModel([])).toBeNull()
  })

  it('falls back to the field name when nothing tells records apart', () => {
    const one = timeseriesModel([{ timeframe: tf, interval, 'avg(cpu)': [1, 2, 3] }])!
    expect(one.series('avg(cpu)').series[0].label).toBe('avg(cpu)')
  })

  it('reads longs Grail sent as strings', () => {
    const big = timeseriesModel([{ timeframe: tf, interval, c: ['1', '2', null] }])!
    expect(big.series('c').series[0].values).toEqual([1, 2, null])
  })
})

describe('dimensionFields', () => {
  it('keeps the readable field of an id/name pair, whichever comes first', () => {
    const recs = [
      { 'host.name': 'a', 'dt.smartscape.host': 'HOST-0000000000000001' },
      { 'host.name': 'b', 'dt.smartscape.host': 'HOST-0000000000000002' },
    ]
    expect(dimensionFields(recs, [])).toEqual([{ field: 'host.name' }])
  })

  it('shows a looked-up name in place of the id it belongs to, the id where the lookup missed', () => {
    const recs = [
      { svc: 'SERVICE-00000000000000A1', 's.name': 'api', 's.type': 'Web' },
      { svc: 'SERVICE-00000000000000B2', 's.name': null, 's.type': 'Web' },
      { svc: 'SERVICE-00000000000000C3', 's.name': 'db', 's.type': 'Queue' },
    ]
    const dims = dimensionFields(recs, [], undefined, 'timeseries x = sum(m), by:{svc}')
    expect(dims).toEqual([{ field: 'svc', alias: 's.name' }])
    expect(labelsOf(recs, dims, '')).toEqual(['api', 'SERVICE-00000000000000B2', 'db'])
  })

  it('tells apart two series of the same name by their id', () => {
    const recs = [
      { svc: 'SERVICE-00000000000000A1', name: 'api' },
      { svc: 'SERVICE-00000000000000B2', name: 'api' },
      { svc: 'SERVICE-00000000000000C3', name: 'db' },
    ]
    const dims = dimensionFields(recs, [], undefined, 'by:{svc}')
    expect(labelsOf(recs, dims, '')).toEqual(['api (SERVICE-00000000000000A1)', 'api (SERVICE-00000000000000B2)', 'db'])
  })

  it('keeps two dimensions that each split the records differently', () => {
    const recs = [
      { ns: 'a', pod: 'x' },
      { ns: 'a', pod: 'y' },
      { ns: 'b', pod: 'x' },
    ]
    expect(dimensionFields(recs, [])).toEqual([{ field: 'ns' }, { field: 'pod' }])
  })

  it('never names a series by a number or a typed numeric field', () => {
    expect(dimensionFields([{ a: 'x', n: 1 }, { a: 'y', n: 2 }], [])).toEqual([{ field: 'a' }])
    expect(dimensionFields([{ a: 'x', n: '1' }, { a: 'y', n: '2' }], [], { n: 'long' })).toEqual([{ field: 'a' }])
    expect(dimensionFields([{ n: '1' }, { n: '2' }], [])).toEqual([])
  })
})

describe('fieldUnits', () => {
  it('leaves a ratio of two metrics without a unit', () => {
    expect(fieldUnits(['x'], [{ field: 'a', unit: 'us' }, { field: 'b', unit: 'us' }], 'timeseries {a=avg(m1), b=avg(m2)}\n| fieldsAdd x = a[] / b[]')).toEqual({})
  })

  it('leaves a mix of units without a unit', () => {
    expect(fieldUnits(['x'], [{ field: 'a', unit: 'us' }, { field: 'b', unit: 'By' }], '| fieldsAdd x = coalesce(a[], b[])')).toEqual({})
  })

  it('follows a chain of derivations', () => {
    expect(fieldUnits(['y'], [{ field: 'a', unit: 'ms' }], '| fieldsAdd x = a[]\n| fieldsAdd y = coalesce(x[], 0)')).toEqual({ y: 'ms' })
  })
})

describe('assignments', () => {
  it('splits at top-level commas and pipes only', () => {
    expect(assignments('fetch x | fieldsAdd a = coalesce(b, c), d = "x, | y", e == f | fields g = if(h, 1, else: 2)')).toEqual([
      { name: 'a', expr: 'coalesce(b, c)' },
      { name: 'd', expr: '"x, | y"' },
      { name: 'g', expr: 'if(h, 1, else: 2)' },
    ])
  })
})

describe('additive', () => {
  it('sums counts and whole numbers, averages the rest', () => {
    expect(additive('count', [1.5])).toBe(true)
    expect(additive('', [1, 2, null])).toBe(true)
    expect(additive('', [1, 2.5])).toBe(false)
    expect(additive('µs', [1, 2])).toBe(false)
  })
})

describe('barsModel', () => {
  it('charts a summarize result by its dimension, largest first', () => {
    const b = barsModel([
      { 'host.name': 'a', cnt: '5', avg_cpu: 1.5 },
      { 'host.name': 'b', cnt: '50', avg_cpu: 0.5 },
    ])!
    expect(b.dims).toEqual([{ field: 'host.name' }])
    expect(b.fields.map((f) => f.field)).toEqual(['cnt', 'avg_cpu'])
    expect(b.bars('cnt').map((x) => x.label)).toEqual(['b', 'a'])
  })

  it('is not a bar chart without numbers', () => {
    expect(barsModel([{ a: 'x' }, { a: 'y' }])).toBeNull()
  })
})

describe('byFields', () => {
  it('names what a query groups by, aliases included', () => {
    expect(byFields('timeseries avg(x), by:{dt.smartscape.host, code = http.response.status_code}')).toEqual(['dt.smartscape.host', 'code'])
    expect(byFields('summarize count(), by:status')).toEqual(['status'])
    expect(byFields('fetch logs')).toEqual([])
  })

  it('keeps a numeric group-by field as a dimension', () => {
    const recs = [
      { timeframe: tf, interval, code: '200', c: [1, 2, 3] },
      { timeframe: tf, interval, code: '500', c: [0, 1, 0] },
    ]
    const m = timeseriesModel(recs, { types: { code: 'long' }, query: 'timeseries c = sum(m), by:{code}' })!
    expect(m.series('c').series.map((s) => s.label)).toEqual(['200', '500'])
  })
})

describe('inQueryOrder', () => {
  it('orders by definition, then by mention, unknown fields last', () => {
    expect(inQueryOrder(['b', 'a', 'z', 'c'], 'timeseries { c = sum(x) } | fieldsAdd a = c[], b = a[]')).toEqual(['c', 'a', 'b', 'z'])
    expect(inQueryOrder(['avg(x)', 'n'], 'timeseries n = count(), avg(x)')).toEqual(['n', 'avg(x)'])
  })
})
