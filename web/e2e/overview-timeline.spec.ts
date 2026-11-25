// The Overview's Timeline across projects (its Timeline tab, ?tab=timeline;
// OverviewTimeline.vue, #667): one row per project, bars coloured by
// milestone with a legend, overlapping bars on separate lines, a
// 6 h / 24 h / 7 d switch kept in ?window=, and the info alert without the
// research database. The fixture (fixtures/timeline.ts) has team-a/shop's
// #11, #12, #13 of %14 and #20 of %15, and team-b's #7 (today) and #4
// (three days ago) of %3.
// Run with npm run e2e (built UI, Chromium).
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { answer, now } from './fixtures'
import { overview } from './fixtures/overview'

const longProject = 'a-project-with-a-rather-long-name.example'

interface Opts {
  history?: boolean
  width?: number
  colorScheme?: 'light' | 'dark'
  query?: string
}

async function open(page: Page, baseURL: string | undefined, opts: Opts = {}) {
  const problems: string[] = []
  const windows: string[] = []
  const origin = new URL(baseURL ?? '').origin
  await page.setViewportSize({ width: opts.width ?? 1280, height: 900 })
  await page.emulateMedia({ colorScheme: opts.colorScheme ?? 'light' })
  await page.clock.setFixedTime(now)
  page.on('pageerror', (err) => problems.push(err.message))
  await page.route((url) => url.protocol !== 'data:' && url.protocol !== 'blob:' && url.origin !== origin, (r) => r.abort('blockedbyclient'))
  await page.route('**/api/v1/**', async (r) => {
    const url = new URL(r.request().url())
    const p = url.pathname.slice('/api/v1'.length)
    if (p === '/events') return r.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 3600000\n\n' })
    if (r.request().method() === 'POST' && p.endsWith('/warm')) return r.fulfill({ status: 204 })
    if (p === '/timeline') windows.push(`${url.searchParams.get('from')}..${url.searchParams.get('to')}`)
    let body = answer(p, url.searchParams)
    if (p === '/overview' && opts.history === false) body = { ...overview, history: false }
    if (body === undefined) {
      problems.push(`${r.request().method()} ${url.pathname}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    if (typeof body === 'string') return r.fulfill({ status: 200, contentType: 'text/plain; charset=utf-8', body })
    return r.fulfill({ status: 200, json: body })
  })
  // The Timeline is the Overview's second tab (R16 of "Desk: the Cockpit").
  await page.goto(`/?tab=timeline${(opts.query ?? '').replace(/^\?/, '&')}`)
  await expect(page.getByText('Checkout without account').first()).toBeVisible()
  return { problems, windows }
}

const section = (page: Page) => page.locator('main [data-overview-timeline]')
const rows = (page: Page) => section(page).locator('[data-timeline-row]')
const bar = (page: Page, project: string, ms: number, iid: number) =>
  section(page).locator(`a[href="/projects/${project}/milestones/${ms}/issues/${iid}"]`)

test('one row per project, bars on lines that do not overlap, legend and colours@1280', async ({ page, baseURL }) => {
  const { problems } = await open(page, baseURL)
  await expect(rows(page)).toHaveCount(2)
  const links = rows(page).locator('[data-timeline-row-link]')
  await expect(links).toHaveText(['team-a/shop', `team-b/${longProject}`])
  await expect(links.first()).toHaveAttribute('href', '/projects/team-a/shop')

  // #11 and #12 overlap, so shop has more than one line; no two bars on
  // one line overlap.
  const shopLines = rows(page).first().locator('ul')
  expect(await shopLines.count()).toBeGreaterThan(1)
  for (const line of await rows(page).locator('ul').all()) {
    const boxes = await line.locator('[data-bar]').evaluateAll((els) => els.map((el) => el.getBoundingClientRect()).map((b) => [b.left, b.right]))
    boxes.sort((a, b) => a[0]! - b[0]!)
    for (let i = 1; i < boxes.length; i++) expect(boxes[i]![0]!, 'bars on one line do not overlap').toBeGreaterThanOrEqual(boxes[i - 1]![1]!)
  }

  await expect(bar(page, 'team-a/shop', 14, 11)).toHaveText('#11')
  await expect(bar(page, 'team-a/shop', 14, 11)).toHaveAttribute('aria-label', /^#11 Add the card provider, running .*, in %14$/)
  await expect(bar(page, 'team-a/shop', 15, 20)).toHaveAttribute('aria-label', /running now, in %15$/)
  // #4 ran three days ago: outside the 24 h window.
  await expect(bar(page, `team-b/${longProject}`, 3, 4)).toHaveCount(0)

  const legend = section(page).locator('[data-timeline-key] li')
  await expect(legend).toHaveText(['%14 Payment providers', '%15 Refunds', '%3 Search', 'Merged', 'Skipped'])

  // A bar's running segment takes its milestone's colour; the legend's
  // swatch shows the same colour.
  const colour = (iid: number, ms: number, project = 'team-a/shop') =>
    bar(page, project, ms, iid)
      .locator('[data-segment="running"]')
      .first()
      .evaluate((el) => getComputedStyle(el).borderBottomColor)
  const swatch = (n: number) => legend.nth(n).locator('span').evaluate((el) => getComputedStyle(el).backgroundColor)
  expect(await colour(11, 14)).toBe(await colour(12, 14))
  expect(await colour(11, 14)).toBe(await swatch(0))
  expect(await colour(20, 15)).toBe(await swatch(1))
  expect(await colour(7, 3, `team-b/${longProject}`)).toBe(await swatch(2))
  expect(await colour(11, 14)).not.toBe(await colour(20, 15))
  expect(problems).toEqual([])
})

test('the window switch widens to 7 d and keeps it in ?window=@1280', async ({ page, baseURL }) => {
  const { problems, windows } = await open(page, baseURL)
  const sw = section(page).locator('[data-timeline-window]')
  await expect(sw.getByRole('tab', { name: '24 h' })).toHaveAttribute('aria-selected', 'true')
  await expect.poll(() => windows.at(-1)).toBe(`${new Date(now.getTime() - 24 * 3600e3).toISOString()}..${now.toISOString()}`)

  await sw.getByRole('tab', { name: '7 d' }).click()
  await expect(page).toHaveURL(/[?&]window=7d(&|$)/)
  await expect.poll(() => windows.at(-1)).toBe(`${new Date(now.getTime() - 7 * 24 * 3600e3).toISOString()}..${now.toISOString()}`)
  await expect(bar(page, `team-b/${longProject}`, 3, 4)).toBeVisible()

  await page.reload()
  await expect(section(page).locator('[data-timeline-window]').getByRole('tab', { name: '7 d' })).toHaveAttribute('aria-selected', 'true')
  await expect(bar(page, `team-b/${longProject}`, 3, 4)).toBeVisible()
  expect(problems).toEqual([])
})

test('?window=6h opens the 6 h window@1280', async ({ page, baseURL }) => {
  const { problems } = await open(page, baseURL, { query: '?window=6h' })
  await expect(section(page).locator('[data-timeline-window]').getByRole('tab', { name: '6 h' })).toHaveAttribute('aria-selected', 'true')
  await expect(rows(page)).toHaveCount(2)
  expect(problems).toEqual([])
})

test('without the research database the section shows the info alert@1280', async ({ page, baseURL }) => {
  const { problems, windows } = await open(page, baseURL, { history: false })
  await expect(page.locator('main [data-timeline-no-history]')).toHaveText('Done milestones and spend need the research database.')
  await expect(section(page)).toHaveCount(0)
  expect(windows).toEqual([])
  expect(problems).toEqual([])
})

for (const colorScheme of ['light', 'dark'] as const) {
  test(`axe passes on the Overview timeline (${colorScheme})@1280`, async ({ page, baseURL }) => {
    const { problems } = await open(page, baseURL, { colorScheme })
    await expect(rows(page)).toHaveCount(2)
    const results = await new AxeBuilder({ page })
      .include('main [data-overview-timeline]')
      .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
      .analyze()
    // In dark, a primary UTabs pill's active label (--ui-text-inverted on
    // --primary, about 3.2:1) fails color-contrast on every page: one of
    // the dark color-contrast findings known-a11y.ts leaves to #641. Only
    // that node is let through here; everything else must pass.
    const known = (id: string, target: string) => colorScheme === 'dark' && id === 'color-contrast' && /-trigger-24h\b/.test(target)
    const found = results.violations.flatMap((v) => v.nodes.map((n) => [v.id, n.target.join(' ')] as const)).filter(([id, t]) => !known(id, t))
    expect(found.map(([id, t]) => `${id}: ${t}`)).toEqual([])
    expect(problems).toEqual([])
  })
}
