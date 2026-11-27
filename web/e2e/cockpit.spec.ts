// The milestone Cockpit (R10 of "Desk: the Cockpit", R3 of "Desk: the
// Cockpit newest first by issue"): the pinned cards above the stream, one
// thread per issue with the newest on top and its folded groups closed, a
// group opened by keyboard, a problem link opening at its card, search,
// Expand all, the live line of a running issue (R11) under the pinned
// cards, axe. The composer, the conversation and Load older are
// cockpit-composer.spec.ts's.
// The fixture milestone %14 has a stuck issue (#281) and two merged ones
// (#279, #280). The live lines (R11) put #282's running implementer into
// it. Run with npm run e2e (built UI, Chromium).
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { journeyAt } from './journey'
import { answer, now } from './fixtures'
import { captures, capturesTranscript, refundsTitle } from './fixtures/task'
import type { MilestoneView } from '../src/generated/views'
import { ago } from './fixtures/clock'
import type { StreamEvent, StreamKind } from '../src/types'

const milestonePath = '/projects/team-a/shop/milestones/14'
// The Cockpit is a milestone's second tab (D1 of "Desk: Milestones and
// Issues first").
const cockpitPath = `${milestonePath}?tab=cockpit`

let nextID = 4000
let events: StreamEvent[] = []
const ev = (kind: StreamKind, minutes: number, issueIID: number, summary: string, data: StreamEvent['data'] = {}): StreamEvent => ({
  id: ++nextID,
  at: ago(minutes),
  namespace: 'team-a',
  project: 'shop',
  milestoneIID: 14,
  issueIID,
  task: `shop-${issueIID}-implement`,
  kind,
  summary,
  data,
})

// The stream oldest first: runs, CI and reviews fold into one group per
// issue between the merges.
const oldestFirst: StreamEvent[] = [
  ev('run.started', 80, 279, 'Run 1 started on #279'),
  ev('ci', 70, 279, 'Pipeline passed on #279', { state: 'success' }),
  ev('merged', 60, 279, 'Merged !303 Refund API', {}),
  ev('run.started', 50, 280, 'Run 1 started on #280'),
  ev('ci', 40, 280, 'Pipeline passed on #280', { state: 'success' }),
  ev('review', 35, 280, 'Review round 1: clean', { round: 1, blocking: 0 }),
  ev('merged', 30, 280, 'Merged !304 Partial captures', {}),
  ev('run.started', 20, 281, 'Run 2 started on #281'),
  ev('ci', 14, 281, 'Pipeline failed on #281', { state: 'failed' }),
  ev('run.finished', 10, 281, 'Run 2 needs input', { result: 'needsInput' }),
]
// splitIssue: #281's events split by a pilot action into two groups of
// one thread.
const splitIssue: StreamEvent[] = [
  ev('run.started', 20, 281, 'Run 2 started on #281'),
  ev('pilot.acted', 16, 281, 'Pilot asked #281 to retry'),
  ev('ci', 14, 281, 'Pipeline failed on #281', { state: 'failed' }),
]

// fiveItems: #281's thread with five items, newest first: a group, a pilot
// action, a group, a pilot action, a group.
const fiveItems: StreamEvent[] = [
  ev('run.started', 30, 281, 'Run 1 started on #281'),
  ev('pilot.acted', 28, 281, 'Pilot asked #281 to retry'),
  ev('ci', 26, 281, 'Pipeline failed on #281', { state: 'failed' }),
  ev('pilot.acted', 24, 281, 'Pilot asked #281 to retry again'),
  ev('run.started', 20, 281, 'Run 2 started on #281'),
]
// withRunning: the stream with an event of #282, whose implementer runs
// with Live.
const withRunning: StreamEvent[] = [...oldestFirst, ev('run.started', 4, 282, 'Run 3 started on #282')]

type Opened = { problems: string[]; searches: string[]; transcripts: string[] }
// Live: #282's implementer runs, and the viewer has the transcript grant
// (transcript) or not.
type Live = { transcript: boolean }

async function open(
  page: Page,
  baseURL: string | undefined,
  path: string,
  width: number,
  stream = oldestFirst,
  live?: Live,
): Promise<Opened> {
  events = [...stream].reverse()
  const origin = new URL(baseURL ?? '').origin
  const opened: Opened = { problems: [], searches: [], transcripts: [] }
  await page.setViewportSize({ width, height: 800 })
  await page.clock.install({ time: now })
  page.on('pageerror', (err) => opened.problems.push(err.message))
  await page.route((url) => url.protocol !== 'data:' && url.protocol !== 'blob:' && url.origin !== origin, (r) => r.abort('blockedbyclient'))
  await page.route('**/api/v1/**', async (r) => {
    const req = r.request()
    const url = new URL(req.url())
    const p = url.pathname.slice('/api/v1'.length)
    if (p === '/events') return r.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 3600000\n\n' })
    if (req.method() !== 'GET') {
      if (p.endsWith('/warm')) return r.fulfill({ status: 204 })
      opened.problems.push(`${req.method()} ${url.pathname}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    // The Cockpit's stream: every kind of %14 (the Activity tab asks with
    // runtime kinds only, without the chat ones), q narrows it.
    if (p === '/stream' && url.searchParams.get('milestone') === '14' && url.searchParams.get('kinds')?.includes('pilot.acted')) {
      if (url.searchParams.has('after')) return r.fulfill({ status: 200, json: { events: [], history: true } })
      const q = url.searchParams.get('q') ?? ''
      if (q) opened.searches.push(q)
      const found = events.filter((e) => !q || e.summary.toLowerCase().includes(q.toLowerCase()))
      return r.fulfill({ status: 200, json: { events: found, newest: `c-${events[0]!.id}`, history: true } })
    }
    if (live && p === `/projects/team-a/shop/tasks/${captures.name}/transcript`) {
      opened.transcripts.push(p)
      const body = capturesTranscript.map((e) => `event: entry\ndata: ${JSON.stringify(e)}\n\n`).join('')
      return r.fulfill({ status: 200, contentType: 'text/event-stream', body: `retry: 3600000\n\n${body}` })
    }
    let body = answer(p, url.searchParams)
    if (live && p === '/projects/team-a/shop/milestones/14') {
      const m = body as MilestoneView
      body = {
        ...m,
        tasks: [...m.tasks.filter((t) => t.issue?.iid !== 282), captures],
        permissions: { ...m.permissions, transcript: live.transcript },
      }
    }
    if (body === undefined) {
      opened.problems.push(`GET ${url.pathname}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    if (typeof body === 'string') return r.fulfill({ status: 200, contentType: 'text/plain; charset=utf-8', body })
    return r.fulfill({ status: 200, json: body })
  })
  await page.goto(path)
  return opened
}

const view = (page: Page) => page.locator('main').getByRole('navigation', { name: 'View' })
const pinned = (page: Page) => page.locator('main [data-cockpit-pinned]')
const stream = (page: Page) => page.locator('main [data-cockpit-stream]')
const groups = (page: Page) => stream(page).locator('[data-cockpit-group]')
const trigger = (page: Page, n: number) => groups(page).nth(n).getByRole('button').first()
// toggle opens or closes a group from its trigger by keyboard (Enter), the
// way the keyboard test does at every width.
const toggle = async (page: Page, n: number) => {
  await trigger(page, n).focus()
  await page.keyboard.press('Enter')
}
const row = (page: Page, iid: number) =>
  pinned(page).locator(`[data-problem="issue-${iid}"], [data-problem="${1000 + iid}"], [data-problem="${2000 + iid}"]`)

for (const width of [375, 1280]) {
  test(`the milestone opens on the Cockpit: pinned cards, then the threads newest on top with closed groups@${width}`, journeyAt(width), async ({ page, baseURL }) => {
    const { problems } = await open(page, baseURL, cockpitPath, width)
    await expect(view(page).getByRole('link', { name: /^Cockpit/ })).toHaveAttribute('aria-current', 'page')
    await expect(row(page, 281)).toBeVisible()
    await expect(groups(page)).toHaveCount(3)
    const pinnedBox = (await pinned(page).boundingBox())!
    const streamBox = (await stream(page).boundingBox())!
    expect(pinnedBox.y).toBeLessThan(streamBox.y)

    // One thread per issue, the newest activity on top: #281's group, then
    // #280's merge and group, then #279's merge and group.
    const threads = stream(page).locator('[data-cockpit-thread]')
    await expect(threads).toHaveCount(3)
    for (const [i, name] of [`#281 · ${refundsTitle}`, '#280 · Provider SDK 4 with refund support', '#279 · Refund reasons in the order history'].entries())
      await expect(threads.nth(i)).toHaveAttribute('aria-label', name)
    // The header: the issue's state from its row and queue lane (R4).
    await expect(threads.nth(0).locator('[data-cockpit-thread-state]')).toHaveText('Running')
    await expect(threads.nth(1).locator('[data-cockpit-thread-state]')).toHaveText('Merged')
    await expect(groups(page).getByRole('button')).toHaveText([
      /^#281 · 1 run, 1 CI round/,
      /^#280 · 1 run, 1 CI round, review clean/,
      /^#279 · 1 run, 1 CI round/,
    ])
    await expect(stream(page).locator('ol.items > li')).toHaveCount(5)
    await expect(threads.nth(1).locator('ol.items > li').first()).toContainText('Merged !304 Partial captures')
    await expect(threads.nth(2).locator('ol.items > li').first()).toContainText('Merged !303 Refund API')
    for (let i = 0; i < 3; i++) await expect(trigger(page, i)).toHaveAttribute('aria-expanded', 'false')
    await expect(stream(page).getByText('Pipeline failed on #281')).toHaveCount(0)
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0)
    expect(problems).toEqual([])
  })

  test(`a group opens with Enter and Space on its trigger@${width}`, async ({ page, baseURL }) => {
    const { problems } = await open(page, baseURL, cockpitPath, width)
    await trigger(page, 0).focus()
    await page.keyboard.press('Enter')
    await expect(trigger(page, 0)).toHaveAttribute('aria-expanded', 'true')
    await expect(groups(page).nth(0).getByText('Pipeline failed on #281')).toBeVisible()
    await expect(groups(page).nth(0).getByText('Run 2 needs input')).toBeVisible()
    await page.keyboard.press('Space')
    await expect(trigger(page, 0)).toHaveAttribute('aria-expanded', 'false')
    await trigger(page, 2).focus()
    await page.keyboard.press('Space')
    await expect(trigger(page, 2)).toHaveAttribute('aria-expanded', 'true')
    await expect(groups(page).nth(2).getByText('Pipeline passed on #279')).toBeVisible()
    expect(problems).toEqual([])
  })

  test(`two groups of one issue, split by a shown event, open on their own@${width}`, async ({ page, baseURL }) => {
    const { problems } = await open(page, baseURL, cockpitPath, width, splitIssue)
    await expect(groups(page)).toHaveCount(2)
    await toggle(page, 0)
    await expect(trigger(page, 0)).toHaveAttribute('aria-expanded', 'true')
    await expect(trigger(page, 1)).toHaveAttribute('aria-expanded', 'false')
    await stream(page).getByRole('switch', { name: 'Expand all' }).click()
    await toggle(page, 1)
    await expect(trigger(page, 1)).toHaveAttribute('aria-expanded', 'false')
    await expect(trigger(page, 0)).toHaveAttribute('aria-expanded', 'true')
    expect(problems).toEqual([])
  })

  // R4: a thread shows its newest 3 items, Show N more the rest; Expand
  // all opens the rest and every group.
  test(`a thread with 5 items shows 3, Show 2 more the rest, Expand all all of them@${width}`, async ({ page, baseURL }) => {
    const { problems } = await open(page, baseURL, cockpitPath, width, fiveItems)
    const thread = stream(page).locator('#issue-281')
    const items = thread.locator('ol.items > li')
    await expect(items).toHaveCount(3)
    await expect(items.nth(1)).toContainText('Pilot asked #281 to retry again')
    const more = thread.getByRole('button', { name: 'Show 2 more' })
    await expect(more).toHaveAttribute('aria-expanded', 'false')
    await more.focus()
    await page.keyboard.press('Enter')
    await expect(items).toHaveCount(5)
    await expect(items.nth(3)).toContainText('Pilot asked #281 to retry')
    await thread.getByRole('button', { name: 'Show fewer' }).click()
    await expect(items).toHaveCount(3)

    await stream(page).getByRole('switch', { name: 'Expand all' }).click()
    await expect(items).toHaveCount(5)
    for (let i = 0; i < 3; i++) await expect(trigger(page, i)).toHaveAttribute('aria-expanded', 'true')
    await expect(thread.getByText('Run 1 started on #281')).toBeVisible()
    expect(problems).toEqual([])
  })

  test(`#issue-<iid> scrolls to its thread and opens it fully@${width}`, async ({ page, baseURL }) => {
    // Without ?tab= the thread link opens the Cockpit and says so (D6).
    const { problems } = await open(page, baseURL, `${milestonePath}#issue-279`, width)
    await expect(page).toHaveURL(/\/milestones\/14\?tab=cockpit#issue-279$/)
    const thread = stream(page).locator('#issue-279')
    await expect(thread).toBeInViewport()
    await expect(page.locator('main #issue-281')).toHaveAttribute('aria-label', `#281 · ${refundsTitle}`)
    // A link on the page (a hash change) does the same.
    await page.evaluate(() => (location.hash = '#issue-281'))
    await expect(page.locator('main #issue-281')).toBeInViewport()
    expect(problems).toEqual([])
  })

  test(`a running issue's header shows its step, ?live=1 opens its Live transcript@${width}`, async ({ page, baseURL }) => {
    const opened = await open(page, baseURL, `${milestonePath}?live=1#issue-282`, width, withRunning, { transcript: true })
    await expect(page).toHaveURL(/\/milestones\/14\?live=1&tab=cockpit#issue-282$/)
    const thread = stream(page).locator('#issue-282')
    await expect(thread.locator('[data-cockpit-thread-step]')).toHaveText('Implementing · 4 min')
    await expect(thread).toHaveAttribute('aria-label', '#282 · Partial captures for split shipments')
    const live = thread.getByRole('button', { name: 'Live transcript' })
    await expect(live).toHaveAttribute('aria-expanded', 'true')
    await expect(thread.getByText('Switching to the retry helper.')).toBeVisible()
    const owner = thread.locator('[data-owner-message]')
    await expect(owner).toContainText('Message from @alice via the pilot')
    await expect(owner).toContainText('Use the existing retry helper.')
    await expect(owner).not.toContainText('Take this into account')
    await expect(thread).toBeInViewport()
    expect(opened.transcripts.length).toBeGreaterThan(0)
    // It closes and opens by keyboard.
    await live.focus()
    await page.keyboard.press('Space')
    await expect(live).toHaveAttribute('aria-expanded', 'false')
    expect(opened.problems).toEqual([])
  })

  test(`without the transcript grant a thread has no Live transcript@${width}`, async ({ page, baseURL }) => {
    const opened = await open(page, baseURL, `${milestonePath}?live=1#issue-282`, width, withRunning, { transcript: false })
    const thread = stream(page).locator('#issue-282')
    await expect(thread.locator('[data-cockpit-thread-step]')).toHaveText('Implementing · 4 min')
    await expect(thread.getByRole('button', { name: 'Live transcript' })).toHaveCount(0)
    expect(opened.transcripts).toEqual([])
    expect(opened.problems).toEqual([])
  })

  test(`Expand all opens every group, search narrows the stream@${width}`, async ({ page, baseURL }) => {
    const opened = await open(page, baseURL, cockpitPath, width)
    await expect(groups(page)).toHaveCount(3)
    await stream(page).getByRole('switch', { name: 'Expand all' }).click()
    for (let i = 0; i < 3; i++) await expect(trigger(page, i)).toHaveAttribute('aria-expanded', 'true')
    await expect(stream(page).getByText('Review round 1: clean')).toBeVisible()

    const search = stream(page).getByRole('searchbox', { name: 'Search the Cockpit' })
    await search.fill('merged')
    await search.press('Enter')
    await expect(stream(page).locator('ol.items > li')).toHaveCount(2)
    await expect(groups(page)).toHaveCount(0)
    expect(opened.searches).toEqual(['merged'])
    await search.fill('')
    await search.press('Enter')
    await expect(groups(page)).toHaveCount(3)
    expect(opened.problems).toEqual([])
  })

  for (const [name, path] of [
    ['the problem route', `${milestonePath}/problems/issue-281`],
    ['a #problem- link', `${milestonePath}#problem-281`],
  ] as const) {
    test(`${name} opens the Cockpit at its card@${width}`, journeyAt(width), async ({ page, baseURL }) => {
      const { problems } = await open(page, baseURL, path, width)
      await expect(page).toHaveURL(/\/milestones\/14\/problems\/issue-281$/)
      await expect(view(page).getByRole('link', { name: /^Cockpit/ })).toHaveAttribute('aria-current', 'page')
      await expect(row(page, 281)).toHaveAttribute('aria-current', 'page')
      await expect(row(page, 281)).toBeInViewport()
      await expect(page.locator('main [data-problem-thread]').first()).toBeVisible()
      await expect(groups(page)).toHaveCount(3)
      expect(problems).toEqual([])
    })
  }

  // A running issue loads no transcript unasked (R7 of "Desk: the
  // Cockpit newest first by issue"): #282's running implementer is in the
  // Now & next sidebar (milestone-panel.spec.ts) and its thread.
  test(`a running issue loads no transcript in the Cockpit@${width}`, async ({ page, baseURL }) => {
    const opened = await open(page, baseURL, cockpitPath, width, oldestFirst, { transcript: true })
    await expect(groups(page)).toHaveCount(3)
    await expect(pinned(page)).toBeVisible()
    expect(opened.transcripts).toEqual([])
    expect(opened.problems).toEqual([])
  })

  // R14: a removed milestone tab's old link opens where its content moved,
  // and the address shows the new place (D789-P1).
  for (const [old, want, check] of [
    ['activity', '?tab=cockpit', '[data-cockpit-stream]'],
    ['stream', '?tab=cockpit', '[data-cockpit-stream]'],
    ['needs', '?tab=cockpit', '[data-cockpit-pinned]'],
    ['pilot', '?tab=cockpit', '[data-cockpit-composer]'],
    ['board', '?tab=issues&view=board', '[data-board]'],
  ] as const) {
    test(`?tab=${old} opens ${want}@${width}`, async ({ page, baseURL }) => {
      const { problems } = await open(page, baseURL, `${milestonePath}?tab=${old}`, width)
      await expect(page).toHaveURL(new URL(`${milestonePath}${want}`, baseURL).href)
      await expect(page.locator(check).first()).toBeVisible()
      const tab = want.startsWith('?tab=issues') ? 'Issues' : 'Cockpit'
      await expect(page.getByRole('navigation', { name: 'View' }).getByRole('link', { name: new RegExp(`^${tab}`) })).toHaveAttribute('aria-current', 'page')
      if (old === 'board') await expect(page.locator('[data-issues-view-switch]').getByRole('tab', { name: 'Board' })).toHaveAttribute('aria-selected', 'true')
      expect(problems).toEqual([])
    })
  }

  // axe (WCAG 2.2 AA) on the whole Cockpit with a group open, light and
  // dark. color-contrast is left out: it is the rule of R6 (#641) that no
  // requirement of this milestone covers, listed in known-a11y.ts for the
  // pinned cards and the event cards (stream-milestone-cockpit, once
  // stream-milestone-needs and -activity) the Cockpit reuses unchanged; every other
  // rule must pass, and no known-a11y entry is added.
  for (const colorScheme of ['light', 'dark'] as const) {
    test(`axe is green on the Cockpit in ${colorScheme}@${width}`, async ({ page, baseURL }) => {
      await page.emulateMedia({ colorScheme })
      const { problems } = await open(page, baseURL, cockpitPath, width, oldestFirst, { transcript: true })
      await expect(groups(page)).toHaveCount(3)
      await expect(row(page, 281)).toBeVisible()
      await toggle(page, 0)
      await expect(groups(page).nth(0).getByText('Pipeline failed on #281')).toBeVisible()
      const results = await new AxeBuilder({ page })
        .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
        .disableRules(['color-contrast'])
        .analyze()
      const violations = results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)
      expect(violations).toEqual([])
      expect(problems).toEqual([])
    })
  }
}
