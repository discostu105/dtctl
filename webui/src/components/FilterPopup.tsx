import clsx from 'clsx'
import { Ban, Check, ChevronLeft, ChevronRight, ListFilter, Loader2, Minus, X } from 'lucide-react'
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { useDql } from '../lib/api'
import { useAdaptiveDql } from '../lib/sampling'
import {
  ATTRIBUTES,
  describeField,
  discoveryQuery,
  groupRank,
  isPattern,
  parseDiscovery,
  parseValues,
  TOP_VALUES,
  UNSET,
  valuesQuery,
  type AttrCandidate,
} from '../lib/attrs'
import { NONE } from '../lib/facets'
import { fmtCompact, fmtInt, fmtPct } from '../lib/format'
import type { FacetCtl } from './Facets'
import { Kbd } from './ui'

// The filter popup ('f'): one keyboard-first place to filter a list by anything
// it knows about. Left: the fields — what is active, the page's curated facets
// (instant, client-side) and every attribute and tag the entities carry
// (discovered from a sample, filtered server-side). Right: the values of the
// highlighted field with counts, previewed as you move. Typing `app=check`
// jumps straight to a value; Enter picks and closes, Space keeps picking.

type Field =
  | { kind: 'facet'; id: string; key: string; name: string; group: string }
  | { kind: 'attr'; id: string; field: string; name: string; group: string; tag: string; coverage?: number; typed?: boolean; curated?: boolean }

interface Opt {
  value: string
  count: number | null
  state: 'inc' | 'exc' | null
  special?: 'pattern' | 'exact'
}

const ACTIVE = 'Active'
const SUGGESTED = 'Suggested'

export const facetFieldId = (key: string) => `f:${key}`
export const attrFieldId = (field: string) => `a:${field}`

/** Field paths read as code; curated everyday names (Level, Namespace) don't. */
const rawName = (f: Field) => f.kind === 'attr' && !f.curated

function attrField(c: AttrCandidate | { field: string; coverage?: number }, typed = false): Field {
  const d = describeField(c.field)
  return { kind: 'attr', id: attrFieldId(c.field), field: c.field, name: d.name, group: d.group, tag: d.kind, coverage: c.coverage, typed }
}

function rank(f: Field, q: string) {
  const name = f.name.toLowerCase()
  if (name === q) return 0
  if (name.startsWith(q)) return 1
  if (name.includes(q)) return 2
  if (f.kind === 'attr' && f.field.toLowerCase().includes(q)) return 3
  if (f.group.toLowerCase().includes(q) || (f.kind === 'attr' && f.tag.toLowerCase().includes(q))) return 4
  return -1
}

export function FilterPopup<T>({
  fc,
  noun,
  initialField,
  fetching,
  onClose,
}: {
  fc: FacetCtl<T>
  noun: string
  initialField?: string
  fetching?: boolean
  onClose: () => void
}) {
  const attrs = fc.attrs
  const src = attrs?.source
  const [q, setQ] = useState('')
  const [vq, setVq] = useState('')
  const [pane, setPane] = useState<'fields' | 'values'>('fields')
  const [sel, setSel] = useState(0)
  const [vsel, setVsel] = useState(0)
  const [locked, setLocked] = useState<Field | null>(null)
  // opened from a chip: Esc closes instead of stepping back to the field list
  const [direct, setDirect] = useState(false)
  const input = useRef<HTMLInputElement>(null)

  // return focus to wherever it was (usually the table) on close
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null
    return () => prev?.focus?.({ preventScroll: true })
  }, [])

  // ── fields ────────────────────────────────────────────────────────────────
  const disc = useDql(src ? { query: discoveryQuery(src), from: src.from, to: src.to, ttl: 300 } : null)
  const candidates = useMemo(() => (disc.data ? parseDiscovery(disc.data.records) : []), [disc.data])

  const [qField, qValue] = useMemo(() => {
    const i = q.indexOf('=')
    return i >= 0 ? [q.slice(0, i).trim().toLowerCase(), q.slice(i + 1)] : [q.trim().toLowerCase(), null]
  }, [q])

  const all = useMemo(() => {
    // A curated facet with nothing but "not set" in the loaded rows (a
    // namespace on EC2 instances) is noise, unless it is being filtered on.
    const useful = (key: string) => !fc.rows || fc.filters.some((x) => x.key === key) || fc.counts(key).some((c) => c.value !== NONE && c.count > 0)
    const facetFields: Field[] = [
      ...fc.facets.filter((f) => useful(f.key)).map((f): Field => ({ kind: 'facet', id: facetFieldId(f.key), key: f.key, name: f.label, group: SUGGESTED })),
      // server-side sources can name their everyday fields too (Logs: level, namespace, …)
      ...(src?.suggested ?? []).map((s): Field => ({
        ...(attrField(candidates.find((c) => c.field === s.field) ?? { field: s.field }) as Extract<Field, { kind: 'attr' }>),
        name: s.label,
        group: SUGGESTED,
        tag: '',
        curated: true,
      })),
    ]
    const suggestedAttrs = new Set((src?.suggested ?? []).map((s) => s.field))
    const discovered = candidates
      .filter((c) => !suggestedAttrs.has(c.field))
      .map((c) => attrField(c))
      .sort((a, b) => groupRank(a.group) - groupRank(b.group) || a.group.localeCompare(b.group) || (b.kind === 'attr' && a.kind === 'attr' ? (b.coverage ?? 0) - (a.coverage ?? 0) : 0) || a.name.localeCompare(b.name))
    const activeKeys = [...new Set(fc.filters.map((f) => f.key))]
    const activeAttrs = [...new Set((attrs?.filters ?? []).map((f) => f.field))]
    const active: Field[] = [
      ...activeKeys.flatMap((k) => facetFields.filter((f) => f.kind === 'facet' && f.key === k)),
      ...activeAttrs.map((field) => {
        const f = attrField(candidates.find((c) => c.field === field) ?? { field }) as Extract<Field, { kind: 'attr' }>
        const s = src?.suggested?.find((x) => x.field === field)
        return s ? { ...f, name: s.label, tag: '', curated: true } : f
      }),
    ].map((f) => ({ ...f, group: ACTIVE }))
    return { active, facetFields, discovered }
  }, [fc, src, attrs?.filters, candidates])

  const rows: Field[] = useMemo(() => {
    const { active, facetFields, discovered } = all
    if (!qField) return [...active, ...facetFields, ...discovered]
    const match = (list: Field[]) =>
      list
        .map((f) => [f, rank(f, qField)] as const)
        .filter(([, r]) => r >= 0)
        .sort((a, b) => a[1] - b[1])
        .map(([f]) => f)
    // While searching, the best matches lead regardless of group.
    const out = [...match(active), ...match(facetFields), ...match(discovered).sort((a, b) => rank(a, qField) - rank(b, qField))]
    const exact = out.some((f) => f.kind === 'attr' && (f.field.toLowerCase() === qField || f.name.toLowerCase() === qField))
    if (attrs && !exact &&/^[\w.:\-/[\]@]+$/.test(qField) && !qField.endsWith('.')) {
      // escape hatch: a field the sample didn't contain is still filterable
      const typed = q.slice(0, qValue != null ? q.indexOf('=') : undefined).trim()
      out.push(attrField({ field: typed }, true))
    }
    return out
  }, [all, qField, q, qValue, attrs])

  const cur: Field | undefined = pane === 'values' && locked ? locked : rows[Math.min(sel, rows.length - 1)]

  // open straight at a field (chips, column menus)
  const opened = useRef(false)
  useEffect(() => {
    if (opened.current || !initialField) return
    const f = [...all.active, ...all.facetFields, ...all.discovered].find((x) => x.id === initialField)
    if (f || !initialField.startsWith('a:')) {
      opened.current = true
      const target = f ?? attrField({ field: initialField.slice(2) })
      setLocked(target)
      setPane('values')
      setDirect(true)
    }
  }, [initialField, all])

  // ── values of the current field ──────────────────────────────────────────
  // Attribute values come from the server; debounce while arrowing through.
  const [qAttr, setQAttr] = useState<string | null>(null)
  useEffect(() => {
    const want = cur?.kind === 'attr' ? cur.field : null
    const t = setTimeout(() => setQAttr(want), pane === 'values' ? 0 : 140)
    return () => clearTimeout(t)
  }, [cur?.kind === 'attr' ? cur.field : null, pane]) // eslint-disable-line react-hooks/exhaustive-deps
  const valSpec = src && attrs && qAttr ? { query: valuesQuery(src, qAttr, attrs.filters), from: src.from, to: src.to, ttl: 60 } : null
  // Log values count records, which on a big tenant means sampling like every other log chart.
  const logs = src?.kind === 'logs'
  const plainRes = useDql(logs ? null : valSpec)
  const sampledVals = useAdaptiveDql('logs', logs ? valSpec : null, ['n'])
  const valRes = logs ? sampledVals.res : plainRes
  const sampleRatio = logs ? sampledVals.ratio : 1
  const attrValues = valRes.data && !valRes.isPlaceholderData && qAttr ? parseValues(src!, valRes.data.records) : null
  const valuesLoading = cur?.kind === 'attr' && (!attrValues || qAttr !== cur.field) && !valRes.error

  const valueFilter = pane === 'values' ? vq : (qValue ?? '')
  const opts: Opt[] = useMemo(() => {
    if (!cur) return []
    const vf = valueFilter.trim()
    const needle = vf.toLowerCase()
    const hit = (v: string) => !needle || isPattern(needle) || (v === UNSET ? 'not set'.includes(needle) : v.toLowerCase().includes(needle))
    if (cur.kind === 'facet') {
      return fc
        .counts(cur.key)
        .filter((c) => hit(c.value))
        .map((c) => {
          const f = fc.filters.find((x) => x.key === cur.key && x.value === c.value)
          return { value: c.value, count: c.count, state: f ? (f.neg ? 'exc' : 'inc') : null }
        })
    }
    const sel = (attrs?.filters ?? []).filter((f) => f.field === cur.field)
    const stateOf = (v: string): Opt['state'] => {
      const f = sel.find((x) => x.value === v)
      return f ? (f.neg ? 'exc' : 'inc') : null
    }
    const base: Opt[] = (attrValues?.values ?? []).map((v) => ({ value: v.value, count: v.count, state: stateOf(v.value) }))
    // selected values the top list doesn't contain (patterns, typed values) stay visible
    for (const f of sel) if (!base.some((o) => o.value === f.value)) base.unshift({ value: f.value, count: null, state: stateOf(f.value) })
    // "not set" goes last; it is the answer to "what lacks this tag?"
    base.sort((a, b) => (a.value === UNSET ? 1 : 0) - (b.value === UNSET ? 1 : 0))
    const out = base.filter((o) => hit(o.value))
    if (vf && isPattern(vf)) out.unshift({ value: vf, count: null, state: stateOf(vf), special: 'pattern' })
    else if (vf && !base.some((o) => o.value === vf)) out.push({ value: vf, count: null, state: null, special: 'exact' })
    return out
  }, [cur, valueFilter, fc, attrs?.filters, attrValues])

  // ── actions ─────────────────────────────────────────────────────────────
  const pick = (o: Opt, neg: boolean, stay: boolean) => {
    if (!cur) return
    if (cur.kind === 'facet') fc.toggle(cur.key, o.value, neg)
    else attrs?.toggle(cur.field, o.value, neg)
    if (!stay) onClose()
  }
  const only = (o: Opt) => {
    if (!cur) return
    if (cur.kind === 'facet') fc.only(cur.key, o.value)
    else attrs?.only(cur.field, o.value)
  }
  const clearField = (f: Field) => {
    if (f.kind === 'facet') fc.clearKey(f.key)
    else attrs?.clearField(f.field)
  }
  const enterValues = (f: Field | undefined = cur) => {
    if (!f) return
    setLocked(f)
    setPane('values')
    setVq(qValue ?? '')
    setVsel(0)
  }
  const back = () => {
    setDirect(false)
    setPane('fields')
    setVq('')
    setLocked(null)
    // keep the field we came from highlighted
    if (locked) {
      const i = rows.findIndex((r) => r.id === locked.id)
      if (i >= 0) setSel(i)
    }
  }

  useEffect(() => setSel(0), [qField])
  useEffect(() => setVsel(0), [valueFilter, cur?.id])
  useEffect(() => input.current?.focus(), [pane])

  const onKey = (e: React.KeyboardEvent<HTMLInputElement>) => {
    const k = e.key
    const fields = pane === 'fields'
    const stop = () => {
      e.preventDefault()
      e.stopPropagation()
    }
    if (k === 'ArrowDown' || (e.ctrlKey && k === 'n')) {
      stop()
      if (fields) setSel((s) => Math.min(rows.length - 1, s + 1))
      else setVsel((s) => Math.min(opts.length - 1, s + 1))
    } else if (k === 'ArrowUp' || (e.ctrlKey && k === 'p')) {
      stop()
      if (fields) setSel((s) => Math.max(0, s - 1))
      else setVsel((s) => Math.max(0, s - 1))
    } else if (k === 'PageDown' || k === 'PageUp') {
      stop()
      const d = k === 'PageDown' ? 10 : -10
      if (fields) setSel((s) => Math.max(0, Math.min(rows.length - 1, s + d)))
      else setVsel((s) => Math.max(0, Math.min(opts.length - 1, s + d)))
    } else if (k === 'Tab') {
      stop()
      if (fields && !e.shiftKey) enterValues()
      else if (!fields && e.shiftKey) back()
    } else if (k === 'ArrowRight' && fields && input.current?.selectionStart === q.length && cur) {
      stop()
      enterValues()
    } else if (k === 'ArrowLeft' && !fields && !vq) {
      stop()
      back()
    } else if (k === 'Enter') {
      stop()
      if (fields) {
        // "app=checkout" + Enter applies the first matching value right away
        if (qValue != null && qValue.trim() && opts[0]) pick(opts[0], e.shiftKey, e.metaKey || e.ctrlKey)
        else enterValues()
      } else if (opts[vsel]) pick(opts[vsel], e.shiftKey, e.metaKey || e.ctrlKey)
    } else if (k === ' ' && !fields && !vq) {
      stop()
      if (opts[vsel]) pick(opts[vsel], e.shiftKey, true)
    } else if (k === 'Escape') {
      stop()
      if (!fields) {
        if (vq) setVq('')
        else if (direct) onClose()
        else back()
      } else if (q) setQ('')
      else onClose()
    } else if (k === 'Backspace' && !fields && !vq) {
      stop()
      back()
    } else if ((k === 'Delete' || (k === 'Backspace' && (e.metaKey || e.ctrlKey))) && fields && cur?.group === ACTIVE) {
      stop()
      clearField(cur)
    }
  }

  const facetCount = (key: string) => fc.counts(key).filter((c) => c.count > 0).length
  const maxCount = Math.max(1, ...opts.map((o) => o.count ?? 0))
  const shown = fc.rows?.length

  return createPortal(
    <div className="fixed inset-0 z-50 flex items-start justify-center bg-black/40 pt-[10vh] backdrop-blur-[2px]" onMouseDown={onClose}>
      <div
        role="dialog"
        aria-label={`Filter ${noun}`}
        className="anim-pop flex h-[min(560px,78vh)] w-[min(880px,94vw)] flex-col overflow-hidden rounded-xl bg-raised shadow-pop"
        onMouseDown={(e) => {
          e.stopPropagation()
          // keep the input focused while clicking around in the lists
          if (e.target !== input.current) e.preventDefault()
        }}
      >
        <div className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-3">
          <ListFilter className="size-4 shrink-0 text-ink-3" />
          {pane === 'values' && cur && (
            <button type="button" onClick={back} className="flex shrink-0 items-center gap-1 rounded-md bg-accent-wash py-0.5 pr-2 pl-1 text-sm text-accent-ink hover:brightness-110">
              <ChevronLeft className="size-3.5" />
              <FieldName f={cur} />
            </button>
          )}
          <input
            ref={input}
            autoFocus
            value={pane === 'fields' ? q : vq}
            onChange={(e) => (pane === 'fields' ? setQ(e.target.value) : setVq(e.target.value))}
            onKeyDown={onKey}
            spellCheck={false}
            placeholder={pane === 'fields' ? `Filter ${noun} by… (try “app=” or a tag)` : `Find a value, or type one — * matches a pattern`}
            className="h-full min-w-0 flex-1 bg-transparent text-[15px] text-ink outline-none placeholder:text-ink-4"
          />
          <Kbd>esc</Kbd>
        </div>

        <div className="grid min-h-0 flex-1 grid-cols-[minmax(240px,320px)_1fr] max-sm:grid-cols-1">
          {/* fields */}
          <div className={clsx('min-h-0 overflow-y-auto border-r border-line py-1', pane === 'values' && 'max-sm:hidden')}>
            {rows.length === 0 && <div className="px-3 py-4 text-xs text-ink-3">{disc.isLoading ? 'Discovering attributes…' : 'No field matches.'}</div>}
            {rows.map((f, i) => {
              const header = !qField && (i === 0 || rows[i - 1].group !== f.group) ? f.group : null
              const isCur = pane === 'fields' ? i === Math.min(sel, rows.length - 1) : locked?.id === f.id && f.group !== ACTIVE
              return (
                <div key={`${f.group}|${f.id}`}>
                  {header && <GroupHeader label={header} loading={header === SUGGESTED && !!src && disc.isLoading} />}
                  <FieldRow
                    f={f}
                    active={isCur}
                    focused={pane === 'fields'}
                    fc={fc}
                    showGroup={!!qField}
                    right={
                      f.group === ACTIVE ? null : f.kind === 'facet' ? (
                        <span className="tnum">{facetCount(f.key)}</span>
                      ) : f.typed ? (
                        <span>any field</span>
                      ) : f.coverage != null ? (
                        <span className="tnum" title={`on ${fmtPct(100 * f.coverage, 0)} of sampled ${noun}`}>
                          {f.coverage >= 0.995 ? 'all' : fmtPct(100 * f.coverage, 0)}
                        </span>
                      ) : null
                    }
                    onHover={() => pane === 'fields' && setSel(i)}
                    onClick={() => {
                      setSel(i)
                      enterValues(f)
                    }}
                    onRemove={f.group === ACTIVE ? () => clearField(f) : undefined}
                  />
                </div>
              )
            })}
            {src && disc.error && <div className="px-3 py-2 text-2xs text-crit">Attribute discovery failed: {disc.error.message}</div>}
            {src && !disc.isLoading && !disc.error && !qField && candidates.length === 0 && (
              <div className="px-3 py-2 text-2xs text-ink-4">No further attributes found on these {noun}.</div>
            )}
          </div>

          {/* values */}
          <div className={clsx('flex min-h-0 flex-col', pane === 'fields' && 'max-sm:hidden')}>
            {cur ? (
              <>
                <ValuesHeader f={cur} fc={fc} noun={noun} total={attrValues?.total} ratio={sampleRatio} n={cur.kind === 'facet' ? facetCount(cur.key) : attrValues?.values.filter((v) => v.value !== UNSET).length} />
                <div className={clsx('min-h-0 flex-1 overflow-y-auto py-1', pane === 'fields' && 'opacity-80')}>
                  {cur.kind === 'attr' && valRes.error && <div className="px-3 py-3 text-xs text-crit">{valRes.error.message}</div>}
                  {valuesLoading && !opts.some((o) => o.special) && <ValueSkeleton />}
                  {!valuesLoading && opts.length === 0 && (
                    <div className="px-3 py-3 text-xs text-ink-3">{valueFilter ? 'No value matches.' : 'No values.'}</div>
                  )}
                  {(!valuesLoading || opts.some((o) => o.special)) &&
                    opts.map((o, i) => (
                      <ValueOption
                        key={`${o.special ?? ''}${o.value}`}
                        o={o}
                        max={maxCount}
                        active={pane === 'values' && i === vsel}
                        display={cur.kind === 'facet' ? fc.display(cur.key, o.value) : o.value === UNSET ? <NotSet /> : o.value}
                        onHover={() => pane === 'values' && setVsel(i)}
                        onPick={(neg) => {
                          if (pane === 'fields') enterValues(cur)
                          pick(o, neg, true)
                        }}
                        onOnly={() => only(o)}
                      />
                    ))}
                </div>
              </>
            ) : (
              <div className="grid flex-1 place-items-center px-6 text-center text-xs text-ink-3">Pick a field to see its values.</div>
            )}
          </div>
        </div>

        <div className="flex h-9 shrink-0 items-center gap-3 border-t border-line px-3 text-2xs whitespace-nowrap text-ink-4">
          {pane === 'fields' ? (
            <>
              <Hint k="↑↓">field</Hint>
              <Hint k="↵">values</Hint>
              <Hint k="key=value ↵">quick pick</Hint>
              {all.active.length > 0 && <Hint k="del">remove</Hint>}
            </>
          ) : (
            <>
              <Hint k="↵">pick</Hint>
              <Hint k="space">pick more</Hint>
              <Hint k="⇧">exclude</Hint>
              <Hint k="⌫">back</Hint>
            </>
          )}
          <span className="ml-auto flex items-center gap-1.5 text-ink-3">
            {fetching && <Loader2 className="size-3 animate-spin" />}
            {shown == null ? '…' : shown !== fc.total ? `${fmtInt(shown)} of ${fmtInt(fc.total)} ${noun}` : `${fmtInt(shown)} ${noun}`}
          </span>
        </div>
      </div>
    </div>,
    document.body,
  )
}

function Hint({ k, children }: { k: string; children: ReactNode }) {
  return (
    <span className="flex items-center gap-1">
      <Kbd>{k}</Kbd> {children}
    </span>
  )
}

function GroupHeader({ label, loading }: { label: string; loading?: boolean }) {
  return (
    <div className="flex items-center gap-1.5 px-3 pt-2.5 pb-1 text-2xs font-medium tracking-wide text-ink-3 uppercase">
      {label}
      {loading && <Loader2 className="size-3 animate-spin text-ink-4" />}
    </div>
  )
}

function FieldName({ f }: { f: Field }) {
  return (
    <span className="flex min-w-0 items-baseline gap-1">
      {f.kind === 'attr' && f.tag && <span className="shrink-0 text-2xs opacity-70">{f.tag}</span>}
      <span className={clsx('truncate', rawName(f) && 'font-mono text-[13px]')}>{f.name}</span>
    </span>
  )
}

function FieldRow<T>({
  f,
  active,
  focused,
  fc,
  showGroup,
  right,
  onHover,
  onClick,
  onRemove,
}: {
  f: Field
  active: boolean
  focused: boolean
  fc: FacetCtl<T>
  showGroup: boolean
  right: ReactNode
  onHover: () => void
  onClick: () => void
  onRemove?: () => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    if (active && focused) ref.current?.scrollIntoView({ block: 'nearest' })
  }, [active, focused])
  return (
    <div
      ref={ref}
      role="option"
      aria-selected={active}
      onMouseMove={onHover}
      onClick={onClick}
      title={f.kind === 'attr' ? f.field : undefined}
      className={clsx(
        'group/f flex h-8 cursor-pointer items-center gap-2 px-3 text-sm',
        active ? (focused ? 'bg-accent-wash text-ink' : 'bg-panel-hover') : 'text-ink-2 hover:bg-panel-hover',
      )}
    >
      {showGroup && f.group !== ACTIVE && f.group !== SUGGESTED && f.group !== ATTRIBUTES && f.kind === 'attr' && f.tag && (
        <span className="shrink-0 text-2xs text-ink-4">{f.tag}</span>
      )}
      <span className={clsx('min-w-0 truncate', rawName(f) && 'font-mono text-[13px]')}>{f.name}</span>
      {f.group === ACTIVE && <ActiveSummary f={f} fc={fc} />}
      <span className="ml-auto flex shrink-0 items-center gap-1 text-2xs text-ink-4">
        {right}
        {onRemove ? (
          <button
            type="button"
            aria-label="Remove filter"
            onClick={(e) => {
              e.stopPropagation()
              onRemove()
            }}
            className="grid size-5 place-items-center rounded text-ink-3 hover:bg-line hover:text-ink"
          >
            <X className="size-3" />
          </button>
        ) : (
          <ChevronRight className={clsx('size-3', !active && 'opacity-0 group-hover/f:opacity-100')} />
        )}
      </span>
    </div>
  )
}

function ActiveSummary<T>({ f, fc }: { f: Field; fc: FacetCtl<T> }) {
  const list =
    f.kind === 'facet'
      ? fc.filters.filter((x) => x.key === f.key).map((x) => ({ value: x.value, neg: !!x.neg }))
      : (fc.attrs?.filters ?? []).filter((x) => x.field === f.field).map((x) => ({ value: x.value, neg: !!x.neg }))
  const inc = list.filter((x) => !x.neg).map((x) => x.value)
  const exc = list.filter((x) => x.neg).map((x) => x.value)
  const fmt = (vs: string[]) => vs.map((v) => (v === NONE ? 'not set' : v)).slice(0, 2).join(', ') + (vs.length > 2 ? ` +${vs.length - 2}` : '')
  return (
    <span className="min-w-0 truncate text-xs">
      {inc.length > 0 && <span className="text-accent-ink">= {fmt(inc)}</span>}
      {inc.length > 0 && exc.length > 0 && ' · '}
      {exc.length > 0 && <span className="text-crit">≠ {fmt(exc)}</span>}
    </span>
  )
}

function ValuesHeader<T>({ f, fc, noun, total, n, ratio = 1 }: { f: Field; fc: FacetCtl<T>; noun: string; total?: number; n?: number; ratio?: number }) {
  const has = f.kind === 'facet' ? fc.filters.some((x) => x.key === f.key) : (fc.attrs?.filters ?? []).some((x) => x.field === f.field)
  const facts: string[] = []
  if (f.kind === 'attr') {
    if (f.coverage != null) facts.push(`on ${fmtPct(100 * f.coverage, 0)} of sampled ${noun}`)
    if (n != null) facts.push(`${fmtInt(n)}${n >= TOP_VALUES ? '+' : ''} ${n === 1 ? 'value' : 'values'}`)
    if (total != null) facts.push(`${fmtInt(total)} ${noun} in scope`)
    if (ratio > 1) facts.push(`counts ≈ from a 1:${fmtInt(ratio)} sample`)
  } else {
    if (n != null) facts.push(`${fmtInt(n)} ${n === 1 ? 'value' : 'values'} in the loaded ${noun}`)
  }
  return (
    <div className="flex shrink-0 items-start justify-between gap-3 border-b border-line px-3 py-2">
      <div className="min-w-0">
        <div className="flex min-w-0 items-baseline gap-1.5 text-sm font-medium text-ink">
          {f.kind === 'attr' && f.tag && <span className="shrink-0 text-xs font-normal text-ink-3">{f.tag}</span>}
          <span className={clsx('truncate', rawName(f) && 'font-mono text-[13px]')} title={f.kind === 'attr' ? f.field : undefined}>
            {f.name}
          </span>
        </div>
        <div className="truncate text-2xs text-ink-4">{facts.join(' · ') || (f.kind === 'attr' ? 'filtered on the server' : 'from the loaded rows')}</div>
      </div>
      {has && (
        <button
          type="button"
          onClick={() => (f.kind === 'facet' ? fc.clearKey(f.key) : fc.attrs?.clearField(f.field))}
          className="shrink-0 rounded px-1.5 py-0.5 text-2xs text-accent-ink hover:bg-line"
        >
          clear
        </button>
      )}
    </div>
  )
}

function NotSet() {
  return <span className="text-ink-3 italic">not set</span>
}

function ValueSkeleton() {
  return (
    <div className="space-y-2 px-3 py-2">
      {[70, 52, 64, 40, 58, 46].map((w, i) => (
        <div key={i} className="flex items-center gap-2">
          <span className="size-4 rounded border border-line" />
          <span className="h-3 animate-pulse rounded bg-line" style={{ width: `${w}%` }} />
        </div>
      ))}
    </div>
  )
}

function ValueOption({
  o,
  max,
  active,
  display,
  onHover,
  onPick,
  onOnly,
}: {
  o: Opt
  max: number
  active: boolean
  display: ReactNode
  onHover: () => void
  onPick: (neg: boolean) => void
  onOnly: () => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    if (active) ref.current?.scrollIntoView({ block: 'nearest' })
  }, [active])
  return (
    <div
      ref={ref}
      role="option"
      aria-selected={active}
      onMouseMove={onHover}
      onClick={(e) => onPick(e.shiftKey || e.altKey)}
      className={clsx('group/v flex h-8 cursor-pointer items-center gap-2 px-3 text-sm', active ? 'bg-accent-wash' : 'hover:bg-panel-hover')}
    >
      <span
        className={clsx(
          'grid size-4 shrink-0 place-items-center rounded border',
          o.state === 'inc' && 'border-accent bg-accent text-white',
          o.state === 'exc' && 'border-crit bg-crit text-white',
          !o.state && 'border-line-strong',
        )}
      >
        {o.state === 'inc' && <Check className="size-3" />}
        {o.state === 'exc' && <Minus className="size-3" />}
      </span>
      {o.special && <span className="shrink-0 text-2xs text-ink-3">{o.special === 'pattern' ? 'matches' : 'is exactly'}</span>}
      <span
        className={clsx('min-w-0 flex-1 truncate', o.state === 'exc' && 'line-through decoration-crit/60', o.count === 0 && !o.state && 'text-ink-4')}
        title={typeof display === 'string' ? display : undefined}
      >
        {display}
      </span>
      <span className="hidden shrink-0 items-center gap-0.5 group-hover/v:flex">
        <button
          type="button"
          title="Show only this"
          onClick={(e) => {
            e.stopPropagation()
            onOnly()
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
            onPick(true)
          }}
          className="grid size-5 place-items-center rounded text-ink-3 hover:bg-crit-wash hover:text-crit"
        >
          <Ban className="size-3" />
        </button>
      </span>
      {o.count != null ? (
        <>
          <span className="h-1 w-12 shrink-0 overflow-hidden rounded-full bg-line">
            <span className={clsx('block h-full rounded-full', o.state === 'exc' ? 'bg-crit/60' : 'bg-accent/70')} style={{ width: `${(100 * o.count) / max}%` }} />
          </span>
          <span className="tnum w-12 shrink-0 text-right text-xs text-ink-3">{o.count >= 1e5 ? fmtCompact(o.count) : fmtInt(o.count)}</span>
        </>
      ) : (
        <span className="w-[6.5rem] shrink-0 text-right text-2xs text-ink-4">{o.special ? (o.special === 'pattern' ? 'pattern' : 'typed') : ''}</span>
      )}
    </div>
  )
}
