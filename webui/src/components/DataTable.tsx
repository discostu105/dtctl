import { useVirtualizer } from '@tanstack/react-virtual'
import clsx from 'clsx'
import { ArrowDown, ArrowUp } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useLocation } from 'wouter'
import type { Rec } from '../lib/api'
import { CellFilter, ColumnFacetButton, type FacetCtl } from './Facets'
import { openFilterPopup } from '../lib/store'
import { Empty, SkeletonRows } from './ui'

export interface Column<T = Rec> {
  key: string
  header: ReactNode
  /** CSS grid track, e.g. '120px', 'minmax(200px,1fr)' */
  width?: string
  align?: 'left' | 'right'
  render: (r: T) => ReactNode
  sort?: (r: T) => string | number | null | undefined
  className?: string
  /** Facet key: adds a filter menu to the header and +/− on hovered cells. */
  facet?: string
}

export function DataTable<T = Rec>({
  rows,
  columns,
  loading,
  href,
  onOpen,
  onHover,
  rowKey,
  initialSort,
  rowHeight = 36,
  empty,
  className,
  maxHeight,
  selectedKey,
  rowClassName,
  autoFocus,
  facets,
}: {
  rows: T[] | undefined
  columns: Column<T>[]
  loading?: boolean
  href?: (r: T) => string
  onOpen?: (r: T) => void
  onHover?: (r: T) => void
  rowKey: (r: T, i: number) => string
  initialSort?: { key: string; dir: 'asc' | 'desc' }
  rowHeight?: number
  empty?: ReactNode
  className?: string
  maxHeight?: number | string
  selectedKey?: string | null
  rowClassName?: (r: T) => string | undefined
  autoFocus?: boolean
  facets?: FacetCtl<T>
}) {
  const [sort, setSort] = useState(initialSort)
  const [cursor, setCursor] = useState(-1)
  const [, navigate] = useLocation()
  const scroller = useRef<HTMLDivElement>(null)

  const sorted = useMemo(() => {
    const r = rows ?? []
    if (!sort) return r
    const col = columns.find((c) => c.key === sort.key)
    if (!col?.sort) return r
    const dir = sort.dir === 'asc' ? 1 : -1
    return [...r].sort((a, b) => {
      const va = col.sort!(a)
      const vb = col.sort!(b)
      if (va == null || (typeof va === 'number' && isNaN(va))) return 1
      if (vb == null || (typeof vb === 'number' && isNaN(vb))) return -1
      return va < vb ? -dir : va > vb ? dir : 0
    })
  }, [rows, sort, columns])

  const v = useVirtualizer({
    count: sorted.length,
    getScrollElement: () => scroller.current,
    estimateSize: () => rowHeight,
    overscan: 12,
  })

  useEffect(() => {
    if (cursor >= 0) v.scrollToIndex(cursor, { align: 'auto' })
  }, [cursor, v])

  useEffect(() => {
    if (autoFocus) scroller.current?.focus({ preventScroll: true })
  }, [autoFocus])

  const template = columns.map((c) => c.width ?? 'minmax(0,1fr)').join(' ')

  // Rows only look interactive when they are.
  const clickable = !!(onOpen || href)

  const open = (r: T) => {
    if (onOpen) onOpen(r)
    else if (href) navigate(href(r))
  }

  const onKey = (e: React.KeyboardEvent) => {
    if (e.metaKey || e.ctrlKey || e.altKey) return
    if (e.key === 'j' || e.key === 'ArrowDown') {
      e.preventDefault()
      setCursor((c) => Math.min(sorted.length - 1, c + 1))
    } else if (e.key === 'k' || e.key === 'ArrowUp') {
      e.preventDefault()
      setCursor((c) => Math.max(0, c - 1))
    } else if (e.key === 'Enter' && cursor >= 0 && sorted[cursor]) {
      e.preventDefault()
      open(sorted[cursor])
    } else if (e.key === 'Home') {
      // (not g/G: "g" is the app-wide go-to prefix)
      e.preventDefault()
      setCursor(0)
    } else if (e.key === 'End') {
      e.preventDefault()
      setCursor(sorted.length - 1)
    }
  }

  const header = (
    <div
      className="sticky top-0 z-[1] grid h-8 items-center gap-3 border-b border-line bg-panel px-3 text-2xs font-medium tracking-wide text-ink-3 uppercase"
      style={{ gridTemplateColumns: template }}
    >
      {columns.map((c) => (
        <div key={c.key} className={clsx('group/h flex min-w-0 items-center gap-0.5', c.align === 'right' && 'justify-end')}>
          <button
            type="button"
            disabled={!c.sort}
            onClick={() =>
              setSort((s) => (s?.key === c.key ? { key: c.key, dir: s.dir === 'asc' ? 'desc' : 'asc' } : { key: c.key, dir: c.align === 'right' ? 'desc' : 'asc' }))
            }
            className={clsx('flex min-w-0 items-center gap-1 truncate', c.align === 'right' && 'justify-end', c.sort && 'hover:text-ink-2')}
          >
            <span className="truncate">{c.header}</span>
            {sort?.key === c.key && (sort.dir === 'asc' ? <ArrowUp className="size-3 shrink-0" /> : <ArrowDown className="size-3 shrink-0" />)}
          </button>
          {facets && c.facet && <ColumnFacetButton fc={facets} facetKey={c.facet} />}
        </div>
      ))}
    </div>
  )

  return (
    <div
      ref={scroller}
      tabIndex={0}
      onKeyDown={onKey}
      className={clsx('relative min-h-0 overflow-auto outline-none', className)}
      style={{ maxHeight }}
      data-table
    >
      {header}
      {loading && !rows ? (
        <SkeletonRows rows={8} />
      ) : sorted.length === 0 ? (
        facets?.active && (facets.total > 0 || !!facets.attrs?.filters.length) ? (
          <div className="flex flex-col items-center gap-2 py-10">
            <Empty
              title="Nothing matches these filters"
              hint={facets.total > 0 ? `${facets.total} rows are hidden by the current filters.` : 'No record matches the attribute filters.'}
              className="py-0"
            />
            <div className="flex items-center gap-2">
              <button type="button" onClick={() => openFilterPopup()} className="rounded-md px-2.5 py-1 text-xs text-ink-2 hover:bg-line hover:text-ink">
                Edit filters
              </button>
              <button type="button" onClick={facets.clear} className="rounded-md bg-accent-wash px-2.5 py-1 text-xs font-medium text-accent-ink hover:brightness-110">
                Clear filters
              </button>
            </div>
          </div>
        ) : (
          (empty ?? <Empty title="Nothing to show" />)
        )
      ) : (
        <div style={{ height: v.getTotalSize(), position: 'relative' }}>
          {v.getVirtualItems().map((vi) => {
            const r = sorted[vi.index]
            const key = rowKey(r, vi.index)
            const Tag = href ? 'a' : 'div'
            return (
              <Tag
                key={key}
                href={href ? href(r) : undefined}
                onClick={(e: React.MouseEvent) => {
                  if (!clickable || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return
                  e.preventDefault()
                  setCursor(vi.index)
                  open(r)
                }}
                onMouseEnter={onHover ? () => onHover(r) : undefined}
                className={clsx(
                  'absolute inset-x-0 grid items-center gap-3 border-b border-line px-3 text-sm transition-colors',
                  clickable && 'cursor-pointer hover:bg-panel-hover',
                  (vi.index === cursor || (selectedKey != null && selectedKey === key)) && 'bg-accent-wash! shadow-[inset_2px_0_0_var(--accent)]',
                  rowClassName?.(r),
                )}
                style={{ gridTemplateColumns: template, height: vi.size, transform: `translateY(${vi.start}px)` }}
              >
                {columns.map((c) => (
                  <div
                    key={c.key}
                    className={clsx('min-w-0 truncate', c.align === 'right' && 'tnum text-right', facets && c.facet && 'group/cell relative', c.className)}
                  >
                    {c.render(r)}
                    {facets && c.facet && <CellFilter fc={facets} facetKey={c.facet} row={r} />}
                  </div>
                ))}
              </Tag>
            )
          })}
        </div>
      )}
    </div>
  )
}
