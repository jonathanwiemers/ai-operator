// The Overview Cockpit (R16 of "Desk: the Cockpit", #791): the Overview at /
// opens on a Cockpit across all projects. On top the Inbox's open items of
// every project, pinned as the Inbox's rows, each a link to its card in the
// milestone's Cockpit (nothing is answered here, D6); below the stream with
// merges and milestone state shown and the rest folded per project; the
// live lines of Working now; no composer. The panel is Working now with the
// summary and spend tiles (a column at 1280 px, a slide-over from the
// header's Working now button at 390 px). The old ?tab=activity, stream and
// overview open the Cockpit; Timeline is a tab. The fixture Inbox is
// team-a/shop's; this spec adds a proposal of team-b's project, so the pinned
// items come from two projects. Run with npm run e2e (built UI, Chromium).
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
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
    const body = p === '/inbox' ? twoProjects : answer(p, url.searchParams)
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
const column = (page: Page) => page.getByRole('complementary', { name: 'Working now' })
// The header's button by its data attribute: while the modal slide-over is
// open, the page behind it is hidden from role queries.
const headerButton = (page: Page) => page.locator('main [data-panel-open="working"]')

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

  // The stream: the merge is shown, the rest is folded per project, closed.
  await expect(page.locator('main [data-cockpit-stream]').getByText('Merged !298 Guest checkout: address form').first()).toBeVisible()
  await expect(groups(page).first()).toBeVisible()
  for (const g of await groups(page).all()) {
    await expect(g).toContainText('shop ·')
    await expect(g.getByRole('button').first()).toHaveAttribute('aria-expanded', 'false')
  }

  // The live line of the running issue links to its milestone's Cockpit; no composer.
  const line = page.locator('main [data-cockpit-live-line]')
  await expect(line).toHaveCount(1)
  await expect(line).toContainText('shop #277')
  await expect(line).toHaveAttribute('href', '/projects/team-a/shop/milestones/12')
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

    expect(await axe(page, 'main')).toEqual([])
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow, 'no sideways scrolling').toBe(0)
    expect(problems).toEqual([])
  })
}

test('the panel is Working now with the tiles, beside the Cockpit@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, '/', 1280)
  await expect(column(page)).toBeVisible()
  await expect(headerButton(page), 'no panel button beside a column').toHaveCount(0)
  await expect(column(page).getByRole('heading', { name: 'Working now' })).toBeVisible()
  await expect(column(page).locator('[data-overview-working] article')).toHaveCount(2)
  await expect(column(page).locator('[data-overview-tiles] a[href="/inbox"]')).toBeVisible()
  await expect(column(page).locator('[data-overview-tiles] [data-chip-costs]')).toHaveCount(2)
  expect(await axe(page, '[data-overview-panel]')).toEqual([])
  expect(problems).toEqual([])
})

test('the Working now button opens the panel as a slide-over, Escape closes it and focus returns@390', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, '/', 390)
  await expect(pinned(page).first()).toBeVisible()
  await expect(column(page), 'no column on a phone').toHaveCount(0)
  const button = headerButton(page)
  await expect(button).toHaveAttribute('aria-expanded', 'false')
  await button.click()
  const sheet = page.getByRole('dialog', { name: 'Working now' })
  await expect(sheet).toBeVisible()
  await sheet.evaluate((e) => Promise.all(e.getAnimations().map((a) => a.finished)))
  await expect(sheet.locator('[data-overview-working] article')).toHaveCount(2)
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
