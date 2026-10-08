import { ArrowLeft } from 'lucide-react'
import { useEffect, useSyncExternalStore } from 'react'
import { useLocation } from 'wouter'
import { titleStore, useStore } from '../lib/store'

// In-app navigation trail, so "back" can say where it goes: the page you came
// from (by its title) when there is one, else the section's list.

interface Visit {
  path: string
  title: string
}
let trail: Visit[] = []
const listeners = new Set<() => void>()
const emit = () => listeners.forEach((l) => l())

function record(path: string, title: string) {
  const last = trail[trail.length - 1]
  if (last?.path === path) {
    if (title && last.title !== title) {
      trail = [...trail.slice(0, -1), { path, title }]
      emit()
    }
    return
  }
  // going back to the previous entry pops, anything else pushes
  if (trail[trail.length - 2]?.path === path) trail = trail.slice(0, -1)
  else trail = [...trail.slice(-30), { path, title }]
  emit()
}

/** Mount once (Shell): records every page with the title it sets. */
export function useNavTrail() {
  const [loc] = useLocation()
  const title = useStore(titleStore)
  useEffect(() => record(loc, title), [loc, title])
}

/**
 * The one back link for detail pages: "← <previous page>" (history back)
 * when the user navigated here inside the app, otherwise "← <section>".
 */
export function BackLink({ fallback, label }: { fallback: string; label: string }) {
  const [loc, navigate] = useLocation()
  const t = useSyncExternalStore(
    (cb) => (listeners.add(cb), () => listeners.delete(cb)),
    () => trail,
  )
  const here = t[t.length - 1]?.path === loc ? t.length - 1 : -1
  const prev = here > 0 ? t[here - 1] : null
  return (
    <button
      type="button"
      onClick={() => (prev ? history.back() : navigate(fallback))}
      className="mb-3 inline-flex max-w-full items-center gap-1 self-start text-xs text-ink-3 hover:text-ink-2"
    >
      <ArrowLeft className="size-3.5 shrink-0" />
      <span className="truncate">{prev?.title || label}</span>
    </button>
  )
}

/** Error / not-found states of a detail page keep the way back. */
export function DetailFallback({ fallback, label, children }: { fallback: string; label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col p-5">
      <BackLink fallback={fallback} label={label} />
      {children}
    </div>
  )
}
