import { expect, mock, test } from 'claude-code/testing'
import type { On } from 'claude-code'

const ROOT = '/work/shop'
const FILE = `${ROOT}/internal/shared/dql/client.go`

const ENVELOPE = JSON.stringify({
  ok: true,
  result: {
    kind: 'records',
    records: [
      { 'span.name': 'dql.execute', 'dt.service.name': 'shop (prod)', spans: '9000', errors: '120', exceptions: '40', p50: '1000000', p95: '294000000', msg: 'request failed with status 403' },
    ],
  },
  metadata: { scannedBytes: 2999673523 },
})

/** The world beneath the mod: a repo, dtctl, and the engine calls it makes. */
function world(on: On) {
  const runs: string[][] = []
  const prompts: string[] = []
  const clock = mock.clock(on, { now: 1_000_000 })
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  on('session.root', () => ({ value: ROOT }))
  on('process.run', ($, e) => {
    runs.push([...e.argv])
    const stdout = e.argv[0] === 'git' ? 'git@github.com:Acme/Shop.git\n' : ENVELOPE
    return { value: { exitCode: 0, stdout, stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }
  })
  on('command.register', ($, e) => ({ value: { command: e.name } }))
  on('tool.register', ($, e) => ({ value: { tool: `mcp__dt-prod-signals__${e.name}` } }))
  on('ui.open', () => ({ value: { isPlaced: true } }))
  on('prompt.submit', ($, e) => {
    prompts.push(e.text)
    return { text: e.text }
  })
  on('tool.call', { tool: 'Read' }, () => ({ result: { type: 'text', file: { filePath: FILE, content: '', numLines: 0, startLine: 1, totalLines: 0 } } }) as never)

  return { runs, prompts, clock }
}

const BAND = { component: 'AbovePrompt', props: { hasSurvey: false, isWorking: false, maxRows: 6, bodyColumns: 120 } } as const

test('reading a source file shows its production signals in the band, on terminal and desktop', async ($, on) => {
  const w = world(on)
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true } as never)

  await $.tool.call({ tool: 'Read', file_path: FILE })
  await w.clock.settle()

  const dtctl = w.runs.find(argv => argv[0] === 'dtctl')
  expect(dtctl).toBeDefined()
  expect(dtctl?.[2]).toContain('endsWith(lower(vcs.repository.url.full), "github.com/acme/shop")')
  expect(dtctl?.[2]).toContain('endsWith(otel.scope.name, "/internal/shared/dql")')

  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await $.ui.mount({ plugin: 'dt-prod-signals', surface, ...BAND } as never)
    expect(await ui.find({ text: 'internal/shared/dql/client.go' })).toBeDefined()
    expect(await ui.find({ text: /120 errors/ })).toBeDefined()
    expect(await ui.find({ text: /request failed with status 403/ })).toBeDefined()
    expect(await ui.find({ key: 'ask' })).toBeDefined()
    await ui.unmount()
  }
})

test('Ask Claude hands the worst failure to the model', async ($, on) => {
  const w = world(on)
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true } as never)
  await $.tool.call({ tool: 'Read', file_path: FILE })
  await w.clock.settle()

  const ui = await $.ui.mount({ plugin: 'dt-prod-signals', surface: 'terminal', ...BAND } as never)
  await ui.press({ key: 'ask' })
  expect(w.prompts).toHaveLength(1)
  expect(w.prompts[0]).toContain('dql.execute')
  expect(w.prompts[0]).toContain('fetch spans')
})

test('the model can call the lookup tool and reads the signals; one query serves both', async ($, on) => {
  const w = world(on)
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true } as never)

  const first = await $.tool.call({ tool: 'mcp__dt-prod-signals__lookup', file_path: 'internal/shared/dql/client.go' } as never)
  const said = String((first as { result?: unknown }).result)
  expect(said).toContain('120 errors')
  expect(said).toContain('dql.execute · shop (prod)')

  await $.tool.call({ tool: 'mcp__dt-prod-signals__lookup', file_path: FILE } as never)
  expect(w.runs.filter(argv => argv[0] === 'dtctl')).toHaveLength(1)
})

test('test files and paths outside the project are never queried', async ($, on) => {
  const w = world(on)
  await $.session.start({ cwd: ROOT, surface: 'terminal', isInteractive: true } as never)
  await $.tool.call({ tool: 'Read', file_path: `${ROOT}/internal/shared/dql/client_test.go` })
  await $.tool.call({ tool: 'Read', file_path: '/etc/hosts.go' })
  await w.clock.settle()
  expect(w.runs.filter(argv => argv[0] === 'dtctl')).toHaveLength(0)
})
