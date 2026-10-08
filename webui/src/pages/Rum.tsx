import clsx from 'clsx'
import { AlertTriangle, ArrowLeft, Clapperboard, ExternalLink, Laptop, MonitorSmartphone, Smartphone, Tablet, User, X } from 'lucide-react'
import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Link, useLocation, useSearch } from 'wouter'
import { DataTable, type Column } from '../components/DataTable'
import { DataTabs } from '../components/DataTabs'
import { EntityLink } from '../components/Entity'
import { FilterInput, PageHeader, Panel } from '../components/Panel'
import { Inspector, SidePanel } from '../components/signals'
import { Spark } from '../components/Spark'
import { Badge, CopyButton, Empty, ErrorBox, Segmented, Skeleton, SkeletonRows, TimeAgo, Tip, type Tone } from '../components/ui'
import { arr, num, useDql, useMeta, type DqlSpec, type Rec } from '../lib/api'
import { fmtCompact, fmtDateTime, fmtInt, fmtMs, fmtTime } from '../lib/format'
import { dtLinks, traceHref } from '../lib/links'
import {
  errorGroupsQuery, errorOccurrencesQuery, flag, fmtVital, frontendsQuery, frontendTrafficQuery, pagesQuery, sessionEventsQuery, sessionQuery,
  sessionsQuery, VITAL_LABEL, vitalRating, vitalValue, type SessionLens, type Vital,
} from '../lib/rum'
import { tfSpec } from '../lib/shared'
import { useNames } from '../lib/names'
import { pushRecent, useTitle } from '../lib/store'
import { floorTf, sparkInterval, useTimeframe, type Timeframe } from '../lib/timeframe'

// RUM is sparse compared to backend signals: floor the window at 24h (as
// dynatui does) so lists are never misleadingly empty, and say so.
const rumTf = (tf: Timeframe) => floorTf(tf, '24h')

export const frontendsSpec = (tf: Timeframe, realOnly = true): DqlSpec => tfSpec(rumTf(tf), frontendsQuery(realOnly), { ttl: 60 })

export const sessionsSpec = (tf: Timeframe, realOnly: boolean, frontend: string | null | undefined, lens: SessionLens) =>
  tfSpec(rumTf(tf), sessionsQuery({ realOnly, frontend, lens }), { ttl: 30 })
export const errorGroupsSpec = (tf: Timeframe, realOnly: boolean, frontend?: string | null) => tfSpec(rumTf(tf), errorGroupsQuery({ realOnly, frontend }), { ttl: 30 })
export const pagesSpec = (tf: Timeframe, realOnly: boolean, frontend?: string | null) => tfSpec(rumTf(tf), pagesQuery({ realOnly, frontend }), { ttl: 60 })

const sessionHref = (id: string) => `/rum/sessions/${encodeURIComponent(id)}`

function useRealOnly() {
  const params = new URLSearchParams(useSearch())
  return params.get('traffic') !== 'all'
}

// ── vitals cells ──────────────────────────────────────────────────────────

export function VitalCell({ r, v }: { r: Rec; v: Vital }) {
  const value = vitalValue(r, v)
  const rating = vitalRating(v, value)
  return (
    <Tip content={rating ? `${VITAL_LABEL[v]} (p75): ${rating.label}` : `${VITAL_LABEL[v]}: not measured`}>
      <span className="inline-flex items-center justify-end gap-1.5">
        {rating && <RatingDot tone={rating.tone} />}
        <span className={clsx(!rating && 'text-ink-4')}>{fmtVital(v, value)}</span>
      </span>
    </Tip>
  )
}

/** Shape + color, so the rating never relies on color alone. */
function RatingDot({ tone }: { tone: Tone }) {
  if (tone === 'ok') return <span className="inline-block size-2 rounded-full bg-ok" />
  if (tone === 'warn') return <span className="inline-block size-2 rotate-45 rounded-[1px] bg-warn" />
  return <span className="inline-block size-0 border-x-[5px] border-b-[8px] border-x-transparent border-b-crit" />
}

export function VitalsLegend() {
  return (
    <span className="flex items-center gap-3 text-xs text-ink-3">
      <span className="flex items-center gap-1.5">
        <RatingDot tone="ok" /> good
      </span>
      <span className="flex items-center gap-1.5">
        <RatingDot tone="warn" /> needs improvement
      </span>
      <span className="flex items-center gap-1.5">
        <RatingDot tone="crit" /> poor
      </span>
      <span className="text-ink-4">p75</span>
    </span>
  )
}

// ── frontends table (shared with Pulse) ──────────────────────────────────

export function FrontendsTable({
  tf,
  realOnly,
  selected,
  onSelect,
  maxHeight,
  compact,
}: {
  tf: Timeframe
  realOnly: boolean
  selected?: string | null
  onSelect?: (id: string | null) => void
  maxHeight?: number
  compact?: boolean
}) {
  const res = useDql(frontendsSpec(tf, realOnly))
  const traffic = useDql(tfSpec(rumTf(tf), frontendTrafficQuery(sparkInterval(rumTf(tf).ms)), { ttl: 60 }))
  const rows = res.data?.records
  const names = useNames((rows ?? []).map((r) => r['dt.smartscape.frontend']))
  const byId = useMemo(() => new Map((traffic.data?.records ?? []).map((r) => [r['dt.smartscape.frontend'], r])), [traffic.data])

  const cols: Column[] = [
    {
      key: 'name',
      header: 'Frontend',
      width: 'minmax(200px,2fr)',
      render: (r) =>
        r['dt.smartscape.frontend'] ? (
          <span className="flex min-w-0 flex-col">
            <EntityLink id={r['dt.smartscape.frontend']} name={names.get(r['dt.smartscape.frontend']) ?? r.name} type="FRONTEND" />
          </span>
        ) : (
          <span className="text-ink-3">{r.name ?? 'unattributed'}</span>
        ),
      sort: (r) => String(names.get(r['dt.smartscape.frontend']) ?? r.name ?? '').toLowerCase(),
    },
    { key: 'sessions', header: 'Sessions', width: '80px', align: 'right', render: (r) => fmtCompact(num(r.sessions)), sort: (r) => num(r.sessions) },
    { key: 'views', header: 'Page views', width: '90px', align: 'right', render: (r) => fmtCompact(num(r.views)), sort: (r) => num(r.views) },
    ...(['lcp', 'inp', 'cls', ...(compact ? [] : ['ttfb'])] as Vital[]).map(
      (v): Column => ({
        key: v,
        header: v.toUpperCase(),
        width: '96px',
        align: 'right',
        render: (r) => <VitalCell r={r} v={v} />,
        sort: (r) => (Number.isFinite(vitalValue(r, v)) ? vitalValue(r, v) : -1),
      }),
    ),
    {
      key: 'errors',
      header: 'Errors',
      width: '72px',
      align: 'right',
      render: (r) => <span className={clsx(num(r.errors) > 0 ? 'text-crit' : 'text-ink-3')}>{fmtCompact(num(r.errors))}</span>,
      sort: (r) => num(r.errors),
    },
    {
      key: 'traffic',
      header: 'Requests',
      width: '110px',
      align: 'right',
      render: (r) => {
        const t = byId.get(r['dt.smartscape.frontend'])
        return t ? <Spark values={t.req} color="var(--s1)" width={96} /> : <span className="text-ink-4">—</span>
      },
    },
  ]

  if (res.error) return <ErrorBox error={res.error} />
  return (
    <DataTable
      rows={rows}
      loading={res.isLoading}
      columns={cols}
      rowKey={(r) => r['dt.smartscape.frontend'] ?? 'none'}
      onOpen={onSelect ? (r) => onSelect(r['dt.smartscape.frontend'] === selected ? null : r['dt.smartscape.frontend']) : undefined}
      href={onSelect ? undefined : (r) => (r['dt.smartscape.frontend'] ? `/rum?app=${r['dt.smartscape.frontend']}` : '/rum')}
      selectedKey={selected}
      initialSort={{ key: 'views', dir: 'desc' }}
      maxHeight={maxHeight}
      rowHeight={38}
      empty={<Empty icon={<MonitorSmartphone className="size-5" />} title="No real-user traffic" hint="No RUM page views recorded in this timeframe." />}
    />
  )
}

// ── Experience page ──────────────────────────────────────────────────────

type Tab = 'sessions' | 'errors' | 'pages'

export default function Experience() {
  useTitle('Experience')
  const raw = useTimeframe()
  const tf = rumTf(raw)
  const params = new URLSearchParams(useSearch())
  const [, navigate] = useLocation()
  const realOnly = useRealOnly()
  const app = params.get('app')
  const tab = (params.get('tab') as Tab) || 'sessions'
  const names = useNames(app ? [app] : [])
  const sSpec = sessionsSpec(raw, realOnly, app, 'all')
  const eSpec = errorGroupsSpec(raw, realOnly, app)
  const pSpec = pagesSpec(raw, realOnly, app)
  const sRes = useDql(sSpec)
  const eRes = useDql(eSpec)
  const pRes = useDql(pSpec)

  const set = (k: string, v: string | null) => {
    const p = new URLSearchParams(location.search)
    if (v == null) p.delete(k)
    else p.set(k, v)
    navigate(`/rum?${p}`, { replace: true })
  }

  return (
    <div className="mx-auto flex max-w-[1600px] flex-col p-5">
      <PageHeader
        title="Experience"
        icon={<MonitorSmartphone className="size-5" />}
        sub={`Real user monitoring · ${tf.label.toLowerCase()}${tf !== raw ? ' (at least 24h: RUM is sparse)' : ''}`}
        actions={
          <Segmented
            value={realOnly ? 'real' : 'all'}
            onChange={(v) => set('traffic', v === 'all' ? 'all' : null)}
            options={[
              { value: 'real', label: 'Real users' },
              { value: 'all', label: 'Incl. synthetic' },
            ]}
          />
        }
      />

      <Panel
        title="Frontends"
        hint="click a row to focus the tabs below"
        spec={frontendsSpec(raw, realOnly)}
        className="mb-4"
        actions={<VitalsLegend />}
      >
        <FrontendsTable tf={raw} realOnly={realOnly} selected={app} onSelect={(id) => set('app', id)} maxHeight={330} />
      </Panel>

      <div className="flex min-h-[560px] flex-col rounded-xl border border-line bg-panel">
        <div className="flex items-center border-b border-line pr-3">
          <DataTabs
            className="flex-1 border-b-0"
            value={tab}
            onChange={(t) => set('tab', t === 'sessions' ? null : t)}
            tabs={[
              { value: 'sessions', label: 'Sessions', spec: sSpec, result: sRes, limit: 500 },
              { value: 'errors', label: 'Errors', spec: eSpec, result: eRes, limit: 300 },
              { value: 'pages', label: 'Pages', spec: pSpec, result: pRes, limit: 300 },
            ]}
            right={
              app && (
                <button
                  type="button"
                  onClick={() => set('app', null)}
                  className="inline-flex h-6 max-w-80 items-center gap-1 rounded-md bg-accent-wash px-2 text-xs text-accent-ink hover:brightness-110"
                >
                  <span className="truncate">{names.get(app) ?? app}</span>
                  <X className="size-3" />
                </button>
              )
            }
          />
        </div>
        <div className="flex min-h-0 flex-1 flex-col">
          {tab === 'sessions' && <SessionsView tf={raw} realOnly={realOnly} frontend={app} />}
          {tab === 'errors' && <ErrorsView tf={raw} realOnly={realOnly} frontend={app} />}
          {tab === 'pages' && <PagesView tf={raw} realOnly={realOnly} frontend={app} />}
        </div>
      </div>
    </div>
  )
}

// ── sessions ─────────────────────────────────────────────────────────────

function DeviceIcon({ type }: { type: unknown }) {
  const t = String(type ?? '')
  const I = t === 'mobile' ? Smartphone : t === 'tablet' ? Tablet : Laptop
  return <I className="size-3.5 shrink-0 text-ink-3" />
}

export function SessionsView({ tf, realOnly, frontend, height }: { tf: Timeframe; realOnly: boolean; frontend?: string | null; height?: number }) {
  const [lens, setLens] = useState<SessionLens>('all')
  const [filter, setFilter] = useState('')
  const spec = sessionsSpec(tf, realOnly, frontend, lens)
  const res = useDql(spec)
  const f = filter.toLowerCase()
  const rows = useMemo(
    () => res.data?.records.filter((r) => !f || JSON.stringify([r['user.identifier'], r['frontend.name'], r['geo.city.name'], r['browser.name'], r['dt.rum.session.id']]).toLowerCase().includes(f)),
    [res.data, f],
  )

  const cols: Column[] = [
    {
      key: 'start',
      header: 'Started',
      width: '130px',
      render: (r) => (
        <span className="flex flex-col leading-tight">
          <span className="tnum text-xs text-ink-2">{fmtDateTime(r.start_time)}</span>
        </span>
      ),
      sort: (r) => r.start_time,
    },
    {
      key: 'user',
      header: 'User',
      width: 'minmax(160px,1.4fr)',
      render: (r) =>
        r['user.identifier'] ? (
          <span className="flex min-w-0 items-center gap-1.5">
            <User className="size-3.5 shrink-0 text-ink-3" />
            <span className="truncate">{r['user.identifier']}</span>
          </span>
        ) : (
          <span className="text-ink-4">anonymous</span>
        ),
      sort: (r) => r['user.identifier'] ?? '~',
    },
    { key: 'app', header: 'App', width: 'minmax(100px,1fr)', render: (r) => <span className="text-ink-2">{arr(r['frontend.name']).join(', ')}</span>, sort: (r) => arr(r['frontend.name'])[0] },
    { key: 'dur', header: 'Duration', width: '80px', align: 'right', render: (r) => fmtMs(num(r.duration) / 1e6), sort: (r) => num(r.duration) },
    { key: 'views', header: 'Views', width: '56px', align: 'right', render: (r) => fmtInt(num(r.view_summary_count)), sort: (r) => num(r.view_summary_count) },
    { key: 'actions', header: 'Actions', width: '64px', align: 'right', render: (r) => fmtInt(num(r.user_action_count)), sort: (r) => num(r.user_action_count) },
    {
      key: 'errors',
      header: 'Errors',
      width: '60px',
      align: 'right',
      render: (r) => <span className={clsx(num(r['error.count']) > 0 ? 'font-medium text-crit' : 'text-ink-4')}>{fmtInt(num(r['error.count']))}</span>,
      sort: (r) => num(r['error.count']),
    },
    {
      key: 'client',
      header: 'Client',
      width: 'minmax(140px,1fr)',
      render: (r) => (
        <span className="flex min-w-0 items-center gap-1.5 text-ink-2">
          <DeviceIcon type={r['device.type']} />
          <span className="truncate">
            {r['browser.name']} {r['browser.version']} · {r['os.name']}
          </span>
        </span>
      ),
      sort: (r) => r['browser.name'],
    },
    {
      key: 'geo',
      header: 'Location',
      width: 'minmax(110px,0.8fr)',
      render: (r) => (
        <span className="truncate text-ink-2">
          {flag(r['geo.country.iso_code'])} {r['geo.city.name'] ?? r['geo.country.iso_code'] ?? '—'}
        </span>
      ),
      sort: (r) => r['geo.country.iso_code'],
    },
    {
      key: 'flags',
      header: '',
      width: '48px',
      render: (r) => (
        <span className="flex items-center justify-end gap-1.5 text-ink-3">
          {r['characteristics.has_replay'] && (
            <Tip content="Session replay recorded">
              <Clapperboard className="size-3.5" />
            </Tip>
          )}
          {r['characteristics.is_bounce'] && (
            <Tip content="Bounced (single view)">
              <span className="text-2xs">B</span>
            </Tip>
          )}
        </span>
      ),
    },
  ]

  return (
    <>
      <div className="flex items-center gap-2 border-b border-line px-3 py-2">
        <Segmented
          value={lens}
          onChange={setLens}
          options={[
            { value: 'all', label: 'All' },
            { value: 'errors', label: 'With errors' },
            { value: 'bounced', label: 'Bounced' },
          ]}
        />
        <span className="tnum text-xs text-ink-3">{rows ? `${rows.length}${rows.length >= 500 ? '+' : ''} sessions` : ''}</span>
        <FilterInput value={filter} onChange={setFilter} placeholder="User, app, city, browser…" className="ml-auto w-64" />
      </div>
      {res.error ? (
        <ErrorBox error={res.error} />
      ) : (
        <DataTable
          rows={rows}
          loading={res.isLoading}
          columns={cols}
          rowKey={(r) => r['dt.rum.session.id']}
          href={(r) => sessionHref(r['dt.rum.session.id'])}
          initialSort={{ key: 'start', dir: 'desc' }}
          className="flex-1"
          maxHeight={height}
          empty={<Empty title="No sessions" hint="No user sessions in this timeframe." />}
        />
      )}
    </>
  )
}

// ── errors ───────────────────────────────────────────────────────────────

export function ErrorsView({ tf, realOnly, frontend, height }: { tf: Timeframe; realOnly: boolean; frontend?: string | null; height?: number }) {
  const spec = errorGroupsSpec(tf, realOnly, frontend)
  const res = useDql(spec)
  const [sel, setSel] = useState<Rec | null>(null)
  const [filter, setFilter] = useState('')
  const f = filter.toLowerCase()
  const rows = useMemo(() => res.data?.records.filter((r) => !f || String(r.error).toLowerCase().includes(f)), [res.data, f])

  const cols: Column[] = [
    {
      key: 'error',
      header: 'Error',
      width: 'minmax(280px,3fr)',
      render: (r) => (
        <span className="flex min-w-0 items-center gap-2">
          <AlertTriangle className="size-3.5 shrink-0 text-crit" />
          <span className="truncate font-mono text-xs">{r.error ?? '(unnamed)'}</span>
        </span>
      ),
      sort: (r) => r.error,
    },
    { key: 'type', header: 'Type', width: '90px', render: (r) => (r.type ? <Badge>{r.type}</Badge> : null), sort: (r) => r.type },
    { key: 'count', header: 'Occurrences', width: '96px', align: 'right', render: (r) => fmtCompact(num(r.count)), sort: (r) => num(r.count) },
    { key: 'sessions', header: 'Sessions', width: '76px', align: 'right', render: (r) => fmtCompact(num(r.sessions)), sort: (r) => num(r.sessions) },
    { key: 'app', header: 'App', width: 'minmax(100px,1fr)', render: (r) => <span className="text-ink-2">{r.app}</span>, sort: (r) => r.app },
    { key: 'last', header: 'Last seen', width: '90px', align: 'right', render: (r) => <TimeAgo value={r.last} className="text-ink-2" />, sort: (r) => r.last },
  ]

  return (
    <div className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex items-center gap-2 border-b border-line px-3 py-2">
          <span className="tnum text-xs text-ink-3">{rows ? `${rows.length} distinct errors` : ''}</span>
          <FilterInput value={filter} onChange={setFilter} placeholder="Filter errors…" className="ml-auto w-64" />
        </div>
        {res.error ? (
          <ErrorBox error={res.error} />
        ) : (
          <DataTable
            rows={rows}
            loading={res.isLoading}
            columns={cols}
            rowKey={(r) => String(r.error)}
            onOpen={setSel}
            selectedKey={sel ? String(sel.error) : null}
            initialSort={{ key: 'count', dir: 'desc' }}
            className="flex-1"
            maxHeight={height}
            empty={<Empty title="No frontend errors" hint="Nothing failed in users' browsers in this timeframe." />}
          />
        )}
      </div>
      {sel && <ErrorPanel group={sel} tf={tf} realOnly={realOnly} frontend={frontend} onClose={() => setSel(null)} />}
    </div>
  )
}

function ErrorPanel({ group, tf, realOnly, frontend, onClose }: { group: Rec; tf: Timeframe; realOnly: boolean; frontend?: string | null; onClose: () => void }) {
  const res = useDql(tfSpec(rumTf(tf), errorOccurrencesQuery(String(group.error), { realOnly, frontend }), { ttl: 30 }))
  const occ = res.data?.records ?? []
  const [open, setOpen] = useState<Rec | null>(null)
  useEffect(() => setOpen(null), [group])
  const latest = open ?? occ[0]
  return (
    <SidePanel title={<span className="font-mono text-xs">{group.error}</span>} onClose={onClose} width="w-[min(560px,44vw)]">
      <div className="mb-3 flex flex-wrap gap-x-4 gap-y-1 text-sm text-ink-2">
        <span>
          <b className="tnum text-ink">{fmtInt(num(group.count))}</b> occurrences
        </span>
        <span>
          <b className="tnum text-ink">{fmtInt(num(group.sessions))}</b> sessions
        </span>
        <span>
          first <TimeAgo value={group.first} />
        </span>
      </div>
      <div className="mb-1 text-2xs font-medium tracking-wide text-ink-3 uppercase">Recent occurrences</div>
      {res.isLoading ? (
        <SkeletonRows rows={4} />
      ) : (
        <ul className="mb-4 max-h-64 divide-y divide-line overflow-auto rounded-lg border border-line">
          {occ.map((o, i) => (
            <li key={i}>
              <button
                type="button"
                onClick={() => setOpen(o)}
                className={clsx('flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-xs hover:bg-panel-hover', latest === o && 'bg-accent-wash')}
              >
                <span className="tnum w-20 shrink-0 text-ink-3">
                  <TimeAgo value={o.start_time} />
                </span>
                <span className="min-w-0 flex-1 truncate text-ink-2">{o['view.detected_name'] ?? o['page.detected_name'] ?? '—'}</span>
                <span className="shrink-0 text-ink-3">{o['browser.name']}</span>
                <Link href={sessionHref(o['dt.rum.session.id'])} className="shrink-0 text-accent-ink hover:underline" onClick={(e) => e.stopPropagation()}>
                  session →
                </Link>
              </button>
            </li>
          ))}
        </ul>
      )}
      {latest && <Inspector rec={latest} />}
    </SidePanel>
  )
}

// ── pages ────────────────────────────────────────────────────────────────

function PagesView({ tf, realOnly, frontend }: { tf: Timeframe; realOnly: boolean; frontend?: string | null }) {
  const res = useDql(pagesSpec(tf, realOnly, frontend))
  const [filter, setFilter] = useState('')
  const f = filter.toLowerCase()
  const rows = useMemo(() => res.data?.records.filter((r) => !f || String(r.page).toLowerCase().includes(f)), [res.data, f])
  const cols: Column[] = [
    { key: 'page', header: 'Page', width: 'minmax(280px,3fr)', render: (r) => <span className="truncate font-mono text-xs">{r.page ?? '(unknown)'}</span>, sort: (r) => r.page },
    { key: 'app', header: 'App', width: 'minmax(90px,1fr)', render: (r) => <span className="text-ink-2">{r.app}</span>, sort: (r) => r.app },
    { key: 'views', header: 'Views', width: '72px', align: 'right', render: (r) => fmtCompact(num(r.views)), sort: (r) => num(r.views) },
    { key: 'sessions', header: 'Sessions', width: '76px', align: 'right', render: (r) => fmtCompact(num(r.sessions)), sort: (r) => num(r.sessions) },
    ...(['lcp', 'inp', 'cls'] as Vital[]).map(
      (v): Column => ({
        key: v,
        header: v.toUpperCase(),
        width: '96px',
        align: 'right',
        render: (r) => <VitalCell r={r} v={v} />,
        sort: (r) => (Number.isFinite(vitalValue(r, v)) ? vitalValue(r, v) : -1),
      }),
    ),
    {
      key: 'errors',
      header: 'JS errors',
      width: '76px',
      align: 'right',
      render: (r) => <span className={clsx(num(r.errors) > 0 ? 'text-crit' : 'text-ink-4')}>{fmtCompact(num(r.errors))}</span>,
      sort: (r) => num(r.errors),
    },
  ]
  return (
    <>
      <div className="flex items-center gap-2 border-b border-line px-3 py-2">
        <span className="tnum text-xs text-ink-3">{rows ? `${rows.length} pages` : ''}</span>
        <span className="ml-3">
          <VitalsLegend />
        </span>
        <FilterInput value={filter} onChange={setFilter} placeholder="Filter pages…" className="ml-auto w-64" />
      </div>
      {res.error ? (
        <ErrorBox error={res.error} />
      ) : (
        <DataTable rows={rows} loading={res.isLoading} columns={cols} rowKey={(r) => `${r.app}|${r.page}`} initialSort={{ key: 'views', dir: 'desc' }} className="flex-1" />
      )}
    </>
  )
}

// ── session page ─────────────────────────────────────────────────────────

const KIND: Record<string, { label: string; tone: Tone }> = {
  view_summary: { label: 'view', tone: 'accent' },
  page_summary: { label: 'page', tone: 'accent' },
  navigation: { label: 'navigate', tone: 'info' },
  user_action: { label: 'action', tone: 'ok' },
  user_interaction: { label: 'interaction', tone: 'muted' },
  error: { label: 'error', tone: 'crit' },
  request: { label: 'request', tone: 'muted' },
  api: { label: 'api', tone: 'muted' },
}

const JOURNEY = new Set(['view_summary', 'page_summary', 'navigation', 'user_action', 'error'])

function eventLabel(e: Rec): ReactNode {
  const c = e['characteristics.classifier']
  const view = e['view.detected_name'] ?? e['view.name'] ?? e['page.detected_name'] ?? e['page.url.full']
  switch (c) {
    case 'view_summary':
    case 'page_summary':
      return <span className="font-mono text-xs">{view ?? 'view'}</span>
    case 'navigation':
      return (
        <span className="font-mono text-xs">
          → {view ?? e['url.full'] ?? ''} {e['navigation.type'] && <span className="font-sans text-ink-3">({e['navigation.type']})</span>}
        </span>
      )
    case 'user_action':
      return <span>{e['user_action.name'] ?? 'user action'}</span>
    case 'user_interaction':
      return (
        <span>
          {e['interaction.type'] ?? 'interaction'} <span className="text-ink-2">{e['ui_element.custom_name'] ?? e['ui_element.name'] ?? ''}</span>
        </span>
      )
    case 'error':
      return <span className="font-mono text-xs text-crit">{e['error.display_name'] ?? e['error.name'] ?? e['exception.message'] ?? 'error'}</span>
    case 'request':
    case 'api': {
      const code = num(e['http.response.status_code'])
      return (
        <span className="font-mono text-xs">
          <span className="text-ink-3">{e['http.request.method'] ?? ''}</span> {e['url.path'] ?? e['url.full'] ?? e['api.name'] ?? ''}{' '}
          {Number.isFinite(code) && <span className={clsx(code >= 500 || code === 0 ? 'text-crit' : code >= 400 ? 'text-warn' : 'text-ink-3')}>{code}</span>}
        </span>
      )
    }
  }
  return <span className="text-ink-2">{c}</span>
}

const eventFailed = (e: Rec) => {
  const code = num(e['http.response.status_code'])
  return e['characteristics.classifier'] === 'error' || (Number.isFinite(code) && (code >= 400 || code === 0) && e['characteristics.classifier'] !== 'view_summary')
}

export function Session({ id }: { id: string }) {
  const { data: meta } = useMeta()
  const sres = useDql({ query: sessionQuery(id), ttl: 60 })
  const s = sres.data?.records[0]
  const win = useMemo(() => {
    if (!s) return null
    const a = Date.parse(s.start_time) - 60e3
    const b = Date.parse(s.end_time ?? s.start_time) + 60e3
    return { from: new Date(a).toISOString(), to: new Date(Math.min(b, Date.now())).toISOString() }
  }, [s])
  const eres = useDql(win ? { query: sessionEventsQuery(id), from: win.from, to: win.to, ttl: 60, maxRecords: 2000 } : null)
  const [lens, setLens] = useState<'journey' | 'all'>('journey')
  const [sel, setSel] = useState<Rec | null>(null)
  const user = s?.['user.identifier']
  useTitle(user ? `Session · ${user}` : 'Session')
  useEffect(() => {
    if (s) pushRecent({ href: sessionHref(id), label: `${user ?? 'anonymous'} · ${arr(s['frontend.name']).join(', ')}`, kind: 'Session' })
  }, [s, id, user])

  const all = eres.data?.records ?? []
  const events = lens === 'journey' ? all.filter((e) => JOURNEY.has(e['characteristics.classifier'])) : all
  const t0 = s ? Date.parse(s.start_time) : 0
  const t1 = s ? Math.max(Date.parse(s.end_time ?? s.start_time), ...all.map((e) => Date.parse(e.start_time) || 0)) : 1
  const total = Math.max(1, t1 - t0)
  const nReq = all.filter((e) => e['characteristics.classifier'] === 'request').length
  const traced = all.filter((e) => e['trace.id']).length
  const views = all.filter((e) => e['characteristics.classifier'] === 'view_summary')
  const worstLcp = Math.max(...views.map((v) => num(v['web_vitals.largest_contentful_paint']) / 1e6).filter(Number.isFinite), -1)
  const frontendId = arr(s?.['dt.smartscape.frontend'])[0]
  const fnames = useNames(frontendId ? [frontendId] : [])

  if (sres.error) return <ErrorBox error={sres.error} />
  if (sres.isLoading)
    return (
      <div className="p-5">
        <Skeleton className="mb-3 h-7 w-96" />
        <Skeleton className="h-96" />
      </div>
    )
  if (!s)
    return (
      <Empty
        title="Session not found"
        hint={
          <>
            No session with this ID in the last 7 days.{' '}
            <button type="button" className="text-accent-ink hover:underline" onClick={() => sres.refetch()}>
              Search again
            </button>
          </>
        }
      />
    )

  const lcpRating = vitalRating('lcp', worstLcp)
  return (
    <div className="flex h-full">
      <div className="flex min-w-0 flex-1 flex-col overflow-auto p-5">
        <Link href="/rum" className="mb-3 inline-flex items-center gap-1 self-start text-xs text-ink-3 hover:text-ink-2">
          <ArrowLeft className="size-3.5" /> Experience
        </Link>
        <div className="mb-4 flex flex-wrap items-start gap-4">
          <div className={clsx('flex size-10 items-center justify-center rounded-xl', num(s['error.count']) > 0 ? 'bg-crit-wash text-crit' : 'bg-accent-wash text-accent-ink')}>
            <User className="size-5" />
          </div>
          <div className="min-w-0 flex-1">
            <h1 className="truncate text-xl font-semibold tracking-tight">{user ?? 'Anonymous user'}</h1>
            <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-ink-3">
              {frontendId ? <EntityLink id={frontendId} name={fnames.get(frontendId) ?? arr(s['frontend.name'])[0]} type="FRONTEND" /> : <span>{arr(s['frontend.name']).join(', ')}</span>}
              <span>{fmtDateTime(s.start_time)}</span>
              <span className="tnum text-ink">{fmtMs(num(s.duration) / 1e6)}</span>
              <span className="inline-flex items-center gap-1">
                <DeviceIcon type={s['device.type']} /> {s['browser.name']} {s['browser.version']} · {s['os.name']}
              </span>
              <span>
                {flag(s['geo.country.iso_code'])} {[s['geo.city.name'], s['geo.region.name'], s['geo.country.name']].filter(Boolean).join(', ')}
              </span>
              {s.end_reason && <span>ended: {String(s.end_reason).replace(/_/g, ' ')}</span>}
              {s['characteristics.has_replay'] && (
                <Badge tone="accent">
                  <Clapperboard className="size-3" /> replay
                </Badge>
              )}
              {s['dt.rum.user_type'] !== 'real_user' && <Badge tone="warn">{s['dt.rum.user_type']}</Badge>}
            </div>
            <div className="mt-1 inline-flex items-center gap-1 font-mono text-2xs text-ink-4">
              {id}
              <CopyButton value={id} label="session ID" />
            </div>
          </div>
          {meta?.environment && frontendId && (
            <a
              href={dtLinks.entity(meta.environment, frontendId, 'FRONTEND')}
              target="_blank"
              rel="noreferrer"
              className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-line bg-sunken px-3 text-sm text-ink-2 hover:border-line-strong hover:text-ink"
            >
              Open in Dynatrace <ExternalLink className="size-3.5" />
            </a>
          )}
        </div>

        <div className="mb-4 grid grid-cols-6 gap-3 max-xl:grid-cols-3">
          {[
            ['Views', num(s.view_summary_count)],
            ['Actions', num(s.user_action_count)],
            ['Interactions', num(s.user_interaction_count)],
            ['Requests', num(s.request_count)],
            ['Errors', num(s['error.count'])],
          ].map(([l, v]) => (
            <div key={l as string} className="rounded-xl border border-line bg-panel px-3 py-2.5">
              <div className="text-2xs font-medium tracking-wide text-ink-3 uppercase">{l}</div>
              <div className={clsx('tnum mt-0.5 text-lg font-semibold', l === 'Errors' && (v as number) > 0 && 'text-crit')}>{fmtInt(v as number)}</div>
            </div>
          ))}
          <div className="rounded-xl border border-line bg-panel px-3 py-2.5">
            <div className="text-2xs font-medium tracking-wide text-ink-3 uppercase">Slowest LCP</div>
            <div className="tnum mt-0.5 flex items-center gap-2 text-lg font-semibold">
              {lcpRating && <RatingDot tone={lcpRating.tone} />}
              {fmtVital('lcp', worstLcp > 0 ? worstLcp : NaN)}
            </div>
          </div>
        </div>

        <div className="flex min-h-[420px] flex-1 flex-col rounded-xl border border-line bg-panel">
          <div className="flex items-center gap-3 border-b border-line px-3 py-2">
            <span className="text-sm font-medium">Timeline</span>
            <Segmented
              value={lens}
              onChange={setLens}
              options={[
                { value: 'journey', label: 'Journey' },
                { value: 'all', label: 'Everything', count: all.length || undefined },
              ]}
            />
            {traced > 0 && <span className="text-xs text-ink-3">{traced} of {nReq} requests link to a backend trace</span>}
          </div>
          {eres.error ? (
            <ErrorBox error={eres.error} />
          ) : eres.isLoading ? (
            <SkeletonRows rows={8} />
          ) : !events.length ? (
            <Empty title="No events" hint="This session's events may be older than the retention window." />
          ) : (
            <div className="min-h-0 flex-1 overflow-auto">
              {events.map((e, i) => {
                const st = Date.parse(e.start_time)
                const dur = Math.min(num(e.duration) / 1e6 || 0, t1 - st)
                const k = KIND[e['characteristics.classifier']] ?? { label: e['characteristics.classifier'], tone: 'muted' as Tone }
                const failed = eventFailed(e)
                const on = sel === e
                return (
                  <button
                    type="button"
                    key={e.id ?? i}
                    onClick={() => setSel(on ? null : e)}
                    className={clsx(
                      'grid w-full grid-cols-[64px_92px_minmax(240px,40%)_1fr_70px] items-center gap-3 border-b border-line px-3 py-1.5 text-left text-sm hover:bg-panel-hover',
                      on && 'bg-accent-wash! shadow-[inset_2px_0_0_var(--accent)]',
                      failed && !on && 'bg-crit-wash/40',
                    )}
                  >
                    <span className="tnum font-mono text-xs text-ink-3">+{fmtMs(st - t0).replace(' ms', 'ms')}</span>
                    <Badge tone={failed ? 'crit' : k.tone} className="w-[84px] justify-center">
                      {k.label}
                    </Badge>
                    <span className="flex min-w-0 items-center gap-2">
                      <span className="min-w-0 truncate">{eventLabel(e)}</span>
                      {e['trace.id'] && (
                        <Link href={traceHref(e['trace.id'], e.start_time ?? e.timestamp)} onClick={(ev) => ev.stopPropagation()} className="shrink-0 text-xs text-accent-ink hover:underline">
                          trace →
                        </Link>
                      )}
                      {e['characteristics.classifier'] === 'view_summary' && <ViewVitals e={e} />}
                    </span>
                    <span className="relative h-4">
                      <span
                        className={clsx('absolute top-1 h-2 rounded-[2px]', failed ? 'bg-crit' : k.tone === 'accent' ? 'bg-accent/50' : 'bg-[var(--s1)]')}
                        style={{ left: `${((st - t0) / total) * 100}%`, width: `${Math.max(0.25, (dur / total) * 100)}%`, minWidth: 2 }}
                      />
                    </span>
                    <span className="tnum text-right font-mono text-xs text-ink-3">{dur > 0 ? fmtMs(dur) : ''}</span>
                  </button>
                )
              })}
            </div>
          )}
        </div>
      </div>
      {sel && (
        <SidePanel title={<span className="flex items-center gap-2">{KIND[sel['characteristics.classifier']]?.label ?? sel['characteristics.classifier']} · {fmtTime(sel.start_time)}</span>} onClose={() => setSel(null)}>
          {sel['trace.id'] && (
            <Link href={traceHref(sel['trace.id'], sel.start_time ?? sel.timestamp)} className="mb-3 flex items-center gap-2 rounded-lg border border-accent/30 bg-accent-wash px-3 py-2 text-sm text-accent-ink hover:brightness-110">
              Follow this request into the backend trace →
            </Link>
          )}
          <Inspector rec={sel} />
        </SidePanel>
      )}
    </div>
  )
}

function ViewVitals({ e }: { e: Rec }) {
  const lcp = num(e['web_vitals.largest_contentful_paint']) / 1e6
  const rating = vitalRating('lcp', lcp)
  if (!rating) return null
  return (
    <Tip content={`LCP ${rating.label}`}>
      <span className="inline-flex shrink-0 items-center gap-1 text-xs text-ink-3">
        <RatingDot tone={rating.tone} /> LCP {fmtVital('lcp', lcp)}
      </span>
    </Tip>
  )
}
