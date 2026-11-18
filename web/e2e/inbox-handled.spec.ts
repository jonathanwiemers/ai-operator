// The Inbox's Open | Handled switch (#532: R15): the switch lives in
// ?view=, Handled lists the closed prompts and the pilot's actions with
// their labels, a row opens its problem on the milestone's Needs you tab,
// and Open keeps the open items. Run with npm run e2e (built UI, Chromium).
import { expect, test, type Page } from '@playwright/test'
import { answer } from './fixtures'
import { inbox } from './fixtures/rail'

const widths = [
  { width: 375, height: 812 },
  { width: 1280, height: 800 },
]

// open shows path with the API answered from the fixtures (GET only), the
// Inbox from inboxBody when given. No
// fixed clock: Vue skips a bubbling handler attached at the event's own
// Date.now(), which a frozen clock makes equal.
async function open(page: Page, baseURL: string | undefined, path: string, size: { width: number; height: number }, inboxBody?: unknown) {
  const origin = new URL(baseURL ?? '').origin
  const problems: string[] = []
  await page.setViewportSize(size)
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
    // A problem card wakes the pilot (#586).
    if (req.method() === 'POST' && p.endsWith('/warm')) return r.fulfill({ status: 204 })
    const body = req.method() !== 'GET' ? undefined : inboxBody && p === '/inbox' ? inboxBody : answer(p, url.searchParams)
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

const switcher = (page: Page) => page.getByRole('navigation', { name: 'Inbox view' })
const handledRows = (page: Page) => page.locator('main [data-handled] > li')

for (const size of widths) {
  test(`the switch changes ?view=@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, '/inbox', size)
    await expect(switcher(page).getByRole('link', { name: 'Open' })).toHaveAttribute('aria-current', 'page')
    await switcher(page).getByRole('link', { name: 'Handled' }).click()
    await expect(page).toHaveURL(/\/inbox\?view=handled$/)
    await expect(switcher(page).getByRole('link', { name: 'Handled' })).toHaveAttribute('aria-current', 'page')
    await switcher(page).getByRole('link', { name: 'Open' }).click()
    await expect(page).toHaveURL(/\/inbox$/)
    expect(problems).toEqual([])
  })

  test(`Handled shows the rows with their labels@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, '/inbox?view=handled', size)
    const rows = handledRows(page)
    await expect(rows).toHaveCount(inbox.handled.length)
    await expect(rows.nth(0)).toContainText('#281')
    await expect(rows.nth(0)).toContainText('Release SDK 4 before the refunds issue?')
    await expect(rows.nth(0)).toContainText('Answered by jo')
    await expect(rows.nth(1)).toContainText('#282')
    await expect(rows.nth(1)).toContainText('Pilot')
    await expect(rows.nth(2)).toContainText('Milestone')
    await expect(rows.nth(2)).toContainText('Resolved on its own: main is green again')
    // Untrusted text renders as text: the backticks stay.
    await expect(rows.nth(2)).toContainText('`d4e5f6a`')
    await expect(page.getByText('Support refunds through the payment provider')).toHaveCount(0)
    expect(problems).toEqual([])
  })

  test(`a row opens the milestone's Needs you tab at the problem@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, '/inbox?view=handled', size)
    await handledRows(page).nth(0).getByRole('link').click()
    await expect(page).toHaveURL(/\/projects\/team-a\/shop\/milestones\/14\?tab=needs#problem-281$/)
    await expect(page.getByRole('navigation', { name: 'View' }).getByRole('link', { name: /^Needs you/ })).toHaveAttribute('aria-current', 'page')
    expect(problems).toEqual([])
  })

  test(`Open still shows the open items@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, '/inbox', size)
    await expect(page.locator('main ul').last().locator(':scope > li')).toHaveCount(inbox.items.length)
    await expect(page.getByText('Support refunds through the payment provider')).toBeVisible()
    await expect(handledRows(page)).toHaveCount(0)
    expect(problems).toEqual([])
  })
}

test('Handled says so when nothing was handled', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, '/inbox?view=handled', widths[1]!, { ...inbox, handled: [] })
  await expect(page.getByText('Nothing handled in the last 7 days.')).toBeVisible()
  expect(problems).toEqual([])
})
