// Browser checks of the Desk (docs/ui.md, "Checks"): every route at a
// phone, a tablet and a desktop width, served by e2e/serve.mjs with the
// server's CSP and the API answered from e2e/fixtures.
import { join } from 'node:path'
import { expect, test, type Page } from '@playwright/test'
import { answer, now } from './fixtures'
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
  {
    name: 'issue',
    path: '/projects/team-a/shop/milestones/14/issues/281',
    ready: (page) => expect(page.locator('main').getByText('Summary of the last run')).toBeVisible(),
  },
  {
    name: 'task',
    path: '/projects/team-a/shop/tasks/shop-281-implement',
    ready: (page) => expect(page.locator('main').getByText('Summary of the last run')).toBeVisible(),
  },
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
    ready: (page) => expect(page.getByRole('heading', { name: 'Not found' })).toBeVisible(),
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
