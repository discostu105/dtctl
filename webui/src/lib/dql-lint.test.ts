import { describe, expect, it } from 'vitest'

// Query shapes that are valid DQL but defeat Grail's indexes. Checked over the
// source text, because the queries are template strings spread across src/.

const SOURCES = import.meta.glob<string>(['../**/*.{ts,tsx}', '!../**/*.test.{ts,tsx}'], { query: '?raw', import: 'default', eager: true })

/** file:line of every source line matching `re`. */
function grep(re: RegExp) {
  return Object.entries(SOURCES).flatMap(([path, text]) =>
    text.split('\n').flatMap((line, i) => (re.test(line) ? [`${path}:${i + 1}`] : [])),
  )
}

describe('DQL in source', () => {
  it('never compares a stringified field: use toSmartscapeId()/toUid() on the literal instead', () => {
    // toString(<field>) == "…" — a JS `.toString()` call is not DQL
    expect(grep(/(?<![.\w])toString\((?:[^()]|\([^()]*\))*\)\s*(==|!=)/)).toEqual([])
  })

  it('reads every source file', () => {
    expect(Object.keys(SOURCES)).toContain('./dql.ts')
  })
})
