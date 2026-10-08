import clsx from 'clsx'
import {
  Boxes, Box, Cloud, Container, Cpu, Database, Globe, HardDrive, Layers, Network, Server, Share2, Sparkles, Workflow, type LucideIcon,
} from 'lucide-react'
import { Link } from 'wouter'
import { prefetchDql } from '../lib/api'
import { detailQuery, typeOfId } from '../lib/dql'
import { shortType } from '../lib/format'
import { entityHref } from '../lib/links'
import { Tip } from './ui'

export function typeIcon(type: string): LucideIcon {
  if (type === 'SERVICE') return Share2
  if (type === 'HOST' || type === 'OTEL_HOST') return Server
  if (type === 'PROCESS' || type === 'OTEL_PROCESS') return Cpu
  if (type === 'CONTAINER') return Container
  if (type === 'K8S_POD') return Box
  if (type === 'K8S_NODE') return HardDrive
  if (type === 'K8S_NAMESPACE') return Layers
  if (type === 'K8S_CLUSTER') return Boxes
  if (type.startsWith('K8S_')) return Workflow
  if (type === 'FRONTEND') return Globe
  if (type.startsWith('DB_')) return Database
  if (type.startsWith('GENAI_')) return Sparkles
  if (type.startsWith('AWS_') || type.startsWith('AZURE_') || type.startsWith('GCP_')) return Cloud
  return Network
}

export function TypeIcon({ type, className }: { type: string; className?: string }) {
  const I = typeIcon(type)
  return <I className={clsx('size-3.5 shrink-0 text-ink-3', className)} />
}

/** A link to any entity, with hover prefetch of its detail record. */
export function EntityLink({
  id,
  name,
  type,
  className,
  showType,
}: {
  id: string
  name?: string | null
  type?: string
  className?: string
  showType?: boolean
}) {
  const t = type || typeOfId(id)
  const label = (name && String(name).trim()) || id
  return (
    <Link
      href={entityHref(id, name ?? undefined)}
      onMouseEnter={() => t && prefetchDql({ query: detailQuery({ id, type: t }) })}
      onClick={(e) => e.stopPropagation()}
      className={clsx('inline-flex min-w-0 items-center gap-1.5 rounded text-ink hover:text-accent-ink hover:underline decoration-accent/40 underline-offset-2', className)}
    >
      <TypeIcon type={t} />
      <span className="truncate">{label}</span>
      {showType && <span className="shrink-0 text-2xs text-ink-3">{shortType(t)}</span>}
    </Link>
  )
}

export function EntityChip({ id, name, type }: { id: string; name?: string | null; type?: string }) {
  const t = type || typeOfId(id)
  return (
    <Tip content={`${shortType(t)} · ${id}`}>
      <span className="inline-flex max-w-full">
        <EntityLink
          id={id}
          name={name}
          type={t}
          className="h-6 rounded-md border border-line bg-sunken px-2 text-xs hover:border-accent/40 hover:no-underline"
        />
      </span>
    </Tip>
  )
}
