import { expect, test, type Page } from '@playwright/test'
import { mockApi } from './mock'

// Every route renders its header without console errors or uncaught
// exceptions, on empty data — the state a new or quiet tenant shows. List pages
// show a PageHeader h1; detail pages, whose ID matches nothing, their
// not-found state; Logs has its own toolbar layout (UI_GUIDELINES.md).

interface Case {
  path: string
  /** document.title before " · dtctl web" */
  title: string
  /** h1 text, or (for detail pages and Logs) text that must be visible */
  h1?: string | RegExp
  text?: string
}

const ROUTES: Case[] = [
  { path: '/', title: 'Pulse', h1: /^(Up late|Good (morning|afternoon|evening)), Test\.$/ },
  { path: '/problems', title: 'Problems', h1: 'Problems' },
  { path: '/problems/P-00000001', title: 'P-00000001', text: 'Problem P-00000001 not found' },
  { path: '/services', title: 'Services', h1: 'Services' },
  { path: '/k8s', title: 'Kubernetes', h1: 'Kubernetes' },
  { path: '/k8s?view=pods', title: 'Kubernetes', h1: 'Kubernetes' },
  { path: '/k8s?view=nodes', title: 'Kubernetes', h1: 'Kubernetes' },
  { path: '/k8s?view=namespaces', title: 'Kubernetes', h1: 'Kubernetes' },
  { path: '/hosts', title: 'Hosts', h1: 'Hosts' },
  { path: '/logs', title: 'Logs', text: 'Errors only' },
  { path: '/traces', title: 'Traces', h1: 'Traces' },
  { path: '/traces/0123456789abcdef0123456789abcdef', title: 'Trace', text: 'Trace not found' },
  { path: '/rum', title: 'Experience', h1: 'Experience' },
  { path: '/rum/sessions/00000000-0000-0000-0000-000000000001', title: 'Session', text: 'Session not found' },
  { path: '/ai', title: 'AI', h1: 'AI' },
  { path: '/ai/conversations/conv-1', title: 'Conversation', text: 'Conversation not found' },
  { path: '/changes', title: 'Changes', h1: 'Changes' },
  { path: '/security', title: 'Security', h1: 'Security' },
  { path: '/query', title: 'Query', h1: 'Query' },
  { path: '/smartscape', title: 'Smartscape', h1: 'Smartscape' },
  { path: '/smartscape?type=AWS_EC2_INSTANCE', title: 'AWS EC2 instance' },
  { path: '/e/HOST-0000000000000001', title: 'HOST-0000000000000001' },
  { path: '/docs', title: 'Documents', h1: 'Documents' },
]

/** Collect console errors and page errors for the whole test. */
function watchErrors(page: Page) {
  const errors: string[] = []
  page.on('console', (m) => {
    if (m.type() !== 'error') return
    errors.push(`console: ${m.text()}`)
  })
  page.on('pageerror', (e) => errors.push(`pageerror: ${e.message}`))
  return errors
}

for (const c of ROUTES) {
  test(`renders ${c.path}`, async ({ page }) => {
    const errors = watchErrors(page)
    await mockApi(page)
    await page.goto(c.path)
    const main = page.locator('main')
    if (c.h1) await expect(main.getByRole('heading', { level: 1, name: c.h1, exact: typeof c.h1 === 'string' })).toBeVisible()
    else if (c.text) await expect(main.getByText(c.text, { exact: true })).toBeVisible()
    else await expect(main.locator('h1').first()).toBeVisible()
    await expect(page).toHaveTitle(`${c.title} · dtctl web`)
    // let queued queries resolve and render their (empty) results
    await page.waitForLoadState('networkidle')
    expect(errors).toEqual([])
  })
}

// DataTable rows link through a stretched <a>, so cells can hold links of their own.
test('hosts table has no nested links', async ({ page }) => {
  const errors = watchErrors(page)
  await mockApi(page)
  await page.goto('/hosts')
  await expect(page.getByText('host-1.example.invalid')).toBeVisible()
  expect(await page.locator('main a a').count()).toBe(0)
  expect(errors).toEqual([])
})

test('filter popup: open with f, browse a tag, pick a value, close with Esc', async ({ page }) => {
  const errors = watchErrors(page)
  const queries = await mockApi(page)
  await page.goto('/hosts')
  await expect(page.getByText('host-1.example.invalid')).toBeVisible()

  await page.locator('main').click({ position: { x: 5, y: 5 } })
  await page.keyboard.press('f')
  const dialog = page.getByRole('dialog')
  await expect(dialog).toBeVisible()

  // discovered AWS tag, searched by name
  await page.keyboard.type('team')
  await expect(dialog.getByText('team-a')).toBeVisible()

  // Tab to the values pane, Enter picks the first value
  await page.keyboard.press('Tab')
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/[?&]a=tags%3Aaws%5Bteam%5D%3Dteam-a/)
  await expect(dialog).toBeHidden()

  // the list query now carries the tag filter right after its source line
  await expect.poll(() => queries.some((q) => q.startsWith('smartscapeNodes HOST\n| filter `tags:aws`[`team`] == "team-a"'))).toBe(true)

  // reopen and dismiss with Escape
  await page.keyboard.press('f')
  await expect(dialog).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
  expect(errors).toEqual([])
})

for (const n of [6, 1000]) {
  const capped = n >= 1000
  test(`filter popup: a curated facet ${capped ? 'filters on the server when the list is capped' : 'filters the loaded rows'}`, async ({ page }) => {
    const errors = watchErrors(page)
    const queries = await mockApi(page, { hosts: n })
    await page.goto('/hosts')
    await expect(page.getByText('host-1.example.invalid')).toBeVisible()
    if (capped) await expect(page.locator('main').getByText('capped', { exact: true })).toBeVisible()

    await page.locator('main').click({ position: { x: 5, y: 5 } })
    await page.keyboard.press('f')
    const dialog = page.getByRole('dialog')
    await page.keyboard.type('Instance type')
    await expect(dialog.getByRole('option', { name: /^t3\.large/ })).toBeVisible()
    await page.keyboard.press('Tab')
    await page.keyboard.press('Enter')
    await expect(dialog).toBeHidden()

    if (capped) {
      // every host's values, and the filter narrows the query rather than the 1000 loaded rows
      await expect(page).toHaveURL(/[?&]a=host\.type%3D/)
      await expect.poll(() => queries.some((q) => q.startsWith('smartscapeNodes HOST\n| filter host.type == '))).toBe(true)
    } else {
      await expect(page).toHaveURL(/[?&]f=instance%3At3\.large/)
    }
    expect(errors).toEqual([])
  })
}
