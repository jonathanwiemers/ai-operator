// The Desk screenshots of the docs (playwright.docs.config.ts): a few pages
// on the browser checks' invented data (e2e/fixtures: the shop team-a/shop
// on gitlab.example.com), saved as PNG to e2e-results/docs/<name>.png.
// `make docs-screenshots` turns them into docs/assets/screenshots/*.webp.
import { join } from 'node:path'
import { expect, test, type Page } from '@playwright/test'
import { answer, now } from './fixtures'

interface Shot {
  // name is the file name, without extension.
  name: string
  path: string
  // ready waits until the page shows its data.
  ready: (page: Page) => Promise<void>
  colorScheme?: 'light' | 'dark'
}

const shop = '/projects/team-a/shop'
const text = (s: string) => (page: Page) => expect(page.locator('main').getByText(s).first()).toBeVisible()

const shots: Shot[] = [
  { name: 'dashboard', path: '/', ready: text('Checkout without account') },
  { name: 'dashboard-dark', path: '/', colorScheme: 'dark', ready: text('Checkout without account') },
  { name: 'project-stream', path: shop, ready: text('Run 2 needs input') },
  { name: 'milestone-issues', path: `${shop}/milestones/14`, ready: text('Document the refund flow for support') },
  { name: 'milestone-pilot', path: `${shop}/milestones/14?tab=pilot`, ready: text('Split across captures') },
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
    await page.clock.setFixedTime(now)
    await mockAPI(page)
    await page.goto(shot.path)
    await shot.ready(page)
    await page.waitForLoadState('networkidle')
    // Charts animate in; give them a moment after the data is drawn.
    await page.waitForTimeout(600)
    await page.screenshot({ path: join(testInfo.project.outputDir, `${shot.name}.png`) })
  })
}
