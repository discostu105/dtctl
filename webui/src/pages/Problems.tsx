import { AlertOctagon } from 'lucide-react'
import { useMemo, useState } from 'react'
import { FilterInput, PageHeader, Panel } from '../components/Panel'
import { ProblemsTable } from '../components/signals'
import { ErrorBox, Segmented } from '../components/ui'
import { prefetchDql, useDql } from '../lib/api'
import { problemDetailQuery, problemsQuery } from '../lib/dql'
import { tfSpec } from '../lib/shared'
import { useTitle } from '../lib/store'
import { floorTf, useTimeframe } from '../lib/timeframe'

export default function Problems() {
  useTitle('Problems')
  const raw = useTimeframe()
  const tf = floorTf(raw, '24h')
  const [status, setStatus] = useState<'all' | 'ACTIVE' | 'CLOSED'>('all')
  const [filter, setFilter] = useState('')
  const spec = tfSpec(tf, problemsQuery({ limit: 500 }), { ttl: 30 })
  const res = useDql(spec, { refetchInterval: 60_000 })

  const all = res.data?.records
  const rows = useMemo(() => {
    const f = filter.toLowerCase()
    return all
      ?.filter((r) => status === 'all' || r.status === status)
      .filter((r) => !f || JSON.stringify([r.name, r.display_id, r.affected, r.category]).toLowerCase().includes(f))
  }, [all, status, filter])
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
            <FilterInput value={filter} onChange={setFilter} placeholder="Filter problems…" className="w-64" />
          </>
        }
      />
      <Panel spec={spec} result={res} className="min-h-0 flex-1" bodyClassName="flex min-h-0 flex-col" title={`${rows?.length ?? '…'} problems`}>
        {res.error ? (
          <ErrorBox error={res.error} />
        ) : (
          <ProblemsTableWithPrefetch records={rows} loading={res.isLoading} />
        )}
      </Panel>
    </div>
  )
}

function ProblemsTableWithPrefetch(props: { records: any[] | undefined; loading: boolean }) {
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
