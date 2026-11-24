// The milestone Cockpit (R10 of "Desk: the Cockpit"): the pinned cards
// first, then the stream with its folded groups closed, a group opened by
// keyboard, a problem link opening at its card, search, Expand all, the
// live line of a running issue (R11), axe.
// The fixture milestone %14 has a stuck issue (#281) and two merged ones
// (#279, #280). The live lines (R11) put #282's running implementer into
// it. Run with npm run e2e (built UI, Chromium).
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { journeyAt } from './journey'
import { answer, now } from './fixtures'
import { captures, capturesTranscript } from './fixtures/task'
import type { MilestoneView } from '../src/generated/views'
import { ago } from './fixtures/clock'
import type { StreamEvent, StreamKind } from '../src/types'

const milestonePath = '/projects/team-a/shop/milestones/14'

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
// splitIssue: #281's events split by a pause into two groups of one key.
const splitIssue: StreamEvent[] = [
  ev('run.started', 20, 281, 'Run 2 started on #281'),
  { ...ev('autopilot.paused', 16, 281, 'Autopilot paused: issue #281 needs input'), issueIID: undefined },
  ev('ci', 14, 281, 'Pipeline failed on #281', { state: 'failed' }),
]

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
const liveLines = (page: Page) => page.locator('main [data-cockpit-live]')
const row = (page: Page, iid: number) =>
  pinned(page).locator(`[data-problem="issue-${iid}"], [data-problem="${1000 + iid}"], [data-problem="${2000 + iid}"]`)

for (const width of [375, 1280]) {
  test(`the milestone opens on the Cockpit: pinned cards first, then the stream with closed groups@${width}`, journeyAt(width), async ({ page, baseURL }) => {
    const { problems } = await open(page, baseURL, milestonePath, width)
    await expect(view(page).getByRole('link', { name: /^Cockpit/ })).toHaveAttribute('aria-current', 'page')
    await expect(row(page, 281)).toBeVisible()
    await expect(groups(page)).toHaveCount(3)
    const pinnedBox = (await pinned(page).boundingBox())!
    const streamBox = (await stream(page).boundingBox())!
    expect(pinnedBox.y).toBeLessThan(streamBox.y)

    // Oldest first, newest at the bottom: #279's group, its merge, #280's
    // group and merge, then #281's group.
    await expect(groups(page).getByRole('button')).toHaveText([
      /^#279 · 1 run, 1 CI round/,
      /^#280 · 1 run, 1 CI round, review clean/,
      /^#281 · 1 run, 1 CI round/,
    ])
    const items = stream(page).locator('ol.items > li')
    await expect(items).toHaveCount(5)
    await expect(items.nth(1)).toContainText('Merged !303 Refund API')
    await expect(items.nth(3)).toContainText('Merged !304 Partial captures')
    for (let i = 0; i < 3; i++) await expect(trigger(page, i)).toHaveAttribute('aria-expanded', 'false')
    await expect(stream(page).getByText('Pipeline failed on #281')).toHaveCount(0)
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0)
    expect(problems).toEqual([])
  })

  test(`a group opens with Enter and Space on its trigger@${width}`, async ({ page, baseURL }) => {
    const { problems } = await open(page, baseURL, milestonePath, width)
    await trigger(page, 2).focus()
    await page.keyboard.press('Enter')
    await expect(trigger(page, 2)).toHaveAttribute('aria-expanded', 'true')
    await expect(groups(page).nth(2).getByText('Pipeline failed on #281')).toBeVisible()
    await expect(groups(page).nth(2).getByText('Run 2 needs input')).toBeVisible()
    await page.keyboard.press('Space')
    await expect(trigger(page, 2)).toHaveAttribute('aria-expanded', 'false')
    await trigger(page, 0).focus()
    await page.keyboard.press('Space')
    await expect(trigger(page, 0)).toHaveAttribute('aria-expanded', 'true')
    await expect(groups(page).nth(0).getByText('Pipeline passed on #279')).toBeVisible()
    expect(problems).toEqual([])
  })

  test(`two groups of one issue, split by a shown event, open on their own@${width}`, async ({ page, baseURL }) => {
    const { problems } = await open(page, baseURL, milestonePath, width, splitIssue)
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

  test(`Expand all opens every group, search narrows the stream@${width}`, async ({ page, baseURL }) => {
    const opened = await open(page, baseURL, milestonePath, width)
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

  // The live lines (R11): #282's running implementer is one line between
  // the stream and the composer, and the finished runs of #280 and #281
  // have none.
  test(`a running issue's line opens its transcript with the owner's message, by keyboard@${width}`, async ({ page, baseURL }) => {
    const opened = await open(page, baseURL, milestonePath, width, oldestFirst, { transcript: true })
    await expect(liveLines(page)).toHaveCount(1)
    const line = liveLines(page).getByRole('button', { name: '#282 implementing · 4 min' })
    await expect(line).toHaveAttribute('aria-expanded', 'false')
    const streamBox = (await stream(page).boundingBox())!
    const lineBox = (await line.boundingBox())!
    expect(lineBox.y).toBeGreaterThan(streamBox.y)
    // Closed, the line opens no transcript stream.
    expect(opened.transcripts).toEqual([])

    await line.focus()
    await page.keyboard.press('Enter')
    await expect(line).toHaveAttribute('aria-expanded', 'true')
    const owner = liveLines(page).locator('[data-owner-message]')
    await expect(owner).toContainText('Message from @alice via the pilot')
    await expect(owner).toContainText('Use the existing retry helper.')
    await expect(owner).not.toContainText('Take this into account')
    await expect(liveLines(page).getByText('Switching to the retry helper.')).toBeVisible()
    await page.keyboard.press('Space')
    await expect(line).toHaveAttribute('aria-expanded', 'false')
    await page.keyboard.press('Space')
    await expect(line).toHaveAttribute('aria-expanded', 'true')
    expect(opened.problems).toEqual([])
  })

  test(`without the transcript grant the line links to the task@${width}`, async ({ page, baseURL }) => {
    const { problems } = await open(page, baseURL, milestonePath, width, oldestFirst, { transcript: false })
    const link = liveLines(page).getByRole('link', { name: '#282 implementing · 4 min' })
    await expect(link).toHaveAttribute('href', `/projects/team-a/shop/tasks/${captures.name}`)
    await expect(liveLines(page).getByRole('button')).toHaveCount(0)
    await expect(liveLines(page).locator('[data-owner-message]')).toHaveCount(0)
    expect(problems).toEqual([])
  })

  test(`without a running issue the Cockpit has no live line@${width}`, async ({ page, baseURL }) => {
    const { problems } = await open(page, baseURL, milestonePath, width)
    await expect(groups(page)).toHaveCount(3)
    await expect(liveLines(page)).toHaveCount(0)
    expect(problems).toEqual([])
  })

  // axe (WCAG 2.2 AA) on the whole Cockpit with a group open, light and
  // dark. color-contrast is left out: it is the rule of R6 (#641) that no
  // requirement of this milestone covers, listed in known-a11y.ts for the
  // pinned cards (stream-milestone-needs) and the event cards
  // (stream-milestone-activity) the Cockpit reuses unchanged; every other
  // rule must pass, and no known-a11y entry is added.
  for (const colorScheme of ['light', 'dark'] as const) {
    test(`axe is green on the Cockpit in ${colorScheme}@${width}`, async ({ page, baseURL }) => {
      await page.emulateMedia({ colorScheme })
      const { problems } = await open(page, baseURL, milestonePath, width, oldestFirst, { transcript: true })
      await expect(groups(page)).toHaveCount(3)
      await expect(row(page, 281)).toBeVisible()
      await toggle(page, 0)
      // and #282's live line open on its transcript.
      await liveLines(page).getByRole('button').click()
      await expect(liveLines(page).locator('[data-owner-message]')).toBeVisible()
      await expect(groups(page).nth(0).getByText('Pipeline passed on #279')).toBeVisible()
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
