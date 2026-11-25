// The Overview Cockpit (R16 of "Desk: the Cockpit", #791; R10 of "Desk: the
// Cockpit newest first by issue", #823): the Overview at / opens on a
// Cockpit across all projects. On top the Inbox's open items of every
// project, pinned as the Inbox's rows, each a link to its card in the
// milestone's Cockpit (nothing is answered here, D6); then one thread per
// project, the newest on top, with merges and milestone state shown and the
// rest folded; no composer and no live lines. The sidebar is Now & next per
// project, then the summary and spend tiles and the engine meters: a column
// at 1280 px that collapses to a rail, a slide-over from the Now strip under
// the header at 390 px. The old ?tab=activity, stream and overview open the
// Cockpit; Timeline is a tab. The fixture Inbox is team-a/shop's; this spec
// adds a proposal of team-b's project, so the pinned items come from two
// projects. Run with npm run e2e (built UI, Chromium).
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page } from '@playwright/test'
import { journeyAt } from './journey'
import { answer, now } from './fixtures'
import { ago } from './fixtures/clock'
import { inbox } from './fixtures/rail'

const teamB = 'a-project-with-a-rather-long-name.example'
const twoProjects = {
  ...inbox,
  items: [
    ...inbox.items,
    {
      namespace: 'team-b',
      project: teamB,
      kind: 'proposal',
      proposalID: 2,
      milestoneIID: 3,
      title: 'Add a health check to the setup',
      reason: 'proposal',
      at: ago(30),
    },
  ],
}

const teamBMerge = {
  id: 9001,
  at: ago(1),
  namespace: 'team-b',
  project: teamB,
  milestoneIID: 3,
  issueIID: 4,
  kind: 'merged',
  summary: 'Merged !12 Health check',
  data: {},
}

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
    let body = p === '/inbox' ? twoProjects : answer(p, url.searchParams)
    // The stream's newest page gets team-b's merge a minute ago, so its
    // thread is on top of team-a/shop's.
    if (p === '/stream' && !url.searchParams.has('after') && !url.searchParams.has('before')) {
      const page = body as { events: unknown[] }
      body = { ...page, events: [teamBMerge, ...page.events] }
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

const tabs = (page: Page) => page.locator('main').getByRole('navigation', { name: 'View' }).getByRole('link')
const pinned = (page: Page) => page.locator('main [data-cockpit-pinned] li[data-inbox-kind]')
const groups = (page: Page) => page.locator('main [data-cockpit-stream] [data-cockpit-group]')
// The column: the panel outside a slide-over.
const column = (page: Page) => page.locator('main [data-overview-panel]')
// The strip by its data attribute: while the modal slide-over is open, the
// page behind it is hidden from role queries.
const strip = (page: Page) => page.locator('main [data-now-strip]')

// axe (WCAG 2.2 AA); color-contrast is left out as on the other Cockpits
// (project-cockpit.spec.ts): the Desk-wide rule of #641. No known-a11y entry.
async function axe(page: Page, include: string) {
  const results = await new AxeBuilder({ page })
    .include(include)
    .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
    .disableRules(['color-contrast'])
    .analyze()
  return results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)
}

async function expectCockpit(page: Page) {
  await expect(tabs(page)).toHaveText(['Cockpit', 'Timeline'])
  await expect(tabs(page).first()).toHaveAttribute('aria-current', 'page')

  // Pinned: every open Inbox item, of both projects, each a link.
  await expect(pinned(page)).toHaveCount(twoProjects.items.length)
  await expect(pinned(page).filter({ hasText: 'shop' }).first()).toBeVisible()
  const b = pinned(page).filter({ hasText: 'Add a health check to the setup' })
  await expect(b).toContainText(teamB)
  await expect(b.getByRole('link')).toHaveAttribute('href', `/projects/team-b/${teamB}/milestones/3#pinned`)
  // Nothing is answered here: no answer form, no proposal buttons.
  await expect(page.locator('main [data-cockpit-pinned] form, main [data-cockpit-pinned] textarea')).toHaveCount(0)

  // One thread per project, the newest on top: team-b's, then shop's.
  const threads = page.locator('main [data-cockpit-stream] [data-cockpit-thread]')
  await expect(threads).toHaveCount(2)
  await expect(threads.nth(0)).toHaveAttribute('aria-label', teamB)
  await expect(threads.nth(0)).toContainText('Merged !12 Health check')
  await expect(threads.nth(1)).toHaveAttribute('aria-label', 'shop')
  // The merges are shown, the rest is folded per project, closed.
  await expect(threads.nth(1).getByText('Merged !298 Guest checkout: address form').first()).toBeVisible()
  await expect(groups(page).first()).toBeVisible()
  for (const g of await groups(page).all()) {
    await expect(g).toContainText('shop ·')
    await expect(g.getByRole('button').first()).toHaveAttribute('aria-expanded', 'false')
  }

  // No live lines in the stream (Now & next has what runs), no composer.
  await expect(page.locator('main [data-cockpit] [data-cockpit-live]')).toHaveCount(0)
  await expect(page.locator('main [data-cockpit-composer], main textarea')).toHaveCount(0)
}

for (const width of [1280, 390]) {
  test(`the Overview opens on a Cockpit with the Inbox items of two projects pinned@${width}`, journeyAt(width), async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, '/', width)
    await expectCockpit(page)

    // A group opens by keyboard.
    const trigger = groups(page).first().getByRole('button').first()
    await trigger.focus()
    await page.keyboard.press('Enter')
    await expect(trigger).toHaveAttribute('aria-expanded', 'true')

    // axe on the whole page at once: focusing the trigger scrolled the
    // window, and on a phone a row half under the sticky mobile bar (or
    // the bottom bar) would count as an obscured target. A viewport as tall
    // as the page, scrolled to the top, leaves nothing under the bars.
    const height = await page.evaluate(() => document.documentElement.scrollHeight)
    await page.setViewportSize({ width, height: Math.max(800, height) })
    await page.evaluate(() => window.scrollTo(0, 0))
    expect(await axe(page, 'main')).toEqual([])
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow, 'no sideways scrolling').toBe(0)
    expect(problems).toEqual([])
  })
}

// The sidebar (R10 of "Desk: the Cockpit newest first by issue"): Now &
// next per project, then the summary tiles and the engine meters. shop runs
// #277 and has #279 held and #280 waiting for CI; team-b's project has its
// next issue only.
async function expectSidebar(panel: Locator) {
  const projects = panel.locator('[data-overview-project]')
  await expect(projects).toHaveCount(2)
  const shop = panel.locator('[data-overview-project="team-a/shop"]')
  await expect(shop.getByRole('heading', { name: /^shop/ })).toBeVisible()
  await expect(shop.locator('[data-overview-finishing]')).toHaveText('2 finishing')
  const running = shop.locator('[data-now-section="running"] [data-now-row]')
  await expect(running).toHaveCount(1)
  await expect(running).toContainText('#277 implementing · 4 min')
  // A Running row opens the issue's thread in its milestone's Cockpit.
  await expect(running).toHaveAttribute('href', '/projects/team-a/shop/milestones/12#issue-277')
  await expect(shop.locator('[data-now-section="waiting"] [data-now-row]')).toHaveText([
    /#279 held: CI failed 3 times/,
    /#280 waiting for CI/,
  ])
  await expect(shop.locator('[data-now-section="next"] [data-now-row]')).toContainText('#278')
  await expect(shop.locator('[data-now-milestone]')).toHaveCount(2)
  await expect(shop.locator('[data-now-milestone="14"] a')).toHaveAttribute('href', '/projects/team-a/shop/milestones/14')
  const other = panel.locator(`[data-overview-project="team-b/${teamB}"]`)
  await expect(other.locator('[data-now-section="running"]')).toHaveCount(0)
  await expect(other.locator('[data-now-section="next"]')).toContainText('#3')
  // Now & next above the tiles, the tiles above the engine meters.
  await expect(panel.locator('[data-overview-tiles] a[href="/inbox"]')).toBeVisible()
  await expect(panel.locator('[data-overview-tiles] [data-chip-costs]')).toHaveCount(2)
  await expect(panel.locator('[data-overview-engine]')).toHaveCount(1)
  const tops = await Promise.all(
    [projects.last(), panel.locator('[data-overview-tiles]'), panel.locator('[data-overview-engines]')].map(async (l) => (await l.boundingBox())!.y),
  )
  expect(tops, 'Now & next, tiles, engine meters').toEqual([...tops].sort((a, b) => a - b))
}

test('the sidebar is Now & next per project above the tiles and engine meters, beside the Cockpit@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, '/', 1280)
  await expect(column(page)).toBeVisible()
  await expect(strip(page), 'no strip beside a column').toHaveCount(0)
  await expectSidebar(column(page))
  expect(await axe(page, '[data-overview-panel]')).toEqual([])
  expect(problems).toEqual([])
})

test('the sidebar collapses to a rail with the counts, which stays after a reload@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, '/', 1280)
  await page.locator('main').getByRole('button', { name: 'Collapse Now & next' }).click()
  const rail = page.locator('main [data-sidebar-rail]')
  await expect(rail).toBeVisible()
  await expect(column(page)).toHaveCount(0)
  await expect(rail.getByRole('button', { name: 'Expand Now & next' })).toBeFocused()
  await expect(rail.getByRole('list', { name: 'Now & next counts' }).getByRole('listitem')).toHaveText([/1\s*1 running/, /2\s*2 waiting/, /2\s*2 next/])
  expect(await axe(page, '[data-sidebar-rail]')).toEqual([])

  await page.reload()
  await expect(page.locator('main [data-sidebar-rail]'), 'still collapsed').toBeVisible()
  await page.locator('main [data-sidebar-rail]').getByRole('button', { name: 'Expand Now & next' }).click()
  await expect(column(page)).toBeVisible()
  await expect(page.locator('main').getByRole('button', { name: 'Collapse Now & next' })).toBeFocused()
  expect(problems).toEqual([])
})

test('the Now strip opens the sidebar as a slide-over, Escape closes it and focus returns@390', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, '/', 390)
  await expect(pinned(page).first()).toBeVisible()
  await expect(column(page), 'no column on a phone').toHaveCount(0)
  await expect(page.locator('main [data-sidebar-rail], main [data-rail-collapse]'), 'no rail on a phone').toHaveCount(0)
  const button = strip(page)
  await expect(button).toHaveText('1 running · 2 waiting · next #278')
  await expect(button).toHaveAttribute('aria-expanded', 'false')
  await button.click()
  const sheet = page.getByRole('dialog', { name: 'Now & next' })
  await expect(sheet).toBeVisible()
  await sheet.evaluate((e) => Promise.all(e.getAnimations().map((a) => a.finished)))
  await expectSidebar(sheet)
  expect(await axe(page, '[role="dialog"]')).toEqual([])

  await page.keyboard.press('Escape')
  await expect(sheet).toBeHidden()
  await expect(button).toBeFocused()
  expect(problems).toEqual([])
})

test('a pinned item opens its card in the milestone Cockpit@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, '/', 1280)
  await pinned(page).filter({ hasText: 'Support refunds through the payment provider' }).getByRole('link').first().click()
  // #prompt-p-281-1 opens the prompt's problem thread among the pinned cards.
  await expect(page).toHaveURL(/\/projects\/team-a\/shop\/milestones\/14\/problems\/issue-281$/)
  await expect(page.getByRole('navigation', { name: 'View' }).getByRole('link', { name: 'Cockpit' })).toHaveAttribute('aria-current', 'page')
  await expect(page.locator('[data-cockpit-pinned] [data-needs-problems] a[aria-current="page"]')).toBeInViewport()
  expect(problems).toEqual([])
})

// The old dashboard tabs open the Cockpit, and the URL drops them.
for (const old of ['activity', 'stream', 'overview']) {
  test(`?tab=${old} opens the Cockpit@1280`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `/?tab=${old}`, 1280)
    await expect(page).toHaveURL(/:\d+\/$/)
    await expect(tabs(page).first()).toHaveAttribute('aria-current', 'page')
    await expect(page.locator('main [data-cockpit]')).toBeVisible()
    expect(problems).toEqual([])
  })
}

test('Timeline is a tab: the Timeline across projects and the milestone lists@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, '/', 1280)
  await tabs(page).nth(1).click()
  await expect(page).toHaveURL(/\/\?tab=timeline$/)
  await expect(page.locator('main [data-overview-timeline]')).toBeVisible()
  await expect(page.locator('main ul[aria-label="In progress"]')).toBeVisible()
  await expect(page.locator('main [data-cockpit]')).toHaveCount(0)
  expect(problems).toEqual([])
})
