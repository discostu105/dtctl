import clsx from 'clsx'
import { Share2 } from 'lucide-react'
import { useMemo, useState } from 'react'
import { DataTable, type Column } from '../components/DataTable'
import { EntityLink } from '../components/Entity'
import { FilterInput, PageHeader, Panel } from '../components/Panel'
import { Spark } from '../components/Spark'
import { Badge, ErrorBox, Segmented } from '../components/ui'
import { num, prefetchDql, useDql, type Rec } from '../lib/api'
import { detailQuery, serviceListQuery } from '../lib/dql'
import { fmtCompact, fmtPct } from '../lib/format'
import { entityHref } from '../lib/links'
import { servicesSpec } from '../lib/shared'
import { useTitle } from '../lib/store'
import { useTimeframe } from '../lib/timeframe'
import { fmtLatencyUs } from './Pulse'

export default function Services() {
  useTitle('Services')
  const tf = useTimeframe()
  const red = useDql(servicesSpec(tf))
  const listSpec = { query: serviceListQuery(), ttl: 120 }
  const list = useDql(listSpec)
  const [lens, setLens] = useState<'active' | 'failing' | 'all'>('active')
  const [filter, setFilter] = useState('')

  const rows = useMemo(() => {
    const byId = new Map<string, Rec>()
    for (const s of list.data?.records ?? []) byId.set(s.id, { id: s.id, name: s.name, ns: s['k8s.namespace.name'], kind: s['dt.service.sdv1_type'] })
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
  }, [list.data, red.data])

  const counts = {
    active: rows.filter((r) => r.total > 0).length,
    failing: rows.filter((r) => r.failed > 0).length,
    all: rows.length,
  }
  const f = filter.toLowerCase()
  const shown = rows
    .filter((r) => (lens === 'all' ? true : lens === 'failing' ? r.failed > 0 : r.total > 0))
    .filter((r) => !f || `${r.name} ${r.ns ?? ''} ${r.id}`.toLowerCase().includes(f))

  const cols: Column[] = [
    {
      key: 'name',
      header: 'Service',
      width: 'minmax(220px,2fr)',
      render: (r) => <EntityLink id={r.id} name={r.name} type="SERVICE" />,
      sort: (r) => String(r.name ?? '').toLowerCase(),
    },
    { key: 'ns', header: 'Namespace', width: 'minmax(100px,1fr)', render: (r) => <span className="text-ink-2">{r.ns ?? '—'}</span>, sort: (r) => r.ns },
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
      align: 'right',
      render: (r) => (
        <span className="flex items-center justify-end gap-3">
          <Spark values={r.rt} color="var(--s7)" width={96} zeroBased={false} />
          <span className="w-16">{fmtLatencyUs(r.latency)}</span>
        </span>
      ),
      sort: (r) => r.latency ?? -1,
    },
    {
      key: 'kind',
      header: 'Type',
      width: '130px',
      render: (r) => (r.kind ? <Badge>{String(r.kind).replace(/_SERVICE$/, '').replace(/_/g, ' ').toLowerCase()}</Badge> : null),
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
                { value: 'active', label: 'With traffic', count: counts.active },
                { value: 'failing', label: 'Failing', count: counts.failing },
                { value: 'all', label: 'All', count: counts.all },
              ]}
            />
            <FilterInput value={filter} onChange={setFilter} placeholder="Filter services…" className="w-64" />
          </>
        }
      />
      <Panel spec={servicesSpec(tf)} result={red} className="min-h-0 flex-1" bodyClassName="flex min-h-0 flex-col" title={`${shown.length} services`}>
        {red.error ? (
          <ErrorBox error={red.error} />
        ) : (
          <DataTable
            rows={red.data || list.data ? shown : undefined}
            loading={red.isLoading && list.isLoading}
            columns={cols}
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
