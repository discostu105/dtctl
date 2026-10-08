import { q, smartscapeField, type Entity, type Vital } from './dql'

// Metric discovery for any Smartscape entity: every metric series carrying the
// entity's dt.smartscape.<type> dimension belongs to it. That turns cloud
// resources (RDS, EC2, ELB, EKS, …) and anything else with metrics into charts
// without a hand-written list per type.

export function metricDiscoveryQuery(e: Entity) {
  return `metrics
| filter toString(${smartscapeField(e.type)}) == ${q(e.id)}
| summarize series = count(), by:{metric.key}
| sort metric.key asc
| limit 120`
}

/** "cloud.aws.rds.CPUUtilization.By.DBInstanceIdentifier" → "cloud.aws.rds" + "CPUUtilization" */
export function splitMetric(key: string) {
  const base = key.replace(/\.By\..*$/, '')
  const i = base.lastIndexOf('.')
  return { group: i > 0 ? base.slice(0, i) : '', leaf: i > 0 ? base.slice(i + 1) : base }
}

/** CPUUtilization → "CPU utilization", network_received_data → "Network received data" */
export function prettyMetric(key: string) {
  const { leaf } = splitMetric(key)
  const words = leaf
    .replace(/_/g, ' ')
    .replace(/([a-z\d])([A-Z])/g, '$1 $2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1 $2')
    .split(/\s+/)
    .filter(Boolean)
    .map((w, i) => (/^[A-Z\d]{2,}$/.test(w) ? w : i === 0 ? w[0].toUpperCase() + w.slice(1).toLowerCase() : w.toLowerCase()))
  return words.join(' ') || key
}

/** Units are not in the metric metadata; the names are consistent enough (CloudWatch especially). */
export function unitFor(key: string): Vital['unit'] {
  const { group, leaf } = splitMetric(key)
  const k = leaf.toLowerCase()
  if (/percent|utilization|_percentage|ratio_pct/.test(k)) return '%'
  if (/latency|responsetime|response_time|duration/.test(k)) return group.startsWith('cloud.aws') && !group.includes('bedrock') ? 's' : 'ms'
  if (/throughput|bytespersecond|bytes_per_sec/.test(k)) return 'B/s'
  if (/bytes|memory|swapusage|storage_size|size_bytes/.test(k)) return 'B'
  return ''
}

export function aggFor(key: string): Vital['agg'] {
  const k = splitMetric(key).leaf.toLowerCase()
  return /count$|_total$|^invocations|errors?$|requests?$|restarts$|ops$|pullcount/.test(k) && !/flowcount|hostcount|connections/.test(k) ? 'sum' : 'avg'
}

export function discoveredVital(key: string): Vital {
  return { key, title: prettyMetric(key), unit: unitFor(key), agg: aggFor(key) }
}

/** The handful worth a headline chart when a type has no curated vitals. */
export function headlineVitals(keys: string[], n = 3): Vital[] {
  const rank = [/cpu.?utili[sz]ation|cpu.?usage/i, /latency|response/i, /connections|activeflow|requests?$|invocations$/i, /memory|freeable/i, /error|5xx|fail/i, /network.?(in|receive)/i]
  const picked: string[] = []
  for (const re of rank) {
    const k = keys.find((x) => re.test(splitMetric(x).leaf) && !picked.includes(x))
    if (k) picked.push(k)
    if (picked.length === n) break
  }
  return picked.map(discoveredVital)
}
