// The project's Loose issues tab (LooseIssues.vue, R8 of "Loose issues"):
// ticking issues without a milestone and handing them to autopilot posts
// one take command to the Queue task, its answer shows as a toast and the
// queue shows them after the reload; the ⋯ menu posts drop, resume and
// merge; without the take permission no action shows and a note names who
// may act. Run with npm run e2e (built UI, Chromium).
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { answer, now } from './fixtures'
import { looseIssues, looseItems, looseOpenIssues } from './fixtures/loose'
import { shopDetail } from './fixtures/stream'
import type { LooseIssues } from '../src/generated/views'

const projectPath = '/projects/team-a/shop'
const chatPath = `${projectPath}/tasks/shop-loose-issues/chat`

// Server is what the fake API answers: the view, the Queue task's command
// posts and the operator's reply to each (by post index).
interface Server {
  view: LooseIssues
  replies: string[]
  posts: string[]
  problems: string[]
}

const server = (view: LooseIssues = looseIssues): Server => ({ view, replies: [], posts: [], problems: [] })

async function open(page: Page, baseURL: string | undefined, s: Server, width = 1280) {
  const origin = new URL(baseURL ?? '').origin
  await page.setViewportSize({ width, height: 900 })
  await page.clock.install({ time: now })
  page.on('pageerror', (err) => s.problems.push(err.message))
  // A scripted event stream; __change sends a change event (a sync).
  await page.addInitScript(() => {
    const w = window as unknown as { __change: () => void; EventSource: unknown }
    class FakeEventSource extends EventTarget {
      readyState = 0
      constructor() {
        super()
        w.__change = () => this.dispatchEvent(new MessageEvent('change', { data: '{"kind":"task"}' }))
        setTimeout(() => {
          this.readyState = 1
          this.dispatchEvent(new Event('open'))
        }, 50)
      }
      close() {
        this.readyState = 2
      }
    }
    w.EventSource = FakeEventSource
  })
  await page.route((url) => url.protocol !== 'data:' && url.protocol !== 'blob:' && url.origin !== origin, (r) => r.abort('blockedbyclient'))
  await page.route('**/api/v1/**', async (r) => {
    const req = r.request()
    const url = new URL(req.url())
    const p = url.pathname.slice('/api/v1'.length)
    if (req.method() === 'POST' && p.endsWith('/warm')) return r.fulfill({ status: 204 })
    if (p === chatPath && req.method() === 'POST') {
      s.posts.push((req.postDataJSON() as { body: string }).body)
      return r.fulfill({ status: 200, json: { id: 700 + s.posts.length } })
    }
    if (p === chatPath) {
      const messages = s.replies.flatMap((body, i) => (body ? [{ id: 1701 + i, replyTo: 701 + i, body, author: 'operator' }] : []))
      return r.fulfill({ status: 200, json: { messages } })
    }
    if (req.method() !== 'GET') {
      s.problems.push(`${req.method()} ${url.pathname}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    let body: unknown
    if (p === projectPath) body = { ...shopDetail, looseIssues: true }
    else if (p === `${projectPath}/loose-issues`) body = s.view
    else body = answer(p, url.searchParams)
    if (body === undefined) {
      s.problems.push(`GET ${url.pathname}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    return r.fulfill({ status: 200, json: body })
  })
  await page.goto(`${projectPath}?tab=loose`)
  await expect(queueRows(page)).toHaveCount(s.view.items.length)
}

const queueTable = (page: Page) => page.locator('[data-loose-queue]')
const queueRows = (page: Page) => queueTable(page).locator('tbody tr')
const issuesTable = (page: Page) => page.locator('[data-loose-issues]')
const sync = (page: Page) => page.evaluate(() => (window as unknown as { __change: () => void }).__change())
const toast = (page: Page, text: string) => page.locator('[data-slot="description"]', { hasText: text })
const takeButton = (page: Page) => page.getByRole('button', { name: /^Hand to autopilot/ })

for (const width of [375, 1280]) {
  test(`an owner hands #350 and #421 to autopilot and drops one@${width}`, async ({ page, baseURL }) => {
    const s = server()
    await open(page, baseURL, s, width)
    // The tab sits right after Milestones.
    const tabs = page.locator('main').getByRole('navigation', { name: 'View' }).getByRole('link')
    await expect(tabs).toHaveText(['Cockpit', 'Milestones', 'Loose issues', 'Decisions', 'Metrics'])

    await expect(takeButton(page)).toHaveText('Hand to autopilot (0)')
    await expect(takeButton(page)).toBeDisabled()
    // Issues in the queue cannot be ticked.
    await expect(issuesTable(page).getByRole('checkbox', { name: 'Select #190' })).toBeDisabled()
    await expect(issuesTable(page).locator('[data-loose-in-queue="190"]')).toHaveText('in queue')
    // Ticked in this order: #350 first.
    await issuesTable(page).getByRole('checkbox', { name: 'Select #350' }).click()
    await issuesTable(page).getByRole('checkbox', { name: 'Select #421' }).click()
    await expect(takeButton(page)).toHaveText('Hand to autopilot (2)')
    await takeButton(page).click()
    await expect.poll(() => s.posts).toEqual(['{"cmd":"take","issues":[350,421]}'])
    await expect(takeButton(page)).toHaveText('Hand to autopilot (0)')

    // The operator takes both; the view reloads with them pending.
    s.replies[0] = 'Taken into the loose issues queue: #350, #421.'
    s.view = {
      ...looseIssues,
      items: [...looseItems, { iid: 350, title: 'Flaky helm-lint', webURL: '', state: 'Pending' }, { iid: 421, title: 'Runner timeout in the e2e job', webURL: '', state: 'Pending' }],
      issues: looseOpenIssues.map((i) => (i.iid === 350 || i.iid === 421 ? { ...i, inQueue: true } : i)),
    }
    await sync(page)
    await expect(toast(page, 'Taken into the loose issues queue: #350, #421.')).toBeVisible()
    await expect(queueRows(page)).toHaveCount(looseItems.length + 2)
    await expect(queueTable(page).locator('[data-loose-state="350"]')).toHaveText('Pending · 2')
    await expect(queueTable(page).locator('[data-loose-state="421"]')).toHaveText('Pending · 3')
    await expect(issuesTable(page).getByRole('checkbox', { name: 'Select #350' })).toBeDisabled()

    // Drop #190 from its ⋯ menu.
    await queueTable(page).getByRole('button', { name: 'Actions for #190' }).click()
    await expect(page.getByRole('menuitem')).toHaveText(['Drop'])
    await page.getByRole('menuitem', { name: 'Drop' }).click()
    await expect.poll(() => s.posts[1]).toBe('{"cmd":"drop","issue":190}')
    s.replies[1] = '#190 left the queue: dropped by @ada.'
    await sync(page)
    await expect(toast(page, '#190 left the queue: dropped by @ada.')).toBeVisible()

    // color-contrast is the Desk-wide #641 (e2e/known-a11y.ts lists it on
    // the project pages); this check is about the tables' names and roles.
    const { violations } = await new AxeBuilder({ page })
      .include('main')
      .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
      .disableRules(['color-contrast'])
      .analyze()
    expect(violations.map((v) => v.id)).toEqual([])
    expect(s.problems).toEqual([])
  })
}

test('the ⋯ menu resumes a held issue and merges its head', async ({ page, baseURL }) => {
  const s = server()
  await open(page, baseURL, s)
  await expect(queueTable(page).locator('[data-loose-state="46"]')).toHaveText('Held')
  await expect(queueRows(page).filter({ hasText: 'CI stays red after 3 fix rounds' })).toHaveCount(1)
  // Finished rows have no menu.
  await expect(queueTable(page).getByRole('button', { name: 'Actions for #58' })).toHaveCount(0)
  await queueTable(page).getByRole('button', { name: 'Actions for #46' }).click()
  await expect(page.getByRole('menuitem')).toHaveText(['Resume', 'Drop', 'Merge head'])
  await page.getByRole('menuitem', { name: 'Resume' }).click()
  await expect.poll(() => s.posts[0]).toBe('{"cmd":"resume","issue":46}')
  s.replies[0] = 'The hold on #46 is lifted.'
  await sync(page)
  await expect(toast(page, 'The hold on #46 is lifted.')).toBeVisible()
  await queueTable(page).getByRole('button', { name: 'Actions for #128' }).click()
  await expect(page.getByRole('menuitem')).toHaveText(['Drop', 'Merge head'])
  await page.getByRole('menuitem', { name: 'Merge head' }).click()
  await expect.poll(() => s.posts[1]).toBe('{"cmd":"merge","issue":128,"sha":"4f2a9c1e8b7d"}')
  expect(s.problems).toEqual([])
})

test('the search filters by IID, title, label or author', async ({ page, baseURL }) => {
  const s = server()
  await open(page, baseURL, s)
  const rows = issuesTable(page).locator('tbody tr')
  await expect(rows).toHaveCount(looseOpenIssues.length)
  const search = page.getByRole('textbox', { name: 'Search issues' })
  await search.fill('flaky')
  await expect(rows).toHaveCount(3)
  await search.fill('#350')
  await expect(rows).toHaveCount(1)
  await search.fill('ada')
  await expect(rows).toHaveCount(2)
  await search.fill('nothing like it')
  await expect(rows).toHaveText(['No issue matches the search.'])
  expect(s.problems).toEqual([])
})

test('without the take permission no action shows and a note names who may act', async ({ page, baseURL }) => {
  const s = server({ ...looseIssues, take: false, ownerSubjects: [] })
  await open(page, baseURL, s)
  await expect(page.locator('[data-loose-owners]')).toHaveText(
    "Only the queue's owners may take, drop, resume or merge issues here. Who may act: nobody: the queue has no ownerSubjects.",
  )
  await expect(takeButton(page)).toHaveCount(0)
  await expect(page.getByRole('checkbox')).toHaveCount(0)
  await expect(queueTable(page).getByRole('button', { name: /^Actions for/ })).toHaveCount(0)
  expect(s.problems).toEqual([])
})
