import clsx from 'clsx'
import { AlertOctagon, ArrowLeft, ArrowRight, CheckCircle2, ExternalLink, GitCommitVertical, Network } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { Link, useLocation, useSearch } from 'wouter'
import { TimeChart, tsAxis } from '../components/Chart'
import { EntityLink, TypeIcon } from '../components/Entity'
import { Panel } from '../components/Panel'
import { Inspector, LogDetail, LogStream, ProblemsTable, SidePanel, SpanTable } from '../components/signals'
import { Badge, CopyButton, Empty, ErrorBox, Facts, Skeleton, SkeletonRows, Tabs, TimeAgo } from '../components/ui'
import { arr, num, useDql, useMeta, type DqlSpec, type Rec } from '../lib/api'
import {
  changesQuery, detailQuery, edgesQuery, logsQuery, namesQuery, problemsQuery, signalFilter, signalFilterAll, spanFilter, spanScopable, spansQuery,
  typeOfId, vitalQuery, vitalsFor, type Entity, type Vital,
} from '../lib/dql'
import { fmtBytes, fmtCompact, fmtDateTime, fmtMs, fmtUs, shortType } from '../lib/format'
import { dtLinks } from '../lib/links'
import { tfSpec } from '../lib/shared'
import { pushRecent, useTitle } from '../lib/store'
import { absolute, intervalFor, setTimeframe, useTimeframe } from '../lib/timeframe'
import { EventList } from './Problem'
import { ErrorsView, SessionsView } from './Rum'

type Tab = 'overview' | 'logs' | 'traces' | 'sessions' | 'rumerrors' | 'events' | 'problems' | 'related'

export default function EntityPage({ id }: { id: string }) {
  const type = typeOfId(id)
  const search = new URLSearchParams(useSearch())
  const [, navigate] = useLocation()
  const tab = (search.get('tab') as Tab) || 'overview'
  const setTab = (t: Tab) => {
    const p = new URLSearchParams(location.search)
    p.set('tab', t)
    navigate(`${location.pathname}?${p}`, { replace: true })
  }
  const detailSpec: DqlSpec = { query: detailQuery({ id, type }), ttl: 60 }
  const detail = useDql(detailSpec)
  const rec = detail.data?.records[0]
  const name: string = rec?.name || search.get('n') || id
  const entity: Entity = useMemo(() => ({ id, type, name: rec?.name || search.get('n') || undefined }), [id, type, rec?.name])

  useTitle(name)
  useEffect(() => {
    if (rec) pushRecent({ href: `/e/${id}`, label: rec.name || id, kind: shortType(type) })
  }, [rec, id, type])

  const { data: meta } = useMeta()
  const tf = useTimeframe()

  // health strip
  const probSpec: DqlSpec = { query: problemsQuery({ entityId: id, limit: 50 }), from: tf.ms < 86400e3 ? 'now-24h' : tf.from, to: tf.to, ttl: 30 }
  const problems = useDql(probSpec)
  const active = problems.data?.records.filter((r) => r.status === 'ACTIVE') ?? []
  const lastChange = useDql({ query: changesQuery(1, signalFilter(entity)), from: 'now-7d', ttl: 60 })
  const change = lastChange.data?.records[0]

  const vitals = vitalsFor(type)
  const canSpans = spanScopable(type)
  const isFrontend = type === 'FRONTEND'

  if (detail.error) return <ErrorBox error={detail.error} />

  return (
    <EntityLayout>
      <div className="mb-1">
        <BackLink />
      </div>
      <div className="mb-4 flex flex-wrap items-start gap-4">
        <div className="flex size-10 items-center justify-center rounded-xl bg-accent-wash">
          <TypeIcon type={type} className="size-5 text-accent-ink" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            {detail.isLoading && !search.get('n') ? <Skeleton className="h-7 w-72" /> : <h1 className="truncate text-xl font-semibold tracking-tight">{name}</h1>}
            <Badge tone="accent">{shortType(type)}</Badge>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-ink-3">
            <span className="inline-flex items-center gap-1 font-mono text-xs">
              {id}
              <CopyButton value={id} label="entity ID" />
            </span>
            {rec?.lifetime?.start && (
              <span>
                first seen <TimeAgo value={rec.lifetime.start} />
              </span>
            )}
            {rec?.lifetime?.end && (
              <span>
                last seen <TimeAgo value={rec.lifetime.end} />
              </span>
            )}
          </div>
        </div>
        <div className="flex items-center gap-2">
          {meta?.environment && (
            <a
              href={dtLinks.entity(meta.environment, id, type)}
              target="_blank"
              rel="noreferrer"
              className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-line bg-sunken px-3 text-sm text-ink-2 hover:border-line-strong hover:text-ink"
            >
              Open in Dynatrace <ExternalLink className="size-3.5" />
            </a>
          )}
        </div>
      </div>

      {/* health strip */}
      <div className="mb-4 flex flex-wrap gap-2">
        {problems.isLoading ? (
          <Skeleton className="h-8 w-52" />
        ) : active.length ? (
          <button
            type="button"
            onClick={() => setTab('problems')}
            className="inline-flex h-8 items-center gap-2 rounded-lg bg-crit-wash px-3 text-sm text-crit hover:brightness-110"
          >
            <AlertOctagon className="size-4" /> {active.length} active problem{active.length > 1 ? 's' : ''}: {active[0].name}
          </button>
        ) : (
          <span className="inline-flex h-8 items-center gap-2 rounded-lg bg-ok-wash px-3 text-sm text-ok">
            <CheckCircle2 className="size-4" /> No active problems
            {problems.data?.records.length ? <span className="text-ink-3">· {problems.data.records.length} closed recently</span> : null}
          </span>
        )}
        {change && (
          <span className="inline-flex h-8 items-center gap-2 rounded-lg border border-line bg-panel px-3 text-sm text-ink-2">
            <GitCommitVertical className="size-4 text-accent" /> Last change: {change.what} <TimeAgo value={change.timestamp} className="text-ink-3" />
          </span>
        )}
      </div>

      {vitals.length > 0 && (
        <div className={clsx('mb-4 grid gap-3', vitals.length >= 3 ? 'grid-cols-3 max-xl:grid-cols-1' : 'grid-cols-2 max-xl:grid-cols-1')}>
          {vitals.slice(0, 3).map((v) => (
            <VitalPanel key={v.key} v={v} e={entity} />
          ))}
        </div>
      )}

      <div className="rounded-xl border border-line bg-panel">
        <Tabs
          className="px-2"
          value={tab}
          onChange={setTab}
          tabs={[
            { value: 'overview', label: 'Overview' },
            { value: 'sessions', label: 'Sessions', hidden: !isFrontend },
            { value: 'rumerrors', label: 'Errors', hidden: !isFrontend },
            { value: 'logs', label: 'Logs', hidden: isFrontend },
            { value: 'traces', label: 'Traces', hidden: !canSpans },
            { value: 'events', label: 'Events' },
            { value: 'problems', label: 'Problems', count: problems.data?.records.length || null },
            { value: 'related', label: 'Related' },
          ]}
        />
        <div className="min-h-[420px]">
          {tab === 'overview' && <Overview rec={rec} loading={detail.isLoading} type={type} />}
          {tab === 'logs' && <EntityLogs entity={entity} />}
          {tab === 'sessions' && (
            <div className="flex h-[560px] flex-col">
              <SessionsView tf={tf} realOnly frontend={id} />
            </div>
          )}
          {tab === 'rumerrors' && (
            <div className="flex h-[560px] flex-col">
              <ErrorsView tf={tf} realOnly frontend={id} />
            </div>
          )}
          {tab === 'traces' && <EntityTraces entity={entity} />}
          {tab === 'events' && <EntityEvents entity={entity} />}
          {tab === 'problems' && (
            <div className="h-[460px]">
              {problems.error ? <ErrorBox error={problems.error} /> : <ProblemsTable records={problems.data?.records} loading={problems.isLoading} maxHeight={460} />}
            </div>
          )}
          {tab === 'related' && <Related id={id} />}
        </div>
      </div>
    </EntityLayout>
  )
}

function EntityLayout({ children }: { children: React.ReactNode }) {
  return <div className="mx-auto max-w-[1600px] p-5">{children}</div>
}

function BackLink() {
  return (
    <button type="button" onClick={() => history.back()} className="inline-flex items-center gap-1 text-xs text-ink-3 hover:text-ink-2">
      <ArrowLeft className="size-3.5" /> Back
    </button>
  )
}

export function fmtUnit(unit: Vital['unit']) {
  switch (unit) {
    case '%':
      return (v: number) => `${v.toFixed(v < 10 ? 1 : 0)}%`
    case 'B':
      return (v: number) => fmtBytes(v)
    case 'B/s':
      return (v: number) => `${fmtBytes(v)}/s`
    case 'µs':
      return (v: number) => fmtUs(v)
    case 'ms':
      return (v: number) => fmtMs(v)
    case 's':
      return (v: number) => fmtMs(v * 1000)
    case 'mCores':
      return (v: number) => (v >= 1000 ? `${(v / 1000).toFixed(2)} cores` : `${Math.round(v)} m`)
    default:
      return (v: number) => fmtCompact(v)
  }
}

function VitalPanel({ v, e }: { v: Vital; e: Entity }) {
  const tf = useTimeframe()
  const spec = tfSpec(tf, vitalQuery(v, e, intervalFor(tf.ms)))
  const res = useDql(spec)
  const r = res.data?.records[0]
  const fmt = fmtUnit(v.unit)
  const vals: (number | null)[] = r ? arr(r.v) : []
  const nums = vals.filter((x): x is number => x != null)
  const last = nums.length ? nums[nums.length - 1] : NaN
  const peak = nums.length ? Math.max(...nums) : NaN
  return (
    <Panel
      title={v.title}
      spec={spec}
      result={res}
      actions={
        nums.length > 0 && (
          <span className="tnum mr-1 text-xs text-ink-3">
            now <b className="font-semibold text-ink">{fmt(last)}</b> · peak {fmt(peak)}
          </span>
        )
      }
    >
      {res.error ? (
        <ErrorBox error={res.error} />
      ) : res.isLoading ? (
        <Skeleton className="m-3 h-[110px]" />
      ) : !r ? (
        <Empty title="No data" hint={`${v.key} has no data for this entity in the timeframe.`} className="py-6" />
      ) : (
        <div className="p-2 pl-0">
          <TimeChart
            x={tsAxis(r, 'v')}
            series={[{ label: v.title, values: vals, color: v.key.includes('fail') ? '--s8' : '--s1' }]}
            height={120}
            format={fmt}
            syncKey="vitals"
            yMax={v.unit === '%' ? 100 : undefined}
            onZoom={(a, b) => setTimeframe(absolute(a, b))}
          />
        </div>
      )}
    </Panel>
  )
}

// Curated key facts first; full properties below.
const FACT_KEYS: [string, string][] = [
  ['k8s.cluster.name', 'Cluster'],
  ['k8s.namespace.name', 'Namespace'],
  ['k8s.workload.name', 'Workload'],
  ['k8s.node.name', 'Node'],
  ['k8s.pod.phase', 'Phase'],
  ['host.name', 'Host'],
  ['os.version', 'OS'],
  ['logical_cores', 'vCPUs'],
  ['memory', 'Memory'],
  ['ip', 'IP'],
  ['host.type', 'Instance type'],
  ['cloud.provider', 'Cloud'],
  ['aws.region', 'AWS region'],
  ['aws.account.id', 'AWS account'],
  ['aws.availability_zone', 'Zone'],
  ['dt.service.sdv1_type', 'Service type'],
  ['process.technology', 'Technology'],
  ['container.image.name', 'Image'],
  ['dt.host_group.id', 'Host group'],
]

function Overview({ rec, loading, type }: { rec: Rec | undefined; loading: boolean; type: string }) {
  if (loading) return <SkeletonRows rows={8} />
  if (!rec) return <Empty title="Entity not found in Smartscape" hint="It may no longer exist, or the timeframe may be before it was created." />
  const facts: [string, React.ReactNode][] = FACT_KEYS.filter(([k]) => rec[k] != null && rec[k] !== '').map(([k, label]) => [
    label,
    k === 'memory' ? fmtBytes(num(rec[k])) : Array.isArray(rec[k]) ? rec[k].join(', ') : String(rec[k]),
  ])
  facts.push(['Type', shortType(type)])
  if (rec.lifetime?.start) facts.push(['First seen', fmtDateTime(rec.lifetime.start)])
  if (rec.lifetime?.end) facts.push(['Last seen', fmtDateTime(rec.lifetime.end)])
  const hide = ['k8s.object', 'references']
  const obj = rec['k8s.object']
  return (
    <div className="grid grid-cols-[minmax(260px,1fr)_2fr] gap-6 p-4 max-xl:grid-cols-1">
      <div>
        <div className="mb-2 text-2xs font-medium tracking-wide text-ink-3 uppercase">Key facts</div>
        <Facts items={facts} />
      </div>
      <div className="min-w-0">
        <div className="mb-2 text-2xs font-medium tracking-wide text-ink-3 uppercase">All properties</div>
        <Inspector rec={rec} hide={hide} />
        {obj && (
          <details className="mt-3 rounded-lg border border-line">
            <summary className="cursor-pointer px-3 py-2 text-sm text-ink-2">Kubernetes manifest</summary>
            <pre className="max-h-96 overflow-auto border-t border-line p-3 font-mono text-xs text-ink-2">{prettyJson(obj)}</pre>
          </details>
        )}
      </div>
    </div>
  )
}

function prettyJson(s: unknown) {
  try {
    return JSON.stringify(typeof s === 'string' ? JSON.parse(s) : s, null, 2)
  } catch {
    return String(s)
  }
}

function EntityLogs({ entity }: { entity: Entity }) {
  const tf = useTimeframe()
  // Logs are emitted by processes/containers, not services: hop over runs_on
  // edges to widen a service's scope (dynatui LogHopQuery).
  const hop = useDql(
    entity.type === 'SERVICE'
      ? {
          query: `smartscapeEdges "*"\n| filter source_id == toSmartscapeId("${entity.id}") and type == "runs_on"\n| fieldsAdd target_type\n| filter in(target_type, {"PROCESS", "CONTAINER"})\n| fields target_id, target_type\n| limit 50`,
          ttl: 120,
        }
      : null,
  )
  const ready = entity.type !== 'SERVICE' || hop.data || hop.error
  const ents: Entity[] = [entity, ...(hop.data?.records ?? []).map((r) => ({ id: r.target_id, type: r.target_type }))]
  const spec = ready ? tfSpec(tf, logsQuery([signalFilterAll(ents)], 500)) : null
  const res = useDql(spec)
  const [sel, setSel] = useState<Rec | null>(null)
  return (
    <div className="flex h-[520px]">
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex h-9 items-center gap-2 border-b border-line px-3 text-xs text-ink-3">
          {res.data ? `${res.data.records.length}${res.data.records.length >= 500 ? '+' : ''} records · newest first` : 'Loading…'}
          {ents.length > 1 && <span>· via {ents.length - 1} process/container{ents.length > 2 ? 'es' : ''} this service runs on</span>}
          <span className="ml-auto" />
          {spec && (
            <Link href={`/logs?${new URLSearchParams({ f: signalFilterAll(ents) })}`} className="text-accent-ink hover:underline">
              Open in Logs →
            </Link>
          )}
        </div>
        {res.error ? <ErrorBox error={res.error} /> : <LogStream records={res.data?.records} loading={!res.data} onSelect={setSel} selected={sel} className="flex-1" />}
      </div>
      {sel && (
        <SidePanel title="Log record" onClose={() => setSel(null)} width="w-[min(520px,45vw)]">
          <LogDetail rec={sel} />
        </SidePanel>
      )}
    </div>
  )
}

function EntityTraces({ entity }: { entity: Entity }) {
  const tf = useTimeframe()
  const [lens, setLens] = useState<'all' | 'errors'>('all')
  const spec = tfSpec(tf, spansQuery(lens === 'errors' ? 'errors' : entity.type === 'SERVICE' ? 'roots' : 'all', [spanFilter(entity)], 300))
  const res = useDql(spec)
  return (
    <div className="flex h-[520px] flex-col">
      <div className="flex h-9 items-center gap-3 border-b border-line px-3 text-xs">
        {(['all', 'errors'] as const).map((l) => (
          <button key={l} type="button" onClick={() => setLens(l)} className={clsx(lens === l ? 'text-ink' : 'text-ink-3 hover:text-ink-2')}>
            {l === 'all' ? 'Requests' : 'Errors only'}
          </button>
        ))}
      </div>
      {res.error ? <ErrorBox error={res.error} /> : <SpanTable records={res.data?.records} loading={res.isLoading} className="flex-1" />}
    </div>
  )
}

function EntityEvents({ entity }: { entity: Entity }) {
  const tf = useTimeframe()
  const res = useDql(tfSpec(tf, `fetch events\n| filter ${signalFilter(entity)}\n| sort timestamp desc\n| limit 300`))
  const [sel, setSel] = useState<Rec | null>(null)
  return (
    <div className="flex h-[520px]">
      <div className="min-w-0 flex-1">
        {res.error ? <ErrorBox error={res.error} /> : res.isLoading ? <SkeletonRows /> : <EventList records={res.data?.records ?? []} onSelect={setSel} />}
      </div>
      {sel && (
        <SidePanel title={sel['event.name'] ?? 'Event'} onClose={() => setSel(null)} width="w-[min(520px,45vw)]">
          <Inspector rec={sel} />
        </SidePanel>
      )}
    </div>
  )
}

const VERB_ORDER = ['runs_on', 'is_part_of', 'belongs_to', 'member_of', 'uses', 'calls', 'routes_to', 'owner_of', 'has_member', 'contains', 'parent_of', 'child_of', 'monitors']

function verbLabel(verb: string, dir: 'out' | 'in') {
  const v = verb.replace(/_/g, ' ')
  if (dir === 'out') return v
  const inv: Record<string, string> = {
    runs_on: 'runs',
    calls: 'called by',
    uses: 'used by',
    is_part_of: 'has part',
    belongs_to: 'has',
    routes_to: 'routed from',
    owner_of: 'owned by',
    member_of: 'has member',
    has_member: 'member of',
    contains: 'contained in',
    monitors: 'monitored by',
  }
  return inv[verb] ?? `${v} (inbound)`
}

function Related({ id }: { id: string }) {
  const edges = useDql({ query: edgesQuery(id), ttl: 120 })
  const others = useMemo(() => {
    const s = new Set<string>()
    for (const e of edges.data?.records ?? []) s.add(e.source_id === id ? e.target_id : e.source_id)
    return [...s]
  }, [edges.data, id])
  const names = useDql(others.length ? { query: namesQuery(others.slice(0, 300)), ttl: 120 } : null)
  const nameOf = useMemo(() => new Map((names.data?.records ?? []).map((r) => [r.id, r.name])), [names.data])

  const groups = useMemo(() => {
    const g = new Map<string, { verb: string; dir: 'out' | 'in'; type: string; ids: string[] }>()
    for (const e of edges.data?.records ?? []) {
      const out = e.source_id === id
      const other = out ? e.target_id : e.source_id
      const type = out ? e.target_type : e.source_type
      const key = `${out ? 'out' : 'in'}|${e.type}|${type}`
      if (!g.has(key)) g.set(key, { verb: e.type, dir: out ? 'out' : 'in', type, ids: [] })
      g.get(key)!.ids.push(other)
    }
    return [...g.values()].sort((a, b) => {
      const ia = VERB_ORDER.indexOf(a.verb)
      const ib = VERB_ORDER.indexOf(b.verb)
      return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib) || a.dir.localeCompare(b.dir) || b.ids.length - a.ids.length
    })
  }, [edges.data, id])

  if (edges.error) return <ErrorBox error={edges.error} />
  if (edges.isLoading) return <SkeletonRows rows={6} />
  if (!groups.length) return <Empty icon={<Network className="size-5" />} title="No relationships" hint="Smartscape has no edges for this entity." />
  return (
    <div className="grid grid-cols-2 gap-3 p-3 max-xl:grid-cols-1">
      {groups.map((g) => (
        <div key={`${g.dir}${g.verb}${g.type}`} className="rounded-lg border border-line">
          <div className="flex items-center gap-2 border-b border-line px-3 py-2 text-xs">
            {g.dir === 'out' ? <ArrowRight className="size-3.5 text-accent" /> : <ArrowLeft className="size-3.5 text-ok" />}
            <span className="font-medium text-ink-2">{verbLabel(g.verb, g.dir)}</span>
            <TypeIcon type={g.type} />
            <span className="text-ink-3">{shortType(g.type)}</span>
            <span className="tnum ml-auto text-ink-3">{g.ids.length}</span>
          </div>
          <ul className="max-h-56 overflow-auto py-1">
            {g.ids.map((oid) => (
              <li key={oid} className="px-3 py-1 text-sm">
                <EntityLink id={oid} name={nameOf.get(oid)} type={g.type} />
              </li>
            ))}
          </ul>
        </div>
      ))}
    </div>
  )
}
