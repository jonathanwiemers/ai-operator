// The pilot drawer (Stream.vue; R5 of "Desk: one header and one row of tabs
// on every page", Decision 8): Ask pilot opens the milestone's pilot in a
// sheet over any tab, ?pilot=1 keeps it open across reloads, an old
// ?tab=pilot link opens Issues with it, Escape and Back close it and focus
// returns to the button; axe is green with it open.
// Run with npm run e2e (built UI, Chromium).
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import type { MilestoneView } from '../src/types'
import { answer, now } from './fixtures'
import { knownA11y } from './known-a11y'
import { knownRules } from './knownKeys'
import { paymentsMilestone } from './fixtures/stream'

const milestonePath = '/projects/team-a/shop/milestones/14'

async function open(page: Page, baseURL: string | undefined, path: string, opts: { width?: number; milestone?: MilestoneView } = {}) {
  const problems: string[] = []
  const origin = new URL(baseURL ?? '').origin
  await page.setViewportSize({ width: opts.width ?? 1280, height: 900 })
  await page.clock.setFixedTime(now)
  page.on('pageerror', (err) => problems.push(err.message))
  await page.route((url) => url.protocol !== 'data:' && url.protocol !== 'blob:' && url.origin !== origin, (r) => r.abort('blockedbyclient'))
  await page.route('**/api/v1/**', async (r) => {
    const url = new URL(r.request().url())
    const p = url.pathname.slice('/api/v1'.length)
    if (p === '/events') return r.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 3600000\n\n' })
    if (r.request().method() === 'POST' && p.endsWith('/warm')) return r.fulfill({ status: 204 })
    const body = opts.milestone && p === milestonePath ? opts.milestone : answer(p, url.searchParams)
    if (body === undefined) {
      problems.push(`${r.request().method()} ${url.pathname}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    if (typeof body === 'string') return r.fulfill({ status: 200, contentType: 'text/plain; charset=utf-8', body })
    return r.fulfill({ status: 200, json: body })
  })
  await page.goto(path)
  await expect(page.locator('main h1')).toHaveText(paymentsMilestone.title)
  return { problems }
}

const drawer = (page: Page) => page.getByRole('dialog', { name: 'Pilot' })
// The button by its attribute: while the modal drawer is open the page
// under it is aria-hidden, so getByRole does not find it.
const askButton = (page: Page) => page.locator('main [data-ask-pilot]')
const current = (page: Page) => page.locator('main nav[aria-label="View"] a[aria-current="page"]')
const focusInDrawer = (page: Page) => drawer(page).evaluate((d) => d.contains(document.activeElement))
const settled = (page: Page) => drawer(page).evaluate((e) => Promise.all(e.getAnimations().map((a) => a.finished)))

for (const width of [375, 1280]) {
  for (const [tab, label] of [
    ['activity', 'Activity'],
    ['metrics', 'Metrics'],
  ] as const) {
    test(`Ask pilot opens the drawer on ${label}, traps focus, Escape closes it@${width}`, async ({ page, baseURL }) => {
      const { problems } = await open(page, baseURL, `${milestonePath}?tab=${tab}`, { width })
      await expect(current(page)).toHaveText(label)
      const ask = askButton(page)
      await expect(ask).toHaveText('Ask pilot')
      await expect(ask).toHaveAttribute('aria-expanded', 'false')
      await ask.click()
      await expect(page).toHaveURL(new RegExp(`\\?tab=${tab}&pilot=1$`))
      await expect(drawer(page)).toBeVisible()
      await expect(drawer(page).getByRole('heading', { name: 'Message the pilot' })).toBeVisible()
      await expect(drawer(page)).toContainText(paymentsMilestone.title)
      await expect(ask).toHaveAttribute('aria-expanded', 'true')
      await settled(page)
      // Full width on a phone, a side sheet on wider screens.
      const box = (await drawer(page).boundingBox())!
      if (width < 760) expect(box.width).toBeGreaterThan(width - 2)
      else expect(box.width).toBeLessThanOrEqual(560)

      // Focus is trapped: Tab and Shift+Tab stay in the drawer.
      expect(await focusInDrawer(page), 'focus moved into the drawer').toBe(true)
      for (let i = 0; i < 25; i++) {
        await page.keyboard.press('Tab')
        expect(await focusInDrawer(page), `focus after Tab ${i + 1}`).toBe(true)
      }
      for (let i = 0; i < 5; i++) {
        await page.keyboard.press('Shift+Tab')
        expect(await focusInDrawer(page), `focus after Shift+Tab ${i + 1}`).toBe(true)
      }

      await page.keyboard.press('Escape')
      await expect(drawer(page)).toHaveCount(0)
      await expect(page).toHaveURL(new RegExp(`\\?tab=${tab}$`))
      await expect(ask).toBeFocused()
      await expect(ask).toHaveAttribute('aria-expanded', 'false')
      await expect(current(page)).toHaveText(label)
      expect(problems).toEqual([])
    })
  }
}

test('?pilot=1 opens the drawer on load and after a reload; the close button closes it@1280', async ({ page, baseURL }) => {
  const { problems } = await open(page, baseURL, `${milestonePath}?tab=activity&pilot=1`)
  await expect(drawer(page)).toBeVisible()
  await page.reload()
  await expect(drawer(page)).toBeVisible()
  await expect(drawer(page).getByRole('heading', { name: 'Message the pilot' })).toBeVisible()
  await drawer(page).getByRole('button', { name: /close/i }).click()
  await expect(drawer(page)).toHaveCount(0)
  await expect(page).toHaveURL(/\?tab=activity$/)
  await expect(askButton(page)).toBeFocused()
  await expect(current(page)).toHaveText('Activity')
  expect(problems).toEqual([])
})

test('an old ?tab=pilot link opens Issues with the drawer; Back closes it@1280', async ({ page, baseURL }) => {
  const { problems } = await open(page, baseURL, `${milestonePath}?tab=pilot`)
  await expect(page).toHaveURL(/\/milestones\/14\?pilot=1$/)
  await expect(drawer(page)).toBeVisible()
  await page.goBack()
  await expect(drawer(page)).toHaveCount(0)
  await expect(page).toHaveURL(/\/milestones\/14$/)
  await expect(current(page)).toHaveText('Issues')
  expect(problems).toEqual([])
})

test('a stored milestone has a Pilot button with the pilot events only@1280', async ({ page, baseURL }) => {
  const stored: MilestoneView = { ...paymentsMilestone, state: 'done', stored: true, autopilot: undefined, pilot: undefined, prompts: [] }
  const { problems } = await open(page, baseURL, milestonePath, { milestone: stored })
  const button = askButton(page)
  await expect(button).toHaveText('Pilot')
  await button.click()
  await expect(drawer(page).getByRole('heading', { name: 'Pilot events' })).toBeVisible()
  await expect(drawer(page).getByRole('heading', { name: 'Message the pilot' })).toHaveCount(0)
  expect(problems).toEqual([])
})

// axe (WCAG 2.2 AA) on the open drawer, light and dark, phone and desktop.
// The drawer holds what the Pilot tab held, so its known failures keep the
// tab's key in known-a11y.ts ("stream-milestone-pilot@<width>[-dark]"),
// checked as desk.spec.ts does: exactly the listed rules, then test.fail().
for (const colorScheme of ['light', 'dark'] as const) {
  for (const width of [375, 1280]) {
    test(`axe is green with the pilot drawer open in ${colorScheme}@${width}`, async ({ page, baseURL }) => {
      await page.emulateMedia({ colorScheme })
      const { problems } = await open(page, baseURL, `${milestonePath}?pilot=1`, { width })
      await expect(drawer(page).getByRole('heading', { name: 'Message the pilot' })).toBeVisible()
      await settled(page)
      const results = await new AxeBuilder({ page })
        .include('[role=dialog]')
        .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
        .analyze()
      expect(problems).toEqual([])
      const violations = results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)
      const known = knownA11y[`stream-milestone-pilot@${width}${colorScheme === 'dark' ? '-dark' : ''}`]
      if (known !== undefined) {
        const listed = knownRules(known)
        const found = results.violations.map((v) => v.id)
        expect(violations.filter((_, i) => !listed.includes(found[i]!)), 'axe violations e2e/known-a11y.ts does not list').toEqual([])
        expect(listed.filter((r) => !found.includes(r)), 'rules e2e/known-a11y.ts lists that axe no longer finds').toEqual([])
        test.fail(true, `known a11y: ${known}`)
      }
      expect(violations).toEqual([])
    })
  }
}
