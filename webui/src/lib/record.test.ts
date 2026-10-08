import { describe, expect, it } from 'vitest'
import { inspectorRows, legacyTwins } from './record'

const REC = {
  'dt.smartscape.host': 'HOST-0000000000000001',
  'dt.entity.host': 'HOST-0000000000000001',
  'dt.entity.process_group': 'PROCESS_GROUP-0000000000000002',
  'dt.entity.service': 'SERVICE-0000000000000003',
  'dt.smartscape.service': 'SERVICE-0000000000000004',
  content: 'hello',
  'http.route': null,
  tags: [],
  note: '',
}

describe('legacyTwins', () => {
  it('pairs only identical ids', () => {
    expect([...legacyTwins(Object.entries(REC))]).toEqual(['dt.entity.host'])
  })
})

describe('inspectorRows', () => {
  it('holds back empty values and legacy twins, and counts them', () => {
    const r = inspectorRows(REC)
    expect(r.rows.map(([k]) => k)).toEqual(['content', 'dt.entity.process_group', 'dt.entity.service', 'dt.smartscape.host', 'dt.smartscape.service'])
    expect(r.empty).toBe(3)
    expect(r.twins).toBe(1)
  })

  it('shows everything on request', () => {
    expect(inspectorRows(REC, { all: true }).rows).toHaveLength(Object.keys(REC).length)
  })

  it('filters on key and value, and respects hide', () => {
    expect(inspectorRows(REC, { filter: 'HELLO' }).rows.map(([k]) => k)).toEqual(['content'])
    expect(inspectorRows(REC, { hide: ['content'] }).rows.map(([k]) => k)).not.toContain('content')
  })
})
