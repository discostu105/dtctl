import { Command } from 'cmdk'
import { AlertOctagon, ArrowRight, Building2, MessagesSquare, Clock, Link2, Moon, ScrollText, Terminal, Timer, Waypoints } from 'lucide-react'
import { useDeferredValue, useEffect, useMemo, useState } from 'react'
import { useLocation } from 'wouter'
import { toast } from 'sonner'
import { useDql, useMeta } from '../lib/api'
import { switchTenant } from './TenantSwitcher'
import { searchEntitiesQuery, typeOfId } from '../lib/dql'
import { shortType } from '../lib/format'
import { entityHref, problemHref, traceHref } from '../lib/links'
import { paletteStore, recentStore, toggleTheme, useStore } from '../lib/store'
import { parseRel, PRESETS, setTimeframe } from '../lib/timeframe'
import { NAV } from './Shell'
import { TypeIcon } from './Entity'
import { copy, Kbd } from './ui'

const DQL_START = /^\s*(fetch|timeseries|smartscapeNodes|smartscapeEdges|data|metrics|describe|load)\b/

export function Palette() {
  const { open, initial } = useStore(paletteStore)
  const [search, setSearch] = useState('')
  const [, navigate] = useLocation()
  const recents = useStore(recentStore)
  const { data: meta } = useMeta()
  const deferred = useDeferredValue(search.trim())

  useEffect(() => {
    if (open) setSearch(initial ?? '')
  }, [open, initial])

  const isDql = DQL_START.test(deferred)
  const problemId = /^p-\d+$/i.test(deferred) ? deferred.toUpperCase() : null
  const traceId = /^[0-9a-f]{32}$/i.test(deferred) ? deferred.toLowerCase() : null
  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(deferred) ? deferred.toLowerCase() : null
  const entityId = /^[A-Z][A-Z0-9_]+-[0-9A-F]{16}$/.test(deferred) ? deferred : null
  const wantEntities = open && deferred.length >= 2 && !isDql && !traceId && !entityId && !problemId && !uuid
  const ents = useDql(wantEntities ? { query: searchEntitiesQuery(deferred), ttl: 60 } : null)
  const entRows = useMemo(() => {
    const groups = new Map<string, { r: any; n: number }>()
    for (const r of ents.data?.records ?? []) {
      const k = `${r.type}|${r.name}|${r.ns ?? ''}`
      const g = groups.get(k)
      if (g) g.n++
      else groups.set(k, { r, n: 1 })
    }
    return [...groups.values()].slice(0, 12)
  }, [ents.data])

  const close = () => paletteStore.set({ open: false })
  const go = (href: string) => {
    close()
    navigate(href)
  }

  if (!open) return null
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center bg-black/40 pt-[12vh] backdrop-blur-[2px]" onClick={close}>
      <Command
        label="Command palette"
        className="anim-pop w-[min(640px,92vw)] overflow-hidden rounded-xl bg-raised shadow-pop"
        onClick={(e) => e.stopPropagation()}
        onKeyDown={(e) => e.key === 'Escape' && close()}
        loop
      >
        <Command.Input
          autoFocus
          value={search}
          onValueChange={setSearch}
          placeholder="Search entities, paste a problem/trace/entity ID, type DQL, or jump to a page…"
          className="h-12 w-full border-b border-line bg-transparent px-4 text-[15px] text-ink outline-none placeholder:text-ink-4"
        />
        <Command.List className="max-h-[60vh] overflow-auto p-1.5 [&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:pt-2 [&_[cmdk-group-heading]]:pb-1 [&_[cmdk-group-heading]]:text-2xs [&_[cmdk-group-heading]]:font-medium [&_[cmdk-group-heading]]:tracking-wide [&_[cmdk-group-heading]]:text-ink-3 [&_[cmdk-group-heading]]:uppercase">
          <Command.Empty className="px-3 py-6 text-center text-sm text-ink-3">
            {ents.isFetching ? 'Searching Smartscape…' : 'No matches. Try a name, an ID, or DQL.'}
          </Command.Empty>

          {(problemId || traceId || entityId || isDql || uuid) && (
            <Command.Group heading="Detected">
              {problemId && (
                <Item value={`detected ${search}`} onSelect={() => go(problemHref(problemId))} icon={<AlertOctagon className="size-4 text-crit" />}>
                  Open problem <b>{problemId}</b>
                </Item>
              )}
              {uuid && (
                <Item value={`detected ${search}`} onSelect={() => go(`/ai/conversations/${uuid}`)} icon={<MessagesSquare className="size-4 text-accent" />}>
                  Open AI conversation <span className="font-mono text-xs">{uuid}</span>
                </Item>
              )}
              {traceId && (
                <Item value={`detected ${search}`} onSelect={() => go(traceHref(traceId))} icon={<Waypoints className="size-4 text-accent" />}>
                  Open trace <span className="font-mono text-xs">{traceId}</span>
                </Item>
              )}
              {entityId && (
                <Item value={`detected ${search}`} onSelect={() => go(entityHref(entityId))} icon={<TypeIcon type={typeOfId(entityId)} className="size-4" />}>
                  Open {shortType(typeOfId(entityId))} <span className="font-mono text-xs">{entityId}</span>
                </Item>
              )}
              {isDql && (
                <Item value={`detected ${search}`} onSelect={() => go(`/query?${new URLSearchParams({ dql: search, run: '1' })}`)} icon={<Terminal className="size-4 text-accent" />}>
                  Run as DQL
                </Item>
              )}
            </Command.Group>
          )}

          {wantEntities && entRows.length > 0 && (
            <Command.Group heading="Entities">
              {entRows.map(({ r, n }) => (
                <Item
                  key={r.id}
                  value={`entity ${r.name} ${r.id} ${deferred}`}
                  onSelect={() => go(entityHref(r.id, r.name))}
                  icon={<TypeIcon type={r.type} className="size-4" />}
                  hint={`${shortType(r.type)}${n > 1 ? ` ×${n}` : ''}`}
                >
                  {r.name || r.id}
                  {r.ns && <span className="ml-2 text-xs text-ink-3">{r.ns}</span>}
                </Item>
              ))}
            </Command.Group>
          )}

          {deferred && !isDql && !traceId && !entityId && !problemId && (
            <Command.Group heading="Search">
              <Item value={`search logs ${search}`} onSelect={() => go(`/logs?${new URLSearchParams({ q: search })}`)} icon={<ScrollText className="size-4" />}>
                Search logs for “{search}”
              </Item>
              <Item value={`search ai conversations ${search}`} onSelect={() => go(`/ai?${new URLSearchParams({ q: search })}`)} icon={<MessagesSquare className="size-4" />}>
                Search AI conversations for “{search}”
              </Item>
            </Command.Group>
          )}

          {!deferred && recents.length > 0 && (
            <Command.Group heading="Recent">
              {recents.slice(0, 6).map((r) => (
                <Item key={r.href} value={`recent ${r.label}`} onSelect={() => go(r.href)} icon={<Clock className="size-4" />} hint={r.kind}>
                  {r.label}
                </Item>
              ))}
            </Command.Group>
          )}

          <Command.Group heading="Go to">
            {NAV.map((n) => (
              <Item key={n.href} value={`go ${n.label}`} onSelect={() => go(n.href)} icon={<n.icon className="size-4" />} keys={['G', n.key.toUpperCase()]}>
                {n.label}
              </Item>
            ))}
          </Command.Group>

          {(meta?.tenants?.length ?? 0) > 1 && (
            <Command.Group heading="Tenant">
              {meta!.tenants!
                .filter((t) => t.name !== meta!.context)
                .map((t) => (
                  <Item
                    key={t.name}
                    value={`switch tenant context ${t.name} ${t.environment}`}
                    onSelect={() => switchTenant(t.name).catch((e) => toast.error(String(e instanceof Error ? e.message : e)))}
                    icon={<Building2 className="size-4" />}
                    hint={t.environment.replace(/^https?:\/\//, '').split('.')[0]}
                  >
                    Switch to {t.name}
                  </Item>
                ))}
            </Command.Group>
          )}

          <Command.Group heading="Timeframe">
            {PRESETS.map((p) => (
              <Item
                key={p.key}
                value={`timeframe ${p.label} ${p.key}`}
                onSelect={() => {
                  setTimeframe(parseRel(p.key)!)
                  close()
                }}
                icon={<Timer className="size-4" />}
              >
                {p.label}
              </Item>
            ))}
          </Command.Group>

          <Command.Group heading="Actions">
            <Item
              value="copy link share url"
              onSelect={() => {
                copy(location.href, 'link')
                close()
              }}
              icon={<Link2 className="size-4" />}
            >
              Copy link to this view
            </Item>
            <Item
              value="toggle theme dark light"
              onSelect={() => {
                toggleTheme()
                close()
              }}
              icon={<Moon className="size-4" />}
            >
              Toggle dark / light theme
            </Item>
          </Command.Group>
        </Command.List>
        <div className="flex items-center gap-3 border-t border-line px-3 py-2 text-2xs text-ink-3">
          <span className="flex items-center gap-1">
            <Kbd>↑</Kbd>
            <Kbd>↓</Kbd> navigate
          </span>
          <span className="flex items-center gap-1">
            <Kbd>↵</Kbd> open
          </span>
          <span className="flex items-center gap-1">
            <Kbd>esc</Kbd> close
          </span>
        </div>
      </Command>
    </div>
  )
}

function Item({
  children,
  value,
  onSelect,
  icon,
  hint,
  keys,
}: {
  children: React.ReactNode
  value: string
  onSelect: () => void
  icon?: React.ReactNode
  hint?: string
  keys?: string[]
}) {
  return (
    <Command.Item
      value={value}
      onSelect={onSelect}
      className="group flex h-9 cursor-pointer items-center gap-2.5 rounded-md px-2 text-sm text-ink-2 data-[selected=true]:bg-accent-wash data-[selected=true]:text-ink"
    >
      <span className="text-ink-3 group-data-[selected=true]:text-accent-ink">{icon}</span>
      <span className="min-w-0 flex-1 truncate">{children}</span>
      {hint && <span className="text-2xs text-ink-3">{hint}</span>}
      {keys && (
        <span className="flex gap-0.5">
          {keys.map((k) => (
            <Kbd key={k}>{k}</Kbd>
          ))}
        </span>
      )}
      <ArrowRight className="size-3.5 text-ink-4 opacity-0 group-data-[selected=true]:opacity-100" />
    </Command.Item>
  )
}
