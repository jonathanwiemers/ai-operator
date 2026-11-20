// The Needs you milestone tab (spec desk-problem-thread): problem list +
// thread, hash redirects, one bar, optimistic chat, 60 s note, history off.
// Run with npm run e2e (built UI, Chromium).
import { join } from 'node:path'
import { expect, test, type Page } from '@playwright/test'
import { answer, answerProblems, now } from './fixtures'
import { incident558, incident558Dirty, incidentError } from './fixtures/problem'
import { paymentsMilestone } from './fixtures/stream'
import type { MilestoneView, PromptView } from '../src/types'

const widths = [
  { width: 375, height: 812 },
  { width: 1280, height: 800 },
]
const milestonePath = '/projects/team-a/shop/milestones/14'
const milestoneAPI = '/projects/team-a/shop/milestones/14'
const pilotPath = '/projects/team-a/shop/tasks/shop-m14-pilot'
const pilotChatPath = `${pilotPath}/chat`
const q3 = paymentsMilestone.prompts!.find((p) => p.id === 'Q3')!

const openQ3: PromptView = {
  ...q3, state: 'open', answer: undefined, closedAt: undefined,
  closedReason: undefined, closedBy: undefined, canAnswer: true,
}
const running: MilestoneView = {
  ...paymentsMilestone, state: 'running',
  autopilot: { ...paymentsMilestone.autopilot!, phase: 'Running', holds: [], pauseKind: undefined, pausedFor: undefined, pausedReason: undefined, exits: [] },
  pilot: { ...paymentsMilestone.pilot!, proposals: [], acted: [], interventions: [] }, prompts: [openQ3], pauses: undefined,
}
const q5: PromptView = {
  id: 'Q5', kind: 'mainHealth', question: 'Main is red since `d4e5f6a`. Revert it?',
  options: [
    { id: 'revert', label: 'Revert d4e5f6a', actions: ['Opens a revert MR'], recommended: true, fromAnswer: false },
    { id: 'wait', label: 'Wait for a fix', actions: [], recommended: false, fromAnswer: false },
  ],
  allowFreeText: false, state: 'open', postedAt: q3.postedAt, canAnswer: true,
  target: { namespace: 'team-a', task: 'shop-m14-pilot' },
}
const mainRed: MilestoneView = {
  ...paymentsMilestone,
  autopilot: { ...paymentsMilestone.autopilot!, holds: [], pauseKind: 'mainRed', pausedFor: undefined, pauseIssue: undefined, exits: [] },
  pilot: { ...paymentsMilestone.pilot!, proposals: [], acted: [], interventions: [] }, prompts: [q5, openQ3], pauses: undefined,
}
const stuckPrompt = (id: string): PromptView => ({ ...openQ3, id, issueIID: 281, kind: 'assessment', question: `${id}: rerun #281?` })
const stuck: MilestoneView = {
  ...paymentsMilestone,
  autopilot: {
    ...paymentsMilestone.autopilot!, pauseKind: 'stuck', pausedFor: undefined, pauseIssue: 281,
    holds: [{ ...paymentsMilestone.autopilot!.holds![0]!, iid: 281, title: 'Support refunds', kind: 'needsInput' }],
  },
  pilot: { ...paymentsMilestone.pilot!, proposals: [], acted: [], interventions: [] }, prompts: [stuckPrompt('Q8'), stuckPrompt('Q9')], pauses: undefined,
}
const budget: MilestoneView = {
  ...paymentsMilestone,
  autopilot: { ...paymentsMilestone.autopilot!, pauseKind: 'budget', pausedFor: undefined, pauseIssue: undefined },
  pilot: { ...paymentsMilestone.pilot!, proposals: [], acted: [], interventions: [] }, prompts: [], pauses: undefined,
}
const stored: MilestoneView = { ...paymentsMilestone, state: 'done', stored: true, autopilot: undefined, pilot: undefined, prompts: [] }
const quiet: MilestoneView = { ...running, prompts: [], pilot: { ...running.pilot!, acted: [], interventions: [] }, pauses: undefined }

type OpenOpts = { milestone?: MilestoneView; history?: boolean; warms?: string[] }

async function open(page: Page, baseURL: string | undefined, path: string, size: { width: number; height: number }, opts: OpenOpts = {}) {
  const origin = new URL(baseURL ?? '').origin
  const problems: string[] = []
  const warms = opts.warms ?? []
  // A second open on the same page: unload the old document first, so its
  // late fetches (a thread reload after a chat POST) never reach the new
  // route handler and its fixture (#646).
  if (page.url() !== 'about:blank') await page.goto('about:blank')
  await page.setViewportSize(size)
  await page.clock.install({ time: now })
  page.on('pageerror', (err) => problems.push(err.message))
  await page.route((url) => url.protocol !== 'data:' && url.protocol !== 'blob:' && url.origin !== origin, (r) => r.abort('blockedbyclient'))
  await page.route('**/api/v1/**', async (r) => {
    const req = r.request()
    const url = new URL(req.url())
    const p = url.pathname.slice('/api/v1'.length)
    if (p === '/events' && req.method() === 'GET') return r.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 3600000\n\n' })
    if (req.method() === 'POST' && p.endsWith('/warm')) { warms.push(p); return r.fulfill({ status: 204 }) }
    if (req.method() !== 'GET') { problems.push(`${req.method()} ${url.pathname}`); return r.fulfill({ status: 404, json: { error: 'no fixture' } }) }
    const m = opts.milestone
    const pref = `${milestoneAPI}/problems`
    let body: unknown
    if (m && p === milestoneAPI) body = m
    else if (m && p === pref) body = answerProblems(m, undefined, { history: opts.history })
    else if (m && p.startsWith(`${pref}/`)) body = answerProblems(m, p.slice(pref.length + 1), { history: opts.history })
    else body = answer(p, url.searchParams)
    if (body === undefined) { problems.push(`GET ${url.pathname}`); return r.fulfill({ status: 404, json: { error: 'no fixture' } }) }
    if (typeof body === 'string') return r.fulfill({ status: 200, contentType: 'text/plain; charset=utf-8', body })
    return r.fulfill({ status: 200, json: body })
  })
  await page.goto(path)
  return problems
}

const tab = (page: Page, name: RegExp) => page.getByRole('navigation', { name: 'View' }).getByRole('link', { name })
const list = (page: Page) => page.locator('[data-needs-problems]')
const thread = (page: Page, iid: number) =>
  page.locator(`[data-problem-thread="issue-${iid}"], [data-problem-thread="${1000 + iid}"], [data-problem-thread="${2000 + iid}"]`)
const row = (page: Page, iid: number) =>
  list(page).locator(`[data-problem="issue-${iid}"], [data-problem="${1000 + iid}"], [data-problem="${2000 + iid}"]`)
const overflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
const pilotPOST = async (page: Page, posts: unknown[]) => {
  await page.route((url) => url.pathname === `/api/v1${pilotChatPath}`, async (r) => {
    if (r.request().method() !== 'POST') return r.fallback()
    posts.push(r.request().postDataJSON())
    return r.fulfill({ status: 200, json: { id: 530 } })
  })
}

for (const size of widths) {
  test(`a prompt link redirects to its problem thread@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}#prompt-Q3`, size)
    await expect(page).toHaveURL(/\/milestones\/14\/problems\/issue-0$/)
    await expect(tab(page, /^Needs you/)).toHaveAttribute('aria-current', 'page')
    await expect(row(page, 0)).toHaveAttribute('aria-current', 'page')
    await expect(thread(page, 0)).toBeVisible()
    expect(problems).toEqual([])
  })

  test(`the problem list shows each problem once with its status@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, { milestone: running })
    await expect(tab(page, /^Needs you · 1$/)).toBeVisible()
    await expect(list(page).locator('[data-problem]')).toHaveCount(1)
    await expect(row(page, 0)).toContainText('Waiting for you')
    await expect(thread(page, 0).locator('[data-prompt="Q3"]')).toHaveCount(1)
    await expect(page.getByRole('button', { name: 'Release SDK 4 first', exact: true })).toBeVisible()
    expect(await overflow(page)).toBeLessThanOrEqual(0)
    expect(problems).toEqual([])
  })

  test(`one bar only for pause+prompts@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, { milestone: stuck })
    const t = thread(page, 281)
    await expect(t.locator('[data-prompt]')).toHaveCount(1)
    await expect(t.locator('[data-pause-card]')).toHaveCount(0)
    expect(problems).toEqual([])
  })

  test(`mainRed pause prompt once in the bar@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, { milestone: mainRed })
    const t = thread(page, 0)
    await expect(t.locator('[data-prompt]')).toHaveCount(1)
    await expect(t.locator('[data-pause-card]')).toHaveCount(0)
    expect(problems).toEqual([])
  })

  test(`#558 cause + facts in the thread header@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, { milestone: incident558 })
    const t = thread(page, 558)
    await expect(t.locator('[data-problem-cause]')).toHaveText(`the run failed: ${incidentError}`)
    const facts = t.locator('[data-problem-facts]')
    await expect(facts.getByRole('link', { name: '!583' })).toBeVisible()
    await expect(facts).toContainText('run 1 failed')
    await expect(facts.getByRole('link', { name: 'log' })).toBeVisible()
    await expect(facts).toContainText('348 lines saved')
    await expect(facts).toContainText('#558 → #559 → #560')
    expect(await overflow(page)).toBeLessThanOrEqual(0)
    expect(problems).toEqual([])
  })

  test(`#558 consequences and retry warning@${size.width}`, async ({ page, baseURL }) => {
    let problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, { milestone: incident558 })
    await expect(thread(page, 558).locator('[data-consequence]').first()).toContainText('starts a new run')
    await expect(thread(page, 558).locator('[data-retry-warning]')).toHaveCount(0)
    expect(problems).toEqual([])
    problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, { milestone: incident558Dirty })
    await expect(thread(page, 558).locator('[data-retry-warning]').first()).toBeVisible()
    expect(await overflow(page)).toBeLessThanOrEqual(0)
    expect(problems).toEqual([])
  })

  test(`composer posts problem: #558 and shows sent@${size.width}`, async ({ page, baseURL }) => {
    const warms: string[] = [], posts: unknown[] = []
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, { milestone: incident558, warms })
    await pilotPOST(page, posts)
    const t = thread(page, 558)
    await t.getByRole('textbox').fill('retry with the saved WIP')
    await t.getByRole('textbox').press('Enter')
    await expect.poll(() => posts).toEqual([{ body: 'problem: #558\n\nretry with the saved WIP', clientKey: expect.any(String), kind: 'message' }])
    await expect(t.getByText('sent')).toBeVisible()
    expect(warms).toEqual([`${pilotPath}/warm`])
    expect(await overflow(page)).toBeLessThanOrEqual(0)
    expect(problems).toEqual([])
  })

  test(`milestone problem prefix and no-chat rights@${size.width}`, async ({ page, baseURL }) => {
    const posts: unknown[] = []
    let problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, { milestone: mainRed })
    await pilotPOST(page, posts)
    await thread(page, 0).getByRole('textbox').fill('revert it')
    await thread(page, 0).getByRole('textbox').press('Enter')
    await expect.poll(() => posts).toEqual([{ body: 'problem: milestone\n\nrevert it', clientKey: expect.any(String), kind: 'message' }])
    expect(problems).toEqual([])
    const warms: string[] = []
    const noChat: MilestoneView = { ...incident558, permissions: { ...incident558.permissions!, chat: false } }
    problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, { milestone: noChat, warms })
    await expect(thread(page, 558).getByRole('textbox')).toHaveCount(0)
    expect(warms).toEqual([])
    expect(problems).toEqual([])
  })

  test(`budget pause ways out; hold on #284@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, { milestone: budget })
    await expect(tab(page, /^Needs you · 2$/)).toBeVisible()
    await expect(thread(page, 0).locator('[data-pause-card]')).toHaveCount(1)
    await expect(thread(page, 0).locator('[data-prompt]')).toHaveCount(0)
    await row(page, 284).click()
    await expect(thread(page, 284).locator('[data-pause-card][data-hold="284"]')).toHaveCount(1)
    expect(problems).toEqual([])
  })

  test(`Pilot tab unchanged; proposals Accept/Reject@${size.width}`, async ({ page, baseURL }) => {
    let problems = await open(page, baseURL, `${milestonePath}?tab=pilot`, size)
    await expect(page.locator('[data-prompt]')).toHaveCount(0)
    await expect(page.locator('[data-pauses]')).toHaveCount(0)
    await expect(page.getByRole('textbox')).toBeVisible()
    expect(problems).toEqual([])
    problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size)
    const p7 = page.locator('[data-needs-proposals] [data-proposal="7"]')
    await expect(p7.getByRole('button', { name: 'Accept', exact: true })).toBeVisible()
    await expect(p7.getByRole('button', { name: 'Reject', exact: true })).toBeVisible()
    await expect(page.locator('[data-pauses]')).toBeVisible()
    expect(problems).toEqual([])
  })

  test(`hash links, stored, empty, history off@${size.width}`, async ({ page, baseURL }) => {
    let problems = await open(page, baseURL, `${milestonePath}#problem-280`, size)
    await expect(page).toHaveURL(/\/problems\/issue-280$/)
    await expect(thread(page, 280)).toBeVisible()
    expect(problems).toEqual([])
    problems = await open(page, baseURL, `${milestonePath}#prompt-Q6`, size)
    await expect(page).toHaveURL(/\/problems\/issue-280$/)
    expect(problems).toEqual([])
    problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, { milestone: stored })
    await expect(list(page).locator('[data-problem]').first()).toBeVisible()
    expect(problems).toEqual([])
    problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, { milestone: quiet })
    await expect(list(page)).toContainText('Nothing needs you.')
    expect(problems).toEqual([])
    problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, { milestone: running, history: false })
    await expect(thread(page, 0).locator('[data-history-off]')).toContainText('History is off')
    expect(problems).toEqual([])
  })

  test(`no reaction after 60 s with Task logs@${size.width}`, async ({ page, baseURL }) => {
    const posts: unknown[] = []
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, { milestone: incident558, history: false })
    await pilotPOST(page, posts)
    const t = thread(page, 558)
    await t.getByRole('textbox').fill('ping')
    await t.getByRole('textbox').press('Enter')
    await expect(t.getByText('sent')).toBeVisible()
    await page.clock.fastForward(60_000)
    await page.evaluate(() => undefined)
    await expect(t.getByText('No reaction from the operator yet.')).toBeVisible({ timeout: 10_000 })
    await expect(t.getByRole('link', { name: 'Task logs' })).toBeVisible()
    expect(problems).toEqual([])
  })

  // The thread is a chat: history oldest first, opened at its newest
  // message, the text box below the history.
  test(`thread reads like a chat@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, { milestone: incident558 })
    const t = thread(page, 558)
    const log = t.getByRole('log')
    const mine = log.getByText('retry with a JSON hint', { exact: true })
    const reply = log.getByText('I can retry #558 with a hint to answer in JSON.')
    await expect(reply).toBeVisible()
    const y = async (l: typeof log) => (await l.boundingBox())!.y
    expect(await y(mine), 'the owner\'s message above the newer reply').toBeLessThan(await y(reply))
    expect(await log.evaluate((e) => e.scrollHeight - e.scrollTop - e.clientHeight), 'scrolled to the newest').toBeLessThanOrEqual(2)
    const logBox = (await log.boundingBox())!
    const box = (await t.getByRole('textbox').boundingBox())!
    expect(box.y, 'text box below the history').toBeGreaterThanOrEqual(logBox.y + logBox.height - 1)
    expect(problems).toEqual([])
  })

  test(`list and thread layout@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, { milestone: incident558 })
    await expect(thread(page, 558)).toBeVisible()
    await expect(list(page)).toBeVisible()
    const layout = await page.evaluate(() => {
      const nav = document.querySelector('[data-needs-problems]')
      const th = document.querySelector('[data-problem-thread]')
      if (!nav || !th) return null
      const a = nav.getBoundingClientRect(), b = th.getBoundingClientRect()
      return {
        sideBySide: a.right <= b.left + 8 && Math.abs(a.top - b.top) < 80,
        stacked: b.top >= a.top + a.height - 8,
        tops: [a.top, b.top], rights: [a.right, b.left],
      }
    })
    expect(layout, 'list and thread mounted').not.toBeNull()
    if (size.width >= 1024) expect(layout!.sideBySide, JSON.stringify(layout)).toBe(true)
    else expect(layout!.stacked, JSON.stringify(layout)).toBe(true)
    expect(await overflow(page)).toBeLessThanOrEqual(0)
    expect(problems).toEqual([])
  })
}

for (const shot of [
  { name: 'needs-you-problem', size: widths[1]!, colorScheme: 'light' as const },
  { name: 'needs-you-problem-dark', size: widths[1]!, colorScheme: 'dark' as const },
  { name: 'needs-you-problem-375', size: widths[0]!, colorScheme: 'light' as const },
]) {
  test(`screenshot ${shot.name}`, async ({ page, baseURL }, testInfo) => {
    await page.emulateMedia({ colorScheme: shot.colorScheme })
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, shot.size, { milestone: incident558Dirty })
    const t = thread(page, 558)
    await expect(t.locator('[data-retry-warning]').first()).toBeVisible()
    const path = join(testInfo.project.outputDir, '..', 'screenshots', `${shot.name}.png`)
    await t.screenshot({ path })
    await testInfo.attach('screenshot', { path, contentType: 'image/png' })
    expect(problems).toEqual([])
  })
}
