// The milestone context panel (R13 of "Desk: the Cockpit", #786): Issues ·
// Queue · Decisions. At 1280 px it is a column beside the Cockpit and each
// view opens from its switch; at 390 px the header's Issues, Queue and
// Decisions buttons open it as a slide-over on that view, Escape closes it
// and focus returns to the button. The view is kept in ?panel=. axe on both
// widths. The fixture milestone %14 has merged (#279, #280), running (#281)
// and waiting (#282, #283, and #284 on a hold) issues, is second in the
// project's queue, and has one superseded decision (D280-1). Run with npm run e2e (built UI,
// Chromium).
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { journeyAt } from './journey'
import { answer, now } from './fixtures'

const milestonePath = '/projects/team-a/shop/milestones/14'

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
    const body = answer(p, url.searchParams)
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

const panel = (page: Page) => page.getByRole('complementary', { name: 'Milestone context' })
const switchTo = (page: Page, label: string) => panel(page).getByRole('tab', { name: label, exact: true })
// The header's buttons by their data attribute: while the modal slide-over
// is open, the page behind it is hidden from role queries.
const headerButton = (page: Page, label: string) => page.locator(`main [data-stream-head] [data-panel-open="${label.toLowerCase()}"]`)

// expectView checks what each view shows of the fixture.
async function expectView(page: Page, view: 'issues' | 'queue' | 'decisions') {
  const p = panel(page)
  await expect(switchTo(page, view[0]!.toUpperCase() + view.slice(1))).toHaveAttribute('aria-selected', 'true')
  if (view === 'issues') {
    await expect(p.getByRole('heading')).toHaveText([/^Running\s*1$/, /^Waiting\s*3$/, /^Merged\s*2$/])
    await expect(p.locator('[data-panel-state="running"]').getByRole('link')).toHaveAttribute('href', `${milestonePath}/issues/281`)
    await expect(p.getByRole('link', { name: 'Open table' })).toHaveAttribute('href', `${milestonePath}?tab=issues`)
  } else if (view === 'queue') {
    await expect(p.locator('[data-panel-queue-entry]')).toHaveCount(3)
    const current = p.locator('[data-panel-queue-entry="14"]').getByRole('link')
    await expect(current).toHaveAttribute('aria-current', 'page')
    await expect(current).toContainText('This milestone')
    await expect(current).toContainText('Paused')
  } else {
    await expect(p.locator('[data-panel-decision]')).toHaveCount(3)
    await expect(p.getByText('D281-2')).toBeVisible()
    // The superseded one is in a closed group.
    const old = p.getByRole('button', { name: 'Superseded (1)' })
    await expect(old).toHaveAttribute('aria-expanded', 'false')
    await expect(p.getByText('D280-1')).toHaveCount(0)
  }
}

// axe (WCAG 2.2 AA) on the panel. color-contrast is left out as on the
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

test('the panel is a column beside the Cockpit, each view opens from the switch@1280', journeyAt(1280), async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, milestonePath, 1280)
  await expect(panel(page)).toBeVisible()
  await expect(page.locator('main [data-panel-open]')).toHaveCount(0)
  const cockpit = (await page.locator('main [data-cockpit-stream]').boundingBox())!
  const side = (await panel(page).boundingBox())!
  expect(side.x, 'the panel is right of the Cockpit').toBeGreaterThanOrEqual(cockpit.x + cockpit.width)
  await expectView(page, 'issues')
  expect(await axe(page, '[data-milestone-panel]')).toEqual([])

  // The switch by keyboard: arrow keys move between the views.
  await switchTo(page, 'Issues').focus()
  await page.keyboard.press('ArrowRight')
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/\?panel=queue$/)
  await expectView(page, 'queue')
  expect(await axe(page, '[data-milestone-panel]')).toEqual([])

  await switchTo(page, 'Decisions').click()
  await expect(page).toHaveURL(/\?panel=decisions$/)
  await expectView(page, 'decisions')
  const old = panel(page).getByRole('button', { name: 'Superseded (1)' })
  await old.focus()
  await page.keyboard.press('Enter')
  await expect(old).toHaveAttribute('aria-expanded', 'true')
  await expect(panel(page).getByText('D280-1')).toBeVisible()
  expect(await axe(page, '[data-milestone-panel]')).toEqual([])

  await switchTo(page, 'Issues').click()
  await expect(page).toHaveURL(new RegExp(`${milestonePath}$`))
  await panel(page).getByRole('link', { name: 'Open table' }).click()
  await expect(page).toHaveURL(/\?tab=issues$/)
  // The Issues table needs the full width: there the panel is a
  // slide-over at every width, opened by the header's buttons.
  await expect(panel(page), 'no column beside the Issues table').toHaveCount(0)
  await headerButton(page, 'Queue').click()
  await expect(page.getByRole('dialog', { name: 'Queue' })).toBeVisible()
  await expectView(page, 'queue')
  await page.keyboard.press('Escape')
  await expect(headerButton(page, 'Queue')).toBeFocused()
  expect(problems).toEqual([])
})

test('?panel=decisions opens the Decisions view, a reload keeps it@1280', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, `${milestonePath}?panel=decisions`, 1280)
  await expectView(page, 'decisions')
  await page.reload()
  await expectView(page, 'decisions')
  expect(problems).toEqual([])
})

for (const [label, view] of [
  ['Issues', 'issues'],
  ['Queue', 'queue'],
  ['Decisions', 'decisions'],
] as const) {
  test(`the ${label} button opens its view as a slide-over, Escape closes it and focus returns@390`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, milestonePath, 390)
    await expect(panel(page), 'no column on a phone').toHaveCount(0)
    const button = headerButton(page, label)
    await expect(button).toHaveAttribute('aria-expanded', 'false')
    await button.click()
    const sheet = page.getByRole('dialog', { name: label })
    await expect(sheet).toBeVisible()
    await sheet.evaluate((e) => Promise.all(e.getAnimations().map((a) => a.finished)))
    await expect(button).toHaveAttribute('aria-expanded', 'true')
    await expectView(page, view)
    if (view !== 'issues') await expect(page).toHaveURL(new RegExp(`\\?panel=${view}$`))
    expect(await axe(page, '[role="dialog"]')).toEqual([])
    const fit = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(fit, 'the page does not scroll sideways').toBe(0)

    await page.keyboard.press('Escape')
    await expect(sheet).toBeHidden()
    await expect(button).toBeFocused()
    expect(problems).toEqual([])
  })
}

test('switching the view inside the slide-over keeps it open@390', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, milestonePath, 390)
  await headerButton(page, 'Issues').click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await switchTo(page, 'Decisions').click()
  await expect(page).toHaveURL(/\?panel=decisions$/)
  await expectView(page, 'decisions')
  await expect(page.getByRole('dialog')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(headerButton(page, 'Issues')).toBeFocused()
  expect(problems).toEqual([])
})

// A link to a panel view (?panel=, the old ?tab=decisions, a decision link)
// opens the slide-over on that view below 1180 px; Escape closes it.
test('a ?panel=decisions link opens the slide-over on Decisions@390', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, `${milestonePath}?panel=decisions`, 390)
  const sheet = page.getByRole('dialog', { name: 'Decisions' })
  await expect(sheet).toBeVisible()
  await expectView(page, 'decisions')
  await page.keyboard.press('Escape')
  await expect(sheet).toBeHidden()
  await expect(page).toHaveURL(/\?panel=decisions$/)
  expect(problems).toEqual([])
})
