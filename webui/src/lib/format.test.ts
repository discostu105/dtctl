import { describe, expect, it } from 'vitest'
import { ago, fmtBytes, fmtCompact, fmtInt, fmtMs, fmtPct, shortType, span, titleCase, toDate } from './format'

describe('numbers', () => {
  it('formats integers and placeholders', () => {
    expect(fmtInt(1284)).toBe('1,284')
    expect(fmtInt(NaN)).toBe('—')
  })

  it('compacts large numbers', () => {
    expect(fmtCompact(1284)).toBe('1,284')
    expect(fmtCompact(12_900)).toBe('12.9K')
    expect(fmtCompact(4_200_000)).toBe('4.2M')
    expect(fmtCompact(3.14159)).toBe('3.14')
  })

  it('promotes to the next unit when rounding reaches 1000', () => {
    expect(fmtCompact(999_960)).toBe('1M')
    expect(fmtCompact(999_940)).toBe('999.9K')
    expect(fmtCompact(999_960_000)).toBe('1B')
  })

  it('formats percentages', () => {
    expect(fmtPct(12.34)).toBe('12.3%')
    expect(fmtPct(5)).toBe('5%')
    expect(fmtPct(0.05)).toBe('<0.1%')
    expect(fmtPct(0)).toBe('0%')
    expect(fmtPct(0.4, 0)).toBe('<1%')
    expect(fmtPct(0.6, 0)).toBe('1%')
  })

  it('formats bytes in binary units', () => {
    expect(fmtBytes(512)).toBe('512 B')
    expect(fmtBytes(1536)).toBe('1.5 KiB')
    expect(fmtBytes(5 * 1024 ** 3)).toBe('5 GiB')
  })
})

describe('durations', () => {
  it('picks a unit by magnitude', () => {
    expect(fmtMs(0)).toBe('0')
    expect(fmtMs(0.25)).toBe('250 µs')
    expect(fmtMs(4.56)).toBe('4.6 ms')
    expect(fmtMs(250)).toBe('250 ms')
    expect(fmtMs(1500)).toBe('1.5 s')
    expect(fmtMs(125_000)).toBe('2m 5s')
    expect(fmtMs(3 * 3_600_000 + 4 * 60_000)).toBe('3h 4m')
    expect(fmtMs(2 * 86_400_000 + 5 * 3_600_000)).toBe('2d 5h')
  })

  it('never shows 60 in the minor unit', () => {
    expect(fmtMs(119_700)).toBe('2m 0s')
    expect(fmtMs(59_999)).toBe('1m 0s')
    expect(fmtMs(2 * 3_600_000 - 20_000)).toBe('2h 0m')
    expect(fmtMs(86_400_000 - 20_000)).toBe('1d 0h')
  })

  it('measures spans and drops a zero minor unit', () => {
    expect(span(0, 3 * 3_600_000)).toBe('3h')
    expect(span(null)).toBe('—')
  })

  it('says how long ago', () => {
    const now = Date.UTC(2026, 0, 10, 12, 0, 0)
    expect(ago(now - 30_000, now)).toBe('30s ago')
    expect(ago(now - 5 * 60_000, now)).toBe('5m ago')
    expect(ago(now - 3 * 3_600_000, now)).toBe('3h ago')
    expect(ago(now - 4 * 86_400_000, now)).toBe('4d ago')
    expect(ago(now + 10_000, now)).toBe('just now')
    expect(ago('not a date', now)).toBe('—')
  })

  it('parses dates leniently', () => {
    expect(toDate('')).toBeNull()
    expect(toDate('2026-01-10T12:00:00Z')?.getTime()).toBe(Date.UTC(2026, 0, 10, 12))
    expect(toDate(0)?.getTime()).toBe(0)
  })
})

describe('words', () => {
  it('names entity types the way people write them', () => {
    expect(shortType('K8S_POD')).toBe('Pod')
    expect(shortType('K8S_STATEFULSET')).toBe('StatefulSet')
    expect(shortType('AWS_EC2_INSTANCE')).toBe('AWS EC2 instance')
    expect(shortType('HOST')).toBe('Host')
  })

  it('title-cases enum values', () => {
    expect(titleCase('LINUX')).toBe('Linux')
    expect(titleCase('NOT_READY')).toBe('Not Ready')
  })
})
