import clsx from 'clsx'
import { GitCommitVertical } from 'lucide-react'
import { useMemo, useState } from 'react'
import { DataTable, type Column } from '../components/DataTable'
import { FacetSearch, FacetSummary, useFacets } from '../components/Facets'
import { PageHeader, Panel } from '../components/Panel'
import { Inspector, SidePanel } from '../components/signals'
import { Badge, CopyButton, ErrorBox, Segmented, TimeAgo } from '../components/ui'
import { useDql, type Rec } from '../lib/api'
import { fmtDateTime } from '../lib/format'
import type { Facet } from '../lib/facets'
import { changesSpec } from '../lib/shared'
import { useTitle } from '../lib/store'
import { useTimeframe } from '../lib/timeframe'

const isDeploy = (r: Rec) => r['event.type'] === 'deployment' || /deploy/i.test(String(r.what ?? ''))

/** What kind of change, in words a person would use. */
function changeKind(r: Rec) {
  const t = String(r['event.type'] ?? '')
  if (t === 'deployment' || t === 'CUSTOM_DEPLOYMENT') return 'Deployment'
  if (t === 'PROCESS_RESTART') return 'Process restart'
  if (t === 'CUSTOM_CONFIGURATION') return 'Configuration'
  if (t === 'CUSTOM_ANNOTATION') return 'Annotation'
  if (r['event.provider'] === 'KUBERNETES_INFERRED_EVENT') return 'Kubernetes spec change'
  return 'Other'
}

const SOURCES: Record<string, string> = { KUBERNETES_INFERRED_EVENT: 'Kubernetes', ONEAGENT: 'OneAgent' }
const prettySource = (s: unknown) => (s == null ? s : (SOURCES[String(s)] ?? String(s)))
const lower = (v: unknown) => (v == null ? v : String(v).toLowerCase())

const FACETS: Facet<Rec>[] = [
  { key: 'type', label: 'Type', value: changeKind, aliases: ['kind'] },
  { key: 'env', label: 'Environment', value: (r) => r.env, aliases: ['stage'] },
  { key: 'outcome', label: 'Outcome', value: (r) => lower(r.outcome) as string, aliases: ['status'] },
  { key: 'ns', label: 'Namespace', value: (r) => r.ns, aliases: ['namespace'] },
  { key: 'workload', label: 'Workload', value: (r) => r.workload, aliases: ['target'] },
  { key: 'source', label: 'Source', value: (r) => prettySource(r.source) as string, aliases: ['provider'] },
  { key: 'cluster', label: 'Cluster', value: (r) => r.cluster },
]

export default function Changes() {
  useTitle('Changes')
  const tf = useTimeframe()
  const spec = changesSpec(tf)
  const res = useDql(spec)
  const [lens, setLens] = useState<'deploy' | 'all'>('deploy')
  const [sel, setSel] = useState<Rec | null>(null)
  const all = res.data?.records
  const lensRows = useMemo(() => all?.filter((r) => lens === 'all' || isDeploy(r)), [all, lens])
  const fc = useFacets(lensRows, FACETS, { text: (r) => `${r.what} ${r.rev ?? ''}` })

  const cols: Column[] = [
    {
      key: 'time',
      header: 'When',
      width: '150px',
      render: (r) => (
        <span className="flex items-center gap-2">
          <span className="tnum text-ink-2">{fmtDateTime(r.timestamp)}</span>
        </span>
      ),
      sort: (r) => r.timestamp,
    },
    { key: 'ago', header: '', width: '70px', render: (r) => <TimeAgo value={r.timestamp} className="text-ink-3" /> },
    {
      key: 'what',
      header: 'Change',
      width: 'minmax(220px,2fr)',
      render: (r) => (
        <span className="flex min-w-0 items-center gap-2">
          <GitCommitVertical className="size-3.5 shrink-0 text-accent" />
          <span className="truncate font-medium">{r.what}</span>
        </span>
      ),
      sort: (r) => r.what,
    },
    {
      key: 'target',
      header: 'Target',
      width: 'minmax(160px,1.4fr)',
      facet: 'workload',
      render: (r) =>
        r.workload ? (
          <span className="truncate">
            <span className="text-ink-2">{r.workload}</span>
            {r.ns && <span className="text-ink-4"> · {r.ns}</span>}
          </span>
        ) : null,
      sort: (r) => r.workload,
    },
    { key: 'env', header: 'Environment', width: '120px', facet: 'env', render: (r) => (r.env ? <Badge>{r.env}</Badge> : null), sort: (r) => r.env },
    {
      key: 'outcome',
      header: 'Outcome',
      width: '110px',
      facet: 'outcome',
      render: (r) => {
        const o = String(r.outcome ?? '')
        return o ? <Badge tone={/fail|error/i.test(o) ? 'crit' : /succe|finished/i.test(o) ? 'ok' : 'info'}>{o.toLowerCase()}</Badge> : null
      },
      sort: (r) => r.outcome,
    },
    {
      key: 'rev',
      header: 'Revision',
      width: '110px',
      render: (r) =>
        r.rev ? (
          <span className="flex items-center gap-1 font-mono text-xs text-ink-2">
            {String(r.rev).slice(0, 8)}
            <CopyButton value={String(r.rev)} label="revision" />
          </span>
        ) : null,
    },
    {
      key: 'src',
      header: 'Source',
      width: '120px',
      facet: 'source',
      render: (r) => <span className="text-ink-3">{prettySource(r.source) as string}</span>,
      sort: (r) => prettySource(r.source) as string,
    },
  ]

  return (
    <div className="flex h-full">
      <div className="flex min-w-0 flex-1 flex-col p-5">
        <PageHeader
          title="Changes"
          icon={<GitCommitVertical className="size-5" />}
          sub={`Deployments, configuration changes and restarts · ${tf.label.toLowerCase()}`}
          actions={
            <>
              <Segmented
                value={lens}
                onChange={setLens}
                options={[
                  { value: 'deploy', label: 'Deployments', count: all?.filter(isDeploy).length },
                  { value: 'all', label: 'All changes', count: all?.length },
                ]}
              />
              <FacetSearch fc={fc} placeholder="Filter changes…" className="w-72" />
            </>
          }
        />
        <Panel spec={spec} result={res} className="min-h-0 flex-1" bodyClassName="flex min-h-0 flex-col" head={<FacetSummary fc={fc} noun="changes" />}>
          {res.error ? (
            <ErrorBox error={res.error} />
          ) : (
            <DataTable
              rows={fc.rows}
              loading={res.isLoading}
              columns={cols}
              facets={fc}
              rowKey={(r, i) => `${r['event.id'] ?? i}-${r.timestamp}`}
              onOpen={setSel}
              selectedKey={sel ? `${sel['event.id']}-${sel.timestamp}` : null}
              initialSort={{ key: 'time', dir: 'desc' }}
              className={clsx('flex-1')}
              autoFocus
            />
          )}
        </Panel>
      </div>
      {sel && (
        <SidePanel title={sel.what} onClose={() => setSel(null)}>
          <Inspector rec={sel} />
        </SidePanel>
      )}
    </div>
  )
}
