import { describe, expect, it } from 'vitest'
import { classify, noticesOf, warningOf } from './notices'

const SCAN = 'Your execution was stopped after 500 gigabytes of data were scanned. Use the "scanLimitGBytes" parameter in the fetch command to adjust the limit.'

describe('classify', () => {
  it('reads the type first', () => {
    expect(classify({ type: 'SCAN_LIMIT_GBYTES', message: '' })).toBe('scan')
    expect(classify({ type: 'API_RECORDS_LIMIT_ADDED', message: '' })).toBe('rows')
    expect(classify({ type: 'RESULT_LIMIT_BYTES', message: '' })).toBe('rows')
    expect(classify({ type: 'FETCH_TIMEOUT', message: '' })).toBe('timeout')
    expect(classify({ type: 'SAMPLING_APPLIED', message: '' })).toBe('sampled')
  })

  it('falls back to the wording', () => {
    expect(classify({ message: SCAN })).toBe('scan')
    expect(classify({ message: 'Your result has been limited to 1000.' })).toBe('rows')
    expect(classify({ message: 'The fetch timed out.' })).toBe('timeout')
    expect(classify({ message: 'Something else entirely.' })).toBeNull()
  })
})

describe('noticesOf', () => {
  it('says what happened and what helps, with the numbers', () => {
    const [n] = noticesOf({ executionMs: 0, scannedRecords: 0, scannedBytes: 0, notices: [{ type: 'SCAN_LIMIT_GBYTES', message: SCAN }] })
    expect(n.text).toBe('Partial result: Grail stopped after scanning 500 GB. Narrow the timeframe or add a filter.')
    expect(n.raw).toBe(SCAN)
    const [r] = noticesOf({ executionMs: 0, scannedRecords: 0, scannedBytes: 0, notices: [{ type: 'API_RECORDS_LIMIT_ADDED', message: 'Your result has been limited to 1000.' }] })
    expect(r.text).toBe('Only the first 1,000 rows came back; more matched. Add a filter to see the rest.')
  })

  it('reads plain notifications from an older server', () => {
    expect(noticesOf({ executionMs: 0, scannedRecords: 0, scannedBytes: 0, notifications: [SCAN] }).map((n) => n.kind)).toEqual(['scan'])
  })

  it('orders by importance and drops repeats', () => {
    const meta = {
      executionMs: 0,
      scannedRecords: 0,
      scannedBytes: 0,
      notices: [
        { type: 'SAMPLING_APPLIED', message: 'sampled' },
        { type: 'API_RECORDS_LIMIT_ADDED', message: 'Your result has been limited to 5.' },
        { type: 'SCAN_LIMIT_GBYTES', message: SCAN },
        { type: 'SCAN_LIMIT_GBYTES', message: SCAN },
      ],
    }
    expect(noticesOf(meta).map((n) => n.kind)).toEqual(['scan', 'rows', 'sampled'])
    expect(warningOf(meta)?.kind).toBe('scan')
  })

  it('does not warn about sampling alone', () => {
    expect(warningOf({ executionMs: 0, scannedRecords: 0, scannedBytes: 0, notices: [{ type: 'SAMPLING_APPLIED', message: 'x' }] })).toBeNull()
    expect(warningOf(undefined)).toBeNull()
  })
})
