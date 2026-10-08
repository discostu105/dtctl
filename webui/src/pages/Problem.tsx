import clsx from 'clsx'
import { AlertOctagon, ArrowLeft, ExternalLink, Lightbulb, Sparkles } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { Link } from 'wouter'
import { TimeChart, tsAxis } from '../components/Chart'
import { EntityChip } from '../components/Entity'
import { Markdown } from '../components/Markdown'
import { DataTabs } from '../components/DataTabs'
import { Panel } from '../components/Panel'
import { Inspector, LogDetail, LogStream, ProblemStatus, SidePanel, SpanTable } from '../components/signals'
import { Badge, CopyButton, Empty, ErrorBox, Facts, Skeleton, SkeletonRows, TimeAgo, useNow } from '../components/ui'
import { arr, num, useDql, useMeta, type Rec } from '../lib/api'
import { evidenceQuery, logsQuery, problemDetailQuery, signalFilterAll, spanFilter, spanScopable, spansQuery, type Entity } from '../lib/dql'
import { fmtCompact, fmtDateTime, span, titleCase } from '../lib/format'
import { dtLinks } from '../lib/links'
import { ERROR_LEVELS } from '../lib/shared'
import { useNames } from '../lib/names'
import { pushRecent, useTitle } from '../lib/store'
import { intervalFor } from '../lib/timeframe'

export default function Problem({ id }: { id: string }) {
  const res = useDql({ query: problemDetailQuery(id), ttl: 15 })
  const p = res.data?.records[0]
  useTitle(p ? `${id} · ${p['event.name']}` : id)
  useEffect(() => {
    if (p) pushRecent({ href: `/problems/${id}`, label: `${id} ${p['event.name']}`, kind: 'Problem' })
  }, [p, id])

  if (res.error) return <ErrorBox error={res.error} />
  if (res.isLoading)
    return (
      <div className="p-5">
        <Skeleton className="mb-3 h-7 w-96" />
        <Skeleton className="mb-6 h-4 w-64" />
        <Skeleton className="h-64" />
      </div>
    )
  if (!p) return <Empty title={`Problem ${id} not found`} hint="It may be older than 30 days or the ID may be mistyped." />
  return <ProblemView p={p} id={id} />
}

function ProblemView({ p, id }: { p: Rec; id: string }) {
  const { data: meta } = useMeta()
  const now = useNow(1000)
  const active = p['event.status'] === 'ACTIVE'
  const start = Date.parse(p['event.start'])
  const end = active ? now : Date.parse(p['event.end'])

  const affected: Entity[] = useMemo(
    () =>
      arr(p['smartscape.affected_entities']).map((e: Rec) => ({ id: e.id, type: e.type, name: e.name })).filter((e: Entity) => e.id) as Entity[],
    [p],
  )
  const related: Entity[] = useMemo(() => arr(p['smartscape.related_entities']).map((e: Rec) => ({ id: e.id, type: e.type, name: e.name })), [p])

  // The problem window, padded — the global timeframe deliberately doesn't apply.
  const win = useMemo(() => {
    const f = new Date(start - 30 * 60e3)
    const t = new Date((active ? Date.now() : end) + 10 * 60e3)
    return { from: f.toISOString(), to: t.toISOString(), ms: t.getTime() - f.getTime() }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [p])

  const sigFilter = affected.length ? signalFilterAll(affected) : ''
  const spanEnts = affected.filter((e) => spanScopable(e.type))
  const svcIds = affected.filter((e) => e.type === 'SERVICE').map((e) => e.id)
  const iv = intervalFor(win.ms)

  const errSpec = sigFilter
    ? { query: `fetch logs\n| filter ${sigFilter}\n| filter in(loglevel, ${ERROR_LEVELS})\n| makeTimeseries count = count(), interval:${iv}`, from: win.from, to: win.to }
    : null
  const failSpec = svcIds.length
    ? {
        query: `timeseries failed = sum(dt.service.request.failure_count, default:0), interval:${iv}, filter:{ in(dt.smartscape.service, {${svcIds.map((i) => `toSmartscapeId("${i}")`).join(', ')}}) }`,
        from: win.from,
        to: win.to,
      }
    : null
  const errs = useDql(errSpec)
  const fails = useDql(failSpec)

  const chart = useMemo(() => {
    const e = errs.data?.records[0]
    const f = fails.data?.records[0]
    const base = f ?? e
    if (!base) return null
    const x = tsAxis(base, f ? 'failed' : 'count')
    const series = []
    if (f) series.push({ label: 'Failed requests', values: f.failed, color: '--s1' })
    if (e) series.push({ label: 'Error logs', values: e.count, color: '--s2' })
    return { x, series }
  }, [errs.data, fails.data])

  const eventIds = arr(p['dt.davis.event_ids'])
  const evidence = useDql(eventIds.length ? { query: evidenceQuery(eventIds) } : null)

  const [tab, setTab] = useState<'logs' | 'traces' | 'events' | 'raw'>('logs')
  const logsSpec = sigFilter ? { query: logsQuery([sigFilter], 500), from: win.from, to: win.to } : null
  const spansSpec = spanEnts.length
    ? { query: spansQuery('all', [spanEnts.map((e) => `(${spanFilter(e)})`).join(' or ')], 300), from: win.from, to: win.to }
    : null
  const eventsSpec = sigFilter
    ? { query: `fetch events\n| filter ${sigFilter}\n| sort timestamp desc\n| limit 200`, from: win.from, to: win.to }
    : null
  // All tab queries run up front: counts, empty states, instant switching.
  const logs = useDql(logsSpec)
  const spans = useDql(spansSpec)
  const events = useDql(eventsSpec)
  const [sel, setSel] = useState<Rec | null>(null)

  const tags: string[] = arr(p.entity_tags)
  const names = useNames([...related.map((e) => e.id), ...(evidence.data?.records ?? []).map((e) => e.entity_id)])

  return (
    <div className="flex h-full">
      <div className="min-w-0 flex-1 overflow-auto p-5">
        <Link href="/problems" className="mb-3 inline-flex items-center gap-1 text-xs text-ink-3 hover:text-ink-2">
          <ArrowLeft className="size-3.5" /> Problems
        </Link>

        {/* header */}
        <div className="mb-5 flex flex-wrap items-start gap-4">
          <div
            className={clsx(
              'flex size-10 items-center justify-center rounded-xl',
              active ? 'bg-crit-wash text-crit' : 'bg-line text-ink-3',
            )}
          >
            <AlertOctagon className="size-5" />
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="text-xl font-semibold tracking-tight">{p['event.name']}</h1>
              <ProblemStatus status={p['event.status']} />
            </div>
            <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-ink-3">
              <span className="inline-flex items-center gap-1 font-mono text-xs">
                {id}
                <CopyButton value={id} label="problem ID" />
              </span>
              <span>{titleCase(String(p['event.category'] ?? ''))}</span>
              <span>
                started {fmtDateTime(p['event.start'])} (<TimeAgo value={p['event.start']} />)
              </span>
              <span className={clsx('tnum', active && 'text-crit')}>
                {active ? 'ongoing for ' : 'lasted '}
                {span(start, end)}
              </span>
              {arr(p['dt.davis.impact_level']).map((l: string) => (
                <Badge key={l}>{l} impact</Badge>
              ))}
            </div>
          </div>
          {meta?.environment && (
            <a
              href={dtLinks.problem(meta.environment, p['event.id'])}
              target="_blank"
              rel="noreferrer"
              className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-line bg-sunken px-3 text-sm text-ink-2 hover:border-line-strong hover:text-ink"
            >
              Open in Dynatrace <ExternalLink className="size-3.5" />
            </a>
          )}
        </div>

        <div className="grid grid-cols-3 gap-4 max-xl:grid-cols-1">
          <div className="col-span-2 flex flex-col gap-4 max-xl:col-span-1">
            <Panel
              title="Impact timeline"
              hint="problem window shaded"
              spec={failSpec ?? errSpec}
              result={failSpec ? fails : errs}
              actions={
                chart && (
                  <div className="mr-2 flex items-center gap-3 text-xs text-ink-2">
                    {chart.series.map((s) => (
                      <span key={s.label} className="flex items-center gap-1.5">
                        <i className="inline-block h-0.5 w-3 rounded" style={{ background: `var(${s.color})` }} />
                        {s.label}
                      </span>
                    ))}
                  </div>
                )
              }
            >
              {!errSpec && !failSpec ? (
                <Empty title="No signal scope" hint="This problem carries no Smartscape entities to scope signals to." />
              ) : !chart ? (
                errs.isLoading || fails.isLoading ? <Skeleton className="m-3 h-[160px]" /> : <Empty title="No errors recorded in the window" />
              ) : (
                <div className="p-3 pl-1">
                  <TimeChart x={chart.x} series={chart.series} height={170} format={fmtCompact} band={[start / 1000, end / 1000]} />
                </div>
              )}
            </Panel>

            <Panel
              title={
                <span className="flex items-center gap-1.5">
                  <Sparkles className="size-3.5 text-accent" /> Davis analysis
                </span>
              }
            >
              <div className="p-4">
                {p['event.description'] ? <Markdown text={String(p['event.description'])} /> : <span className="text-sm text-ink-3">No description.</span>}
              </div>
            </Panel>

            <div className="rounded-xl border border-line bg-panel">
              <DataTabs
                value={tab}
                onChange={(t) => {
                  setTab(t)
                  setSel(null)
                }}
                tabs={[
                  { value: 'logs', label: 'Logs', spec: logsSpec, result: logs, limit: 500 },
                  { value: 'traces', label: 'Traces', hidden: !spansSpec, spec: spansSpec, result: spans, limit: 300 },
                  { value: 'events', label: 'Events', spec: eventsSpec, result: events, limit: 200 },
                  { value: 'raw', label: 'Raw record' },
                ]}
              />
              <div className="h-[420px]">
                {tab === 'logs' &&
                  (logs.error ? <ErrorBox error={logs.error} /> : <LogStream records={logs.data?.records} loading={logs.isLoading} onSelect={setSel} selected={sel} className="h-full" />)}
                {tab === 'traces' && (spans.error ? <ErrorBox error={spans.error} /> : <SpanTable records={spans.data?.records} loading={spans.isLoading} className="h-full" />)}
                {tab === 'events' &&
                  (events.error ? (
                    <ErrorBox error={events.error} />
                  ) : events.isLoading ? (
                    <SkeletonRows />
                  ) : (
                    <EventList records={events.data?.records ?? []} onSelect={setSel} />
                  ))}
                {tab === 'raw' && (
                  <div className="h-full overflow-auto p-3">
                    <Inspector rec={p} />
                  </div>
                )}
              </div>
            </div>
          </div>

          <div className="flex flex-col gap-4">
            <Panel title="Affected">
              <div className="flex flex-wrap gap-1.5 p-3">
                {affected.length ? affected.map((e) => <EntityChip key={e.id} id={e.id} name={e.name} type={e.type} />) : <span className="text-sm text-ink-3">—</span>}
              </div>
              {related.length > 0 && (
                <>
                  <div className="border-t border-line px-3 pt-2.5 text-2xs font-medium tracking-wide text-ink-3 uppercase">Related</div>
                  <div className="flex flex-wrap gap-1.5 p-3">
                    {related.map((e) => (
                      <EntityChip key={e.id} id={e.id} name={e.name ?? names.get(e.id) ?? relatedName(p, e.id)} type={e.type} />
                    ))}
                  </div>
                </>
              )}
            </Panel>

            <Panel title="Evidence" spec={eventIds.length ? { query: evidenceQuery(eventIds) } : null} result={evidence}>
              {evidence.isLoading ? (
                <SkeletonRows rows={3} />
              ) : !evidence.data?.records.length ? (
                <Empty title="No evidence events" />
              ) : (
                <ul className="divide-y divide-line">
                  {evidence.data.records.map((e) => (
                    <li key={e['event.id']} className="px-3 py-2">
                      <div className="flex items-center gap-2">
                        {e.root && <Lightbulb className="size-3.5 shrink-0 text-warn" />}
                        <span className="truncate text-sm">{e.name}</span>
                      </div>
                      <div className="mt-0.5 flex flex-wrap items-center gap-2 text-xs text-ink-3">
                        {e.entity_id ? <EntityChip id={e.entity_id} name={e.entity ?? names.get(e.entity_id)} /> : e.entity && <span>{e.entity}</span>}
                        <TimeAgo value={e.start} />
                        {e.root && <Badge tone="warn">root-cause relevant</Badge>}
                      </div>
                    </li>
                  ))}
                </ul>
              )}
            </Panel>

            <Panel title="Facts">
              <div className="p-3">
                <Facts
                  items={[
                    ['Event ID', <span className="font-mono text-xs">{p['event.id']}</span>],
                    ['Root cause', p.root_cause_entity_name],
                    ['Detected by', arr(p['event.provider']).join(', ')],
                    ['Severity', p['event.severity']],
                    ['Muted', p['dt.davis.mute.status'] === 'MUTED' ? 'yes' : 'no'],
                    ['Maintenance', p['maintenance.is_under_maintenance'] ? 'yes' : 'no'],
                    ['Frequent', p['dt.davis.is_frequent_event'] ? 'yes' : 'no'],
                    ['Ended', active ? null : fmtDateTime(p['event.end'])],
                  ]}
                />
                {tags.length > 0 && (
                  <div className="mt-3 flex flex-wrap gap-1">
                    {tags.map((t) => (
                      <Badge key={t} mono>
                        {t}
                      </Badge>
                    ))}
                  </div>
                )}
              </div>
            </Panel>
          </div>
        </div>
      </div>
      {sel && (
        <SidePanel title={sel.content != null ? 'Log record' : sel['event.name'] ?? 'Record'} onClose={() => setSel(null)}>
          {sel.content != null ? <LogDetail rec={sel} /> : <Inspector rec={sel} />}
        </SidePanel>
      )}
    </div>
  )
}

function relatedName(p: Rec, id: string) {
  const ids = arr(p.related_entity_ids)
  const names = arr(p.related_entity_names)
  const i = ids.indexOf(id)
  return i >= 0 ? names[i] : undefined
}

export function EventList({ records, onSelect }: { records: Rec[]; onSelect?: (r: Rec) => void }) {
  if (!records.length) return <Empty title="No events" hint="No events recorded for this scope and window." />
  return (
    <ul className="h-full divide-y divide-line overflow-auto">
      {records.map((e, i) => {
        const sev = num(e['event.severity'])
        return (
          <li key={i}>
            <button type="button" onClick={() => onSelect?.(e)} className="flex w-full items-center gap-3 px-3 py-2 text-left hover:bg-panel-hover">
              <span className="tnum w-24 shrink-0 text-xs text-ink-3">
                <TimeAgo value={e.timestamp} />
              </span>
              <Badge tone={e['event.kind'] === 'DAVIS_PROBLEM' ? 'crit' : sev <= 3 ? 'warn' : 'muted'} className="w-28 justify-center">
                {String(e['event.kind'] ?? '').replace(/_EVENT$/, '').toLowerCase()}
              </Badge>
              <span className="min-w-0 flex-1 truncate text-sm">{e['event.name'] ?? e['event.type']}</span>
              <span className="truncate text-xs text-ink-3">{e['event.type']}</span>
            </button>
          </li>
        )
      })}
    </ul>
  )
}

