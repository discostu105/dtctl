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

/** Probe: does the app emit OTel GenAI client metrics? */
export const GENAI_METRIC_PROBE = `metrics
| filter metric.key == "gen_ai.client.operation.duration"
| limit 1`

/** Calls by model from gen_ai.client.operation.duration: scale-free, full range. */
export function callsByModelMetricSeries(interval: string) {
  return `timeseries calls = count(gen_ai.client.operation.duration), by:{gen_ai.response.model}, interval:${interval}
| fieldsRename model = gen_ai.response.model`
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

/** One span; `at` (its start time) keeps the scan to a narrow window instead of 7 days. */
export function callDetailQuery(traceId: string, spanId: string, at?: unknown) {
  const win = at ? `from: toTimestamp(${q(String(at))}) - 15m, to: toTimestamp(${q(String(at))}) + 60m` : 'from:now()-7d'
  return `fetch spans, ${win}
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

// ── conversations (the unit people reason about) ─────────────────────────────

export interface ConversationFilter {
  search?: string
  agent?: string | null
  errorsOnly?: boolean
}

const CONV_SPANS = `isNotNull(gen_ai.conversation.id) and in(gen_ai.operation.name, {"chat", "execute_tool", "invoke_agent"})`

/**
 * One row per conversation with its opening prompt and final answer
 * (trimmed in DQL: message payloads are large). Search selects
 * conversations first (join), then aggregates ALL their spans, so step
 * counts and tokens stay complete.
 */
export function conversationsQuery(f: ConversationFilter = {}) {
  const t = f.search?.trim()
  const search = t
    ? `| join [fetch spans
  | filter isNotNull(gen_ai.conversation.id)
  | filter contains(gen_ai.input.messages, ${q(t)}, caseSensitive:false) or contains(gen_ai.output.messages, ${q(t)}, caseSensitive:false) or contains(span.name, ${q(t)}, caseSensitive:false) or gen_ai.conversation.id == ${q(t)}${/^[0-9a-f]{32}$/i.test(t) ? ` or trace.id == toUid(${q(t.toLowerCase())})` : ''}
  | summarize hits = count(), by:{c = gen_ai.conversation.id}], on:{left[gen_ai.conversation.id] == right[c]}, kind:inner, fields:{hits}\n`
    : ''
  return `fetch spans
| filter ${CONV_SPANS}
${search}| sort start_time asc
| summarize { start = min(start_time), end = max(end_time), llm = countIf(gen_ai.operation.name == "chat"), tools = countIf(gen_ai.operation.name == "execute_tool"), tool_fail = countIf(gen_ai.operation.name == "execute_tool" and span.status_code == "error"), errors = countIf(span.status_code == "error"), input = sum(gen_ai.usage.input_tokens), output = sum(gen_ai.usage.output_tokens), cached = sum(gen_ai.usage.cache_read.input_tokens), agents = arrayRemoveNulls(collectDistinct(gen_ai.agent.name)), models = arrayRemoveNulls(collectDistinct(gen_ai.request.model)), service = takeFirst(coalesce(dt.service.name, service.name)), service_id = takeFirst(dt.smartscape.service), trace = takeFirst(trace.id), first_in = takeFirst(gen_ai.input.messages), answer = takeLast(if(contains(gen_ai.output.messages, "\\"type\\": \\"text\\""), gen_ai.output.messages))${t ? ', hits = takeFirst(hits)' : ''} }, by:{conversation = gen_ai.conversation.id}
| filter llm > 0${f.errorsOnly ? ' and errors > 0' : ''}${f.agent ? `\n| filter iAny(agents[] == ${q(f.agent)})` : ''}
| fieldsAdd prompt = substring(first_in, from: 0, to: 3000), answer = substring(answer, from: if(stringLength(answer) > 1200, stringLength(answer) - 1200, else: 0))
| fieldsRemove first_in
| sort start desc
| limit 300`
}

/** Narrow window around a known start time; without one, the last 7 days. */
const convWindow = (at?: string | null) => (at ? `from: toTimestamp(${q(at)}) - 30m, to: toTimestamp(${q(at)}) + 12h` : 'from:now()-7d')

/** Every step of one conversation; message payloads only for outputs (small). */
export function conversationStepsQuery(id: string, at?: string | null) {
  return `fetch spans, ${convWindow(at)}
| filter gen_ai.conversation.id == ${q(id)}
| fields start_time, end_time, duration, trace.id, span.id, span.parent_id, span.name, span.status_code, op = gen_ai.operation.name, model = gen_ai.request.model, agent = gen_ai.agent.name, tool = gen_ai.tool.name, input = gen_ai.usage.input_tokens, output = gen_ai.usage.output_tokens, cached = gen_ai.usage.cache_read.input_tokens, ttft = gen_ai.server.time_to_first_token, finish = gen_ai.response.finish_reasons, out = gen_ai.output.messages, service = coalesce(dt.service.name, service.name), service_id = dt.smartscape.service
| sort start_time asc
| limit 3000`
}

/** The opening prompt: the first LLM call's input messages. */
export function conversationPromptQuery(id: string, at?: string | null) {
  return `fetch spans, ${convWindow(at)}
| filter gen_ai.conversation.id == ${q(id)} and gen_ai.operation.name == "chat"
| sort start_time asc
| limit 1
| fields gen_ai.input.messages`
}

export function toolExecutionsQuery(tool: string, failedOnly: boolean) {
  return `fetch spans
| filter gen_ai.operation.name == "execute_tool" and gen_ai.tool.name == ${q(tool)}${failedOnly ? ' and span.status_code == "error"' : ''}
| fields start_time, duration, span.name, span.status_code, service = coalesce(dt.service.name, service.name), conversation = gen_ai.conversation.id, trace.id, span.id
| sort start_time desc
| limit 300`
}

// ── evaluations: where they come from, and how they trend ───────────────────

export function evalSourceQuery() {
  return `fetch bizevents
| filter event.type == "gen_ai.evaluation.result"
| summarize { n = count(), provider = takeFirst(event.provider), client = takeFirst(gen_ai.eval.client), pipeline = takeFirst(dt.openpipeline.pipelines), source = takeFirst(dt.openpipeline.source), first = min(timestamp), last = max(timestamp) }`
}

export function evalsByQuestionQuery() {
  return `fetch bizevents
| filter event.type == "gen_ai.evaluation.result"
| sort timestamp asc
| summarize { n = count(), passed = countIf(gen_ai.evaluation.score.label == "pass"), avg = avg(gen_ai.evaluation.score.value), last = max(timestamp), last_label = takeLast(gen_ai.evaluation.score.label), scores = collectArray(gen_ai.evaluation.score.value) }, by:{question = gen_ai.evaluation.input.question}
| fieldsAdd rate = 100.0 * passed / n
| sort rate asc, n desc
| limit 500`
}

export function evalRunsQuery() {
  return `fetch bizevents
| filter event.type == "gen_ai.evaluation.result"
| summarize { start = min(timestamp), end = max(timestamp), n = count(), passed = countIf(gen_ai.evaluation.score.label == "pass"), avg = avg(gen_ai.evaluation.score.value), judge = takeFirst(gen_ai.request.model) }, by:{run = dt.eval.run_id}
| fieldsAdd rate = 100.0 * passed / n
| sort start desc
| limit 200`
}

// ── message helpers ─────────────────────────────────────────────────────────

/** Pull text contents out of (possibly truncated) message JSON. */
export function textParts(raw: unknown): string[] {
  if (typeof raw !== 'string') return []
  const out: string[] = []
  const re = /"content":\s*"((?:[^"\\]|\\.)*)"/g
  let m: RegExpExecArray | null
  while ((m = re.exec(raw))) {
    try {
      out.push(JSON.parse(`"${m[1]}"`))
    } catch {
      out.push(m[1])
    }
  }
  return out
}

/** Readable prompt: drop harness markers ([sent: …], [BEGIN UNTRUSTED…], tags). */
export function cleanPrompt(s: string) {
  return s
    .replace(/\[sent:[^\]]*\]/g, '')
    .replace(/\[(BEGIN|END) UNTRUSTED[^\]]*\]/g, '')
    .replace(/<\/?user_content>/g, '')
    .replace(/\[stripped:[^\]]*\]/g, '')
    .replace(/\s+/g, ' ')
    .trim()
}

/**
 * The opening user message from the (head-trimmed) first input: the last
 * user turn's text, read tolerantly even when the trim cut it off.
 */
export function userPrompt(raw: unknown) {
  if (typeof raw !== 'string') return ''
  const at = raw.lastIndexOf('"role": "user"')
  if (at >= 0) {
    const c = raw.indexOf('"content": "', at)
    if (c >= 0) {
      let i = c + 12
      let out = ''
      for (; i < raw.length; i++) {
        const ch = raw[i]
        if (ch === '\\' && i + 1 < raw.length) {
          const nx = raw[++i]
          out += nx === 'n' ? '\n' : nx === 't' ? ' ' : nx
        } else if (ch === '"') break
        else out += ch
      }
      const t = cleanPrompt(out)
      if (t) return t
    }
  }
  return lastText(raw)
}

export function lastText(raw: unknown) {
  const t = textParts(raw).map(cleanPrompt).filter(Boolean)
  if (t.length) return t[t.length - 1]
  // The trim may have cut off the opening "content": " of a long text part:
  // fall back to the raw tail, unescaped as well as we can.
  if (typeof raw !== 'string') return ''
  const tail = cleanPrompt(
    raw
      .replace(/"\s*\}\s*\]\s*\}\s*\]\s*$/, '')
      .replace(/\\n/g, ' ')
      .replace(/\\"/g, '"'),
  )
  // A tail-trimmed text starts mid-word: cut to the next word boundary.
  return raw.startsWith('[') ? tail : '…' + tail.replace(/^\S*\s+/, '')
}

/**
 * What a conversation is about, in a few words. Agent harnesses often send a
 * generic instruction followed by a JSON payload ("Investigate the following
 * task. {"task": {"title": …}}"): then the payload's human field (title,
 * question, …) is the headline and the instruction is only context.
 */
export function promptHeadline(prompt: string): { headline: string; lead?: string } {
  const start = prompt.indexOf('{')
  if (start < 0) return { headline: prompt }
  const json = firstJsonObject(prompt, start)
  if (!json) return { headline: prompt }
  let v: any
  try {
    v = JSON.parse(json)
  } catch {
    return { headline: prompt }
  }
  const pick = (o: any): string | undefined => {
    if (!o || typeof o !== 'object') return undefined
    for (const k of ['title', 'question', 'query', 'prompt', 'name', 'summary', 'description']) if (typeof o[k] === 'string' && o[k].trim()) return o[k].trim()
    return undefined
  }
  const inner = v.task ?? v.request ?? v.input ?? v
  const title = pick(inner) ?? pick(v)
  if (!title) return { headline: prompt }
  const kind = typeof inner?.type === 'string' ? inner.type : undefined
  const lead = prompt.slice(0, start).trim()
  return { headline: kind && !title.includes(kind) ? `${title} · ${kind}` : title, lead: lead || undefined }
}

function firstJsonObject(s: string, start: number): string | null {
  let depth = 0
  let inStr = false
  for (let i = start; i < s.length; i++) {
    const c = s[i]
    if (inStr) {
      if (c === '\\') i++
      else if (c === '"') inStr = false
    } else if (c === '"') inStr = true
    else if (c === '{') depth++
    else if (c === '}' && --depth === 0) return s.slice(start, i + 1)
  }
  return null
}
