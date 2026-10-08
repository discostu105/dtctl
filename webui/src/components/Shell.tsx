import { useIsFetching, useQueryClient } from '@tanstack/react-query'
import * as Popover from '@radix-ui/react-popover'
import clsx from 'clsx'
import {
  Activity, AlertOctagon, Boxes, FileText, GitCommitVertical, Moon, Network, RefreshCw, ScrollText, Search, Server, Share2, ShieldAlert, Sun,
  Terminal, Waypoints, Keyboard, Timer, MonitorSmartphone, Sparkles,
} from 'lucide-react'
import { useEffect, useRef, type ReactNode } from 'react'
import { Link, useLocation } from 'wouter'
import { forceFresh, prefetchDql, useDql, type DqlSpec } from '../lib/api'
import { problemsQuery } from '../lib/dql'
import { activeProblemsSpec, changesSpec, servicesSpec, tfSpec, vulnsSpec } from '../lib/shared'
import { autoRefreshStore, helpStore, openFilterPopup, paletteStore, tfPickerStore, titleStore, toggleTheme, useStore } from '../lib/store'
import { floorTf, getTimeframe, syncUrl, useTimeframe } from '../lib/timeframe'
import { useThemeVersion } from './Chart'
import { QueryActivity } from './Activity'
import { useNavTrail } from './BackLink'
import { TenantSwitcher } from './TenantSwitcher'
import { TimeframePicker } from './TimeframePicker'
import { Kbd, Tip } from './ui'

export const NAV: { href: string; label: string; icon: typeof Activity; key: string; group: 0 | 1 | 2 }[] = [
  { href: '/', label: 'Pulse', icon: Activity, key: 'h', group: 0 },
  { href: '/problems', label: 'Problems', icon: AlertOctagon, key: 'p', group: 0 },
  { href: '/services', label: 'Services', icon: Share2, key: 's', group: 1 },
  { href: '/k8s', label: 'Kubernetes', icon: Boxes, key: 'k', group: 1 },
  { href: '/hosts', label: 'Hosts', icon: Server, key: 'o', group: 1 },
  { href: '/logs', label: 'Logs', icon: ScrollText, key: 'l', group: 0 },
  { href: '/traces', label: 'Traces', icon: Waypoints, key: 't', group: 0 },
  { href: '/rum', label: 'Experience', icon: MonitorSmartphone, key: 'u', group: 0 },
  { href: '/ai', label: 'AI', icon: Sparkles, key: 'a', group: 0 },
  { href: '/changes', label: 'Changes', icon: GitCommitVertical, key: 'c', group: 0 },
  { href: '/security', label: 'Security', icon: ShieldAlert, key: 'v', group: 0 },
  { href: '/query', label: 'Query', icon: Terminal, key: 'q', group: 2 },
  { href: '/smartscape', label: 'Smartscape', icon: Network, key: 'x', group: 2 },
  { href: '/docs', label: 'Documents', icon: FileText, key: 'd', group: 2 },
]

function isActive(loc: string, href: string) {
  if (href === '/') return loc === '/'
  if (loc.startsWith('/e/')) {
    // Entity pages light up the section they belong to.
    const id = decodeURIComponent(loc.slice(3))
    const section = id.startsWith('SERVICE-') ? '/services' : id.startsWith('K8S_') || id.startsWith('CONTAINER-') ? '/k8s' : id.startsWith('HOST-') ? '/hosts' : id.startsWith('FRONTEND-') ? '/rum' : id.startsWith('GENAI_') ? '/ai' : '/smartscape'
    return href === section
  }
  return loc === href || loc.startsWith(href + '/')
}

/** Hovering a rail item warms that page's main query. */
function prefetchSection(href: string) {
  const tf = getTimeframe()
  const specs: Record<string, () => DqlSpec> = {
    '/services': () => servicesSpec(tf),
    '/problems': () => tfSpec(floorTf(tf, '24h'), problemsQuery({ limit: 500 }), { ttl: 30 }),
    '/security': () => vulnsSpec(tf),
    '/changes': () => changesSpec(tf),
    // (AI and RUM are not prefetched: they scan spans and user events, which is
    // too heavy to start on a mere hover in big tenants.)
  }
  const spec = specs[href]?.()
  if (spec) prefetchDql(spec)
}

function Rail() {
  const [loc] = useLocation()
  const active = useDql(activeProblemsSpec, { refetchInterval: 60_000 })
  const nActive = active.data?.records.length ?? 0
  const groups = [0, 1, 2] as const
  const order = [...NAV].sort((a, b) => a.group - b.group)
  return (
    <nav className="flex w-[200px] shrink-0 flex-col border-r border-line bg-bg px-2 py-3 max-lg:w-14">
      <Link href="/" className="mb-4 flex items-center gap-2 px-2">
        <img src="/favicon.svg" className="size-6" alt="" />
        <span className="font-semibold tracking-tight max-lg:hidden">
          dtctl <span className="text-ink-3">web</span>
        </span>
      </Link>
      {groups.map((g) => (
        <div key={g} className={clsx('flex flex-col gap-px', g > 0 && 'mt-3 border-t border-line pt-3')}>
          {order
            .filter((n) => n.group === g)
            .map((n) => {
              const on = isActive(loc, n.href)
              return (
                <Tip key={n.href} content={<span className="flex items-center gap-2">{n.label} <Kbd>G</Kbd><Kbd>{n.key.toUpperCase()}</Kbd></span>} side="right">
                  <Link
                    href={n.href}
                    onMouseEnter={() => prefetchSection(n.href)}
                    className={clsx(
                      'group flex h-8 items-center gap-2.5 rounded-lg px-2 text-sm transition-colors max-lg:justify-center',
                      on ? 'bg-panel-hover text-ink shadow-[inset_0_0_0_1px_var(--line)]' : 'text-ink-2 hover:bg-panel hover:text-ink',
                    )}
                  >
                    <n.icon className={clsx('size-4 shrink-0', on ? 'text-accent' : 'text-ink-3 group-hover:text-ink-2')} />
                    <span className="flex-1 max-lg:hidden">{n.label}</span>
                    {n.href === '/problems' && nActive > 0 && (
                      <span className="tnum flex h-[18px] min-w-[18px] items-center justify-center rounded-full bg-crit px-1 text-2xs font-semibold text-white max-lg:hidden">
                        {nActive}
                      </span>
                    )}
                  </Link>
                </Tip>
              )
            })}
        </div>
      ))}
      <div className="mt-auto flex flex-col gap-px pt-3 max-lg:items-center">
        <button
          type="button"
          onClick={() => helpStore.set(true)}
          className="flex h-8 items-center gap-2.5 rounded-lg px-2 text-sm text-ink-3 hover:bg-panel hover:text-ink-2"
        >
          <Keyboard className="size-4" />
          <span className="max-lg:hidden">Shortcuts</span>
          <Kbd className="ml-auto max-lg:hidden">?</Kbd>
        </button>
      </div>
    </nav>
  )
}

function RefreshButton() {
  const qc = useQueryClient()
  const fetching = useIsFetching({ queryKey: ['dql'] })
  const auto = useStore(autoRefreshStore)
  return (
    <div className="flex items-center rounded-lg border border-line bg-sunken">
      <Tip content={<span className="flex items-center gap-2">Refresh <Kbd>R</Kbd></span>}>
        <button
          type="button"
          onClick={() => refreshAll(qc)}
          className="inline-flex h-[30px] w-8 items-center justify-center text-ink-2 hover:text-ink"
          aria-label="Refresh"
        >
          <RefreshCw className={clsx('size-3.5', fetching > 0 && 'animate-spin text-accent')} />
        </button>
      </Tip>
      <Popover.Root>
        <Tip content="Auto-refresh">
          <Popover.Trigger asChild>
            <button
              type="button"
              className={clsx('inline-flex h-[30px] items-center gap-1 border-l border-line px-2 text-xs', auto ? 'text-accent-ink' : 'text-ink-3 hover:text-ink-2')}
            >
              <Timer className="size-3.5" />
              {auto ? `${auto}s` : 'off'}
            </button>
          </Popover.Trigger>
        </Tip>
        <Popover.Portal>
          <Popover.Content align="end" sideOffset={6} className="anim-pop z-50 w-36 rounded-lg bg-raised p-1 shadow-pop">
            {[0, 15, 30, 60].map((s) => (
              <Popover.Close asChild key={s}>
                <button
                  type="button"
                  onClick={() => autoRefreshStore.set(s)}
                  className={clsx('flex h-7 w-full items-center rounded-md px-2 text-sm hover:bg-panel-hover', auto === s ? 'text-ink' : 'text-ink-2')}
                >
                  {s === 0 ? 'Off' : `Every ${s}s`}
                </button>
              </Popover.Close>
            ))}
          </Popover.Content>
        </Popover.Portal>
      </Popover.Root>
    </div>
  )
}

function ThemeIcon() {
  useThemeVersion()
  const t = document.documentElement.dataset.theme ?? (matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark')
  return t === 'dark' ? <Sun className="size-3.5" /> : <Moon className="size-3.5" />
}

function refreshAll(qc: ReturnType<typeof useQueryClient>) {
  forceFresh()
  qc.invalidateQueries({ queryKey: ['dql'] })
}

function TopBar() {
  const title = useStore(titleStore)
  return (
    <header className="flex h-12 shrink-0 items-center gap-2 border-b border-line bg-bg/80 px-4 backdrop-blur">
      <div className="min-w-0 truncate text-sm font-medium text-ink-2">{title}</div>
      <button
        type="button"
        onClick={() => paletteStore.set({ open: true })}
        className="mx-auto inline-flex h-8 w-[min(420px,40vw)] items-center gap-2 rounded-lg border border-line bg-sunken px-2.5 text-sm text-ink-3 transition-colors hover:border-line-strong hover:text-ink-2"
      >
        <Search className="size-3.5" />
        <span className="flex-1 truncate text-left">Search entities, problems, traces, or jump to…</span>
        <Kbd>⌘K</Kbd>
      </button>
      <TimeframePicker />
      <RefreshButton />
      <Tip content="Toggle theme">
        <button
          type="button"
          onClick={toggleTheme}
          className="inline-flex h-8 w-8 items-center justify-center rounded-lg border border-line bg-sunken text-ink-2 hover:text-ink"
          aria-label="Toggle theme"
        >
          <ThemeIcon />
        </button>
      </Tip>
      <QueryActivity />
      <TenantSwitcher />
    </header>
  )
}

/** Global keyboard: ⌘K, /, t, r, ?, and g-prefixed jumps. */
function useHotkeys() {
  const [, navigate] = useLocation()
  const qc = useQueryClient()
  const gPending = useRef(0)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      const typing = t.closest('input, textarea, [contenteditable=true]')
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        paletteStore.set({ open: !paletteStore.get().open })
        return
      }
      if (typing || e.metaKey || e.ctrlKey || e.altKey) return
      if (Date.now() - gPending.current < 1200) {
        gPending.current = 0
        const n = NAV.find((x) => x.key === e.key.toLowerCase())
        if (n) {
          e.preventDefault()
          navigate(n.href)
        }
        return
      }
      switch (e.key) {
        case 'g':
          gPending.current = Date.now()
          break
        case '/': {
          e.preventDefault()
          const f = document.querySelector<HTMLInputElement>('[data-filter]')
          if (f) f.focus()
          else paletteStore.set({ open: true })
          break
        }
        case 't':
          e.preventDefault()
          tfPickerStore.set(true)
          break
        case 'f':
          if (openFilterPopup()) e.preventDefault()
          break
        case 'r':
          refreshAll(qc)
          break
        case '?':
          helpStore.set(!helpStore.get())
          break
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [navigate, qc])
}

function useAutoRefresh() {
  const auto = useStore(autoRefreshStore)
  const qc = useQueryClient()
  useEffect(() => {
    if (!auto) return
    const t = setInterval(() => {
      if (document.visibilityState === 'visible') refreshAll(qc)
    }, auto * 1000)
    return () => clearInterval(t)
  }, [auto, qc])
}

export function Shell({ children }: { children: ReactNode }) {
  useHotkeys()
  useAutoRefresh()
  useNavTrail()
  const [loc] = useLocation()
  const tf = useTimeframe()
  // Keep ?tf= in the URL on every navigation and timeframe change.
  useEffect(() => syncUrl(), [loc, tf])
  return (
    <div className="flex h-full">
      <Rail />
      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar />
        <main className="min-h-0 flex-1 overflow-auto">{children}</main>
      </div>
    </div>
  )
}

export function Help() {
  const open = useStore(helpStore)
  if (!open) return null
  const groups: [string, [string[], string][]][] = [
    [
      'Anywhere',
      [
        [['⌘', 'K'], 'Search & jump'],
        [['/'], 'Filter the current list'],
        [['F'], 'Filter by attribute, tag or label'],
        [['T'], 'Change timeframe'],
        [['R'], 'Refresh'],
        [['?'], 'Toggle this help'],
      ],
    ],
    ['Go to', NAV.map((n) => [['G', n.key.toUpperCase()], n.label] as [string[], string])],
    [
      'Lists',
      [
        [['J'], 'Next row'],
        [['K'], 'Previous row'],
        [['Home', 'End'], 'First / last row'],
        [['↵'], 'Open'],
        [['⌘', 'click'], 'Open in new tab'],
        [['M'], 'Maximize / restore detail panel'],
        [['Esc'], 'Close panel / clear filter'],
      ],
    ],
    [
      'Trace',
      [
        [['←', '→'], 'Collapse / expand span'],
        [['N'], 'Next search match (⇧N previous)'],
        [['E'], 'Next failed span'],
        [['C'], 'Critical path'],
        [['Z'], 'Zoom to span · 0 resets'],
      ],
    ],
    [
      'Query',
      [
        [['⌘', '↵'], 'Run query'],
        [['⌘', '↑/↓'], 'History'],
      ],
    ],
  ]
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-[2px]" onClick={() => helpStore.set(false)}>
      <div className="anim-pop w-[min(760px,92vw)] rounded-xl bg-raised p-5 shadow-pop" onClick={(e) => e.stopPropagation()}>
        <div className="mb-4 flex items-center gap-2 text-base font-semibold">
          <Keyboard className="size-4 text-accent" /> Keyboard shortcuts
        </div>
        <div className="grid grid-cols-2 gap-x-8 gap-y-5 max-md:grid-cols-1">
          {groups.map(([g, items]) => (
            <div key={g}>
              <div className="mb-1.5 text-2xs font-medium tracking-wide text-ink-3 uppercase">{g}</div>
              {items.map(([keys, label]) => (
                <div key={label} className="flex h-7 items-center justify-between text-sm">
                  <span className="text-ink-2">{label}</span>
                  <span className="flex gap-1">
                    {keys.map((k) => (
                      <Kbd key={k}>{k}</Kbd>
                    ))}
                  </span>
                </div>
              ))}
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
