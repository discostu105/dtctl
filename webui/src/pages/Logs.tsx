import clsx from 'clsx'
import { ScrollText, Search, X } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useLocation, useSearch } from 'wouter'
import { StackedBars, tsAxis } from '../components/Chart'
import { QueryInfo, queryHref } from '../components/Panel'
import { LogDetail, LogStream, SidePanel } from '../components/signals'
import { ErrorBox, Kbd, Skeleton, Tip } from '../components/ui'
import { num, useDql } from '../lib/api'
import { facetQuery, logHistogramQuery, logsQuery, q, searchFilter } from '../lib/dql'
import { fmtCompact, fmtInt } from '../lib/format'
import { ERROR_LEVELS, tfSpec } from '../lib/shared'
import { useAdaptiveDql } from '../lib/sampling'
import { SampledBadge } from '../components/Sampled'
import { useTitle } from '../lib/store'
import { absolute, intervalFor, setTimeframe, useTimeframe } from '../lib/timeframe'

const FACETS: { key: string; label: string; field: string }[] = [
  { key: 'loglevel', label: 'Level', field: 'loglevel' },
  { key: 'ns', label: 'Namespace', field: 'k8s.namespace.name' },
  { key: 'svc', label: 'Service', field: 'service.name' },
  { key: 'src', label: 'Source', field: 'log.source' },
  { key: 'host', label: 'Host', field: 'host.name' },
]

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

  // Facet selections + search live in the URL: every view is shareable.
  const facetSel = useMemo(() => {
    const m: Record<string, string[]> = {}
    for (const f of FACETS) m[f.key] = params.getAll(f.key)
    return m
  }, [params.toString()]) // eslint-disable-line react-hooks/exhaustive-deps
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

  const filtersExcept = (except?: string) => {
    const fs: string[] = []
    if (raw) fs.push(raw)
    if (level === 'error') fs.push(`in(loglevel, ${ERROR_LEVELS})`)
    if (debounced) fs.push(searchFilter(debounced))
    for (const f of FACETS) {
      if (f.key === except) continue
      const vals = facetSel[f.key]
      if (vals.length) fs.push(vals.length === 1 ? `${f.field} == ${q(vals[0])}` : `in(${f.field}, {${vals.map(q).join(', ')}})`)
    }
    return fs
  }
  const filters = filtersExcept()
  const iv = intervalFor(tf.ms)

  const streamSpec = tfSpec(tf, logsQuery(filters, 1000), { maxRecords: 1000 })
  const histSpec = tfSpec(tf, logHistogramQuery(filters, iv))
  const stream = useDql(streamSpec)
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

  const activeChips = [
    ...(raw ? [{ k: 'f', v: raw, label: 'scope' }] : []),
    ...(level === 'error' ? [{ k: 'level', v: 'error', label: 'errors only' }] : []),
    ...FACETS.flatMap((f) => facetSel[f.key].map((v) => ({ k: f.key, v, label: `${f.label}: ${v}` }))),
  ]

  return (
    <div className="flex h-full">
      <aside className="w-64 shrink-0 overflow-auto border-r border-line p-3 max-lg:hidden">
        <div className="mb-3 flex items-center gap-2 text-sm font-medium">
          <ScrollText className="size-4 text-ink-3" /> Facets
        </div>
        {FACETS.map((f) => (
          <Facet
            key={f.key}
            label={f.label}
            spec={tfSpec(tf, facetQuery(filtersExcept(f.key), f.field, 10), { ttl: 30 })}
            selected={facetSel[f.key]}
            onToggle={(v) =>
              update((p) => {
                const cur = p.getAll(f.key)
                p.delete(f.key)
                for (const x of cur.includes(v) ? cur.filter((c) => c !== v) : [...cur, v]) p.append(f.key, x)
              })
            }
          />
        ))}
      </aside>

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
                  if (e.key === 'Escape') (e.target as HTMLInputElement).blur()
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
          {activeChips.length > 0 && (
            <div className="flex flex-wrap items-center gap-1.5">
              {activeChips.map((c) => (
                <button
                  key={c.k + c.v}
                  type="button"
                  onClick={() =>
                    update((p) => {
                      const rest = p.getAll(c.k).filter((x) => x !== c.v)
                      p.delete(c.k)
                      rest.forEach((x) => p.append(c.k, x))
                    })
                  }
                  className="inline-flex h-6 max-w-80 items-center gap-1 rounded-md bg-accent-wash px-2 text-xs text-accent-ink hover:brightness-110"
                  title={c.v}
                >
                  <span className="truncate">{c.label}</span>
                  <X className="size-3 shrink-0" />
                </button>
              ))}
              <button type="button" onClick={() => navigate('/logs', { replace: true })} className="text-xs text-ink-3 hover:text-ink-2">
                clear all
              </button>
            </div>
          )}
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

        <div className="flex h-8 items-center gap-2 border-b border-line px-3 text-xs text-ink-3">
          {stream.data && (
            <span className="tnum">
              showing {fmtInt(stream.data.records.length)}
              {stream.data.records.length >= 1000 ? ' newest' : ''} records
            </span>
          )}
          <span className="ml-auto" />
          <QueryInfo spec={streamSpec} result={stream} />
        </div>
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
        <SidePanel title="Log record" onClose={() => setSel(null)} width="w-[min(560px,42vw)]">
          <LogDetail rec={sel} />
        </SidePanel>
      )}
    </div>
  )
}

function Facet({ label, spec, selected, onToggle }: { label: string; spec: any; selected: string[]; onToggle: (v: string) => void }) {
  const { res, ratio, waiting } = useAdaptiveDql('logs', spec, ['count'])
  const rows = (res.data?.records ?? []).filter((r) => r.v != null)
  const max = Math.max(1, ...rows.map((r) => num(r.count)))
  return (
    <div className="mb-4">
      <div className="mb-1 flex items-center justify-between text-2xs font-medium tracking-wide text-ink-3 uppercase">
        <span>
          {label}
          {ratio > 1 && (
            <span className="ml-1 font-normal tracking-normal normal-case text-warn" title={`Counts extrapolated from a 1:${ratio} sample`}>
              ≈
            </span>
          )}
        </span>
        {res.isFetching && <span className="size-1.5 animate-pulse rounded-full bg-accent" />}
      </div>
      {res.isLoading || waiting ? (
        <div className="flex flex-col gap-1.5">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-5" />
          ))}
        </div>
      ) : rows.length === 0 ? (
        <div className="text-xs text-ink-4">no values</div>
      ) : (
        rows.map((r) => {
          const v = String(r.v)
          const on = selected.includes(v)
          return (
            <button
              key={v}
              type="button"
              onClick={() => onToggle(v)}
              className={clsx('relative flex h-6 w-full items-center gap-2 overflow-hidden rounded-md px-1.5 text-left text-xs', on ? 'text-accent-ink' : 'text-ink-2 hover:text-ink')}
            >
              <span
                className={clsx('absolute inset-y-0.5 left-0 rounded-[4px]', on ? 'bg-accent-wash' : 'bg-line')}
                style={{ width: `${Math.max(3, (100 * num(r.count)) / max)}%` }}
              />
              <span className="relative min-w-0 flex-1 truncate" title={v}>
                {v}
              </span>
              <span className="tnum relative text-ink-3">
                {ratio > 1 ? '≈' : ''}
                {fmtCompact(num(r.count))}
              </span>
            </button>
          )
        })
      )}
    </div>
  )
}
