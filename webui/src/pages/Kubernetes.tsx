import clsx from 'clsx'
import { Boxes } from 'lucide-react'
import { useLocation, useSearch } from 'wouter'
import { DataTable, type Column } from '../components/DataTable'
import { EntityLink } from '../components/Entity'
import { FacetSearch, FacetSummary, useAttrs, useFacets } from '../components/Facets'
import { PageHeader, Panel } from '../components/Panel'
import { Badge, Empty, ErrorBox, Segmented, TimeAgo } from '../components/ui'
import { num, useDql, type Rec } from '../lib/api'
import { nodesSource, parseAttrs, serializeAttr, withAttrs, type AttrSource } from '../lib/attrs'
import { parseFilters, serializeFilter, type Facet } from '../lib/facets'
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
| sort ready >= desired asc, namespace asc, name asc
| limit 2000
| sort namespace asc, name asc`,
  pods: `smartscapeNodes "K8S_POD"
| parse k8s.object, "JSON:obj"
| expand cs = obj[status][containerStatuses]
| summarize { name = takeFirst(name), phase = takeFirst(k8s.pod.phase), namespace = takeFirst(k8s.namespace.name), node = takeFirst(k8s.node.name), workload = takeFirst(k8s.workload.name), kind = takeFirst(k8s.workload.kind), ready = countIf(cs[ready] == true), total = count(), restarts = sum(toLong(cs[restartCount])), created = takeFirst(toTimestamp(obj[metadata][creationTimestamp])) }, by:{id}
| fieldsAdd trouble = if(not(in(phase, {"Running", "Succeeded"})) or (phase == "Running" and ready < total), 0, else: if(restarts > 0, 1, else: 2))
| sort trouble asc, namespace asc, name asc
| limit 3000
| sort namespace asc, name asc`,
  nodes: `smartscapeNodes "K8S_NODE"
| parse k8s.object, "JSON:obj"
| fields id, name, cluster = k8s.cluster.name, kubelet = obj[status][nodeInfo][kubeletVersion], os = obj[status][nodeInfo][osImage], cpu = obj[status][capacity][cpu], instance = \`tags:k8s.labels\`[\`node.kubernetes.io/instance-type\`], zone = \`tags:k8s.labels\`[\`topology.kubernetes.io/zone\`], created = toTimestamp(obj[metadata][creationTimestamp])
| sort name asc
| limit 1000`,
  namespaces: `smartscapeNodes "K8S_NAMESPACE"
| fields id, name, cluster = k8s.cluster.name, lifetime
| sort name asc
| limit 1000`,
}

// The manifest (k8s.object) has labels stripped; they live in the tag maps.
const SOURCES: Record<View, AttrSource> = {
  workloads: nodesSource(['K8S_DEPLOYMENT', 'K8S_STATEFULSET', 'K8S_DAEMONSET']),
  pods: nodesSource('K8S_POD'),
  nodes: nodesSource('K8S_NODE'),
  namespaces: nodesSource('K8S_NAMESPACE'),
}

// On big clusters the cap keeps the broken objects: the slice is taken trouble-first, then shown by name.
const LIMITS: Record<View, number> = { workloads: 2000, pods: 3000, nodes: 1000, namespaces: 1000 }

/** Attribute filters that mean the same on every Kubernetes view survive a view switch. */
const portableAttr = (field: string) => field.startsWith('tags:') || field.startsWith('primary_tags.') || field === 'k8s.namespace.name' || field === 'k8s.cluster.name'

const nsFacet: Facet<Rec> = { key: 'ns', label: 'Namespace', value: (r) => r.namespace, aliases: ['namespace'] }
const clusterFacet: Facet<Rec> = { key: 'cluster', label: 'Cluster', value: (r) => r.cluster }

const FACETS: Record<View, Facet<Rec>[]> = {
  workloads: [
    nsFacet,
    {
      key: 'health',
      label: 'Health',
      value: (r) => (num(r.desired) === 0 ? 'Scaled to zero' : num(r.ready) < num(r.desired) ? 'Degraded' : 'Ready'),
      order: ['Degraded', 'Ready', 'Scaled to zero'],
    },
    { key: 'kind', label: 'Kind', value: (r) => shortType(r.type) },
    clusterFacet,
  ],
  pods: [
    nsFacet,
    { key: 'health', label: 'Health', value: podHealth, order: ['Failed', 'Pending', 'Not ready', 'Restarting', 'Healthy', 'Completed'] },
    { key: 'phase', label: 'Phase', value: (r) => r.phase },
    { key: 'workload', label: 'Workload', value: (r) => r.workload },
    { key: 'kind', label: 'Owner kind', value: (r) => r.kind },
    { key: 'node', label: 'Node', value: (r) => r.node },
  ],
  nodes: [
    { key: 'instance', label: 'Instance type', value: (r) => r.instance, aliases: ['type'] },
    { key: 'zone', label: 'Zone', value: (r) => r.zone, aliases: ['az'] },
    { key: 'kubelet', label: 'Kubelet', value: (r) => r.kubelet, aliases: ['version'] },
    { key: 'os', label: 'OS image', value: (r) => r.os },
    clusterFacet,
  ],
  namespaces: [clusterFacet],
}

export default function Kubernetes() {
  useTitle('Kubernetes')
  const tf = useTimeframe()
  const search = new URLSearchParams(useSearch())
  const [, navigate] = useLocation()
  const view = (search.get('view') as View) || 'workloads'
  const attrs = useAttrs(SOURCES[view])
  const query = (v: View) => withAttrs(QUERIES[v], v === view ? attrs.filters : attrs.filters.filter((f) => portableAttr(f.field)))
  const spec = tfSpec(tf, query(view), { ttl: 60, maxRecords: LIMITS[view] })
  const res = useDql(spec)

  // prefetch the sibling views so switching is instant
  useDql(tfSpec(tf, query('pods'), { ttl: 60, maxRecords: LIMITS.pods }))
  useDql(tfSpec(tf, query('workloads'), { ttl: 60, maxRecords: LIMITS.workloads }))

  const fc = useFacets(res.data?.records, FACETS[view], { text: (r) => `${r.name} ${r.id}`, attrs })
  const rows = fc.rows ?? []

  // Filters that also exist in the target view (namespace, cluster…) come along.
  const setView = (v: View) => {
    const p = new URLSearchParams(window.location.search)
    const keep = parseFilters(p).filter((f) => FACETS[v].some((x) => x.key === f.key))
    p.delete('f')
    keep.forEach((f) => p.append('f', serializeFilter(f)))
    const keepAttrs = parseAttrs(p).filter((f) => portableAttr(f.field))
    p.delete('a')
    keepAttrs.forEach((f) => p.append('a', serializeAttr(f)))
    p.set('view', v)
    navigate(`/k8s?${p}`, { replace: true })
  }

  const unhealthyPods = view === 'pods' ? rows.filter(podUnhealthy).length : 0
  const degraded = view === 'workloads' ? rows.filter((r) => num(r.ready) < num(r.desired)).length : 0

  return (
    <div className="flex h-full flex-col p-5">
      <PageHeader
        title="Kubernetes"
        icon={<Boxes className="size-5" />}
        sub={
          view === 'pods' && unhealthyPods ? (
            <button type="button" onClick={() => fc.setKey('health', ['Failed', 'Pending', 'Not ready', 'Restarting'])} className="text-warn hover:underline">
              {fmtInt(unhealthyPods)} pods not ready or restarting
            </button>
          ) : view === 'workloads' && degraded ? (
            <button type="button" onClick={() => fc.setKey('health', ['Degraded'])} className="text-warn hover:underline">
              {fmtInt(degraded)} workloads below desired replicas
            </button>
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
            <FacetSearch key={view} fc={fc} placeholder={`Filter ${view}…`} className="w-72" />
          </>
        }
      />
      <Panel spec={spec} result={res} className="min-h-0 flex-1" bodyClassName="flex min-h-0 flex-col" head={<FacetSummary fc={fc} noun={view} fetching={res.isFetching} limit={LIMITS[view]} />}>
        {res.error ? (
          <ErrorBox error={res.error} />
        ) : (
          <DataTable
            key={view}
            rows={fc.rows}
            loading={res.isLoading}
            columns={COLUMNS[view]}
            facets={fc}
            empty={<Empty title={`No ${view}`} hint="Smartscape has no Kubernetes objects of this kind." />}
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

function podHealth(r: Rec) {
  if (r.phase === 'Failed' || r.phase === 'Unknown') return 'Failed'
  if (r.phase === 'Pending') return 'Pending'
  if (r.phase === 'Succeeded') return 'Completed'
  if (r.phase === 'Running' && num(r.ready) < num(r.total)) return 'Not ready'
  if (num(r.restarts) > 0) return 'Restarting'
  return 'Healthy'
}

function podUnhealthy(r: Rec) {
  return (r.phase === 'Running' && num(r.ready) < num(r.total)) || num(r.restarts) > 0 || r.phase === 'Pending' || r.phase === 'Failed' || r.phase === 'Unknown'
}

const nsCol: Column = {
  key: 'ns',
  header: 'Namespace',
  width: 'minmax(120px,1fr)',
  facet: 'ns',
  render: (r) => <span className="text-ink-2">{r.namespace}</span>,
  sort: (r) => r.namespace,
}

const COLUMNS: Record<View, Column[]> = {
  workloads: [
    { key: 'name', header: 'Workload', width: 'minmax(220px,2fr)', render: (r) => <EntityLink id={r.id} name={r.name} type={r.type} />, sort: (r) => r.name },
    nsCol,
    { key: 'kind', header: 'Kind', width: '110px', facet: 'kind', render: (r) => <Badge>{shortType(r.type)}</Badge>, sort: (r) => r.type },
    {
      key: 'ready',
      header: 'Ready',
      width: '150px',
      facet: 'health',
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
    { key: 'cluster', header: 'Cluster', width: '120px', facet: 'cluster', render: (r) => <span className="text-ink-3">{r.cluster}</span>, sort: (r) => r.cluster },
    { key: 'age', header: 'Age', width: '96px', align: 'right', render: (r) => <TimeAgo value={r.created} className="text-ink-3" />, sort: (r) => r.created },
  ],
  pods: [
    {
      key: 'health',
      header: '',
      width: '14px',
      render: (r) => <span className={clsx('inline-block size-2 rounded-full', podUnhealthy(r) ? 'bg-warn' : r.phase === 'Running' ? 'bg-ok' : 'bg-ink-4')} />,
      sort: (r) => (podUnhealthy(r) ? 2 : r.phase === 'Running' ? 1 : 0),
    },
    { key: 'name', header: 'Pod', width: 'minmax(240px,2fr)', render: (r) => <EntityLink id={r.id} name={r.name} type="K8S_POD" />, sort: (r) => r.name },
    nsCol,
    {
      key: 'phase',
      header: 'Phase',
      width: '96px',
      facet: 'phase',
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
    { key: 'workload', header: 'Workload', width: 'minmax(140px,1fr)', facet: 'workload', render: (r) => <span className="text-ink-2">{r.workload ?? '—'}</span>, sort: (r) => r.workload },
    { key: 'node', header: 'Node', width: 'minmax(120px,1fr)', facet: 'node', render: (r) => <span className="text-ink-3">{r.node}</span>, sort: (r) => r.node },
    { key: 'age', header: 'Age', width: '80px', align: 'right', render: (r) => <TimeAgo value={r.created} className="text-ink-3" />, sort: (r) => r.created },
  ],
  nodes: [
    { key: 'name', header: 'Node', width: 'minmax(220px,2fr)', render: (r) => <EntityLink id={r.id} name={r.name} type="K8S_NODE" />, sort: (r) => r.name },
    { key: 'instance', header: 'Instance', width: '120px', facet: 'instance', render: (r) => (r.instance ? <Badge mono>{r.instance}</Badge> : null), sort: (r) => r.instance },
    { key: 'zone', header: 'Zone', width: '110px', facet: 'zone', render: (r) => <span className="text-ink-2">{r.zone}</span>, sort: (r) => r.zone },
    { key: 'cpu', header: 'CPUs', width: '60px', align: 'right', render: (r) => r.cpu, sort: (r) => num(r.cpu) },
    { key: 'kubelet', header: 'Kubelet', width: '140px', facet: 'kubelet', render: (r) => <span className="font-mono text-xs text-ink-2">{r.kubelet}</span>, sort: (r) => r.kubelet },
    { key: 'os', header: 'OS image', width: 'minmax(140px,1fr)', facet: 'os', render: (r) => <span className="text-ink-3">{r.os}</span>, sort: (r) => r.os },
    { key: 'age', header: 'Age', width: '80px', align: 'right', render: (r) => <TimeAgo value={r.created} className="text-ink-3" />, sort: (r) => r.created },
  ],
  namespaces: [
    { key: 'name', header: 'Namespace', width: 'minmax(220px,2fr)', render: (r) => <EntityLink id={r.id} name={r.name} type="K8S_NAMESPACE" />, sort: (r) => r.name },
    { key: 'cluster', header: 'Cluster', width: 'minmax(120px,1fr)', facet: 'cluster', render: (r) => <span className="text-ink-2">{r.cluster}</span>, sort: (r) => r.cluster },
    { key: 'seen', header: 'Last seen', width: '100px', align: 'right', render: (r) => <TimeAgo value={r.lifetime?.end} className="text-ink-3" />, sort: (r) => r.lifetime?.end },
  ],
}
