// The Overview's Projects tab (R5 of "Desk: Milestones and Issues first, a
// Timeline tab, and what each project builds", D3 and D4): / opens on one
// card per project that says what it builds now, what comes next and
// whether it needs you; projects that need you first, then running ones,
// then the rest with a queue; idle and finishing-only projects in one line
// below, then Recently done across projects. Fixtures in
// e2e/fixtures/projectCards.ts. Run with npm run e2e (built UI, Chromium).
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { journeyAt } from './journey'
import { answer, now } from './fixtures'
import { cardInbox, cardMilestones, cardOverview, cardProjects, shopNeeds } from './fixtures/projectCards'

// open loads path at width with the fixtures; projects replaces the
// answer of GET /projects (status and body).
async function open(
  page: Page,
  baseURL: string | undefined,
  path: string,
  width: number,
  projects: { status: number; json: unknown } = { status: 200, json: cardProjects },
): Promise<string[]> {
  const origin = new URL(baseURL ?? '').origin
  const problems: string[] = []
  await page.setViewportSize({ width, height: 900 })
  await page.clock.install({ time: now })
  page.on('pageerror', (err) => problems.push(err.message))
  await page.route((url) => url.protocol !== 'data:' && url.protocol !== 'blob:' && url.origin !== origin, (r) => r.abort('blockedbyclient'))
  await page.route('**/api/v1/**', async (r) => {
    const req = r.request()
    const url = new URL(req.url())
    const p = url.pathname.slice('/api/v1'.length)
    if (p === '/events') return r.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 3600000\n\n' })
    if (req.method() !== 'GET') {
      problems.push(`${req.method()} ${url.pathname}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    if (p === '/projects') return r.fulfill(projects)
    let body: unknown
    if (p === '/milestones' && url.searchParams.get('state') === 'open') body = cardMilestones
    else if (p === '/overview') body = cardOverview
    else if (p === '/inbox') body = cardInbox
    else body = answer(p, url.searchParams)
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
const cards = (page: Page) => page.locator('main [data-project-card]')
const shop = (page: Page) => page.locator('main [data-project-card="team-a/shop"]')

// axe (WCAG 2.2 AA) on the tab; color-contrast is left out as on the other
// Overview tabs (overview-cockpit.spec.ts): the Desk-wide rule of #641.
async function axe(page: Page) {
  const results = await new AxeBuilder({ page })
    .include('main')
    .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
    .disableRules(['color-contrast'])
    .analyze()
  return results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)
}

for (const width of [1280, 390]) {
  test(`the Overview opens on Projects: what each project builds, what comes next, who needs you@${width}`, journeyAt(width), async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, '/', width)
    await expect(tabs(page)).toHaveText(['Projects', 'Timeline', 'Cockpit'])
    await expect(tabs(page).first()).toHaveAttribute('aria-current', 'page')
    await expect(page).toHaveURL(/:\d+\/$/)

    // Needs you first, then the one with a queue; idle ones fold into a line.
    await expect(cards(page)).toHaveCount(2)
    await expect(cards(page).nth(0)).toHaveAttribute('data-project-card', 'team-a/shop')
    await expect(cards(page).nth(1)).toHaveAttribute('data-project-card', 'team-b/api')
    await expect(cards(page).nth(1).getByText('Suspended')).toBeVisible()
    await expect(cards(page).nth(1).locator('[data-card-building]')).toContainText('Nothing running')

    // Header: the project's Milestones, and its needs-you count into its Cockpit.
    const card = shop(page)
    await expect(card.locator('[data-card-project]')).toHaveText('shop')
    await expect(card.locator('[data-card-project]')).toHaveAttribute('href', '/projects/team-a/shop')
    const needs = card.locator('[data-card-needs-you]')
    await expect(needs).toHaveText(`${shopNeeds} need${shopNeeds === 1 ? 's' : ''} you`)
    await expect(needs).toHaveAttribute('href', '/projects/team-a/shop?tab=cockpit')

    // Building now: %12 running, 3 of 7 merged, and both running issues.
    const building = card.locator('[data-card-milestone="12"]')
    await expect(building.getByRole('link', { name: 'Checkout without account' })).toHaveAttribute('href', '/projects/team-a/shop/milestones/12')
    await expect(building.getByText('Running', { exact: true })).toBeVisible()
    await expect(building.getByRole('progressbar', { name: '3 of 7 merged' })).toBeVisible()
    const issues = building.locator('[data-card-issue]')
    await expect(issues).toHaveCount(2)
    await expect(issues.nth(0)).toHaveText(/^#277 Guest checkout: keep the cart after the address step · 3 run implementer · 4 min/)
    await expect(issues.nth(0)).toHaveAttribute('href', '/projects/team-a/shop/milestones/12?tab=cockpit#issue-277')
    await expect(issues.nth(1)).toHaveText(/^#283 Guest checkout: confirmation mail · 2 review · 9 min/)
    await expect(issues.nth(1)).toHaveAttribute('href', '/projects/team-a/shop/milestones/12?tab=cockpit#issue-283')

    // Up next: three in queue order, numbered by place, then "1 more".
    const next = card.locator('[data-card-next] li')
    await expect(next).toHaveText(['2. Wish lists', '3. Coupons', '4. Order history export'])
    await expect(next.first().getByRole('link')).toHaveAttribute('href', '/projects/team-a/shop/milestones/15')
    await expect(card.locator('[data-card-more]')).toHaveText('1 more')
    await expect(card.locator('[data-card-more]')).toHaveAttribute('href', '/projects/team-a/shop')

    // Footer: the finishing and planned counts, into the Milestones tab.
    const footer = card.locator('[data-card-footer]').getByRole('link')
    await expect(footer).toHaveText(['2 finishing', '1 planned without autopilot'])
    for (const l of await footer.all()) await expect(l).toHaveAttribute('href', '/projects/team-a/shop')

    // Idle: finishing-only and empty projects, each a link.
    const idle = page.locator('main [data-projects-idle]')
    await expect(idle).toHaveText(/^Idle:\s*blog,\s*docs$/)
    await expect(idle.getByRole('link', { name: 'blog' })).toHaveAttribute('href', '/projects/team-c/blog')
    await expect(idle.getByRole('link', { name: 'docs' })).toHaveAttribute('href', '/projects/team-a/docs')

    // Recently done across projects, below the cards; In progress is not here.
    await expect(page.locator('main ul[aria-label="Recently done"]')).toContainText('Search')
    await expect(page.locator('main').getByRole('heading', { name: 'In progress' })).toHaveCount(0)
    const below = await Promise.all([idle, page.locator('main ul[aria-label="Recently done"]')].map(async (l) => (await l.boundingBox())!.y))
    expect(below[1], 'Recently done below the idle line').toBeGreaterThan(below[0]!)

    // A viewport as tall as the page, scrolled to the top, leaves nothing
    // under the sticky bars for axe's target checks.
    const height = await page.evaluate(() => document.documentElement.scrollHeight)
    await page.setViewportSize({ width, height: Math.max(900, height) })
    await page.evaluate(() => window.scrollTo(0, 0))
    expect(await axe(page)).toEqual([])
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow, 'no sideways scrolling').toBe(0)
    expect(problems).toEqual([])
  })
}

test('on a phone the cards are one column and every link is at least 44 px high@390', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, '/', 390)
  await expect(cards(page)).toHaveCount(2)
  const [a, b] = await Promise.all([cards(page).nth(0).boundingBox(), cards(page).nth(1).boundingBox()])
  expect(Math.round(a!.x), 'one column').toBe(Math.round(b!.x))
  expect(b!.y).toBeGreaterThan(a!.y)
  const small = await page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>('main [data-project-cards] a, main [data-projects-idle] a')]
      .filter((e) => e.getBoundingClientRect().height < 44)
      .map((e) => `${e.textContent?.trim()} ${Math.round(e.getBoundingClientRect().height)}px`),
  )
  expect(small).toEqual([])
  expect(problems).toEqual([])
})

test('the issue link opens its thread in the milestone Cockpit@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, '/', 1280)
  await shop(page).locator('[data-card-issue="277"]').click()
  await expect(page).toHaveURL(/\/projects\/team-a\/shop\/milestones\/12\?tab=cockpit#issue-277$/)
  await expect(page.getByRole('navigation', { name: 'View' }).getByRole('link', { name: /^Cockpit/ })).toHaveAttribute('aria-current', 'page')
  expect(problems).toEqual([])
})

test('the project name opens its Milestones tab@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, '/', 1280)
  await shop(page).locator('[data-card-project]').click()
  await expect(page).toHaveURL(/\/projects\/team-a\/shop$/)
  await expect(page.getByRole('navigation', { name: 'View' }).getByRole('link', { name: 'Milestones' })).toHaveAttribute('aria-current', 'page')
  expect(problems).toEqual([])
})

test('without any project the tab says so@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, '/', 1280, { status: 200, json: [] })
  await expect(page.locator('main').getByText('No project yet.')).toBeVisible()
  await expect(cards(page)).toHaveCount(0)
  expect(problems).toEqual([])
})

test('a load error shows in the tab@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, '/', 1280, { status: 500, json: { error: 'database is down' } })
  await expect(page.locator('main [data-project-cards-error]')).toContainText('The server answered 500.')
  await expect(cards(page)).toHaveCount(0)
  expect(problems).toEqual([])
})
