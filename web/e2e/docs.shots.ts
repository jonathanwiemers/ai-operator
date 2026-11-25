// The Desk screenshots of the docs (playwright.docs.config.ts): a few pages
// on the browser checks' invented data (e2e/fixtures: the shop team-a/shop
// on gitlab.example.com), saved as PNG to e2e-results/docs/<name>.png.
// `make docs-screenshots` turns them into docs/assets/screenshots/*.webp.
import { join } from 'node:path'
import { expect, test, type Page } from '@playwright/test'
import { answer, now } from './fixtures'
import { openAccountMenu } from './account'

interface Shot {
  // name is the file name, without extension.
  name: string
  path: string
  // ready waits until the page shows its data.
  ready: (page: Page) => Promise<void>
  colorScheme?: 'light' | 'dark'
  // phone renders the shot at 375×812 instead of the config's desktop size.
  phone?: boolean
  // open runs after ready, e.g. to open a sheet; the shot waits for the
  // sheet's animation to end.
  open?: (page: Page) => Promise<void>
}

// openSheet taps the control and waits until the dialog has slid in.
const openSheet = (control: (page: Page) => ReturnType<Page['locator']>, name?: string) => async (page: Page) => {
  await control(page).click()
  const sheet = name ? page.getByRole('dialog', { name }) : page.getByRole('dialog')
  await expect(sheet).toBeVisible()
  await sheet.evaluate((e) => Promise.all(e.getAnimations().map((a) => a.finished)))
}

const shop = '/projects/team-a/shop'
const text = (s: string) => (page: Page) => expect(page.locator('main').getByText(s).first()).toBeVisible()

const shots: Shot[] = [
  // The Overview's Cockpit (R16 of "Desk: the Cockpit"): the Inbox's items pinned, Working now in the panel.
  { name: 'dashboard', path: '/', ready: text('Support refunds through the payment provider') },
  { name: 'dashboard-dark', path: '/', colorScheme: 'dark', ready: text('Support refunds through the payment provider') },
  // The project's Cockpit (R15 of "Desk: the Cockpit"): %14's pinned cards, the stream, the panel.
  { name: 'project-stream', path: shop, ready: (page) => expect(page.locator('main [data-pinned-milestone="14"] [data-problem]').first()).toBeVisible() },
  { name: 'milestone-issues', path: `${shop}/milestones/14?tab=issues`, ready: text('Document the refund flow for support') },
  { name: 'milestone-pilot', path: `${shop}/milestones/14`, ready: text('Split across captures') },
  {
    name: 'milestone-retrospective',
    path: `${shop}/milestones/14?tab=retrospective`,
    ready: text('Retry the registry push in the release job'),
  },
  { name: 'task-chat', path: `${shop}/tasks/shop-281-implement?tab=chat`, ready: text('Please keep the refund reasons in the') },
  {
    name: 'costs',
    path: '/costs?range=30d',
    ready: async (page) => {
      await text('Research counted since')(page)
      await expect(page.locator('[data-chart=spend] canvas[role=img]')).toBeVisible()
    },
  },
  // The phone's rail sheet with the account menu open from its bottom:
  // login, Appearance (Light/Dark/Auto), version and Sign out.
  {
    name: 'account-menu',
    path: '/',
    phone: true,
    ready: text('Support refunds through the payment provider'),
    open: async (page) => {
      await openSheet((p) => p.getByRole('navigation', { name: 'Sections' }).getByRole('button', { name: 'Projects' }))(page)
      await page.getByRole('dialog').locator('[data-account-button]').scrollIntoViewIfNeeded()
      await openAccountMenu(page)
    },
  },
  // The Metrics page's value filters in the phone's Filters sheet.
  {
    name: 'metrics-filters',
    path: '/metrics?range=30d',
    phone: true,
    ready: text('Research counted since'),
    open: openSheet((p) => p.locator('[data-filters-open]'), 'Filters'),
  },
]

// The docs show an invented user "sam" (the fixtures' "jo") and a second
// project "storefront" (the fixtures' long name that checks truncation).
const renames: [string, string][] = [
  ['"jo"', '"sam"'],
  ['"Jo"', '"Sam"'],
  ['@jo', '@sam'],
  ['a-project-with-a-rather-long-name.example', 'storefront'],
]
const docsData = (body: unknown) => JSON.parse(renames.reduce((s, [from, to]) => s.replaceAll(from, to), JSON.stringify(body)))

async function mockAPI(page: Page): Promise<void> {
  await page.route(
    (url) => url.protocol !== 'data:' && url.protocol !== 'blob:' && !url.pathname.startsWith('/api/') && url.hostname !== '127.0.0.1',
    (r) => r.abort('blockedbyclient'),
  )
  await page.route('**/api/v1/**', async (r) => {
    const req = r.request()
    const url = new URL(req.url())
    const path = url.pathname.slice('/api/v1'.length)
    if (path === '/events') return r.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 3600000\n\n' })
    const body = req.method() === 'GET' ? answer(path, url.searchParams) : undefined
    if (body === undefined) return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    return r.fulfill({ status: 200, json: docsData(body) })
  })
}

for (const shot of shots) {
  test(shot.name, async ({ page }, testInfo) => {
    if (shot.colorScheme) await page.emulateMedia({ colorScheme: shot.colorScheme })
    if (shot.phone) await page.setViewportSize({ width: 375, height: 812 })
    await page.clock.setFixedTime(now)
    await mockAPI(page)
    await page.goto(shot.path)
    await shot.ready(page)
    await page.waitForLoadState('networkidle')
    // Charts animate in; give them a moment after the data is drawn.
    await page.waitForTimeout(600)
    if (shot.open) await shot.open(page)
    await page.screenshot({ path: join(testInfo.project.outputDir, `${shot.name}.png`) })
  })
}
