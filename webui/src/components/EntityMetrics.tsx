import type { UseQueryResult } from '@tanstack/react-query'
import { Activity } from 'lucide-react'
import { useMemo, useState } from 'react'
import { arr, useDql, type DqlResult } from '../lib/api'
import { smartscapeField, q, type Entity } from '../lib/dql'
import { discoveredVital, splitMetric } from '../lib/metrics'
import { tfSpec } from '../lib/shared'
import { absolute, intervalFor, setTimeframe, useTimeframe } from '../lib/timeframe'
import { fmtUnit } from '../pages/Entity'
import { TimeChart, tsAxis } from './Chart'
import { FilterInput, Panel } from './Panel'
import { Empty, ErrorBox, Skeleton } from './ui'

const CHUNK = 8

/**
 * Every metric that carries this entity's Smartscape dimension, grouped by
 * namespace, as small synced charts. Several metrics share one timeseries
 * query, so even 40 charts cost a handful of Grail queries.
 */
export function EntityMetrics({ entity, discovery }: { entity: Entity; discovery: UseQueryResult<DqlResult, Error> }) {
  const tf = useTimeframe()
  const [filter, setFilter] = useState('')
  const keys: string[] = useMemo(() => (discovery.data?.records ?? []).map((r) => r['metric.key']), [discovery.data])
  const shown = keys.filter((k) => !filter || k.toLowerCase().includes(filter.toLowerCase()) || discoveredVital(k).title.toLowerCase().includes(filter.toLowerCase()))

  // chunked queries over the full key list (stable regardless of the filter)
  const chunks = useMemo(() => {
    const out: string[][] = []
    for (let i = 0; i < keys.length; i += CHUNK) out.push(keys.slice(i, i + CHUNK))
    return out
  }, [keys])
  const interval = intervalFor(tf.ms)
  const filterExpr = `${smartscapeField(entity.type)} == toSmartscapeId(${q(entity.id)})`

  // Namespaces with a few metrics get a section; singletons pool into one grid
  // (labelled per chart) so the page has no half-empty rows.
  const groups = useMemo(() => {
    const m = new Map<string, string[]>()
    for (const k of shown) {
      const g = splitMetric(k).group || 'other'
      m.set(g, [...(m.get(g) ?? []), k])
    }
    const big = [...m].filter(([, ks]) => ks.length >= 3)
    const small = [...m].filter(([, ks]) => ks.length < 3).flatMap(([, ks]) => ks)
    return small.length ? [...big, [big.length ? 'more' : '', small] as [string, string[]]] : big
  }, [shown])

  if (discovery.error) return <ErrorBox error={discovery.error} />
  if (discovery.isLoading) return <Skeleton className="m-4 h-40" />
  if (!keys.length)
    return (
      <Empty
        icon={<Activity className="size-5" />}
        title="No metrics for this entity"
        hint={
          <>
            No metric series carries <code className="font-mono">{smartscapeField(entity.type)}</code> = this entity.
            {entity.type === 'AWS_S3_BUCKET' && (
              <> S3 storage metrics (bucket size, object count) are daily CloudWatch metrics; enable the S3 namespace in the AWS connection's metric settings to see them here.</>
            )}
            {entity.type.startsWith('AWS_') && entity.type !== 'AWS_S3_BUCKET' && <> Check which CloudWatch namespaces the AWS connection collects.</>}
          </>
        }
        className="py-16"
      />
    )

  return (
    <div className="p-4">
      <div className="mb-3 flex items-center gap-3">
        <div className="text-sm text-ink-2">
          <b className="font-semibold text-ink">{keys.length}</b> metrics found by <code className="font-mono text-xs text-ink-3">{smartscapeField(entity.type)}</code>
        </div>
        {keys.length > 6 && <FilterInput value={filter} onChange={setFilter} placeholder="Filter metrics…" className="ml-auto w-60" />}
      </div>
      {groups.map(([g, ks]) => (
        <section key={g} className="mb-5">
          {g && <h3 className="mb-2 font-mono text-2xs tracking-wide text-ink-3">{g}</h3>}
          <div className="grid grid-cols-3 gap-3 max-2xl:grid-cols-2 max-lg:grid-cols-1">
            {ks.map((k) => {
              const ci = Math.floor(keys.indexOf(k) / CHUNK)
              return <MetricChart key={k} metric={k} chunk={chunks[ci]} interval={interval} filterExpr={filterExpr} showGroup={g === 'more' || g === ''} />
            })}
          </div>
        </section>
      ))}
      {!shown.length && <Empty title="No metric matches" className="py-8" />}
    </div>
  )
}

function chunkQuery(chunk: string[], interval: string, filterExpr: string) {
  const parts = chunk.map((k, i) => {
    const v = discoveredVital(k)
    return `m${i} = ${v.agg}(${k})`
  })
  return `timeseries { ${parts.join(', ')} }, interval:${interval}, filter:{ ${filterExpr} }`
}

function MetricChart({
  metric,
  chunk,
  interval,
  filterExpr,
  showGroup,
}: {
  metric: string
  chunk: string[]
  interval: string
  filterExpr: string
  showGroup?: boolean
}) {
  const tf = useTimeframe()
  const spec = tfSpec(tf, chunkQuery(chunk, interval, filterExpr), { ttl: 60 })
  const res = useDql(spec)
  const v = discoveredVital(metric)
  const field = `m${chunk.indexOf(metric)}`
  const r = res.data?.records[0]
  const vals: (number | null)[] = r ? arr(r[field]) : []
  const nums = vals.filter((x): x is number => x != null)
  const fmt = fmtUnit(v.unit)
  const last = nums.length ? nums[nums.length - 1] : NaN
  const peak = nums.length ? Math.max(...nums) : NaN
  return (
    <Panel
      title={
        <span title={metric} className="flex min-w-0 items-baseline gap-2">
          <span className="truncate">{v.title}</span>
          {showGroup && <span className="truncate font-mono text-2xs font-normal text-ink-4">{splitMetric(metric).group}</span>}
        </span>
      }
      spec={spec}
      result={res}
      actions={
        nums.length > 0 && (
          <span className="tnum mr-1 text-2xs text-ink-3">
            <b className="font-semibold text-ink">{fmt(last)}</b> · peak {fmt(peak)}
          </span>
        )
      }
    >
      {res.error ? (
        <ErrorBox error={res.error} />
      ) : res.isLoading ? (
        <Skeleton className="m-3 h-[84px]" />
      ) : !nums.length ? (
        <div className="grid h-[100px] place-items-center text-xs text-ink-4">no data in this timeframe</div>
      ) : (
        <div className="p-2 pl-0">
          <TimeChart
            x={tsAxis(r, field)}
            series={[{ label: v.title, values: vals, color: /error|fail|5xx|reject/i.test(metric) ? '--s8' : '--s1' }]}
            height={84}
            format={fmt}
            syncKey="metrics"
            yMax={v.unit === '%' ? 100 : undefined}
            onZoom={(a, b) => setTimeframe(absolute(a, b))}
          />
        </div>
      )}
    </Panel>
  )
}
