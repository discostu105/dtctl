import { useEffect, useSyncExternalStore } from 'react'

// Tiny external stores (no state library needed).

function createStore<T>(initial: T) {
  let v = initial
  const ls = new Set<() => void>()
  return {
    get: () => v,
    set: (n: T) => {
      v = n
      ls.forEach((l) => l())
    },
    subscribe: (cb: () => void) => {
      ls.add(cb)
      return () => ls.delete(cb)
    },
  }
}

export function useStore<T>(s: { get: () => T; subscribe: (cb: () => void) => () => void }) {
  return useSyncExternalStore(s.subscribe, s.get)
}

// ── page title (top bar + document.title) ───────────────────────────────────
export const titleStore = createStore<string>('')
export function useTitle(title: string | undefined) {
  useEffect(() => {
    if (!title) return
    titleStore.set(title)
    document.title = `${title} · dtctl web`
  }, [title])
}

// ── palette / overlays ──────────────────────────────────────────────────────
export const paletteStore = createStore<{ open: boolean; initial?: string }>({ open: false })
export const helpStore = createStore(false)
export const tfPickerStore = createStore(false)

// ── filter popup ('f') ──────────────────────────────────────────────────────
// Every filterable list registers as a host; 'f' opens the most recently
// mounted one (the list the user is looking at), a chip opens its own host
// straight at its field.
export const filterPopupStore = createStore<{ host: string | null; field?: string }>({ host: null })
const filterHosts: string[] = []
export function registerFilterHost(id: string) {
  filterHosts.push(id)
  return () => {
    const i = filterHosts.lastIndexOf(id)
    if (i >= 0) filterHosts.splice(i, 1)
    if (filterPopupStore.get().host === id) filterPopupStore.set({ host: null })
  }
}
/** Open the filter popup of the current list; false when the page has none. */
export function openFilterPopup(field?: string, host?: string) {
  const h = host ?? filterHosts[filterHosts.length - 1]
  if (!h) return false
  filterPopupStore.set({ host: h, field })
  return true
}
export const closeFilterPopup = () => filterPopupStore.set({ host: null })

// ── recents (per browser; survives reloads) ─────────────────────────────────
export interface Recent {
  href: string
  label: string
  kind: string
  at: number
}
const RECENT_KEY = 'dtctl-web:recent'
function loadRecents(): Recent[] {
  try {
    return JSON.parse(localStorage.getItem(RECENT_KEY) || '[]')
  } catch {
    return []
  }
}
export const recentStore = createStore<Recent[]>(loadRecents())
export function pushRecent(r: Omit<Recent, 'at'>) {
  const next = [{ ...r, at: Date.now() }, ...recentStore.get().filter((x) => x.href !== r.href)].slice(0, 12)
  recentStore.set(next)
  try {
    localStorage.setItem(RECENT_KEY, JSON.stringify(next))
  } catch {
    /* ignore */
  }
}

// ── auto refresh ────────────────────────────────────────────────────────────
export const autoRefreshStore = createStore<number>(0) // seconds; 0 = off

// ── theme ───────────────────────────────────────────────────────────────────
export function toggleTheme() {
  const root = document.documentElement
  const cur = root.dataset.theme ?? (matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark')
  const next = cur === 'light' ? 'dark' : 'light'
  root.dataset.theme = next
  try {
    localStorage.setItem('dtctl-web:theme', next)
  } catch {
    /* ignore */
  }
}
