import * as Popover from '@radix-ui/react-popover'
import { Command } from 'cmdk'
import { Check, ExternalLink } from 'lucide-react'
import { useState } from 'react'
import { useMeta, type Meta } from '../lib/api'
import { Dot } from './ui'

const host = (env: string) => env.replace(/^https?:\/\//, '').replace(/\/$/, '')
const toneOf = (safety?: string) => {
  const s = safety || 'readwrite'
  return s.startsWith('readonly') ? 'ok' : s.includes('dangerously') ? 'crit' : 'warn'
}

/**
 * Switch tenant: any context from the dtctl config, in this server process
 * only (the config file's current context is not changed). The page reloads
 * on the new tenant, keeping the section and timeframe; detail pages fall
 * back to their list, since an ID from one tenant means nothing in another.
 */
export async function switchTenant(name: string) {
  const res = await fetch('/api/context', {
    method: 'POST',
    headers: { 'X-Dtctl-Web': '1', 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  })
  if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error ?? `HTTP ${res.status}`)
  const seg = location.pathname.split('/').filter(Boolean)
  const path = seg.length > 1 ? `/${seg[0] === 'e' ? 'smartscape' : seg[0]}` : location.pathname
  const tf = new URLSearchParams(location.search).get('tf')
  location.assign(`${path}${tf ? `?tf=${encodeURIComponent(tf)}` : ''}`)
}

export function TenantSwitcher() {
  const { data: meta } = useMeta()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState<string | null>(null)
  const [err, setErr] = useState<string | null>(null)
  if (!meta) return <div className="h-8 w-40 shimmer rounded-lg" />
  const tenants = meta.tenants ?? (meta.contexts ?? []).map((name) => ({ name, environment: '', safetyLevel: '' }))
  const pick = async (name: string) => {
    if (name === meta.context) return setOpen(false)
    setBusy(name)
    setErr(null)
    try {
      await switchTenant(name)
    } catch (e) {
      setErr(`${name}: ${e instanceof Error ? e.message : String(e)}`)
      setBusy(null)
    }
  }
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger asChild>
        <button
          type="button"
          title="Switch tenant"
          className="inline-flex h-8 max-w-64 items-center gap-2 rounded-lg border border-line bg-sunken px-2.5 text-sm text-ink-2 hover:border-line-strong"
        >
          <Dot tone={toneOf(meta.safetyLevel)} />
          <span className="truncate font-medium text-ink">{meta.context}</span>
          <span className="truncate text-ink-3 max-xl:hidden">{host(meta.environment).split('.')[0]}</span>
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content align="end" sideOffset={6} className="anim-pop z-50 w-[360px] overflow-hidden rounded-lg bg-raised text-sm shadow-pop">
          <Current meta={meta} />
          <Command loop className="border-t border-line">
            {tenants.length > 5 && (
              <Command.Input
                autoFocus
                placeholder={`Search ${tenants.length} tenants…`}
                className="h-9 w-full border-b border-line bg-transparent px-3 text-sm outline-none placeholder:text-ink-4"
              />
            )}
            <Command.List className="max-h-80 overflow-y-auto p-1">
              <Command.Empty className="px-3 py-4 text-center text-xs text-ink-3">No tenant matches</Command.Empty>
              {tenants.map((t) => (
                <Command.Item
                  key={t.name}
                  value={`${t.name} ${t.environment}`}
                  onSelect={() => pick(t.name)}
                  className="flex h-9 cursor-pointer items-center gap-2 rounded-md px-2 data-[selected=true]:bg-panel-hover"
                >
                  <Dot tone={toneOf(t.safetyLevel)} />
                  <span className="truncate font-medium text-ink">{t.name}</span>
                  <span className="min-w-0 flex-1 truncate text-xs text-ink-3">{host(t.environment)}</span>
                  {busy === t.name ? (
                    <span className="size-3 animate-spin rounded-full border-2 border-accent border-t-transparent" />
                  ) : t.name === meta.context ? (
                    <Check className="size-3.5 text-accent-ink" />
                  ) : null}
                </Command.Item>
              ))}
            </Command.List>
          </Command>
          {err && <div className="border-t border-line px-3 py-2 text-xs text-crit">{err}</div>}
          <div className="border-t border-line px-3 py-1.5 text-2xs text-ink-4">Switches this browser session only; your dtctl config stays as it is.</div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  )
}

function Current({ meta }: { meta: Meta }) {
  return (
    <div className="px-3 py-2.5">
      <div className="text-2xs font-medium tracking-wide text-ink-3 uppercase">Connected via dtctl</div>
      <div className="mt-1.5 grid grid-cols-[auto_1fr] gap-x-4 gap-y-0.5 text-xs">
        <span className="text-ink-3">Environment</span>
        <a href={meta.environment} target="_blank" rel="noreferrer" className="flex min-w-0 items-center gap-1 text-accent-ink hover:underline">
          <span className="truncate">{host(meta.environment)}</span>
          <ExternalLink className="size-3 shrink-0" />
        </a>
        <span className="text-ink-3">Safety</span>
        <span className="flex items-center gap-1.5">
          <Dot tone={toneOf(meta.safetyLevel)} />
          {meta.safetyLevel || 'readwrite'}
        </span>
        {meta.userEmail && (
          <>
            <span className="text-ink-3">User</span>
            <span className="truncate">{meta.userEmail}</span>
          </>
        )}
        <span className="text-ink-3">dtctl</span>
        <span>{meta.version}</span>
      </div>
    </div>
  )
}
