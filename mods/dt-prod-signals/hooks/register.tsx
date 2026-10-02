import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { FileSignal } from '../types'
import {
  buildDql,
  bytes,
  compact,
  duration,
  forModel,
  headline,
  parseEnvelope,
  repoKey,
  targetFor,
  totals,
  worst,
} from './signals'

const PLUGIN = 'dt-prod-signals'
const PANE = 'prod-signals'
const FRESH_MS = 10 * 60 * 1000
const KEEP = 12

const files = atom({ plugin: 'dt-prod-signals', key: 'files' } as const, {})
const recent = atom({ plugin: 'dt-prod-signals', key: 'recent' } as const, [])
const isHidden = atom({ plugin: 'dt-prod-signals', key: 'isHidden' } as const, false)
const openDql = atom({ plugin: 'dt-prod-signals', key: 'openDql' } as const, null)
const repoAtom = atom({ plugin: 'dt-prod-signals', key: 'repo' } as const, null)

const LOOKUP_TOOL = 'mcp__dt-prod-signals__lookup'
const SOURCE_TOOLS = new Set(['Read', 'Edit', 'Write', 'MultiEdit'])

type Settings = { timeframe: string; context: string; matchRepo: boolean; autoLookup: boolean }

// Module state: set by register and session.start, fresh on every reload.
let settings: Settings = { timeframe: '2h', context: '', matchRepo: true, autoLookup: true }
let root = ''
let repo: string | null = null
const inflight = new Map<string, Promise<FileSignal | null>>()

/** Repo-relative path for an absolute or relative one inside the project. */
function relative(path: string): string | null {
  const p = path.replace(/\\/g, '/')
  if (!p.startsWith('/')) return p.replace(/^\.\//, '')
  if (root !== '' && p.startsWith(`${root}/`)) return p.slice(root.length + 1)

  return null
}

/** Queries Dynatrace for one file, unless a fresh answer is already held. */
async function lookup($: EngineInterface, rel: string, force = false): Promise<FileSignal | null> {
  const target = targetFor(rel)
  if (target === null) return null
  const { timeframe, context, matchRepo } = settings

  await update($, recent, list => [rel, ...list.filter(r => r !== rel)].slice(0, KEEP))
  const held = (await read($, files))[rel]
  const now = await $.clock.now()
  const isFresh =
    held !== undefined &&
    held.status !== 'error' &&
    held.status !== 'loading' &&
    held.timeframe === timeframe &&
    now - held.fetchedAt < FRESH_MS
  if (isFresh && !force) return held

  const running = inflight.get(rel)
  if (running) return running

  const dql = buildDql(target, matchRepo ? repo : null, timeframe)
  const base: FileSignal = {
    rel,
    status: 'loading',
    matchedBy: target.matchedBy + (matchRepo && repo ? `, in repo ${repo}` : ''),
    timeframe,
    dql,
    rows: [],
    scannedBytes: null,
    error: null,
    fetchedAt: now,
  }
  const job = runQuery($, base, context)
  inflight.set(rel, job)
  try {
    return await job
  } finally {
    inflight.delete(rel)
  }
}

/** Runs the file's DQL through dtctl and stores what came back. */
async function runQuery($: EngineInterface, base: FileSignal, context: string): Promise<FileSignal> {
  await update($, files, all => prune({ ...all, [base.rel]: base }))

  const argv = ['dtctl', 'query', base.dql, '--agent', '-o', 'json', '-M=minimal', '--no-progress']
  if (context !== '') argv.push('--context', context)

  let done: FileSignal
  try {
    const ran = await $.process.run(argv, { cwd: root || undefined, timeoutMs: 90_000 })
    const parsed = parseEnvelope(ran.stdout, ran.stderr, ran.exitCode)
    done = parsed.ok
      ? { ...base, status: parsed.rows.length === 0 ? 'empty' : 'ok', rows: parsed.rows, scannedBytes: parsed.scannedBytes }
      : { ...base, status: 'error', error: parsed.error }
  } catch (err) {
    done = { ...base, status: 'error', error: err instanceof Error ? err.message : String(err) }
  }
  done = { ...done, fetchedAt: await $.clock.now() }
  await update($, files, all => prune({ ...all, [base.rel]: done }))

  return done
}

/** Hands the worst failure to Claude as a new prompt. */
function askClaude($: EngineInterface, s: FileSignal) {
  const bad = worst(s.rows)
  const focus = bad
    ? ` The worst operation is "${bad.op}" in ${bad.service}${bad.message ? ` failing with "${bad.message}"` : ''}.`
    : ''

  return $.prompt.submit({
    text: `Dynatrace shows production errors for ${s.rel} (${headline(s)}).${focus} Investigate with dtctl (the DQL below found them), relate the failures to the code in ${s.rel}, and propose a fix.\n\n${s.dql}`,
  })
}

/** Opens the detail pane. */
function openPane($: EngineInterface) {
  return $.ui.open({ id: PANE, title: 'Prod signals · Dynatrace' })
}

export const register: Register = (on, options) => {
  settings = {
    timeframe: String(options.timeframe ?? '2h'),
    context: String(options.context ?? ''),
    matchRepo: options.matchRepo !== false,
    autoLookup: options.autoLookup !== false,
  }
  const { timeframe, context, matchRepo } = settings

  on('session.start', async ($, e, next) => {
    root = await $.session.root()

    try {
      const remote = await $.process.run(['git', 'remote', 'get-url', 'origin'], { cwd: root, timeoutMs: 5_000 })
      repo = remote.exitCode === 0 ? repoKey(remote.stdout) : null
    } catch {
      repo = null
    }
    await update($, repoAtom, () => repo)

    await $.command.register({
      name: 'prod-signals',
      description: 'Show Dynatrace production signals for touched files (or for a given file)',
      argumentHint: '[file]',
    })
    await $.tool.register({
      name: 'lookup',
      description:
        'Dynatrace production signals for one source file of this repository: matching spans, error and exception counts, ' +
        'p50/p95 latency and the worst operations (span name × service) with a sample error message, over the last ' +
        `${timeframe}. Spans are matched by code location (Go package instrumentation scope, code.filepath, exception ` +
        'stack frames) and by the repo\'s vcs.repository.url.full. Use it before changing a code path to learn how it ' +
        'behaves in production, or when investigating an error. Read-only; one DQL query via dtctl.',
      inputSchema: {
        type: 'object',
        properties: {
          file_path: { type: 'string', description: 'Absolute or repo-relative path of a source file' },
          refresh: { type: 'boolean', description: 'Query again even when a recent answer is held' },
        },
        required: ['file_path'],
      },
    })

    return next(e)
  })

  on('tool.call', async ($, e, next) => {
    // The model's own lookup tool. Matched by name here, not by a matcher:
    // the engine's typings list only the MCP tools connected at the last
    // reload, which this tool may not yet be among.
    if ((e.tool as string) === LOOKUP_TOOL) {
      const input = e as unknown as { file_path?: unknown; refresh?: unknown }
      const rel = typeof input.file_path === 'string' ? relative(input.file_path) : null
      if (rel === null) {
        return { result: `${String(input.file_path)} is not a path inside ${root || 'this project'}.` }
      }
      const signal = await lookup($, rel, input.refresh === true)
      if (signal === null) {
        return { result: `${rel} is not production source this tool can match (test, vendored or non-source file).` }
      }

      return { result: forModel(signal) }
    }

    // Claude reading or editing a source file kicks off a lookup in the background.
    const ran = await next(e)
    if (!settings.autoLookup || !SOURCE_TOOLS.has(e.tool) || 'deny' in ran || ran.isError === true) return ran

    const path = (e as unknown as { file_path?: unknown }).file_path
    const rel = typeof path === 'string' ? relative(path) : null
    if (rel !== null && targetFor(rel) !== null) {
      $.clock.after(0, () => void lookup($, rel))
    }

    return ran
  })

  on('command.run', { command: 'prod-signals' }, async ($, e) => {
    const arg = e.args.trim()
    if (arg !== '') {
      const rel = relative(arg)
      if (rel === null || targetFor(rel) === null) {
        return { text: `${arg}: not production source inside ${root}.` }
      }
      $.clock.after(0, () => void lookup($, rel, true))
    }
    await update($, isHidden, () => false)
    await openPane($)

    return { text: arg === '' ? 'Prod signals pane opened.' : `Querying Dynatrace for ${relative(arg)}…` }
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    if (e.props.hasSurvey || (await read($, isHidden))) return next(e)
    const rel = (await read($, recent))[0]
    const s = rel === undefined ? undefined : (await read($, files))[rel]
    if (s === undefined) return next(e)

    const { Box, Text, Button } = $.ui.resolve(e)
    const t = totals(s.rows)
    const bad = s.status === 'ok' ? worst(s.rows) : null
    const tone = s.status === 'error' ? 'yellow' : t.errors > 0 ? 'red' : t.exceptions > 0 ? 'yellow' : 'green'

    return (
      <Box flexDirection="column">
        <Box>
          <Text color={s.status === 'ok' ? tone : undefined} dimColor={s.status !== 'ok'}>
            {s.status === 'loading' ? '◇' : '◆'} prod{' '}
          </Text>
          <Text bold>{s.rel}</Text>
          <Text dimColor wrap="truncate-end">
            {' · '}
            {headline(s)}
          </Text>
        </Box>
        {bad && (
          <Text dimColor wrap="truncate-end">
            {'  worst: '}
            {bad.op} · {bad.service} — {compact(bad.errors)} err
            {bad.message ? ` · ${bad.message}` : ''}
          </Text>
        )}
        <Box>
          <Button key="details" label="Details" onPress={() => openPane($)} />
          {bad && <Button key="ask" label="Ask Claude" onPress={() => askClaude($, s)} />}
          <Button key="hide" label="Hide" onPress={() => update($, isHidden, () => true)} />
        </Box>
      </Box>
    )
  })

  on('ui.render', { component: 'Pane', requestId: PANE }, async ($, e) => {
    const { Box, Text, Button, Code } = $.ui.resolve(e)
    const all = await read($, files)
    const list = (await read($, recent)).map(rel => all[rel]).filter((s): s is FileSignal => s !== undefined)
    const revealed = await read($, openDql)
    const repoName = await read($, repoAtom)

    return (
      <Box flexDirection="column">
        <Text dimColor wrap="truncate-end">
          {repoName ?? 'no git remote'} · {context || 'current dtctl context'} · last {timeframe}
          {matchRepo ? '' : ' · repo match off'}
        </Text>
        {list.length === 0 && (
          <Text dimColor>Nothing yet: signals appear when Claude reads or edits a source file, or run /prod-signals &lt;file&gt;.</Text>
        )}
        {list.slice(0, 6).map(s => {
          const t = totals(s.rows)
          const bad = worst(s.rows)

          return (
            <Box key={`file:${s.rel}`} flexDirection="column" marginTop={1}>
              <Text bold wrap="truncate-end">
                {s.rel}
              </Text>
              <Text color={s.status === 'ok' && t.errors > 0 ? 'red' : undefined} dimColor={s.status !== 'ok'} wrap="truncate-end">
                {headline(s)}
              </Text>
              <Text dimColor wrap="truncate-end">
                matched by {s.matchedBy}
                {s.scannedBytes !== null ? ` · scanned ${bytes(s.scannedBytes)}` : ''}
              </Text>
              {s.rows.slice(0, 5).map(r => (
                <Text key={`row:${s.rel}:${r.op}:${r.service}`} wrap="truncate-end">
                  {'  '}
                  {compact(r.errors).padStart(5)} err {compact(r.exceptions).padStart(5)} exc  p95 {duration(r.p95).padStart(6)}{' '}
                  {compact(r.spans).padStart(5)} spans  {r.op} · {r.service}
                </Text>
              ))}
              {bad?.message && (
                <Text dimColor wrap="truncate-end">
                  {'  '}“{bad.message}”
                </Text>
              )}
              <Box>
                <Button key={`refresh:${s.rel}`} label="Refresh" onPress={() => void lookup($, s.rel, true)} />
                <Button
                  key={`dql:${s.rel}`}
                  label={revealed === s.rel ? 'Hide DQL' : 'DQL'}
                  onPress={() => update($, openDql, cur => (cur === s.rel ? null : s.rel))}
                />
                {bad && <Button key={`ask:${s.rel}`} label="Ask Claude" onPress={() => askClaude($, s)} />}
              </Box>
              {revealed === s.rel && <Code source={s.dql} language="sql" />}
            </Box>
          )
        })}
      </Box>
    )
  })
}

/** Keeps the state bounded: the newest KEEP files. */
function prune(all: Record<string, FileSignal>): Record<string, FileSignal> {
  const kept = Object.values(all)
    .sort((a, b) => b.fetchedAt - a.fetchedAt)
    .slice(0, KEEP)

  return Object.fromEntries(kept.map(s => [s.rel, s]))
}
