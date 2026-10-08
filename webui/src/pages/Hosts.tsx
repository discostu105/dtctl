import clsx from 'clsx'
import { Server } from 'lucide-react'
import { useMemo } from 'react'
import { DataTable, type Column } from '../components/DataTable'
import { EntityLink } from '../components/Entity'
import { FacetSearch, FacetSummary, useFacets } from '../components/Facets'
import { PageHeader, Panel } from '../components/Panel'
import { Meter, Spark } from '../components/Spark'
import { Badge, Empty, ErrorBox, TimeAgo } from '../components/ui'
import { arr, num, useDql, type Rec } from '../lib/api'
import { bucket, type Facet } from '../lib/facets'
import { fmtBytes, fmtInt, fmtPct, titleCase } from '../lib/format'
import { entityHref } from '../lib/links'
import { tfSpec } from '../lib/shared'
import { useTitle } from '../lib/store'
import { sparkInterval, useTimeframe } from '../lib/timeframe'

const lastVal = (a: unknown) => {
  const v = arr(a).filter((x) => x != null)
  return v.length ? num(v[v.length - 1]) : NaN
}

const UTIL = ['≥ 90%', '75–90%', '50–75%', '< 50%']
const utilFacet = (key: 'cpu' | 'mem' | 'disk', label: string): Facet<Rec> => ({
  key,
  label,
  value: (r) => bucket(r[`${key}Now`], [[90, UTIL[0]], [75, UTIL[1]], [50, UTIL[2]]], UTIL[3]),
  order: UTIL,
})
const osName = (r: Rec) => String(r['os.version'] ?? r['os.type'] ?? '').split('(')[0].trim() || null

const FACETS: Facet<Rec>[] = [
  utilFacet('cpu', 'CPU'),
  utilFacet('mem', 'Memory'),
  utilFacet('disk', 'Disk'),
  { key: 'os', label: 'OS', value: osName },
  { key: 'ostype', label: 'OS family', value: (r) => r['os.type'], display: (v) => titleCase(v) },
  { key: 'instance', label: 'Instance type', value: (r) => r['host.type'], aliases: ['type'] },
  { key: 'size', label: 'Size', value: (r) => (r.logical_cores ? `${r.logical_cores} vCPU` : null), aliases: ['cores'] },
  { key: 'cloud', label: 'Cloud', value: (r) => r['cloud.provider'], display: (v) => v.toUpperCase() },
  { key: 'group', label: 'Host group', value: (r) => r['dt.host_group.id'], aliases: ['hostgroup'] },
]

export default function Hosts() {
  useTitle('Hosts')
  const tf = useTimeframe()
  const listSpec = tfSpec(tf, `smartscapeNodes HOST
| fields id, name, os.type, os.version, logical_cores, memory, ip, host.type, cloud.provider, lifetime, dt.host_group.id
| sort name asc
| limit 1000`)
  const metricSpec = tfSpec(
    tf,
    `timeseries { cpu = avg(dt.host.cpu.usage), mem = avg(dt.host.memory.usage), disk = max(dt.host.disk.used.percent) }, by:{dt.smartscape.host}, interval:${sparkInterval(tf.ms)}`,
  )
  const list = useDql(listSpec)
  const metrics = useDql(metricSpec)

  const rows = useMemo(() => {
    const m = new Map((metrics.data?.records ?? []).map((r) => [r['dt.smartscape.host'], r]))
    return (list.data?.records ?? []).map((h) => {
      const mr = m.get(h.id)
      return { ...h, cpu: mr?.cpu, mem: mr?.mem, disk: mr?.disk, cpuNow: lastVal(mr?.cpu), memNow: lastVal(mr?.mem), diskNow: lastVal(mr?.disk) } as Rec
    })
  }, [list.data, metrics.data])
  const fc = useFacets(list.data ? rows : undefined, FACETS, { text: (r) => `${r.name} ${r.id} ${arr(r.ip).join(' ')}` })

  const pctCol = (key: 'cpu' | 'mem' | 'disk', label: string, color: string): Column => ({
    key,
    header: label,
    width: '210px',
    facet: key,
    align: 'right',
    render: (r) => {
      const now = r[`${key}Now`] as number
      return (
        <span className="flex items-center justify-end gap-2.5">
          <Spark values={r[key]} color={color} width={72} max={100} />
          <Meter pct={now} className="w-12" />
          <span className={clsx('w-11', now >= 90 ? 'text-crit' : now >= 75 ? 'text-warn' : '')}>{Number.isFinite(now) ? fmtPct(now, 0) : '—'}</span>
        </span>
      )
    },
    sort: (r) => (Number.isFinite(r[`${key}Now`]) ? r[`${key}Now`] : -1),
  })

  const cols: Column[] = [
    { key: 'name', header: 'Host', width: 'minmax(220px,2fr)', render: (r) => <EntityLink id={r.id} name={r.name} type="HOST" />, sort: (r) => r.name },
    pctCol('cpu', 'CPU', 'var(--s1)'),
    pctCol('mem', 'Memory', 'var(--s7)'),
    pctCol('disk', 'Disk (fullest)', 'var(--s3)'),
    {
      key: 'spec',
      header: 'Size',
      width: '130px',
      facet: 'size',
      align: 'right',
      render: (r) => (
        <span className="text-ink-2">
          {r.logical_cores ?? '?'} vCPU · {fmtBytes(num(r.memory)).replace('.0 ', ' ')}
        </span>
      ),
      sort: (r) => num(r.logical_cores),
    },
    {
      key: 'os',
      header: 'OS',
      width: 'minmax(120px,1fr)',
      facet: 'os',
      render: (r) => <span className="text-ink-2" title={r['os.version']}>{osName(r)}</span>,
      sort: (r) => r['os.version'],
    },
    { key: 'type', header: 'Instance', width: '110px', facet: 'instance', render: (r) => (r['host.type'] ? <Badge mono>{r['host.type']}</Badge> : null), sort: (r) => r['host.type'] },
    { key: 'seen', header: 'Last seen', width: '90px', align: 'right', render: (r) => <TimeAgo value={r.lifetime?.end} className="text-ink-3" />, sort: (r) => r.lifetime?.end },
  ]

  return (
    <div className="flex h-full flex-col p-5">
      <PageHeader
        title="Hosts"
        icon={<Server className="size-5" />}
        sub={`${list.data ? fmtInt(rows.length) : '…'} hosts · utilization · ${tf.label.toLowerCase()}`}
        actions={<FacetSearch fc={fc} placeholder="Filter hosts…" className="w-72" />}
      />
      <Panel spec={metricSpec} result={metrics} className="min-h-0 flex-1" bodyClassName="flex min-h-0 flex-col" head={<FacetSummary fc={fc} noun="hosts" />}>
        {list.error ? (
          <ErrorBox error={list.error} />
        ) : (
          <DataTable
            rows={fc.rows}
            loading={list.isLoading}
            columns={cols}
            facets={fc}
            empty={<Empty title="No hosts" hint="Smartscape knows no hosts." />}
            rowKey={(r) => r.id}
            href={(r) => entityHref(r.id, r.name)}
            initialSort={{ key: 'cpu', dir: 'desc' }}
            rowHeight={40}
            className="flex-1"
            autoFocus
          />
        )}
      </Panel>
    </div>
  )
}
