import clsx from 'clsx'
import { Search, X } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useLocation, useSearch } from 'wouter'
import { StackedBars, tsAxis } from '../components/Chart'
import { QueryInfo, queryHref, QueryWarning } from '../components/Panel'
import { LogDetail, LogStream, SidePanel } from '../components/signals'
import { ErrorBox, Kbd, Skeleton, Tip } from '../components/ui'
import { useDql, type Rec } from '../lib/api'
import { serializeAttr, withAttrs, type AttrSource } from '../lib/attrs'
import { logHistogramQuery, logsQuery, searchFilter } from '../lib/dql'
import type { Facet } from '../lib/facets'
import { FacetSummary, useAttrs, useFacets } from '../components/Facets'
import { fmtCompact, fmtInt } from '../lib/format'
import { ERROR_LEVELS, tfSpec } from '../lib/shared'
import { useAdaptiveDql } from '../lib/sampling'
import { SampledBadge } from '../components/Sampled'
import { useTitle } from '../lib/store'
import { absolute, intervalFor, setTimeframe, useTimeframe } from '../lib/timeframe'

// The everyday log fields, offered first in the filter popup (F).
const SUGGESTED = [
  { field: 'loglevel', label: 'Level' },
  { field: 'k8s.namespace.name', label: 'Namespace' },
  { field: 'service.name', label: 'Service' },
  { field: 'log.source', label: 'Source' },
  { field: 'host.name', label: 'Host' },
  { field: 'k8s.workload.name', label: 'Workload' },
  { field: 'dt.process_group.detected_name', label: 'Process' },
]
// Facet params of the old sidebar, still honored in links: ?ns=prod → ?a=k8s.namespace.name=prod
const LEGACY: Record<string, string> = { loglevel: 'loglevel', ns: 'k8s.namespace.name', svc: 'service.name', src: 'log.source', host: 'host.name' }
const LIMIT = 1000
const NO_FACETS: Facet<Rec>[] = []

export default function Logs() {
  useTitle('Logs')
  const tf = useTimeframe()
  const params = new URLSearchParams(useSearch())
  const [, navigate] = useLocation()
  const [text, setText] = useState(params.get('q') ?? '')
  const [debounced, setDebounced] = useState(text)
  const [sel, setSel] = useState<any>(null)

  useEffect(() => {
    const t = setTimeout(() => setDebounced(text), 350)
    return () => clearTimeout(t)
  }, [text])

  // Filters, search and level live in the URL: every view is shareable.
  const level = params.get('level')
  const raw = params.get('f')

  const update = (mut: (p: URLSearchParams) => void) => {
    const p = new URLSearchParams(location.search)
    mut(p)
    navigate(`/logs?${p}`, { replace: true })
  }
  useEffect(() => {
    if ((params.get('q') ?? '') !== debounced) update((p) => (debounced ? p.set('q', debounced) : p.delete('q')))
  }, [debounced]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!Object.keys(LEGACY).some((k) => params.has(k))) return
    update((p) => {
      for (const [k, field] of Object.entries(LEGACY)) {
        for (const v of p.getAll(k)) p.append('a', serializeAttr({ field, value: v }))
        p.delete(k)
      }
    })
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  // The view's own filters (entity scope, level, search) shape the population
  // the filter popup discovers fields and counts values in.
  const base = [raw ?? '', level === 'error' ? `in(loglevel, ${ERROR_LEVELS})` : '', debounced ? searchFilter(debounced) : ''].filter(Boolean)
  const baseKey = base.join('\n')
  const source: AttrSource = useMemo(
    () => ({ kind: 'logs', head: ['fetch logs', ...base.map((f) => `| filter ${f}`)].join('\n'), from: tf.from, to: tf.to, suggested: SUGGESTED }),
    [baseKey, tf.from, tf.to], // eslint-disable-line react-hooks/exhaustive-deps
  )
  const attrs = useAttrs(source)
  const iv = intervalFor(tf.ms)

  const streamSpec = tfSpec(tf, withAttrs(logsQuery(base, LIMIT), attrs.filters), { maxRecords: LIMIT })
  const histSpec = tfSpec(tf, withAttrs(logHistogramQuery(base, iv), attrs.filters))
  const stream = useDql(streamSpec)
  // no curated client facets: every log filter runs in the query (own params, so ?q= stays the content search)
  const fc = useFacets(stream.data?.records, NO_FACETS, { param: 'lf', attrs, limit: LIMIT })
  // the histogram counts every record in the timeframe: adaptive sampling keeps it under the scan limit
  const histA = useAdaptiveDql('logs', histSpec, ['count'])
  const hist = histA.res

  const histData = useMemo(() => {
    const recs = hist.data?.records ?? []
    if (!recs.length) return null
    const x = tsAxis(recs[0], 'count')
    const by = (l: string) => (recs.find((r) => r.lvl === l)?.count as number[] | undefined) ?? x.map(() => 0)
    const stacks = [
      { label: 'Other', values: by('other'), color: '--s1' },
      { label: 'Warn', values: by('warn'), color: '--warn' },
      { label: 'Error', values: by('error'), color: '--crit' },
    ]
    const total = stacks.reduce((a, s) => a + s.values.reduce((x, y) => x + (y || 0), 0), 0)
    const errors = stacks[2].values.reduce((x, y) => x + (y || 0), 0)
    return { x, stacks, total, errors }
  }, [hist.data])

  return (
    <div className="flex h-full">
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex flex-col gap-2 border-b border-line p-3">
          <div className="flex items-center gap-2">
            <div className="relative flex-1">
              <Search className="absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-ink-3" />
              <input
                data-filter
                value={text}
                onChange={(e) => setText(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') setDebounced(text)
                  if (e.key === 'Escape') {
                    // like every filter field: Esc clears, a second Esc leaves the field
                    if (text) {
                      setText('')
                      setDebounced('')
                    } else (e.target as HTMLInputElement).blur()
                  }
                }}
                placeholder="Search log content…"
                className="h-9 w-full rounded-lg border border-line bg-sunken pr-10 pl-8 font-mono text-sm outline-none placeholder:font-sans placeholder:text-ink-4 focus:border-accent/60"
              />
              <Kbd className="absolute top-1/2 right-2.5 -translate-y-1/2">/</Kbd>
            </div>
            <button
              type="button"
              onClick={() => update((p) => (level === 'error' ? p.delete('level') : p.set('level', 'error')))}
              className={clsx(
                'h-9 rounded-lg border px-3 text-sm transition-colors',
                level === 'error' ? 'border-crit/40 bg-crit-wash text-crit' : 'border-line bg-sunken text-ink-2 hover:text-ink',
              )}
            >
              Errors only
            </button>
            <Tip content="Open this exact query in the Query workbench">
              <button
                type="button"
                onClick={() => navigate(queryHref(streamSpec))}
                className="h-9 rounded-lg border border-line bg-sunken px-3 text-sm text-ink-2 hover:text-ink"
              >
                DQL
              </button>
            </Tip>
          </div>
        </div>

        <div className="border-b border-line px-3 pt-2 pb-1">
          <div className="mb-1 flex items-center gap-3 text-xs text-ink-3">
            {histData ? (
              <>
                <span className="tnum">
                  <b className="font-semibold text-ink">
                    {histA.ratio > 1 ? `≈${fmtCompact(histData.total)}` : fmtInt(histData.total)}
                  </b>{' '}
                  records
                </span>
                <span className="tnum">
                  <b className={clsx('font-semibold', histData.errors ? 'text-crit' : 'text-ink')}>
                    {histA.ratio > 1 ? `≈${fmtCompact(histData.errors)}` : fmtInt(histData.errors)}
                  </b>{' '}
                  errors
                </span>
                <SampledBadge ratio={histA.ratio} sampling={histA.sampling} />
                <span>{tf.label.toLowerCase()} · click a bar to zoom</span>
              </>
            ) : (
              <Skeleton className="h-4 w-48" />
            )}
            <span className="ml-auto" />
            <QueryInfo spec={histA.spec ?? histSpec} result={hist} />
          </div>
          {hist.error ? (
            <ErrorBox error={hist.error} />
          ) : histData ? (
            <StackedBars x={histData.x} stacks={histData.stacks} height={96} format={fmtCompact} onPick={(a, b) => setTimeframe(absolute(a, b))} />
          ) : (
            <Skeleton className="h-[96px]" />
          )}
        </div>

        <div className="flex h-10 items-center gap-2 border-b border-line px-3">
          {raw && (
            <Tip content={<span className="font-mono text-2xs">{raw}</span>}>
              <button
                type="button"
                onClick={() => update((p) => p.delete('f'))}
                className="inline-flex h-6 shrink-0 items-center gap-1 rounded-md bg-accent-wash px-2 text-xs text-accent-ink hover:brightness-110"
              >
                scoped to entity
                <X className="size-3" />
              </button>
            </Tip>
          )}
          <FacetSummary fc={fc} noun="records" fetching={stream.isFetching} />
          <QueryInfo spec={streamSpec} result={stream} />
        </div>
        <QueryWarning result={stream} className="border-b border-warn/30" />
        {stream.error ? (
          <ErrorBox error={stream.error} />
        ) : (
          <LogStream
            records={stream.data?.records}
            loading={stream.isLoading}
            onSelect={setSel}
            selected={sel}
            highlight={debounced}
            className={clsx('flex-1 transition-opacity', stream.isPlaceholderData && 'opacity-60')}
          />
        )}
      </div>

      {sel && (
        <SidePanel title="Log record" onClose={() => setSel(null)}>
          <LogDetail rec={sel} />
        </SidePanel>
      )}
    </div>
  )
}
