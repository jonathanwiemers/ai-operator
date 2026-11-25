// The project Cockpit (R15 of "Desk: the Cockpit", #790; R9 of "Desk: the
// Cockpit newest first by issue", #822): the project page opens on a
// Cockpit across its milestones. On top the pinned cards of every milestone
// with open needs, each under its milestone's name with a link to that
// milestone's Cockpit; below them the threads per milestone and issue,
// newest on top; no composer and no live lines. The sidebar is Now & next
// across the project (a column at 1280 px, a slide-over from the Now strip
// at 390 px): Running and Waiting grouped by milestone, each row a link to
// the issue's thread in its milestone's Cockpit, Up next of the milestone
// that runs first, the project queue, and a History section, closed by
// default, whose day pages the Cockpit back. The old project links
// (?tab=activity, decisions, board; ?panel=queue, decisions) open where
// their content moved. axe on both widths. The fixture project team-a/shop
// has %14 (paused, its problems) and %12 (running, one open proposal:
// fixtures/projectCockpit.ts); withRunning (below) makes #282 of %14 run its
// implementer and holds #281. Run with npm run e2e (built UI, Chromium).
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { journeyAt } from './journey'
import { answer, now } from './fixtures'
import { checkoutWithProposal } from './fixtures/projectCockpit'
import { captures, checkout as checkoutMilestone, payments as paymentsMilestone } from './fixtures/task'
import type { ProjectDetail } from '../src/generated/views'

// withRunning: #282 of %14 runs its implementer (captures, 4 min) and the
// autopilot holds #281, so Running has a group per milestone (#277 of %12
// runs too) and Waiting one.
function withRunning(p: ProjectDetail): ProjectDetail {
  const hold = { kind: 'ciRed', reason: 'CI failed 3 times', since: now.toISOString() }
  return {
    ...p,
    milestones: p.milestones.map((m) => {
      if (m.iid !== 14) return m
      const spec = m.spec!
      const items = [{ iid: 281, state: 'Active' as const, hold }, { iid: 282, state: 'Active' as const }]
      return {
        ...m,
        tasks: m.tasks.map((t) => (t.issue?.iid === 282 ? captures : t)),
        spec: { ...spec, status: { ...spec.status, autopilot: { ...spec.status.autopilot!, items } } },
      }
    }),
  }
}

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
    let body = p === `${projectPath}/milestones/12` ? checkoutWithProposal : answer(p, url.searchParams)
    if (p === projectPath) body = withRunning(body as ProjectDetail)
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
const panel = (page: Page) => page.getByRole('complementary', { name: 'Now & next' })
// The Now strip by its data attribute: while the modal slide-over is open,
// the page behind it is hidden from role queries.
const strip = (page: Page) => page.locator('main [data-stream-head] [data-now-strip]')

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

  // No composer (D9) and no live lines: what runs is in the sidebar.
  await expect(page.locator('main [data-cockpit-composer]')).toHaveCount(0)
  await expect(page.locator('main [data-cockpit-live]')).toHaveCount(0)
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

    // On a phone the fixed bottom bar covers what scrolled under it, which
    // axe's target-size counts as obscured: the viewport takes the whole
    // page, so nothing lies under the bar.
    if (width < 760) {
      const height = await page.evaluate(() => document.documentElement.scrollHeight)
      await page.setViewportSize({ width, height: height + 200 })
    }
    expect(await axe(page, 'main')).toEqual([])
    const fit = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(fit, 'the page does not scroll sideways').toBe(0)
    expect(problems).toEqual([])
  })
}

// What the sidebar shows with withRunning, wide or in the slide-over.
async function expectNowNext(scope: ReturnType<Page['locator']>) {
  // Running grouped by milestone, in the project's order; each row opens
  // the issue's thread in its milestone's Cockpit, a live run with its
  // transcript (the transcript grant); #277 has no run request to show.
  const running = scope.locator('[data-now-section="running"]')
  await expect(running.locator('h4')).toHaveText([paymentsMilestone.title, checkoutMilestone.title])
  await expect(running.locator('[data-now-row="282"]')).toContainText('#282 implementing · 4 min')
  await expect(running.locator('[data-now-row="282"]')).toHaveAttribute('href', `${projectPath}/milestones/14?live=1#issue-282`)
  await expect(running.locator('[data-now-row="277"]')).toHaveAttribute('href', `${projectPath}/milestones/12#issue-277`)
  const waiting = scope.locator('[data-now-section="waiting"]')
  await expect(waiting.locator('h4')).toHaveText([paymentsMilestone.title])
  await expect(waiting.locator('[data-now-row="281"]')).toContainText('#281 held: CI failed 3 times')
  // Up next of %12, the milestone that runs first; Milestones is the queue.
  await expect(scope.locator('[data-now-section="next"] [data-now-row]')).toHaveText([/^#278 waiting for a free slot/])
  await expect(scope.locator('[data-now-milestone]')).toHaveCount(3)
  await expect(scope.locator('[data-now-milestone]').first()).toContainText('running')
  await expect(scope.locator('[data-now-milestone="12"] a')).toHaveAttribute('href', `${projectPath}/milestones/12`)
}

test('the sidebar is Now & next across the milestones, History pages the Cockpit, it collapses to a rail@1280', journeyAt(1280), async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, projectPath, 1280)
  await expect(panel(page)).toBeVisible()
  await expect(page.locator('main [data-panel-open]'), 'no strip beside the column').toHaveCount(0)
  await expectNowNext(panel(page))

  // History is closed by default; opened, a day pages the Cockpit back and
  // Back to now returns.
  const toggle = panel(page).locator('[data-project-history-toggle]')
  await expect(toggle).toHaveAttribute('aria-expanded', 'false')
  await expect(panel(page).locator('[data-heat-day]')).toHaveCount(0)
  await toggle.click()
  await expect(panel(page).getByText('Last 30 days')).toBeVisible()
  await expect(panel(page).locator('[data-heat-day]')).toHaveCount(30)
  expect(await axe(page, '[data-project-panel]')).toEqual([])
  await panel(page).locator('[data-heat-day]').nth(20).click()
  await expect(page.locator('main [data-cockpit-jumped]')).toContainText('Events up to the end of')
  await expect(panel(page).locator('[data-heat-day]').nth(20)).toHaveAttribute('aria-pressed', 'true')
  await page.locator('main [data-cockpit-jumped]').getByRole('button', { name: 'Back to now' }).click()
  await expect(page.locator('main [data-cockpit-jumped]')).toHaveCount(0)

  // The rail with the counts, as on a milestone.
  await page.locator('[data-rail-collapse]').click()
  await expect(page.locator('[data-now-rail] [data-now-count="running"]')).toContainText('2 running')
  await expect(page.locator('[data-now-rail] [data-now-count="waiting"]')).toContainText('1 waiting')
  expect(await axe(page, '[data-sidebar-rail]')).toEqual([])
  await page.locator('[data-rail-expand]').click()
  await expect(panel(page)).toBeVisible()
  expect(problems).toEqual([])
})

test('the Now strip opens the sidebar as a slide-over, Escape closes it and focus returns@390', journeyAt(390), async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, projectPath, 390)
  await expect(panel(page), 'no column on a phone').toHaveCount(0)
  await expect(strip(page)).toHaveText('2 running · 1 waiting · next #278')
  await expect(strip(page)).toHaveAttribute('aria-expanded', 'false')
  await strip(page).click()
  const sheet = page.getByRole('dialog', { name: 'Now & next' })
  await expect(sheet).toBeVisible()
  await sheet.evaluate((e) => Promise.all(e.getAnimations().map((a) => a.finished)))
  await expectNowNext(sheet)
  await sheet.locator('[data-project-history-toggle]').click()
  await expect(sheet.getByText('Last 30 days', { exact: true })).toBeVisible()
  expect(await axe(page, '[role="dialog"]')).toEqual([])

  await page.keyboard.press('Escape')
  await expect(sheet).toBeHidden()
  await expect(strip(page)).toBeFocused()
  expect(problems).toEqual([])
})

// The old link to the panel's Queue view opens the sidebar (as on a
// milestone page): a slide-over on a phone.
test('?panel=queue opens the sidebar@390', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, `${projectPath}?panel=queue`, 390)
  await expect(page).toHaveURL(new RegExp(`${projectPath}$`))
  await expect(page.getByRole('dialog', { name: 'Now & next' })).toBeVisible()
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
