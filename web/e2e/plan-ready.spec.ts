// A ready plan in the Inbox (R4 of "Desk: a notification on your phone and
// desktop when a plan agent has answered"): the row shows Plan ready with the
// plan's title and the start of the reply as text, opens the plan page, and
// axe is green. Run with npm run e2e (built UI, Chromium).
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { answer } from './fixtures'
import { planReady } from './fixtures/plans'
import { inbox } from './fixtures/rail'
import { features } from './fixtures/session'

const inboxBody = { ...inbox, items: [...inbox.items, planReady] }
// Plan pages need the plans feature.
const withPlans = { features: [...features.features, { name: 'plans' }] }

// open shows path with the API answered from the fixtures (GET only), the
// Inbox with the ready plan appended after the task items, and the plans
// feature on.
async function open(page: Page, baseURL: string | undefined, path: string) {
  const origin = new URL(baseURL ?? '').origin
  const problems: string[] = []
  await page.setViewportSize({ width: 1280, height: 800 })
  page.on('pageerror', (err) => problems.push(err.message))
  await page.route(
    (url) => url.protocol !== 'data:' && url.protocol !== 'blob:' && url.origin !== origin,
    (r) => r.abort('blockedbyclient'),
  )
  await page.route('**/api/v1/**', async (r) => {
    const req = r.request()
    const url = new URL(req.url())
    const p = url.pathname.slice('/api/v1'.length)
    if (p === '/events' && req.method() === 'GET') {
      return r.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 3600000\n\n' })
    }
    const body = req.method() !== 'GET' ? undefined : p === '/inbox' ? inboxBody : p === '/features' ? withPlans : answer(p, url.searchParams)
    if (body === undefined) {
      problems.push(`${req.method()} ${url.pathname}${url.search}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    if (typeof body === 'string') return r.fulfill({ status: 200, contentType: 'text/plain; charset=utf-8', body })
    return r.fulfill({ status: 200, json: body })
  })
  await page.goto(path)
  return problems
}

const planRow = (page: Page) => page.locator('main li[data-inbox-kind="plan"]')

test('a ready plan shows in the Inbox and opens the plan page@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, '/inbox')
  const row = planRow(page)
  await expect(row).toHaveCount(1)
  await expect(row).toContainText('Plan ready · CSV export on the Metrics page')
  // Untrusted text renders as text: the asterisks stay.
  await expect(row).toContainText('Here is a first draft with **three** issues.')
  await row.getByRole('link', { name: /Plan ready/ }).click()
  await expect(page).toHaveURL(/\/projects\/team-a\/tools-foo\/plans\/plan-wait$/)
  await expect(page.locator('main [data-plan-phase]')).toHaveText('Starting')
  expect(problems).toEqual([])
})

for (const colorScheme of ['light', 'dark'] as const) {
  test(`axe is green on the Inbox with a ready plan in ${colorScheme}@1280`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, '/inbox')
    await page.emulateMedia({ colorScheme })
    await expect(planRow(page)).toHaveCount(1)
    // The Inbox has a known dark color-contrast finding (known-a11y.ts,
    // inbox@1280-dark, R6 #641) in the rows it shares with the plan row, so
    // the dark pass leaves that rule out; every other rule must pass.
    let axe = new AxeBuilder({ page }).include('main').withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
    if (colorScheme === 'dark') axe = axe.disableRules(['color-contrast'])
    const results = await axe.analyze()
    expect(results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)).toEqual([])
    expect(problems).toEqual([])
  })
}
