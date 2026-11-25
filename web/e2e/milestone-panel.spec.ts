// The milestone's Now & next sidebar (R7 of "Desk: the Cockpit newest first
// by issue", #820), which replaced the panel's Issues and Queue views. At
// 1280 px it is a column beside the Cockpit; at 390 px the header's Now &
// next button, or an old ?panel=issues or ?panel=queue link, opens it as a
// slide-over, Escape closes it and focus returns to the button. A Running
// row opens the issue's thread in the Cockpit, with ?live=1 under the
// transcript grant. The old Decisions view is the Decisions tab (R6): an
// old ?panel=decisions#<id> link lands on the tab at the card. axe on both
// widths. The fixture milestone %14 has merged (#279, #280) and waiting
// (#283, and #284 on a hold) issues, is second in the project's queue, and
// has one superseded decision (D280-1); withNow (below) makes #282's
// implementer run and #281 wait for its pipeline. Run with npm run e2e
// (built UI, Chromium).
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { journeyAt } from './journey'
import { answer, now } from './fixtures'
import { ago } from './fixtures/clock'
import { captures, capturesTranscript, refunds } from './fixtures/task'
import type { MilestoneView } from '../src/generated/views'
import type { StreamEvent } from '../src/types'

const milestonePath = '/projects/team-a/shop/milestones/14'

// withNow: #282 runs its implementer (captures, 4 min), #281 waits for the
// pipeline of its pushed head, #283 comes after #282, and the stream has
// an event of #282, so it has a thread.
function withNow(m: MilestoneView, transcript: boolean): MilestoneView {
  const items = (m.autopilot?.items ?? []).map((it) => {
    if (it.iid === 282) return { ...it, lane: 'running' as const, waitingFor: undefined, task: captures.name }
    if (it.iid === 283) return { ...it, waitingFor: [282] }
    return it
  })
  const ci = { sha: '4f1c9e2a7b3d', state: 'Running' as const, pipelineID: 88200 }
  const tasks = [...m.tasks.filter((t) => t.issue?.iid !== 282 && t.issue?.iid !== 281), { ...refunds, status: { ...refunds.status, ci } }, captures]
  return { ...m, autopilot: { ...m.autopilot!, items }, tasks, permissions: { ...m.permissions!, transcript } }
}

const run282: StreamEvent = {
  id: 4901,
  at: ago(4),
  namespace: 'team-a',
  project: 'shop',
  milestoneIID: 14,
  issueIID: 282,
  task: captures.name,
  kind: 'run.started',
  summary: 'Run 1 started on #282',
  data: {},
}

async function open(page: Page, baseURL: string | undefined, path: string, width: number, transcript = true): Promise<string[]> {
  const origin = new URL(baseURL ?? '').origin
  const problems: string[] = []
  await page.setViewportSize({ width, height: 800 })
  await page.clock.install({ time: now })
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
    if (p === `/projects/team-a/shop/tasks/${captures.name}/transcript`) {
      const body = capturesTranscript.map((e) => `event: entry\ndata: ${JSON.stringify(e)}\n\n`).join('')
      return r.fulfill({ status: 200, contentType: 'text/event-stream', body: `retry: 3600000\n\n${body}` })
    }
    let body = answer(p, url.searchParams)
    if (p === '/projects/team-a/shop/milestones/14') body = withNow(body as MilestoneView, transcript)
    if (p === '/stream' && url.searchParams.get('milestone') === '14' && !url.searchParams.has('before') && !url.searchParams.has('after')) {
      const s = body as { events: StreamEvent[] }
      body = { ...s, events: [run282, ...s.events] }
    }
    if (body === undefined) {
      problems.push(`GET ${url.pathname}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    if (typeof body === 'string') return r.fulfill({ status: 200, contentType: 'text/plain; charset=utf-8', body })
    return r.fulfill({ status: 200, json: body })
  })
  await page.goto(path)
  return problems
}

const panel = (page: Page) => page.getByRole('complementary', { name: 'Now & next' })
const section = (page: Page, id: string) => panel(page).locator(`[data-now-section="${id}"]`)
// The header's button by its data attribute: while the modal slide-over is
// open, the page behind it is hidden from role queries.
const headerButton = (page: Page) => page.locator('main [data-stream-head] [data-panel-open="now"]')

// expectSections checks the four sections on the fixture.
async function expectSections(page: Page) {
  const p = panel(page)
  await expect(p.getByRole('heading', { level: 3 })).toHaveText([/^Running\s*1$/, /^Waiting\s*2$/, /^Up next\s*1$/, /^Milestones$/])
  await expect(section(page, 'running').getByRole('link')).toHaveText([/^#282 implementing · 4 min\s*Partial captures/])
  await expect(section(page, 'waiting').getByRole('link')).toHaveText([/^#281 waiting for CI/, /^#284 held: CI failed on !306 \(job docs-lint\)\./])
  await expect(section(page, 'next').locator('[data-now-row]')).toHaveText([/^#283 after #282/])
  const milestones = section(page, 'milestones').getByRole('link')
  await expect(milestones).toHaveCount(2)
  await expect(milestones.first()).toHaveAttribute('aria-current', 'page')
  await expect(milestones.first()).toContainText('This milestone · 2 of 6 merged')
  await expect(milestones.nth(1)).toHaveAttribute('href', '/projects/team-a/shop/milestones/15')
}

// axe (WCAG 2.2 AA) on the sidebar. color-contrast is left out as on the
// Cockpit (cockpit.spec.ts): it is the Desk-wide rule of #641 the badges
// share; every other rule must pass, and no known-a11y entry is added.
async function axe(page: Page, include: string) {
  const results = await new AxeBuilder({ page })
    .include(include)
    .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
    .disableRules(['color-contrast'])
    .analyze()
  return results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)
}

test('the sidebar is a column beside the Cockpit with its four sections, a Running row opens the thread with the transcript@1280', journeyAt(1280), async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, `${milestonePath}?tab=decisions`, 1280)
  await expect(panel(page)).toBeVisible()
  await expect(page.locator('main [data-panel-open]')).toHaveCount(0)
  await expectSections(page)
  expect(await axe(page, '[data-milestone-panel]')).toEqual([])

  // From another tab the row opens the Cockpit at the thread.
  await section(page, 'running').getByRole('link').click()
  await expect(page).toHaveURL(new RegExp(`${milestonePath}\\?live=1#issue-282$`))
  const thread = page.locator('main #issue-282')
  await expect(thread).toBeInViewport()
  await expect(thread.getByRole('button', { name: 'Live transcript' })).toHaveAttribute('aria-expanded', 'true')
  await expect(thread.getByText('Switching to the retry helper.')).toBeVisible()
  // A Waiting row opens its thread without the transcript.
  await section(page, 'waiting').getByRole('link').first().click()
  await expect(page).toHaveURL(new RegExp(`${milestonePath}#issue-281$`))

  // No live lines in the milestone Cockpit (R7).
  await expect(page.locator('main [data-cockpit-live]')).toHaveCount(0)
  expect(problems).toEqual([])
})

test('without the transcript grant a Running row opens the thread only@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, milestonePath, 1280, false)
  await section(page, 'running').getByRole('link').click()
  await expect(page).toHaveURL(new RegExp(`${milestonePath}#issue-282$`))
  await expect(page.locator('main #issue-282')).toBeInViewport()
  expect(problems).toEqual([])
})

test('on the Issues tab the sidebar is a slide-over at every width@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, `${milestonePath}?tab=issues`, 1280)
  await expect(panel(page), 'no column beside the Issues table').toHaveCount(0)
  await headerButton(page).click()
  await expect(page.getByRole('dialog', { name: 'Now & next' })).toBeVisible()
  await expectSections(page)
  await page.keyboard.press('Escape')
  await expect(headerButton(page)).toBeFocused()
  expect(problems).toEqual([])
})

test('the Now & next button opens the slide-over, Escape closes it and focus returns@390', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, milestonePath, 390)
  await expect(panel(page), 'no column on a phone').toHaveCount(0)
  await expect(page.locator('main [data-stream-head] [data-panel-open]'), 'one button').toHaveCount(1)
  const button = headerButton(page)
  await expect(button).toHaveText('Now & next')
  await expect(button).toHaveAttribute('aria-expanded', 'false')
  await button.click()
  const sheet = page.getByRole('dialog', { name: 'Now & next' })
  await expect(sheet).toBeVisible()
  await sheet.evaluate((e) => Promise.all(e.getAnimations().map((a) => a.finished)))
  await expect(button).toHaveAttribute('aria-expanded', 'true')
  await expectSections(page)
  expect(await axe(page, '[role="dialog"]')).toEqual([])
  const fit = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(fit, 'the page does not scroll sideways').toBe(0)

  await page.keyboard.press('Escape')
  await expect(sheet).toBeHidden()
  await expect(button).toBeFocused()
  expect(problems).toEqual([])
})

test('a Running row in the slide-over closes it and opens the thread@390', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, milestonePath, 390)
  await headerButton(page).click()
  const sheet = page.getByRole('dialog', { name: 'Now & next' })
  await sheet.getByRole('link', { name: /^#282 implementing/ }).click()
  await expect(sheet).toBeHidden()
  await expect(page).toHaveURL(new RegExp(`${milestonePath}\\?live=1#issue-282$`))
  await expect(page.locator('main #issue-282')).toBeInViewport()
  expect(problems).toEqual([])
})

// Every link of the sidebar closes the slide-over, also one that keeps the
// page (this milestone's own link).
test('a milestone link in the slide-over closes it@390', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, milestonePath, 390)
  await headerButton(page).click()
  const sheet = page.getByRole('dialog', { name: 'Now & next' })
  await expect(sheet).toBeVisible()
  await sheet.locator('[data-now-milestone="14"]').getByRole('link').click()
  await expect(sheet).toBeHidden()
  await expect(page).toHaveURL(new RegExp(`${milestonePath}$`))
  expect(problems).toEqual([])
})

// The old links to the panel's Issues and Queue views open the sidebar
// (D820-P2): the address drops ?panel=.
for (const view of ['issues', 'queue']) {
  test(`an old ?panel=${view} link opens the slide-over@390`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?panel=${view}`, 390)
    const sheet = page.getByRole('dialog', { name: 'Now & next' })
    await expect(sheet).toBeVisible()
    await expect(page).toHaveURL(new RegExp(`${milestonePath}$`))
    await expectSections(page)
    await page.keyboard.press('Escape')
    await expect(sheet).toBeHidden()
    expect(problems).toEqual([])
  })

  test(`an old ?panel=${view} link shows the column@1280`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?panel=${view}`, 1280)
    await expect(page).toHaveURL(new RegExp(`${milestonePath}$`))
    await expect(panel(page)).toBeVisible()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    expect(problems).toEqual([])
  })
}

// landsOnCard checks that the old link opened the Decisions tab at the
// superseded card D280-1, scrolled into view and focused, with no panel
// view or sheet for it.
async function landsOnCard(page: Page) {
  await expect(page).toHaveURL(new RegExp(`${milestonePath}\\?tab=decisions#D280-1$`))
  await expect(page.locator('main [data-stream-head]').getByRole('navigation', { name: 'View' }).getByRole('link', { name: 'Decisions' })).toHaveAttribute('aria-current', 'page')
  const card = page.locator('main article#D280-1[data-decision]')
  await expect(card).toBeInViewport()
  await expect(card).toBeFocused()
  await expect(card.locator('[data-superseded]')).toHaveText('Superseded by D281-2')
  await expect(page.getByRole('dialog')).toHaveCount(0)
}

test('an old ?panel=decisions#<id> link lands on the Decisions tab at the card@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, `${milestonePath}?panel=decisions#D280-1`, 1280)
  await landsOnCard(page)
  // Revisit is on the tab's cards (DecisionRevisit, R6).
  await expect(page.locator('main [data-decisions] [data-revisit]').first()).toBeAttached()
  expect(await axe(page, '[data-decisions]')).toEqual([])
  expect(problems).toEqual([])
})

// Active hides superseded cards: a link to one switches the tab to All.
test('a #<id> link to a superseded card under ?state=active switches to All@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, `${milestonePath}?tab=decisions&state=active#D280-1`, 1280)
  await landsOnCard(page)
  expect(problems).toEqual([])
})

// An old link to the panel's Decisions view opens the tab, not a
// slide-over, on phones too.
test('an old ?panel=decisions#<id> link lands on the Decisions tab at the card@390', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, `${milestonePath}?panel=decisions#D280-1`, 390)
  await landsOnCard(page)
  expect(await axe(page, '[data-decisions]')).toEqual([])
  expect(problems).toEqual([])
})
