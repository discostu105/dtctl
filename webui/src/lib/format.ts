const nf0 = new Intl.NumberFormat('en-US', { maximumFractionDigits: 0 })
const nf1 = new Intl.NumberFormat('en-US', { maximumFractionDigits: 1 })
const nf2 = new Intl.NumberFormat('en-US', { maximumFractionDigits: 2 })

export function fmtInt(n: number) {
  return Number.isFinite(n) ? nf0.format(n) : '—'
}

/** 1,284 · 12.9K · 4.2M */
export function fmtCompact(n: number) {
  if (!Number.isFinite(n)) return '—'
  const a = Math.abs(n)
  if (a < 10_000) return a < 10 && a % 1 !== 0 ? nf2.format(n) : nf0.format(n)
  // thresholds sit where one decimal rounds up to the next unit: 999,960 is 1M, not 1,000K
  if (a < 999_950) return nf1.format(n / 1e3) + 'K'
  if (a < 999_950_000) return nf1.format(n / 1e6) + 'M'
  return nf1.format(n / 1e9) + 'B'
}

export function fmtPct(n: number, digits = 1) {
  if (!Number.isFinite(n)) return '—'
  // never round a non-zero share down to 0
  if (n > 0 && n < 0.1) return '<0.1%'
  if (n > 0 && digits === 0 && n < 0.5) return '<1%'
  return n.toFixed(digits).replace(/\.0+$/, '') + '%'
}

/** Durations: input in nanoseconds. */
export function fmtNs(ns: number) {
  if (!Number.isFinite(ns)) return '—'
  return fmtMs(ns / 1e6)
}
export function fmtUs(us: number) {
  if (!Number.isFinite(us)) return '—'
  return fmtMs(us / 1e3)
}
/** Durations given in seconds (e.g. gen_ai.server.time_to_first_token). */
export function fmtSec(s: number) {
  return fmtMs(s * 1000)
}
export function fmtMs(ms: number) {
  if (!Number.isFinite(ms)) return '—'
  if (ms === 0) return '0'
  const a = Math.abs(ms)
  if (a < 1) return nf2.format(ms * 1000) + ' µs'
  if (a < 1000) return (a < 10 ? nf1 : nf0).format(ms) + ' ms'
  if (a < 59_995) return nf2.format(ms / 1000) + ' s'
  // round to the smaller unit first, then split: 119.7 s is 2m 0s, never 1m 60s
  const sign = ms < 0 ? '-' : ''
  const s = Math.round(a / 1000)
  if (s < 3600) return `${sign}${Math.floor(s / 60)}m ${s % 60}s`
  const m = Math.round(a / 60_000)
  if (m < 1440) return `${sign}${Math.floor(m / 60)}h ${m % 60}m`
  const h = Math.round(a / 3_600_000)
  return `${sign}${Math.floor(h / 24)}d ${h % 24}h`
}

export function fmtBytes(b: number) {
  if (!Number.isFinite(b)) return '—'
  const u = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let i = 0
  while (Math.abs(b) >= 1024 && i < u.length - 1) {
    b /= 1024
    i++
  }
  return (i === 0 ? nf0 : nf1).format(b) + ' ' + u[i]
}

export function toDate(v: unknown): Date | null {
  if (v == null || v === '') return null
  if (v instanceof Date) return v
  const d = typeof v === 'number' ? new Date(v) : new Date(String(v))
  return Number.isNaN(d.getTime()) ? null : d
}

/** "3m ago" · "2h ago" · "5d ago" */
export function ago(v: unknown, now = Date.now()) {
  const d = toDate(v)
  if (!d) return '—'
  const s = Math.round((now - d.getTime()) / 1000)
  if (s < 0) return 'just now'
  if (s < 45) return `${s}s ago`
  if (s < 3600) return `${Math.round(s / 60)}m ago`
  if (s < 86400) return `${Math.round(s / 3600)}h ago`
  if (s < 86400 * 60) return `${Math.round(s / 86400)}d ago`
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })
}

/** Compact duration between two instants ("12m", "3h 4m"). */
export function span(from: unknown, to: unknown = Date.now()) {
  const a = toDate(from)
  const b = toDate(to)
  if (!a || !b) return '—'
  return fmtMs(b.getTime() - a.getTime()).replace(/ 0s$/, '').replace(/ 0m$/, '')
}

const dtf = new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
const tf = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })

export function fmtDateTime(v: unknown) {
  const d = toDate(v)
  return d ? dtf.format(d) : '—'
}
export function fmtTime(v: unknown) {
  const d = toDate(v)
  if (!d) return '—'
  const ms = String(d.getMilliseconds()).padStart(3, '0')
  return `${tf.format(d)}.${ms}`
}
/** Clock time for list cells: "14:32" today, "Oct 7 14:32" on other days. */
export function fmtClock(v: unknown, now = Date.now()) {
  const d = toDate(v)
  if (!d) return '—'
  const t = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', hour12: false })
  if (new Date(now).toDateString() === d.toDateString()) return t
  return `${d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })} ${t}`
}
export function fmtAbs(v: unknown) {
  const d = toDate(v)
  return d ? d.toISOString().replace('T', ' ').replace('Z', ' UTC') : ''
}

// Entity type words as people write them: acronyms upper-cased, Kubernetes
// kinds in their CamelCase, run-together AWS resource names split.
const TYPE_WORDS: Record<string, string> = {
  aws: 'AWS', ec2: 'EC2', rds: 'RDS', iam: 'IAM', sqs: 'SQS', sns: 'SNS', ses: 'SES', ecr: 'ECR', eks: 'EKS', ecs: 'ECS', s3: 'S3',
  vpc: 'VPC', kms: 'KMS', acm: 'ACM', db: 'DB', api: 'API', http: 'HTTP', dns: 'DNS', otel: 'OTel', genai: 'GenAI', cpu: 'CPU',
  dbinstance: 'DB instance', dbcluster: 'DB cluster', dbsnapshot: 'DB snapshot', dbsubnetgroup: 'DB subnet group',
  dbclustersnapshot: 'DB cluster snapshot', dbparametergroup: 'DB parameter group', dbclusterparametergroup: 'DB cluster parameter group',
  elasticloadbalancingv2: 'ELBv2', elasticloadbalancing: 'ELB', loadbalancer: 'load balancer', targetgroup: 'target group',
  networkinterface: 'network interface', securitygroup: 'security group', launchtemplate: 'launch template', routetable: 'route table',
  vpcendpoint: 'VPC endpoint', loggroup: 'log group', hostedzone: 'hosted zone', secretsmanager: 'Secrets Manager',
  cloudformation: 'CloudFormation', cloudtrail: 'CloudTrail', certificatemanager: 'Certificate Manager', managedpolicy: 'managed policy',
  replicaset: 'ReplicaSet', statefulset: 'StatefulSet', daemonset: 'DaemonSet', cronjob: 'CronJob', dynakube: 'DynaKube',
  customresourcedefinition: 'CustomResourceDefinition', horizontalpodautoscaler: 'HorizontalPodAutoscaler',
  persistentvolumeclaim: 'PersistentVolumeClaim', persistentvolume: 'PersistentVolume', networkpolicy: 'NetworkPolicy',
  configmap: 'ConfigMap', serviceaccount: 'ServiceAccount', activegate: 'ActiveGate', oneagent: 'OneAgent',
}
export function shortType(t: string) {
  const words = t
    .replace(/^K8S_/, '')
    .split('_')
    .filter(Boolean)
    .map((w) => w.toLowerCase())
  return words
    .map((w, i) => TYPE_WORDS[w] ?? (i === 0 ? w[0].toUpperCase() + w.slice(1) : w))
    .join(' ')
    .replace(/^./, (c) => c.toUpperCase())
}

export function titleCase(s: string) {
  return s
    .toLowerCase()
    .replace(/_/g, ' ')
    .replace(/\b\w/g, (c) => c.toUpperCase())
}

/** Formatter for a metric unit (curated vitals and discovered metrics). */
export function fmtUnit(unit: string) {
  switch (unit) {
    case '%':
      return (v: number) => fmtPct(v, v < 10 ? 1 : 0)
    case 'B':
      return (v: number) => fmtBytes(v)
    case 'B/s':
      return (v: number) => `${fmtBytes(v)}/s`
    case 'µs':
      return (v: number) => fmtUs(v)
    case 'ms':
      return (v: number) => fmtMs(v)
    case 's':
      return (v: number) => fmtSec(v)
    case 'mCores':
      return (v: number) => (v >= 1000 ? `${nf2.format(v / 1000)} cores` : `${Math.round(v)} m`)
    default:
      return (v: number) => fmtCompact(v)
  }
}
