// The project Cockpit (R15 of "Desk: the Cockpit", #790): the project page
// opens on a Cockpit across its milestones. On top the pinned cards of every
// milestone with open needs, each under its milestone's name with a link to
// that milestone's Cockpit; the live lines of all running issues; below
// them the threads per milestone and issue, newest on top; no composer. The panel is Queue ·
// Decisions (a column at 1280 px, a slide-over from the header's buttons at
// 390 px), and the old project tabs (?tab=activity, decisions, board) open
// where their content moved. axe on both widths. The fixture project
// team-a/shop has %14 (paused, its problems) and %12 (running, one open
// proposal: fixtures/projectCockpit.ts). Run with npm run e2e (built UI,
// Chromium).
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { journeyAt } from './journey'
import { answer, now } from './fixtures'
import { checkoutWithProposal } from './fixtures/projectCockpit'
import { checkout as checkoutMilestone, payments as paymentsMilestone } from './fixtures/task'

const projectPath = '/projects/team-a/shop'

async function open(page: Page, baseURL: string | undefined, path: string, width: number): Promise<string[]> {
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
    const body = p === `${projectPath}/milestones/12` ? checkoutWithProposal : answer(p, url.searchParams)
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

const tabs = (page: Page) => page.locator('main [data-stream-head]').getByRole('navigation', { name: 'View' }).getByRole('link')
const pinnedMilestone = (page: Page, iid: number) => page.locator(`main [data-pinned-milestone="${iid}"]`)
const groups = (page: Page) => page.locator('main [data-cockpit-stream] [data-cockpit-group]')
const panel = (page: Page) => page.getByRole('complementary', { name: 'Project context' })
// The header's buttons by their data attribute: while the modal slide-over
// is open, the page behind it is hidden from role queries.
const headerButton = (page: Page, view: string) => page.locator(`main [data-stream-head] [data-panel-open="${view}"]`)

// axe (WCAG 2.2 AA); color-contrast is left out as on the milestone Cockpit
// (cockpit.spec.ts): the Desk-wide rule of #641. No known-a11y entry.
async function axe(page: Page, include: string) {
  const results = await new AxeBuilder({ page })
    .include(include)
    .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
    .disableRules(['color-contrast'])
    .analyze()
  return results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)
}

async function expectCockpit(page: Page) {
  // Cockpit · Milestones · Decisions · Metrics (Plans follows with the plans feature).
  for (const [n, label] of ['Cockpit', 'Milestones', 'Decisions', 'Metrics'].entries()) await expect(tabs(page).nth(n)).toHaveText(label)
  await expect(tabs(page).first()).toHaveAttribute('aria-current', 'page')

  // Both milestones' pinned cards, each under its milestone's name, in the
  // project's order, with a link to its Cockpit.
  await expect(page.locator('main [data-pinned-milestone]')).toHaveCount(2)
  const payments = pinnedMilestone(page, 14)
  const checkout = pinnedMilestone(page, 12)
  await expect(payments.getByRole('heading', { level: 2 }).first()).toHaveText(/^Payment providers/)
  await expect(checkout.getByRole('heading', { level: 2 }).first()).toHaveText('Checkout without account')
  await expect(payments.locator('[data-problem]').first()).toBeVisible()
  await expect(checkout.locator('[data-proposal="4"]')).toContainText('Split #278')
  await expect(checkout.locator('[data-pinned-cockpit]')).toHaveAttribute('href', `${projectPath}/milestones/12`)
  await expect(payments.locator('[data-pinned-cockpit]')).toHaveAttribute('href', `${projectPath}/milestones/14`)

  // One thread per milestone and issue, the newest on top: #277 of %12
  // (3 min ago), then the others with #281 of %14 below it; each folds its
  // runs into a group, closed by default.
  const threads = page.locator('main [data-cockpit-stream] [data-cockpit-thread]')
  // Each header shows its milestone's title (R4).
  await expect(threads.first()).toHaveAttribute('aria-label', new RegExp(`^#277 · .+ · ${checkoutMilestone.title}$`))
  await expect(threads.first().locator('[data-cockpit-thread-milestone]')).toHaveText(checkoutMilestone.title)
  await expect(page.locator(`main [data-cockpit-thread][aria-label="Milestone · ${paymentsMilestone.title}"]`)).toHaveCount(1)
  await expect(page.locator('main #issue-281[data-cockpit-thread] [data-cockpit-group]')).toHaveCount(1)
  await expect(page.locator('main #issue-281 [data-cockpit-thread-milestone]')).toHaveText(paymentsMilestone.title)
  await expect(groups(page)).toHaveCount(2)
  await expect(groups(page).nth(0)).toContainText('#277 ·')
  await expect(groups(page).nth(1)).toContainText('#281 ·')
  for (const n of [0, 1]) await expect(groups(page).nth(n).getByRole('button').first()).toHaveAttribute('aria-expanded', 'false')

  // No composer (D9); the running issue's live line links to its
  // milestone's Cockpit.
  await expect(page.locator('main [data-cockpit-composer]')).toHaveCount(0)
  await expect(page.locator('main [data-cockpit-live-line]').first()).toHaveAttribute('href', `${projectPath}/milestones/12`)
}

for (const width of [1280, 390]) {
  test(`a project opens on its Cockpit with both milestones' cards and groups, no composer@${width}`, journeyAt(width), async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, projectPath, width)
    await expectCockpit(page)

    // A group opens by keyboard and shows its event cards.
    const trigger = groups(page).nth(0).getByRole('button').first()
    await trigger.focus()
    await page.keyboard.press('Enter')
    await expect(trigger).toHaveAttribute('aria-expanded', 'true')
    await expect(groups(page).nth(0).locator('li').first()).toBeVisible()

    expect(await axe(page, 'main')).toEqual([])
    const fit = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(fit, 'the page does not scroll sideways').toBe(0)
    expect(problems).toEqual([])
  })
}

test('the panel is Queue beside the Cockpit, a history day pages the Cockpit@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, projectPath, 1280)
  await expect(panel(page)).toBeVisible()
  await expect(page.locator('main [data-panel-open]')).toHaveCount(0)
  await expect(panel(page).getByRole('tab'), 'one view, no switch; decisions are a tab').toHaveCount(0)
  await expect(panel(page).getByText('Last 30 days')).toBeVisible()
  await expect(panel(page).locator('[data-heat-day]')).toHaveCount(30)
  expect(await axe(page, '[data-project-panel]')).toEqual([])

  // A day pages the Cockpit back; Back to now returns.
  await panel(page).locator('[data-heat-day]').nth(20).click()
  await expect(page.locator('main [data-cockpit-jumped]')).toBeVisible()
  await expect(panel(page).locator('[data-heat-day]').nth(20)).toHaveAttribute('aria-pressed', 'true')
  await page.locator('main [data-cockpit-jumped]').getByRole('button', { name: 'Back to now' }).click()
  await expect(page.locator('main [data-cockpit-jumped]')).toHaveCount(0)

  expect(problems).toEqual([])
})

test('the Queue button opens the panel as a slide-over, Escape closes it and focus returns@390', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, projectPath, 390)
  await expect(panel(page), 'no column on a phone').toHaveCount(0)
  await expect(headerButton(page, 'decisions'), 'decisions are a tab').toHaveCount(0)
  const button = headerButton(page, 'queue')
  await expect(button).toHaveAttribute('aria-expanded', 'false')
  await button.click()
  const sheet = page.getByRole('dialog', { name: 'Queue' })
  await expect(sheet).toBeVisible()
  await sheet.evaluate((e) => Promise.all(e.getAnimations().map((a) => a.finished)))
  await expect(sheet.getByText('Last 30 days', { exact: true })).toBeVisible()
  expect(await axe(page, '[role="dialog"]')).toEqual([])

  await page.keyboard.press('Escape')
  await expect(sheet).toBeHidden()
  await expect(button).toBeFocused()
  expect(problems).toEqual([])
})

// The old project tabs load where their content moved, one link each.
for (const old of ['activity', 'stream']) {
  test(`?tab=${old} opens the Cockpit@1280`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${projectPath}?tab=${old}`, 1280)
    await expect(page).toHaveURL(new RegExp(`${projectPath}$`))
    await expect(tabs(page).first()).toHaveAttribute('aria-current', 'page')
    await expect(page.locator('main [data-cockpit]')).toBeVisible()
    expect(problems).toEqual([])
  })
}

// The old link to the panel's Decisions view opens the Decisions tab (R6 of
// "Desk: the Cockpit newest first by issue"), keeping ?milestone=; the
// tab's milestone select is in desk.spec.ts ("milestone decisions").
test('?panel=decisions opens the Decisions tab, keeping the milestone@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, `${projectPath}?milestone=14&panel=decisions`, 1280)
  await expect(page).toHaveURL(new RegExp(`${projectPath}\\?milestone=14&tab=decisions$`))
  await expect(tabs(page).filter({ hasText: 'Decisions' })).toHaveAttribute('aria-current', 'page')
  await expect(page.locator('main [data-decisions-milestone]')).toContainText('Payment providers')
  expect(problems).toEqual([])
})

// Board: the Milestones tab's Board view; the switch goes back to the lists.
test('?tab=board opens the Board view of Milestones@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, `${projectPath}?tab=board`, 1280)
  await expect(page).toHaveURL(new RegExp(`${projectPath}\\?tab=milestones&view=board$`))
  const views = page.locator('main [data-milestones-view-switch]')
  await expect(views.getByRole('tab', { name: 'Board', exact: true })).toHaveAttribute('aria-selected', 'true')
  // One board per milestone (%14, %12) and one for the issues without a milestone.
  await expect(page.locator('main [data-board-milestone]')).toHaveCount(3)
  await views.getByRole('tab', { name: 'Milestones', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`${projectPath}\\?tab=milestones$`))
  await expect(page.locator('main [data-board-milestone]')).toHaveCount(0)
  expect(problems).toEqual([])
})
