import { describe, expect, it } from 'vitest'
import { NONE, applyFacets, bucket, countFacet, parseFilters, serializeFilter, textMatcher, valuesOf, type Facet } from './facets'

interface Row {
  name: string
  ns?: string | null
  tags?: string[]
  cpu?: number
}

const rows: Row[] = [
  { name: 'api-1', ns: 'prod', tags: ['a', 'b'], cpu: 95 },
  { name: 'api-2', ns: 'prod', tags: ['b'], cpu: 40 },
  { name: 'web-1', ns: 'dev', tags: [], cpu: 80 },
  { name: 'job-1', ns: null, cpu: NaN },
]

const facets: Facet<Row>[] = [
  { key: 'ns', label: 'Namespace', value: (r) => r.ns },
  { key: 'tag', label: 'Tag', value: (r) => r.tags },
  { key: 'cpu', label: 'CPU', value: (r) => bucket(r.cpu, [[90, 'hot'], [75, 'warm']], 'ok'), order: ['hot', 'warm', 'ok'] },
]

const names = (rs: Row[]) => rs.map((r) => r.name)

describe('valuesOf', () => {
  it('maps missing, empty and NaN values to NONE', () => {
    expect(valuesOf(facets[0], rows[3])).toEqual([NONE])
    expect(valuesOf(facets[1], rows[2])).toEqual([NONE])
    expect(valuesOf({ key: 'x', label: 'x', value: () => NaN }, rows[0])).toEqual([NONE])
  })
  it('stringifies scalars and spreads arrays', () => {
    expect(valuesOf(facets[1], rows[0])).toEqual(['a', 'b'])
    expect(valuesOf({ key: 'x', label: 'x', value: () => 3 }, rows[0])).toEqual(['3'])
  })
})

describe('parseFilters / serializeFilter', () => {
  it('splits at the first colon so values may contain colons', () => {
    const p = new URLSearchParams([
      ['f', 'ns:prod'],
      ['f', '-image:repo:tag'],
      ['f', ':bad'],
      ['f', 'nocolon'],
    ])
    const got = parseFilters(p)
    expect(got).toEqual([
      { key: 'ns', value: 'prod', neg: false },
      { key: 'image', value: 'repo:tag', neg: true },
    ])
    expect(got.map(serializeFilter)).toEqual(['ns:prod', '-image:repo:tag'])
  })
})

describe('textMatcher', () => {
  it('needs every term and none of the excluded ones, case-insensitively', () => {
    expect(textMatcher('  ')).toBeNull()
    const m = textMatcher('API -2')!
    expect(names(rows.filter((r) => m(r.name)))).toEqual(['api-1'])
  })
})

describe('applyFacets', () => {
  it('ORs values within a facet and ANDs facets', () => {
    expect(names(applyFacets(rows, facets, [{ key: 'ns', value: 'prod' }, { key: 'ns', value: 'dev' }], null))).toEqual(['api-1', 'api-2', 'web-1'])
    expect(names(applyFacets(rows, facets, [{ key: 'ns', value: 'prod' }, { key: 'cpu', value: 'hot' }], null))).toEqual(['api-1'])
  })

  it('excludes any row carrying an excluded value', () => {
    expect(names(applyFacets(rows, facets, [{ key: 'tag', value: 'a', neg: true }], null))).toEqual(['api-2', 'web-1', 'job-1'])
  })

  it('filters on NONE and ignores unknown facet keys', () => {
    expect(names(applyFacets(rows, facets, [{ key: 'ns', value: NONE }], null))).toEqual(['job-1'])
    expect(names(applyFacets(rows, facets, [{ key: 'nope', value: 'x' }], null))).toEqual(names(rows))
  })

  it('applies the text matcher too', () => {
    const m = textMatcher('web')
    expect(names(applyFacets(rows, facets, [], m ? (r) => m(r.name) : null))).toEqual(['web-1'])
  })
})

describe('countFacet', () => {
  it('ignores the facet’s own selection so more values can be added', () => {
    expect(countFacet(rows, facets, [{ key: 'ns', value: 'prod' }], null, 'ns')).toEqual([
      { value: 'prod', count: 2 },
      { value: 'dev', count: 1 },
      { value: NONE, count: 1 },
    ])
  })

  it('narrows by the other facets and keeps a selected value at zero', () => {
    expect(countFacet(rows, facets, [{ key: 'cpu', value: 'hot' }, { key: 'ns', value: 'dev' }], null, 'ns')).toEqual([
      { value: 'prod', count: 1 },
      { value: 'dev', count: 0 },
    ])
  })

  it('follows a fixed order, NONE after it', () => {
    expect(countFacet(rows, facets, [], null, 'cpu').map((c) => c.value)).toEqual(['hot', 'warm', 'ok', NONE])
  })

  it('counts a multi-valued row once per distinct value', () => {
    expect(countFacet([{ name: 'x', tags: ['a', 'a'] }], facets, [], null, 'tag')).toEqual([{ value: 'a', count: 1 }])
  })
})

describe('bucket', () => {
  it('picks the first step the value reaches', () => {
    const steps: [number, string][] = [[90, 'hot'], [75, 'warm']]
    expect(bucket(90, steps, 'ok')).toBe('hot')
    expect(bucket(89.9, steps, 'ok')).toBe('warm')
    expect(bucket(0, steps, 'ok')).toBe('ok')
    expect(bucket(null, steps, 'ok')).toBeNull()
    expect(bucket(Infinity, steps, 'ok')).toBeNull()
  })
})
