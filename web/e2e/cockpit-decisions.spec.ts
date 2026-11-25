// Decisions in the Cockpit's issue threads (R5 of "Desk: the Cockpit newest
// first by issue"): a decision.recorded event shows in its issue's thread
// with its ID, title and choice; Revisit there posts the same command as on
// the Decisions tab; the ID opens the tab at the card; a superseded decision
// says "Superseded by D…"; a decision the store no longer has shows the
// event's data without Revisit; axe. The fixture milestone %14's decisions
// are fixtures/decisions.ts (D280-1 superseded by D281-2, D280-2 revisited
// through the pilot). Run with npm run e2e (built UI, Chromium).
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { journeyAt } from './journey'
import { answer, now } from './fixtures'
import { ago } from './fixtures/clock'
import type { StreamEvent, StreamEventData } from '../src/types'

const milestonePath = '/projects/team-a/shop/milestones/14'

let nextID = 5000
const decision = (minutes: number, issueIID: number, data: StreamEventData): StreamEvent => ({
  id: ++nextID,
  at: ago(minutes),
  namespace: 'team-a',
  project: 'shop',
  milestoneIID: 14,
  issueIID,
  kind: 'decision.recorded',
  summary: `${data.id} ${data.title}`,
  data,
})

// The stream newest first: #281's decision on top, #280's two below, and
// D279-1, whose stored row expired.
const events: StreamEvent[] = [
  decision(90, 281, {
    id: 'D281-2',
    title: 'Refund in the order currency',
    choice: 'The order currency',
    supersedes: 'D280-1',
  }),
  decision(60 * 26, 280, {
    id: 'D280-2',
    title: 'Refund retries',
    choice: 'Three times with backoff',
  }),
  decision(60 * 26 + 1, 280, {
    id: 'D280-1',
    title: 'Refund currency',
    choice: 'The settlement currency',
  }),
  decision(60 * 40, 279, {
    id: 'D279-1',
    title: 'Refund API shape',
    choice: 'One endpoint per refund',
  }),
]

type Post = { path: string; body: unknown }

async function open(page: Page, baseURL: string | undefined, path: string, width = 1280) {
  const origin = new URL(baseURL ?? '').origin
  const problems: string[] = []
  const posts: Post[] = []
  await page.setViewportSize({ width, height: 800 })
  await page.clock.install({ time: now })
  page.on('pageerror', (err) => problems.push(err.message))
  await page.route(
    (url) => url.protocol !== 'data:' && url.protocol !== 'blob:' && url.origin !== origin,
    (r) => r.abort('blockedbyclient'),
  )
  await page.route('**/api/v1/**', async (r) => {
    const req = r.request()
    const url = new URL(req.url())
    const p = url.pathname.slice('/api/v1'.length)
    if (p === '/events')
      return r.fulfill({
        status: 200,
        contentType: 'text/event-stream',
        body: 'retry: 3600000\n\n',
      })
    if (p.endsWith('/chat')) {
      if (req.method() === 'POST') {
        posts.push({ path: p, body: req.postDataJSON() })
        return r.fulfill({ status: 200, json: { id: 99 } })
      }
      return r.fulfill({ status: 200, json: { messages: [] } })
    }
    if (req.method() !== 'GET') {
      if (p.endsWith('/warm')) return r.fulfill({ status: 204 })
      problems.push(`${req.method()} ${url.pathname}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    if (p === '/stream' && url.searchParams.get('milestone') === '14' && url.searchParams.get('kinds')?.includes('decision.recorded')) {
      if (url.searchParams.has('after')) return r.fulfill({ status: 200, json: { events: [], history: true } })
      return r.fulfill({
        status: 200,
        json: { events, newest: `c-${events[0]!.id}`, history: true },
      })
    }
    const body = answer(p, url.searchParams)
    if (body === undefined) {
      problems.push(`GET ${url.pathname}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    if (typeof body === 'string')
      return r.fulfill({
        status: 200,
        contentType: 'text/plain; charset=utf-8',
        body,
      })
    return r.fulfill({ status: 200, json: body })
  })
  await page.goto(path)
  return { problems, posts }
}

const item = (page: Page, iid: number, id: string) =>
  page.locator(`main #issue-${iid} [data-cockpit-decision]`).filter({ has: page.getByRole('link', { name: id, exact: true }) })

test.describe('decisions in Cockpit threads', () => {
  test('a decision shows in its issue thread with its ID, title and choice', journeyAt(1280), async ({ page, baseURL }) => {
    const { problems } = await open(page, baseURL, milestonePath)
    const d = item(page, 281, 'D281-2')
    await expect(d).toBeVisible()
    await expect(d).toContainText('Refund in the order currency')
    await expect(d.locator('[data-cockpit-decision-choice]')).toContainText('The order currency, converted back by the provider')
    await expect(page.locator('main #issue-280 [data-cockpit-decision]')).toHaveCount(2)
    expect(problems).toEqual([])
  })

  test('a superseded decision says Superseded by', async ({ page, baseURL }) => {
    const { problems } = await open(page, baseURL, milestonePath)
    await expect(item(page, 280, 'D280-1').locator('[data-superseded]')).toHaveText('Superseded by D281-2')
    await expect(item(page, 280, 'D280-2').locator('[data-superseded]')).toHaveCount(0)
    expect(problems).toEqual([])
  })

  test('a decision the store no longer has shows the event without Revisit', async ({ page, baseURL }) => {
    const { problems } = await open(page, baseURL, milestonePath)
    const d = page.locator('main #issue-279 [data-cockpit-decision]')
    await expect(d).toContainText('D279-1')
    await expect(d).toContainText('One endpoint per refund')
    await expect(d.getByRole('button')).toHaveCount(0)
    expect(problems).toEqual([])
  })

  test('Revisit in the thread posts the same command as on the Decisions tab', journeyAt(1280), async ({ page, baseURL }) => {
    const { problems, posts } = await open(page, baseURL, `${milestonePath}?tab=decisions`)
    await page.locator('main #D280-2').getByRole('button', { name: 'Revisit D280-2' }).click()
    await expect.poll(() => posts.length).toBe(1)

    await page.goto(milestonePath)
    await item(page, 280, 'D280-2').getByRole('button', { name: 'Revisit D280-2' }).click()
    await expect.poll(() => posts.length).toBe(2)
    const [tab, thread] = posts as [Post, Post]
    expect(thread.path).toBe(tab.path)
    const strip = (b: unknown) => ({
      ...(b as Record<string, unknown>),
      clientKey: '',
    })
    expect(strip(thread.body)).toEqual(strip(tab.body))
    expect(tab.path).toBe('/projects/team-a/shop/tasks/shop-m14-pilot/chat')
    expect(problems).toEqual([])
  })

  test('the ID opens the Decisions tab at the card', journeyAt(1280), async ({ page, baseURL }) => {
    const { problems } = await open(page, baseURL, milestonePath)
    await item(page, 280, 'D280-2').getByRole('link', { name: 'D280-2', exact: true }).click()
    await expect(page).toHaveURL(/\?tab=decisions#D280-2$/)
    await expect(page.locator('main #D280-2[data-decision]')).toBeInViewport()
    expect(problems).toEqual([])
  })

  // No known-a11y entry is added; color-contrast is the event cards' known
  // rule (cockpit.spec.ts).
  for (const width of [1280, 390]) {
    test(`axe is green with decisions in the threads@${width}`, async ({ page, baseURL }) => {
      const { problems } = await open(page, baseURL, milestonePath, width)
      await expect(item(page, 281, 'D281-2')).toBeVisible()
      const results = await new AxeBuilder({ page })
        .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
        .disableRules(['color-contrast'])
        .analyze()
      expect(results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)).toEqual([])
      expect(problems).toEqual([])
    })
  }
})
