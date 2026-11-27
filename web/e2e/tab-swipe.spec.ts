// Swiping between tabs (R3, R5 of "Desk: tab rows without stray dots, and
// swiping between tabs on touch screens", #877): on a touch screen a
// sideways swipe on a page's content opens the next or previous tab of its
// RouteTabs row (SwipeTabs), the same router.push as a tap, so Back
// returns. During the drag the neighbour is a placeholder with its label;
// its content is not mounted. A short drag, a swipe past the first or last
// tab, a vertical drag and swipes that start on the tab strip, a sideways
// scroller, the composer or the screen edge keep the tab. A mouse never
// switches. The touches are CDP Input.dispatchTouchEvent (swipe, hold).
// The fixture project team-a/shop and its milestone %14 (fixtures). Run
// with npm run e2e (built UI, Chromium).
import { expect, test, type CDPSession, type Page } from '@playwright/test'
import { answer, now } from './fixtures'

interface Point {
  x: number
  y: number
}

async function open(page: Page, baseURL: string | undefined, path: string) {
  const origin = new URL(baseURL ?? '').origin
  await page.clock.setFixedTime(now)
  await page.route((url) => url.protocol !== 'data:' && url.protocol !== 'blob:' && url.origin !== origin, (r) => r.abort('blockedbyclient'))
  await page.route('**/api/v1/**', async (r) => {
    const url = new URL(r.request().url())
    const p = url.pathname.slice('/api/v1'.length)
    if (p === '/events') return r.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 3600000\n\n' })
    if (p.endsWith('/warm')) return r.fulfill({ status: 204 })
    const body = r.request().method() === 'GET' ? answer(p, url.searchParams) : undefined
    if (body === undefined) return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    if (typeof body === 'string') return r.fulfill({ status: 200, contentType: 'text/plain; charset=utf-8', body })
    return r.fulfill({ status: 200, json: body })
  })
  await page.goto(path)
  await expect(surface(page)).toBeVisible()
}

const wait = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

// hold puts a finger down at from and moves it to to in steps over ms,
// without lifting it: the drag is still running. Lift it with lift.
async function hold(page: Page, from: Point, to: Point, { steps = 10, ms = 200 } = {}): Promise<CDPSession> {
  const cdp = await page.context().newCDPSession(page)
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: from.x, y: from.y }] })
  for (let i = 1; i <= steps; i++) {
    await wait(ms / steps)
    const x = from.x + ((to.x - from.x) * i) / steps
    const y = from.y + ((to.y - from.y) * i) / steps
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y }] })
  }
  return cdp
}

async function lift(cdp: CDPSession) {
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
  await cdp.detach()
}

// swipe is a whole touch: down at from, moved to to, lifted.
async function swipe(page: Page, from: Point, to: Point, opts: { steps?: number; ms?: number } = {}) {
  await lift(await hold(page, from, to, opts))
}

const surface = (page: Page) => page.locator('main [data-swipe-tabs]')
const track = (page: Page) => surface(page).locator(':scope > div').first()
const activeTab = (page: Page) => page.locator('main [data-stream-head] nav a[aria-current="page"]')
// A tab's text starts with its label; a badge (the milestone Cockpit's
// count of open needs) may follow.
const tabLabel = (label: string) => new RegExp(`^${label.replace(/\d+$/, '')}`)
// How far the content scrolled: any scroller in it (the tab's .scroll, the
// Cockpit's pinned column from 1024 px), main or the window (phones).
const scrolled = (page: Page) =>
  page.evaluate(() => {
    const els = [...document.querySelectorAll('main [data-swipe-tabs] *')]
    const own = els.reduce((sum, e) => sum + e.scrollTop, 0)
    return own + (document.querySelector('main')?.scrollTop ?? 0) + (document.scrollingElement?.scrollTop ?? 0)
  })

// A point in the content, at fx of the content's width (the rail and the
// Now & next column sit beside it on wide screens), on the first row
// from about fy of the content down where a swipe may start everywhere
// between 20 % and 80 % of the width: no text field, tab strip or
// sideways scroller under it (SwipeTabs' exclusions). The row does not
// depend on fx, so two points make a level swipe.
async function at(page: Page, fx: number, fy = 0.4): Promise<Point> {
  const y = await surface(page).evaluate((root, fy) => {
    const box = root.getBoundingClientRect()
    const bottom = Math.min(box.bottom, window.innerHeight) - 8
    const free = (x: number, y: number) => {
      const hit = document.elementFromPoint(x, y)
      if (!hit || !root.contains(hit)) return false
      for (let el: Element | null = hit; el && el !== root; el = el.parentElement) {
        if (el.matches('input, textarea, select, [data-route-tabs], [contenteditable]:not([contenteditable="false"])')) return false
        const s = getComputedStyle(el)
        // Any scroll container the browser may pan sideways: a sideways
        // scroller, or a vertical one SwipeTabs did not make pan-y.
        const scroller = ['auto', 'scroll'].includes(s.overflowX) || ['auto', 'scroll'].includes(s.overflowY)
        if (scroller && s.touchAction !== 'pan-y') return false
      }
      return true
    }
    const xs = [0.2, 0.3, 0.45, 0.5, 0.6, 0.7, 0.8].map((f) => box.left + box.width * f)
    const start = box.top + Math.min((bottom - box.top) * fy, 300)
    for (let y = start; y < bottom; y += 8) if (xs.every((x) => free(x, y))) return y
    for (let y = start; y > box.top; y -= 8) if (xs.every((x) => free(x, y))) return y
    return null
  }, fy)
  expect(y, 'a row of the content where a swipe may start').not.toBeNull()
  const box = (await surface(page).boundingBox())!
  return { x: Math.round(box.x + box.width * fx), y: Math.round(y!) }
}

// keeps checks that the tab did not change and the content is at rest.
async function keeps(page: Page, url: RegExp, label: string) {
  await expect(page).toHaveURL(url)
  await expect(activeTab(page)).toHaveText(tabLabel(label))
  await expect(page.locator('[data-swipe-panel]')).toHaveCount(0)
  await expect.poll(() => track(page).evaluate((e) => e.style.transform)).toBe('')
}

const projectPath = '/projects/team-a/shop'
const milestonePath = '/projects/team-a/shop/milestones/14'
const cockpitURL = /\/projects\/team-a\/shop(\?|$)/

for (const [width, height] of [[390, 844], [1024, 768]] as const) {
  test.describe(`swiping tabs at ${width} px`, () => {
    test.use({ hasTouch: true, viewport: { width, height } })

    test('a left swipe opens the next tab, Back returns', async ({ page, baseURL }) => {
      await open(page, baseURL, projectPath)
      await expect(activeTab(page)).toHaveText(tabLabel('Cockpit'))
      await swipe(page, await at(page, 0.8), await at(page, 0.2))
      await expect(page).toHaveURL(/\?tab=milestones$/)
      await expect(activeTab(page)).toHaveText(tabLabel('Milestones'))
      await expect(page.locator('[data-milestones-view-switch]')).toBeVisible()
      await expect.poll(() => track(page).evaluate((e) => e.style.transform)).toBe('')
      await page.goBack()
      await expect(page).toHaveURL(cockpitURL)
      await expect(activeTab(page)).toHaveText(tabLabel('Cockpit'))
    })

    test('mid-drag the neighbour is a placeholder', async ({ page, baseURL }) => {
      await open(page, baseURL, projectPath)
      const cdp = await hold(page, await at(page, 0.8), await at(page, 0.45))
      const placeholder = page.locator('[data-swipe-panel]')
      await expect(placeholder).toHaveText('Milestones')
      await expect(placeholder).toHaveAttribute('aria-hidden', 'true')
      await expect(page.locator('[data-milestones-view-switch]')).toHaveCount(0)
      await lift(cdp)
    })

    test('a right swipe on a milestone Issues opens its Cockpit', async ({ page, baseURL }) => {
      await open(page, baseURL, `${milestonePath}?tab=issues`)
      await expect(activeTab(page)).toHaveText(tabLabel('Issues'))
      await swipe(page, await at(page, 0.2), await at(page, 0.8))
      await expect(page).toHaveURL(/\/milestones\/14$/)
      await expect(activeTab(page)).toHaveText(tabLabel('Cockpit'))
    })

    test('the first and last tab keep the tab', async ({ page, baseURL }) => {
      await open(page, baseURL, projectPath)
      await swipe(page, await at(page, 0.2), await at(page, 0.8))
      await keeps(page, cockpitURL, 'Cockpit')
      const links = page.locator('main [data-stream-head] nav a')
      const last = links.last()
      const label = (await last.textContent())!.trim()
      await page.goto((await last.getAttribute('href'))!)
      await expect(activeTab(page)).toHaveText(tabLabel(label))
      const url = page.url()
      await swipe(page, await at(page, 0.8), await at(page, 0.2))
      await keeps(page, new RegExp(`${url.replace(/[?.]/g, '\\$&')}$`), label)
    })

    test('a 40 px drag springs back', async ({ page, baseURL }) => {
      await open(page, baseURL, projectPath)
      const from = await at(page, 0.6)
      await swipe(page, from, { x: from.x - 40, y: from.y }, { steps: 4, ms: 40 })
      await keeps(page, cockpitURL, 'Cockpit')
    })

    test('a vertical drag scrolls and keeps the tab', async ({ page, baseURL }) => {
      await open(page, baseURL, projectPath)
      const scroller = surface(page).locator('.scroll')
      // Content taller than any fixture's, so the content can scroll: in
      // the tab's scroller, or the window where that grows with it.
      await scroller.evaluate((e) => {
        const spacer = document.createElement('div')
        spacer.style.height = '2000px'
        e.append(spacer)
      })
      const before = await scrolled(page)
      const from = await at(page, 0.5, 0.8)
      await swipe(page, from, { x: from.x - 20, y: from.y - 250 })
      await expect.poll(() => scrolled(page)).toBeGreaterThan(before)
      await keeps(page, cockpitURL, 'Cockpit')
    })

    test('a swipe on the tab strip keeps the tab', async ({ page, baseURL }) => {
      await open(page, baseURL, projectPath)
      const box = (await page.locator('main [data-stream-head] nav').boundingBox())!
      const x = box.x + box.width * 0.6
      const y = box.y + box.height / 2
      await swipe(page, { x, y }, { x: x - 150, y })
      await keeps(page, cockpitURL, 'Cockpit')
    })

    test('a swipe on a sideways scroller keeps the tab', async ({ page, baseURL }) => {
      await open(page, baseURL, `${projectPath}?tab=metrics`)
      await expect(activeTab(page)).toHaveText(tabLabel('Metrics'))
      await expect(surface(page).locator('table').first()).toBeVisible()
      // The table's sideways scroller. Where the table fits (a wide
      // screen), the scroller is narrowed until it overflows.
      const box = await surface(page).evaluate((root) => {
        const table = root.querySelector('table')
        let el = table?.parentElement ?? null
        while (el && el !== root && !['auto', 'scroll'].includes(getComputedStyle(el).overflowX)) el = el.parentElement
        if (!el || el === root) return null
        if (el.scrollWidth <= el.clientWidth) el.style.width = `${Math.floor(el.scrollWidth / 2)}px`
        if (el.scrollWidth <= el.clientWidth) return null
        el.scrollIntoView({ block: 'center' })
        const r = el.getBoundingClientRect()
        return { x: r.x, y: r.y, width: r.width, height: r.height }
      })
      expect(box, 'a sideways scroller that overflows (scrollWidth > clientWidth)').not.toBeNull()
      const y = box!.y + Math.min(box!.height / 2, 40)
      const x = Math.min(box!.x + box!.width - 30, width * 0.8)
      await swipe(page, { x, y }, { x: Math.max(x - 200, 40), y })
      await keeps(page, /\?tab=metrics$/, 'Metrics')
    })

    test('a swipe in the milestone composer keeps the tab', async ({ page, baseURL }) => {
      await open(page, baseURL, milestonePath)
      const field = page.locator('main [data-cockpit-composer] textarea')
      await expect(field).toBeVisible()
      const box = (await field.boundingBox())!
      const y = box.y + box.height / 2
      const x = Math.min(box.x + box.width - 20, width * 0.8)
      await swipe(page, { x, y }, { x: x - 200, y })
      await keeps(page, /\/milestones\/14$/, 'Cockpit')
    })

    test('a swipe from the screen edge keeps the tab', async ({ page, baseURL }) => {
      await open(page, baseURL, projectPath)
      const { y } = await at(page, 0.5)
      await swipe(page, { x: width - 10, y }, { x: width - 260, y })
      await keeps(page, cockpitURL, 'Cockpit')
    })
  })
}

test.describe('a mouse', () => {
  test.use({ hasTouch: false, viewport: { width: 1280, height: 800 } })

  test('a mouse drag never switches the tab', async ({ page, baseURL }) => {
    await open(page, baseURL, projectPath)
    const from = await at(page, 0.7)
    await page.mouse.move(from.x, from.y)
    await page.mouse.down()
    await page.mouse.move(from.x - 500, from.y, { steps: 10 })
    await page.mouse.up()
    await keeps(page, cockpitURL, 'Cockpit')
  })
})
