// Browser checks of the Desk (docs/ui.md, "Checks"): every route at a
// phone, a tablet and a desktop width, served by e2e/serve.mjs with the
// server's CSP and the API answered from e2e/fixtures.
import { join } from 'node:path'
import { expect, test, type Page } from '@playwright/test'
import { answer, now } from './fixtures'
import { inbox } from './fixtures/rail'
import { features } from './fixtures/session'
import { overview } from './fixtures/overview'
import { paymentsMilestone } from './fixtures/stream'
import { refundsPrompt, refundsResponse } from './fixtures/task'
import { taskTabs, type TaskTab } from '../src/streamTabs'
import type { Feature } from '../src/types'
import { knownOverflow } from './known-overflow'

const widths = [
  { width: 375, height: 812 },
  { width: 768, height: 1024 },
  { width: 1280, height: 800 },
]

interface Route {
  // name is the check's name, "<route name>" or "<route name>-<variant>".
  name: string
  path: string
  // signedOut: every API request answers 401, so the app shows the sign-in.
  signedOut?: boolean
  // colorScheme is the system's scheme the page sees, light when unset
  // (playwright.config.ts).
  colorScheme?: 'light' | 'dark'
  // ready waits until the page shows its data.
  ready: (page: Page) => Promise<void>
}

// What the project and milestone tabs show once loaded (e2e/fixtures/stream.ts).
const projectReady = {
  stream: 'Run 2 needs input',
  board: 'Guest checkout: confirmation mail',
  spend: 'Cache-read share',
}
const milestoneReady = {
  issues: 'Document the refund flow for support',
  pilot: 'Split across captures',
  retrospective: 'Retry the registry push in the release job',
  stream: 'Run 2 needs input',
  board: 'Partial captures for split shipments',
  spend: 'Cache-read share',
}

// What the task tabs show once loaded (e2e/fixtures/task.ts).
const taskPagePaths = {
  issue: '/projects/team-a/shop/milestones/14/issues/281',
  task: '/projects/team-a/shop/tasks/shop-281-implement',
}
const taskReady: Record<TaskTab, string> = {
  overview: 'Summary of the last run',
  chat: 'Please keep the refund reasons in the',
  live: 'The pod is not available',
  calls: 'Agent calls',
  details: 'shop-281-implement-ws',
}

// Every named route but the redirects (everything, project-board,
// milestone) has a case here, with a variant per tab.
const routes: Route[] = [
  {
    name: 'overview',
    path: '/',
    ready: (page) => expect(page.getByText('Checkout without account').first()).toBeVisible(),
  },
  // The dark tokens (.dark in style.css), for the screenshots.
  {
    name: 'overview-dark',
    path: '/',
    colorScheme: 'dark',
    ready: (page) => expect(page.getByText('Checkout without account').first()).toBeVisible(),
  },
  {
    name: 'overview-stream',
    path: '/?tab=stream',
    ready: (page) => expect(page.getByText('Review round 2').first()).toBeVisible(),
  },
  {
    name: 'inbox',
    path: '/inbox',
    ready: (page) => expect(page.getByText('Support refunds through the payment provider').first()).toBeVisible(),
  },
  ...(['stream', 'board', 'spend'] as const).map(
    (tab): Route => ({
      name: `stream-project-${tab}`,
      path: `/projects/team-a/shop?tab=${tab}`,
      ready: (page) => expect(page.locator('main').getByText(projectReady[tab]).first()).toBeVisible(),
    }),
  ),
  ...(['issues', 'pilot', 'retrospective', 'stream', 'board', 'spend'] as const).map(
    (tab): Route => ({
      name: `stream-milestone-${tab}`,
      path: `/projects/team-a/shop/milestones/14?tab=${tab}`,
      ready: (page) => expect(page.locator('main').getByText(milestoneReady[tab]).first()).toBeVisible(),
    }),
  ),
  // The task page and the issue page with a live task: Overview by default,
  // a variant per other tab (TaskBody.vue, R10).
  ...(['issue', 'task'] as const).flatMap((name): Route[] =>
    taskTabs.map((t) => ({
      name: t.id === 'overview' ? name : `${name}-${t.id}`,
      path: `${taskPagePaths[name]}${t.id === 'overview' ? '' : `?tab=${t.id}`}`,
      ready: (page) => expect(page.locator('main').getByText(taskReady[t.id]).first()).toBeVisible(),
    })),
  ),
  {
    name: 'researches',
    path: '/research',
    ready: (page) => expect(page.locator('main').getByText('checkout-ux-study').first()).toBeVisible(),
  },
  {
    name: 'research-new',
    path: '/research/new',
    ready: (page) => expect(page.locator('main').getByText('claude/opus').first()).toBeAttached(),
  },
  {
    name: 'research',
    path: '/research/research/payments-news',
    ready: (page) => expect(page.locator('main').getByText('Card payments failed in the EU region').first()).toBeVisible(),
  },
  {
    name: 'research-report',
    path: '/research/research/checkout-ux-study',
    ready: (page) => expect(page.locator('main').getByText('Guest checkout in five large shops').first()).toBeVisible(),
  },
  {
    name: 'research-runs',
    path: '/research/research/payments-news/runs',
    ready: (page) => expect(page.locator('main').getByText('cursor/auto').first()).toBeVisible(),
  },
  {
    name: 'research-run',
    path: '/research/research/checkout-ux-study/runs/run-0007',
    ready: (page) => expect(page.locator('main').getByText('a-shop-with-a-rather-long-domain-name.example').first()).toBeVisible(),
  },
  {
    name: 'not-found',
    path: '/no/such/page',
    // Built with Nuxt UI (#305): its button and the bundled lucide icon,
    // drawn as an inline svg, not fetched.
    ready: async (page) => {
      await expect(page.getByRole('heading', { name: 'Not found' })).toBeVisible()
      const back = page.getByRole('link', { name: 'Back to Overview' })
      await expect(back).toBeVisible()
      await expect(back.locator('svg path, svg g').first()).toBeAttached()
    },
  },
  {
    name: 'sign-in',
    path: '/',
    signedOut: true,
    ready: (page) => expect(page.getByRole('link', { name: 'Sign in with SSO' })).toBeVisible(),
  },
]

// What a check saw go wrong besides overflow.
interface Problems {
  unknownAPI: string[]
  otherOrigin: string[]
  csp: string[]
  pageErrors: string[]
}

// mockAPI answers /api/v1 from the fixtures, an empty event stream for
// /api/v1/events, and 404 for paths without a fixture (recorded).
async function mockAPI(page: Page, route: Route, problems: Problems): Promise<void> {
  await page.route('**/api/v1/**', async (r) => {
    const req = r.request()
    const url = new URL(req.url())
    const path = url.pathname.slice('/api/v1'.length)
    if (route.signedOut) {
      return r.fulfill({ status: 401, json: { error: 'login required' } })
    }
    if (path === '/events' && req.method() === 'GET') {
      // The stream ends at once; the retry keeps EventSource from
      // reconnecting during the check.
      return r.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 3600000\n\n' })
    }
    const body = req.method() === 'GET' ? answer(path, url.searchParams) : undefined
    if (body === undefined) {
      problems.unknownAPI.push(`${req.method()} ${url.pathname}${url.search}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    return r.fulfill({ status: 200, json: body })
  })
}

// watch records CSP violations, page errors and requests to other origins.
// Requests to other origins are aborted, so a check never leaves the machine.
async function watch(page: Page, origin: string, problems: Problems): Promise<void> {
  await page.addInitScript(() => {
    const seen: string[] = []
    ;(window as unknown as { __cspViolations: string[] }).__cspViolations = seen
    document.addEventListener('securitypolicyviolation', (e) => {
      seen.push(`${e.effectiveDirective} blocked ${e.blockedURI || '(inline)'}`)
    })
  })
  page.on('console', (msg) => {
    if (msg.type() === 'error' && /Content[- ]Security[- ]Policy/i.test(msg.text())) problems.csp.push(msg.text())
  })
  page.on('pageerror', (err) => problems.pageErrors.push(err.message))
  page.on('request', (req) => {
    const url = new URL(req.url())
    if (url.protocol === 'data:' || url.protocol === 'blob:') return
    if (url.origin !== origin) problems.otherOrigin.push(req.url())
  })
  await page.route(
    (url) => url.protocol !== 'data:' && url.protocol !== 'blob:' && url.origin !== origin,
    (r) => r.abort('blockedbyclient'),
  )
}

for (const route of routes) {
  for (const size of widths) {
    const id = `${route.name}@${size.width}`
    test(id, async ({ page, baseURL }, testInfo) => {
      const known = knownOverflow[id]
      test.fail(known !== undefined, `known overflow: ${known}`)

      const origin = new URL(baseURL ?? '').origin
      const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
      await page.setViewportSize(size)
      if (route.colorScheme) await page.emulateMedia({ colorScheme: route.colorScheme })
      await page.clock.setFixedTime(now)
      await watch(page, origin, problems)
      await mockAPI(page, route, problems)

      await page.goto(route.path)
      // The screenshot is taken even when the page never gets ready.
      const shot = join(testInfo.project.outputDir, '..', 'screenshots', `${id}.png`)
      try {
        await route.ready(page)
        await page.waitForLoadState('networkidle')
      } finally {
        await page.screenshot({ path: shot, fullPage: true })
        await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })
      }

      problems.csp.push(...(await page.evaluate(() => (window as unknown as { __cspViolations: string[] }).__cspViolations)))
      expect(problems.unknownAPI, 'API requests without a fixture (add them to e2e/fixtures)').toEqual([])
      expect(problems.csp, 'CSP violations').toEqual([])
      expect(problems.otherOrigin, 'requests to another origin').toEqual([])
      expect(problems.pageErrors, 'uncaught errors').toEqual([])

      // Overflow: how far the page and the content pane (App.vue's main)
      // scroll sideways, in px.
      const overflow = await page.evaluate(() => {
        const doc = document.scrollingElement ?? document.documentElement
        const main = document.querySelector('main')
        return {
          page: doc.scrollWidth - doc.clientWidth,
          pane: main ? main.scrollWidth - main.clientWidth : 0,
        }
      })
      expect(overflow, 'sideways scroll in px of the page and of the content pane').toEqual({ page: 0, pane: 0 })
    })
  }
}

// The Desk follows the system's colour scheme while it is open
// (src/colorScheme.ts): .dark on <html>, the dark tokens on the page.
test('colour scheme follows the system live', async ({ page, baseURL }) => {
  const route = routes.find((r) => r.name === 'overview')!
  const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
  await page.clock.setFixedTime(now)
  await watch(page, new URL(baseURL ?? '').origin, problems)
  await mockAPI(page, route, problems)
  await page.goto(route.path)
  await route.ready(page)

  const html = page.locator('html')
  const bodyBackground = () => page.evaluate(() => getComputedStyle(document.body).backgroundColor)
  await expect(html).not.toHaveClass(/\bdark\b/)
  expect(await bodyBackground(), '--bg light').toBe('rgb(246, 246, 248)')

  await page.emulateMedia({ colorScheme: 'dark' })
  await expect(html).toHaveClass(/\bdark\b/)
  expect(await bodyBackground(), '--bg dark').toBe('rgb(19, 17, 25)')

  await page.emulateMedia({ colorScheme: 'light' })
  await expect(html).not.toHaveClass(/\bdark\b/)
  expect(await bodyBackground(), '--bg light again').toBe('rgb(246, 246, 248)')

  problems.csp.push(...(await page.evaluate(() => (window as unknown as { __cspViolations: string[] }).__cspViolations)))
  expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
})

// DataList (src/ui/DataList.vue): on a phone each run row shows its labels,
// from the tablet width up they are hidden and the headers show instead.
for (const { width, height, labels } of [
  { width: 375, height: 812, labels: true },
  { width: 1280, height: 800, labels: false },
]) {
  test(`research-runs DataList labels ${labels ? 'visible' : 'hidden'}@${width}`, async ({ page, baseURL }) => {
    const route = routes.find((r) => r.name === 'research-runs')!
    const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
    await page.setViewportSize({ width, height })
    await page.clock.setFixedTime(now)
    await watch(page, new URL(baseURL ?? '').origin, problems)
    await mockAPI(page, route, problems)
    await page.goto(route.path)
    await route.ready(page)

    const row = page.locator('main tbody tr').first()
    const rowLabels = row.locator('[data-label]')
    await expect(rowLabels).toHaveText(['Started', 'Duration', 'Status', 'Agent', 'Choice', 'Cost', 'Error'])
    for (const l of await rowLabels.all()) {
      if (labels) await expect(l).toBeVisible()
      else await expect(l).toBeHidden()
    }
    if (labels) await expect(page.locator('main thead')).toBeHidden()
    else await expect(page.locator('main thead')).toBeVisible()
  })
}

// RouteTabs (src/ui/RouteTabs.vue): on a phone the milestone's six tabs are
// wider than the screen. They scroll inside the strip, the page does not,
// and the tab of ?tab=spend, the last one, is scrolled into view on load.
test('stream-milestone RouteTabs scroll inside the strip@375', async ({ page, baseURL }, testInfo) => {
  const route = routes.find((r) => r.name === 'stream-milestone-spend')!
  const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
  await page.setViewportSize({ width: 375, height: 812 })
  await page.clock.setFixedTime(now)
  await watch(page, new URL(baseURL ?? '').origin, problems)
  await mockAPI(page, route, problems)
  await page.goto(route.path)
  await route.ready(page)

  const strip = page.locator('main').getByRole('navigation', { name: 'View' })
  const active = strip.getByRole('link', { name: 'Spend' })
  await expect(active).toHaveAttribute('aria-current', 'page')
  await expect(active).toBeInViewport({ ratio: 1 })
  await expect(strip.getByRole('link', { name: 'Issues' })).not.toBeInViewport({ ratio: 1 })

  const shot = join(testInfo.project.outputDir, '..', 'screenshots', 'route-tabs@375.png')
  await page.screenshot({ path: shot })
  await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })

  const widths = await strip.evaluate((nav) => {
    const doc = document.scrollingElement ?? document.documentElement
    const main = document.querySelector('main')!
    return {
      strip: nav.scrollWidth > nav.clientWidth,
      page: doc.scrollWidth - doc.clientWidth,
      pane: main.scrollWidth - main.clientWidth,
    }
  })
  expect(widths, 'the strip scrolls, the page and the content pane do not').toEqual({ strip: true, page: 0, pane: 0 })
  expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
})

// The stream header (Stream.vue, docs/ui.md "Stream header"): on a phone the
// title, the tab strip and the search each take a full-width row, in that
// order, and the search is 16 px; from 760 px they share the first row.
for (const { width, height, phone } of [
  { width: 375, height: 812, phone: true },
  { width: 1280, height: 900, phone: false },
]) {
  test(`stream-project header rows@${width}`, async ({ page, baseURL }) => {
    const route = routes.find((r) => r.name === 'stream-project-stream')!
    const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
    await page.setViewportSize({ width, height })
    await page.clock.setFixedTime(now)
    await watch(page, new URL(baseURL ?? '').origin, problems)
    await mockAPI(page, route, problems)
    await page.goto(route.path)
    await route.ready(page)

    const head = page.locator('[data-stream-head]')
    const box = async (sel: string) => (await head.locator(sel).first().boundingBox())!
    const row = (await head.locator('h1').evaluate((h) => h.parentElement!.getBoundingClientRect().toJSON())) as DOMRect
    const title = await box('h1')
    const tabs = await box('nav[aria-label="View"]')
    const search = await box('input[type="search"]')
    const size = await head.locator('input[type="search"]').evaluate((i) => getComputedStyle(i).fontSize)
    if (phone) {
      expect(title.width, 'the title takes the full row').toBeCloseTo(row.width, 0)
      expect(tabs.y, 'the tabs sit below the title').toBeGreaterThanOrEqual(title.y + title.height)
      expect(tabs.width, 'the tabs take the full row').toBeCloseTo(row.width, 0)
      expect(search.y, 'the search sits below the tabs').toBeGreaterThanOrEqual(tabs.y + tabs.height)
      expect(search.width, 'the search takes the full row').toBeGreaterThan(row.width - 2)
      expect(size).toBe('16px')
    } else {
      const mid = (b: { y: number; height: number }) => b.y + b.height / 2
      expect(Math.abs(mid(tabs) - mid(title)), 'the tabs sit beside the title').toBeLessThan(8)
      expect(Math.abs(mid(search) - mid(title)), 'the search sits beside the title').toBeLessThan(8)
      expect(search.x + search.width, 'the search is on the right').toBeGreaterThan(row.x + row.width - 60)
    }
    expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
  })
}

// The shell on a phone (App.vue, docs/design.md "Phones"): on a short page
// the mobile bar keeps its natural height instead of stretching with the
// grid's rows, on Not found and on an empty list.
for (const { name, empty } of [
  { name: 'not-found', empty: false },
  { name: 'researches', empty: true },
]) {
  test(`shell mobile bar keeps its height on ${empty ? 'an empty list' : name}@375`, async ({ page, baseURL }) => {
    const route = routes.find((r) => r.name === name)!
    const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
    await page.setViewportSize({ width: 375, height: 812 })
    await page.clock.setFixedTime(now)
    await watch(page, new URL(baseURL ?? '').origin, problems)
    await mockAPI(page, route, problems)
    // Registered last, so it answers before the fixtures.
    if (empty) await page.route('**/api/v1/researches', (r) => r.fulfill({ status: 200, json: [] }))
    await page.goto(route.path)
    if (empty) await expect(page.locator('main').getByRole('heading', { name: 'No researches' })).toBeVisible()
    else await route.ready(page)

    const bar = page.locator('header.mobilebar')
    await expect(bar).toBeVisible()
    const box = (await bar.boundingBox())!
    expect(box.height, 'the bar is its natural height').toBeLessThan(70)
    expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
  })
}

// The task's Calls tab (TaskCalls.vue): a run opens its calls on tap (its
// 44 px target is in hitRoutes). On a phone each run and each call stacks as
// label and value pairs; from the tablet width up both are tables, the calls one
// scrolling inside its row. The page never scrolls sideways.
for (const { width, height } of widths) {
  test(`task-calls run opened@${width}`, async ({ page, baseURL }, testInfo) => {
    const route = routes.find((r) => r.name === 'task-calls')!
    const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
    const phone = width < 760
    await page.setViewportSize({ width, height })
    await page.clock.setFixedTime(now)
    await watch(page, new URL(baseURL ?? '').origin, problems)
    await mockAPI(page, route, problems)
    await page.goto(route.path)
    await route.ready(page)

    const main = page.locator('main')
    const toggle = main.getByRole('button', { name: 'Run 2' })
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await toggle.click()
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await expect(main.getByText('claude-opus-5-5').first()).toBeVisible()
    await page.waitForLoadState('networkidle')

    const shot = join(testInfo.project.outputDir, '..', 'screenshots', `task-calls-open@${width}.png`)
    await page.screenshot({ path: shot, fullPage: true })
    await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })

    const callLabels = main.locator('tbody tbody tr').first().locator('[data-label]')
    await expect(callLabels).toHaveText(['#', 'Time', 'Kind', 'Role', 'Engine / model', 'Resumed', 'Lines', 'In', 'Out', 'Cache read', 'Cache write', 'Duration', 'Cost'])
    if (phone) await expect(callLabels.first()).toBeVisible()
    else await expect(callLabels.first()).toBeHidden()

    const overflow = await page.evaluate(() => {
      const doc = document.scrollingElement ?? document.documentElement
      const pane = document.querySelector('main')!
      return { page: doc.scrollWidth - doc.clientWidth, pane: pane.scrollWidth - pane.clientWidth }
    })
    expect(overflow, 'sideways scroll in px of the page and of the content pane').toEqual({ page: 0, pane: 0 })

    await toggle.click()
    await expect(main.getByText('claude-opus-5-5').first()).toBeHidden()
    expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
  })
}

// iOS zooms on focus into a control under 16 px (style.css, the phone rule
// in the base layer): on a phone every visible input of the task's Chat
// tab, the chat composer's among them, and the stream search are 16 px; on a desktop
// the composer keeps its own size.
for (const { width, height, phone } of [
  { width: 375, height: 812, phone: true },
  { width: 1280, height: 800, phone: false },
]) {
  test(`inputs ${phone ? 'at 16 px' : 'keep their size'}@${width}`, async ({ page, baseURL }) => {
    const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
    await page.setViewportSize({ width, height })
    await page.clock.setFixedTime(now)
    await watch(page, new URL(baseURL ?? '').origin, problems)
    const fontSize = (l: ReturnType<Page['locator']>) => l.evaluate((e) => parseFloat(getComputedStyle(e).fontSize))

    const task = routes.find((r) => r.name === 'task-chat')!
    await mockAPI(page, task, problems)
    await page.goto(task.path)
    await task.ready(page)
    const composer = page.getByPlaceholder('Message the agent…')
    await expect(composer).toBeVisible()
    if (phone) {
      expect(await fontSize(composer), 'task composer').toBeGreaterThanOrEqual(16)
      const sizes = await page.locator('main').evaluate((main) =>
        [...main.querySelectorAll<HTMLElement>('input, select, textarea')]
          .filter((e) => e.offsetParent !== null && !['checkbox', 'radio'].includes((e as HTMLInputElement).type))
          .map((e) => `${e.tagName.toLowerCase()} ${e.getAttribute('aria-label') ?? e.getAttribute('placeholder') ?? ''}: ${getComputedStyle(e).fontSize}`),
      )
      expect(sizes.length, 'inputs on the task page').toBeGreaterThan(0)
      expect(sizes.filter((s) => parseFloat(s.slice(s.lastIndexOf(':') + 1)) < 16), 'inputs under 16 px').toEqual([])
    } else {
      expect(await fontSize(composer), 'task composer on a desktop').toBeLessThan(16)
    }

    const stream = routes.find((r) => r.name === 'stream-project-stream')!
    await page.goto(stream.path)
    await stream.ready(page)
    const search = page.getByRole('searchbox', { name: 'Search stream' })
    await expect(search).toBeVisible()
    if (phone) expect(await fontSize(search), 'stream search').toBeGreaterThanOrEqual(16)
    else expect(await fontSize(search), 'stream search on a desktop').toBeLessThan(16)

    problems.csp.push(...(await page.evaluate(() => (window as unknown as { __cspViolations: string[] }).__cspViolations)))
    expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
  })
}

// Truncated (src/ui/Truncated.vue): on a phone the long branch name of the
// task page's Details tab is clamped, a tap opens it in place and a second tap closes it.
test.describe('touch', () => {
  test.use({ hasTouch: true })

  test('task Truncated branch fact opens on tap@375', async ({ page, baseURL }) => {
    const route = routes.find((r) => r.name === 'task-details')!
    const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
    await page.setViewportSize({ width: 375, height: 812 })
    await page.clock.setFixedTime(now)
    await watch(page, new URL(baseURL ?? '').origin, problems)
    await mockAPI(page, route, problems)
    await page.goto(route.path)
    await route.ready(page)

    const branch = 'agent/281-support-refunds-through-the-payment-provider'
    const value = page.locator('main .fact').filter({ hasText: 'Branch' }).getByRole('button')
    await expect(value).toHaveAttribute('aria-expanded', 'false')
    await expect(value).not.toHaveAttribute('title', /.+/)
    const before = (await value.boundingBox())!
    expect(await value.evaluate((e) => e.scrollWidth > e.clientWidth), 'clamped').toBe(true)

    await value.tap()
    await expect(value).toHaveAttribute('aria-expanded', 'true')
    await expect(value).toHaveText(branch)
    const after = (await value.boundingBox())!
    expect(after.height, 'taller once open').toBeGreaterThan(before.height)
    expect(await value.evaluate((e) => e.scrollWidth <= e.clientWidth), 'fully shown').toBe(true)

    await value.tap()
    await expect(value).toHaveAttribute('aria-expanded', 'false')
    expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
  })
})

// Hit areas on a phone (decision 13; style.css, the phone rule in the base
// layer): every visible button, button-styled link, role=button and input of
// these pages is at least 44 px high at 375 px. Not counted: what cannot be
// tapped (hidden from assistive technology, transparent, or a disabled
// checkbox of a markdown task list). Exceptions are listed in hitAllowlist.
const hitRoutes = ['overview', 'inbox', 'task', 'task-chat', 'task-calls', 'task-details', 'stream-milestone-retrospective']

// "<route> <element as reported>" → why it may stay under 44 px.
const hitAllowlist: Record<string, string> = {
  // The reported label is cut at 40 characters.
  'stream-milestone-retrospective input[checkbox].m-0.size-4 "Take leftover 1 into a follow-up milesto"':
    'a checkbox keeps its size; the label around it is the 44 px hit area (milestone retrospective e2e)',
}

for (const name of hitRoutes) {
  test(`${name} hit areas of at least 44 px@375`, async ({ page, baseURL }, testInfo) => {
    const route = routes.find((r) => r.name === name)!
    const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
    await page.setViewportSize({ width: 375, height: 812 })
    await page.clock.setFixedTime(now)
    await watch(page, new URL(baseURL ?? '').origin, problems)
    await mockAPI(page, route, problems)
    await page.goto(route.path)
    await route.ready(page)
    await page.waitForLoadState('networkidle')

    const found = await page.evaluate(() => {
      const small: string[] = []
      let counted = 0
      for (const e of document.querySelectorAll<HTMLElement>('button, a.btn, [role=button], input')) {
        const input = e instanceof HTMLInputElement ? e : null
        if (input?.type === 'hidden') continue
        if (input && input.disabled && ['checkbox', 'radio'].includes(input.type)) continue
        if (e.closest('[aria-hidden="true"], [inert]')) continue
        const r = e.getBoundingClientRect()
        const style = getComputedStyle(e)
        if (r.width === 0 || r.height === 0 || style.visibility === 'hidden' || style.opacity === '0') continue
        counted++
        if (r.height >= 44) continue
        const label = (e.getAttribute('aria-label') ?? e.getAttribute('placeholder') ?? e.textContent ?? '').trim().replace(/\s+/g, ' ').slice(0, 40)
        const classes = [...e.classList].map((c) => '.' + c).join('')
        small.push(`${e.tagName.toLowerCase()}${input ? `[${input.type}]` : ''}${classes} "${label}" ${Math.round(r.height)}px`)
      }
      return { small, counted }
    })
    const shot = join(testInfo.project.outputDir, '..', 'screenshots', `hit-areas-${name}@375.png`)
    await page.screenshot({ path: shot, fullPage: true })
    await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })

    expect(found.counted, 'controls on the page').toBeGreaterThan(0)
    const unexplained = found.small.filter((s) => !(`${name} ${s.replace(/ \d+px$/, '')}` in hitAllowlist))
    expect(unexplained, 'controls under 44 px high (fix them, or add them to hitAllowlist with a reason)').toEqual([])
    expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
  })
}

// The Inbox's rows (views/Inbox.vue, R12): each row is at least 44 px high
// on a phone and opens its item on a tap anywhere but a clipped reason,
// which opens in place. No title attribute holds text of the list.
test.describe('inbox rows', () => {
  for (const { width, height } of [widths[0]!, widths[2]!]) {
    test(`rows navigate, a long reason opens in place@${width}`, async ({ page, baseURL }, testInfo) => {
      const route = routes.find((r) => r.name === 'inbox')!
      const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
      await page.setViewportSize({ width, height })
      // No fixed clock: Vue skips a bubbling handler attached at the event's
      // own Date.now(), which a frozen clock makes equal.
      await watch(page, new URL(baseURL ?? '').origin, problems)
      await mockAPI(page, route, problems)
      await page.goto(route.path)
      await route.ready(page)

      const list = page.locator('main ul').last()
      const rows = list.locator(':scope > li')
      await expect(rows).toHaveCount(inbox.items.length)
      if (width === 375) {
        for (const h of await rows.evaluateAll((els) => els.map((e) => e.getBoundingClientRect().height))) {
          expect(h, 'row height').toBeGreaterThanOrEqual(44)
        }
        // A row's title link covers its row with ::after: its hit area is
        // the row. Every other link (the paused strip, Open) is 44 px itself.
        const links = await page.locator('main').evaluate((main) =>
          [...main.querySelectorAll('a')].map((a) => {
            const covers = getComputedStyle(a, '::after').position === 'absolute'
            const box = (covers ? a.closest('li')! : a).getBoundingClientRect()
            return { text: (a.textContent ?? '').trim().slice(0, 40), height: box.height }
          }),
        )
        expect(links.length, 'links').toBeGreaterThan(0)
        for (const l of links) expect(l.height, `link ${l.text}`).toBeGreaterThanOrEqual(44)
      }
      expect(await page.locator('main [title]:not(time)').count(), 'title attributes (RelTime keeps its absolute time)').toBe(0)

      // The task row's long reason is clipped at two lines: a tap opens it
      // in place and stays on the Inbox.
      const clipped = page.locator('main [role=button][aria-expanded]', { hasText: 'blocking review findings' })
      await expect(clipped).toHaveAttribute('aria-expanded', 'false')
      await clipped.click()
      await expect(clipped).toHaveAttribute('aria-expanded', 'true')
      await expect(page).toHaveURL(/\/inbox$/)

      const shot = join(testInfo.project.outputDir, '..', 'screenshots', `inbox-rows@${width}.png`)
      await page.screenshot({ path: shot, fullPage: true })
      await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })

      // A tap on the row's time opens the item, as the whole row did.
      const prompt = list.locator('li', { hasText: 'Support refunds' })
      const box = (await prompt.boundingBox())!
      await page.mouse.click(box.x + box.width - 8, box.y + box.height - 8)
      await expect(page).toHaveURL(/\/projects\/team-a\/shop\/milestones\/14\?tab=pilot#prompt-p-281-1$/)

      // The pause row's Open leads to the milestone's Pilot tab.
      await page.goBack()
      await route.ready(page)
      await list.locator('li', { hasText: 'Payment providers' }).getByRole('link', { name: 'Open' }).click()
      await expect(page).toHaveURL(/\/projects\/team-a\/shop\/milestones\/14\?tab=pilot$/)
    })
  }
})

// Desktop is unchanged: the phone rule stops below 760 px, so the global
// .btn keeps its 34 px on the overview at 1280 px.
test('overview buttons keep their desktop height@1280', async ({ page, baseURL }) => {
  const route = routes.find((r) => r.name === 'overview')!
  const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
  await page.setViewportSize({ width: 1280, height: 800 })
  await page.clock.setFixedTime(now)
  await watch(page, new URL(baseURL ?? '').origin, problems)
  await mockAPI(page, route, problems)
  await page.goto(route.path)
  await route.ready(page)
  const heights = await page.evaluate(() => {
    const b = document.createElement('button')
    b.className = 'btn'
    b.textContent = 'probe'
    // Out of the flow: main is a flex column that stretches its children.
    b.style.position = 'absolute'
    document.querySelector('main')!.append(b)
    const h = b.getBoundingClientRect().height
    b.remove()
    return h
  })
  expect(heights, '.btn at desktop width').toBe(34)
  expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
})

// The rail sheet on a phone (App.vue, src/ui/useBackClose.ts, docs/ui.md
// "Sheets"): the menu opens the rail as a USlideover; scrim, close button,
// Escape and back each close it. Closing leaves the URL and the route as
// they were and no history entry behind, and the page does not load its data
// again: the API requests stay at their count.
test.describe('rail sheet', () => {
  const start = routes.find((r) => r.name === 'stream-project-stream')!

  // apiCount counts the API requests of the page but the event stream.
  function apiCount(page: Page): () => number {
    let n = 0
    page.on('request', (req) => {
      const url = new URL(req.url())
      if (url.pathname.startsWith('/api/v1/') && url.pathname !== '/api/v1/events') n++
    })
    return () => n
  }

  // keepRoute remembers vue-router's current route; sameRoute tells whether
  // it is still that object, that is the router did not navigate since (a
  // navigation to the same URL makes a new one).
  type Routed = { __vue_app__: { config: { globalProperties: { $router: { currentRoute: { value: object } } } } } }
  type Kept = { __keptRoute?: object }
  const keepRoute = (page: Page) =>
    page.evaluate(() => {
      const app = (document.querySelector('#app') as unknown as Routed).__vue_app__
      ;(window as unknown as Kept).__keptRoute = app.config.globalProperties.$router.currentRoute.value
    })
  const sameRoute = (page: Page) =>
    page.evaluate(() => {
      const app = (document.querySelector('#app') as unknown as Routed).__vue_app__
      return (window as unknown as Kept).__keptRoute === app.config.globalProperties.$router.currentRoute.value
    })

  async function openSheet(page: Page) {
    await page.getByRole('navigation', { name: 'Sections' }).getByRole('button', { name: 'Projects' }).click()
    const sheet = page.getByRole('dialog')
    await expect(sheet).toBeVisible()
    // The slide-in has ended.
    await sheet.evaluate((e) => Promise.all(e.getAnimations().map((a) => a.finished)))
    await expect(sheet.getByRole('link', { name: /^shop/ })).toBeVisible()
    await expect.poll(() => page.evaluate(() => (history.state as Record<string, unknown> | null)?.__sheet), 'the sheet pushed its entry').toBeDefined()
    return sheet
  }

  const closes: { how: string; close: (page: Page) => Promise<void> }[] = [
    { how: 'scrim', close: (page) => page.mouse.click(360, 600) },
    { how: 'close button', close: (page) => page.getByRole('dialog').getByRole('button', { name: 'Close' }).click() },
    { how: 'Escape', close: (page) => page.keyboard.press('Escape') },
    { how: 'back', close: (page) => page.evaluate(() => history.back()) },
  ]

  for (const { how, close } of closes) {
    test(`closes by ${how} and keeps the route@375`, async ({ page, baseURL }, testInfo) => {
      const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
      await page.setViewportSize({ width: 375, height: 812 })
      await page.clock.setFixedTime(now)
      await watch(page, new URL(baseURL ?? '').origin, problems)
      await mockAPI(page, start, problems)
      const requests = apiCount(page)
      await page.goto(start.path)
      await start.ready(page)
      await page.waitForLoadState('networkidle')
      const url = page.url()
      const length = await page.evaluate(() => history.length)

      const sheet = await openSheet(page)
      // The sheet covers the bars: the mobile bar's brand and the bottom
      // bar's Projects are under it or its scrim.
      for (const sel of ['header.mobilebar .brand-m', 'nav.bottombar button']) {
        const box = (await page.locator(sel).boundingBox())!
        expect(
          await page.evaluate(([x, y, s]) => !!document.elementFromPoint(Number(x), Number(y))?.closest(String(s)), [box.x + box.width / 2, box.y + box.height / 2, sel]),
          `${sel} is under the sheet`,
        ).toBe(false)
      }
      if (how === 'scrim') {
        const shot = join(testInfo.project.outputDir, '..', 'screenshots', 'rail-sheet@375.png')
        await page.screenshot({ path: shot })
        await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })
      }
      const before = requests()
      await keepRoute(page)

      await close(page)
      await expect(sheet).toBeHidden()
      await expect.poll(() => page.evaluate(() => (history.state as Record<string, unknown> | null)?.__sheet), 'the entry is gone').toBeUndefined()
      expect(page.url(), 'the URL is unchanged').toBe(url)
      expect(await sameRoute(page), 'the router did not navigate').toBe(true)
      await expect(page.locator('main').getByText(projectReady.stream).first()).toBeVisible()
      // A reload of the page's data would come within this time.
      await page.waitForTimeout(500)
      await page.waitForLoadState('networkidle')
      expect(requests(), 'API requests after closing').toBe(before)
      // The pushed entry was popped; at most it stays as a forward entry.
      expect(await page.evaluate(() => history.length)).toBe(length + 1)

      problems.csp.push(...(await page.evaluate(() => (window as unknown as { __cspViolations: string[] }).__cspViolations)))
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
    })
  }

  test('a project link navigates and closes the sheet, back returns@375', async ({ page, baseURL }) => {
    const inbox = routes.find((r) => r.name === 'inbox')!
    const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
    await page.setViewportSize({ width: 375, height: 812 })
    // No fixed clock: Vue skips a bubbling handler (RailNav's click) attached
    // at the event's own Date.now(), which a frozen clock makes equal.
    await watch(page, new URL(baseURL ?? '').origin, problems)
    await mockAPI(page, inbox, problems)
    await page.goto(inbox.path)
    await inbox.ready(page)

    // A link to the page the sheet is on closes it and stays.
    let sheet = await openSheet(page)
    await sheet.getByRole('link', { name: /^Inbox/ }).click()
    await expect(sheet).toBeHidden()
    await expect.poll(() => page.evaluate(() => (history.state as Record<string, unknown> | null)?.__sheet)).toBeUndefined()
    await expect(page).toHaveURL(/\/inbox$/)

    sheet = await openSheet(page)
    await sheet.getByRole('link', { name: /^shop/ }).click()
    await expect(page).toHaveURL(/\/projects\/team-a\/shop$/)
    await expect(sheet).toBeHidden()
    await start.ready(page)
    expect(await page.evaluate(() => (history.state as Record<string, unknown> | null)?.__sheet)).toBeUndefined()

    // One back is the Inbox: the sheet's entry is not left in between.
    await page.goBack()
    await expect(page).toHaveURL(/\/inbox$/)
    await inbox.ready(page)
    await expect(page.getByRole('dialog')).toHaveCount(0)
    expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
  })

  // Tablet and desktop keep the rail in the aside: no bottom bar, no sheet.
  for (const size of widths.filter((w) => w.width >= 760)) {
    test(`stays the aside@${size.width}`, async ({ page, baseURL }) => {
      const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
      await page.setViewportSize(size)
      await page.clock.setFixedTime(now)
      await watch(page, new URL(baseURL ?? '').origin, problems)
      await mockAPI(page, start, problems)
      await page.goto(start.path)
      await start.ready(page)
      await expect(page.locator('aside.rail').getByRole('link', { name: /^shop/ })).toBeVisible()
      await expect(page.locator('nav.bottombar')).toBeHidden()
      await expect(page.getByRole('dialog')).toHaveCount(0)
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
    })
  }
})

// The context sheet below 1180 px (Stream.vue, docs/ui.md "Sheets"): the
// Context button in the header carries the Needs-you count and opens the
// context panel from the right, on project and milestone pages. The sheet
// shows Needs you, the milestone's queue, its companions and the last 30
// days, fits the width, and back (or scrim, close button, Escape) closes it
// without leaving an entry. From 1180 px the project page keeps the panel at
// its side and neither page has the button.
test.describe('context sheet', () => {
  const pages = [
    { page: 'project', route: routes.find((r) => r.name === 'stream-project-stream')!, milestone: /^Checkout without account/ },
    { page: 'milestone', route: routes.find((r) => r.name === 'stream-milestone-issues')!, milestone: /^Payment providers/ },
  ]
  const sections = (m: RegExp) => [/^Needs you/, m, /^Companions/, /^Last 30 days/]
  const sheetEntry = (page: Page) => page.evaluate(() => (history.state as Record<string, unknown> | null)?.__sheet)

  async function load(page: Page, baseURL: string | undefined, route: Route, size: { width: number; height: number }) {
    const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
    await page.setViewportSize(size)
    await page.clock.setFixedTime(now)
    await watch(page, new URL(baseURL ?? '').origin, problems)
    await mockAPI(page, route, problems)
    await page.goto(route.path)
    await route.ready(page)
    await page.waitForLoadState('networkidle')
    return problems
  }

  async function openSheet(page: Page) {
    const button = page.locator('main').getByRole('button', { name: `Context, ${inbox.items.length} need you` })
    await expect(button, 'the button shows the Needs-you count').toContainText(String(inbox.items.length))
    await button.click()
    const sheet = page.getByRole('dialog', { name: 'Context' })
    await expect(sheet).toBeVisible()
    await sheet.evaluate((e) => Promise.all(e.getAnimations().map((a) => a.finished)))
    await expect.poll(() => sheetEntry(page), 'the sheet pushed its entry').toBeDefined()
    return sheet
  }

  for (const { page: name, route, milestone } of pages) {
    for (const size of widths.filter((w) => w.width < 1180)) {
      test(`${name} page opens it, back closes it@${size.width}`, async ({ page, baseURL }, testInfo) => {
        const problems = await load(page, baseURL, route, size)
        const url = page.url()
        const sheet = await openSheet(page)

        for (const heading of sections(milestone)) {
          await expect(sheet.getByRole('heading', { name: heading })).toBeVisible()
        }
        const shot = join(testInfo.project.outputDir, '..', 'screenshots', `context-sheet-${name}@${size.width}.png`)
        await page.screenshot({ path: shot })
        await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })

        // The page does not scroll sideways, the sheet lies within the
        // viewport, and nothing in it reaches past its edges. Content cut off
        // by an ancestor that clips (a truncated reason's text behind its
        // ellipsis) is not seen; the clipping ancestor itself is checked.
        // An ancestor that scrolls sideways does not count as clipping.
        const fit = await sheet.evaluate((e) => {
          const doc = document.scrollingElement ?? document.documentElement
          const r = e.getBoundingClientRect()
          const clipped = (c: Element) => {
            for (let p = c.parentElement; p && p !== e; p = p.parentElement) {
              if (['hidden', 'clip'].includes(getComputedStyle(p).overflowX)) return true
            }
            return false
          }
          const outside = [...e.querySelectorAll('*')]
            .filter((c) => {
              if (clipped(c)) return false
              const b = c.getBoundingClientRect()
              return b.width > 0 && (b.left < r.left - 0.5 || b.right > r.right + 0.5)
            })
            .map((c) => `${c.tagName.toLowerCase()}.${[...c.classList].join('.')}`)
          return { page: doc.scrollWidth - doc.clientWidth, inside: r.left >= 0 && r.right <= window.innerWidth, outside }
        })
        expect(fit, 'the page does not scroll sideways, the sheet fits the width').toEqual({ page: 0, inside: true, outside: [] })

        await page.evaluate(() => history.back())
        await expect(sheet).toBeHidden()
        await expect.poll(() => sheetEntry(page), 'the entry is gone').toBeUndefined()
        expect(page.url(), 'the URL is unchanged').toBe(url)

        problems.csp.push(...(await page.evaluate(() => (window as unknown as { __cspViolations: string[] }).__cspViolations)))
        expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
      })
    }
  }

  const closes: { how: string; close: (page: Page) => Promise<void> }[] = [
    { how: 'scrim', close: (page) => page.mouse.click(15, 400) },
    { how: 'close button', close: (page) => page.getByRole('dialog').getByRole('button', { name: 'Close' }).click() },
    { how: 'Escape', close: (page) => page.keyboard.press('Escape') },
  ]
  for (const { how, close } of closes) {
    test(`closes by ${how}@375`, async ({ page, baseURL }) => {
      const problems = await load(page, baseURL, pages[0]!.route, widths[0]!)
      const sheet = await openSheet(page)
      await close(page)
      await expect(sheet).toBeHidden()
      await expect.poll(() => sheetEntry(page), 'the entry is gone').toBeUndefined()
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
    })
  }

  for (const { page: name, route, milestone } of pages) {
    const project = name === 'project'
    test(`${name} page ${project ? 'keeps the side panel' : 'has no panel'} and no button@1280`, async ({ page, baseURL }) => {
      const problems = await load(page, baseURL, route, widths[2]!)
      await expect(page.locator('main').getByRole('button', { name: /^Context/ })).toHaveCount(0)
      const panel = page.locator('aside.ctxp')
      if (project) {
        for (const heading of sections(milestone)) await expect(panel.getByRole('heading', { name: heading })).toBeVisible()
      } else {
        await expect(panel).toHaveCount(0)
      }
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
    })
  }
})

// The bottom bar on a phone (App.vue, docs/ui.md "Navigation"): Inbox with
// its count, Overview, Projects (the rail sheet) and Research with the
// feature on; aria-current on the section shown. It hides while a text field
// has the focus, and the pane's end stays above it. Tablet and desktop have
// none (`rail sheet stays the aside@…`).
test.describe('bottom bar', () => {
  const task = routes.find((r) => r.name === 'task')!
  const bar = (page: Page) => page.getByRole('navigation', { name: 'Sections' })

  async function open(page: Page, baseURL: string | undefined, route: Route, problems: Problems, features?: { features: Feature[] }) {
    await page.setViewportSize({ width: 375, height: 812 })
    // No fixed clock: Vue skips a bubbling handler attached at the event's
    // own Date.now(), which a frozen clock makes equal (rail sheet).
    await watch(page, new URL(baseURL ?? '').origin, problems)
    await mockAPI(page, route, problems)
    // Registered last, so it answers before the fixtures.
    if (features) await page.route('**/api/v1/features', (r) => r.fulfill({ status: 200, json: features }))
    await page.goto(route.path)
    await route.ready(page)
  }

  test('items navigate and Projects opens the rail sheet@375', async ({ page, baseURL }, testInfo) => {
    const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
    await open(page, baseURL, task, problems)
    const nav = bar(page)
    await expect(nav).toBeVisible()
    const count = String(inbox.items?.length ?? 0)
    expect(Number(count), 'the fixture has Inbox items').toBeGreaterThan(0)
    await expect(nav.getByRole('link', { name: `Inbox (${count})`, exact: true })).toBeVisible()
    // The task page is no section of the bar.
    await expect(nav.locator('[aria-current]')).toHaveCount(0)
    for (const item of await nav.locator('a, button').all()) {
      expect((await item.boundingBox())!.height, 'hit area').toBeGreaterThanOrEqual(44)
    }
    const shot = join(testInfo.project.outputDir, '..', 'screenshots', 'bottom-bar@375.png')
    await page.screenshot({ path: shot })
    await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })

    for (const { name, url, route } of [
      { name: /^Inbox/, url: /\/inbox$/, route: 'inbox' },
      { name: 'Overview', url: /:\d+\/$/, route: 'overview' },
      { name: 'Research', url: /\/research$/, route: 'researches' },
    ]) {
      const link = nav.getByRole('link', { name })
      await link.click()
      await expect(page).toHaveURL(url)
      await routes.find((r) => r.name === route)!.ready(page)
      await expect(link).toHaveAttribute('aria-current', 'page')
      await expect(nav.locator('[aria-current]')).toHaveCount(1)
    }
    // A research's own page is in the Research section too.
    await page.locator('main').getByRole('link', { name: 'payments-news' }).first().click()
    await expect(page).toHaveURL(/\/research\/research\/payments-news$/)
    await expect(nav.getByRole('link', { name: 'Research' })).toHaveAttribute('aria-current', 'page')

    const projects = nav.getByRole('button', { name: 'Projects' })
    await expect(projects).toHaveAttribute('aria-expanded', 'false')
    await projects.click()
    const sheet = page.getByRole('dialog')
    await expect(sheet.getByRole('link', { name: /^shop/ })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(sheet).toBeHidden()
    expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
  })

  test('Research is absent without the feature@375', async ({ page, baseURL }) => {
    const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
    const overview = routes.find((r) => r.name === 'overview')!
    await open(page, baseURL, overview, problems, { features: features.features.filter((f) => f.name !== 'research') })
    const nav = bar(page)
    await expect(nav.getByRole('link', { name: 'Overview' })).toHaveAttribute('aria-current', 'page')
    await expect(nav.getByRole('link')).toHaveText([/Inbox/, 'Overview'])
    await expect(nav.getByRole('button')).toHaveText(['Projects'])
    expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
  })

  test('the end of a long page stays above the bar@375', async ({ page, baseURL }, testInfo) => {
    const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
    // The issue page: the task's Overview, then the issue's stream.
    await open(page, baseURL, routes.find((r) => r.name === 'issue')!, problems)
    await page.waitForLoadState('networkidle')
    await page.evaluate(() => window.scrollTo(0, document.scrollingElement!.scrollHeight))
    const seen = await page.evaluate(() => {
      const top = document.querySelector('nav.bottombar')!.getBoundingClientRect().top
      // The view's root, then down its last visible children to a leaf.
      const view = document.querySelector('main')!.lastElementChild!
      let last: Element = view
      for (;;) {
        const next = [...last.children].filter((c) => c.getBoundingClientRect().height > 0).at(-1)
        if (!next) break
        last = next
      }
      return {
        long: document.scrollingElement!.scrollHeight > 2 * innerHeight,
        view: view.getBoundingClientRect().bottom,
        last: last.getBoundingClientRect().bottom,
        top,
      }
    })
    const shot = join(testInfo.project.outputDir, '..', 'screenshots', 'bottom-bar-end@375.png')
    await page.screenshot({ path: shot })
    await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })
    expect(seen.long, 'the page is long').toBe(true)
    expect(seen.view, 'the view ends above the bar').toBeLessThanOrEqual(seen.top + 0.5)
    expect(seen.last, 'its last element ends above the bar').toBeLessThanOrEqual(seen.top + 0.5)
    expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
  })

  test('hides while the task composer has the focus@375', async ({ page, baseURL }, testInfo) => {
    const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
    await open(page, baseURL, routes.find((r) => r.name === 'task-chat')!, problems)
    const nav = bar(page)
    await expect(nav).toBeVisible()
    const composer = page.getByPlaceholder('Message the agent…')
    await composer.focus()
    await expect(nav).toBeHidden()
    const shot = join(testInfo.project.outputDir, '..', 'screenshots', 'bottom-bar-typing@375.png')
    await page.screenshot({ path: shot })
    await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })
    await composer.blur()
    await expect(nav).toBeVisible()
    expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
  })
})

// Task tabs (R10, decision 11): the task page and the issue page open on
// Overview with the last run's summary first and the open questions' count
// on the tab; every tab is one tap away and lives in ?tab=; an old
// #live-transcript link opens Live.
test.describe('task tabs', () => {
  const openQuestions = (refundsResponse.prompts ?? []).filter((p) => p.kind === 'needsInput' && (!p.state || p.state === 'open')).length

  for (const pageName of ['task', 'issue'] as const) {
    for (const { width, height } of [widths[0]!, widths[2]!]) {
      test(`${pageName} tabs, badge and summary first@${width}`, async ({ page, baseURL }, testInfo) => {
        const route = routes.find((r) => r.name === pageName)!
        const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
        await page.setViewportSize({ width, height })
        await page.clock.setFixedTime(now)
        await watch(page, new URL(baseURL ?? '').origin, problems)
        await mockAPI(page, route, problems)
        await page.goto(route.path)
        await route.ready(page)

        // The header's status line (D319-P1): phase · current step · last
        // result · last activity, above the tabs. It wraps, never wider than
        // the page; on a phone the long step is clamped and opens on tap.
        const status = page.locator('main [data-status-line]')
        await expect(status).toBeVisible()
        await expect(status).toContainText(/Idle\s*·\s*Waiting for an answer on refunds.*currency\s*·\s*NeedsInput\s*·\s*active \S/)
        expect(await status.evaluate((e) => e.scrollWidth <= e.clientWidth), 'the status line wraps').toBe(true)
        const step = status.getByRole('button')
        if (width < 760) {
          await expect(step).toHaveAttribute('aria-expanded', 'false')
          await step.click()
          await expect(step).toHaveAttribute('aria-expanded', 'true')
          await step.click()
        }
        expect(
          await page.locator('main').evaluate((main) => {
            const s = main.querySelector('[data-status-line]')!
            const nav = main.querySelector('nav[aria-label="Task"]')!
            return !!(s.compareDocumentPosition(nav) & Node.DOCUMENT_POSITION_FOLLOWING)
          }),
          'the status line comes before the tabs',
        ).toBe(true)

        const strip = page.locator('main').getByRole('navigation', { name: 'Task' })
        await expect(strip.getByRole('link')).toHaveText(taskTabs.map((t) => new RegExp(`^${t.label}`)))
        const overview = strip.getByRole('link', { name: /^Overview/ })
        await expect(overview).toHaveAttribute('aria-current', 'page')
        expect(openQuestions, 'the fixture has an open question').toBeGreaterThan(0)
        await expect(overview).toHaveText(`Overview ${openQuestions}`)
        await expect(page.locator('main nav[aria-label="Task"] + section > h3')).toHaveText('Summary of the last run')

        for (const t of taskTabs.slice(1)) {
          await strip.getByRole('link', { name: t.label, exact: true }).click()
          await expect(page).toHaveURL(new RegExp(`[?&]tab=${t.id}$`))
          await expect(page.locator('main').getByText(taskReady[t.id]).first()).toBeVisible()
          await expect(strip.locator('[aria-current="page"]')).toHaveText(t.label)
          await expect(page.locator('main').getByText('Summary of the last run')).toHaveCount(0)
        }
        await overview.click()
        await expect(page).not.toHaveURL(/[?&]tab=/)
        await expect(page.locator('main').getByText('Summary of the last run')).toBeVisible()

        const shot = join(testInfo.project.outputDir, '..', 'screenshots', `task-tabs-${pageName}@${width}.png`)
        await page.screenshot({ path: shot })
        await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })
        expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
      })
    }

    test(`${pageName} #live-transcript opens Live@375`, async ({ page, baseURL }) => {
      const route = routes.find((r) => r.name === pageName)!
      const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
      await page.setViewportSize({ width: 375, height: 812 })
      await page.clock.setFixedTime(now)
      await watch(page, new URL(baseURL ?? '').origin, problems)
      await mockAPI(page, route, problems)
      await page.goto(`${route.path}#live-transcript`)
      const strip = page.locator('main').getByRole('navigation', { name: 'Task' })
      await expect(strip.getByRole('link', { name: 'Live', exact: true })).toHaveAttribute('aria-current', 'page')
      await expect(page.locator('#live-transcript')).toContainText(taskReady.live)
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
    })
  }
})

// Block markdown (R8, decision 7): the run summary and a conversation note
// render as markdown, no literal asterisks.
test.describe('block markdown', () => {
  for (const { width, height } of [widths[0]!, widths[2]!]) {
    test(`task summary and note render markdown@${width}`, async ({ page, baseURL }, testInfo) => {
      const route = routes.find((r) => r.name === 'task')!
      const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
      await page.setViewportSize({ width, height })
      await page.clock.setFixedTime(now)
      await watch(page, new URL(baseURL ?? '').origin, problems)
      await mockAPI(page, route, problems)
      await page.goto(route.path)
      await route.ready(page)

      // The summary's box follows its heading (TaskBody.vue, Overview).
      const summary = page
        .locator('main section')
        .filter({ has: page.getByRole('heading', { name: 'Summary of the last run', exact: true }) })
        .locator('.card')
        .first()
      await expect(summary.locator('strong').first()).toHaveText('full refunds')
      expect(await summary.locator('li').count(), 'a list').toBeGreaterThan(1)
      await expect(summary).not.toContainText('**')

      await page.locator('main').getByRole('navigation', { name: 'Task' }).getByRole('link', { name: 'Chat' }).click()
      const note = page.locator('main .md').filter({ hasText: 'partial refunds' }).filter({ has: page.locator('strong') }).first()
      await expect(note.locator('strong').first()).toBeVisible()
      await expect(note).not.toContainText('**')

      const shot = join(testInfo.project.outputDir, '..', 'screenshots', `markdown-task@${width}.png`)
      await page.screenshot({ path: shot, fullPage: true })
      await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
    })
  }
})

// Inline markdown (R9, decision 7): one-line agent and human text keeps
// strong and code, shows no literal asterisks, and nests no link in a link.
test.describe('inline markdown', () => {
  for (const { width, height } of [widths[0]!, widths[2]!]) {
    for (const name of ['inbox', 'stream-milestone-stream', 'stream-milestone-pilot']) {
      test(`${name} shows no literal ** and no nested links@${width}`, async ({ page, baseURL }, testInfo) => {
        const route = routes.find((r) => r.name === name)!
        const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
        await page.setViewportSize({ width, height })
        await page.clock.setFixedTime(now)
        await watch(page, new URL(baseURL ?? '').origin, problems)
        await mockAPI(page, route, problems)
        await page.goto(route.path)
        await route.ready(page)

        await expect(page.locator('main .md-inline strong').first()).toBeVisible()
        await expect(page.locator('main')).not.toContainText('**')
        expect(await page.evaluate(() => document.querySelectorAll('a a').length), 'nested links').toBe(0)

        const shot = join(testInfo.project.outputDir, '..', 'screenshots', `inline-markdown-${name}@${width}.png`)
        await page.screenshot({ path: shot, fullPage: true })
        await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })
        expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
      })
    }
  }
})

// The composer (Composer.vue, R11): one component for the task's chat and
// the project stream's notes. Typing and Ctrl+Enter or Send post the body
// the callers built; on a phone the text is 16 px, Send at least 44 px, and
// the chat composer stays at the bottom of the screen, above the bottom bar
// or the keyboard (--keyboard-inset).
test.describe('composer', () => {
  // captured answers POSTs to paths matching re and keeps their URLs and
  // bodies; registered after mockAPI, so it answers first.
  async function capture(page: Page, re: RegExp, json: unknown) {
    const seen: { path: string; body: unknown }[] = []
    await page.route(
      (url) => re.test(url.pathname),
      async (r) => {
        if (r.request().method() !== 'POST') return r.fallback()
        seen.push({ path: new URL(r.request().url()).pathname, body: r.request().postDataJSON() })
        return r.fulfill({ status: 200, json })
      },
    )
    return seen
  }

  async function open(page: Page, baseURL: string | undefined, name: string, size: { width: number; height: number }) {
    const route = routes.find((r) => r.name === name)!
    const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
    await page.setViewportSize(size)
    await watch(page, new URL(baseURL ?? '').origin, problems)
    await mockAPI(page, route, problems)
    return { route, problems }
  }

  async function touchSizes(box: ReturnType<Page['locator']>, phone: boolean) {
    const text = box.locator('textarea')
    const send = box.getByRole('button', { name: 'Send' })
    const font = await text.evaluate((e) => parseFloat(getComputedStyle(e).fontSize))
    const h = (await send.boundingBox())!.height
    if (phone) {
      expect(font, 'textarea font size').toBe(16)
      expect(h, 'Send height').toBeGreaterThanOrEqual(44)
    } else {
      expect(font, 'textarea font size on a desktop').toBeLessThan(16)
    }
  }

  for (const size of [widths[0]!, widths[2]!]) {
    const phone = size.width < 760

    test(`task chat sends a message@${size.width}`, async ({ page, baseURL }, testInfo) => {
      const { route, problems } = await open(page, baseURL, 'task-chat', size)
      const posts = await capture(page, /\/api\/v1\/projects\/team-a\/shop\/tasks\/[^/]+\/chat$/, { id: 99 })
      await page.goto(route.path)
      await route.ready(page)

      const text = page.getByPlaceholder('Message the agent…')
      const composer = page.locator('.composer.chat')
      await touchSizes(composer, phone)
      await expect(composer.getByRole('button', { name: 'Send' })).toBeDisabled()
      await text.fill('  Keep the refund reasons short.  ')
      const shot = join(testInfo.project.outputDir, '..', 'screenshots', `composer-chat@${size.width}.png`)
      await page.screenshot({ path: shot })
      await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })
      await composer.getByRole('button', { name: 'Send' }).click()

      await expect(text).toHaveValue('')
      expect(posts).toHaveLength(1)
      expect(posts[0]!.path).toBe('/api/v1/projects/team-a/shop/tasks/shop-281-implement/chat')
      expect(posts[0]!.body).toEqual({ body: 'Keep the refund reasons short.', clientKey: expect.any(String), kind: 'message' })

      // Ctrl+Enter sends too, with a new key.
      await text.fill('And link the issue.')
      await text.press('Control+Enter')
      await expect(text).toHaveValue('')
      expect(posts).toHaveLength(2)
      expect(posts[1]!.body).toMatchObject({ body: 'And link the issue.', kind: 'message' })
      expect((posts[1]!.body as { clientKey: string }).clientKey).not.toBe((posts[0]!.body as { clientKey: string }).clientKey)
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
    })

    test(`task chat keeps the draft and shows the error of a failed send@${size.width}`, async ({ page, baseURL }) => {
      const { route, problems } = await open(page, baseURL, 'task-chat', size)
      const keys: string[] = []
      await page.route(/\/api\/v1\/projects\/team-a\/shop\/tasks\/[^/]+\/chat$/, async (r) => {
        if (r.request().method() !== 'POST') return r.fallback()
        keys.push((r.request().postDataJSON() as { clientKey: string }).clientKey)
        return r.fulfill({ status: 429, json: { error: 'slow down' } })
      })
      await page.goto(route.path)
      await route.ready(page)
      const text = page.getByPlaceholder('Message the agent…')
      await text.fill('Retry me')
      await text.press('Control+Enter')
      await expect(page.getByText('Too many messages; try again in a minute.')).toBeVisible()
      await expect(text).toHaveValue('Retry me')
      // A reload restores the draft, and the retry sends the same key.
      await page.reload()
      await route.ready(page)
      await expect(text).toHaveValue('Retry me')
      await page.locator('.composer.chat').getByRole('button', { name: 'Send' }).click()
      await expect(page.getByText('Too many messages; try again in a minute.')).toBeVisible()
      expect(keys).toHaveLength(2)
      expect(keys[1]).toBe(keys[0])
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
    })

    test(`project stream posts a note@${size.width}`, async ({ page, baseURL }, testInfo) => {
      const { route, problems } = await open(page, baseURL, 'stream-project-stream', size)
      const posts = await capture(page, /\/api\/v1\/projects\/team-a\/shop\/(milestones\/\d+|tasks\/[^/]+)\/notes$/, {})
      await page.goto(route.path)
      await route.ready(page)

      const composer = page.locator('.composer.note')
      const text = composer.locator('textarea')
      await expect(text).toBeEnabled()
      await touchSizes(composer, phone)

      // The target menu lists where to post; the first one is picked.
      const targetButton = composer.getByRole('button', { name: /^→/ })
      const target = (await targetButton.textContent())!
      await targetButton.click()
      await expect(composer.getByText('Send to')).toBeVisible()
      await targetButton.click()
      await expect(composer.getByText('Send to')).toBeHidden()

      // "/" opens the commands; picking one fills the text.
      await text.fill('/')
      await text.dispatchEvent('input')
      await composer.getByRole('button', { name: /\/pilot pause/ }).click()
      await expect(text).toHaveValue('/pilot pause ')
      await text.fill('Ship the refunds first.')
      const shot = join(testInfo.project.outputDir, '..', 'screenshots', `composer-note@${size.width}.png`)
      await page.screenshot({ path: shot })
      await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })
      await text.press('Control+Enter')

      await expect(text).toHaveValue('')
      expect(posts).toHaveLength(1)
      if (target.includes('Pilot')) {
        expect(posts[0]!.path).toMatch(/\/milestones\/\d+\/notes$/)
        expect(posts[0]!.body).toEqual({ body: 'Ship the refunds first.' })
      } else {
        expect(posts[0]!.path).toMatch(/\/tasks\/[^/]+\/notes$/)
        expect(posts[0]!.body).toEqual({ target: 'issue', body: 'Ship the refunds first.' })
      }
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
    })
  }

  test('chat composer stays above the bottom bar and the keyboard@375', async ({ page, baseURL }, testInfo) => {
    const { route, problems } = await open(page, baseURL, 'task-chat', widths[0]!)
    await page.goto(route.path)
    await route.ready(page)
    const composer = page.locator('.composer.chat')
    const bottom = async () => (await composer.boundingBox())!.y + (await composer.boundingBox())!.height
    const barTop = await page.getByRole('navigation', { name: 'Sections' }).evaluate((e) => e.getBoundingClientRect().top)

    await page.evaluate(() => window.scrollTo(0, 0))
    expect(await bottom(), 'the composer ends above the bottom bar').toBeLessThanOrEqual(barTop + 0.5)
    expect((await composer.boundingBox())!.y, 'and is on screen').toBeLessThan(barTop)

    // Typing: the bar goes; an iOS keyboard of 300 px (what
    // useKeyboardInset sets from visualViewport) lifts the composer.
    await composer.locator('textarea').focus()
    await page.evaluate(() => document.documentElement.style.setProperty('--keyboard-inset', '300px'))
    // The chat card from 100 px down: the composer floats within it.
    await page.locator('.card.chat').evaluate((e) => window.scrollBy(0, e.getBoundingClientRect().top - 100))
    await expect.poll(bottom).toBeLessThanOrEqual(812 - 300 + 0.5)
    expect(await bottom()).toBeGreaterThan(812 - 300 - 2)
    const shot = join(testInfo.project.outputDir, '..', 'screenshots', 'composer-keyboard@375.png')
    await page.screenshot({ path: shot })
    await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })
    expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
  })
})

// The prompt card (PromptCard.vue, R11): one component, full on the task
// page and the milestone's Pilot tab, compact in the stream. An option, the
// text box and the compact card's recommended option post an answer command
// to the prompt's target task's chat; the card then waits for the reply. On
// a phone the option buttons and Send are at least 44 px and the text 16 px.
test.describe('prompt card', () => {
  const chatPath = `/api/v1/projects/team-a/shop/tasks/${refundsPrompt.target.task}/chat`

  async function open(page: Page, baseURL: string | undefined, name: string, size: { width: number; height: number }) {
    const route = routes.find((r) => r.name === name)!
    const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
    await page.setViewportSize(size)
    await page.clock.setFixedTime(now)
    await watch(page, new URL(baseURL ?? '').origin, problems)
    await mockAPI(page, route, problems)
    // Registered after mockAPI, so it answers the POSTs first.
    const posts: unknown[] = []
    await page.route(
      (url) => url.pathname === chatPath,
      async (r) => {
        if (r.request().method() !== 'POST') return r.fallback()
        posts.push(r.request().postDataJSON())
        return r.fulfill({ status: 200, json: { id: 991 } })
      },
    )
    await page.goto(route.path)
    await route.ready(page)
    return { problems, posts }
  }

  async function shoot(page: Page, testInfo: { project: { outputDir: string }; attach: (n: string, o: { path: string; contentType: string }) => Promise<void> }, name: string) {
    const shot = join(testInfo.project.outputDir, '..', 'screenshots', `${name}.png`)
    await page.screenshot({ path: shot, fullPage: true })
    await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })
  }

  async function noSidewaysScroll(page: Page) {
    const overflow = await page.evaluate(() => {
      const doc = document.scrollingElement ?? document.documentElement
      const main = document.querySelector('main')!
      return { page: doc.scrollWidth - doc.clientWidth, pane: main.scrollWidth - main.clientWidth }
    })
    expect(overflow, 'sideways scroll in px').toEqual({ page: 0, pane: 0 })
  }

  const command = (cmd: Record<string, string>) => ({ body: JSON.stringify(cmd), clientKey: expect.any(String), kind: 'command' })

  for (const size of [widths[0]!, widths[2]!]) {
    const phone = size.width < 760

    test(`task page answers with an option@${size.width}`, async ({ page, baseURL }, testInfo) => {
      const { problems, posts } = await open(page, baseURL, 'task', size)
      const card = page.locator(`#prompt-${refundsPrompt.id}`)
      const option = card.getByRole('button', { name: 'Split across captures', exact: true })
      await expect(option).toBeVisible()
      if (phone) {
        expect((await option.boundingBox())!.height, 'option height').toBeGreaterThanOrEqual(44)
        expect(await card.locator('textarea').evaluate((e) => parseFloat(getComputedStyle(e).fontSize)), 'text size').toBe(16)
      }
      await expect(card.locator('.md strong').first()).toHaveText('partial refund')
      await noSidewaysScroll(page)
      await shoot(page, testInfo, `prompt-card-task@${size.width}`)

      await option.click()
      await expect(card.getByText('sent…')).toBeVisible()
      await expect(card.getByRole('button', { name: 'Refuse above one capture', exact: true })).toBeDisabled()
      expect(posts).toEqual([command({ cmd: 'answer', prompt: refundsPrompt.id, option: 'split' })])

      // The answered card of the same task shows who answered, read-only.
      const closed = page.locator('#prompt-p-281-0')
      await expect(closed.getByText('Answered by jo in the Desk: The order currency')).toBeVisible()
      await expect(closed.getByRole('button')).toHaveCount(0)
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
    })

    test(`pilot tab answers in free text@${size.width}`, async ({ page, baseURL }, testInfo) => {
      const { problems, posts } = await open(page, baseURL, 'stream-milestone-pilot', size)
      const card = page.locator(`#prompt-${refundsPrompt.id}`)
      const text = card.getByRole('textbox', { name: `Answer ${refundsPrompt.id} in your words` })
      const send = card.getByRole('button', { name: 'Send', exact: true })
      await expect(send).toBeDisabled()
      if (phone) expect((await send.boundingBox())!.height, 'Send height').toBeGreaterThanOrEqual(44)
      await text.fill('  Split, but never more than three captures.  ')
      await noSidewaysScroll(page)
      await shoot(page, testInfo, `prompt-card-pilot@${size.width}`)
      await send.click()

      await expect(text).toHaveValue('')
      await expect(card.getByText('Your answer:')).toBeVisible()
      expect(posts).toEqual([command({ cmd: 'answer', prompt: refundsPrompt.id, text: 'Split, but never more than three captures.' })])
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
    })

    test(`stream compact card answers with the recommended option@${size.width}`, async ({ page, baseURL }, testInfo) => {
      const { problems, posts } = await open(page, baseURL, 'stream-milestone-stream', size)
      // The prompt.posted row of the stream (e2e/fixtures/overview.ts).
      const card = page.locator(`.ev [data-prompt="${refundsPrompt.id}"]`)
      await expect(card).toHaveCount(1)
      await expect(card.locator('.md-inline strong')).toHaveText('partial refund')
      await expect(card.getByRole('link', { name: 'more…' })).toHaveAttribute('href', `/projects/team-a/shop/milestones/14#prompt-${refundsPrompt.id}`)
      // exact: a clipped question is a Truncated button whose name contains
      // the option's words.
      const recommended = card.getByRole('button', { name: 'Split across captures', exact: true })
      if (phone) expect((await recommended.boundingBox())!.height, 'button height').toBeGreaterThanOrEqual(44)
      await expect(card.getByRole('button', { name: 'Refuse above one capture', exact: true })).toHaveCount(0)
      await noSidewaysScroll(page)
      await shoot(page, testInfo, `prompt-card-compact@${size.width}`)

      await recommended.click()
      await expect(card.getByText('sent…')).toBeVisible()
      expect(posts).toEqual([command({ cmd: 'answer', prompt: refundsPrompt.id, option: 'split' })])
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
    })
  }
})

// The note thread (NoteThread.vue, R11): one renderer for the task page's
// conversation and the stream's threads. The conversation shows every note
// of the picked issue or MR and posts a comment; the stream's thread shows
// the last three notes and "N earlier" for the rest. On a phone the tabs,
// the commands, Post and "earlier" are at least 44 px.
test.describe('note thread', () => {
  async function minHeight(loc: ReturnType<Page['locator']>) {
    let min = Infinity
    for (const b of await loc.all()) min = Math.min(min, (await b.boundingBox())!.height)
    return min
  }

  for (const size of [widths[0]!, widths[2]!]) {
    const phone = size.width < 760

    test(`conversation shows the notes and posts a comment@${size.width}`, async ({ page, baseURL }, testInfo) => {
      const route = routes.find((r) => r.name === 'task-chat')!
      const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
      await page.setViewportSize(size)
      await page.clock.setFixedTime(now)
      await watch(page, new URL(baseURL ?? '').origin, problems)
      await mockAPI(page, route, problems)
      const posts: unknown[] = []
      await page.route(
        (url) => url.pathname.endsWith('/tasks/shop-281-implement/notes'),
        async (r) => {
          posts.push(r.request().postDataJSON())
          return r.fulfill({ status: 200, json: {} })
        },
      )
      await page.goto(route.path)
      await route.ready(page)

      const main = page.locator('main')
      const mine = main.locator('[data-note="51001"]')
      await expect(mine.getByText('you', { exact: true })).toBeVisible()
      await expect(mine.locator('strong').first()).toHaveText('card and wallet')
      await expect(main.locator('[data-note="51002"]')).toHaveCSS('opacity', '0.6')

      const mr = main.getByRole('button', { name: 'MR !305' })
      await mr.click()
      await expect(mr).toHaveAttribute('aria-pressed', 'true')
      const review = main.locator('[data-note="51010"]')
      await expect(review.getByText('@reviewer', { exact: true })).toBeVisible()
      await expect(review).not.toContainText('**')

      const form = main.locator('form').filter({ has: page.getByRole('button', { name: 'Post comment' }) })
      if (phone) {
        expect(await minHeight(form.getByRole('button')), 'commands and Post').toBeGreaterThanOrEqual(44)
        expect(await minHeight(main.getByRole('button', { name: /^(Issue|MR) / })), 'thread tabs').toBeGreaterThanOrEqual(44)
      }
      await form.getByRole('button', { name: '/agent retry' }).click()
      await form.getByRole('button', { name: 'Post comment' }).click()
      await expect.poll(() => posts).toEqual([{ target: 'mr', body: '/agent retry' }])

      const shot = join(testInfo.project.outputDir, '..', 'screenshots', `note-thread-task@${size.width}.png`)
      await page.screenshot({ path: shot, fullPage: true })
      await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
    })

    test(`stream thread folds earlier notes@${size.width}`, async ({ page, baseURL }, testInfo) => {
      const route = routes.find((r) => r.name === 'stream-milestone-stream')!
      const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
      await page.setViewportSize(size)
      await page.clock.setFixedTime(now)
      await watch(page, new URL(baseURL ?? '').origin, problems)
      await mockAPI(page, route, problems)
      await page.goto(route.path)
      await route.ready(page)

      const main = page.locator('main')
      const thread = main.locator('div').filter({ has: page.locator('[data-note="2005"]') }).last()
      const earlier = thread.getByRole('button', { name: '1 earlier' })
      await expect(thread.locator('[data-note]')).toHaveCount(3)
      await expect(thread.locator('[data-note="2004"]').getByText('Pilot', { exact: true })).toBeVisible()
      await expect(thread.locator('[data-note="2005"]').getByText('you', { exact: true })).toBeVisible()
      if (phone) expect((await earlier.boundingBox())!.height, '"earlier" height').toBeGreaterThanOrEqual(44)
      await earlier.click()
      await expect(thread.locator('[data-note]')).toHaveCount(4)
      await expect(earlier).toHaveCount(0)
      await expect(thread).not.toContainText('**')

      const shot = join(testInfo.project.outputDir, '..', 'screenshots', `note-thread-stream@${size.width}.png`)
      await page.screenshot({ path: shot, fullPage: true })
      await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
    })
  }
})

// Milestone rows (MilestoneList.vue, R11): from tablet width each row is a
// subgrid of the list's one column grid, so its cells start where the
// column heads do. On a phone the heads are hidden and the row stacks:
// title and state on one line, the figures below.
test.describe('milestone rows', () => {
  const pages = [
    { name: 'overview', project: true },
    { name: 'stream-project-stream', project: false },
  ]
  for (const { name, project } of pages) {
    for (const size of widths) {
      const phone = size.width < 760

      test(`${name} ${phone ? 'rows stack' : 'rows follow the heads'}@${size.width}`, async ({ page, baseURL }, testInfo) => {
        const route = routes.find((r) => r.name === name)!
        const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
        await page.setViewportSize(size)
        await page.clock.setFixedTime(now)
        await watch(page, new URL(baseURL ?? '').origin, problems)
        await mockAPI(page, route, problems)
        await page.goto(route.path)
        await route.ready(page)

        const list = page.locator('main ul[aria-label="In progress"]')
        await expect(list.locator('li').first()).toBeVisible()
        const heads = list.locator('xpath=preceding-sibling::*[@data-heads]')
        const layout = await list.evaluate((ul) => {
          const left = (e: Element) => Math.round(e.getBoundingClientRect().left)
          // A row's cells, the figures' wrapper (display: contents from
          // tablet width) unfolded.
          const cells = (li: Element) =>
            [...li.children].flatMap((c) => (getComputedStyle(c).display === 'contents' ? [...c.children] : [c]))
          const head = ul.previousElementSibling!
          return {
            heads: [...head.children].map(left),
            rows: [...ul.children].map((li) => cells(li).map(left)),
            stacked: [...ul.children].map((li) => {
              const box = (e: Element) => e.getBoundingClientRect()
              const [title, state, figures] = [...li.children].slice(-3).map(box)
              return {
                titleStateOneLine: Math.abs(title!.top + title!.height / 2 - (state!.top + state!.height / 2)) < 12,
                figuresBelow: figures!.top >= title!.bottom - 1,
                inside: li.scrollWidth <= li.clientWidth,
              }
            }),
          }
        })

        if (phone) {
          await expect(heads).toBeHidden()
          for (const s of layout.stacked) expect(s).toEqual({ titleStateOneLine: true, figuresBelow: true, inside: true })
        } else {
          await expect(heads).toBeVisible()
          expect(layout.heads).toHaveLength(project ? 7 : 6)
          for (const row of layout.rows) expect(row).toEqual(layout.heads)
        }

        const shot = join(testInfo.project.outputDir, '..', 'screenshots', `milestone-rows-${name}@${size.width}.png`)
        await page.screenshot({ path: shot, fullPage: true })
        await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })
        expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
      })
    }
  }
})

// The overview's Working now cards (DashboardOverview.vue, R12): one column
// on a phone, two on a tablet, and on desktop as many columns of at least
// 300 px as fit, as before. The Inbox chip of the summary strip is a link of
// at least 44 px on a phone and keeps its chip height above.
test.describe('overview cards', () => {
  for (const size of widths) {
    test(`working now columns@${size.width}`, async ({ page, baseURL }, testInfo) => {
      const route = routes.find((r) => r.name === 'overview')!
      const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
      await page.setViewportSize(size)
      await page.clock.setFixedTime(now)
      await watch(page, new URL(baseURL ?? '').origin, problems)
      await mockAPI(page, route, problems)
      await page.goto(route.path)
      await route.ready(page)

      const cards = page.locator('main article')
      await expect(cards).toHaveCount(overview.working.length)
      const [a, b] = await cards.evaluateAll((els) =>
        els.slice(0, 2).map((e) => {
          const r = e.getBoundingClientRect()
          return { left: Math.round(r.left), top: Math.round(r.top), width: Math.round(r.width) }
        }),
      )
      const grid = (await cards.first().locator('xpath=..').boundingBox())!
      if (size.width < 760) {
        expect(b!.left, 'one column').toBe(a!.left)
        expect(b!.top, 'stacked').toBeGreaterThan(a!.top)
        expect(a!.width, 'full width').toBeGreaterThan(grid.width - 2)
      } else if (size.width < 1180) {
        expect(b!.top, 'one row').toBe(a!.top)
        expect(a!.width, 'half the width each').toBeGreaterThan(grid.width / 2 - 12)
      } else {
        expect(b!.top, 'one row').toBe(a!.top)
        expect(a!.width, 'at least 300 px').toBeGreaterThanOrEqual(300)
        expect(a!.width, 'room for more columns').toBeLessThan(grid.width / 2 - 12)
      }

      const chip = page.locator('main ul[aria-label="Summary"] a[href="/inbox"]')
      const height = (await chip.boundingBox())!.height
      if (size.width < 760) expect(height, 'Inbox chip on a phone').toBeGreaterThanOrEqual(44)
      else expect(height, 'Inbox chip above').toBeLessThan(44)

      const shot = join(testInfo.project.outputDir, '..', 'screenshots', `overview-cards@${size.width}.png`)
      await page.screenshot({ path: shot, fullPage: true })
      await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
    })
  }
})

// The milestone's Issues tab (MilestoneIssues.vue, R12): issue numbers stay
// on one line, release rows sit between the issues in the same order at
// every width. Below desktop width an issue's number and title come first,
// its state and figures below: on a tablet the rail leaves too little room
// for eight columns. Links are at least 44 px high on a phone. From desktop
// width every issue row's cells line up with the column heads.
test.describe('milestone issues', () => {
  for (const size of widths) {
    const phone = size.width < 760
    const stacked = size.width < 1180
    test(`issues and releases@${size.width}`, async ({ page, baseURL }, testInfo) => {
      const route = routes.find((r) => r.name === 'stream-milestone-issues')!
      const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
      await page.setViewportSize(size)
      await page.clock.setFixedTime(now)
      await watch(page, new URL(baseURL ?? '').origin, problems)
      await mockAPI(page, route, problems)
      await page.goto(route.path)
      await route.ready(page)

      const table = page.locator('main [role="table"][aria-label="Issues"]')
      // Issues and releases in the order of the fixture: each release
      // follows the issue it was decided after.
      const order = await table
        .locator('[data-issue], [data-release]')
        .evaluateAll((els) =>
          els.map((e) => (e.hasAttribute('data-release') ? 'release' : e.querySelector('[data-iid]')!.textContent!.trim())),
        )
      expect(order).toEqual(['#279', 'release', '#280', 'release', '#281', '#282', 'release', '#283', '#284'])
      await expect(table.locator('[data-issue]').first()).toContainText('2026.11.3')

      const layout = await table.evaluate((t) => {
        const box = (e: Element) => e.getBoundingClientRect()
        const lineHeight = (e: Element) => parseFloat(getComputedStyle(e).lineHeight) || 20
        const head = t.querySelector('[data-heads]')!
        return {
          iidsOneLine: [...t.querySelectorAll('[data-iid]')].every((e) => box(e).height < lineHeight(e) * 1.5),
          inside: t.scrollWidth <= t.clientWidth,
          heads: [...head.children].map((c) => Math.round(box(c).left)),
          headsShown: getComputedStyle(head).display !== 'none',
          rows: [...t.querySelectorAll('[data-issue]')].map((row) => {
            const cells = [...row.querySelectorAll('[role="cell"]')]
            const [title, state] = cells.map(box)
            return {
              lefts: cells.map((c) => Math.round(box(c).left)),
              stateBelowTitle: state!.top >= title!.bottom - 1,
            }
          }),
          links: [...t.querySelectorAll('a')].map((a) => box(a).height),
        }
      })
      expect(layout.iidsOneLine, 'issue numbers on one line').toBe(true)
      expect(layout.inside, 'no sideways scroll in the table').toBe(true)
      if (phone) for (const h of layout.links) expect(h, 'link height on a phone').toBeGreaterThanOrEqual(44)
      if (stacked) {
        expect(layout.headsShown).toBe(false)
        for (const r of layout.rows) expect(r.stateBelowTitle).toBe(true)
      } else {
        expect(layout.headsShown).toBe(true)
        expect(layout.heads).toHaveLength(8)
        for (const r of layout.rows) expect(r.lefts).toEqual(layout.heads)
      }

      const shot = join(testInfo.project.outputDir, '..', 'screenshots', `milestone-issues@${size.width}.png`)
      await page.screenshot({ path: shot, fullPage: true })
      await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
    })
  }
})

// The milestone's Pilot tab (MilestonePilot.vue) and the queue
// (QueueList.vue, R12), utilities only. A prompt link without ?tab= opens
// the Pilot tab, unfolds Earlier questions for a closed prompt and scrolls
// to its card. The queue keeps its order, numbers stay on one line, and on
// a phone the links and the fold are 44 px high.
test.describe('milestone pilot', () => {
  async function open(page: Page, baseURL: string | undefined, name: string, size: { width: number; height: number }, path?: string) {
    const route = routes.find((r) => r.name === name)!
    const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
    await page.setViewportSize(size)
    await page.clock.setFixedTime(now)
    await watch(page, new URL(baseURL ?? '').origin, problems)
    await mockAPI(page, route, problems)
    await page.goto(path ?? route.path)
    await route.ready(page)
    return problems
  }

  async function shoot(page: Page, testInfo: { project: { outputDir: string }; attach: (n: string, o: { path: string; contentType: string }) => Promise<void> }, name: string) {
    const shot = join(testInfo.project.outputDir, '..', 'screenshots', `${name}.png`)
    await page.screenshot({ path: shot, fullPage: true })
    await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })
  }

  async function sideways(page: Page) {
    return page.evaluate(() => {
      const doc = document.scrollingElement ?? document.documentElement
      const main = document.querySelector('main')!
      return { page: doc.scrollWidth - doc.clientWidth, pane: main.scrollWidth - main.clientWidth }
    })
  }

  for (const size of widths) {
    const phone = size.width < 760

    test(`pilot tab and prompt link@${size.width}`, async ({ page, baseURL }, testInfo) => {
      const problems = await open(page, baseURL, 'stream-milestone-pilot', size)
      expect(await sideways(page), 'sideways scroll in px').toEqual({ page: 0, pane: 0 })
      if (phone) {
        const heights = await page.locator('main').evaluate((main) => {
          const pilot = main.querySelector('[data-pilot]')!
          return {
            links: [...pilot.querySelectorAll('a')].map((a) => a.getBoundingClientRect().height),
            fold: main.querySelector('[data-earlier] > summary')!.getBoundingClientRect().height,
          }
        })
        for (const h of heights.links) expect(h, 'pilot link height').toBeGreaterThanOrEqual(44)
        expect(heights.fold, 'Earlier questions height').toBeGreaterThanOrEqual(44)
      }
      await shoot(page, testInfo, `milestone-pilot@${size.width}`)

      // The answered Q3 is folded under Earlier questions: its link opens
      // the Pilot tab, unfolds it and scrolls to the card.
      await page.goto('/projects/team-a/shop/milestones/14#prompt-Q3')
      const card = page.locator('#prompt-Q3')
      await expect(card).toBeVisible()
      await expect(page.locator('main [data-earlier]')).toHaveAttribute('open', '')
      await expect(card).toBeInViewport()
      expect(await sideways(page), 'sideways scroll in px').toEqual({ page: 0, pane: 0 })
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
    })

    test(`queue order and numbers@${size.width}`, async ({ page, baseURL }, testInfo) => {
      const problems = await open(page, baseURL, 'stream-milestone-issues', size)
      const queue = page.locator('main ol[aria-label="Queue"]')
      await expect(queue).toBeVisible()
      const layout = await queue.evaluate((q) => {
        const box = (e: Element) => e.getBoundingClientRect()
        const items = [...q.querySelectorAll('[data-queue-item]')]
        return {
          order: items.map((i) => i.querySelector('[data-iid]')!.textContent!.trim()),
          positions: items.map((i) => i.querySelector('[data-position]')!.textContent!.trim()),
          // One line box per number: a range over its text has one rect.
          iidsOneLine: items.every((i) => {
            const text = [...i.querySelector('[data-iid]')!.childNodes].find((c) => c.nodeType === 3 && c.textContent!.trim())!
            const range = document.createRange()
            range.selectNodeContents(text)
            return range.getClientRects().length === 1
          }),
          inside: q.scrollWidth <= q.clientWidth,
          links: [...q.querySelectorAll('a')].map((a) => box(a).height),
        }
      })
      expect(layout.order).toEqual(['#279', '#280', '#281', '#282', '#283'])
      expect(layout.positions).toEqual(['1', '2', '3', '4', '5'])
      expect(layout.iidsOneLine, 'issue numbers on one line').toBe(true)
      expect(layout.inside, 'no sideways scroll in the queue').toBe(true)
      if (phone) for (const h of layout.links) expect(h, 'link height on a phone').toBeGreaterThanOrEqual(44)
      expect(await sideways(page), 'sideways scroll in px').toEqual({ page: 0, pane: 0 })
      await queue.screenshot({ path: join(testInfo.project.outputDir, '..', 'screenshots', `milestone-queue@${size.width}.png`) })
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
    })
  }

  // The pause cards (PauseCard.vue, PausesCard.vue, R12): on a phone a
  // pause history reason clipped to one line opens in full on a tap, no
  // title holds it, and the Way out's buttons and select are 44 px high.
  // Skip posts its command to the spec task's chat, as before.
  for (const size of [widths[0]!, widths[2]!]) {
    const phone = size.width < 760

    test(`pause cards: reason opens on tap, Skip posts@${size.width}`, async ({ page, baseURL }, testInfo) => {
      const route = routes.find((r) => r.name === 'stream-milestone-pilot')!
      const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
      await page.setViewportSize(size)
      await page.clock.setFixedTime(now)
      await watch(page, new URL(baseURL ?? '').origin, problems)
      await mockAPI(page, route, problems)
      const specChat = '/api/v1/projects/team-a/shop/tasks/shop-m14-spec/chat'
      const posts: unknown[] = []
      // Registered after mockAPI, so it answers the spec chat first: the
      // POST, then the card's look for the reply.
      await page.route(
        (url) => url.pathname === specChat,
        async (r) => {
          if (r.request().method() === 'POST') {
            posts.push(r.request().postDataJSON())
            return r.fulfill({ status: 200, json: { id: 992 } })
          }
          return r.fulfill({ status: 200, json: { messages: [] } })
        },
      )
      await page.goto(route.path)
      await route.ready(page)
      const card = page.locator('main [data-pause-card]')
      const history = page.locator('main [data-pauses]')
      await expect(card).toBeVisible()
      await expect(history).toBeVisible()
      await expect(card.locator('.md strong')).toHaveText('partial refunds')
      // RelTime keeps its absolute time; the companion chip (CompanionChip,
      // shared with the companions list) is not part of these cards.
      const titles = await page.locator('main').evaluate((main) =>
        [...main.querySelectorAll('[data-pause-card] [title], [data-pauses] [title]')]
          .filter((e) => e.tagName !== 'TIME' && !e.closest('.companion-link') && !e.textContent!.includes('companion:'))
          .map((e) => e.outerHTML.slice(0, 80)),
      )
      expect(titles, 'title attributes').toEqual([])
      expect(await sideways(page), 'sideways scroll in px').toEqual({ page: 0, pane: 0 })

      const skip = card.locator('[data-way-out]').getByRole('button', { name: 'Skip', exact: true })
      if (phone) {
        expect((await skip.boundingBox())!.height, 'Skip height').toBeGreaterThanOrEqual(44)
        // The ciRed pause's reason is clipped at one line: a tap opens it.
        const reason = history.locator('[role=button][aria-expanded]', { hasText: 'test:integration-payments' })
        await expect(reason).toHaveAttribute('aria-expanded', 'false')
        await reason.click()
        await expect(reason).toHaveAttribute('aria-expanded', 'true')
        expect(await reason.evaluate((e) => e.scrollWidth <= e.clientWidth && e.scrollHeight <= e.clientHeight), 'reason shown in full').toBe(true)
        await expect(reason.locator('code')).toHaveText('test:integration-payments')
        await expect(page).toHaveURL(/\/milestones\/14(\?tab=pilot)?$/)
      }
      await shoot(page, testInfo, `pause-cards@${size.width}`)

      await skip.click()
      await expect(card.getByRole('status')).toContainText('Skip #281: sent…')
      expect(posts).toEqual([{ body: JSON.stringify({ cmd: 'skip', issue: 281 }), clientKey: expect.any(String), kind: 'command' }])
      expect(await sideways(page), 'sideways scroll in px').toEqual({ page: 0, pane: 0 })
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
    })
  }
})

// The milestone's Retrospective tab (MilestoneRetro.vue, R12), utilities
// only: no sideways scroll, cost drivers stacked on a phone, the leftovers'
// checkboxes, links and folds 44 px high there, the editor's inputs at
// 16 px. Creating a follow-up milestone and dismissing the leftovers post
// as before; the dismiss confirmation is a dialog that back closes.
test.describe('milestone retrospective', () => {
  const base = '/api/v1/projects/team-a/shop/milestones/14'

  async function sideways(page: Page) {
    return page.evaluate(() => {
      const doc = document.scrollingElement ?? document.documentElement
      const main = document.querySelector('main')!
      return { page: doc.scrollWidth - doc.clientWidth, pane: main.scrollWidth - main.clientWidth }
    })
  }

  for (const size of widths) {
    const phone = size.width < 760
    const shot = (outputDir: string, name: string) => join(outputDir, '..', 'screenshots', `${name}@${size.width}.png`)

    test(`follow-ups: create and dismiss@${size.width}`, async ({ page, baseURL }, testInfo) => {
      const route = routes.find((r) => r.name === 'stream-milestone-retrospective')!
      const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
      await page.setViewportSize(size)
      await page.clock.setFixedTime(now)
      await watch(page, new URL(baseURL ?? '').origin, problems)
      await mockAPI(page, route, problems)
      const posts: { path: string; body: unknown }[] = []
      // Registered after mockAPI, so they answer first. Without draft the
      // editor starts from the leftovers' titles and bodies, no pilot.
      await page.route(
        (url) => url.pathname === base,
        (r) =>
          r.fulfill({
            status: 200,
            json: { ...paymentsMilestone, permissions: { ...paymentsMilestone.permissions, draft: false } },
          }),
      )
      await page.route(
        (url) => url.pathname === `${base}/followup` || url.pathname === `${base}/followups/dismiss`,
        async (r) => {
          const path = new URL(r.request().url()).pathname.slice(base.length)
          posts.push({ path: `${r.request().method()} ${path}`, body: r.request().postDataJSON() })
          if (path === '/followup') {
            return r.fulfill({ status: 201, json: { iid: 15, webURL: 'https://gitlab.example.com/groups/team-a/-/milestones/15' } })
          }
          return r.fulfill({ status: 202, json: { id: 7, author: 'jo', body: 'create none', createdAt: new Date(now).toISOString() } })
        },
      )
      await page.goto(route.path)
      await route.ready(page)
      const main = page.locator('main')
      await expect(main.locator('[data-drivers] tbody tr')).toHaveCount(3)
      expect(await sideways(page), 'sideways scroll in px').toEqual({ page: 0, pane: 0 })

      const layout = await main.evaluate((m) => {
        const h = (e: Element | null) => e?.getBoundingClientRect().height ?? 0
        return {
          heads: getComputedStyle(m.querySelector('[data-drivers] thead')!).display !== 'none',
          tick: h(m.querySelector('[data-followup] label')),
          stateLink: h(m.querySelector('[data-followup] a')),
          fold: h(m.querySelector('[data-followup] summary')),
          facts: [...m.querySelectorAll('[data-retro-facts] a')].map(h),
          buttons: [...m.querySelectorAll('[data-dismiss] button, [data-draft] button')].map(h),
        }
      })
      expect(layout.heads, 'cost driver heads shown').toBe(!phone)
      expect(layout.buttons).toHaveLength(2)
      if (phone) {
        for (const v of [layout.tick, layout.stateLink, layout.fold, ...layout.facts, ...layout.buttons]) {
          expect(v, 'hit area on a phone').toBeGreaterThanOrEqual(44)
        }
      }
      const tab = shot(testInfo.project.outputDir, 'milestone-retro')
      await page.screenshot({ path: tab, fullPage: true })
      await testInfo.attach('screenshot', { path: tab, contentType: 'image/png' })

      // Tick leftover 1, draft from it, edit, create.
      await main.getByRole('checkbox', { name: 'Take leftover 1 into a follow-up milestone' }).check()
      await main.getByRole('button', { name: 'Draft follow-up milestone' }).click()
      const editor = main.locator('[data-editor]')
      const title = editor.getByLabel('Title')
      await expect(title).toHaveValue('Follow-ups of Payment providers: refunds, partial captures and the provider switch')
      const fontSize = (l: ReturnType<Page['locator']>) => l.evaluate((e) => getComputedStyle(e).fontSize)
      expect(await fontSize(title), 'title font size').toBe(phone ? '16px' : '14px')
      expect(await fontSize(editor.getByLabel('Description')), 'description font size').toBe(phone ? '16px' : '13px')
      await expect(editor.locator('.md strong')).toHaveText('publish:image')
      expect(await sideways(page), 'sideways scroll in px').toEqual({ page: 0, pane: 0 })
      await editor.screenshot({ path: shot(testInfo.project.outputDir, 'milestone-retro-editor') })
      await title.fill('Registry retries')
      await editor.getByRole('button', { name: 'Create milestone and start autopilot' }).click()
      await expect(main.getByRole('status').filter({ hasText: 'Created the milestone %15' })).toBeVisible()
      expect(posts).toEqual([
        {
          path: 'POST /followup',
          body: {
            followUps: [1],
            title: 'Registry retries',
            description: expect.stringContaining('## Retry the registry push in the release job'),
          },
        },
      ])

      // Dismiss: the dialog closes by back without posting, then confirms.
      const dialog = page.getByRole('dialog')
      await main.getByRole('button', { name: 'Dismiss remaining' }).click()
      await expect(dialog).toBeVisible()
      await page.goBack()
      await expect(dialog).toBeHidden()
      await expect(page).toHaveURL(/\/milestones\/14\?tab=retrospective$/)
      expect(posts).toHaveLength(1)
      await main.getByRole('button', { name: 'Dismiss remaining' }).click()
      await expect(dialog).toContainText('create none')
      const confirm = dialog.getByRole('button', { name: 'Dismiss them' })
      // Polled: the dialog scales in, a box measured during it is smaller.
      if (phone) await expect.poll(async () => (await confirm.boundingBox())!.height, { message: 'Dismiss them height' }).toBeGreaterThanOrEqual(44)
      expect(await sideways(page), 'sideways scroll in px').toEqual({ page: 0, pane: 0 })
      await page.screenshot({ path: shot(testInfo.project.outputDir, 'milestone-retro-dismiss') })
      await confirm.click()
      await expect(dialog).toBeHidden()
      await expect(main.getByRole('status').filter({ hasText: 'Dismissing…' })).toBeVisible()
      expect(posts[1]).toEqual({ path: 'POST /followups/dismiss', body: {} })
      await expect(page).toHaveURL(/\/milestones\/14\?tab=retrospective$/)
      expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
    })
  }
})

// The Board tab (Board.vue, TaskCard.vue, ProjectBoards.vue, R12),
// utilities only: four columns side by side on desktop, two on a tablet,
// stacked on a phone. Every column keeps its heading and count, the cards
// stay inside their column with wrapping titles and are at least 44 px
// high, and on a phone the milestone headings' links are 44 px high.
test.describe('board', () => {
  for (const size of widths) {
    const phone = size.width < 760
    const perRow = phone ? 1 : size.width < 1180 ? 2 : 4
    for (const name of ['stream-project-board', 'stream-milestone-board']) {
      test(`${name} columns and cards@${size.width}`, async ({ page, baseURL }, testInfo) => {
        const route = routes.find((r) => r.name === name)!
        const problems: Problems = { unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] }
        await page.setViewportSize(size)
        await page.clock.setFixedTime(now)
        await watch(page, new URL(baseURL ?? '').origin, problems)
        await mockAPI(page, route, problems)
        await page.goto(route.path)
        await route.ready(page)

        const boards = await page.locator('main [data-board]').evaluateAll((els) =>
          els.map((b) => {
            const box = (e: Element) => e.getBoundingClientRect()
            const columns = [...b.querySelectorAll('[data-column]')]
            return {
              columns: columns.map((c) => c.getAttribute('data-column')),
              counts: columns.map((c) => Number(c.querySelector('[data-count]')!.textContent!.trim())),
              cardsPerColumn: columns.map((c) => c.querySelectorAll('[data-task-card]').length),
              headingsShown: columns.every((c) => box(c.querySelector('header')!).height > 0),
              lefts: [...new Set(columns.map((c) => Math.round(box(c).left)))].length,
              cards: [...b.querySelectorAll('[data-task-card]')].map((card) => {
                const col = box(card.closest('[data-column]')!)
                const r = box(card)
                const title = card.querySelector('[data-title]')!
                return {
                  height: r.height,
                  inside: r.left >= col.left - 0.5 && r.right <= col.right + 0.5,
                  titleInside: title.scrollWidth <= title.clientWidth,
                }
              }),
              inside: b.scrollWidth <= b.clientWidth,
            }
          }),
        )
        expect(boards.length).toBeGreaterThan(0)
        for (const b of boards) {
          expect(b.columns).toEqual(['progress', 'attention', 'ci', 'review'])
          expect(b.counts).toEqual(b.cardsPerColumn)
          expect(b.headingsShown, 'every column shows its heading and count').toBe(true)
          expect(b.lefts, 'columns side by side').toBe(perRow)
          expect(b.inside, 'no sideways scroll in the board').toBe(true)
          for (const c of b.cards) {
            expect(c.height, 'card height').toBeGreaterThanOrEqual(44)
            expect(c.inside, 'card inside its column').toBe(true)
            expect(c.titleInside, 'title wraps').toBe(true)
          }
        }
        if (phone) {
          const links = await page
            .locator('main [data-board-milestone] > .row a')
            .evaluateAll((as) => as.map((a) => a.getBoundingClientRect().height))
          for (const h of links) expect(h, 'heading link height on a phone').toBeGreaterThanOrEqual(44)
        }
        const overflow = await page.evaluate(() => {
          const doc = document.scrollingElement ?? document.documentElement
          const main = document.querySelector('main')!
          return { page: doc.scrollWidth - doc.clientWidth, pane: main.scrollWidth - main.clientWidth }
        })
        expect(overflow, 'sideways scroll in px').toEqual({ page: 0, pane: 0 })

        const shot = join(testInfo.project.outputDir, '..', 'screenshots', `board-${name.split('-')[1]}@${size.width}.png`)
        await page.screenshot({ path: shot, fullPage: true })
        await testInfo.attach('screenshot', { path: shot, contentType: 'image/png' })
        expect(problems).toEqual({ unknownAPI: [], otherOrigin: [], csp: [], pageErrors: [] })
      })
    }
  }
})
