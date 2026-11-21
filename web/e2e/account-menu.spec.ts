// The rail's footer (RailNav.vue, accountMenu.ts, #670): the usage meters with
// their reset line, and the account menu (keyboard, auth off, collapsed,
// theme, axe).
// The fixture's clock is 2026-11-10 12:00 UTC.
// Run with npm run e2e (built UI, Chromium).
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import type { Me, ProjectSummary } from '../src/types'
import { answer, now } from './fixtures'
import { projects } from './fixtures/rail'
import { openAccountMenu, themeChoice } from './account'

test.use({ timezoneId: 'UTC' })

// shop's Claude windows reset later today and on Saturday; the cursor
// window of the other project reset an hour ago.
const withUsage: ProjectSummary[] = projects.map((p, i) => ({
  ...p,
  engineUsage:
    i === 0
      ? [
          {
            engine: 'claude',
            windows: [
              { name: 'five_hour', percent: 42, resetsAt: '2026-11-10T14:05:00Z' },
              { name: 'seven_day', percent: 83, resetsAt: '2026-11-14T09:00:00Z' },
            ],
            observedAt: '2026-11-10T11:50:00Z',
          },
        ]
      : [{ engine: 'cursor', windows: [{ name: 'seven_day', percent: 12, resetsAt: '2026-11-10T11:00:00Z' }], observedAt: '2026-11-09T08:00:00Z' }],
}))

interface Opts {
  width?: number
  me?: Partial<Me>
}

async function open(page: Page, baseURL: string | undefined, opts: Opts = {}) {
  const problems: string[] = []
  const origin = new URL(baseURL ?? '').origin
  await page.setViewportSize({ width: opts.width ?? 1280, height: 900 })
  await page.emulateMedia({ colorScheme: 'light' })
  await page.clock.setFixedTime(now)
  page.on('pageerror', (err) => problems.push(err.message))
  await page.route((url) => url.protocol !== 'data:' && url.protocol !== 'blob:' && url.origin !== origin, (r) => r.abort('blockedbyclient'))
  await page.route('**/api/v1/**', async (r) => {
    const url = new URL(r.request().url())
    const p = url.pathname.slice('/api/v1'.length)
    if (p === '/events') return r.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 3600000\n\n' })
    if (r.request().method() === 'POST' && p.endsWith('/warm')) return r.fulfill({ status: 204 })
    let body = answer(p, url.searchParams)
    if (p === '/projects') body = withUsage
    if (p === '/me' && opts.me) body = { ...(body as Me), ...opts.me }
    if (body === undefined) {
      problems.push(`${r.request().method()} ${url.pathname}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    if (typeof body === 'string') return r.fulfill({ status: 200, contentType: 'text/plain; charset=utf-8', body })
    return r.fulfill({ status: 200, json: body })
  })
  await page.goto('/')
  await expect(page.getByText('Checkout without account').first()).toBeVisible()
  return { problems }
}

test('each usage meter says when its window resets, muted once past@1280', async ({ page, baseURL }) => {
  const { problems } = await open(page, baseURL)
  const meters = page.locator('[data-usage-meter]')
  await expect(meters).toHaveCount(3)
  const reset = (i: number) => meters.nth(i).locator('[data-usage-reset]')
  await expect(reset(0)).toHaveText(/^resets (14:05|0?2:05\s?PM)$/)
  await expect(reset(1)).toHaveText(/^resets Sat (09:00|0?9:00\s?AM)$/)
  await expect(reset(2)).toHaveText('reset')
  await expect(reset(0)).toHaveAttribute('title', /2026/)
  const color = (i: number) => reset(i).evaluate((e) => getComputedStyle(e).color)
  expect(await color(2), 'past is muted').not.toBe(await color(0))
  expect(problems).toEqual([])
})

// The account button is in the tab order and opens the menu by keyboard;
// with OIDC the menu shows the login, its roles and lifetime, Appearance,
// the version and Sign out; Escape closes it and focus returns.
test('the account menu opens by keyboard and shows the login and Sign out@1280', async ({ page, baseURL }) => {
  const { problems } = await open(page, baseURL)
  const button = page.locator('[data-account-button]')
  await expect(button).toHaveAttribute('aria-label', /^Account, jo, (Live|Offline)$/)
  await expect(button).toContainText('jo')
  expect(await button.evaluate((e) => (e as HTMLElement).tabIndex)).toBe(0)
  await button.focus()
  await page.keyboard.press('Enter')
  const menu = page.getByRole('menu')
  await expect(menu).toBeVisible()
  await expect(menu).toContainText('jo')
  await expect(menu).toContainText('owner')
  await expect(menu.locator('[data-login-lasts]')).toHaveText(/^Signed in until /)
  await expect(menu.getByRole('menuitemcheckbox')).toHaveText(['Light', 'Dark', 'Auto'])
  await expect(menu.locator('[data-about-version]')).toHaveText('ai-operator 2026.11.3')
  await expect(menu.getByRole('menuitem', { name: 'Sign out' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(menu).toBeHidden()
  await expect(button).toBeFocused()
  expect(problems).toEqual([])
})

test('with auth off the menu says so and has no Sign out@1280', async ({ page, baseURL }) => {
  const { problems } = await open(page, baseURL, { me: { username: 'anonymous', roles: [], authMode: 'none' } })
  await page.locator('[data-account-button]').focus()
  await page.keyboard.press('Space')
  const menu = page.getByRole('menu')
  await expect(menu).toBeVisible()
  await expect(menu).toContainText('No login (auth off)')
  await expect(menu).toContainText('no roles')
  await expect(menu.locator('[data-login-lasts]')).toHaveCount(0)
  await expect(menu.getByRole('menuitem', { name: 'Sign out' })).toHaveCount(0)
  await expect(menu.getByRole('menuitemcheckbox')).toHaveCount(3)
  expect(problems).toEqual([])
})

// Collapsed, the meters are hidden and the button is the avatar with the
// dot; its menu opens to the right of the 64 px sidebar.
test('the account menu opens from the collapsed sidebar@1280', async ({ page, baseURL }) => {
  const { problems } = await open(page, baseURL)
  const rail = page.locator('.rail')
  await rail.getByRole('button', { name: 'Collapse sidebar' }).click()
  await expect(rail).toHaveAttribute('data-collapsed', 'true')
  await expect.poll(async () => (await rail.boundingBox())?.width).toBe(64)
  await expect(page.locator('[data-usage-meter]')).toHaveCount(0)
  const button = rail.getByRole('button', { name: /^Account, jo, / })
  await expect(button).toBeVisible()
  const box = (await button.boundingBox())!
  expect(box.x + box.width, 'inside the collapsed sidebar').toBeLessThanOrEqual(64)
  await button.click()
  const menu = page.getByRole('menu')
  await expect(menu).toBeVisible()
  await expect(menu).toContainText('jo')
  await expect(menu.getByRole('menuitem', { name: 'Sign out' })).toBeVisible()
  await menu.evaluate((e) => Promise.all(e.getAnimations().map((a) => a.finished)))
  expect((await menu.boundingBox())!.x, 'to the right of the sidebar').toBeGreaterThanOrEqual(64)
  expect(problems).toEqual([])
})

test('choosing Dark in the menu sets the dark class at once@1280', async ({ page, baseURL }) => {
  const { problems } = await open(page, baseURL)
  await expect(page.locator('html')).not.toHaveClass(/\bdark\b/)
  await (await themeChoice(page, 'Dark')).click()
  await expect(page.locator('html')).toHaveClass(/\bdark\b/)
  await expect(await themeChoice(page, 'Dark')).toHaveAttribute('aria-checked', 'true')
  expect(await page.evaluate(() => localStorage.getItem('desk.theme'))).toBe('dark')
  expect(problems).toEqual([])
})

// axe (WCAG 2.2 AA) on the open menu and its button, light and dark, on
// the desktop and in the phone's rail sheet.
for (const colorScheme of ['light', 'dark'] as const) {
  for (const width of [1280, 375]) {
    test(`axe is green with the account menu open in ${colorScheme}@${width}`, async ({ page, baseURL }) => {
      const { problems } = await open(page, baseURL, { width })
      await page.emulateMedia({ colorScheme })
      if (width < 768) {
        await page.getByRole('navigation', { name: 'Sections' }).getByRole('button', { name: 'Projects' }).click()
        const sheet = page.getByRole('dialog')
        await sheet.evaluate((e) => Promise.all(e.getAnimations().map((a) => a.finished)))
        await sheet.locator('[data-account-button]').scrollIntoViewIfNeeded()
      }
      await openAccountMenu(page)
      const results = await new AxeBuilder({ page })
        .include('[role=menu]')
        .include('[data-account-button]')
        .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
        .analyze()
      expect(results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)).toEqual([])
      expect(problems).toEqual([])
    })
  }
}
