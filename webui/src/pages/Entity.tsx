import clsx from 'clsx'
import { AlertOctagon, ArrowLeft, ArrowRight, CheckCircle2, GitCommitVertical, Network } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { Link, useLocation, useSearch } from 'wouter'
import { TimeChart, tsAxis } from '../components/Chart'
import { EntityLink, TypeIcon } from '../components/Entity'
import { Panel } from '../components/Panel'
import { Inspector, LogDetail, LogStream, ProblemsTable, SidePanel, SpanTable } from '../components/signals'
import { Badge, Empty, ErrorBox, Facts, Skeleton, SkeletonRows, TimeAgo } from '../components/ui'
import { arr, num, useDql, useMeta, type DqlSpec, type Rec } from '../lib/api'
import {
  changesQuery, detailQuery, edgesQuery, logsQuery, namesQuery, problemsQuery, signalFilter, signalFilterAll, spanFilter, spanScopable, spansQuery,
  typeOfId, vitalQuery, vitalsFor, type Entity, type Vital,
} from '../lib/dql'
import { fmtBytes, fmtDateTime, fmtUnit, shortType } from '../lib/format'
import { describeField, groupRank, mapField } from '../lib/attrs'
import { dtLinks, entityHref, listHref } from '../lib/links'
import { tfSpec } from '../lib/shared'
import { pushRecent, useTitle } from '../lib/store'
import { absolute, intervalFor, setTimeframe, useTimeframe, type Timeframe } from '../lib/timeframe'
import { displayName, useResolved } from '../lib/names'
import { EventList } from './Problem'
import { ErrorsView, errorGroupsSpec, SessionsView, sessionsSpec } from './Rum'
import { DataTabs } from '../components/DataTabs'
import { BackLink, DetailFallback } from '../components/BackLink'
import { DetailHeader, IdCopy, OpenInDynatrace } from '../components/DetailHeader'
import { EntityMetrics } from '../components/EntityMetrics'
import { headlineVitals, metricDiscoveryQuery } from '../lib/metrics'

// Tab query builders, shared by the page (counts, empty states, "show
// query") and the tab bodies: identical specs share one cache entry.
const hopSpec = (e: Entity): DqlSpec | null =>
  e.type === 'SERVICE'
    ? {
        query: `smartscapeEdges "*"\n| filter source_id == toSmartscapeId("${e.id}") and type == "runs_on"\n| fieldsAdd target_type\n| filter in(target_type, {"PROCESS", "CONTAINER"})\n| fields target_id, target_type\n| limit 50`,
        ttl: 120,
      }
    : null
const logEntities = (e: Entity, hop?: Rec[]): Entity[] => [e, ...(hop ?? []).map((r) => ({ id: r.target_id, type: r.target_type }))]
const logsSpec = (tf: Timeframe, ents: Entity[]) => tfSpec(tf, logsQuery([signalFilterAll(ents)], 500))
const tracesSpec = (tf: Timeframe, e: Entity, lens: 'all' | 'errors') =>
  tfSpec(tf, spansQuery(lens === 'errors' ? 'errors' : e.type === 'SERVICE' ? 'roots' : 'all', [spanFilter(e)], 300))
const eventsSpec = (tf: Timeframe, e: Entity) => tfSpec(tf, `fetch events\n| filter ${signalFilter(e)}\n| sort timestamp desc\n| limit 300`)
const relatedSpec = (id: string): DqlSpec => ({ query: edgesQuery(id), ttl: 120 })

type Tab = 'overview' | 'metrics' | 'logs' | 'traces' | 'sessions' | 'rumerrors' | 'events' | 'problems' | 'related'

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
  const resolved = useResolved(id)
  const name: string = (rec && displayName(rec) !== rec.id ? displayName(rec) : '') || search.get('n') || resolved?.name || id
  const entity: Entity = useMemo(() => ({ id, type, name: rec?.name || search.get('n') || undefined }), [id, type, rec?.name])

  useTitle(name)
  useEffect(() => {
    if (rec) pushRecent({ href: entityHref(id, rec.name), label: rec.name || id, kind: shortType(type) })
  }, [rec, id, type])

  const { data: meta } = useMeta()
  const tf = useTimeframe()

  // health strip
  const probSpec: DqlSpec = { query: problemsQuery({ entityId: id, limit: 50 }), from: tf.ms < 86400e3 ? 'now-24h' : tf.from, to: tf.to, ttl: 30 }
  const problems = useDql(probSpec)
  const active = problems.data?.records.filter((r) => r.status === 'ACTIVE') ?? []
  const lastChange = useDql({ query: changesQuery(1, signalFilter(entity)), from: 'now-7d', ttl: 60 })
  const change = lastChange.data?.records[0]

  // Every metric carrying this entity's Smartscape dimension (cloud resources etc.).
  const classic = resolved?.source === 'classic'
  const metricsSpec = classic ? null : tfSpec(tf, metricDiscoveryQuery(entity), { ttl: 300 })
  const metrics = useDql(metricsSpec)
  const curated = vitalsFor(type)
  const vitals = curated.length ? curated : headlineVitals((metrics.data?.records ?? []).map((r) => r['metric.key']))
  const canSpans = spanScopable(type)
  const isFrontend = type === 'FRONTEND'

  // Every tab's query runs now (one streamed batch): counts + empty states.
  const hop = useDql(isFrontend ? null : hopSpec(entity))
  const tabLogsSpec = isFrontend || (type === 'SERVICE' && !hop.data && !hop.error) ? null : logsSpec(tf, logEntities(entity, hop.data?.records))
  const tabTracesSpec = canSpans ? tracesSpec(tf, entity, 'all') : null
  const tabEventsSpec = eventsSpec(tf, entity)
  const tabSessionsSpec = isFrontend ? sessionsSpec(tf, true, id, 'all') : null
  const tabErrorsSpec = isFrontend ? errorGroupsSpec(tf, true, id) : null
  const tabs = {
    logs: useDql(tabLogsSpec),
    traces: useDql(tabTracesSpec),
    events: useDql(tabEventsSpec),
    related: useDql(relatedSpec(id)),
    sessions: useDql(tabSessionsSpec),
    rumerrors: useDql(tabErrorsSpec),
  }

  if (detail.error)
    return (
      <DetailFallback {...sectionOf(type)}>
        <ErrorBox error={detail.error} />
      </DetailFallback>
    )

  return (
    <EntityLayout>
      <BackLink {...sectionOf(type)} />
      <DetailHeader
        icon={<TypeIcon type={type} />}
        title={detail.isLoading && !search.get('n') ? <Skeleton className="h-7 w-72" /> : name}
        badges={<Badge tone="accent">{shortType(type)}</Badge>}
        meta={
          <>
            <IdCopy id={id} label="entity ID" />
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
          </>
        }
        actions={<OpenInDynatrace href={meta?.environment && dtLinks.entity(meta.environment, id, type)} />}
      />

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
        <DataTabs
          value={tab}
          onChange={setTab}
          tabs={[
            { value: 'overview', label: 'Overview', spec: detailSpec, result: detail, count: null },
            { value: 'metrics', label: 'Metrics', hidden: classic, spec: metricsSpec, result: metrics, limit: 120 },
            { value: 'sessions', label: 'Sessions', hidden: !isFrontend, spec: tabSessionsSpec, result: tabs.sessions, limit: 500 },
            { value: 'rumerrors', label: 'Errors', hidden: !isFrontend, spec: tabErrorsSpec, result: tabs.rumerrors, limit: 300 },
            { value: 'logs', label: 'Logs', hidden: isFrontend, spec: tabLogsSpec, result: tabs.logs, limit: 500 },
            { value: 'traces', label: 'Traces', hidden: !canSpans, spec: tabTracesSpec, result: tabs.traces, limit: 300 },
            { value: 'events', label: 'Events', spec: tabEventsSpec, result: tabs.events, limit: 300 },
            { value: 'problems', label: 'Problems', spec: probSpec, result: problems },
            { value: 'related', label: 'Related', spec: relatedSpec(id), result: tabs.related },
          ]}
        />
        <div className="min-h-[420px]">
          {tab === 'overview' && <Overview rec={rec} loading={detail.isLoading} type={type} classic={resolved?.source === 'classic'} />}
          {tab === 'metrics' && <EntityMetrics entity={entity} discovery={metrics} />}
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
  return <div className="p-5">{children}</div>
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
        <div className="grid h-[120px] place-items-center px-4 text-center text-xs text-ink-4" title={v.key}>
          No data in this timeframe
        </div>
      ) : (
        <div className="p-2 pl-0">
          <TimeChart
            x={tsAxis(r, 'v')}
            series={[{ label: v.title, values: vals, color: v.key.includes('fail') ? '--crit' : '--s1' }]}
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

function Overview({ rec, loading, type, classic }: { rec: Rec | undefined; loading: boolean; type: string; classic?: boolean }) {
  if (loading) return <SkeletonRows rows={8} />
  if (!rec && classic)
    return (
      <Empty
        title="Classic entity"
        hint="This ID comes from the classic entity model (dt.entity.*), not Smartscape, so there is no topology record. Its logs, events and problems still work in the tabs above."
      />
    )
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
        <EntityTags rec={rec} type={type} />
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

// Groups whose values are mostly machine noise start collapsed.
const QUIET_GROUPS = new Set(['Kubernetes annotations'])

/**
 * Tags, labels and primary tags, grouped by where they come from. Each one
 * links to every entity of this type carrying the same tag — the "what else
 * is tagged like this?" pivot.
 */
function EntityTags({ rec, type }: { rec: Rec; type: string }) {
  const groups = useMemo(() => {
    const tags: { field: string; group: string; name: string; value: string }[] = []
    for (const [k, v] of Object.entries(rec)) {
      if (k.startsWith('tags:') && v && typeof v === 'object' && !Array.isArray(v)) {
        for (const [key, val] of Object.entries(v as Record<string, unknown>)) {
          if (val == null || typeof val === 'object') continue
          const field = mapField(k, key)
          tags.push({ field, ...describeField(field), value: String(val) })
        }
      } else if (k.startsWith('primary_tags.') && v != null && v !== '' && typeof v !== 'object') {
        tags.push({ field: k, ...describeField(k), value: String(v) })
      }
    }
    const by = new Map<string, typeof tags>()
    for (const t of tags) by.set(t.group, [...(by.get(t.group) ?? []), t])
    return [...by]
      .map(([group, items]) => ({ group, items: items.sort((a, b) => a.name.localeCompare(b.name)) }))
      .sort((a, b) => groupRank(a.group) - groupRank(b.group) || a.group.localeCompare(b.group))
  }, [rec])
  if (!groups.length) return null
  const noun = shortType(type)
  return (
    <div className="mt-5 space-y-3">
      {groups.map(({ group, items }) => {
        const chips = (
          <div className="flex flex-wrap gap-1">
            {items.map((t) => (
              <Link
                key={t.field}
                href={listHref(type, t.field, t.value)}
                title={`Every ${noun} with ${t.name} = ${t.value}`}
                className="inline-flex max-w-full min-w-0 items-center rounded-md border border-line bg-sunken px-1.5 py-0.5 font-mono text-2xs hover:border-accent/60 hover:bg-accent-wash"
              >
                <span className="shrink-0 text-ink-3">{t.name}</span>
                <span className="px-0.5 text-ink-4">=</span>
                <span className="truncate text-ink-2">{t.value}</span>
              </Link>
            ))}
          </div>
        )
        const head = (
          <>
            {group} <span className="text-ink-4">{items.length}</span>
          </>
        )
        return QUIET_GROUPS.has(group) ? (
          <details key={group}>
            <summary className="mb-1.5 cursor-pointer text-2xs font-medium tracking-wide text-ink-3 uppercase hover:text-ink-2">{head}</summary>
            {chips}
          </details>
        ) : (
          <div key={group}>
            <div className="mb-1.5 text-2xs font-medium tracking-wide text-ink-3 uppercase">{head}</div>
            {chips}
          </div>
        )
      })}
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
  const hop = useDql(hopSpec(entity))
  const ready = entity.type !== 'SERVICE' || hop.data || hop.error
  const ents = logEntities(entity, hop.data?.records)
  const spec = ready ? logsSpec(tf, ents) : null
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
        <SidePanel title="Log record" onClose={() => setSel(null)}>
          <LogDetail rec={sel} />
        </SidePanel>
      )}
    </div>
  )
}

function EntityTraces({ entity }: { entity: Entity }) {
  const tf = useTimeframe()
  const [lens, setLens] = useState<'all' | 'errors'>('all')
  const spec = tracesSpec(tf, entity, lens)
  const res = useDql(spec)
  return (
    <div className="flex h-[520px] flex-col">
      <div className="flex h-9 items-center gap-3 border-b border-line px-3 text-xs">
        {(['all', 'errors'] as const).map((l) => (
          <button key={l} type="button" onClick={() => setLens(l)} className={clsx(lens === l ? 'text-ink' : 'text-ink-3 hover:text-ink-2')}>
            {l === 'all' ? 'Requests' : 'Failed'}
          </button>
        ))}
      </div>
      {res.error ? <ErrorBox error={res.error} /> : <SpanTable records={res.data?.records} loading={res.isLoading} className="flex-1" />}
    </div>
  )
}

function EntityEvents({ entity }: { entity: Entity }) {
  const tf = useTimeframe()
  const res = useDql(eventsSpec(tf, entity))
  const [sel, setSel] = useState<Rec | null>(null)
  return (
    <div className="flex h-[520px]">
      <div className="min-w-0 flex-1">
        {res.error ? <ErrorBox error={res.error} /> : res.isLoading ? <SkeletonRows /> : <EventList records={res.data?.records ?? []} onSelect={setSel} />}
      </div>
      {sel && (
        <SidePanel title={sel['event.name'] ?? 'Event'} onClose={() => setSel(null)}>
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
  const edges = useDql(relatedSpec(id))
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

/** The list an entity type belongs to (where "back" goes without history). */
function sectionOf(type: string): { fallback: string; label: string } {
  if (type === 'SERVICE') return { fallback: '/services', label: 'Services' }
  if (type === 'HOST') return { fallback: '/hosts', label: 'Hosts' }
  if (type.startsWith('K8S_') || type === 'CONTAINER') return { fallback: '/k8s', label: 'Kubernetes' }
  if (type === 'FRONTEND') return { fallback: '/rum', label: 'Experience' }
  if (type.startsWith('GENAI_')) return { fallback: '/ai', label: 'AI' }
  return { fallback: `/smartscape?type=${encodeURIComponent(type)}`, label: shortType(type) }
}
