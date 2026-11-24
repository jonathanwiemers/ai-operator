// The composer to the pilot at the bottom of the milestone Cockpit (R12 of
// "Desk: the Cockpit"): the Desk chat with the chat grant, a /pilot note
// with steer only, else the read-only line; "No pilot for this milestone"
// without a pilot; none on a stored milestone. The old pilot drawer links
// (?pilot=1, ?tab=pilot) open the Cockpit and lose their parameters. On a
// phone the composer stays at the bottom of the screen, above the bottom
// bar and the keyboard. Sending and hearing the answer is desk.spec.ts's
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

async function open(page: Page, baseURL: string | undefined, path: string, opts: { width?: number; milestone?: MilestoneView } = {}) {
  const problems: string[] = []
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
      problems.push(`${req.method()} ${url.pathname}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    if (p === '/stream' && url.searchParams.get('milestone') === '14' && url.searchParams.get('kinds')?.includes('pilot.acted')) {
      if (url.searchParams.has('after')) return r.fulfill({ status: 200, json: { events: [], history: true } })
      return r.fulfill({ status: 200, json: { events: merges, newest: `c-${merges[0]!.id}`, history: true } })
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
  return { problems }
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
  // The composer comes after the stream and, sticky, stays on screen at
  // the bottom of the pane while the pinned cards and the stream scroll.
  const after = await page
    .locator('main [data-cockpit-stream]')
    .evaluate((stream) => !!(stream.compareDocumentPosition(document.querySelector('main [data-cockpit-composer]')!) & Node.DOCUMENT_POSITION_FOLLOWING))
  expect(after, 'the composer follows the stream').toBe(true)
  await expect(composer(page)).toBeInViewport({ ratio: 1 })
  const b = (await composer(page).boundingBox())!
  expect(b.y + b.height, 'at the bottom of the screen').toBeGreaterThan(700)
  expect(problems).toEqual([])
})

test('steer without the chat grant: a /pilot note; neither: the read-only line@1280', async ({ page, baseURL }) => {
  let { problems } = await open(page, baseURL, milestonePath, { milestone: withPerms({ chat: false }) })
  await expect(composer(page).locator('.composer.note').getByRole('textbox')).toBeVisible()
  await expect(composer(page).locator('.composer.chat')).toHaveCount(0)
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

test('on a phone the composer stays at the bottom, above the bottom bar and the keyboard@390', async ({ page, baseURL }) => {
  const { problems } = await open(page, baseURL, milestonePath, { width: 390 })
  await expect(composer(page).getByRole('textbox', { name: 'Message' })).toBeVisible()
  const bottom = async () => {
    const b = (await composer(page).boundingBox())!
    return b.y + b.height
  }
  const barTop = await page.getByRole('navigation', { name: 'Sections' }).evaluate((e) => e.getBoundingClientRect().top)
  // At the top of the page, with the stream below the screen: the
  // composer sits on the bottom bar.
  await page.evaluate(() => window.scrollTo(0, 0))
  await expect.poll(bottom).toBeLessThanOrEqual(barTop + 0.5)
  expect(await bottom()).toBeGreaterThan(barTop - 2)
  expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0)

  // Typing: the bar goes; an iOS keyboard of 300 px (what
  // useKeyboardInset sets from visualViewport) lifts the composer. The
  // composer stays within the Cockpit, so the page is scrolled with the
  // Cockpit from 100 px down, as the phone scrolls the focused box into view.
  await composer(page).getByRole('textbox', { name: 'Message' }).focus()
  await page.evaluate(() => document.documentElement.style.setProperty('--keyboard-inset', '300px'))
  await page.locator('main [data-cockpit]').evaluate((e) => window.scrollBy(0, e.getBoundingClientRect().top - 100))
  await expect.poll(bottom).toBeLessThanOrEqual(800 - 300 + 0.5)
  expect(await bottom()).toBeGreaterThan(800 - 300 - 2)
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
