// R1 of "Desk: tab rows without stray dots" (#875): lists rendered by Nuxt UI
// (tab rows, rail, breadcrumb) show no markers; markdown lists keep theirs.
import { expect, test, type Page } from '@playwright/test'
import { answer, now } from './fixtures'

async function open(page: Page, baseURL: string | undefined, path: string, width: number, height: number) {
  const origin = new URL(baseURL ?? '').origin
  await page.setViewportSize({ width, height })
  await page.clock.install({ time: now })
  await page.route((url) => url.protocol !== 'data:' && url.protocol !== 'blob:' && url.origin !== origin, (r) => r.abort('blockedbyclient'))
  await page.route('**/api/v1/**', async (r) => {
    const url = new URL(r.request().url())
    const p = url.pathname.slice('/api/v1'.length)
    if (p === '/events') return r.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 3600000\n\n' })
    const body = r.request().method() === 'GET' ? answer(p, url.searchParams) : undefined
    if (body === undefined) return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    if (typeof body === 'string') return r.fulfill({ status: 200, contentType: 'text/plain; charset=utf-8', body })
    return r.fulfill({ status: 200, json: body })
  })
  await page.goto(path)
}

const markers = (page: Page, sel: string) =>
  page.locator(sel).evaluateAll((els) => els.map((e) => getComputedStyle(e).listStyleType))

for (const [width, height] of [[390, 844], [1280, 800]] as const) {
  test(`Nuxt UI lists show no markers at ${width} px`, async ({ page, baseURL }) => {
    await open(page, baseURL, '/projects/team-a/shop', width, height)
    await expect(page.locator('main [data-stream-head]').getByRole('navigation', { name: 'View' })).toBeVisible()
    const types = await markers(page, 'ul[data-slot], ol[data-slot]')
    expect(types.length).toBeGreaterThan(0)
    for (const t of types) expect(t).toBe('none')
  })
}

// The research news flash prompt is block markdown with a list (desk.spec.ts).
test('markdown lists keep their markers', async ({ page, baseURL }) => {
  await open(page, baseURL, '/research/research/payments-news', 1280, 800)
  const list = page.locator('main [data-research-head] .md ul, main [data-research-head] .md ol').first()
  await expect(list).toBeAttached()
  expect(await list.evaluate((e) => getComputedStyle(e).listStyleType)).not.toBe('none')
})
