// The Needs you milestone tab (#530: R12): prompt links open it, an open
// prompt shows once, the pause card's own prompt is not listed again, and
// the Pilot tab keeps only the pilot line, the composer and its events.
// Run with npm run e2e (built UI, Chromium).
import { expect, test, type Page } from '@playwright/test'
import { answer, now } from './fixtures'
import { paymentsMilestone } from './fixtures/stream'
import type { MilestoneView, PromptView } from '../src/types'

const widths = [
  { width: 375, height: 812 },
  { width: 1280, height: 800 },
]

const milestonePath = '/projects/team-a/shop/milestones/14'
const q3 = paymentsMilestone.prompts!.find((p) => p.id === 'Q3')!

// openQ3: Q3 open again, without its answer and close fields.
const openQ3: PromptView = {
  ...q3,
  state: 'open',
  answer: undefined,
  closedAt: undefined,
  closedReason: undefined,
  closedBy: undefined,
  canAnswer: true,
}

// running: milestone 14 running, with Q3 its only open item.
const running: MilestoneView = {
  ...paymentsMilestone,
  state: 'running',
  autopilot: { ...paymentsMilestone.autopilot!, phase: 'Running', holds: [], pauseKind: undefined, pausedFor: undefined, pausedReason: undefined, exits: [] },
  pilot: { ...paymentsMilestone.pilot!, proposals: [] },
  prompts: [openQ3],
}

// mainRed: milestone 14 paused on main, whose mainHealth prompt Q5 the
// pause card shows.
const q5: PromptView = {
  id: 'Q5',
  kind: 'mainHealth',
  question: 'Main is red since `d4e5f6a`. Revert it?',
  options: [
    { id: 'revert', label: 'Revert d4e5f6a', actions: ['Opens a revert MR'], recommended: true, fromAnswer: false },
    { id: 'wait', label: 'Wait for a fix', actions: [], recommended: false, fromAnswer: false },
  ],
  allowFreeText: false,
  state: 'open',
  postedAt: q3.postedAt,
  canAnswer: true,
  target: { namespace: 'team-a', task: 'shop-m14-pilot' },
}
const mainRed: MilestoneView = {
  ...paymentsMilestone,
  autopilot: { ...paymentsMilestone.autopilot!, holds: [], pauseKind: 'mainRed', pausedFor: undefined, exits: [] },
  pilot: { ...paymentsMilestone.pilot!, proposals: [] },
  prompts: [q5, openQ3],
}

// open shows path with the API answered from the fixtures (GET only), the
// milestone's view replaced by milestone when given.
async function open(page: Page, baseURL: string | undefined, path: string, size: { width: number; height: number }, milestone?: MilestoneView) {
  const origin = new URL(baseURL ?? '').origin
  const problems: string[] = []
  await page.setViewportSize(size)
  await page.clock.setFixedTime(now)
  page.on('pageerror', (err) => problems.push(err.message))
  await page.route(
    (url) => url.protocol !== 'data:' && url.protocol !== 'blob:' && url.origin !== origin,
    (r) => r.abort('blockedbyclient'),
  )
  await page.route('**/api/v1/**', async (r) => {
    const req = r.request()
    const url = new URL(req.url())
    const p = url.pathname.slice('/api/v1'.length)
    if (p === '/events' && req.method() === 'GET') {
      return r.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 3600000\n\n' })
    }
    const body = req.method() !== 'GET' ? undefined : milestone && p === milestonePath ? milestone : answer(p, url.searchParams)
    if (body === undefined) {
      problems.push(`${req.method()} ${url.pathname}${url.search}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    if (typeof body === 'string') return r.fulfill({ status: 200, contentType: 'text/plain; charset=utf-8', body })
    return r.fulfill({ status: 200, json: body })
  })
  await page.goto(path)
  return problems
}

const strip = (page: Page) => page.getByRole('navigation', { name: 'View' })
const tab = (page: Page, name: RegExp) => strip(page).getByRole('link', { name })

for (const size of widths) {
  test(`a prompt link opens Needs you at the card@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}#prompt-Q3`, size)
    await expect(page.locator('#prompt-Q3')).toBeInViewport()
    await expect(page.locator('[data-needs-handled] [data-earlier]')).toHaveAttribute('open', '')
    await expect(tab(page, /^Needs you/)).toHaveAttribute('aria-current', 'page')
    expect(problems).toEqual([])
  })

  test(`one open prompt shows once, the label counts it@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, running)
    await expect(tab(page, /^Needs you/)).toHaveText('Needs you · 1')
    const tabs = await strip(page).getByRole('link').allTextContents()
    expect(tabs.map((t) => t.trim())).toEqual(['Issues', 'Needs you · 1', 'Pilot', 'Retrospective', 'Stream', 'Board', 'Spend'])
    await expect(page.locator('[data-needs-open] [data-prompt="Q3"]')).toHaveCount(1)
    await expect(page.locator('[data-prompt="Q3"]')).toHaveCount(1)
    await expect(page.getByRole('button', { name: 'Release SDK 4 first', exact: true })).toHaveCount(1)
    expect(problems).toEqual([])
  })

  test(`a paused milestone lists the pause card's prompt once@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, mainRed)
    const needs = page.locator('[data-needs-open]')
    await expect(needs.locator('[data-pause-card]')).toHaveCount(1)
    await expect(page.locator('[data-prompt="Q5"]')).toHaveCount(1)
    await expect(needs.locator('[data-pause-card] [data-prompt="Q5"]')).toHaveCount(1)
    await expect(page.locator('[data-prompt="Q3"]')).toHaveCount(1)
    // The pause card and Q3.
    await expect(tab(page, /^Needs you/)).toHaveText('Needs you · 2')
    expect(problems).toEqual([])
  })

  test(`the Pilot tab has no questions and no pause history@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=pilot`, size)
    await expect(page.getByRole('heading', { name: 'Pilot events' })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Message the pilot' })).toBeVisible()
    await expect(page.locator('main [data-pilot]')).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Questions', exact: true })).toHaveCount(0)
    await expect(page.locator('main [data-pauses]')).toHaveCount(0)
    await expect(page.locator('main [data-pause-card]')).toHaveCount(0)
    await expect(page.locator('main [data-prompt]')).toHaveCount(0)
    expect(problems).toEqual([])
  })

  test(`the open older proposal sits on Needs you, not in the pilot chat@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size)
    // The pause card, #284's hold, #281's question and P7.
    await expect(tab(page, /^Needs you/)).toHaveText('Needs you · 4')
    const p7 = page.locator('[data-needs-open] [data-proposal="7"]')
    await expect(p7).toContainText('P7')
    await expect(p7.getByRole('button', { name: 'Accept', exact: true })).toBeVisible()
    await expect(p7.getByRole('button', { name: 'Reject', exact: true })).toBeVisible()
    await expect(page.locator('[data-needs-handled] [data-pauses]')).toBeVisible()

    await tab(page, /^Pilot$/).click()
    await expect(page.getByRole('heading', { name: 'Pilot events' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Accept P7' })).toHaveCount(0)
    expect(problems).toEqual([])
  })
}
