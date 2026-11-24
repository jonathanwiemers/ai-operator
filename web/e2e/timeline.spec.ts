// The Timeline on a milestone's Issues tab (MilestoneIssues.vue,
// ui/Timeline.vue, #666): with the history feature Timeline is the default
// and a switch opens Lanes, kept in ?view=; without it only Lanes. The
// fixture (fixtures/timeline.ts) is milestone 14 with #11 and #12 side by
// side in W1 and #13 after both in W2.
// Run with npm run e2e (built UI, Chromium).
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { answer, now } from './fixtures'
import { features } from './fixtures/session'

const milestonePath = '/projects/team-a/shop/milestones/14'
const issuePath = (iid: number) => `${milestonePath}/issues/${iid}`

interface Opts {
  history?: boolean
  width?: number
  colorScheme?: 'light' | 'dark'
  query?: string
}

async function open(page: Page, baseURL: string | undefined, opts: Opts = {}) {
  const problems: string[] = []
  const origin = new URL(baseURL ?? '').origin
  await page.setViewportSize({ width: opts.width ?? 1280, height: 800 })
  await page.emulateMedia({ colorScheme: opts.colorScheme ?? 'light' })
  await page.clock.setFixedTime(now)
  page.on('pageerror', (err) => problems.push(err.message))
  await page.route((url) => url.protocol !== 'data:' && url.protocol !== 'blob:' && url.origin !== origin, (r) => r.abort('blockedbyclient'))
  await page.route('**/api/v1/**', async (r) => {
    const url = new URL(r.request().url())
    const p = url.pathname.slice('/api/v1'.length)
    // The stream ends at once; the retry keeps EventSource from reconnecting.
    if (p === '/events') return r.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 3600000\n\n' })
    if (r.request().method() === 'POST' && p.endsWith('/warm')) return r.fulfill({ status: 204 })
    let body = answer(p, url.searchParams)
    if (p === '/features' && opts.history === false) body = { features: features.features.filter((f) => f.name !== 'history') }
    if (body === undefined) {
      problems.push(`${r.request().method()} ${url.pathname}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    if (typeof body === 'string') return r.fulfill({ status: 200, contentType: 'text/plain; charset=utf-8', body })
    return r.fulfill({ status: 200, json: body })
  })
  await page.goto(`${milestonePath}?tab=issues${opts.query ?? ''}`)
  await expect(page.locator('main').getByText('Document the refund flow for support').first()).toBeVisible()
  return problems
}

const timeline = (page: Page) => page.locator('main [data-timeline]')
const bar = (page: Page, iid: number) => timeline(page).locator(`a[href="${issuePath(iid)}"]`)
const box = async (page: Page, iid: number) => (await bar(page, iid).boundingBox())!

test('Timeline is the default; the switch opens Lanes and keeps it in ?view=@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL)
  const sw = page.locator('main [data-issues-view-switch]')
  await expect(sw.getByRole('tab', { name: 'Timeline' })).toHaveAttribute('aria-selected', 'true')
  await expect(timeline(page)).toBeVisible()
  await expect(page.locator('main [data-queue]')).toHaveCount(0)

  await sw.getByRole('tab', { name: 'Lanes' }).click()
  await expect(page).toHaveURL(/[?&]view=lanes(&|$)/)
  await expect(page).toHaveURL(/[?&]tab=issues(&|$)/)
  await expect(page.locator('main [data-queue]')).toBeVisible()
  await expect(timeline(page)).toHaveCount(0)

  await sw.getByRole('tab', { name: 'Timeline' }).click()
  await expect(page).toHaveURL(/[?&]view=timeline(&|$)/)
  await expect(timeline(page)).toBeVisible()
  expect(problems).toEqual([])
})

test('?view=lanes opens Lanes@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, { query: '&view=lanes' })
  await expect(page.locator('main [data-issues-view-switch]').getByRole('tab', { name: 'Lanes' })).toHaveAttribute('aria-selected', 'true')
  await expect(page.locator('main [data-queue]')).toBeVisible()
  await expect(timeline(page)).toHaveCount(0)
  expect(problems).toEqual([])
})

test('without the history feature the tab shows Lanes, and the switch only Lanes and Board@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, { history: false, query: '&view=timeline' })
  await expect(page.locator('main [data-queue]')).toBeVisible()
  const views = page.locator('main [data-issues-view-switch]').getByRole('tab')
  await expect(views).toHaveText(['Lanes', 'Board'])
  await expect(views.first()).toHaveAttribute('aria-selected', 'true')
  await expect(timeline(page)).toHaveCount(0)
  expect(problems).toEqual([])
})

test('bars: waves, overlap, segments, markers and links@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL)
  await expect(timeline(page).locator('[data-wave]')).toHaveText(['W1', 'W2'])
  await expect(timeline(page).locator('[data-now]')).toBeVisible()

  // #11 and #12 overlap in time; #13 starts after both and sits under W2.
  const [b11, b12, b13] = [await box(page, 11), await box(page, 12), await box(page, 13)]
  expect(b11.x < b12.x + b12.width && b12.x < b11.x + b11.width, 'W1 bars overlap').toBe(true)
  expect(b13.x, '#13 starts after #11 and #12').toBeGreaterThan(Math.max(b11.x + b11.width, b12.x + b12.width))
  const w2 = (await timeline(page).locator('[data-wave]', { hasText: 'W2' }).boundingBox())!
  expect(b12.y).toBeLessThan(w2.y)
  expect(b13.y).toBeGreaterThan(w2.y)

  for (const iid of [11, 12, 13]) {
    const b = bar(page, iid)
    await expect(b).toHaveText(`#${iid}`)
    await expect(b).toHaveAttribute('aria-label', new RegExp(`^#${iid} .*, running .*, merged `))
    await expect(b.locator('[data-segment="running"]')).toHaveCount(1)
    await expect(b.locator('[data-segment="waiting"]')).toHaveCount(1)
    await expect(b.locator('[data-marker="merged"]')).toHaveCount(1)
  }

  // #12's hold is drawn in the warning tone.
  const held = bar(page, 12).locator('[data-segment="held"]')
  await expect(held).toHaveCount(1)
  const colours = await held.evaluate((el) => {
    const probe = document.createElement('span')
    probe.style.background = 'var(--tone-warning-bg)'
    document.body.append(probe)
    const want = getComputedStyle(probe).backgroundColor
    probe.remove()
    return { got: getComputedStyle(el).backgroundColor, want }
  })
  expect(colours.got).toBe(colours.want)
  expect(problems).toEqual([])

  await bar(page, 13).click()
  await expect(page).toHaveURL(new RegExp(`${issuePath(13)}$`))
})

test('at 375 px the labels stay while the axis scrolls@375', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, { width: 375 })
  const scroller = timeline(page)
  await scroller.scrollIntoViewIfNeeded()
  const label = scroller.locator('[data-timeline-row]').first().locator('div').first()
  const before = { label: (await label.boundingBox())!, bar: await box(page, 13) }
  const scrolled = await scroller.evaluate((el) => {
    el.scrollLeft = 200
    return el.scrollLeft
  })
  expect(scrolled, 'the timeline scrolls sideways').toBeGreaterThan(0)
  await expect.poll(async () => (await box(page, 13)).x).toBeLessThan(before.bar.x - scrolled + 1)
  expect((await label.boundingBox())!.x).toBeCloseTo(before.label.x, 0)
  const sideways = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(sideways, 'the page itself does not scroll sideways').toBe(0)
  expect(problems).toEqual([])
})

for (const colorScheme of ['light', 'dark'] as const) {
  test(`axe passes on the Timeline (${colorScheme})@1280`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, { colorScheme })
    await expect(timeline(page)).toBeVisible()
    const results = await new AxeBuilder({ page })
      .include('main [data-issues-view]')
      .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
      .analyze()
    expect(results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)).toEqual([])
    expect(problems).toEqual([])
  })
}
