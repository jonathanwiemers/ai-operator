// The project queue's drag handle, row menu and confirmation (ProjectQueue.vue,
// #662): a move sends the `move` command to the milestone's spec task, the row
// stays pending until the queue reports the order, goes back on a refusal, and
// a move above the running milestone asks first.
// Run with npm run e2e (built UI, Chromium).
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { journeyAt } from './journey'
import { answer, now } from './fixtures'
import { cost } from './fixtures/clock'
import { shopDetail } from './fixtures/stream'
import type { QueueEntry } from '../src/generated/views'

const projectPath = '/projects/team-a/shop'
const chatRE = /^\/projects\/team-a\/shop\/tasks\/(shop-m\d+-spec)\/chat$/

const entry = (iid: number, state: QueueEntry['state']): QueueEntry => ({
  ...cost('0.00', 0),
  iid,
  title: `Milestone ${iid}`,
  task: `shop-m${iid}-spec`,
  state,
  done: 0,
  total: 3,
})

// Server is what the fake API answers: the queue, the spec tasks' command
// posts and the operator's reply to them.
interface Server {
  queue: QueueEntry[]
  commands: boolean
  reply: string
  posts: { task: string; body: string }[]
  problems: string[]
}

async function open(page: Page, baseURL: string | undefined, server: Server, width = 1280) {
  const origin = new URL(baseURL ?? '').origin
  await page.setViewportSize({ width, height: 800 })
  // No row animation: the drag sees the rows where they end up.
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.clock.install({ time: now })
  page.on('pageerror', (err) => server.problems.push(err.message))
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
    const chat = chatRE.exec(p)
    if (chat && req.method() === 'POST') {
      server.posts.push({ task: chat[1]!, body: (req.postDataJSON() as { body: string }).body })
      return r.fulfill({ status: 200, json: { id: 700 + server.posts.length } })
    }
    if (chat) {
      const id = 700 + server.posts.length
      const messages = server.posts.length && server.reply ? [{ id: id + 1000, replyTo: id, body: server.reply, author: 'operator' }] : []
      return r.fulfill({ status: 200, json: { messages } })
    }
    if (req.method() !== 'GET') {
      server.problems.push(`${req.method()} ${url.pathname}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    const body = p === projectPath ? { ...shopDetail, queue: server.queue, queueCommands: server.commands } : answer(p, url.searchParams)
    if (body === undefined) {
      server.problems.push(`GET ${url.pathname}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    if (typeof body === 'string') return r.fulfill({ status: 200, contentType: 'text/plain; charset=utf-8', body })
    return r.fulfill({ status: 200, json: body })
  })
  // The queue is on the Milestones tab (the project's default tab).
  await page.goto(`${projectPath}?tab=milestones`)
  await expect(queue(page).locator('[data-queue-entry]')).toHaveCount(server.queue.length)
}

const queue = (page: Page) => page.locator('section[aria-labelledby="project-queue"]')
const row = (page: Page, iid: number) => queue(page).locator(`[data-queue-entry="${iid}"]`)
const order = (page: Page) => queue(page).locator('[data-queue-entry]').evaluateAll((rows) => rows.map((r) => Number(r.getAttribute('data-queue-entry'))))
const sync = (page: Page) => page.evaluate(() => (window as unknown as { __change: () => void }).__change())

// drag takes iid's handle to the row onto (SortableJS's pointer fallback
// checks where the pointer is every 50 ms) and drops it once the rows show
// the order want.
async function drag(page: Page, iid: number, onto: number, want: number[]) {
  const from = (await row(page, iid).locator('[data-queue-handle]').boundingBox())!
  const to = (await row(page, onto).boundingBox())!
  const x = from.x + from.width / 2
  const y = from.y + from.height / 2
  await page.mouse.move(x, y)
  await page.mouse.down()
  for (let k = 1; k <= 8; k++) await page.mouse.move(x, y + ((to.y + to.height / 2 - y) * k) / 8)
  await expect.poll(() => order(page)).toEqual(want)
  await page.mouse.up()
}

const three = (first: QueueEntry['state'] = 'waiting'): Server => ({
  queue: [entry(5, first), entry(6, 'waiting'), entry(7, 'waiting')],
  commands: true,
  reply: '',
  posts: [],
  problems: [],
})

test('dragging %7 above %6 sends move before %6 and stays pending until the sync', async ({ page, baseURL }) => {
  const server = three()
  await open(page, baseURL, server)
  await drag(page, 7, 6, [5, 7, 6])
  await expect.poll(() => order(page)).toEqual([5, 7, 6])
  await expect(row(page, 7).locator('[data-queue-pending]')).toHaveText('Moving…')
  expect(server.posts).toEqual([{ task: 'shop-m7-spec', body: '{"cmd":"move","before":6}' }])

  // The operator applies it; the row stays pending until the queue says so.
  server.reply = 'Moved before %6.'
  await sync(page)
  await expect(queue(page).getByRole('status')).toHaveText('Moved before %6.')
  await expect(row(page, 7).locator('[data-queue-pending]')).toHaveCount(1)
  server.queue = [entry(5, 'waiting'), entry(7, 'waiting'), entry(6, 'waiting')]
  await sync(page)
  await expect(row(page, 7).locator('[data-queue-pending]')).toHaveCount(0)
  expect(await order(page)).toEqual([5, 7, 6])
  expect(server.problems).toEqual([])
})

for (const width of [375, 1280]) {
  test(`the row menu moves %7 up by keyboard and keeps the focus on it@${width}`, journeyAt(width), async ({ page, baseURL }) => {
    const server = three()
    await open(page, baseURL, server, width)
    const menu = queue(page).getByRole('button', { name: 'Move %7' })
    await menu.focus()
    await page.keyboard.press('Enter')
    const items = page.getByRole('menuitem')
    await expect(items).toHaveText(['Move to top', 'Move up'])
    await expect(items.first()).toBeFocused()
    await page.keyboard.press('ArrowDown')
    await expect(page.getByRole('menuitem', { name: 'Move up' })).toBeFocused()
    await page.keyboard.press('Enter')
    await expect.poll(() => order(page)).toEqual([5, 7, 6])
    expect(server.posts).toEqual([{ task: 'shop-m7-spec', body: '{"cmd":"move","before":6}' }])
    await expect(queue(page).getByRole('button', { name: 'Move %7' })).toBeFocused()
    // The first row offers no move up, the last no move down.
    await queue(page).getByRole('button', { name: 'Move %5' }).click()
    await expect(page.getByRole('menuitem')).toHaveText(['Move down'])
    await expect(page.getByRole('menuitem', { name: 'Move down' })).toBeDisabled()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('menuitem')).toHaveCount(0)
    // color-contrast is the Desk-wide #641 (e2e/known-a11y.ts lists it on the
    // project pages); this check is about the menu's names, roles and states.
    const { violations } = await new AxeBuilder({ page })
      .include('section[aria-labelledby="project-queue"]')
      .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
      .disableRules(['color-contrast'])
      .analyze()
    expect(violations.map((v) => v.id)).toEqual([])
    expect(server.problems).toEqual([])
  })
}

test('a move above the running milestone asks first; Cancel sends nothing', async ({ page, baseURL }) => {
  const server = three('running')
  await open(page, baseURL, server)
  await drag(page, 7, 5, [7, 5, 6])
  const dialog = page.getByRole('dialog', { name: 'Move above the running milestone?' })
  await expect(dialog).toContainText("Move %7 above %5? %5's running issues finish, then %7 runs. %5 starts no new issues until %7 is done.")
  const { violations } = await new AxeBuilder({ page }).include('[role="dialog"]').withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']).analyze()
  expect(violations.map((v) => v.id)).toEqual([])
  await dialog.getByRole('button', { name: 'Cancel' }).click()
  await expect(dialog).toHaveCount(0)
  expect(await order(page)).toEqual([5, 6, 7])
  expect(server.posts).toEqual([])

  await drag(page, 7, 5, [7, 5, 6])
  await dialog.getByRole('button', { name: 'Move', exact: true }).click()
  await expect(dialog).toHaveCount(0)
  await expect.poll(() => order(page)).toEqual([7, 5, 6])
  expect(server.posts).toEqual([{ task: 'shop-m7-spec', body: '{"cmd":"move","to":"top"}' }])
  expect(server.problems).toEqual([])
})

test('a queue that changes while the confirmation is open sends nothing', async ({ page, baseURL }) => {
  const server = three('running')
  await open(page, baseURL, server)
  await queue(page).getByRole('button', { name: 'Move %7' }).click()
  await page.getByRole('menuitem', { name: 'Move to top' }).click()
  const dialog = page.getByRole('dialog', { name: 'Move above the running milestone?' })
  await expect(dialog).toContainText('Move %7 above %5?')
  // A sync: %6 moved to the top from GitLab meanwhile.
  server.queue = [entry(6, 'waiting'), entry(5, 'running'), entry(7, 'waiting')]
  await sync(page)
  await expect.poll(() => order(page)).toEqual([6, 5, 7])
  await dialog.getByRole('button', { name: 'Move', exact: true }).click()
  await expect(dialog).toHaveCount(0)
  await expect(queue(page).getByRole('alert')).toHaveText('The queue changed while you confirmed; nothing was moved. Try again.')
  expect(server.posts).toEqual([])
  expect(await order(page)).toEqual([6, 5, 7])
  expect(server.problems).toEqual([])
})

test('a refused move goes back with the operator answer', async ({ page, baseURL }) => {
  const server = three()
  await open(page, baseURL, server)
  server.reply = 'Only owners may move milestones.'
  await queue(page).getByRole('button', { name: 'Move %6' }).click()
  await page.getByRole('menuitem', { name: 'Move down' }).click()
  await expect(queue(page).getByRole('alert')).toHaveText('Only owners may move milestones.')
  expect(await order(page)).toEqual([5, 6, 7])
  await expect(queue(page).locator('[data-queue-pending]')).toHaveCount(0)
  expect(server.posts).toEqual([{ task: 'shop-m6-spec', body: '{"cmd":"move","to":"bottom"}' }])
  expect(server.problems).toEqual([])
})

test('without queue commands there is no handle and no menu', async ({ page, baseURL }) => {
  const server = { ...three(), commands: false }
  await open(page, baseURL, server)
  await expect(queue(page).locator('[data-queue-handle]')).toHaveCount(0)
  await expect(queue(page).getByRole('button')).toHaveCount(0)
  expect(server.problems).toEqual([])
})
