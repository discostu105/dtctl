import clsx from 'clsx'
import { Boxes } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useLocation, useSearch } from 'wouter'
import { DataTable, type Column } from '../components/DataTable'
import { EntityLink } from '../components/Entity'
import { FilterInput, PageHeader, Panel } from '../components/Panel'
import { Badge, ErrorBox, Segmented, TimeAgo, Tip } from '../components/ui'
import { num, useDql, type Rec } from '../lib/api'
import { fmtInt, shortType } from '../lib/format'
import { entityHref } from '../lib/links'
import { tfSpec } from '../lib/shared'
import { useTitle } from '../lib/store'
import { useTimeframe } from '../lib/timeframe'

type View = 'workloads' | 'pods' | 'nodes' | 'namespaces'

const QUERIES: Record<View, string> = {
  workloads: `smartscapeNodes "K8S_DEPLOYMENT", "K8S_STATEFULSET", "K8S_DAEMONSET"
| parse k8s.object, "JSON:obj"
| fieldsAdd ready = toLong(coalesce(obj[status][readyReplicas], obj[status][numberReady], 0)), desired = toLong(coalesce(obj[spec][replicas], obj[status][desiredNumberScheduled], 0))
| fields id, name, type, namespace = k8s.namespace.name, cluster = k8s.cluster.name, ready, desired, created = toTimestamp(obj[metadata][creationTimestamp])
| sort namespace asc, name asc
| limit 2000`,
  pods: `smartscapeNodes "K8S_POD"
| parse k8s.object, "JSON:obj"
| expand cs = obj[status][containerStatuses]
| summarize { name = takeFirst(name), phase = takeFirst(k8s.pod.phase), namespace = takeFirst(k8s.namespace.name), node = takeFirst(k8s.node.name), workload = takeFirst(k8s.workload.name), kind = takeFirst(k8s.workload.kind), ready = countIf(cs[ready] == true), total = count(), restarts = sum(toLong(cs[restartCount])), created = takeFirst(toTimestamp(obj[metadata][creationTimestamp])) }, by:{id}
| sort namespace asc, name asc
| limit 3000`,
  nodes: `smartscapeNodes "K8S_NODE"
| parse k8s.object, "JSON:obj"
| fields id, name, cluster = k8s.cluster.name, kubelet = obj[status][nodeInfo][kubeletVersion], os = obj[status][nodeInfo][osImage], cpu = obj[status][capacity][cpu], instance = obj[metadata][labels][\`node.kubernetes.io/instance-type\`], zone = obj[metadata][labels][\`topology.kubernetes.io/zone\`], created = toTimestamp(obj[metadata][creationTimestamp])
| sort name asc
| limit 1000`,
  namespaces: `smartscapeNodes "K8S_NAMESPACE"
| fields id, name, cluster = k8s.cluster.name, lifetime
| sort name asc
| limit 1000`,
}

export default function Kubernetes() {
  useTitle('Kubernetes')
  const tf = useTimeframe()
  const search = new URLSearchParams(useSearch())
  const [, navigate] = useLocation()
  const view = (search.get('view') as View) || 'workloads'
  const [filter, setFilter] = useState(search.get('ns') ?? '')
  const spec = tfSpec(tf, QUERIES[view], { ttl: 60 })
  const res = useDql(spec)

  // prefetch the sibling views so switching is instant
  useDql(tfSpec(tf, QUERIES.pods, { ttl: 60 }))
  useDql(tfSpec(tf, QUERIES.workloads, { ttl: 60 }))

  const f = filter.toLowerCase()
  const rows = useMemo(() => (res.data?.records ?? []).filter((r) => !f || Object.values(r).join(' ').toLowerCase().includes(f)), [res.data, f])

  const setView = (v: View) => navigate(`/k8s?view=${v}`, { replace: true })

  const unhealthyPods = view === 'pods' ? rows.filter(podUnhealthy).length : 0
  const degraded = view === 'workloads' ? rows.filter((r) => num(r.ready) < num(r.desired)).length : 0

  return (
    <div className="flex h-full flex-col p-5">
      <PageHeader
        title="Kubernetes"
        icon={<Boxes className="size-5" />}
        sub={
          view === 'pods' && unhealthyPods ? (
            <span className="text-warn">{unhealthyPods} pods not ready or restarting</span>
          ) : view === 'workloads' && degraded ? (
            <span className="text-warn">{degraded} workloads below desired replicas</span>
          ) : (
            'Workloads, pods and nodes from Smartscape'
          )
        }
        actions={
          <>
            <Segmented
              value={view}
              onChange={setView}
              options={[
                { value: 'workloads', label: 'Workloads' },
                { value: 'pods', label: 'Pods' },
                { value: 'nodes', label: 'Nodes' },
                { value: 'namespaces', label: 'Namespaces' },
              ]}
            />
            <FilterInput value={filter} onChange={setFilter} placeholder={`Filter ${view}…`} className="w-64" />
          </>
        }
      />
      <Panel spec={spec} result={res} className="min-h-0 flex-1" bodyClassName="flex min-h-0 flex-col" title={`${rows.length} ${view}`}>
        {res.error ? (
          <ErrorBox error={res.error} />
        ) : (
          <DataTable
            key={view}
            rows={res.data ? rows : undefined}
            loading={res.isLoading}
            columns={COLUMNS[view](setFilter)}
            rowKey={(r) => r.id}
            href={(r) => entityHref(r.id, r.name)}
            initialSort={view === 'pods' ? { key: 'health', dir: 'desc' } : view === 'workloads' ? { key: 'ready', dir: 'asc' } : undefined}
            className="flex-1"
            autoFocus
          />
        )}
      </Panel>
    </div>
  )
}

function podUnhealthy(r: Rec) {
  return (r.phase === 'Running' && num(r.ready) < num(r.total)) || num(r.restarts) > 0 || r.phase === 'Pending' || r.phase === 'Failed' || r.phase === 'Unknown'
}

const nsCell = (setFilter: (s: string) => void): Column => ({
  key: 'ns',
  header: 'Namespace',
  width: 'minmax(120px,1fr)',
  render: (r) => (
    <Tip content="Filter by this namespace">
      <button
        type="button"
        className="truncate text-ink-2 hover:text-accent-ink"
        onClick={(e) => {
          e.preventDefault()
          e.stopPropagation()
          setFilter(r.namespace)
        }}
      >
        {r.namespace}
      </button>
    </Tip>
  ),
  sort: (r) => r.namespace,
})

const COLUMNS: Record<View, (setFilter: (s: string) => void) => Column[]> = {
  workloads: (sf) => [
    { key: 'name', header: 'Workload', width: 'minmax(220px,2fr)', render: (r) => <EntityLink id={r.id} name={r.name} type={r.type} />, sort: (r) => r.name },
    nsCell(sf),
    { key: 'kind', header: 'Kind', width: '110px', render: (r) => <Badge>{shortType(r.type)}</Badge>, sort: (r) => r.type },
    {
      key: 'ready',
      header: 'Ready',
      width: '150px',
      align: 'right',
      render: (r) => {
        const ready = num(r.ready)
        const desired = num(r.desired)
        const ok = ready >= desired
        return (
          <span className="flex items-center justify-end gap-2">
            <span className="flex h-1.5 w-16 overflow-hidden rounded-full bg-line">
              <span className={clsx('h-full rounded-full', ok ? 'bg-ok' : 'bg-warn')} style={{ width: `${desired ? (100 * ready) / desired : 100}%` }} />
            </span>
            <span className={clsx('w-12', !ok && 'text-warn')}>
              {fmtInt(ready)}/{fmtInt(desired)}
            </span>
          </span>
        )
      },
      sort: (r) => (num(r.desired) ? num(r.ready) / num(r.desired) : 1),
    },
    { key: 'cluster', header: 'Cluster', width: '120px', render: (r) => <span className="text-ink-3">{r.cluster}</span>, sort: (r) => r.cluster },
    { key: 'age', header: 'Created', width: '96px', align: 'right', render: (r) => <TimeAgo value={r.created} className="text-ink-3" />, sort: (r) => r.created },
  ],
  pods: (sf) => [
    {
      key: 'health',
      header: '',
      width: '14px',
      render: (r) => <span className={clsx('inline-block size-2 rounded-full', podUnhealthy(r) ? 'bg-warn' : r.phase === 'Running' ? 'bg-ok' : 'bg-ink-4')} />,
      sort: (r) => (podUnhealthy(r) ? 2 : r.phase === 'Running' ? 1 : 0),
    },
    { key: 'name', header: 'Pod', width: 'minmax(240px,2fr)', render: (r) => <EntityLink id={r.id} name={r.name} type="K8S_POD" />, sort: (r) => r.name },
    nsCell(sf),
    {
      key: 'phase',
      header: 'Phase',
      width: '96px',
      render: (r) => <Badge tone={r.phase === 'Running' ? 'ok' : r.phase === 'Succeeded' ? 'muted' : 'warn'}>{r.phase}</Badge>,
      sort: (r) => r.phase,
    },
    {
      key: 'ready',
      header: 'Ready',
      width: '64px',
      align: 'right',
      render: (r) => <span className={clsx(r.phase === 'Running' && num(r.ready) < num(r.total) && 'text-warn')}>{`${num(r.ready)}/${num(r.total)}`}</span>,
      sort: (r) => num(r.ready) / Math.max(1, num(r.total)),
    },
    {
      key: 'restarts',
      header: 'Restarts',
      width: '72px',
      align: 'right',
      render: (r) => <span className={clsx(num(r.restarts) > 0 ? 'text-warn' : 'text-ink-3')}>{fmtInt(num(r.restarts))}</span>,
      sort: (r) => num(r.restarts),
    },
    { key: 'workload', header: 'Workload', width: 'minmax(140px,1fr)', render: (r) => <span className="text-ink-2">{r.workload ?? '—'}</span>, sort: (r) => r.workload },
    { key: 'node', header: 'Node', width: 'minmax(120px,1fr)', render: (r) => <span className="text-ink-3">{r.node}</span>, sort: (r) => r.node },
    { key: 'age', header: 'Age', width: '80px', align: 'right', render: (r) => <TimeAgo value={r.created} className="text-ink-3" />, sort: (r) => r.created },
  ],
  nodes: () => [
    { key: 'name', header: 'Node', width: 'minmax(220px,2fr)', render: (r) => <EntityLink id={r.id} name={r.name} type="K8S_NODE" />, sort: (r) => r.name },
    { key: 'instance', header: 'Instance', width: '120px', render: (r) => (r.instance ? <Badge mono>{r.instance}</Badge> : null), sort: (r) => r.instance },
    { key: 'zone', header: 'Zone', width: '110px', render: (r) => <span className="text-ink-2">{r.zone}</span>, sort: (r) => r.zone },
    { key: 'cpu', header: 'CPUs', width: '60px', align: 'right', render: (r) => r.cpu, sort: (r) => num(r.cpu) },
    { key: 'kubelet', header: 'Kubelet', width: '140px', render: (r) => <span className="font-mono text-xs text-ink-2">{r.kubelet}</span>, sort: (r) => r.kubelet },
    { key: 'os', header: 'OS image', width: 'minmax(140px,1fr)', render: (r) => <span className="text-ink-3">{r.os}</span>, sort: (r) => r.os },
    { key: 'age', header: 'Age', width: '80px', align: 'right', render: (r) => <TimeAgo value={r.created} className="text-ink-3" />, sort: (r) => r.created },
  ],
  namespaces: () => [
    { key: 'name', header: 'Namespace', width: 'minmax(220px,2fr)', render: (r) => <EntityLink id={r.id} name={r.name} type="K8S_NAMESPACE" />, sort: (r) => r.name },
    { key: 'cluster', header: 'Cluster', width: 'minmax(120px,1fr)', render: (r) => <span className="text-ink-2">{r.cluster}</span>, sort: (r) => r.cluster },
    { key: 'seen', header: 'Last seen', width: '100px', align: 'right', render: (r) => <TimeAgo value={r.lifetime?.end} className="text-ink-3" />, sort: (r) => r.lifetime?.end },
  ],
}
