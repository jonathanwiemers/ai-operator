// The composer to the pilot at the top of the milestone Cockpit (R12 of
// "Desk: the Cockpit", R3 of "Desk: the Cockpit newest first by issue"):
// the Desk chat with the chat grant, a /pilot note with steer only, else
// the read-only line; "No pilot for this milestone" without a pilot; none
// on a stored milestone. The Cockpit reads from the top: the composer, the
// latest exchange with the pilot (Show earlier reveals older ones), the
// pinned cards, the threads, newest on top, and Load older last. A sent
// message shows as the latest exchange with its answer. The composer is not
// sticky. The old pilot drawer links (?pilot=1, ?tab=pilot) open the
// Cockpit and lose their parameters. Hearing the answer is desk.spec.ts's
// flow 3. Run with npm run e2e (built UI, Chromium).
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import type { MilestoneView, StreamEvent } from '../src/types'
import { answer, now } from './fixtures'
import { ago } from './fixtures/clock'
import { paymentsMilestone } from './fixtures/stream'
import { journeyAt } from './journey'

const milestonePath = '/projects/team-a/shop/milestones/14'

// A stream long enough to scroll on a phone: one merge per issue.
const merges: StreamEvent[] = Array.from({ length: 24 }, (_, i) => ({
  id: 5000 + i,
  at: ago(5 + i * 3),
  namespace: 'team-a',
  project: 'shop',
  milestoneIID: 14,
  issueIID: 300 + i,
  kind: 'merged',
  summary: `Merged !${400 + i} Change ${i}`,
  data: {},
}))

// chat builds a chat event of %14's pilot m minutes ago: the owner's
// message (id) or the pilot's answer to it (replyTo).
const chat = (id: number, m: number, body: string, replyTo?: number): StreamEvent => ({
  id,
  at: ago(m),
  namespace: 'team-a',
  project: 'shop',
  milestoneIID: 14,
  task: paymentsMilestone.pilot!.task,
  kind: replyTo ? 'chat.agent' : 'chat.user',
  summary: 'message',
  data: replyTo ? { role: 'agent', kind: 'message', authorName: 'pilot', body, replyTo } : { role: 'user', kind: 'message', authorName: 'Jo', body },
})
// Seven exchanges, one every 10 minutes, the newest 1 minute ago.
const exchanges: StreamEvent[] = Array.from({ length: 7 }, (_, i) => [
  chat(6101 + 2 * i, 1 + 10 * i, `Answer ${i}`, 6100 + 2 * i),
  chat(6100 + 2 * i, 2 + 10 * i, `Question ${i}`),
]).flat()

type Open = {
  width?: number
  milestone?: MilestoneView
  // events: the newest page (default merges), with an older page behind it.
  events?: StreamEvent[]
  // sent: the events the stream adds once a message is posted to the pilot.
  sent?: StreamEvent[]
}

async function open(page: Page, baseURL: string | undefined, path: string, opts: Open = {}) {
  const problems: string[] = []
  const posts: unknown[] = []
  const origin = new URL(baseURL ?? '').origin
  await page.setViewportSize({ width: opts.width ?? 1280, height: 800 })
  await page.clock.setFixedTime(now)
  page.on('pageerror', (err) => problems.push(err.message))
  await page.route((url) => url.protocol !== 'data:' && url.protocol !== 'blob:' && url.origin !== origin, (r) => r.abort('blockedbyclient'))
  await page.route('**/api/v1/**', async (r) => {
    const req = r.request()
    const url = new URL(req.url())
    const p = url.pathname.slice('/api/v1'.length)
    if (p === '/events') return r.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 3600000\n\n' })
    if (req.method() !== 'GET') {
      if (p.endsWith('/warm')) return r.fulfill({ status: 204 })
      if (opts.sent && req.method() === 'POST' && p === `/projects/team-a/shop/tasks/${paymentsMilestone.pilot!.task}/chat`) {
        posts.push(req.postDataJSON())
        return r.fulfill({ status: 200, json: { id: opts.sent.at(-1)!.id } })
      }
      problems.push(`${req.method()} ${url.pathname}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    if (p === '/stream' && url.searchParams.get('milestone') === '14' && url.searchParams.get('kinds')?.includes('pilot.acted')) {
      if (url.searchParams.has('after')) return r.fulfill({ status: 200, json: { events: [], history: true } })
      if (url.searchParams.has('before')) return r.fulfill({ status: 200, json: { events: [], history: true } })
      const events = [...(posts.length ? opts.sent! : []), ...(opts.events ?? merges)]
      return r.fulfill({ status: 200, json: { events, newest: `c-${events[0]!.id}`, older: 'c-older', history: true } })
    }
    const body = opts.milestone && p === milestonePath ? opts.milestone : answer(p, url.searchParams)
    if (body === undefined) {
      problems.push(`GET ${url.pathname}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    if (typeof body === 'string') return r.fulfill({ status: 200, contentType: 'text/plain; charset=utf-8', body })
    return r.fulfill({ status: 200, json: body })
  })
  await page.goto(path)
  await expect(page.locator('main h1')).toHaveText(paymentsMilestone.title)
  return { problems, posts }
}

const composer = (page: Page) => page.locator('main [data-cockpit-composer]')
const current = (page: Page) => page.locator('main nav[aria-label="View"] a[aria-current="page"]')
const withPerms = (perms: Partial<NonNullable<MilestoneView['permissions']>>): MilestoneView => ({
  ...paymentsMilestone,
  permissions: { ...paymentsMilestone.permissions!, ...perms },
})

test('with the chat grant: the pilot line and the chat to the pilot, no Ask pilot and no drawer@1280', journeyAt(1280), async ({ page, baseURL }) => {
  const { problems } = await open(page, baseURL, milestonePath)
  await expect(current(page)).toHaveText(/^Cockpit/)
  await expect(composer(page)).toHaveAttribute('aria-label', 'Message the pilot')
  const line = composer(page).locator('[data-pilot]')
  await expect(line).toContainText(paymentsMilestone.pilot!.autonomy)
  await expect(line.getByRole('link', { name: 'Pilot task' })).toHaveAttribute('href', `/projects/team-a/shop/tasks/${paymentsMilestone.pilot!.task}`)
  await expect(composer(page).locator('.composer.chat').getByRole('textbox', { name: 'Message' })).toBeVisible()
  await expect(page.getByRole('button', { name: /Ask pilot/ })).toHaveCount(0)
  await expect(page.getByRole('dialog')).toHaveCount(0)
  // The composer is the top of the Cockpit, on screen, and not sticky.
  await expect(composer(page)).toBeInViewport({ ratio: 1 })
  expect(await composer(page).evaluate((e) => getComputedStyle(e).position)).toBe('static')
  expect(problems).toEqual([])
})

// inOrder: whether each selector's first element in main follows the one
// before it in the document.
const inOrder = (page: Page, selectors: string[]) =>
  page.evaluate((sel) => {
    const els = sel.map((s) => document.querySelector(`main ${s}`))
    if (els.some((e) => !e)) return `missing: ${sel.filter((_, i) => !els[i]).join(', ')}`
    return els.every((e, i) => i === 0 || !!(els[i - 1]!.compareDocumentPosition(e!) & Node.DOCUMENT_POSITION_FOLLOWING)) || 'out of order'
  }, selectors)

for (const width of [390, 1280]) {
  test(`the Cockpit reads from the top: composer, latest exchange, pinned cards, threads, Load older@${width}`, journeyAt(width), async ({ page, baseURL }) => {
    const { problems } = await open(page, baseURL, milestonePath, { width, events: [...exchanges, ...merges].sort((a, b) => Date.parse(b.at) - Date.parse(a.at)) })
    const conversation = page.locator('main [data-cockpit-conversation]')
    const threads = page.locator('main [data-cockpit-thread]')
    await expect(conversation.locator('[data-cockpit-exchange]')).toHaveCount(1)
    await expect(threads).toHaveCount(merges.length)
    expect(
      await inOrder(page, [
        '[data-cockpit-composer]',
        '[data-cockpit-conversation]',
        '[data-cockpit-pinned]',
        '[data-cockpit-stream] [data-tab-toolbar]',
        '[data-cockpit-thread]',
        '[data-cockpit-older]',
      ]),
    ).toBe(true)
    // Load older is the last thing in the Cockpit.
    const olderLast = await page.locator('main [data-cockpit-older]').evaluate((b) => {
      const last = [...document.querySelectorAll('main [data-cockpit-thread]')].at(-1)!
      const stream = b.closest('[data-cockpit-stream]')!
      return !!(last.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING) && stream.lastElementChild === b && stream.parentElement!.lastElementChild === stream
    })
    expect(olderLast, 'Load older is last').toBe(true)

    // The latest exchange: the newest question, then its answer.
    const latest = conversation.locator('[data-cockpit-exchange]').first()
    await expect(latest.locator(':scope > ol > li')).toHaveCount(2)
    await expect(latest.locator(':scope > ol > li').first()).toContainText('Question 0')
    await expect(latest.locator(':scope > ol > li').last()).toContainText('Answer 0')
    // The thread with the newest event (#300's merge) is on top.
    await expect(threads.first()).toHaveAttribute('aria-label', '#300')
    await expect(threads.first()).toContainText('Merged !400 Change 0')
    await expect(threads.last()).toHaveAttribute('aria-label', '#323')

    // Show earlier reveals the older exchanges 5 at a time, newest first.
    const earlier = conversation.getByRole('button', { name: 'Show earlier (6)' })
    await earlier.click()
    await expect(conversation.locator('[data-cockpit-exchange]')).toHaveCount(6)
    await expect(conversation.locator('[data-cockpit-exchange]').nth(5)).toContainText('Question 5')
    await conversation.getByRole('button', { name: 'Show earlier (1)' }).click()
    await expect(conversation.locator('[data-cockpit-exchange]')).toHaveCount(7)
    await expect(conversation.getByRole('button', { name: /^Show earlier/ })).toHaveCount(0)
    expect(problems).toEqual([])
  })
}

test('a sent message shows as the latest exchange with its answer@1280', journeyAt(1280), async ({ page, baseURL }) => {
  const sent = [chat(6201, 0, 'Two issues are in review.', 6200), chat(6200, 0, 'What is #281 doing?')]
  const { problems, posts } = await open(page, baseURL, milestonePath, { events: [...exchanges, ...merges].sort((a, b) => Date.parse(b.at) - Date.parse(a.at)), sent })
  const box = composer(page).getByRole('textbox', { name: 'Message' })
  await box.fill('What is #281 doing?')
  await box.press('ControlOrMeta+Enter')
  await expect.poll(() => posts).toEqual([{ body: 'What is #281 doing?', clientKey: expect.any(String), kind: 'message' }])
  const latest = page.locator('main [data-cockpit-conversation] [data-cockpit-exchange]').first()
  await expect(latest.locator(':scope > ol > li').first()).toContainText('What is #281 doing?')
  await expect(latest.locator(':scope > ol > li').last()).toContainText('Two issues are in review.')
  await expect(page.locator('main [data-cockpit-conversation]').getByRole('button', { name: 'Show earlier (7)' })).toBeVisible()
  expect(problems).toEqual([])
})

test('steer without the chat grant: a /pilot note; neither: the read-only line@1280', async ({ page, baseURL }) => {
  let { problems } = await open(page, baseURL, milestonePath, { milestone: withPerms({ chat: false }) })
  await expect(composer(page).locator('.composer.note').getByRole('textbox')).toBeVisible()
  await expect(composer(page).locator('.composer.chat')).toHaveCount(0)
  // The note composer scrolls with the page too (R3), not sticky.
  expect(await composer(page).locator('.composer.note').evaluate((e) => getComputedStyle(e).position)).toBe('static')
  expect(problems).toEqual([])
  ;({ problems } = await open(page, baseURL, milestonePath, { milestone: withPerms({ chat: false, steer: false }) }))
  await expect(composer(page)).toContainText("Read-only: only the milestone's owners can message the pilot.")
  await expect(composer(page).getByRole('textbox')).toHaveCount(0)
  expect(problems).toEqual([])
})

test('a milestone without a pilot says so; a stored milestone has no composer@1280', async ({ page, baseURL }) => {
  let { problems } = await open(page, baseURL, milestonePath, { milestone: { ...paymentsMilestone, pilot: undefined } })
  await expect(composer(page)).toContainText('No pilot for this milestone.')
  await expect(composer(page).getByRole('textbox')).toHaveCount(0)
  expect(problems).toEqual([])
  const stored: MilestoneView = { ...paymentsMilestone, state: 'done', stored: true, autopilot: undefined, pilot: undefined, prompts: [] }
  ;({ problems } = await open(page, baseURL, milestonePath, { milestone: stored }))
  await expect(current(page)).toHaveText(/^Cockpit/)
  await expect(page.locator('main [data-cockpit-stream]')).toBeVisible()
  await expect(composer(page)).toHaveCount(0)
  expect(problems).toEqual([])
})

for (const [label, query] of [
  ['?pilot=1', '?pilot=1'],
  ['?tab=pilot', '?tab=pilot'],
  ['?tab=pilot&pilot=1', '?tab=pilot&pilot=1'],
] as const) {
  test(`an old ${label} link opens the Cockpit without the pilot parameters@1280`, async ({ page, baseURL }) => {
    const { problems } = await open(page, baseURL, `${milestonePath}${query}`)
    await expect(page).toHaveURL(/\/milestones\/14$/)
    await expect(current(page)).toHaveText(/^Cockpit/)
    await expect(composer(page).getByRole('textbox', { name: 'Message' })).toBeVisible()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    expect(problems).toEqual([])
  })
}

test('?pilot=1 on another tab keeps the tab and drops the parameter@1280', async ({ page, baseURL }) => {
  const { problems } = await open(page, baseURL, `${milestonePath}?tab=metrics&pilot=1`)
  await expect(page).toHaveURL(/\/milestones\/14\?tab=metrics$/)
  await expect(current(page)).toHaveText('Metrics')
  expect(problems).toEqual([])
})

test('on a phone the composer is the top of the Cockpit and not sticky@390', async ({ page, baseURL }) => {
  const { problems } = await open(page, baseURL, milestonePath, { width: 390 })
  await expect(composer(page).getByRole('textbox', { name: 'Message' })).toBeVisible()
  const top = await page.locator('main [data-cockpit]').evaluate((e) => e.getBoundingClientRect().top)
  expect((await composer(page).boundingBox())!.y).toBeLessThanOrEqual(top + 0.5)
  expect(await composer(page).evaluate((e) => getComputedStyle(e).position)).toBe('static')
  expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0)
  expect(problems).toEqual([])
})

// axe (WCAG 2.2 AA) on the Cockpit with each composer, light and dark.
// color-contrast is left out as in cockpit.spec.ts: the pinned and event
// cards' known R6 failures; every other rule must pass.
for (const colorScheme of ['light', 'dark'] as const) {
  for (const width of [390, 1280]) {
    for (const [name, milestone] of [
      ['chat', paymentsMilestone],
      ['note', withPerms({ chat: false })],
      ['read-only', withPerms({ chat: false, steer: false })],
    ] as const) {
      test(`axe is green with the ${name} composer in ${colorScheme}@${width}`, async ({ page, baseURL }) => {
        await page.emulateMedia({ colorScheme })
        const { problems } = await open(page, baseURL, milestonePath, { width, milestone })
        await expect(composer(page)).toBeVisible()
        const results = await new AxeBuilder({ page })
          .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
          .disableRules(['color-contrast'])
          .analyze()
        const violations = results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)
        expect(violations).toEqual([])
        expect(problems).toEqual([])
      })
    }
  }
}
