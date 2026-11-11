// Browser checks of the Desk (docs/ui.md, "Checks"): every route at a
// phone, a tablet and a desktop width, served by e2e/serve.mjs with the
// server's CSP and the API answered from e2e/fixtures.
import { join } from 'node:path'
import { expect, test, type Page } from '@playwright/test'
import { answer, now } from './fixtures'
import { inbox } from './fixtures/rail'
import { features } from './fixtures/session'
import { refundsResponse } from './fixtures/task'
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
const hitRoutes = ['overview', 'inbox', 'task', 'task-chat', 'task-calls', 'task-details']

// "<route> <element as reported>" → why it may stay under 44 px. Empty: no
// control needs an exception.
const hitAllowlist: Record<string, string> = {}

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
