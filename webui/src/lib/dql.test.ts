import { describe, expect, it } from 'vitest'
import { evidenceQuery, problemDetailQuery, problemSince } from './dql'

describe('problemSince', () => {
  const now = Date.UTC(2026, 9, 8, 21, 0)

  it('reads from the day before the day in the display id', () => {
    expect(problemSince('P-2610087056', now)).toBe(Date.UTC(2026, 9, 7))
  })

  it('beyond 30 days, leaves the window to now()-30d', () => {
    expect(problemSince('P-2608254279', now)).toBeNull()
  })

  it('gives up on ids without a date', () => {
    expect(problemSince('P-123', now)).toBeNull()
    expect(problemSince('not-a-problem', now)).toBeNull()
    expect(problemSince('P-9912319999', now)).toBeNull() // 2099: in the future
  })

  it('puts the instant into the query, and falls back to 30 days', () => {
    expect(problemDetailQuery('P-2610087056')).toMatch(/^fetch dt\.davis\.problems, from:toTimestamp\("20\d\d-/)
    expect(problemDetailQuery('P-1')).toMatch(/^fetch dt\.davis\.problems, from:now\(\)-30d\n/)
  })
})

describe('evidenceQuery', () => {
  it('reads the same text on every render, however old the problem', () => {
    // a moving lower bound would change the cache key and refetch forever
    expect(evidenceQuery(['e1'], '2020-01-01T00:00:00Z')).toBe(evidenceQuery(['e1'], '2020-01-01T00:00:00Z'))
    expect(evidenceQuery(['e1'], '2020-01-01T00:00:00Z')).toContain('from:now()-30d')
    expect(evidenceQuery(['e1'])).toContain('from:now()-30d')
  })
})
