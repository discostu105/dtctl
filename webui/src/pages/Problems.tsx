import { AlertOctagon } from 'lucide-react'
import { useMemo, useState } from 'react'
import { FacetSearch, FacetSummary, useFacets, type FacetCtl } from '../components/Facets'
import { PageHeader, Panel } from '../components/Panel'
import { ProblemsTable } from '../components/signals'
import { ErrorBox, Segmented } from '../components/ui'
import { prefetchDql, useDql } from '../lib/api'
import { problemDetailQuery, problemsQuery } from '../lib/dql'
import type { Facet } from '../lib/facets'
import { titleCase } from '../lib/format'
import { tfSpec } from '../lib/shared'
import { useTitle } from '../lib/store'
import { floorTf, useTimeframe } from '../lib/timeframe'

const FACETS: Facet[] = [
  { key: 'status', label: 'Status', value: (r) => r.status, order: ['ACTIVE', 'CLOSED'], display: (v) => titleCase(v) },
  { key: 'category', label: 'Category', value: (r) => r.category, display: (v) => titleCase(v), aliases: ['cat'] },
  { key: 'affected', label: 'Affected', value: (r) => r.affected, aliases: ['entity'] },
  { key: 'etype', label: 'Entity type', value: (r) => r.affected_types, display: (v) => titleCase(v.replace(/^K8S_/, 'K8s ')) },
  { key: 'root', label: 'Root cause', value: (r) => r.root, aliases: ['rc'] },
  { key: 'impact', label: 'Impact', value: (r) => r.impact, display: (v) => titleCase(v) },
]

export default function Problems() {
  useTitle('Problems')
  const raw = useTimeframe()
  const tf = floorTf(raw, '24h')
  const [status, setStatus] = useState<'all' | 'ACTIVE' | 'CLOSED'>('all')
  const spec = tfSpec(tf, problemsQuery({ limit: 500 }), { ttl: 30 })
  const res = useDql(spec, { refetchInterval: 60_000 })

  const all = res.data?.records
  const lensRows = useMemo(() => all?.filter((r) => status === 'all' || r.status === status), [all, status])
  const fc = useFacets(lensRows, FACETS, { text: (r) => `${r.name} ${r.display_id}` })
  const nActive = all?.filter((r) => r.status === 'ACTIVE').length

  return (
    <div className="flex h-full flex-col p-5">
      <PageHeader
        title="Problems"
        icon={<AlertOctagon className="size-5" />}
        sub={`Davis-detected problems · ${tf.label.toLowerCase()}${tf !== raw ? ' (at least 24h)' : ''}`}
        actions={
          <>
            <Segmented
              value={status}
              onChange={setStatus}
              options={[
                { value: 'all', label: 'All', count: all?.length },
                { value: 'ACTIVE', label: 'Active', count: nActive },
                { value: 'CLOSED', label: 'Closed', count: all && nActive != null ? all.length - nActive : undefined },
              ]}
            />
            <FacetSearch fc={fc} placeholder="Filter problems…" className="w-72" />
          </>
        }
      />
      <Panel spec={spec} result={res} className="min-h-0 flex-1" bodyClassName="flex min-h-0 flex-col" head={<FacetSummary fc={fc} noun="problems" />}>
        {res.error ? (
          <ErrorBox error={res.error} />
        ) : (
          <ProblemsTableWithPrefetch records={fc.rows} loading={res.isLoading} facets={fc} />
        )}
      </Panel>
    </div>
  )
}

function ProblemsTableWithPrefetch(props: { records: any[] | undefined; loading: boolean; facets: FacetCtl }) {
  // Hovering a row warms the problem page's main query.
  return (
    <div
      className="flex min-h-0 flex-1 flex-col"
      onMouseOver={(e) => {
        const a = (e.target as HTMLElement).closest('a[href^="/problems/"]')
        if (a) prefetchDql({ query: problemDetailQuery(decodeURIComponent(a.getAttribute('href')!.split('/')[2])) })
      }}
    >
      <ProblemsTable {...props} />
    </div>
  )
}
