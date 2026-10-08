import { ExternalLink, ShieldAlert } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useLocation, useSearch } from 'wouter'
import { DataTable, type Column } from '../components/DataTable'
import { Markdown } from '../components/Markdown'
import { FilterInput, PageHeader, Panel } from '../components/Panel'
import { SidePanel } from '../components/signals'
import { Badge, Empty, ErrorBox, Facts, Segmented, SkeletonRows, TimeAgo } from '../components/ui'
import { arr, num, useDql, type Rec } from '../lib/api'
import { q } from '../lib/dql'
import { titleCase } from '../lib/format'
import { vulnsSpec } from '../lib/shared'
import { useTitle } from '../lib/store'
import { useTimeframe } from '../lib/timeframe'
import { EntityChip } from '../components/Entity'
import { RiskBadge } from './Pulse'

export default function Security() {
  useTitle('Security')
  const tf = useTimeframe()
  const params = new URLSearchParams(useSearch())
  const [, navigate] = useLocation()
  const [lens, setLens] = useState<'open' | 'muted' | 'all'>('open')
  const [filter, setFilter] = useState('')
  const spec = vulnsSpec(tf, lens)
  const res = useDql(spec)
  const selId = params.get('v')
  const all = res.data?.records
  const sel = all?.find((r) => r['vulnerability.id'] === selId) ?? null
  const f = filter.toLowerCase()
  const rows = useMemo(() => all?.filter((r) => !f || JSON.stringify([r.title, r.display_id, r.cve, r.tech]).toLowerCase().includes(f)), [all, f])
  const counts = useMemo(() => {
    const c: Record<string, number> = {}
    for (const r of all ?? []) c[r.level] = (c[r.level] ?? 0) + 1
    return c
  }, [all])

  const select = (r: Rec | null) => navigate(r ? `/security?v=${encodeURIComponent(r['vulnerability.id'])}` : '/security', { replace: true })

  const cols: Column[] = [
    { key: 'risk', header: 'Risk', width: '78px', render: (r) => <RiskBadge level={r.level} score={r.score} />, sort: (r) => num(r.score) },
    { key: 'id', header: 'ID', width: '64px', render: (r) => <span className="font-mono text-xs text-ink-3">{r.display_id}</span>, sort: (r) => num(String(r.display_id).slice(2)) },
    { key: 'title', header: 'Vulnerability', width: 'minmax(260px,3fr)', render: (r) => <span className="truncate">{r.title}</span>, sort: (r) => r.title },
    {
      key: 'exposure',
      header: 'Exposure',
      width: '96px',
      render: (r) =>
        r.exposure === 'PUBLIC_NETWORK' ? <Badge tone="crit">public</Badge> : r.exposure === 'ADJACENT_NETWORK' ? <Badge tone="warn">adjacent</Badge> : <span className="text-ink-4">—</span>,
      sort: (r) => r.exposure,
    },
    {
      key: 'exploit',
      header: 'Exploit',
      width: '84px',
      render: (r) => (r.exploit === 'AVAILABLE' ? <Badge tone="crit">known</Badge> : <span className="text-ink-4">—</span>),
      sort: (r) => r.exploit,
    },
    { key: 'fix', header: 'Fix', width: '60px', render: (r) => (r.fix ? <Badge tone="ok">yes</Badge> : <span className="text-ink-4">no</span>), sort: (r) => (r.fix ? 1 : 0) },
    { key: 'tech', header: 'Tech', width: '90px', render: (r) => <span className="text-ink-2">{titleCase(String(r.tech ?? ''))}</span>, sort: (r) => r.tech },
    { key: 'cve', header: 'CVE', width: '140px', render: (r) => <span className="font-mono text-xs text-ink-3">{arr(r.cve)[0] ?? '—'}</span> },
    { key: 'aff', header: 'Affected', width: '72px', align: 'right', render: (r) => num(r.affected) || '—', sort: (r) => num(r.affected) },
  ]

  return (
    <div className="flex h-full">
      <div className="flex min-w-0 flex-1 flex-col p-5">
        <PageHeader
          title="Security"
          icon={<ShieldAlert className="size-5" />}
          sub={
            all ? (
              <span className="flex items-center gap-3">
                {['CRITICAL', 'HIGH', 'MEDIUM', 'LOW'].map((l) => (
                  <span key={l} className="flex items-center gap-1.5">
                    <RiskBadge level={l} />
                    <b className="tnum font-semibold text-ink">{counts[l] ?? 0}</b>
                  </span>
                ))}
              </span>
            ) : (
              'Third-party and code-level vulnerabilities'
            )
          }
          actions={
            <>
              <Segmented
                value={lens}
                onChange={setLens}
                options={[
                  { value: 'open', label: 'Open' },
                  { value: 'muted', label: 'Muted' },
                  { value: 'all', label: 'All' },
                ]}
              />
              <FilterInput value={filter} onChange={setFilter} placeholder="Title, CVE, technology…" className="w-64" />
            </>
          }
        />
        <Panel spec={spec} result={res} className="min-h-0 flex-1" bodyClassName="flex min-h-0 flex-col" title={`${rows?.length ?? '…'} vulnerabilities`}>
          {res.error ? (
            <ErrorBox error={res.error} />
          ) : (
            <DataTable
              rows={rows}
              loading={res.isLoading}
              columns={cols}
              rowKey={(r) => r['vulnerability.id']}
              onOpen={select}
              selectedKey={selId}
              initialSort={{ key: 'risk', dir: 'desc' }}
              className="flex-1"
              autoFocus
            />
          )}
        </Panel>
      </div>
      {sel && <VulnPanel v={sel} onClose={() => select(null)} />}
    </div>
  )
}

function VulnPanel({ v, onClose }: { v: Rec; onClose: () => void }) {
  const tf = useTimeframe()
  const ents = useDql({
    query: `fetch security.events
| filter event.type == "VULNERABILITY_STATE_REPORT_EVENT" and event.level == "ENTITY" and vulnerability.id == ${q(v['vulnerability.id'])}
| sort timestamp desc
| dedup affected_entity.id
| fields affected_entity.id, affected_entity.name, affected_entity.type, component = coalesce(vulnerable_component.name, vulnerable_component.short_name), status = vulnerability.resolution.status
| limit 50`,
    from: tf.ms < 86400e3 ? 'now-24h' : tf.from,
    ttl: 120,
  })
  return (
    <SidePanel
      title={
        <span className="flex items-center gap-2">
          <RiskBadge level={v.level} score={v.score} /> {v.display_id}
        </span>
      }
      onClose={onClose}
      width="w-[min(600px,46vw)]"
      actions={
        v.url ? (
          <a href={v.url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 rounded px-1.5 py-1 text-xs text-accent-ink hover:bg-accent-wash">
            Dynatrace <ExternalLink className="size-3" />
          </a>
        ) : null
      }
    >
      <h2 className="mb-3 text-base font-semibold">{v.title}</h2>
      <Facts
        className="mb-4"
        items={[
          ['Status', v.status],
          ['Exposure', titleCase(String(v.exposure ?? ''))],
          ['Exploit', titleCase(String(v.exploit ?? ''))],
          ['Data assets', titleCase(String(v.data_assets ?? ''))],
          ['Fix available', v.fix ? 'yes' : 'no'],
          ['Technology', titleCase(String(v.tech ?? ''))],
          ['Type', titleCase(String(v.type ?? ''))],
          ['CVE', arr(v.cve).join(', ')],
          ['First seen', <TimeAgo value={v.first_seen} />],
        ]}
      />
      <div className="mb-2 text-2xs font-medium tracking-wide text-ink-3 uppercase">Affected entities</div>
      {ents.isLoading ? (
        <SkeletonRows rows={2} />
      ) : !ents.data?.records.length ? (
        <Empty title="No entity details" className="py-4" />
      ) : (
        <div className="mb-4 flex flex-col gap-1.5">
          {ents.data.records.map((e) => (
            <div key={e['affected_entity.id']} className="flex items-center gap-2 text-sm">
              {String(e['affected_entity.id']).includes('-') ? (
                <EntityChip id={e['affected_entity.id']} name={e['affected_entity.name']} />
              ) : (
                <span>{e['affected_entity.name']}</span>
              )}
              {e.component && <span className="truncate font-mono text-xs text-ink-3">{e.component}</span>}
            </div>
          ))}
        </div>
      )}
      {v.description && (
        <>
          <div className="mb-1 text-2xs font-medium tracking-wide text-ink-3 uppercase">Description</div>
          <Markdown text={String(v.description)} className="mb-4" />
        </>
      )}
      {v.remediation && (
        <>
          <div className="mb-1 text-2xs font-medium tracking-wide text-ink-3 uppercase">Remediation</div>
          <Markdown text={String(v.remediation)} />
        </>
      )}
    </SidePanel>
  )
}
