import { describe, expect, it } from 'vitest'
import { trailingLimit } from './api'

describe('trailingLimit', () => {
  it('reads the final limit as the record budget', () => {
    expect(trailingLimit('smartscapeNodes HOST\n| sort name asc\n| limit 3000')).toBe(3000)
    expect(trailingLimit('fetch logs | limit 50  \n')).toBe(50)
  })

  it('ignores limits that are not the last command', () => {
    expect(trailingLimit('fetch logs\n| limit 3000\n| sort timestamp desc')).toBeUndefined()
    expect(trailingLimit('fetch logs\n| lookup [fetch spans | limit 5], sourceField:a, lookupField:b')).toBeUndefined()
    expect(trailingLimit('timeseries avg(dt.host.cpu.usage)')).toBeUndefined()
  })
})
