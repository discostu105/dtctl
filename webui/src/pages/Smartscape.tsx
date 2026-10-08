import clsx from 'clsx'
import { ArrowLeft, Network } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link, useLocation, useSearch } from 'wouter'
import { DataTable } from '../components/DataTable'
import { EntityLink, TypeIcon } from '../components/Entity'
import { FilterInput, PageHeader, Panel } from '../components/Panel'
import { ErrorBox, Skeleton, TimeAgo } from '../components/ui'
import { num, prefetchDql, useDql, type Rec } from '../lib/api'
import { censusQuery, instancesQuery } from '../lib/dql'
import { fmtInt, shortType } from '../lib/format'
import { entityHref } from '../lib/links'
import { useTitle } from '../lib/store'

const DOMAINS: { label: string; match: (t: string) => boolean }[] = [
  { label: 'Applications', match: (t) => ['SERVICE', 'FRONTEND', 'PROCESS', 'OTEL_PROCESS', 'HTTP_MONITOR_STEP', 'SYNTHETIC_LOCATION'].includes(t) || t.startsWith('GENAI_') || t.startsWith('DB_') },
  { label: 'Kubernetes', match: (t) => t.startsWith('K8S_') || t === 'CONTAINER' },
  { label: 'Infrastructure', match: (t) => ['HOST', 'OTEL_HOST', 'DISK', 'NETWORK_INTERFACE', 'ONEAGENT', 'ACTIVEGATE'].includes(t) },
  { label: 'Cloud', match: (t) => /^(AWS|AZURE|GCP)_/.test(t) },
  { label: 'Other', match: () => true },
]

export default function Smartscape() {
  const params = new URLSearchParams(useSearch())
  const type = params.get('type')
  return type ? <Instances type={type} /> : <Census />
}

function Census() {
  useTitle('Smartscape')
  const spec = { query: censusQuery(), ttl: 300 }
  const res = useDql(spec)
  const [filter, setFilter] = useState('')
  const f = filter.toLowerCase()
  const groups = useMemo(() => {
    const recs = (res.data?.records ?? []).filter((r) => !f || String(r.type).toLowerCase().includes(f) || shortType(r.type).toLowerCase().includes(f))
    const used = new Set<string>()
    return DOMAINS.map((d) => {
      const items = recs.filter((r) => !used.has(r.type) && d.match(r.type))
      items.forEach((r) => used.add(r.type))
      return { ...d, items }
    }).filter((g) => g.items.length)
  }, [res.data, f])
  const total = (res.data?.records ?? []).reduce((a, r) => a + num(r.count), 0)

  return (
    <div className="mx-auto max-w-[1600px] p-5">
      <PageHeader
        title="Smartscape"
        icon={<Network className="size-5" />}
        sub={res.data ? `${fmtInt(total)} entities across ${res.data.records.length} types` : 'Every entity Dynatrace knows about'}
        actions={<FilterInput value={filter} onChange={setFilter} placeholder="Filter types…" className="w-64" />}
      />
      {res.error && <ErrorBox error={res.error} />}
      {res.isLoading && (
        <div className="grid grid-cols-4 gap-3">
          {Array.from({ length: 12 }, (_, i) => (
            <Skeleton key={i} className="h-16" />
          ))}
        </div>
      )}
      {groups.map((g) => (
        <section key={g.label} className="mb-6">
          <h2 className="mb-2 text-2xs font-medium tracking-wide text-ink-3 uppercase">{g.label}</h2>
          <div className="grid grid-cols-[repeat(auto-fill,minmax(220px,1fr))] gap-2">
            {g.items.map((r) => (
              <Link
                key={r.type}
                href={`/smartscape?type=${encodeURIComponent(r.type)}`}
                onMouseEnter={() => prefetchDql({ query: instancesQuery(r.type), ttl: 120 })}
                className="group flex items-center gap-3 rounded-xl border border-line bg-panel p-3 transition-colors hover:border-line-strong hover:bg-panel-hover"
              >
                <span className="flex size-8 items-center justify-center rounded-lg bg-sunken">
                  <TypeIcon type={r.type} className="size-4 group-hover:text-accent-ink" />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm">{shortType(r.type)}</span>
                  <span className="block truncate font-mono text-2xs text-ink-4">{r.type}</span>
                </span>
                <span className="tnum text-sm font-semibold text-ink-2">{fmtInt(num(r.count))}</span>
              </Link>
            ))}
          </div>
        </section>
      ))}
    </div>
  )
}

function Instances({ type }: { type: string }) {
  useTitle(shortType(type))
  const spec = { query: instancesQuery(type), ttl: 120 }
  const res = useDql(spec)
  const [filter, setFilter] = useState('')
  const [, navigate] = useLocation()
  const f = filter.toLowerCase()
  const rows = useMemo(() => res.data?.records.filter((r) => !f || `${r.name} ${r.id} ${r['k8s.namespace.name'] ?? ''}`.toLowerCase().includes(f)), [res.data, f])
  const hasNs = rows?.some((r) => r['k8s.namespace.name'])
  const hasRegion = rows?.some((r) => r['aws.region'])
  return (
    <div className="flex h-full flex-col p-5">
      <button type="button" onClick={() => navigate('/smartscape')} className="mb-3 inline-flex items-center gap-1 self-start text-xs text-ink-3 hover:text-ink-2">
        <ArrowLeft className="size-3.5" /> All types
      </button>
      <PageHeader
        title={shortType(type)}
        icon={<TypeIcon type={type} className="size-5" />}
        sub={<span className="font-mono text-xs">{type}</span>}
        actions={<FilterInput value={filter} onChange={setFilter} placeholder="Filter by name or ID…" className="w-72" />}
      />
      <Panel spec={spec} result={res} className="min-h-0 flex-1" bodyClassName="flex min-h-0 flex-col" title={`${rows?.length ?? '…'} entities`}>
        {res.error ? (
          <ErrorBox error={res.error} />
        ) : (
          <DataTable
            rows={rows}
            loading={res.isLoading}
            rowKey={(r: Rec) => r.id}
            href={(r) => entityHref(r.id, r.name)}
            className="flex-1"
            autoFocus
            columns={[
              { key: 'name', header: 'Name', width: 'minmax(260px,2fr)', render: (r) => <EntityLink id={r.id} name={r.name} type={type} />, sort: (r) => String(r.name).toLowerCase() },
              ...(hasNs ? [{ key: 'ns', header: 'Namespace', width: 'minmax(120px,1fr)', render: (r: Rec) => <span className="text-ink-2">{r['k8s.namespace.name']}</span>, sort: (r: Rec) => r['k8s.namespace.name'] }] : []),
              ...(hasRegion ? [{ key: 'region', header: 'Region', width: '120px', render: (r: Rec) => <span className="text-ink-2">{r['aws.region']}</span>, sort: (r: Rec) => r['aws.region'] }] : []),
              { key: 'id', header: 'ID', width: '220px', render: (r) => <span className={clsx('font-mono text-xs text-ink-3')}>{r.id}</span> },
              { key: 'seen', header: 'Last seen', width: '100px', align: 'right', render: (r) => <TimeAgo value={r.lifetime?.end} className="text-ink-3" />, sort: (r) => r.lifetime?.end },
            ]}
          />
        )}
      </Panel>
    </div>
  )
}
