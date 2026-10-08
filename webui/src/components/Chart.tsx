import clsx from 'clsx'
import { useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from 'react'
import uPlot from 'uplot'
import { num, type Rec } from '../lib/api'
import { fmtTime } from '../lib/format'

// ── theme awareness ──────────────────────────────────────────────────────────
// Canvas charts can't use CSS variables directly; read them and redraw on change.

let themeVersion = 0
const themeListeners = new Set<() => void>()
function bumpTheme() {
  themeVersion++
  themeListeners.forEach((l) => l())
}
if (typeof window !== 'undefined') {
  new MutationObserver(bumpTheme).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
  window.matchMedia('(prefers-color-scheme: light)').addEventListener('change', bumpTheme)
}
export function useThemeVersion() {
  return useSyncExternalStore(
    (cb) => {
      themeListeners.add(cb)
      return () => themeListeners.delete(cb)
    },
    () => themeVersion,
  )
}
export function cssVar(name: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

export const SERIES = ['--s1', '--s2', '--s3', '--s4', '--s5', '--s6', '--s7', '--s8']

// ── DQL timeseries helpers ─────────────────────────────────────────────────

/** x axis (unix seconds) for a timeseries record. */
export function tsAxis(rec: Rec | undefined, field: string): number[] {
  if (!rec) return []
  const start = Date.parse(rec.timeframe?.start) / 1000
  const step = num(rec.interval) / 1e9
  const n = Array.isArray(rec[field]) ? rec[field].length : 0
  return Array.from({ length: n }, (_, i) => start + i * step)
}

// ── line / area chart ───────────────────────────────────────────────────────

export interface Series {
  label: string
  values: (number | null)[]
  /** CSS var name, e.g. '--s1' or '--crit' */
  color?: string
  fill?: boolean
}

export interface Marker {
  t: number // unix seconds
  label: string
}

export function TimeChart({
  x,
  series,
  height = 160,
  format = (v: number) => String(v),
  markers,
  band,
  syncKey,
  onZoom,
  className,
  yMin,
  yMax,
}: {
  x: number[]
  series: Series[]
  height?: number
  format?: (v: number) => string
  markers?: Marker[]
  band?: [number, number]
  syncKey?: string
  onZoom?: (from: Date, to: Date) => void
  className?: string
  yMin?: number
  yMax?: number
}) {
  const wrap = useRef<HTMLDivElement>(null)
  const tip = useRef<HTMLDivElement>(null)
  const plot = useRef<uPlot | null>(null)
  const theme = useThemeVersion()
  const [width, setWidth] = useState(0)
  const cbs = useRef({ format, onZoom, markers, band })
  cbs.current = { format, onZoom, markers, band }

  useEffect(() => {
    const el = wrap.current
    if (!el) return
    const ro = new ResizeObserver(([e]) => setWidth(Math.floor(e.contentRect.width)))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  const data = useMemo(() => [x, ...series.map((s) => s.values)] as uPlot.AlignedData, [x, series])

  useEffect(() => {
    if (!wrap.current || width === 0) return
    const ink3 = cssVar('--ink-3')
    const grid = cssVar('--grid')
    const surface = cssVar('--panel')
    const resolve = (c?: string, i = 0) => cssVar(c ?? SERIES[i % SERIES.length])

    const opts: uPlot.Options = {
      width,
      height,
      padding: [8, 8, 0, 0],
      legend: { show: false },
      cursor: {
        sync: syncKey ? { key: syncKey } : undefined,
        drag: { x: !!onZoom, y: false, setScale: false },
        points: { size: 7, width: 2, stroke: () => surface, fill: (u, si) => (u.series[si].stroke as () => string)() },
      },
      select: { show: !!onZoom, left: 0, top: 0, width: 0, height: 0 },
      scales: {
        x: { time: true },
        y: { range: (_u, lo, hi) => [yMin ?? Math.min(0, lo), yMax ?? (hi <= 0 ? 1 : hi * 1.08)] },
      },
      axes: [
        {
          stroke: ink3,
          grid: { show: false },
          ticks: { show: false },
          font: '11px Inter Variable',
          size: 24,
          space: 80,
          values: (u, splits) => {
            const range = (u.scales.x.max ?? 0) - (u.scales.x.min ?? 0)
            return splits.map((s) => fmtAxisTime(s, range))
          },
        },
        {
          stroke: ink3,
          grid: { stroke: grid, width: 1 },
          ticks: { show: false },
          font: '11px Inter Variable',
          size: 52,
          space: 28,
          values: (_u, vals) => vals.map((v) => cbs.current.format(v)),
        },
      ],
      series: [
        {},
        ...series.map((s, i) => {
          const c = resolve(s.color, i)
          return {
            label: s.label,
            stroke: () => c,
            width: 2,
            fill: s.fill === false ? undefined : c + '1a',
            points: { show: false },
            spanGaps: false,
          } as uPlot.Series
        }),
      ],
      hooks: {
        draw: [
          (u) => {
            const ctx = u.ctx
            const { band, markers } = cbs.current
            ctx.save()
            if (band) {
              const x0 = u.valToPos(band[0], 'x', true)
              const x1 = u.valToPos(band[1], 'x', true)
              ctx.fillStyle = cssVar('--crit-wash')
              ctx.fillRect(x0, u.bbox.top, Math.max(2, x1 - x0), u.bbox.height)
            }
            if (markers) {
              const accent = cssVar('--accent')
              ctx.strokeStyle = accent + '55'
              ctx.fillStyle = accent
              ctx.lineWidth = 1
              for (const m of markers) {
                const px = u.valToPos(m.t, 'x', true)
                if (px < u.bbox.left || px > u.bbox.left + u.bbox.width) continue
                ctx.setLineDash([3, 3])
                ctx.beginPath()
                ctx.moveTo(px, u.bbox.top)
                ctx.lineTo(px, u.bbox.top + u.bbox.height)
                ctx.stroke()
                ctx.setLineDash([])
                ctx.beginPath()
                ctx.moveTo(px - 4 * devicePixelRatio, u.bbox.top)
                ctx.lineTo(px + 4 * devicePixelRatio, u.bbox.top)
                ctx.lineTo(px, u.bbox.top + 5 * devicePixelRatio)
                ctx.fill()
              }
            }
            ctx.restore()
          },
        ],
        setCursor: [
          (u) => {
            const t = tip.current
            if (!t) return
            const idx = u.cursor.idx
            if (idx == null || u.cursor.left == null || u.cursor.left < 0) {
              t.style.display = 'none'
              return
            }
            const ts = u.data[0][idx]
            const near = (cbs.current.markers ?? []).filter((m) => Math.abs(m.t - ts) <= (u.data[0][1] - u.data[0][0] || 60) / 2)
            const rows = series
              .map((s, i) => {
                const v = u.data[i + 1][idx]
                return `<div style="display:flex;align-items:center;gap:6px;justify-content:space-between"><span style="display:flex;align-items:center;gap:6px;color:var(--ink-2)"><i style="width:8px;height:2px;border-radius:1px;background:${resolve(s.color, i)}"></i>${escapeHtml(s.label)}</span><b class="tnum" style="font-weight:600;color:var(--ink)">${v == null ? '—' : escapeHtml(cbs.current.format(v as number))}</b></div>`
              })
              .join('')
            const mk = near.map((m) => `<div style="color:var(--accent-ink);margin-top:4px">◆ ${escapeHtml(m.label)}</div>`).join('')
            t.innerHTML = `<div style="color:var(--ink-3);margin-bottom:4px">${fmtTime(new Date(ts * 1000)).slice(0, 8)}</div>${rows}${mk}`
            t.style.display = 'block'
            const w = t.offsetWidth
            const left = u.cursor.left + u.bbox.left / devicePixelRatio
            t.style.left = `${left + 14 + w > width ? left - w - 14 : left + 14}px`
            t.style.top = `8px`
          },
        ],
        setSelect: [
          (u) => {
            if (!cbs.current.onZoom || u.select.width < 4) return
            const a = u.posToVal(u.select.left, 'x')
            const b = u.posToVal(u.select.left + u.select.width, 'x')
            u.setSelect({ left: 0, top: 0, width: 0, height: 0 }, false)
            cbs.current.onZoom(new Date(a * 1000), new Date(b * 1000))
          },
        ],
      },
    }
    plot.current?.destroy()
    plot.current = new uPlot(opts, data, wrap.current)
    const over = wrap.current.querySelector('.u-over')
    const hide = () => tip.current && (tip.current.style.display = 'none')
    over?.addEventListener('mouseleave', hide)
    return () => {
      over?.removeEventListener('mouseleave', hide)
      plot.current?.destroy()
      plot.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [width, height, theme, series.length, syncKey, yMin, yMax, series.map((s) => s.color + s.label).join()])

  useEffect(() => {
    plot.current?.setData(data)
  }, [data])

  // Markers/bands often arrive after the data (separate queries): redraw.
  useEffect(() => {
    plot.current?.redraw(false)
  }, [markers, band])

  return (
    <div className={clsx('relative', className)} style={{ height }}>
      <div ref={wrap} className="absolute inset-0" />
      <div
        ref={tip}
        className="pointer-events-none absolute z-10 hidden min-w-36 rounded-md bg-raised px-2.5 py-2 text-xs shadow-pop"
      />
    </div>
  )
}

/** One-line axis labels: 14:05 · Oct 8 14:00 · Oct 8 */
export function fmtAxisTime(sec: number, rangeSec: number) {
  const d = new Date(sec * 1000)
  const hm = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', hour12: false })
  if (rangeSec <= 26 * 3600) return hm
  const md = d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
  return rangeSec <= 4 * 86400 ? `${md} ${hm}` : md
}

function escapeHtml(s: string) {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!)
}

/** The one chart legend: square swatches for areas/bars, line swatches for line charts. */
export function Legend({
  items,
  className,
  swatch = 'square',
  children,
}: {
  items: { label: string; color: string }[]
  className?: string
  swatch?: 'square' | 'line'
  children?: ReactNode
}) {
  return (
    <div className={clsx('flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-ink-2', className)}>
      {items.map((it) => (
        <span key={it.label} className="inline-flex items-center gap-1.5">
          <i className={clsx('inline-block', swatch === 'line' ? 'h-0.5 w-3 rounded' : 'h-2 w-2 rounded-[2px]')} style={{ background: `var(${it.color})` }} />
          {it.label}
        </span>
      ))}
      {children}
    </div>
  )
}

// ── stacked bars (SVG) ──────────────────────────────────────────────────────

export interface Stack {
  label: string
  values: number[]
  color: string // css var
}

/**
 * Stacked columns with 2px surface gaps and rounded data-ends, hover
 * tooltip, and click-to-zoom into a bucket.
 */
export function StackedBars({
  x,
  stacks,
  height = 120,
  onPick,
  format = (v: number) => String(v),
  className,
}: {
  x: number[]
  stacks: Stack[]
  height?: number
  onPick?: (from: Date, to: Date) => void
  format?: (v: number) => string
  className?: string
}) {
  const ref = useRef<HTMLDivElement>(null)
  const [w, setW] = useState(0)
  const [hover, setHover] = useState<number | null>(null)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const ro = new ResizeObserver(([e]) => setW(Math.floor(e.contentRect.width)))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  const n = x.length
  const totals = Array.from({ length: n }, (_, i) => stacks.reduce((a, s) => a + (s.values[i] || 0), 0))
  const max = Math.max(1, ...totals)
  const axisH = 18
  const plotH = height - axisH
  const slot = n ? w / n : 0
  const bw = Math.max(1, Math.min(24, slot - 2))
  const step = n > 1 ? x[1] - x[0] : 60
  const labelEvery = Math.max(1, Math.ceil(70 / Math.max(slot, 1)))

  return (
    <div ref={ref} className={clsx('relative select-none', className)} style={{ height }} onMouseLeave={() => setHover(null)}>
      {w > 0 && (
        <svg width={w} height={height} className="block">
          {[0.5, 1].map((f) => (
            <line key={f} x1={0} x2={w} y1={plotH - plotH * f + 0.5} y2={plotH - plotH * f + 0.5} stroke="var(--grid)" />
          ))}
          {x.map((_, i) => {
            let y = plotH
            const cx = i * slot + (slot - bw) / 2
            const segs = stacks
              .map((s) => ({ s, v: s.values[i] || 0 }))
              .filter((g) => g.v > 0)
            return (
              <g
                key={i}
                onMouseEnter={() => setHover(i)}
                onClick={() => onPick?.(new Date(x[i] * 1000), new Date((x[i] + step) * 1000))}
                className={onPick ? 'cursor-zoom-in' : undefined}
              >
                <rect x={i * slot} y={0} width={slot} height={plotH} fill={hover === i ? 'var(--line)' : 'transparent'} />
                {segs.map((g, j) => {
                  const h = Math.max(1, (g.v / max) * plotH)
                  const top = y - h
                  const last = j === segs.length - 1
                  const r = last ? Math.min(3, bw / 2, h) : 0
                  const gap = j > 0 ? 2 : 0
                  y = top
                  const hh = Math.max(1, h - gap)
                  return (
                    <path
                      key={g.s.label}
                      d={roundedTop(cx, top, bw, hh, r)}
                      fill={`var(${g.s.color})`}
                    />
                  )
                })}
              </g>
            )
          })}
          {x.map((t, i) =>
            i % labelEvery === 0 ? (
              <text key={i} x={i * slot + slot / 2} y={height - 4} textAnchor="middle" fontSize={11} fill="var(--ink-3)">
                {fmtAxisTime(t, (x[x.length - 1] ?? t) - (x[0] ?? t))}
              </text>
            ) : null,
          )}
        </svg>
      )}
      {hover != null && x[hover] != null && (
        <div
          className="pointer-events-none absolute top-1 z-10 min-w-36 rounded-md bg-raised px-2.5 py-2 text-xs shadow-pop"
          style={hover * slot > w - 170 ? { right: w - hover * slot + 8 } : { left: hover * slot + slot + 8 }}
        >
          <div className="mb-1 text-ink-3">
            {fmtTime(new Date(x[hover] * 1000)).slice(0, 5)} – {fmtTime(new Date((x[hover] + step) * 1000)).slice(0, 5)}
          </div>
          {stacks.map((s) => (
            <div key={s.label} className="flex items-center justify-between gap-4">
              <span className="flex items-center gap-1.5 text-ink-2">
                <i className="inline-block size-2 rounded-[2px]" style={{ background: `var(${s.color})` }} />
                {s.label}
              </span>
              <b className="tnum font-semibold">{format(s.values[hover] || 0)}</b>
            </div>
          ))}
          {onPick && <div className="mt-1 text-2xs text-ink-4">click to zoom in</div>}
        </div>
      )}
    </div>
  )
}

function roundedTop(x: number, y: number, w: number, h: number, r: number) {
  if (r <= 0) return `M${x},${y}h${w}v${h}h${-w}Z`
  return `M${x},${y + h}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + h}Z`
}
