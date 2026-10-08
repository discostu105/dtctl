import clsx from 'clsx'
import { ExternalLink } from 'lucide-react'
import type { ReactNode } from 'react'
import { CopyButton } from './ui'

const TILE = {
  accent: 'bg-accent-wash text-accent-ink',
  crit: 'bg-crit-wash text-crit',
  muted: 'bg-line text-ink-3',
}

/**
 * The header of every detail page (problem, entity, trace, session,
 * conversation): a tone tile with the kind's icon, the title with its badges,
 * a meta row (ID with copy first, then facts), and actions on the right.
 * Tone: `crit` when something is failing or active, `muted` when it is over,
 * else `accent`.
 */
export function DetailHeader({
  icon,
  tone = 'accent',
  title,
  badges,
  meta,
  below,
  actions,
}: {
  icon: ReactNode
  tone?: keyof typeof TILE
  title: ReactNode
  badges?: ReactNode
  meta?: ReactNode
  below?: ReactNode
  actions?: ReactNode
}) {
  return (
    <div className="mb-4 flex flex-wrap items-start gap-4">
      <div className={clsx('flex size-10 shrink-0 items-center justify-center rounded-xl [&_svg]:size-5', TILE[tone])}>{icon}</div>
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <h1 className="line-clamp-2 min-w-0 text-xl font-semibold tracking-tight">{title}</h1>
          {badges}
        </div>
        {meta && <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-ink-3">{meta}</div>}
        {below}
      </div>
      {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}

/** The ID in a detail header's meta row, copyable. */
export function IdCopy({ id, label }: { id: string; label: string }) {
  return (
    <span className="inline-flex items-center gap-1 font-mono text-xs">
      {id}
      <CopyButton value={id} label={label} />
    </span>
  )
}

/** The one way out to the Dynatrace web UI (renders nothing without a URL). */
export function OpenInDynatrace({ href }: { href?: string | null }) {
  if (!href) return null
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-line bg-sunken px-3 text-sm text-ink-2 hover:border-line-strong hover:text-ink"
    >
      Open in Dynatrace <ExternalLink className="size-3.5" />
    </a>
  )
}
