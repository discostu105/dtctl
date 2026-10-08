import * as Popover from '@radix-ui/react-popover'
import clsx from 'clsx'
import { Ban, Check, ChevronRight, ListFilter, Loader2, Minus, Plus, X } from 'lucide-react'
import { useCallback, useEffect, useId, useMemo, useRef, useState, type ReactNode } from 'react'
import { useLocation, useSearch } from 'wouter'
import {
  NONE,
  applyFacets,
  countFacet,
  parseFilters,
  sameFilter,
  serializeFilter,
  textMatcher,
  valuesOf,
  type Facet,
  type FacetCount,
  type FacetFilter,
} from '../lib/facets'
import { describeField, parseAttrs, sameAttr, serializeAttr, UNSET, type AttrFilter, type AttrSource } from '../lib/attrs'
import { fmtInt } from '../lib/format'
import { closeFilterPopup, filterPopupStore, openFilterPopup, registerFilterHost, useStore } from '../lib/store'
import { FilterPopup } from './FilterPopup'
import { Kbd, Tip } from './ui'

// ── controller ──────────────────────────────────────────────────────────────

export interface FacetCtl<T = any> {
  facets: Facet<T>[]
  filters: FacetFilter[]
  text: string
  setText: (s: string) => void
  toggle: (key: string, value: string, neg?: boolean) => void
  only: (key: string, value: string, neg?: boolean) => void
  /** Replace a facet's selection with these values. */
  setKey: (key: string, values: string[], neg?: boolean) => void
  remove: (f: FacetFilter | FacetFilter[]) => void
  clearKey: (key: string) => void
  clear: () => void
  counts: (key: string) => FacetCount[]
  facet: (key: string) => Facet<T> | undefined
  display: (key: string, value: string) => ReactNode
  /** Filtered rows (undefined while loading). */
  rows: T[] | undefined
  total: number
  active: boolean
  /** Server-side attribute and tag filters, when the list supports them. */
  attrs?: AttrCtl
}

// ── server-side attribute filters ───────────────────────────────────────────

export interface AttrCtl {
  source: AttrSource
  filters: AttrFilter[]
  toggle: (field: string, value: string, neg?: boolean) => void
  only: (field: string, value: string, neg?: boolean) => void
  remove: (f: AttrFilter | AttrFilter[]) => void
  clearField: (field: string) => void
  clear: () => void
  /** URL param name (so callers can carry filters to another page). */
  param: string
}

/**
 * Attribute filters that narrow the list's DQL on the server (tags, labels,
 * primary tags, any raw attribute). State lives in the URL (?a=field=value),
 * so the caller builds its query from `filters` and the view stays shareable.
 */
export function useAttrs(source: AttrSource, opts: { param?: string } = {}): AttrCtl {
  const name = opts.param ?? 'a'
  const search = useSearch()
  const [, navigate] = useLocation()
  const filters = useMemo(() => parseAttrs(new URLSearchParams(search), name), [search, name])
  const write = useCallback(
    (next: AttrFilter[]) => {
      const p = new URLSearchParams(window.location.search)
      p.delete(name)
      next.forEach((f) => p.append(name, serializeAttr(f)))
      const qs = p.toString()
      navigate(`${window.location.pathname}${qs ? `?${qs}` : ''}`, { replace: true })
    },
    [name, navigate],
  )
  return {
    source,
    filters,
    param: name,
    toggle: (field, value, neg) => {
      const f = { field, value, neg: !!neg }
      const has = filters.some((x) => sameAttr(x, f))
      const rest = filters.filter((x) => !(x.field === field && x.value === value))
      write(has ? rest : [...rest, f])
    },
    only: (field, value, neg) => write([...filters.filter((x) => x.field !== field), { field, value, neg: !!neg }]),
    remove: (f) => {
      const list = Array.isArray(f) ? f : [f]
      write(filters.filter((x) => !list.some((y) => sameAttr(x, y))))
    },
    clearField: (field) => write(filters.filter((x) => x.field !== field)),
    clear: () => write([]),
  }
}

/**
 * Faceted filtering whose state lives in the URL (?f=ns:prod&f=-phase:Running&q=text),
 * so a filtered view is shareable and survives reloads and back/forward.
 */
export function useFacets<T>(
  rows: T[] | undefined,
  facets: Facet<T>[],
  opts: { text?: (r: T) => string; param?: string; attrs?: AttrCtl } = {},
): FacetCtl<T> {
  const fName = opts.param ?? 'f'
  const qName = opts.param ? `${opts.param}q` : 'q'
  const search = useSearch()
  const [, navigate] = useLocation()
  const params = useMemo(() => new URLSearchParams(search), [search])
  const known = useMemo(() => new Set(facets.map((f) => f.key)), [facets])
  const filters = useMemo(() => parseFilters(params, fName).filter((f) => known.has(f.key)), [params, fName, known])
  const urlText = params.get(qName) ?? ''

  // The text is local so typing never fights the router; the URL follows.
  const [text, setTextState] = useState(urlText)
  const lastWritten = useRef(urlText)
  useEffect(() => {
    if (urlText !== lastWritten.current) {
      lastWritten.current = urlText
      setTextState(urlText)
    }
  }, [urlText])

  const write = useCallback(
    (next: FacetFilter[] | null, nextText: string | null) => {
      const p = new URLSearchParams(window.location.search)
      if (next) {
        p.delete(fName)
        next.forEach((f) => p.append(fName, serializeFilter(f)))
      }
      if (nextText != null) {
        lastWritten.current = nextText
        if (nextText) p.set(qName, nextText)
        else p.delete(qName)
      }
      const qs = p.toString()
      navigate(`${window.location.pathname}${qs ? `?${qs}` : ''}`, { replace: true })
    },
    [fName, qName, navigate],
  )

  const setText = useCallback(
    (s: string) => {
      setTextState(s)
      write(null, s)
    },
    [write],
  )

  // Haystack per row for free text: the caller's text plus every facet value.
  const hay = useMemo(() => {
    const m = new WeakMap<object, string>()
    return (r: T) => {
      let h = m.get(r as object)
      if (h == null) {
        h = `${opts.text ? opts.text(r) : JSON.stringify(r)} ${facets.map((f) => valuesOf(f, r).join(' ')).join(' ')}`.toLowerCase()
        m.set(r as object, h)
      }
      return h
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [facets, rows])

  // Tokens like "ns:" or "ns:pro" are the field's facet picker, not text to match.
  const matchText = useMemo(
    () =>
      text
        .split(/\s+/)
        .filter((t) => !facetToken(t, facets))
        .join(' '),
    [text, facets],
  )
  const match = useMemo(() => {
    const m = textMatcher(matchText)
    return m ? (r: T) => m(hay(r)) : null
  }, [matchText, hay])

  const filtered = useMemo(() => (rows ? applyFacets(rows, facets, filters, match) : undefined), [rows, facets, filters, match])

  const facetOf = useCallback((key: string) => facets.find((f) => f.key === key), [facets])

  return {
    facets,
    filters,
    text,
    setText,
    toggle: (key, value, neg) => {
      const f = { key, value, neg: !!neg }
      const has = filters.some((x) => sameFilter(x, f))
      // include and exclude of the same value are mutually exclusive
      const rest = filters.filter((x) => !(x.key === key && x.value === value))
      write(has ? rest : [...rest, f], null)
    },
    only: (key, value, neg) => write([...filters.filter((x) => x.key !== key), { key, value, neg: !!neg }], null),
    setKey: (key, values, neg) => write([...filters.filter((x) => x.key !== key), ...values.map((value) => ({ key, value, neg: !!neg }))], null),
    remove: (f) => {
      const list = Array.isArray(f) ? f : [f]
      write(
        filters.filter((x) => !list.some((y) => sameFilter(x, y))),
        null,
      )
    },
    clearKey: (key) => write(
      filters.filter((x) => x.key !== key),
      null,
    ),
    clear: () => {
      setTextState('')
      write([], '')
      opts.attrs?.clear()
    },
    counts: (key) => (rows ? countFacet(rows, facets, filters, match, key) : []),
    facet: facetOf,
    display: (key, value) => displayValue(facetOf(key), value),
    rows: filtered,
    total: rows?.length ?? 0,
    active: filters.length > 0 || !!matchText.trim() || !!opts.attrs?.filters.length,
    attrs: opts.attrs,
  }
}

function displayValue(f: Facet | undefined, v: string): ReactNode {
  if (v === NONE) return <span className="text-ink-3 italic">not set</span>
  return f?.display ? f.display(v) : v
}

function findFacet<T>(name: string, facets: Facet<T>[]) {
  const n = name.toLowerCase()
  return facets.find((f) => f.key.toLowerCase() === n || f.label.toLowerCase() === n || f.aliases?.includes(n))
}

/** "-ns:prod" → { facet, neg, partial } when the token names a facet. */
function facetToken<T>(tok: string, facets: Facet<T>[]) {
  const m = /^(-?)([\w.-]+):(.*)$/.exec(tok)
  if (!m) return null
  const facet = findFacet(m[2], facets)
  return facet ? { facet, neg: m[1] === '-', partial: m[3] } : null
}

// ── the filter field ────────────────────────────────────────────────────────

type Item<T> = { kind: 'facet'; facet: Facet<T> } | { kind: 'value'; facet: Facet<T>; value: string; count: number }

/**
 * One field for everything: free text filters as you type, and the same field
 * offers facet values (with counts) for what you typed. `ns:` lists a facet's
 * values, ↵ adds one as a chip, ⇧↵ excludes it, ⌫ on an empty field removes the
 * last chip. Focusing the empty field shows every facet with its top values.
 */
export function FacetSearch<T>({ fc, placeholder = 'Filter…', className }: { fc: FacetCtl<T>; placeholder?: string; className?: string }) {
  const [open, setOpen] = useState(false)
  const [sel, setSel] = useState(-1)
  const input = useRef<HTMLInputElement>(null)

  const tokens = fc.text.split(/(\s+)/)
  const last = tokens[tokens.length - 1] ?? ''
  const ft = facetToken(last, fc.facets)
  const term = !ft && last.replace(/^-/, '').toLowerCase()
  const negDefault = ft ? ft.neg : last.startsWith('-')

  const items: Item<T>[] = useMemo(() => {
    if (!open) return []
    if (ft) {
      const p = ft.partial.toLowerCase()
      return fc
        .counts(ft.facet.key)
        .filter((c) => !p || c.value.toLowerCase().includes(p))
        .map((c) => ({ kind: 'value' as const, facet: ft.facet, value: c.value, count: c.count }))
    }
    if (term && term.length >= 1) {
      const out: Item<T>[] = []
      for (const f of fc.facets) {
        if (f.label.toLowerCase().startsWith(term) || f.aliases?.some((a) => a.startsWith(term))) out.push({ kind: 'facet', facet: f })
      }
      for (const f of fc.facets) {
        for (const c of fc.counts(f.key)) {
          if (c.value !== NONE && c.value.toLowerCase().includes(term) && c.count > 0) out.push({ kind: 'value', facet: f, value: c.value, count: c.count })
        }
      }
      return out.slice(0, 12)
    }
    return fc.facets.map((f) => ({ kind: 'facet' as const, facet: f }))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, fc.text, fc.filters, fc.rows, fc.facets])

  // In facet mode the user asked for a value: preselect the first one.
  useEffect(() => setSel(ft ? 0 : -1), [ft?.facet.key, fc.text]) // eslint-disable-line react-hooks/exhaustive-deps

  const replaceLast = (s: string) => {
    tokens[tokens.length - 1] = s
    fc.setText(tokens.join(''))
  }

  const pick = (it: Item<T>, neg: boolean, keepOpen = false) => {
    if (it.kind === 'facet') {
      replaceLast(`${neg ? '-' : ''}${it.facet.key}:`)
      input.current?.focus()
      return
    }
    fc.toggle(it.facet.key, it.value, neg)
    // stay in the facet's list for multi-select; free-text suggestions consume the term
    replaceLast(ft && keepOpen ? `${ft.neg ? '-' : ''}${ft.facet.key}:` : '')
  }

  const onKey = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setOpen(true)
      setSel((s) => Math.min(items.length - 1, s + 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setSel((s) => Math.max(-1, s - 1))
    } else if (e.key === 'Enter') {
      const it = items[sel]
      if (it) {
        e.preventDefault()
        pick(it, e.shiftKey || e.altKey || negDefault, true)
      } else {
        setOpen(false)
      }
    } else if (e.key === 'Tab' && items[sel]?.kind === 'facet') {
      e.preventDefault()
      pick(items[sel], negDefault)
    } else if (e.key === 'Backspace' && !fc.text && fc.filters.length) {
      e.preventDefault()
      fc.remove(fc.filters[fc.filters.length - 1])
    } else if (e.key === 'Escape') {
      e.preventDefault()
      if (open && fc.text) setOpen(false)
      else if (fc.text) fc.setText('')
      else input.current?.blur()
    }
  }

  const onBlur = () => {
    setOpen(false)
    // a dangling "ns:" is a half-finished pick, not a search term
    if (ft && !ft.partial) replaceLast('')
  }

  const max = Math.max(1, ...items.map((i) => (i.kind === 'value' ? i.count : 0)))

  return (
    <div className={clsx('relative', className)}>
      <ListFilter className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-ink-4" />
      <input
        ref={input}
        data-filter
        value={fc.text}
        onChange={(e) => {
          fc.setText(e.target.value)
          setOpen(true)
        }}
        onFocus={() => setOpen(true)}
        onClick={() => setOpen(true)}
        onBlur={onBlur}
        onKeyDown={onKey}
        placeholder={placeholder}
        spellCheck={false}
        className="h-8 w-full rounded-lg border border-line bg-sunken pr-8 pl-8 text-sm text-ink outline-none placeholder:text-ink-4 focus:border-accent/60"
      />
      {fc.text ? (
        <button
          type="button"
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => fc.setText('')}
          className="absolute top-1/2 right-1.5 grid size-5 -translate-y-1/2 place-items-center rounded text-ink-3 hover:bg-line hover:text-ink"
          aria-label="Clear text"
        >
          <X className="size-3.5" />
        </button>
      ) : (
        <Kbd className="pointer-events-none absolute top-1/2 right-2 -translate-y-1/2">/</Kbd>
      )}
      {open && (items.length > 0 || ft) && (
        <div
          onMouseDown={(e) => e.preventDefault()}
          className="anim-pop absolute top-full right-0 z-40 mt-1.5 w-[400px] max-w-[90vw] overflow-hidden rounded-lg bg-raised shadow-pop"
        >
          <div className="flex items-center justify-between border-b border-line px-3 py-1.5 text-2xs text-ink-3">
            <span>
              {ft ? (
                <>
                  <span className="font-medium text-ink-2">{ft.facet.label}</span> {ft.neg ? 'is not…' : 'is…'}
                </>
              ) : term ? (
                <>
                  Rows containing “<span className="text-ink-2">{last}</span>” are shown · or pick a value
                </>
              ) : (
                'Filter by'
              )}
            </span>
            {!ft && !term && (
              <span className="text-ink-4">
                type to search · <span className="font-mono text-ink-3">{fc.facets[0]?.key}:</span> picks · <span className="font-mono text-ink-3">-word</span> excludes
              </span>
            )}
          </div>
          <div className="max-h-[min(420px,60vh)] overflow-y-auto py-1">
            {items.length === 0 && <div className="px-3 py-3 text-xs text-ink-3">No matching values</div>}
            {items.map((it, i) =>
              it.kind === 'facet' ? (
                <FacetOverviewRow
                  key={`f-${it.facet.key}`}
                  fc={fc}
                  facet={it.facet}
                  active={i === sel}
                  onHover={() => setSel(i)}
                  onOpen={() => pick(it, false)}
                />
              ) : (
                <ValueRow
                  key={`v-${it.facet.key}-${it.value}`}
                  fc={fc}
                  facetKey={it.facet.key}
                  value={it.value}
                  count={it.count}
                  max={max}
                  label={ft ? undefined : it.facet.label}
                  active={i === sel}
                  onHover={() => setSel(i)}
                  onPick={(neg) => pick(it, neg, !!ft)}
                />
              ),
            )}
          </div>
          <div className="flex items-center gap-3 border-t border-line px-3 py-1.5 text-2xs whitespace-nowrap text-ink-4">
            <span className="flex items-center gap-1">
              <Kbd>↵</Kbd> {ft ? 'toggle' : 'pick'}
            </span>
            <span className="flex items-center gap-1">
              <Kbd>⇧↵</Kbd> exclude
            </span>
            <span className="flex items-center gap-1">
              <Kbd>⌫</Kbd> remove last chip
            </span>
            <button
              type="button"
              onClick={() => {
                setOpen(false)
                input.current?.blur()
                openFilterPopup()
              }}
              className="ml-auto flex items-center gap-1 rounded px-1 text-ink-3 hover:bg-line hover:text-ink"
            >
              All attributes & tags <Kbd>F</Kbd>
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

function FacetOverviewRow<T>({ fc, facet, active, onHover, onOpen }: { fc: FacetCtl<T>; facet: Facet<T>; active: boolean; onHover: () => void; onOpen: () => void }) {
  const counts = fc.counts(facet.key)
  // as many top values as fit in roughly one line, each shown in full
  const top: FacetCount[] = []
  let budget = 38
  for (const c of counts) {
    if (c.count <= 0) continue
    const w = (c.value === NONE ? 7 : c.value.length) + 5
    if (top.length && w > budget) break
    top.push(c)
    budget -= w
    if (top.length === 3) break
  }
  const n = fc.filters.filter((f) => f.key === facet.key).length
  return (
    <div
      role="option"
      aria-selected={active}
      onMouseEnter={onHover}
      onClick={onOpen}
      className={clsx('flex h-8 cursor-pointer items-center gap-2 px-3 text-sm', active && 'bg-panel-hover')}
    >
      <span className="w-24 shrink-0 truncate font-medium text-ink-2">
        {facet.label}
        {n > 0 && <span className="ml-1 text-2xs text-accent-ink">· {n}</span>}
      </span>
      <span className="flex min-w-0 flex-1 items-center gap-1 overflow-hidden">
        {top.map((c) => (
          <button
            key={c.value}
            type="button"
            title={`Filter: ${facet.label} is ${c.value === NONE ? 'not set' : c.value}`}
            onClick={(e) => {
              e.stopPropagation()
              fc.toggle(facet.key, c.value, e.shiftKey || e.altKey)
            }}
            className={clsx(
              'inline-flex h-5 max-w-[190px] min-w-0 shrink items-center gap-1 rounded px-1.5 text-2xs whitespace-nowrap',
              fc.filters.some((f) => f.key === facet.key && f.value === c.value) ? 'bg-accent-wash text-accent-ink' : 'bg-line text-ink-2 hover:bg-line-strong',
            )}
          >
            <span className="truncate">{fc.display(facet.key, c.value)}</span>
            <span className="tnum text-ink-4">{fmtInt(c.count)}</span>
          </button>
        ))}
      </span>
      <span className="tnum flex shrink-0 items-center gap-0.5 text-2xs text-ink-4">
        {counts.length}
        <ChevronRight className="size-3" />
      </span>
    </div>
  )
}

/** One facet value: state, label, share-of-rows bar, count; hover offers only / exclude. */
function ValueRow<T>({
  fc,
  facetKey,
  value,
  count,
  max,
  label,
  active,
  onHover,
  onPick,
}: {
  fc: FacetCtl<T>
  facetKey: string
  value: string
  count: number
  max: number
  label?: string
  active?: boolean
  onHover?: () => void
  onPick: (neg: boolean) => void
}) {
  const st = fc.filters.find((f) => f.key === facetKey && f.value === value)
  return (
    <div
      role="option"
      aria-selected={active}
      onMouseEnter={onHover}
      onClick={(e) => onPick(e.shiftKey || e.altKey)}
      className={clsx('group/v relative flex h-8 cursor-pointer items-center gap-2 px-3 text-sm', active && 'bg-panel-hover', !active && 'hover:bg-panel-hover')}
    >
      <span
        className={clsx(
          'relative grid size-4 shrink-0 place-items-center rounded border',
          st && !st.neg && 'border-accent bg-accent text-white',
          st?.neg && 'border-crit bg-crit text-white',
          !st && 'border-line-strong',
        )}
      >
        {st && (st.neg ? <Minus className="size-3" /> : <Check className="size-3" />)}
      </span>
      {label && <span className="relative shrink-0 text-2xs text-ink-3">{label}</span>}
      <span className={clsx('relative min-w-0 flex-1 truncate', st?.neg && 'line-through decoration-crit/60', count === 0 && !st && 'text-ink-4')}>
        {fc.display(facetKey, value)}
      </span>
      <span className="relative hidden shrink-0 items-center gap-0.5 group-hover/v:flex">
        <button
          type="button"
          title="Show only this"
          onClick={(e) => {
            e.stopPropagation()
            fc.only(facetKey, value)
          }}
          className="rounded px-1.5 text-2xs text-ink-3 hover:bg-line hover:text-ink"
        >
          only
        </button>
        <button
          type="button"
          title="Exclude (⇧-click)"
          onClick={(e) => {
            e.stopPropagation()
            fc.toggle(facetKey, value, true)
          }}
          className="grid size-5 place-items-center rounded text-ink-3 hover:bg-crit-wash hover:text-crit"
        >
          <Ban className="size-3" />
        </button>
      </span>
      <span className="relative h-1 w-10 shrink-0 overflow-hidden rounded-full bg-line">
        <span className={clsx('absolute inset-y-0 left-0 rounded-full', st?.neg ? 'bg-crit/60' : 'bg-accent/70')} style={{ width: `${(100 * count) / max}%` }} />
      </span>
      <span className="tnum relative w-9 shrink-0 text-right text-xs text-ink-3">{fmtInt(count)}</span>
    </div>
  )
}

/** A facet's full value list with its own search: column headers and chips open this. */
export function FacetValues<T>({ fc, facetKey }: { fc: FacetCtl<T>; facetKey: string }) {
  const [q, setQ] = useState('')
  const facet = fc.facet(facetKey)
  const counts = fc.counts(facetKey)
  const shown = counts.filter((c) => !q || c.value.toLowerCase().includes(q.toLowerCase()))
  const max = Math.max(1, ...counts.map((c) => c.count))
  const n = fc.filters.filter((f) => f.key === facetKey).length
  if (!facet) return null
  return (
    <div className="w-[320px] max-w-[90vw]">
      <div className="flex items-center justify-between border-b border-line px-3 py-2">
        <span className="text-xs font-medium text-ink-2">{facet.label}</span>
        <span className="text-2xs text-ink-4">
          {counts.length} {counts.length === 1 ? 'value' : 'values'}
          {n > 0 && (
            <button type="button" onClick={() => fc.clearKey(facetKey)} className="ml-2 text-accent-ink hover:underline">
              clear
            </button>
          )}
        </span>
      </div>
      {counts.length > 8 && (
        <div className="border-b border-line p-1.5">
          <input
            autoFocus
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder={`Search ${facet.label.toLowerCase()}…`}
            className="h-7 w-full rounded-md bg-sunken px-2 text-xs outline-none placeholder:text-ink-4"
          />
        </div>
      )}
      <div className="max-h-80 overflow-y-auto py-1">
        {shown.map((c) => (
          <ValueRow key={c.value} fc={fc} facetKey={facetKey} value={c.value} count={c.count} max={max} onPick={(neg) => fc.toggle(facetKey, c.value, neg)} />
        ))}
        {shown.length === 0 && <div className="px-3 py-3 text-xs text-ink-3">No values</div>}
      </div>
      <div className="border-t border-line px-3 py-1.5 text-2xs text-ink-4">Click to toggle · ⇧-click to exclude</div>
    </div>
  )
}

// ── chips + count, for the panel header ────────────────────────────────────

/**
 * "12 of 95 pods" plus one removable chip per filter (client facets and
 * server-side attribute filters alike), each reopening its values. Also hosts
 * the list's filter popup ('f').
 */
export function FacetSummary<T>({
  fc,
  noun,
  fetching,
  limit,
  capped: cappedProp,
}: {
  fc: FacetCtl<T>
  noun: string
  fetching?: boolean
  /** the query's record cap: reaching it marks the list capped */
  limit?: number
  /** for lists merged from several capped queries, where no single limit applies */
  capped?: boolean
}) {
  const groups = useMemo(() => {
    const m = new Map<string, FacetFilter[]>()
    for (const f of fc.filters) {
      const k = `${f.neg ? '-' : ''}${f.key}`
      m.set(k, [...(m.get(k) ?? []), f])
    }
    return [...m.values()]
  }, [fc.filters])
  const attrGroups = useMemo(() => {
    const m = new Map<string, AttrFilter[]>()
    for (const f of fc.attrs?.filters ?? []) {
      const k = `${f.neg ? '-' : ''}${f.field}`
      m.set(k, [...(m.get(k) ?? []), f])
    }
    return [...m.values()]
  }, [fc.attrs?.filters])

  const host = useId()
  useEffect(() => registerFilterHost(host), [host])
  const popup = useStore(filterPopupStore)

  const shown = fc.rows?.length
  const capped = cappedProp ?? (limit != null && fc.total >= limit)
  const nChips = groups.length + attrGroups.length
  return (
    <div className="flex min-w-0 flex-1 items-center gap-1.5">
      <h2 className="flex shrink-0 items-center gap-1.5 text-sm font-medium">
        {/* "x of y" only when filters here hide loaded rows; server-side filters already shaped the total */}
        {shown == null ? '…' : shown !== fc.total ? (
          <span>
            {fmtInt(shown)} <span className="font-normal text-ink-3">of {fmtInt(fc.total)}{capped && '+'}</span>
          </span>
        ) : (
          <span>
            {fmtInt(shown)}
            {capped && '+'}
          </span>
        )}
        <span>{noun}</span>
        {capped && (
          <Tip content={`${limit != null ? `Only the first ${fmtInt(limit)} are loaded.` : 'Not all of them are loaded.'} Filter by attributes or tags (f) to narrow on the server.`}>
            <button type="button" onClick={() => openFilterPopup(undefined, host)} className="rounded bg-warn-wash px-1.5 text-2xs font-normal text-warn hover:brightness-110">
              capped
            </button>
          </Tip>
        )}
        {fetching && <Loader2 className="size-3 animate-spin text-ink-4" />}
      </h2>
      <div className="no-scrollbar flex min-w-0 items-center gap-1 overflow-x-auto">
        {groups.map((g) => (
          <FilterChip key={`${g[0].neg ? '-' : ''}${g[0].key}`} fc={fc} group={g} />
        ))}
        {attrGroups.map((g) => (
          <AttrChip key={`a${g[0].neg ? '-' : ''}${g[0].field}`} fc={fc} group={g} onOpen={() => openFilterPopup(`a:${g[0].field}`, host)} />
        ))}
        <Tip content={<span className="flex items-center gap-1.5">Filter by any attribute, tag or label <Kbd>F</Kbd></span>}>
          <button
            type="button"
            onClick={() => openFilterPopup(undefined, host)}
            className="flex h-6 shrink-0 items-center gap-1 rounded-md px-1.5 text-xs text-ink-3 hover:bg-line hover:text-ink"
          >
            <Plus className="size-3" />
            {nChips === 0 && 'Filter'}
          </button>
        </Tip>
        {(nChips > 1 || (nChips > 0 && fc.text.trim())) && (
          <button type="button" onClick={fc.clear} className="shrink-0 rounded px-1.5 text-2xs text-ink-3 hover:bg-line hover:text-ink">
            Clear all
          </button>
        )}
      </div>
      {popup.host === host && <FilterPopup fc={fc} noun={noun} initialField={popup.field} fetching={fetching} onClose={closeFilterPopup} />}
    </div>
  )
}

function FilterChip<T>({ fc, group }: { fc: FacetCtl<T>; group: FacetFilter[] }) {
  const facet = fc.facet(group[0].key)
  const neg = !!group[0].neg
  return (
    <span
      className={clsx(
        'inline-flex h-6 shrink-0 items-center overflow-hidden rounded-md text-xs',
        neg ? 'bg-crit-wash text-crit' : 'bg-accent-wash text-accent-ink',
      )}
    >
      <Popover.Root>
        <Popover.Trigger asChild>
          <button type="button" className="flex h-full max-w-72 items-center gap-1 pr-1 pl-2 hover:brightness-110">
            <span className="opacity-75">{facet?.label}</span>
            <span className="opacity-75">{neg ? '≠' : group.length > 1 ? 'in' : '='}</span>
            <span className="truncate font-medium">
              {group.slice(0, 2).map((f, i) => (
                <span key={f.value}>
                  {i > 0 && ', '}
                  {fc.display(f.key, f.value)}
                </span>
              ))}
              {group.length > 2 && ` +${group.length - 2}`}
            </span>
          </button>
        </Popover.Trigger>
        <Popover.Portal>
          <Popover.Content align="start" sideOffset={6} className="anim-pop z-50 overflow-hidden rounded-lg bg-raised shadow-pop">
            <FacetValues fc={fc} facetKey={group[0].key} />
          </Popover.Content>
        </Popover.Portal>
      </Popover.Root>
      <button type="button" onClick={() => fc.remove(group)} className="grid h-full w-5 place-items-center hover:bg-black/10" aria-label="Remove filter">
        <X className="size-3" />
      </button>
    </span>
  )
}

function AttrChip<T>({ fc, group, onOpen }: { fc: FacetCtl<T>; group: AttrFilter[]; onOpen: () => void }) {
  const neg = !!group[0].neg
  const curated = fc.attrs?.source.suggested?.find((x) => x.field === group[0].field)
  const d = curated ? { kind: '', name: curated.label } : describeField(group[0].field)
  const show = (v: string) => (v === UNSET ? <span className="italic">not set</span> : v)
  return (
    <span className={clsx('inline-flex h-6 shrink-0 items-center overflow-hidden rounded-md text-xs', neg ? 'bg-crit-wash text-crit' : 'bg-accent-wash text-accent-ink')}>
      <button type="button" onClick={onOpen} title={group[0].field} className="flex h-full max-w-80 items-center gap-1 pr-1 pl-2 hover:brightness-110">
        {d.kind && <span className="opacity-60">{d.kind}</span>}
        <span className="opacity-75">{d.name}</span>
        <span className="opacity-75">{neg ? '≠' : group.length > 1 ? 'in' : '='}</span>
        <span className="truncate font-medium">
          {group.slice(0, 2).map((f, i) => (
            <span key={f.value}>
              {i > 0 && ', '}
              {show(f.value)}
            </span>
          ))}
          {group.length > 2 && ` +${group.length - 2}`}
        </span>
      </button>
      <button type="button" onClick={() => fc.attrs?.remove(group)} className="grid h-full w-5 place-items-center hover:bg-black/10" aria-label="Remove filter">
        <X className="size-3" />
      </button>
    </span>
  )
}

// ── inside the table ────────────────────────────────────────────────────────

/** Funnel in a column header; filled when the column's facet is filtered. */
export function ColumnFacetButton<T>({ fc, facetKey }: { fc: FacetCtl<T>; facetKey: string }) {
  const n = fc.filters.filter((f) => f.key === facetKey).length
  return (
    <Popover.Root>
      <Popover.Trigger asChild>
        <button
          type="button"
          title="Filter this column"
          onClick={(e) => e.stopPropagation()}
          className={clsx(
            'grid size-5 shrink-0 place-items-center rounded transition-opacity',
            n ? 'text-accent-ink opacity-100' : 'opacity-0 group-hover/h:opacity-100 hover:bg-line hover:text-ink focus-visible:opacity-100 data-[state=open]:opacity-100',
          )}
        >
          <ListFilter className="size-3" />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content align="start" sideOffset={6} className="anim-pop z-50 overflow-hidden rounded-lg bg-raised font-normal tracking-normal normal-case shadow-pop">
          <FacetValues fc={fc} facetKey={facetKey} />
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  )
}

/** Hover affordance on a cell: + filter to this value, − exclude it. */
export function CellFilter<T>({ fc, facetKey, row }: { fc: FacetCtl<T>; facetKey: string; row: T }) {
  const facet = fc.facet(facetKey)
  if (!facet) return null
  const vs = valuesOf(facet, row)
  if (vs.length !== 1) return null
  const v = vs[0]
  const stop = (e: React.MouseEvent) => {
    e.preventDefault()
    e.stopPropagation()
  }
  return (
    <span className="absolute top-1/2 right-0 hidden -translate-y-1/2 items-center rounded-md border border-line bg-raised shadow-sm group-hover/cell:flex">
      <button
        type="button"
        title={`Only ${facet.label.toLowerCase()} = ${v === NONE ? 'not set' : v}`}
        onClick={(e) => {
          stop(e)
          fc.only(facetKey, v)
        }}
        className="grid size-5 place-items-center rounded-l-md text-ink-3 hover:bg-accent-wash hover:text-accent-ink"
      >
        <Plus className="size-3" />
      </button>
      <button
        type="button"
        title={`Exclude ${facet.label.toLowerCase()} = ${v === NONE ? 'not set' : v}`}
        onClick={(e) => {
          stop(e)
          fc.toggle(facetKey, v, true)
        }}
        className="grid size-5 place-items-center rounded-r-md text-ink-3 hover:bg-crit-wash hover:text-crit"
      >
        <Minus className="size-3" />
      </button>
    </span>
  )
}
