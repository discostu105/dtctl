import clsx from 'clsx'
import { Kpi } from '../components/Kpi'
import { AlertOctagon, ArrowUpRight, CheckCircle2, GitCommitVertical, MonitorSmartphone, ScrollText, Share2, ShieldAlert } from 'lucide-react'
import { useMemo, type ReactNode } from 'react'
import { Link } from 'wouter'
import { TimeChart, tsAxis, Legend } from '../components/Chart'
import { EntityLink } from '../components/Entity'
import { Panel } from '../components/Panel'
import { ProblemStatus, RiskBadge } from '../components/signals'
import { Spark } from '../components/Spark'
import { Badge, Empty, ErrorBox, Skeleton, SkeletonRows, TimeAgo, Tip, useNow } from '../components/ui'
import { num, useDql, useMeta, type Rec } from '../lib/api'
import { fmtCompact, fmtInt, fmtPct, fmtUs, span } from '../lib/format'
import { problemHref, vulnHref } from '../lib/links'
import { activeProblemsSpec, changesSpec, recentProblemsSpec, ERROR_LEVELS, servicesSpec, tfSpec, vulnsSpec } from '../lib/shared'
import { useAdaptiveDql } from '../lib/sampling'
import { useTitle } from '../lib/store'
import { FrontendsTable, frontendsSpec, VitalsLegend } from './Rum'
import { absolute, intervalFor, setTimeframe, sparkInterval, useTimeframe } from '../lib/timeframe'

function greeting() {
  const h = new Date().getHours()
  return h < 5 ? 'Up late' : h < 12 ? 'Good morning' : h < 18 ? 'Good afternoon' : 'Good evening'
}

export default function Pulse() {
  useTitle('Pulse')
  const tf = useTimeframe()
  const { data: meta } = useMeta()
  const now = useNow(30_000)

  const problems = useDql(activeProblemsSpec, { refetchInterval: 60_000 })
  const services = useDql(servicesSpec(tf))
  const vulns = useDql(vulnsSpec(tf))
  const changes = useDql(changesSpec(tf))
  const iv = intervalFor(tf.ms)
  const errSpec = tfSpec(tf, `fetch logs\n| filter in(loglevel, ${ERROR_LEVELS})\n| makeTimeseries count = count(), interval:${iv}`)
  // error-log trends count every log record: adaptively sampled on big tenants
  const errA = useAdaptiveDql('logs', errSpec, ['count'])
  const errLogs = errA.res
  const failSpec = tfSpec(tf, `timeseries failed = sum(dt.service.request.failure_count, default:0), interval:${iv}`)
  const failed = useDql(failSpec)
  const errSpark = useAdaptiveDql('logs', tfSpec(tf, `fetch logs\n| filter in(loglevel, ${ERROR_LEVELS})\n| makeTimeseries count = count(), interval:${sparkInterval(tf.ms)}`), ['count']).res

  const svcRows = services.data?.records ?? []
  const failing = svcRows.filter((r) => num(r.failed) > 0)
  const vulnRows = vulns.data?.records ?? []
  const critVulns = vulnRows.filter((r) => r.level === 'CRITICAL' || r.level === 'HIGH')
  const deploys = (changes.data?.records ?? []).filter((r) => r['event.type'] === 'deployment' || /deploy/i.test(String(r.what ?? '')))
  const errTotal = (errSpark.data?.records[0]?.count as number[] | undefined)?.reduce((a, b) => a + (b || 0), 0)
  const nActive = problems.data?.records.length

  const chart = useMemo(() => {
    const e = errLogs.data?.records[0]
    const f = failed.data?.records[0]
    const base = e ?? f
    if (!base) return null
    const x = tsAxis(base, e ? 'count' : 'failed')
    return {
      x,
      series: [
        ...(e ? [{ label: 'Error logs', values: (e.count as number[]).map((v) => v ?? 0), color: '--warn' }] : []),
        ...(f ? [{ label: 'Failed requests', values: alignTo(x, f, 'failed'), color: '--crit' }] : []),
      ],
    }
  }, [errLogs.data, failed.data])

  const markers = useMemo(
    () =>
      deploys
        .map((d) => ({ t: Date.parse(d.timestamp) / 1000, label: `${d.what}${d.outcome ? ` · ${d.outcome}` : ''}` }))
        .filter((m) => Number.isFinite(m.t)),
    [deploys],
  )

  const first = meta?.userName?.split(/\s+/)[0]
  const allClear = nActive === 0 && failing.length === 0

  return (
    <div className="p-5">
      <div className="mb-5 flex flex-wrap items-end gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">
            {greeting()}
            {first ? `, ${first}` : ''}.
          </h1>
          <div className="mt-0.5 flex items-center gap-1.5 text-sm text-ink-3">
            {nActive == null ? (
              'Checking your environment…'
            ) : allClear ? (
              <>
                <CheckCircle2 className="size-4 text-ok" /> All clear: no active problems, no failing services · {tf.label.toLowerCase()}
              </>
            ) : (
              <>
                {nActive > 0 ? `${fmtInt(nActive)} active problem${nActive === 1 ? '' : 's'}` : 'No active problems'}
                {failing.length > 0 && ` · ${fmtInt(failing.length)} failing service${failing.length === 1 ? '' : 's'}`} · {tf.label.toLowerCase()}
              </>
            )}
          </div>
        </div>
      </div>

      {/* KPI strip */}
      <div className="mb-4 grid grid-cols-5 gap-3 max-xl:grid-cols-3 max-md:grid-cols-2">
        <Kpi
          href="/problems"
          icon={<AlertOctagon className="size-4" />}
          label="Active problems"
          sub="last 24 hours"
          value={nActive}
          tone={nActive ? 'crit' : 'ok'}
          loading={problems.isLoading}
        />
        <Kpi
          href="/services"
          icon={<Share2 className="size-4" />}
          label="Failing services"
          sub={`of ${svcRows.length || '—'} with traffic`}
          value={services.data ? failing.length : undefined}
          tone={failing.length ? 'warn' : 'ok'}
          loading={services.isLoading}
        />
        <Kpi
          href="/logs?level=error"
          icon={<ScrollText className="size-4" />}
          label="Error logs"
          sub={tf.label.toLowerCase()}
          value={errTotal}
          loading={errSpark.isLoading}
          spark={errSpark.data?.records[0]?.count}
          sparkColor="var(--s2)"
        />
        <Kpi
          href="/security"
          icon={<ShieldAlert className="size-4" />}
          label="Critical & high vulns"
          sub={`${fmtInt(vulnRows.length)} open in total`}
          value={vulns.data ? critVulns.length : undefined}
          tone={critVulns.length ? 'warn' : 'ok'}
          loading={vulns.isLoading}
        />
        <Kpi
          href="/changes"
          icon={<GitCommitVertical className="size-4" />}
          label="Deployments"
          sub={deploys[0] ? <>latest <TimeAgo value={deploys[0].timestamp} /></> : tf.label.toLowerCase()}
          value={changes.data ? deploys.length : undefined}
          loading={changes.isLoading}
        />
      </div>

      <div className="grid grid-cols-3 gap-4 max-xl:grid-cols-1">
        <Panel
          className="col-span-2 max-xl:col-span-1"
          title="Active problems"
          icon={<AlertOctagon className="size-4" />}
          spec={activeProblemsSpec}
          result={problems}
          actions={<HeaderLink href="/problems">All problems</HeaderLink>}
        >
          {problems.error ? (
            <ErrorBox error={problems.error} />
          ) : problems.isLoading ? (
            <SkeletonRows rows={4} />
          ) : !problems.data?.records.length ? (
            <AllQuiet now={now} />
          ) : (
            <ul className="divide-y divide-line">
              {problems.data.records.slice(0, 6).map((r) => (
                <ProblemRow key={r.display_id} r={r} now={now} />
              ))}
            </ul>
          )}
        </Panel>

        <Panel
          title="Recent changes"
          icon={<GitCommitVertical className="size-4" />}
          spec={changesSpec(tf)}
          result={changes}
          actions={<HeaderLink href="/changes">All changes</HeaderLink>}
          bodyClassName="max-h-[330px] overflow-auto"
        >
          {changes.error ? (
            <ErrorBox error={changes.error} />
          ) : changes.isLoading ? (
            <SkeletonRows rows={5} />
          ) : !changes.data?.records.length ? (
            <Empty title="No changes" hint="No deployments or config changes recorded in this timeframe." />
          ) : (
            <ol className="relative px-3 py-2">
              {changes.data.records.slice(0, 12).map((r, i) => (
                <ChangeItem key={i} r={r} />
              ))}
            </ol>
          )}
        </Panel>

        <Panel
          className="col-span-3 max-xl:col-span-1"
          title="Errors & deployments"
          hint="drag to zoom · ◆ = deployment"
          icon={<ScrollText className="size-4" />}
          spec={errA.spec ?? errSpec}
          result={errLogs}
          actions={
            chart && (
              <Legend className="mr-2" swatch="line" items={chart.series.map((s) => ({ label: s.label, color: s.color! }))}>
                {markers.length > 0 && (
                  <span className="flex items-center gap-1.5">
                    <i className="inline-block h-2.5 w-0 border-l border-dashed border-accent" />
                    Deployments
                  </span>
                )}
              </Legend>
            )
          }
        >
          {errLogs.error ? (
            <ErrorBox error={errLogs.error} />
          ) : !chart ? (
            <Skeleton className="m-3 h-[180px]" />
          ) : (
            <div className="p-3 pl-1">
              <TimeChart
                x={chart.x}
                series={chart.series}
                height={190}
                format={fmtCompact}
                markers={markers}
                onZoom={(a, b) => setTimeframe(absolute(a, b))}
              />
            </div>
          )}
        </Panel>

        <Panel
          className="col-span-2 max-xl:col-span-1"
          title="Services needing attention"
          icon={<Share2 className="size-4" />}
          spec={servicesSpec(tf)}
          result={services}
          actions={<HeaderLink href="/services">All services</HeaderLink>}
        >
          {services.error ? (
            <ErrorBox error={services.error} />
          ) : services.isLoading ? (
            <SkeletonRows rows={5} />
          ) : (
            <ServiceAttention rows={svcRows} />
          )}
        </Panel>

        <Panel
          title="Top vulnerabilities"
          icon={<ShieldAlert className="size-4" />}
          spec={vulnsSpec(tf)}
          result={vulns}
          actions={<HeaderLink href="/security">All</HeaderLink>}
        >
          {vulns.error ? (
            <ErrorBox error={vulns.error} />
          ) : vulns.isLoading ? (
            <SkeletonRows rows={5} />
          ) : !vulnRows.length ? (
            <Empty icon={<CheckCircle2 className="size-5 text-ok" />} title="No open vulnerabilities" />
          ) : (
            <ul className="divide-y divide-line">
              {vulnRows.slice(0, 6).map((r) => (
                <li key={r['vulnerability.id']}>
                  <Link href={vulnHref(r['vulnerability.id'])} className="flex items-center gap-2.5 px-3 py-2 hover:bg-panel-hover">
                    <RiskBadge level={r.level} score={r.score} />
                    <span className="min-w-0 flex-1 truncate text-sm">{r.title}</span>
                    {r.exposure === 'PUBLIC_NETWORK' && (
                      <Tip content="Reachable from the public internet">
                        <Badge tone="crit">public</Badge>
                      </Tip>
                    )}
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </Panel>

        <Panel
          className="col-span-3 max-xl:col-span-1"
          title="User experience"
          hint="real users · p75 Core Web Vitals"
          icon={<MonitorSmartphone className="size-4" />}
          spec={frontendsSpec(tf)}
          actions={
            <>
              <VitalsLegend />
              <HeaderLink href="/rum">Experience</HeaderLink>
            </>
          }
        >
          <FrontendsTable tf={tf} realOnly compact maxHeight={300} />
        </Panel>
      </div>
    </div>
  )
}

/** Align a timeseries onto another's x axis (series can start at different buckets). */
function alignTo(x: number[], rec: Rec, field: string): (number | null)[] {
  const xs = tsAxis(rec, field)
  const vals = rec[field] as number[]
  const m = new Map(xs.map((t, i) => [Math.round(t), vals[i]]))
  return x.map((t) => m.get(Math.round(t)) ?? null)
}

function HeaderLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <Link href={href} className="inline-flex h-6 items-center gap-0.5 rounded-md px-1.5 text-xs text-ink-3 hover:bg-line hover:text-ink-2">
      {children}
      <ArrowUpRight className="size-3" />
    </Link>
  )
}


function ProblemRow({ r, now }: { r: Rec; now: number }) {
  const ids: string[] = Array.isArray(r.affected_ids) ? r.affected_ids : []
  const names: string[] = Array.isArray(r.affected) ? r.affected : []
  return (
    <li>
      <Link href={problemHref(r.display_id)} className="flex items-center gap-3 px-3 py-2.5 hover:bg-panel-hover">
        <ProblemStatus status={r.status} />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="truncate font-medium">{r.name}</span>
            <span className="font-mono text-2xs text-ink-3">{r.display_id}</span>
          </div>
          <div className="mt-0.5 flex min-w-0 items-center gap-2 text-xs text-ink-3">
            {ids[0] ? <EntityLink id={ids[0]} name={names[0]} className="text-xs text-ink-2" /> : <span className="truncate">{names[0]}</span>}
            {names.length > 1 && <span>+{names.length - 1} more</span>}
          </div>
        </div>
        <div className="text-right text-xs">
          <div className="tnum text-ink-2">{r.status === 'ACTIVE' ? span(r.start, now) : <TimeAgo value={r.end} />}</div>
          <div className="text-ink-3">{String(r.category ?? '').toLowerCase().replace(/_/g, ' ')}</div>
        </div>
      </Link>
    </li>
  )
}

/** Nothing active: say so, then show what was recently resolved. */
function AllQuiet({ now }: { now: number }) {
  const recent = useDql(recentProblemsSpec)
  const rows = recent.data?.records ?? []
  return (
    <div>
      <div className="flex items-center gap-3 border-b border-line px-3 py-3">
        <span className="flex size-8 items-center justify-center rounded-full bg-ok-wash">
          <CheckCircle2 className="size-4 text-ok" />
        </span>
        <div>
          <div className="text-sm font-medium">No active problems</div>
          <div className="text-xs text-ink-3">Davis hasn't flagged anything that is still open.</div>
        </div>
      </div>
      {rows.length > 0 && (
        <>
          <div className="px-3 pt-2.5 pb-1 text-2xs font-medium tracking-wide text-ink-3 uppercase">Recently resolved</div>
          <ul className="divide-y divide-line">
            {rows.slice(0, 5).map((r) => (
              <ProblemRow key={r.display_id} r={r} now={now} />
            ))}
          </ul>
        </>
      )}
    </div>
  )
}

function ChangeItem({ r }: { r: Rec }) {
  const ok = /succe|finished|ok/i.test(String(r.outcome ?? ''))
  const bad = /fail|error|degrad/i.test(String(r.outcome ?? ''))
  return (
    <li className="relative flex gap-3 pb-3 pl-4 last:pb-1">
      <span className="absolute top-1.5 bottom-0 left-[3px] w-px bg-line" />
      <span className={clsx('absolute top-1.5 left-0 size-[7px] rounded-full ring-2 ring-panel', bad ? 'bg-crit' : ok ? 'bg-ok' : 'bg-accent')} />
      <Link
        href={r.workload ? `/changes?f=${encodeURIComponent(`workload:${r.workload}`)}` : '/changes'}
        className="min-w-0 flex-1 rounded-md hover:text-accent-ink"
      >
        <div className="flex items-center gap-2">
          {/* name the thing that changed; K8s events only say "Deployment spec change" */}
          <span className="truncate text-sm">{r.workload ?? r.what}</span>
          {r.env && <Badge>{r.env}</Badge>}
        </div>
        <div className="flex items-center gap-2 text-xs text-ink-3">
          <TimeAgo value={r.timestamp} />
          {r.workload && <span className="truncate">· {r.what}</span>}
          {r.outcome && <span>· {String(r.outcome).toLowerCase()}</span>}
          {r.rev && <span className="font-mono">· {String(r.rev).slice(0, 7)}</span>}
        </div>
      </Link>
    </li>
  )
}


function ServiceAttention({ rows }: { rows: Rec[] }) {
  const top = [...rows].sort((a, b) => num(b.failure_rate) - num(a.failure_rate) || num(b.failed) - num(a.failed)).slice(0, 7)
  if (!top.length) return <Empty title="No service traffic" hint="No request metrics in this timeframe." />
  return (
    <div className="divide-y divide-line">
      <div className="grid grid-cols-[minmax(0,1fr)_100px_96px_90px_80px] items-center gap-3 px-3 py-1.5 text-2xs font-medium tracking-wide text-ink-3 uppercase">
        <span>Service</span>
        <span>Throughput</span>
        <span className="text-right">Requests</span>
        <span className="text-right">Failure rate</span>
        <span className="text-right">Latency</span>
      </div>
      {top.map((r) => (
        <div key={r['dt.smartscape.service']} className="grid grid-cols-[minmax(0,1fr)_100px_96px_90px_80px] items-center gap-3 px-3 py-2 text-sm">
          <EntityLink id={r['dt.smartscape.service']} name={r['s.name']} type="SERVICE" />
          <Spark values={r.req} color="var(--s1)" width={100} />
          <span className="tnum text-right text-ink-2">{fmtInt(num(r.total))}</span>
          <span className={clsx('tnum text-right', num(r.failure_rate) >= 5 ? 'text-crit' : num(r.failure_rate) > 0 ? 'text-warn' : 'text-ink-3')}>
            {fmtPct(num(r.failure_rate), 2)}
          </span>
          <span className="tnum text-right text-ink-2">{fmtUs(num(r.latency))}</span>
        </div>
      ))}
    </div>
  )
}

