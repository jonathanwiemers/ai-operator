// The prompt card after an answer and the one-line prompt rows of the
// stream (#528: R9, R10, R11). Run with npm run e2e (built UI, Chromium).
import { expect, test, type Page } from '@playwright/test'
import { answer, now } from './fixtures'
import { refundsPrompt } from './fixtures/task'

const widths = [
  { width: 375, height: 812 },
  { width: 1280, height: 800 },
]

const needsTab = '/projects/team-a/shop/milestones/14?tab=needs'
const pilotTab = '/projects/team-a/shop/milestones/14?tab=pilot'
const chatPath = `/api/v1/projects/team-a/shop/tasks/${refundsPrompt.target.task}/chat`

interface Problems {
  unknownAPI: string[]
  otherOrigin: string[]
  pageErrors: string[]
}

// open shows path with the API answered from the fixtures (GET only) and
// the chat POSTs of the prompt's target task answered by chat.
async function open(
  page: Page,
  baseURL: string | undefined,
  path: string,
  size: { width: number; height: number },
  chat: { status: number; json: unknown } = { status: 200, json: { id: 991 } },
  wait = true,
) {
  const origin = new URL(baseURL ?? '').origin
  const problems: Problems = { unknownAPI: [], otherOrigin: [], pageErrors: [] }
  await page.setViewportSize(size)
  await page.clock.setFixedTime(now)
  page.on('pageerror', (err) => problems.pageErrors.push(err.message))
  page.on('request', (req) => {
    const url = new URL(req.url())
    if (url.protocol !== 'data:' && url.protocol !== 'blob:' && url.origin !== origin) problems.otherOrigin.push(req.url())
  })
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
    const body = req.method() === 'GET' ? answer(p, url.searchParams) : undefined
    if (body === undefined) {
      problems.unknownAPI.push(`${req.method()} ${url.pathname}${url.search}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    if (typeof body === 'string') return r.fulfill({ status: 200, contentType: 'text/plain; charset=utf-8', body })
    return r.fulfill({ status: 200, json: body })
  })
  // Registered after the fixtures, so it answers the POSTs first.
  const posts: unknown[] = []
  await page.route(
    (url) => url.pathname === chatPath,
    async (r) => {
      if (r.request().method() !== 'POST') return r.fallback()
      posts.push(r.request().postDataJSON())
      return r.fulfill(chat)
    },
  )
  await page.goto(path)
  if (wait) await expect(page.locator(`#prompt-${refundsPrompt.id}`)).toBeVisible()
  return { problems, posts }
}

const clean = { unknownAPI: [], otherOrigin: [], pageErrors: [] }
const command = (cmd: Record<string, string>) => ({ body: JSON.stringify(cmd), clientKey: expect.any(String), kind: 'command' })

for (const size of widths) {
  test(`a click hides the buttons and shows sending@${size.width}`, async ({ page, baseURL }) => {
    const { problems, posts } = await open(page, baseURL, needsTab, size)
    const card = page.locator(`#prompt-${refundsPrompt.id}`)
    await card.getByRole('button', { name: 'Refuse above one capture', exact: true }).click()

    await expect(card.getByText('✓ Refuse above one capture · sending…')).toBeVisible()
    await expect(card.getByRole('button')).toHaveCount(0)
    await expect(card.getByRole('textbox')).toHaveCount(0)
    expect(posts).toEqual([command({ cmd: 'answer', prompt: refundsPrompt.id, option: 'refuse' })])
    expect(problems).toEqual(clean)
  })

  test(`a refused answer brings the buttons back with the error@${size.width}`, async ({ page, baseURL }) => {
    const { problems, posts } = await open(page, baseURL, needsTab, size, { status: 403, json: { error: 'forbidden' } })
    const card = page.locator(`#prompt-${refundsPrompt.id}`)
    await card.getByRole('button', { name: 'Split across captures', exact: true }).click()

    await expect(card.getByText('You may not chat on this project.')).toBeVisible()
    await expect(card.getByRole('button', { name: 'Split across captures', exact: true })).toBeEnabled()
    await expect(card.getByRole('button', { name: 'Refuse above one capture', exact: true })).toBeEnabled()
    await expect(card.getByRole('textbox')).toBeVisible()
    await expect(card.getByText('sending…')).toHaveCount(0)
    expect(posts).toHaveLength(1)
    expect(problems).toEqual(clean)
  })

  test(`a closed card shows the choice and folds the rest@${size.width}`, async ({ page, baseURL }) => {
    const { problems } = await open(page, baseURL, '/projects/team-a/shop/milestones/14#prompt-Q3', size)

    // Answered: the chosen option with its action, the outcome, the label.
    const q3 = page.locator('#prompt-Q3')
    await expect(q3).toBeInViewport()
    const shown = q3.locator('li:visible')
    await expect(shown).toHaveCount(1)
    await expect(shown).toContainText('✓ Release SDK 4 first')
    await expect(shown).toContainText('→ Plans a release after #280')
    await expect(q3.getByText('Planned release 2 after #280.')).toBeVisible()
    await expect(q3.getByText('Answered by jo in the Desk: Release SDK 4 first')).toBeVisible()
    await expect(q3.getByText('Ship together')).toBeHidden()
    await q3.getByText('Other options (1)').click()
    await expect(q3.getByText('Ship together')).toBeVisible()
    await expect(q3.getByRole('button')).toHaveCount(0)

    // Closed by the pilot: no option in sight, all fold under Options. Its
    // row under Handled unfolds the card.
    const q2 = page.locator('#prompt-Q2')
    await page.locator('#problem-0 [data-entry="prompt"]').filter({ hasText: 'Q2' }).locator('summary').first().click()
    await expect(q2.getByText('Handled by the pilot: the pause ended')).toBeVisible()
    await expect(q2.getByText('Revert a1b2c3d')).toBeHidden()
    await expect(q2.getByText('Options (2)')).toBeVisible()

    // Replaced: the label links to the newer prompt.
    const q1 = page.locator('#prompt-Q1')
    await page.locator('#problem-0 [data-entry="prompt"]').filter({ hasText: 'Q1' }).locator('summary').first().click()
    await expect(q1.getByText('Replaced by')).toBeVisible()
    await expect(q1.getByRole('link', { name: 'Q3', exact: true })).toHaveAttribute('href', /#prompt-Q3$/)
    expect(problems).toEqual(clean)
  })

  test(`the pilot tab shows an open prompt as one row without buttons, the row leads to its card@${size.width}`, async ({ page, baseURL }) => {
    const { problems } = await open(page, baseURL, pilotTab, size, undefined, false)
    const events = page.locator('section', { has: page.getByRole('heading', { name: 'Pilot events' }) })
    const row = events.locator('[data-prompt-row]')
    await expect(row).toHaveCount(1)
    await expect(row.getByRole('link', { name: `${refundsPrompt.id} asked:` })).toBeVisible()
    await expect(row.getByText('open', { exact: true })).toBeVisible()
    await expect(row.getByRole('button')).toHaveCount(0)
    // The card is on Needs you only.
    await expect(page.locator(`[data-prompt="${refundsPrompt.id}"]`)).toHaveCount(0)
    await row.getByRole('link', { name: `${refundsPrompt.id} asked:` }).click()
    await expect(page.locator(`#prompt-${refundsPrompt.id}`)).toBeInViewport()
    await expect(page.locator(`[data-prompt="${refundsPrompt.id}"]`)).toHaveCount(1)
    expect(problems).toEqual(clean)
  })
}
