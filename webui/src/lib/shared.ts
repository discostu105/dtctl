// Query specs shared across pages so they hit the same cache entry: Pulse
// warms Services, Security and Changes, so those pages open instantly.
import type { DqlSpec } from './api'
import { changesQuery, problemsQuery, servicesRedQuery, vulnsQuery } from './dql'
import { floorTf, sparkInterval, type Timeframe } from './timeframe'

export const activeProblemsSpec: DqlSpec = { query: problemsQuery({ status: 'ACTIVE', limit: 100 }), from: 'now-24h', ttl: 30 }

export const recentProblemsSpec: DqlSpec = { query: problemsQuery({ status: 'CLOSED', limit: 6 }), from: 'now-24h', ttl: 60 }

export const tfSpec = (tf: Timeframe, query: string, extra?: Partial<DqlSpec>): DqlSpec => ({ query, from: tf.from, to: tf.to, ...extra })

export const servicesSpec = (tf: Timeframe) => tfSpec(tf, servicesRedQuery(sparkInterval(tf.ms)))

export const vulnsSpec = (tf: Timeframe, lens: 'open' | 'muted' | 'all' = 'open') => {
  const f = floorTf(tf, '24h')
  return tfSpec(f, vulnsQuery(lens), { ttl: 120 })
}

export const changesSpec = (tf: Timeframe) => tfSpec(tf, changesQuery(300))

export const ERROR_LEVELS = '{"ERROR","SEVERE","FATAL","CRITICAL","EMERGENCY","ALERT"}'
