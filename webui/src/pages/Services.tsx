import clsx from 'clsx'
import { Share2 } from 'lucide-react'
import { useMemo, useState } from 'react'
import { DataTable, type Column } from '../components/DataTable'
import { EntityLink } from '../components/Entity'
import { FacetSearch, FacetSummary, useAttrs, useFacets } from '../components/Facets'
import { PageHeader, Panel } from '../components/Panel'
import { Spark } from '../components/Spark'
import { Badge, Empty, ErrorBox, Segmented } from '../components/ui'
import { num, prefetchDql, useDql, type Rec } from '../lib/api'
import { attrCondition, type AttrSource } from '../lib/attrs'
import { detailQuery, serviceListQuery, SERVICES_LIST_LIMIT, SERVICES_RED_LIMIT } from '../lib/dql'
import { bucket, type Facet } from '../lib/facets'
import { fmtCompact, fmtPct, fmtUs } from '../lib/format'
import { entityHref } from '../lib/links'
import { servicesSpec } from '../lib/shared'
import { useTitle } from '../lib/store'
import { useTimeframe } from '../lib/timeframe'

const kindLabel = (k: unknown) => (k ? String(k).replace(/_SERVICE$/, '').replace(/_/g, ' ').toLowerCase() : null)

const HEALTH = ['Failing ≥ 5%', 'Some failures', 'Healthy', 'No traffic']
const LATENCY = ['≥ 1 s', '300 ms – 1 s', '100–300 ms', '< 100 ms']

const FACETS: Facet<Rec>[] = [
  {
    key: 'health',
    label: 'Health',
    value: (r) => (!r.total ? 'No traffic' : r.rate >= 5 ? 'Failing ≥ 5%' : r.rate > 0 ? 'Some failures' : 'Healthy'),
    order: HEALTH,
  },
  { key: 'ns', label: 'Namespace', value: (r) => r.ns, aliases: ['namespace'] },
  { key: 'type', label: 'Type', value: (r) => kindLabel(r.kind), aliases: ['kind'] },
  {
    key: 'latency',
    label: 'Latency',
    value: (r) => bucket(r.total ? r.latency / 1000 : null, [[1000, LATENCY[0]], [300, LATENCY[1]], [100, LATENCY[2]]], LATENCY[3]),
    order: LATENCY,
    aliases: ['rt'],
  },
]

export default function Services() {
  useTitle('Services')
  const tf = useTimeframe()
  const source: AttrSource = useMemo(() => ({ kind: 'service-metrics', head: '', from: tf.from, to: tf.to }), [tf.from, tf.to])
  const attrs = useAttrs(source)
  const filtered = attrs.filters.length > 0
  const redSpec = servicesSpec(tf, attrCondition(attrs.filters))
  const red = useDql(redSpec)
  // Attribute filters match metric dimensions that Smartscape service nodes
  // don't carry, so a filtered list is the metric roster alone.
  const listSpec = filtered ? null : { query: serviceListQuery(), ttl: 120 }
  const list = useDql(listSpec)
  const [lens, setLens] = useState<'active' | 'failing' | 'all'>('active')

  const rows = useMemo(() => {
    const byId = new Map<string, Rec>()
    for (const s of (!filtered && list.data?.records) || []) byId.set(s.id, { id: s.id, name: s.name, ns: s['k8s.namespace.name'], kind: s['dt.service.sdv1_type'] })
    for (const r of red.data?.records ?? []) {
      const id = r['dt.smartscape.service']
      byId.set(id, {
        ...(byId.get(id) ?? { id, name: r['s.name'], ns: r['s.k8s.namespace.name'], kind: r['s.dt.service.sdv1_type'] }),
        req: r.req,
        fail: r.fail,
        rt: r.rt,
        total: num(r.total),
        failed: num(r.failed),
        rate: num(r.failure_rate),
        latency: num(r.latency),
      })
    }
    return [...byId.values()]
  }, [list.data, red.data, filtered])

  const counts = {
    active: rows.filter((r) => r.total > 0).length,
    failing: rows.filter((r) => r.failed > 0).length,
    all: rows.length,
  }
  // The metric roster is capped failing-first: failing services are complete until the cap is all failing.
  const redCapped = (red.data?.records.length ?? 0) >= SERVICES_RED_LIMIT
  const capped = {
    active: redCapped,
    failing: redCapped && counts.failing >= SERVICES_RED_LIMIT,
    all: redCapped || (list.data?.records.length ?? 0) >= SERVICES_LIST_LIMIT,
  }
  const lensRows = useMemo(
    () => (red.data || list.data ? rows.filter((r) => (lens === 'all' ? true : lens === 'failing' ? r.failed > 0 : r.total > 0)) : undefined),
    [rows, lens, red.data, list.data],
  )
  const fc = useFacets(lensRows, FACETS, { text: (r) => `${r.name} ${r.id}`, attrs, capped: capped[lens] })

  const cols: Column[] = [
    {
      key: 'name',
      header: 'Service',
      width: 'minmax(220px,2fr)',
      render: (r) => <EntityLink id={r.id} name={r.name} type="SERVICE" />,
      sort: (r) => String(r.name ?? '').toLowerCase(),
    },
    { key: 'ns', header: 'Namespace', width: 'minmax(100px,1fr)', facet: 'ns', render: (r) => <span className="text-ink-2">{r.ns ?? '—'}</span>, sort: (r) => r.ns },
    {
      key: 'req',
      header: 'Throughput',
      width: '200px',
      align: 'right',
      render: (r) => (
        <span className="flex items-center justify-end gap-3">
          <Spark values={r.req} color="var(--s1)" width={96} />
          <span className="w-14">{r.total != null ? fmtCompact(r.total) : '—'}</span>
        </span>
      ),
      sort: (r) => r.total ?? -1,
    },
    {
      key: 'fail',
      header: 'Failure rate',
      width: '200px',
      facet: 'health',
      align: 'right',
      render: (r) => (
        <span className="flex items-center justify-end gap-3">
          <Spark values={r.fail} color="var(--crit)" width={96} kind="bars" />
          <span className={clsx('w-14', r.rate >= 5 ? 'text-crit' : r.rate > 0 ? 'text-warn' : 'text-ink-3')}>{r.total ? fmtPct(r.rate, 2) : '—'}</span>
        </span>
      ),
      sort: (r) => r.rate ?? -1,
    },
    {
      key: 'rt',
      header: 'Latency (avg)',
      width: '200px',
      facet: 'latency',
      align: 'right',
      render: (r) => (
        <span className="flex items-center justify-end gap-3">
          <Spark values={r.rt} color="var(--s7)" width={96} zeroBased={false} />
          <span className="w-16">{fmtUs(r.latency)}</span>
        </span>
      ),
      sort: (r) => r.latency ?? -1,
    },
    {
      key: 'kind',
      header: 'Type',
      width: '130px',
      facet: 'type',
      render: (r) => (r.kind ? <Badge>{kindLabel(r.kind)}</Badge> : null),
      sort: (r) => r.kind,
    },
  ]

  return (
    <div className="flex h-full flex-col p-5">
      <PageHeader
        title="Services"
        icon={<Share2 className="size-5" />}
        sub={`Throughput, failures and latency · ${tf.label.toLowerCase()}`}
        actions={
          <>
            <Segmented
              value={lens}
              onChange={setLens}
              options={[
                { value: 'active', label: 'With traffic', count: counts.active, capped: capped.active },
                { value: 'failing', label: 'Failing', count: counts.failing, capped: capped.failing },
                { value: 'all', label: 'All', count: counts.all, capped: capped.all },
              ]}
            />
            <FacetSearch fc={fc} placeholder="Filter services…" className="w-72" />
          </>
        }
      />
      <Panel spec={redSpec} result={red} className="min-h-0 flex-1" bodyClassName="flex min-h-0 flex-col" head={<FacetSummary fc={fc} noun="services" fetching={red.isFetching} />}>
        {red.error ? (
          <ErrorBox error={red.error} />
        ) : (
          <DataTable
            rows={fc.rows}
            loading={red.isLoading && (filtered || list.isLoading)}
            columns={cols}
            facets={fc}
            empty={<Empty title="No services" hint="No service reported traffic or appeared in Smartscape in this timeframe." />}
            rowKey={(r) => r.id}
            href={(r) => entityHref(r.id, r.name)}
            onHover={(r) => prefetchDql({ query: detailQuery({ id: r.id, type: 'SERVICE' }) })}
            initialSort={{ key: 'fail', dir: 'desc' }}
            rowHeight={40}
            className="flex-1"
            autoFocus
          />
        )}
      </Panel>
    </div>
  )
}
