// Deep links into the Dynatrace web UI via intent URLs (same shapes as
// dynatui/internal/tui/links.go): {env}/ui/intent/{app}/{intent}#{json}

function intent(env: string, app: string, name: string, payload: Record<string, unknown>) {
  if (!env) return ''
  return `${env.replace(/\/$/, '')}/ui/intent/${app}/${name}#${encodeURIComponent(JSON.stringify(payload))}`
}

export const dtLinks = {
  problem: (env: string, eventId: string) => intent(env, 'dynatrace.davis.problems', 'view-problem', { 'event.id': eventId, 'event.kind': 'DAVIS_PROBLEM' }),
  trace: (env: string, traceId: string) => intent(env, 'dynatrace.distributedtracing', 'view-trace', { 'trace.id': traceId }),
  notebook: (env: string, dql: string) => intent(env, 'dynatrace.notebooks', 'view-query', { 'dt.query': dql }),
  topology: (env: string, id: string) => intent(env, 'dynatrace.smartscape', 'view_topology_in_context', { id }),
  entity: (env: string, id: string, type: string) => {
    const lower = type.toLowerCase()
    if (type.startsWith('K8S_') || type === 'CONTAINER') return intent(env, 'dynatrace.kubernetes', `view-entity-dt.smartscape.${lower}`, { nodeId: id })
    if (type === 'SERVICE') return intent(env, 'dynatrace.services', 'view-entity-dt.smartscape.service', { nodeId: id })
    if (type === 'DB_INSTANCE_POSTGRES') return intent(env, 'dynatrace.database.overview', 'view-instance-details', { 'dt.smartscape.db_instance_id': id })
    return intent(env, 'dynatrace.smartscape', 'view_topology_in_context', { id })
  },
  document: (env: string, type: string, id: string) => {
    if (!env) return ''
    const base = env.replace(/\/$/, '')
    return type === 'notebook' ? `${base}/ui/apps/dynatrace.notebooks/notebook/${id}` : `${base}/ui/apps/dynatrace.dashboards/dashboard/${id}`
  },
}

export const entityHref = (id: string, name?: string) => `/e/${encodeURIComponent(id)}${name ? `?n=${encodeURIComponent(name)}` : ''}`
export const problemHref = (displayId: string) => `/problems/${encodeURIComponent(displayId)}`
export const traceHref = (traceId: string) => `/traces/${encodeURIComponent(traceId)}`
