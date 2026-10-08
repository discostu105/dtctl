import clsx from 'clsx'
import { BackLink } from '../components/BackLink'
import { Network } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link, useSearch } from 'wouter'
import { DataTable } from '../components/DataTable'
import { EntityLink, TypeIcon } from '../components/Entity'
import { FilterInput, PageHeader, Panel } from '../components/Panel'
import { FacetSearch, FacetSummary, useAttrs, useFacets } from '../components/Facets'
import type { Facet } from '../lib/facets'
import { Empty, ErrorBox, Skeleton, TimeAgo } from '../components/ui'
import { num, prefetchDql, useDql, type Rec } from '../lib/api'
import { censusQuery, instancesQuery, INSTANCES_LIMIT } from '../lib/dql'
import { nodesSource, withAttrs } from '../lib/attrs'
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
    <div className="p-5">
      <PageHeader
        title="Smartscape"
        icon={<Network className="size-5" />}
        sub={res.data ? `${fmtInt(total)} entities across ${fmtInt(res.data.records.length)} types` : 'Every entity Dynatrace knows about'}
        actions={<FilterInput value={filter} onChange={setFilter} placeholder="Filter types…" className="w-72" />}
      />
      {res.error && <ErrorBox error={res.error} />}
      {res.isLoading && (
        <div className="grid grid-cols-4 gap-3">
          {Array.from({ length: 12 }, (_, i) => (
            <Skeleton key={i} className="h-16" />
          ))}
        </div>
      )}
      {res.data && !groups.length && (
        <Empty title={filter ? 'No type matches' : 'No entities'} hint={filter ? `Nothing matches “${filter}”.` : 'Smartscape returned no entity types.'} />
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

const INSTANCE_FACETS: Facet<Rec>[] = [
  { key: 'ns', label: 'Namespace', value: (r) => r['k8s.namespace.name'], aliases: ['namespace'] },
  { key: 'cluster', label: 'Cluster', value: (r) => r['k8s.cluster.name'] },
  { key: 'region', label: 'Region', value: (r) => r.region, aliases: ['location'] },
  { key: 'account', label: 'Account', value: (r) => r.account, aliases: ['subscription', 'project'] },
]

/** What the cloud calls the account a resource belongs to. */
const accountWord = (type: string) => (type.startsWith('AZURE_') ? 'Subscription' : type.startsWith('GCP_') ? 'Project' : 'Account')

function Instances({ type }: { type: string }) {
  useTitle(shortType(type))
  const source = useMemo(() => nodesSource(type), [type])
  const attrs = useAttrs(source)
  const spec = { query: withAttrs(instancesQuery(type), attrs.filters), ttl: 120 }
  const res = useDql(spec)
  const fc = useFacets(res.data?.records, INSTANCE_FACETS, { text: (r) => `${r.name} ${r.id}`, attrs })
  const hasNs = res.data?.records.some((r) => r['k8s.namespace.name'])
  const hasRegion = res.data?.records.some((r) => r.region)
  const hasAccount = res.data?.records.some((r) => r.account)
  return (
    <div className="flex h-full flex-col p-5">
      <BackLink fallback="/smartscape" label="All types" />
      <PageHeader
        title={shortType(type)}
        icon={<TypeIcon type={type} className="size-5" />}
        sub={<span className="font-mono text-xs">{type}</span>}
        actions={<FacetSearch fc={fc} placeholder="Filter by name or ID…" className="w-72" />}
      />
      <Panel spec={spec} result={res} className="min-h-0 flex-1" bodyClassName="flex min-h-0 flex-col" head={<FacetSummary fc={fc} noun="entities" fetching={res.isFetching} limit={INSTANCES_LIMIT} />}>
        {res.error ? (
          <ErrorBox error={res.error} />
        ) : (
          <DataTable
            rows={fc.rows}
            loading={res.isLoading}
            facets={fc}
            rowKey={(r: Rec) => r.id}
            empty={<Empty title="No entities" hint="Smartscape has no entities of this type." />}
            href={(r) => entityHref(r.id, r.name)}
            className="flex-1"
            autoFocus
            columns={[
              { key: 'name', header: 'Name', width: 'minmax(260px,2fr)', render: (r) => <EntityLink id={r.id} name={r.name} type={type} />, sort: (r) => String(r.name).toLowerCase() },
              ...(hasNs ? [{ key: 'ns', header: 'Namespace', width: 'minmax(120px,1fr)', facet: 'ns', render: (r: Rec) => <span className="text-ink-2">{r['k8s.namespace.name']}</span>, sort: (r: Rec) => r['k8s.namespace.name'] }] : []),
              ...(hasRegion ? [{ key: 'region', header: 'Region', width: '130px', facet: 'region', render: (r: Rec) => <span className="text-ink-2">{r.region}</span>, sort: (r: Rec) => r.region }] : []),
              ...(hasAccount
                ? [{ key: 'account', header: accountWord(type), width: 'minmax(130px,1fr)', facet: 'account', render: (r: Rec) => <span className="truncate font-mono text-xs text-ink-3">{r.account}</span>, sort: (r: Rec) => r.account }]
                : []),
              { key: 'id', header: 'ID', width: '220px', render: (r) => <span className={clsx('font-mono text-xs text-ink-3')}>{r.id}</span> },
              { key: 'seen', header: 'Last seen', width: '100px', align: 'right', render: (r) => <TimeAgo value={r.lifetime?.end} className="text-ink-3" />, sort: (r) => r.lifetime?.end },
            ]}
          />
        )}
      </Panel>
    </div>
  )
}
