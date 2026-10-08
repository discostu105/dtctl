import clsx from 'clsx'
import { useEffect, useMemo, useRef, useState } from 'react'
import type { DqlResult } from '../lib/api'
import { barsModel, timeseriesModel, type BarsModel, type ChartField, type TimeseriesModel } from '../lib/chart'
import { fmtCompact, fmtUnit } from '../lib/format'
import { SERIES, TimeChart } from './Chart'
import { Empty, Segmented } from './ui'

// The Query page's chart of an arbitrary result. lib/chart.ts decides what is
// plotted; this only lays it out: one value field at a time (switchable), the
// largest series in color, a legend that hovers, hides and solos.

const TOP = SERIES.length

export function chartModel(result: DqlResult | undefined, query: string): TimeseriesModel | BarsModel | null {
  if (!result?.records?.length) return null
  return (
    timeseriesModel(result.records, { types: result.types, metrics: result.meta?.metrics, query }) ??
    barsModel(result.records, { types: result.types, query })
  )
}

export function ResultChart({ model }: { model: TimeseriesModel | BarsModel | null }) {
  const fields = model?.fields ?? []
  const fieldKey = fields.map((f) => f.field).join('\n')
  const [picked, setPicked] = useState<string | null>(null)
  // a new result keeps the chosen field when it still has one
  const field = fields.find((f) => f.field === picked) ?? (model ? firstWithData(model) : undefined)
  useEffect(() => {
    if (picked && !fieldKey.split('\n').includes(picked)) setPicked(null)
  }, [fieldKey, picked])

  if (!model || !field) return <Empty title="Nothing to chart" hint="Charts need a timeseries result, or numbers by a dimension. Switch to the table." />

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2 p-3 pb-2">
      {fields.length > 1 && (
        <div className="flex items-center gap-2">
          <Segmented
            value={field.field}
            onChange={setPicked}
            options={fields.map((f) => ({ value: f.field, label: <FieldLabel f={f} /> }))}
            className="max-w-full overflow-x-auto"
          />
          <span className="text-2xs text-ink-4">one field at a time: each has its own unit</span>
        </div>
      )}
      {model.kind === 'timeseries' ? <SeriesChart key={field.field} model={model} field={field} /> : <Bars model={model} field={field} />}
    </div>
  )
}

function firstWithData(m: TimeseriesModel | BarsModel): ChartField | undefined {
  return m.fields.find((f) => (m.kind === 'timeseries' ? m.series(f.field).series.length : m.bars(f.field).length)) ?? m.fields[0]
}

function FieldLabel({ f }: { f: ChartField }) {
  const unit = f.unit && f.unit !== 'count' ? f.unit : ''
  return (
    <span title={f.name} className="inline-flex items-baseline gap-1">
      <span className="font-mono">{f.field}</span>
      {unit && <span className="text-2xs text-ink-4">{unit}</span>}
    </span>
  )
}

/** The height left for the canvas in a flex column. */
function useHeight<T extends HTMLElement>() {
  const ref = useRef<T>(null)
  const [h, setH] = useState(0)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const ro = new ResizeObserver(([e]) => setH(Math.floor(e.contentRect.height)))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  return [ref, h] as const
}

function SeriesChart({ model, field }: { model: TimeseriesModel; field: ChartField }) {
  const { series: all, stat } = model.series(field.field)
  const fmt = fmtUnit(field.unit)
  const top = all.slice(0, TOP)
  const [hidden, setHidden] = useState<Set<string>>(new Set())
  const [focus, setFocus] = useState<number | null>(null)
  const [box, height] = useHeight<HTMLDivElement>()
  const series = useMemo(
    () => top.map((s, i) => ({ label: s.label, values: s.values, color: SERIES[i], hidden: hidden.has(s.key) })),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [all, hidden],
  )

  const toggle = (key: string) => setHidden((h) => (h.has(key) ? new Set([...h].filter((k) => k !== key)) : new Set([...h, key])))
  const solo = (key: string) => setHidden((h) => (h.size === top.length - 1 && !h.has(key) ? new Set() : new Set(top.map((s) => s.key).filter((k) => k !== key))))

  if (!all.length) return <Empty title={`No values for ${field.field}`} hint="Every series of this field is empty in the timeframe." />

  return (
    <>
      <div ref={box} className="min-h-40 flex-1">
        {height > 0 && <TimeChart x={model.x} series={series} height={height} format={fmt} focus={focus} />}
      </div>
      {(top.length > 1 || model.dims.length > 0) && (
        <div className="max-h-[30%] min-h-0 shrink-0 overflow-auto pl-1">
          <div className="grid grid-cols-[repeat(auto-fill,minmax(280px,1fr))] gap-x-4" onMouseLeave={() => setFocus(null)}>
            {top.map((s, i) => (
              <button
                key={s.key}
                type="button"
                onMouseEnter={() => !hidden.has(s.key) && setFocus(i)}
                onClick={() => toggle(s.key)}
                onDoubleClick={() => solo(s.key)}
                aria-pressed={!hidden.has(s.key)}
                title={`${s.label}\nclick: hide or show · double-click: only this`}
                className={clsx('flex min-w-0 items-center gap-2 rounded px-1.5 py-0.5 text-left text-xs hover:bg-panel-hover', hidden.has(s.key) && 'opacity-40')}
              >
                <i className="h-0.5 w-3 shrink-0 rounded" style={{ background: `var(${SERIES[i]})` }} />
                <span className="min-w-0 flex-1 truncate text-ink-2">{s.label}</span>
                <span className="tnum shrink-0 text-ink">{fmt(s.stat)}</span>
              </button>
            ))}
          </div>
          <div className="mt-1 px-1.5 text-2xs text-ink-4">
            {all.length > top.length ? `Top ${top.length} of ${all.length.toLocaleString('en-US')} series by ${stat}; the table has every one.` : `${all.length} series, by ${stat}.`}
            {model.dims.length > 0 && <> Named by {model.dims.map((d) => (d.alias ? `${d.alias} (else ${d.field})` : d.field)).join(', ')}.</>}
          </div>
        </div>
      )}
    </>
  )
}

const BARS = 30

function Bars({ model, field }: { model: BarsModel; field: ChartField }) {
  const all = model.bars(field.field)
  const fmt = field.unit ? fmtUnit(field.unit) : fmtCompact
  const bars = all.slice(0, BARS)
  const max = Math.max(0, ...bars.map((b) => b.value))
  const min = Math.min(0, ...bars.map((b) => b.value))
  const span = max - min || 1
  if (!bars.length) return <Empty title={`No values for ${field.field}`} />
  return (
    <div className="min-h-0 flex-1 overflow-auto">
      <div className="grid grid-cols-[minmax(120px,max-content)_1fr_auto] items-center gap-x-3 gap-y-1 text-xs">
        {bars.map((b) => (
          <div key={b.key} className="contents">
            <span className="max-w-80 truncate text-ink-2" title={b.label}>
              {b.label}
            </span>
            <div className="relative h-4">
              <div
                className="absolute inset-y-0 rounded-[3px] bg-[var(--s1)]"
                style={{ left: `${((Math.min(0, b.value) - min) / span) * 100}%`, width: `max(2px, ${(Math.abs(b.value) / span) * 100}%)` }}
              />
            </div>
            <span className="tnum text-right text-ink">{fmt(b.value)}</span>
          </div>
        ))}
      </div>
      {all.length > bars.length && <div className="mt-2 text-2xs text-ink-4">Top {bars.length} of {all.length.toLocaleString('en-US')}; the table has every row.</div>}
    </div>
  )
}
