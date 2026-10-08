import clsx from 'clsx'
import { GitCommitVertical } from 'lucide-react'
import { useMemo, useState } from 'react'
import { DataTable, type Column } from '../components/DataTable'
import { FilterInput, PageHeader, Panel } from '../components/Panel'
import { Inspector, SidePanel } from '../components/signals'
import { Badge, CopyButton, ErrorBox, Segmented, TimeAgo } from '../components/ui'
import { useDql, type Rec } from '../lib/api'
import { fmtDateTime } from '../lib/format'
import { changesSpec } from '../lib/shared'
import { useTitle } from '../lib/store'
import { useTimeframe } from '../lib/timeframe'

const isDeploy = (r: Rec) => r['event.type'] === 'deployment' || /deploy/i.test(String(r.what ?? ''))

export default function Changes() {
  useTitle('Changes')
  const tf = useTimeframe()
  const spec = changesSpec(tf)
  const res = useDql(spec)
  const [lens, setLens] = useState<'deploy' | 'all'>('deploy')
  const [filter, setFilter] = useState('')
  const [sel, setSel] = useState<Rec | null>(null)
  const all = res.data?.records
  const f = filter.toLowerCase()
  const rows = useMemo(
    () => all?.filter((r) => lens === 'all' || isDeploy(r)).filter((r) => !f || JSON.stringify([r.what, r.env, r.outcome, r.rev, r.source]).toLowerCase().includes(f)),
    [all, lens, f],
  )

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
    { key: 'env', header: 'Environment', width: '120px', render: (r) => (r.env ? <Badge>{r.env}</Badge> : null), sort: (r) => r.env },
    {
      key: 'outcome',
      header: 'Outcome',
      width: '110px',
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
    { key: 'src', header: 'Source', width: '120px', render: (r) => <span className="text-ink-3">{r.source}</span>, sort: (r) => r.source },
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
              <FilterInput value={filter} onChange={setFilter} placeholder="Filter changes…" className="w-64" />
            </>
          }
        />
        <Panel spec={spec} result={res} className="min-h-0 flex-1" bodyClassName="flex min-h-0 flex-col" title={`${rows?.length ?? '…'} changes`}>
          {res.error ? (
            <ErrorBox error={res.error} />
          ) : (
            <DataTable
              rows={rows}
              loading={res.isLoading}
              columns={cols}
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
