import { useQuery } from '@tanstack/react-query'
import { ExternalLink, FileText, LayoutDashboard, Lock, NotebookPen } from 'lucide-react'
import { useState } from 'react'
import { DataTable } from '../components/DataTable'
import { PageHeader, Panel } from '../components/Panel'
import { FacetSearch, FacetSummary, useFacets } from '../components/Facets'
import type { Facet } from '../lib/facets'
import { Empty, ErrorBox, Segmented, TimeAgo, Tip } from '../components/ui'
import { getJSON, useMeta } from '../lib/api'
import { dtLinks } from '../lib/links'
import { useTitle } from '../lib/store'

interface Doc {
  id: string
  name: string
  type: string
  owner: string
  modified: string
  isPrivate: boolean
  lastOpened?: string
}

const opened = (d: Doc) => !!d.lastOpened && !d.lastOpened.startsWith('0001')
const DOC_FACETS: Facet<Doc>[] = [
  { key: 'opened', label: 'Opened by you', value: (d) => (opened(d) ? 'Yes' : 'Never'), order: ['Yes', 'Never'] },
  { key: 'visibility', label: 'Visibility', value: (d) => (d.isPrivate ? 'Private' : 'Shared'), order: ['Shared', 'Private'] },
]

export default function Documents() {
  useTitle('Documents')
  const [type, setType] = useState<'dashboard' | 'notebook'>('dashboard')
  const { data: meta } = useMeta()
  const res = useQuery({ queryKey: ['docs', type], queryFn: () => getJSON<Doc[]>(`/api/documents?type=${type}`), staleTime: 60_000 })
  // warm the other tab
  useQuery({ queryKey: ['docs', type === 'dashboard' ? 'notebook' : 'dashboard'], queryFn: () => getJSON<Doc[]>(`/api/documents?type=${type === 'dashboard' ? 'notebook' : 'dashboard'}`), staleTime: 60_000 })
  const fc = useFacets(res.data, DOC_FACETS, { text: (d) => d.name })
  const href = (d: Doc) => (meta ? dtLinks.document(meta.environment, d.type, d.id) : '#')

  return (
    <div className="flex h-full flex-col p-5">
      <PageHeader
        title="Documents"
        icon={<FileText className="size-5" />}
        sub="Dashboards and notebooks open in Dynatrace"
        actions={
          <>
            <Segmented
              value={type}
              onChange={setType}
              options={[
                { value: 'dashboard', label: 'Dashboards' },
                { value: 'notebook', label: 'Notebooks' },
              ]}
            />
            <FacetSearch fc={fc} placeholder={`Filter ${type}s…`} className="w-72" />
          </>
        }
      />
      <Panel className="min-h-0 flex-1" bodyClassName="flex min-h-0 flex-col" head={<FacetSummary fc={fc} noun={`${type}s`} />}>
        {res.error ? (
          <ErrorBox error={res.error} />
        ) : (
          <DataTable
            rows={fc.rows}
            loading={res.isLoading}
            facets={fc}
            rowKey={(d) => d.id}
            empty={<Empty title={`No ${type}s`} hint={`You have no ${type}s in this environment.`} />}
            onOpen={(d) => window.open(href(d), '_blank', 'noopener')}
            initialSort={{ key: 'modified', dir: 'desc' }}
            className="flex-1"
            autoFocus
            columns={[
              {
                key: 'name',
                header: 'Name',
                width: 'minmax(300px,3fr)',
                render: (d) => (
                  <span className="flex min-w-0 items-center gap-2">
                    {d.type === 'notebook' ? <NotebookPen className="size-3.5 shrink-0 text-ink-3" /> : <LayoutDashboard className="size-3.5 shrink-0 text-ink-3" />}
                    <span className="truncate">{d.name}</span>
                    {d.isPrivate && (
                      <Tip content="Private">
                        <Lock className="size-3 shrink-0 text-ink-4" />
                      </Tip>
                    )}
                  </span>
                ),
                sort: (d) => d.name.toLowerCase(),
              },
              { key: 'modified', header: 'Modified', width: '110px', align: 'right', render: (d) => <TimeAgo value={d.modified} className="text-ink-2" />, sort: (d) => d.modified },
              {
                key: 'opened',
                header: 'You opened',
                width: '110px',
                align: 'right',
                render: (d) => (opened(d) ? <TimeAgo value={d.lastOpened} className="text-ink-3" /> : <span className="text-ink-4">never</span>),
                sort: (d) => d.lastOpened ?? '',
              },
              { key: 'open', header: '', width: '28px', render: () => <ExternalLink className="size-3.5 text-ink-4" /> },
            ]}
          />
        )}
      </Panel>
    </div>
  )
}
