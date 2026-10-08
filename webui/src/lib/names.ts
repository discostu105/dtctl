// Global entity-name resolver: every Smartscape ID rendered anywhere in the
// UI is resolved to its name through ONE coalesced lookup per tick (the
// batcher then folds that into the same HTTP request as the page's queries).
// IDs Smartscape doesn't know (classic dt.entity.* IDs such as
// PROCESS_GROUP-…, KUBERNETES_CLUSTER-…) fall back to their classic table.
import { useEffect, useSyncExternalStore } from 'react'
import { runDql } from './api'
import { typeOfId } from './dql'

export interface Resolved {
  name: string
  type: string
  /** smartscape: has an entity page; classic: legacy dt.entity.* only */
  source: 'smartscape' | 'classic'
}

const cache = new Map<string, Resolved | null>() // null = looked up, unknown
const pending = new Set<string>()
const listeners = new Set<() => void>()
let version = 0
let scheduled = false

export const ENTITY_ID = /^[A-Z][A-Z0-9_]*-[0-9A-F]{16}$/

function notify() {
  version++
  listeners.forEach((l) => l())
}

/** Smartscape name, else what a person would call it: the AWS Name tag or the ARN's last part. */
export function displayName(r: { id?: string; name?: string; [k: string]: any }): string {
  if (r.name) return r.name
  const tag = r['tags:aws']?.Name
  if (tag) return String(tag)
  const arn: string | undefined = r['aws.arn']
  if (arn) return arn.split(/[:/]/).filter(Boolean).pop() || arn
  return r.id ?? ''
}

/** Seed names we already know (list rows, query results) to skip lookups. */
export function seedNames(entries: { id: string; name?: string | null; type?: string }[]) {
  let changed = false
  for (const e of entries) {
    if (!e.id || !e.name || cache.get(e.id)) continue
    cache.set(e.id, { name: e.name, type: e.type || typeOfId(e.id), source: 'smartscape' })
    changed = true
  }
  if (changed) notify()
}

export function requestNames(ids: string[]) {
  let added = false
  for (const id of ids) {
    if (!id || cache.has(id) || pending.has(id) || !ENTITY_ID.test(id)) continue
    pending.add(id)
    added = true
  }
  if (added && !scheduled) {
    scheduled = true
    setTimeout(flush, 0)
  }
}

const lit = (s: string) => JSON.stringify(s)

async function flush() {
  scheduled = false
  const ids = [...pending]
  pending.clear()
  for (let i = 0; i < ids.length; i += 150) await resolveChunk(ids.slice(i, i + 150))
}

async function resolveChunk(ids: string[]) {
  const found = new Set<string>()
  try {
    const res = await runDql({
      query: `smartscapeNodes "*"\n| filter in(id, {${ids.map((i) => `toSmartscapeId(${lit(i)})`).join(', ')}})\n| fields id, name, type, \`tags:aws\`, aws.arn\n| limit ${ids.length + 10}`,
      from: 'now-30d',
      ttl: 600,
    })
    for (const r of res.records) {
      if (!r.id) continue
      found.add(r.id)
      cache.set(r.id, { name: displayName(r), type: r.type, source: 'smartscape' })
    }
  } catch {
    /* fall through to classic */
  }

  // Classic fallback, one query per legacy table (bad table names just fail).
  const byType = new Map<string, string[]>()
  for (const id of ids) {
    if (found.has(id)) continue
    const t = typeOfId(id)
    if (!byType.has(t)) byType.set(t, [])
    byType.get(t)!.push(id)
  }
  await Promise.all(
    [...byType].map(async ([type, tids]) => {
      try {
        const res = await runDql({
          query: `fetch dt.entity.${type.toLowerCase()}, from:now()-30d\n| filter in(id, {${tids.map(lit).join(', ')}})\n| fields id, name = entity.name\n| limit ${tids.length + 10}`,
          ttl: 600,
        })
        for (const r of res.records) if (r.id && r.name) cache.set(r.id, { name: r.name, type, source: 'classic' })
      } catch {
        /* unknown table */
      }
      for (const id of tids) if (!cache.has(id)) cache.set(id, null)
    }),
  )
  notify()
}

function useVersion() {
  return useSyncExternalStore(
    (cb) => {
      listeners.add(cb)
      return () => listeners.delete(cb)
    },
    () => version,
  )
}

export function useResolved(id: string | null | undefined): Resolved | null | undefined {
  useVersion()
  useEffect(() => {
    if (id) requestNames([id])
  }, [id])
  return id ? cache.get(id) : undefined
}

/** Names for many IDs at once (Map of the ones resolved so far). */
export function useNames(ids: string[]): Map<string, string> {
  useVersion()
  const key = [...new Set(ids.filter(Boolean))].sort().join(',')
  useEffect(() => {
    if (key) requestNames(key.split(','))
  }, [key])
  const m = new Map<string, string>()
  for (const id of ids) {
    const r = cache.get(id)
    if (r) m.set(id, r.name)
  }
  return m
}

export function resolvedName(id: string) {
  return cache.get(id)?.name
}
