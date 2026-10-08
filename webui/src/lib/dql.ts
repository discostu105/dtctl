// DQL builders. Ported from dynatui's catalog (dynatui/internal/tui/catalog):
// the dual entity-ID-era filters, problem dedupe by display_id and the RED
// queries were all validated live there.

export interface Entity {
  id: string
  type: string
  name?: string
}

export const q = (s: string) => JSON.stringify(s)

/** "K8S_POD-0A1B…" → "K8S_POD" */
export function typeOfId(id: string) {
  const i = id.lastIndexOf('-')
  return i > 0 ? id.slice(0, i) : ''
}

export function smartscapeField(type: string) {
  if (type.startsWith('GENAI_')) return 'dt.smartscape.gen_ai.' + type.slice(6).toLowerCase()
  return 'dt.smartscape.' + type.toLowerCase()
}

function legacyField(type: string) {
  return ({ HOST: 'dt.entity.host', SERVICE: 'dt.entity.service', PROCESS: 'dt.entity.process_group_instance' } as Record<string, string>)[type]
}

function k8sNameFilter(e: Entity) {
  if (!e.name) return ''
  switch (e.type) {
    case 'K8S_POD':
      return `k8s.pod.name == ${q(e.name)}`
    case 'K8S_NAMESPACE':
      return `k8s.namespace.name == ${q(e.name)}`
    case 'K8S_NODE':
      return `k8s.node.name == ${q(e.name)}`
    case 'K8S_CLUSTER':
      return `k8s.cluster.name == ${q(e.name)}`
    case 'K8S_DEPLOYMENT':
    case 'K8S_STATEFULSET':
    case 'K8S_DAEMONSET':
      return `(k8s.workload.kind == ${q(e.type.slice(4).toLowerCase())} and k8s.workload.name == ${q(e.name)})`
  }
  return ''
}

/** logs/events filter for one entity, matching both ID eras + source entity. */
export function signalFilter(e: Entity) {
  const parts: string[] = [`${smartscapeField(e.type)} == toSmartscapeId(${q(e.id)})`]
  // Classic IDs (PROCESS_GROUP-…, KUBERNETES_CLUSTER-…) are stamped on records
  // as dt.entity.<type>; unknown fields are null in DQL, so this arm is
  // harmless for Smartscape-only types.
  const lf = legacyField(e.type) ?? `dt.entity.${e.type.toLowerCase()}`
  parts.push(`${lf} == ${q(e.id)}`)
  const kf = k8sNameFilter(e)
  if (kf) parts.push(kf)
  parts.push(`dt.smartscape_source.id == toSmartscapeId(${q(e.id)})`)
  return parts.join(' or ')
}

export function signalFilterAll(es: Entity[]) {
  if (es.length === 1) return signalFilter(es[0])
  return es.map((e) => `(${signalFilter(e)})`).join(' or ')
}

export function spanScopable(type: string) {
  return type === 'SERVICE' || type === 'CONTAINER' || type.startsWith('K8S_') || type.startsWith('GENAI_')
}

export function spanFilter(e: Entity) {
  return `${smartscapeField(e.type)} == toSmartscapeId(${q(e.id)})`
}

export function problemFilter(id: string) {
  return `matchesPhrase(arrayToString(smartscape.affected_entity.ids, delimiter:","), ${q(id)}) or matchesPhrase(arrayToString(affected_entity_ids, delimiter:","), ${q(id)})`
}

/** Full-text search term → DQL filter (case-insensitive phrase on content). */
export function searchFilter(term: string, field = 'content') {
  const t = term.trim()
  if (!t) return ''
  return `contains(${field}, ${q(t)}, caseSensitive:false)`
}

// ── problems ────────────────────────────────────────────────────────────────

/** Latest state per problem (problems are re-emitted on every update). */
export function problemsQuery(opts: { status?: 'ACTIVE' | 'CLOSED'; entityId?: string; limit?: number } = {}) {
  return [
    `fetch dt.davis.problems`,
    `| filter not(dt.davis.is_duplicate)`,
    opts.entityId ? `| filter ${problemFilter(opts.entityId)}` : '',
    `| sort timestamp asc`,
    `| summarize { status = takeLast(event.status), name = takeLast(event.name), category = takeLast(event.category), start = takeLast(event.start), end = takeLast(event.end), affected = takeLast(affected_entity_names), affected_ids = takeLast(smartscape.affected_entity.ids), affected_types = takeLast(smartscape.affected_entity.types), root = takeLast(root_cause_entity_name), id = takeLast(event.id), impact = takeLast(dt.davis.impact_level) }, by:{display_id}`,
    opts.status ? `| filter status == ${q(opts.status)}` : '',
    `| sort status asc, start desc`,
    `| limit ${opts.limit ?? 300}`,
  ]
    .filter(Boolean)
    .join('\n')
}

export function problemDetailQuery(displayId: string) {
  return `fetch dt.davis.problems, from:now()-30d
| filter display_id == ${q(displayId)}
| sort timestamp desc
| limit 1`
}

export function evidenceQuery(eventIds: string[]) {
  return `fetch dt.davis.events, from:now()-30d
| filter in(event.id, {${eventIds.map(q).join(', ')}})
| sort timestamp asc
| summarize { name = takeLast(event.name), type = takeLast(event.type), category = takeLast(event.category), start = takeLast(event.start), end = takeLast(event.end), status = takeLast(event.status), entity = takeLast(dt.source_entity.name), entity_id = takeLast(dt.smartscape_source.id), root = takeLast(dt.davis.is_rootcause_relevant) }, by:{event.id}
| sort start asc`
}

// ── services ────────────────────────────────────────────────────────────────

export function servicesRedQuery(interval: string) {
  return `timeseries { req = sum(dt.service.request.count, default:0), fail = sum(dt.service.request.failure_count, default:0), rt = avg(dt.service.request.response_time) }, by:{dt.smartscape.service}, interval:${interval}
| lookup [smartscapeNodes SERVICE | fields id, name, k8s.namespace.name, dt.service.sdv1_type], sourceField:dt.smartscape.service, lookupField:id, prefix:"s."
| fieldsAdd total = arraySum(req), failed = arraySum(fail), latency = arrayAvg(rt)
| fieldsAdd failure_rate = if(total > 0, 100.0 * failed / total, else: 0.0)
| sort failed desc, total desc
| limit 500`
}

export function serviceListQuery() {
  return `smartscapeNodes SERVICE | fields id, name, k8s.namespace.name, dt.service.sdv1_type, lifetime | sort name asc | limit 1000`
}

// ── logs ────────────────────────────────────────────────────────────────────

export function logsQuery(filters: string[], limit = 500) {
  return ['fetch logs', ...filters.filter(Boolean).map((f) => `| filter ${f}`), '| sort timestamp desc', `| limit ${limit}`].join('\n')
}

export function logHistogramQuery(filters: string[], interval: string) {
  return [
    'fetch logs',
    ...filters.filter(Boolean).map((f) => `| filter ${f}`),
    `| fieldsAdd lvl = if(in(loglevel, {"ERROR","SEVERE","FATAL","CRITICAL","EMERGENCY","ALERT"}), "error", else: if(in(loglevel, {"WARN","WARNING"}), "warn", else: "other"))`,
    `| makeTimeseries count = count(), by:{lvl}, interval:${interval}`,
  ].join('\n')
}

export function facetQuery(filters: string[], field: string, limit = 12) {
  return [
    'fetch logs',
    ...filters.filter(Boolean).map((f) => `| filter ${f}`),
    `| summarize count = count(), by:{v = ${field}}`,
    '| sort count desc',
    `| limit ${limit}`,
  ].join('\n')
}

// ── spans ───────────────────────────────────────────────────────────────────

export const SPAN_LENSES: { key: string; label: string; filter: string }[] = [
  { key: 'roots', label: 'Requests', filter: 'request.is_root_span == true' },
  { key: 'errors', label: 'Errors', filter: 'request.is_failed == true or span.status_code == "error"' },
  { key: 'slow', label: 'Slowest', filter: 'request.is_root_span == true' },
  { key: 'db', label: 'Database', filter: 'isNotNull(db.system.name) or isNotNull(db.system)' },
  { key: 'genai', label: 'GenAI', filter: 'isNotNull(gen_ai.operation.name) or isNotNull(llm.request.type)' },
  { key: 'all', label: 'All spans', filter: '' },
]

export function spansQuery(lens: string, extra: string[], limit = 300) {
  const l = SPAN_LENSES.find((x) => x.key === lens) ?? SPAN_LENSES[0]
  return [
    'fetch spans',
    l.filter ? `| filter ${l.filter}` : '',
    ...extra.filter(Boolean).map((f) => `| filter ${f}`),
    `| fields start_time, end_time, duration, span.name, trace.id, span.id, span.kind, service = coalesce(dt.service.name, service.name), dt.smartscape.service, request.is_failed, span.status_code, http.response.status_code, http.request.method, endpoint.name, db.system = coalesce(db.system.name, db.system), db.query.text, gen_ai.request.model`,
    lens === 'slow' ? '| sort duration desc' : '| sort start_time desc',
    `| limit ${limit}`,
  ]
    .filter(Boolean)
    .join('\n')
}

// ── topology ────────────────────────────────────────────────────────────────

export function detailQuery(e: { id: string; type: string }) {
  return `smartscapeNodes ${q(e.type)}
| filter id == toSmartscapeId(${q(e.id)})
| limit 1`
}

export function edgesQuery(id: string) {
  return `smartscapeEdges "*"
| filter source_id == toSmartscapeId(${q(id)}) or target_id == toSmartscapeId(${q(id)})
| fields source_id, source_type, type, target_id, target_type
| limit 300`
}

export function namesQuery(ids: string[]) {
  return `smartscapeNodes "*"
| filter in(id, {${ids.map((i) => `toSmartscapeId(${q(i)})`).join(', ')}})
| fields id, name, type
| limit ${ids.length + 10}`
}

/** Name search, most useful types first, exact/prefix matches before substrings. */
export function searchEntitiesQuery(term: string) {
  return `smartscapeNodes "*"
| filter contains(name, ${q(term)}, caseSensitive:false)
| fieldsAdd rank = coalesce(if(type == "SERVICE", 0), if(in(type, {"K8S_DEPLOYMENT", "K8S_STATEFULSET", "K8S_DAEMONSET", "HOST", "FRONTEND"}), 1), if(in(type, {"K8S_NAMESPACE", "K8S_CLUSTER", "K8S_NODE", "DB_INSTANCE_POSTGRES", "DB_DATABASE_POSTGRES"}), 2), if(in(type, {"K8S_POD", "PROCESS", "K8S_CRONJOB", "K8S_JOB"}), 3), 4), match = if(lower(name) == lower(${q(term)}), 0, else: if(startsWith(lower(name), lower(${q(term)})), 1, else: 2))
| fields id, name, type, ns = k8s.namespace.name, rank, match
| sort rank asc, match asc, name asc
| limit 120`
}

export function censusQuery() {
  return `smartscapeNodes "*"
| summarize count = count(), by:{type}
| sort count desc
| limit 300`
}

export function instancesQuery(type: string) {
  return `smartscapeNodes ${q(type)}
| fieldsAdd display = coalesce(if(name != "", name), aws.arn, id)
| fields id, name = display, type, k8s.namespace.name, k8s.cluster.name, aws.region, lifetime
| sort name asc
| limit 1000`
}

/** Canned vital charts per entity type (dynatui MetricsFor). */
export interface Vital {
  key: string
  title: string
  unit: '%' | 'B' | 'B/s' | 'µs' | 'ms' | 'mCores' | '' | 's'
  agg: 'avg' | 'sum' | 'max' | 'min'
  filter?: (e: Entity) => string
}

const eq = (e: Entity) => `${smartscapeField(e.type)} == toSmartscapeId(${q(e.id)})`

export function vitalsFor(type: string): Vital[] {
  switch (type) {
    case 'SERVICE':
      return [
        { key: 'dt.service.request.count', title: 'Throughput', unit: '', agg: 'sum' },
        { key: 'dt.service.request.failure_count', title: 'Failed requests', unit: '', agg: 'sum' },
        { key: 'dt.service.request.response_time', title: 'Response time', unit: 'µs', agg: 'avg' },
      ]
    case 'HOST':
      return [
        { key: 'dt.host.cpu.usage', title: 'CPU usage', unit: '%', agg: 'avg' },
        { key: 'dt.host.memory.usage', title: 'Memory usage', unit: '%', agg: 'avg' },
        { key: 'dt.host.disk.used.percent', title: 'Disk used (fullest)', unit: '%', agg: 'max' },
        { key: 'dt.host.net.nic.bytes_rx', title: 'Network received', unit: 'B/s', agg: 'sum' },
      ]
    case 'PROCESS':
      return [
        { key: 'dt.process.cpu.usage', title: 'CPU usage', unit: '%', agg: 'avg' },
        { key: 'dt.process.memory.usage', title: 'Memory usage', unit: '%', agg: 'avg' },
        { key: 'dt.process.memory.working_set_size', title: 'Working set', unit: 'B', agg: 'avg' },
      ]
    case 'CONTAINER':
      return [
        { key: 'dt.kubernetes.container.cpu_usage', title: 'CPU usage', unit: 'mCores', agg: 'avg' },
        { key: 'dt.kubernetes.container.memory_working_set', title: 'Memory working set', unit: 'B', agg: 'avg' },
        { key: 'dt.kubernetes.container.cpu_throttled', title: 'CPU throttled', unit: 'mCores', agg: 'avg' },
      ]
    case 'K8S_POD':
      return [
        { key: 'dt.kubernetes.container.cpu_usage', title: 'CPU usage', unit: 'mCores', agg: 'sum' },
        { key: 'dt.kubernetes.container.memory_working_set', title: 'Memory working set', unit: 'B', agg: 'sum' },
        { key: 'dt.kubernetes.pod.network_received_data', title: 'Network received', unit: 'B/s', agg: 'avg' },
      ]
    case 'K8S_DEPLOYMENT':
    case 'K8S_STATEFULSET':
    case 'K8S_DAEMONSET':
      return [
        { key: 'dt.kubernetes.container.cpu_usage', title: 'CPU usage', unit: 'mCores', agg: 'sum' },
        { key: 'dt.kubernetes.container.memory_working_set', title: 'Memory working set', unit: 'B', agg: 'sum' },
        { key: 'dt.kubernetes.container.restarts', title: 'Container restarts', unit: '', agg: 'sum' },
      ]
    case 'K8S_NODE':
      return [
        { key: 'dt.kubernetes.container.cpu_usage', title: 'CPU used by containers', unit: 'mCores', agg: 'sum' },
        { key: 'dt.kubernetes.container.memory_working_set', title: 'Memory used by containers', unit: 'B', agg: 'sum' },
      ]
    case 'K8S_NAMESPACE':
      return [
        { key: 'dt.kubernetes.container.cpu_usage', title: 'CPU usage', unit: 'mCores', agg: 'sum' },
        { key: 'dt.kubernetes.container.memory_working_set', title: 'Memory working set', unit: 'B', agg: 'sum' },
        { key: 'dt.kubernetes.pods', title: 'Pods', unit: '', agg: 'sum' },
      ]
    case 'K8S_CLUSTER':
      return [
        { key: 'dt.kubernetes.pods', title: 'Pods', unit: '', agg: 'sum' },
        { key: 'dt.kubernetes.nodes', title: 'Nodes', unit: '', agg: 'sum' },
        { key: 'dt.kubernetes.workloads', title: 'Workloads', unit: '', agg: 'sum' },
      ]
    case 'FRONTEND':
      return [
        { key: 'dt.frontend.request.count', title: 'Requests', unit: '', agg: 'sum' },
        { key: 'dt.frontend.error.count', title: 'Errors', unit: '', agg: 'sum' },
        { key: 'dt.frontend.web.page.largest_contentful_paint', title: 'Largest contentful paint', unit: 'ms', agg: 'avg' },
      ]
    case 'DB_INSTANCE_POSTGRES':
      return [
        { key: 'postgres.activity.active', title: 'Active connections', unit: '', agg: 'avg' },
        { key: 'postgres.deadlocks.count', title: 'Deadlocks', unit: '', agg: 'sum' },
      ]
  }
  return []
}

export function vitalQuery(v: Vital, e: Entity, interval: string) {
  const f = v.filter ? v.filter(e) : eq(e)
  return `timeseries v = ${v.agg}(${v.key}), interval:${interval}, filter:{ ${f} }`
}

// ── home ────────────────────────────────────────────────────────────────────

export function changesQuery(limit = 200, extra = '') {
  return `fetch events
| filter event.kind == "SDLC_EVENT" or in(event.type, {"CUSTOM_DEPLOYMENT","CUSTOM_CONFIGURATION","CUSTOM_ANNOTATION","PROCESS_RESTART"}) or (event.type == "CUSTOM_INFO" and matchesPhrase(event.name, "deployment"))
${extra ? `| filter ${extra}\n` : ''}| fieldsAdd what = coalesce(cicd.deployment.name, event.name, task.name, event.type), env = coalesce(deployment.environment.name, deployment.environment), outcome = coalesce(task.outcome, event.status), rev = coalesce(vcs.ref.base.revision, cicd.deployment.id), source = coalesce(event.provider, dt.openpipeline.source), ns = k8s.namespace.name, workload = k8s.workload.name, cluster = k8s.cluster.name
| sort timestamp desc
| limit ${limit}`
}

export function vulnsQuery(lens: 'open' | 'muted' | 'all' = 'open', limit = 300) {
  const f = { open: '| filter status == "OPEN" and muted != "MUTED"', muted: '| filter muted == "MUTED"', all: '' }[lens]
  return `fetch security.events
| filter event.type == "VULNERABILITY_STATE_REPORT_EVENT" and event.level == "VULNERABILITY"
| sort timestamp asc
| summarize { title = takeLast(vulnerability.title), display_id = takeLast(vulnerability.display_id), level = takeLast(vulnerability.risk.level), score = takeLast(vulnerability.risk.score), status = takeLast(vulnerability.resolution.status), muted = takeLast(vulnerability.mute.status), tech = takeLast(vulnerability.technology), cve = takeLast(vulnerability.references.cve), exposure = takeLast(vulnerability.davis_assessment.exposure_status), exploit = takeLast(vulnerability.davis_assessment.exploit_status), data_assets = takeLast(vulnerability.davis_assessment.data_assets_status), fix = takeLast(vulnerability.is_fix_available), affected = takeLast(affected_entities.count), type = takeLast(vulnerability.type), stack = takeLast(vulnerability.stack), description = takeLast(vulnerability.description), remediation = takeLast(vulnerability.remediation.description), url = takeLast(vulnerability.url), first_seen = takeFirst(timestamp) }, by:{vulnerability.id}
${f}
| sort score desc
| limit ${limit}`
}
