// AI observability: OpenTelemetry GenAI semantic conventions on spans
// (gen_ai.operation.name = chat | execute_tool | invoke_agent) plus
// LLM-as-judge results ingested as gen_ai.evaluation.result bizevents.
// All shapes validated live.
import { q } from './dql'

export const GENAI = 'isNotNull(gen_ai.operation.name)'

export function aiKpiQuery() {
  return `fetch spans
| filter ${GENAI}
| summarize { chats = countIf(gen_ai.operation.name == "chat"), chat_fail = countIf(gen_ai.operation.name == "chat" and span.status_code == "error"), tools = countIf(gen_ai.operation.name == "execute_tool"), tool_fail = countIf(gen_ai.operation.name == "execute_tool" and span.status_code == "error"), agents = countIf(gen_ai.operation.name == "invoke_agent"), input = sum(gen_ai.usage.input_tokens), output = sum(gen_ai.usage.output_tokens), cache_read = sum(gen_ai.usage.cache_read.input_tokens), ttft_p50 = percentile(gen_ai.server.time_to_first_token, 50), ttft_p90 = percentile(gen_ai.server.time_to_first_token, 90) }`
}

export function callsByModelSeries(interval: string) {
  return `fetch spans
| filter gen_ai.operation.name == "chat"
| makeTimeseries calls = count(), by:{model = gen_ai.request.model}, interval:${interval}`
}

export function tokensSeries(interval: string) {
  return `fetch spans
| filter gen_ai.operation.name == "chat"
| makeTimeseries { input = sum(gen_ai.usage.input_tokens), cached = sum(gen_ai.usage.cache_read.input_tokens), output = sum(gen_ai.usage.output_tokens) }, interval:${interval}`
}

export function ttftSeries(interval: string) {
  return `fetch spans
| filter gen_ai.operation.name == "chat" and isNotNull(gen_ai.server.time_to_first_token)
| makeTimeseries { p50 = percentile(gen_ai.server.time_to_first_token, 50), p90 = percentile(gen_ai.server.time_to_first_token, 90) }, interval:${interval}`
}

export function modelsQuery() {
  return `fetch spans
| filter gen_ai.operation.name == "chat"
| summarize { calls = count(), failed = countIf(span.status_code == "error"), input = sum(gen_ai.usage.input_tokens), output = sum(gen_ai.usage.output_tokens), cache_read = sum(gen_ai.usage.cache_read.input_tokens), ttft_p50 = percentile(gen_ai.server.time_to_first_token, 50), ttft_p90 = percentile(gen_ai.server.time_to_first_token, 90), dur_p50 = percentile(duration, 50), dur_p90 = percentile(duration, 90) }, by:{model = gen_ai.request.model, provider = gen_ai.provider.name, model_id = dt.smartscape.gen_ai.request_model}
| sort calls desc
| limit 100`
}

export function agentsQuery() {
  return `fetch spans
| filter gen_ai.operation.name == "invoke_agent"
| summarize { runs = count(), failed = countIf(span.status_code == "error"), dur_p50 = percentile(duration, 50), dur_p90 = percentile(duration, 90), services = collectDistinct(coalesce(dt.service.name, service.name)), last = max(start_time) }, by:{agent = gen_ai.agent.name, agent_id = dt.smartscape.gen_ai.agent}
| sort runs desc
| limit 100`
}

export function toolsQuery() {
  return `fetch spans
| filter gen_ai.operation.name == "execute_tool"
| summarize { calls = count(), failed = countIf(span.status_code == "error"), dur_p50 = percentile(duration, 50), dur_p90 = percentile(duration, 90), services = collectDistinct(coalesce(dt.service.name, service.name)) }, by:{tool = gen_ai.tool.name}
| sort calls desc
| limit 200`
}

export function recentCallsQuery(filter?: string) {
  return `fetch spans
| filter gen_ai.operation.name == "chat"
${filter ? `| filter ${filter}\n` : ''}| fields start_time, duration, trace.id, span.id, span.status_code, model = gen_ai.request.model, model_id = dt.smartscape.gen_ai.request_model, provider = gen_ai.provider.name, service = coalesce(dt.service.name, service.name), service_id = dt.smartscape.service, input = gen_ai.usage.input_tokens, output = gen_ai.usage.output_tokens, cached = gen_ai.usage.cache_read.input_tokens, ttft = gen_ai.server.time_to_first_token, finish = gen_ai.response.finish_reasons, conversation = gen_ai.conversation.id
| sort start_time desc
| limit 300`
}

export function callDetailQuery(traceId: string, spanId: string) {
  return `fetch spans, from:now()-7d
| filter trace.id == toUid(${q(traceId)}) and span.id == toUid(${q(spanId)})
| limit 1`
}

export function evalSummaryQuery() {
  return `fetch bizevents
| filter event.type == "gen_ai.evaluation.result"
| summarize { n = count(), passed = countIf(gen_ai.evaluation.score.label == "pass"), avg = avg(gen_ai.evaluation.score.value), last = max(timestamp) }, by:{name = gen_ai.evaluation.name, method = gen_ai.evaluation.method}
| sort n desc`
}

export function evalsQuery() {
  return `fetch bizevents
| filter event.type == "gen_ai.evaluation.result"
| fields timestamp, name = gen_ai.evaluation.name, question = gen_ai.evaluation.input.question, score = gen_ai.evaluation.score.value, label = gen_ai.evaluation.score.label, explanation = gen_ai.evaluation.explanation, criteria = gen_ai.evaluation.criteria_results, answer = gen_ai.evaluation.input.answer, judge = gen_ai.request.model, method = gen_ai.evaluation.method, run = dt.eval.run_id
| sort timestamp desc
| limit 500`
}

export const fmtTokens = (n: number) => {
  if (!Number.isFinite(n)) return '—'
  if (n >= 1e9) return `${(n / 1e9).toFixed(2)}B`
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}K`
  return String(Math.round(n))
}
