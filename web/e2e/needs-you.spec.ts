// The Needs you milestone tab (#530: R12): prompt links open it, an open
// prompt shows once, the pause card's own prompt is not listed again, and
// the Pilot tab keeps only the pilot line, the composer and its events.
// Handled (#531: R14) lists one problem per issue with its prompts, the
// pilot's actions and its pauses; Open (#581) shows one card per open
// problem with its pause, hold and prompts; the card starts with the cause
// line and the operator's facts (#582), shown on the #558 incident, and its
// options with their consequences and the retry warning (#583).
// Run with npm run e2e (built UI, Chromium).
import { join } from 'node:path'
import { expect, test, type Page } from '@playwright/test'
import { answer, now } from './fixtures'
import { incident558, incident558Dirty, incidentError } from './fixtures/problem'
import { paymentsMilestone } from './fixtures/stream'
import { refundsPrompt, refundsTitle } from './fixtures/task'
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
  autopilot: { ...paymentsMilestone.autopilot!, holds: [], pauseKind: 'mainRed', pausedFor: undefined, pauseIssue: undefined, exits: [] },
  pilot: { ...paymentsMilestone.pilot!, proposals: [] },
  prompts: [q5, openQ3],
}

// stuck: milestone 14 paused for #281 (a cycle) while #281 is held, with
// two open pilot prompts about it: one problem (Decision 1).
const stuckPrompt = (id: string): PromptView => ({ ...openQ3, id, issueIID: 281, kind: 'assessment', question: `${id}: rerun #281?` })
const stuck: MilestoneView = {
  ...paymentsMilestone,
  autopilot: {
    ...paymentsMilestone.autopilot!,
    pauseKind: 'stuck',
    pausedFor: undefined,
    pauseIssue: 281,
    holds: [{ ...paymentsMilestone.autopilot!.holds![0]!, iid: 281, title: refundsTitle, kind: 'needsInput' }],
  },
  pilot: { ...paymentsMilestone.pilot!, proposals: [] },
  prompts: [stuckPrompt('Q8'), stuckPrompt('Q9')],
}

// budget: milestone 14 paused on its budget (no single issue) while #284
// is held: two problems, the milestone's and #284's.
const budget: MilestoneView = {
  ...paymentsMilestone,
  autopilot: { ...paymentsMilestone.autopilot!, pauseKind: 'budget', pausedFor: undefined, pauseIssue: undefined },
  pilot: { ...paymentsMilestone.pilot!, proposals: [] },
}

// staleHead: #280 got a new push, so the pilot's count starts over.
const staleHead: MilestoneView = {
  ...paymentsMilestone,
  pilot: { ...paymentsMilestone.pilot!, interventions: [{ issueIID: 280, used: 0, max: 2 }] },
}

// stored: milestone 14 from the history database: no pilot, no prompts,
// only its pauses.
const stored: MilestoneView = { ...paymentsMilestone, state: 'done', stored: true, autopilot: undefined, pilot: undefined, prompts: [] }

// quiet: milestone 14 running with nothing asked, acted or paused.
const quiet: MilestoneView = { ...running, prompts: [], pilot: { ...running.pilot!, acted: [], interventions: [] }, pauses: undefined }

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
    await expect(page.locator('[data-needs-handled] #problem-0')).toHaveAttribute('data-open', '')
    await expect(tab(page, /^Needs you/)).toHaveAttribute('aria-current', 'page')
    expect(problems).toEqual([])
  })

  test(`one open prompt shows once, the label counts it@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, running)
    await expect(tab(page, /^Needs you/)).toHaveText('Needs you · 1')
    const tabs = await strip(page).getByRole('link').allTextContents()
    expect(tabs.map((t) => t.trim())).toEqual(['Issues', 'Needs you · 1', 'Decisions', 'Pilot', 'Retrospective', 'Stream', 'Board', 'Spend'])
    await expect(page.locator('[data-needs-open] [data-prompt="Q3"]')).toHaveCount(1)
    await expect(page.locator('[data-prompt="Q3"]')).toHaveCount(1)
    await expect(page.getByRole('button', { name: 'Release SDK 4 first', exact: true })).toHaveCount(1)
    expect(problems).toEqual([])
  })

  test(`a paused milestone lists the pause card's prompt once@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, mainRed)
    const needs = page.locator('[data-needs-open]')
    await expect(needs.locator('[data-open-problem]')).toHaveCount(1)
    await expect(needs.locator('#problem-0 [data-problem-head]')).toContainText('Milestone')
    await expect(needs.locator('[data-pause-card]')).toHaveCount(1)
    await expect(page.locator('[data-prompt="Q5"]')).toHaveCount(1)
    await expect(needs.locator('[data-pause-card] [data-prompt="Q5"]')).toHaveCount(1)
    await expect(page.locator('[data-prompt="Q3"]')).toHaveCount(1)
    // The pause card and Q3 are the milestone's one problem.
    await expect(needs.locator('#problem-0 [data-prompt="Q3"]')).toHaveCount(1)
    await expect(tab(page, /^Needs you/)).toHaveText('Needs you · 1')
    expect(problems).toEqual([])
  })

  test(`a pause, a hold and two prompts on one issue are one card@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, stuck)
    const needs = page.locator('[data-needs-open]')
    await expect(tab(page, /^Needs you/)).toHaveText('Needs you · 1')
    await expect(needs.locator('[data-open-problem]')).toHaveCount(1)
    const card = needs.locator('#problem-281')
    await expect(card.locator('[data-problem-head]')).toContainText(`#281 ${refundsTitle}`)
    // The pause and the hold, then both prompts, each once on the page.
    await expect(card.locator('[data-pause-card]')).toHaveCount(2)
    await expect(card.locator('[data-prompt]')).toHaveCount(2)
    await expect(page.locator('[data-prompt="Q8"]')).toHaveCount(1)
    await expect(page.locator('[data-prompt="Q9"]')).toHaveCount(1)
    // The history is folded; Handled still lists the problem, without the anchor.
    const history = card.locator('[data-problem-history]')
    await expect(history.locator('[data-entry]').first()).toBeHidden()
    await history.locator('summary').click()
    await expect(history.locator('[data-entry="prompt"]')).toHaveCount(2)
    await expect(page.locator('[data-needs-handled] [data-problem="281"]')).not.toHaveAttribute('id')
    await expect(page.locator('#problem-281')).toHaveCount(1)
    expect(problems).toEqual([])
  })

  test(`the #558 card starts with the cause and the facts@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, incident558)
    const card = page.locator('[data-needs-open] #problem-558')
    await expect(page.locator('[data-needs-open] [data-open-problem]')).toHaveCount(1)
    await expect(card.locator('[data-problem-cause]')).toHaveText(`the run failed: ${incidentError}`)
    await expect(card).not.toContainText('CI is still red, the run failed or the agent asked a question')
    // The stuck pause's own reason is not the run's: it stays.
    await expect(card.locator('[data-pause-card]:not([data-hold])')).toContainText('#560 waits for another pending issue')
    const facts = card.locator('[data-problem-facts]')
    await expect(facts.locator('[data-fact]')).toHaveCount(5)
    await expect(facts.getByRole('link', { name: '#558', exact: true })).toHaveAttribute('href', incident558.problems[0]!.issue.url)
    await expect(facts.locator('[data-fact="mr"]')).toHaveText(/^!583\s*· 80cd646a · 1 commit ahead · pushed 20 minutes ago$/)
    await expect(facts.getByRole('link', { name: '!583' })).toHaveAttribute('href', incident558.problems[0]!.mergeRequest!.url)
    await expect(facts.locator('[data-fact="run"]')).toContainText(`run 1 failed: ${incidentError}`)
    await expect(facts.getByRole('link', { name: 'failure note' })).toHaveAttribute('href', /#note_4263$/)
    await expect(facts.getByRole('link', { name: 'log', exact: true })).toHaveAttribute('href', '/projects/team-a/shop/tasks/shop-m14-558?tab=logs')
    await expect(facts.locator('[data-fact="saved"]')).toHaveText('348 lines saved as WIP commit 80cd646a')
    await expect(facts.locator('[data-fact="blocked"]')).toHaveText('#558 → #559 → #560')
    await expect(facts.locator('[data-fact="pipeline"]')).toHaveCount(0)
    // The cause and the facts come before the pause cards and the prompts.
    const order = await card.locator('[data-problem-cause], [data-problem-facts], [data-pause-card], [data-prompt]').evaluateAll((es) =>
      es.map((e) => [...e.attributes].map((a) => a.name).find((n) => n.startsWith('data-problem-') || n === 'data-pause-card' || n === 'data-prompt')),
    )
    expect(order.slice(0, 3)).toEqual(['data-problem-cause', 'data-problem-facts', 'data-pause-card'])
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
    expect(problems).toEqual([])
  })

  test(`the #558 options show their consequences and the retry warning@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, incident558)
    const card = page.locator('[data-needs-open] #problem-558')
    const q1 = card.locator('[data-prompt="Q1"]')
    await expect(q1.locator('[data-consequence]')).toHaveText([
      'starts a new run from the saved work, about $0.50',
      '#559 and #560 wait until you unskip it',
    ])
    // The work is saved as a WIP commit: no warning.
    await expect(card.locator('[data-retry-warning]')).toHaveCount(0)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
    expect(problems).toEqual([])
  })

  test(`a retry warns when the run left uncommitted lines@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, incident558Dirty)
    const card = page.locator('[data-needs-open] #problem-558')
    // Both prompts' retry option, not the skip option.
    const warnings = card.locator('[data-retry-warning]')
    await expect(warnings).toHaveCount(2)
    await expect(warnings.first()).toHaveText('⚠ Retry resets the workspace: 348 uncommitted lines that are not on the branch are lost.')
    await expect(card.locator('[data-prompt="Q1"] li').filter({ hasText: 'Skip #558' }).locator('[data-retry-warning]')).toHaveCount(0)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
    expect(problems).toEqual([])
  })

  test(`a budget pause is a card of its own@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, budget)
    const needs = page.locator('[data-needs-open]')
    // The milestone's pause, #284's hold and #281's open question.
    await expect(needs.locator('[data-open-problem]')).toHaveCount(3)
    const ids = await needs.locator('[data-open-problem]').evaluateAll((es) => es.map((e) => e.id).sort())
    expect(ids).toEqual(['problem-0', 'problem-281', 'problem-284'])
    await expect(needs.locator('#problem-0 [data-problem-head]')).toContainText('Milestone')
    await expect(needs.locator('#problem-0 [data-pause-card]')).toHaveCount(1)
    await expect(needs.locator('#problem-284 [data-pause-card]')).toHaveCount(1)
    await expect(needs.locator('#problem-281 [data-pause-card]')).toHaveCount(0)
    await expect(tab(page, /^Needs you/)).toHaveText('Needs you · 3')
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
    // #281 (its pause and question), #284's hold and P7.
    await expect(tab(page, /^Needs you/)).toHaveText('Needs you · 3')
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

  test(`Handled has one problem per issue, open ones first@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size)
    const handled = page.locator('[data-needs-handled]')
    // #281 waits for an answer and #284 is held; the milestone's newest
    // entry is newer than #280's.
    await expect(handled.locator('[data-problem]')).toHaveCount(4)
    const ids = await handled.locator('[data-problem]').evaluateAll((es) => es.map((e) => e.getAttribute('data-problem')))
    expect(ids).toEqual(['281', '284', '0', '280'])
    // The open ones' anchors are on their cards under Open.
    await expect(page.locator('[data-needs-open] #problem-281')).toHaveCount(1)
    await expect(page.locator('[data-needs-open] #problem-284')).toHaveCount(1)

    const p281 = handled.locator('[data-problem="281"]')
    await expect(p281).toHaveAttribute('data-open', '')
    await expect(p281.locator('[data-problem-head]')).toContainText('Open')
    await expect(p281.locator('[data-attempts]')).toHaveText('Pilot attempts: 0 of 2')
    // The open prompt is a row that leads to its card under Open.
    const row = p281.locator('[data-entry="prompt"]')
    await expect(row).toContainText('Waiting for an answer')
    await expect(row.getByRole('link', { name: refundsPrompt.id })).toHaveAttribute('href', new RegExp(`#prompt-${refundsPrompt.id}$`))
    await expect(handled.locator('[data-entry="prompt"] button')).toHaveCount(0)
    await expect(page.locator(`[data-prompt="${refundsPrompt.id}"]`)).toHaveCount(1)
    await expect(page.getByRole('button', { name: 'Split across captures', exact: true })).toHaveCount(1)
    await expect(p281.locator('[data-entry="pause"]')).toContainText('The agent needs input')
    await expect(p281.locator('[data-entry="pause"]')).toContainText('since')

    // The milestone: the accepted action that ran nothing, then Q3, Q1, Q2.
    const m = handled.locator('#problem-0')
    await expect(m).not.toHaveAttribute('data-open')
    await expect(m.locator('[data-problem-head]')).toContainText('Milestone')
    await expect(m.locator('[data-problem-head]')).toContainText('Handled')
    await expect(m.locator('[data-entry]').first()).toBeHidden()
    await m.getByRole('button', { name: 'Entries of the milestone' }).click()
    await expect(m.locator('[data-entry]')).toHaveCount(4)
    expect(await m.locator('[data-entry]').evaluateAll((es) => es.map((e) => e.getAttribute('data-entry')))).toEqual(['acted', 'prompt', 'prompt', 'prompt'])
    await expect(m.locator('[data-entry="acted"]')).toContainText('@jo')
    await expect(m.locator('[data-entry="acted"]')).toContainText('ran none of its actions')
    await expect(m.locator('[data-entry="prompt"] summary > code')).toHaveText(['Q3', 'Q1', 'Q2'])

    // The pause totals stay as one line at the end.
    await expect(handled.locator('[data-pauses]')).toHaveText('Pauses: paused 1h 6m in 2 pauses · mean 42m to resume')
    expect(problems).toEqual([])
  })

  test(`#280 shows the pilot's two attempts, its questions and its pause@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size)
    const p = page.locator('[data-needs-handled] #problem-280')
    const head = p.locator('[data-problem-head]')
    await expect(head.getByRole('link', { name: '#280 Provider SDK 4 with refund support' })).toHaveAttribute('href', '/projects/team-a/shop/milestones/14/issues/280')
    await expect(head).toContainText('Handled')
    await expect(p.locator('[data-attempts]')).toHaveText('Pilot attempts: 2 of 2')
    const toggle = p.getByRole('button', { name: 'Entries of #280' })
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await toggle.click()
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await expect(p).toHaveAttribute('data-open', '')

    // Newest first: Q4, the pause, the restart, the retry, Q6.
    const entries = p.locator('[data-entry]')
    expect(await entries.evaluateAll((es) => es.map((e) => e.getAttribute('data-entry')))).toEqual(['prompt', 'pause', 'acted', 'acted', 'prompt'])
    await expect(entries.nth(0)).toContainText('Q4')
    await expect(entries.nth(0)).toContainText('The pipeline of #280 failed twice. Skip #280?')
    await expect(entries.nth(0)).toContainText('Resolved on its own: the pause ended')
    await expect(entries.nth(1)).toContainText('CI is red')
    await expect(entries.nth(1)).toContainText('resolved by self-healed via push')
    await expect(entries.nth(2)).toContainText('Pilot')
    await expect(entries.nth(2)).toContainText('Restart the run of #280')
    await expect(entries.nth(2)).toContainText('The retried job failed again in the same test.')
    await expect(entries.nth(2).locator('strong')).toHaveText('restartRun')
    await expect(entries.nth(2)).toContainText('restartRun #280: started run 3')
    await expect(entries.nth(2)).not.toContainText('ran none')
    // Summary and why are text: the backticks stay.
    await expect(entries.nth(3)).toContainText('Retry the failed job of !303')
    await expect(entries.nth(3)).toContainText('`test:integration-payments` timed out; it looks flaky.')
    await expect(entries.nth(4)).toContainText('Answered by jo on GitLab: Keep it for one release')

    // A closed prompt's row unfolds its full closed card.
    const q4 = page.locator('#prompt-Q4')
    await expect(q4).toBeHidden()
    await entries.nth(0).locator('summary').first().click()
    await expect(q4).toBeVisible()
    await expect(q4).toHaveAttribute('data-prompt', 'Q4')
    await expect(q4.getByText('Options (2)')).toBeVisible()
    await expect(q4.getByRole('button')).toHaveCount(0)
    expect(problems).toEqual([])
  })

  test(`a problem link opens Needs you at the problem@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}#problem-280`, size)
    await expect(tab(page, /^Needs you/)).toHaveAttribute('aria-current', 'page')
    const p = page.locator('#problem-280')
    await expect(p).toHaveAttribute('data-open', '')
    await expect(p).toBeInViewport()
    await expect(p.locator('[data-entry="acted"]').first()).toBeVisible()
    expect(problems).toEqual([])
  })

  test(`a prompt link unfolds its problem and its card@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}#prompt-Q6`, size)
    await expect(page.locator('#problem-280')).toHaveAttribute('data-open', '')
    await expect(page.locator('#prompt-Q6')).toBeVisible()
    await expect(page.locator('#prompt-Q6')).toBeInViewport()
    await expect(page.locator('#prompt-Q4')).toBeHidden()
    expect(problems).toEqual([])
  })

  test(`a new push to #280 shows its attempts as 0 of 2@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, staleHead)
    await expect(page.locator('#problem-280 [data-attempts]')).toHaveText('Pilot attempts: 0 of 2')
    expect(problems).toEqual([])
  })

  test(`a stored milestone shows Handled only, without attempts@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, stored)
    await expect(page.locator('[data-needs-handled] [data-problem]').first()).toBeVisible()
    await expect(page.locator('[data-needs-open]')).toHaveCount(0)
    await expect(page.locator('[data-attempts]')).toHaveCount(0)
    expect(problems).toEqual([])
  })

  test(`a milestone without problems says nothing was handled@${size.width}`, async ({ page, baseURL }) => {
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, size, quiet)
    await expect(page.locator('[data-needs-handled]')).toContainText('Nothing handled yet.')
    await expect(page.locator('[data-problem]')).toHaveCount(0)
    expect(problems).toEqual([])
  })
}

// The full #558 problem card in light, dark and at 375 px (R11), with the
// retry warning: screenshots/needs-you-problem{,-dark,-375}.png.
const shots = [
  { name: 'needs-you-problem', size: widths[1]!, colorScheme: 'light' as const },
  { name: 'needs-you-problem-dark', size: widths[1]!, colorScheme: 'dark' as const },
  { name: 'needs-you-problem-375', size: widths[0]!, colorScheme: 'light' as const },
]
for (const shot of shots) {
  test(`screenshot ${shot.name}`, async ({ page, baseURL }, testInfo) => {
    await page.emulateMedia({ colorScheme: shot.colorScheme })
    const problems = await open(page, baseURL, `${milestonePath}?tab=needs`, shot.size, incident558Dirty)
    const card = page.locator('[data-needs-open] #problem-558')
    await expect(card.locator('[data-retry-warning]').first()).toBeVisible()
    const path = join(testInfo.project.outputDir, '..', 'screenshots', `${shot.name}.png`)
    await card.screenshot({ path })
    await testInfo.attach('screenshot', { path, contentType: 'image/png' })
    expect(problems).toEqual([])
  })
}
