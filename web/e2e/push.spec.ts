// Web Push from the Notifications dialog (R5 of the Desk push milestone,
// #809): push.ts with a stubbed PushManager (headless Chromium has no push
// service) and a mocked /api/v1/push. Turning the switch on subscribes and
// PUTs, off unsubscribes and DELETEs, a category change PUTs again, a
// subscription of a rotated key is made again on start, and the iOS hint
// and push off show in the dialog, with axe green in each state. Not in the
// journey set: push is set up once per device, not a daily path.
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { answer, now } from './fixtures'
import type { PushInfo, PushSubscriptionRequest } from '../src/generated/views'

test.use({ timezoneId: 'UTC' })

// Two P-256 public keys as the server sends them (base64url, 65 bytes): the
// Desk's key and the one before a rotation.
const key = 'BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4'
const oldKey = 'BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw8'
const on: PushInfo = { available: true, publicKey: key }
const off: PushInfo = { available: false, reason: "Push notifications need the Desk's database (database.host)." }
const iphone = 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1'

interface Opts {
  info?: PushInfo
  // The subscription this browser has at start, made with this key.
  stored?: string
  userAgent?: string
}

type Sent = { method: string; body: unknown }

async function open(page: Page, baseURL: string | undefined, opts: Opts = {}) {
  const problems: string[] = []
  const sent: Sent[] = []
  const origin = new URL(baseURL ?? '').origin
  await page.addInitScript(
    ({ stored, userAgent }) => {
      const w = window as unknown as Record<string, unknown>
      Object.defineProperty(Notification, 'permission', { configurable: true, get: () => 'granted' })
      Notification.requestPermission = async () => 'granted'
      if (userAgent) Object.defineProperty(Navigator.prototype, 'userAgent', { configurable: true, get: () => userAgent })
      const log = { subscribes: 0, unsubscribes: 0 }
      w.__push = log
      const bytes = (b64: string) => Uint8Array.from(atob(b64.replace(/-/g, '+').replace(/_/g, '/') + '='), (c) => c.charCodeAt(0))
      let sub: PushSubscription | null = null
      const make = (k: Uint8Array, endpoint: string) =>
        ({
          endpoint,
          options: { applicationServerKey: k.slice().buffer, userVisibleOnly: true },
          toJSON: () => ({ endpoint, expirationTime: null, keys: { p256dh: 'BPk', auth: 'c2Vj' } }),
          unsubscribe: async () => {
            log.unsubscribes++
            sub = null
            return true
          },
        }) as unknown as PushSubscription
      if (stored) sub = make(bytes(stored), 'https://fcm.googleapis.com/fcm/send/old')
      PushManager.prototype.getSubscription = async () => sub
      PushManager.prototype.subscribe = async (o?: PushSubscriptionOptionsInit) => {
        log.subscribes++
        sub = make(new Uint8Array(o!.applicationServerKey as Uint8Array), `https://fcm.googleapis.com/fcm/send/new${log.subscribes}`)
        return sub
      }
    },
    { stored: opts.stored ?? '', userAgent: opts.userAgent ?? '' },
  )
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.emulateMedia({ colorScheme: 'light' })
  await page.clock.setFixedTime(now)
  page.on('pageerror', (err) => problems.push(err.message))
  await page.route((url) => url.protocol !== 'data:' && url.protocol !== 'blob:' && url.origin !== origin, (r) => r.abort('blockedbyclient'))
  await page.route('**/api/v1/**', async (r) => {
    const url = new URL(r.request().url())
    const p = url.pathname.slice('/api/v1'.length)
    const method = r.request().method()
    if (p === '/events') return r.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 3600000\n\n' })
    if (p === '/push') return r.fulfill({ status: 200, json: opts.info ?? on })
    if (p === '/push/subscription') {
      sent.push({ method, body: r.request().postDataJSON() })
      return r.fulfill({ status: 204 })
    }
    const body = answer(p, url.searchParams)
    if (body === undefined) {
      problems.push(`${method} ${url.pathname}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    if (typeof body === 'string') return r.fulfill({ status: 200, contentType: 'text/plain; charset=utf-8', body })
    return r.fulfill({ status: 200, json: body })
  })
  // The Overview's Cockpit, where the Inbox's items are pinned.
  await page.goto('/?tab=cockpit')
  await expect(page.getByText('Support refunds through the payment provider').first()).toBeVisible()
  const log = () => page.evaluate(() => (window as unknown as { __push: { subscribes: number; unsubscribes: number } }).__push)
  return { problems, sent, log }
}

// openNotifications opens the dialog from the account menu and waits for
// its push state.
async function openNotifications(page: Page, state: string) {
  await page.locator('[data-account-button]:visible').click()
  await page.getByRole('menuitem', { name: 'Notifications' }).click()
  const dialog = page.getByRole('dialog', { name: 'Notifications' })
  await expect(dialog.locator(`[data-push-state="${state}"]`)).toBeVisible()
  await dialog.evaluate((e) => Promise.all(e.getAnimations().map((a) => a.finished)))
  return dialog
}

const subscription = (endpoint: string) => ({ endpoint, keys: { p256dh: 'BPk', auth: 'c2Vj' } })
const defaults = ['problem', 'question', 'proposal', 'companion', 'budget', 'milestoneStarted', 'milestoneDone', 'release', 'plan']

test('push on subscribes and PUTs, a category PUTs again, off unsubscribes and DELETEs@1280', async ({ page, baseURL }) => {
  const { problems, sent, log } = await open(page, baseURL)
  const dialog = await openNotifications(page, 'push')
  await expect(dialog).toContainText('What raises a notification on this device, also while the Desk is closed.')
  const main = dialog.getByRole('switch', { name: 'Notifications on this device' })
  await expect(main).toHaveAttribute('aria-checked', 'false')
  expect(sent).toEqual([])

  await main.click()
  await expect(main).toHaveAttribute('aria-checked', 'true')
  await expect.poll(() => sent.length).toBe(1)
  expect(sent[0]).toEqual({ method: 'PUT', body: { ...subscription('https://fcm.googleapis.com/fcm/send/new1'), categories: defaults } })
  expect(await log()).toEqual({ subscribes: 1, unsubscribes: 0 })

  await dialog.getByRole('switch', { name: 'Failed or blocked tasks' }).click()
  await expect.poll(() => sent.length).toBe(2)
  expect((sent[1]!.body as PushSubscriptionRequest).categories).toContain('failed')

  await main.click()
  await expect(main).toHaveAttribute('aria-checked', 'false')
  await expect.poll(() => sent.length).toBe(3)
  expect(sent[2]).toEqual({ method: 'DELETE', body: { endpoint: 'https://fcm.googleapis.com/fcm/send/new1' } })
  expect(await log()).toEqual({ subscribes: 1, unsubscribes: 1 })
  expect(problems).toEqual([])
})

test('a subscribed device PUTs on every start@1280', async ({ page, baseURL }) => {
  const { problems, sent, log } = await open(page, baseURL, { stored: key })
  await expect.poll(() => sent.length).toBe(1)
  expect(sent[0]).toEqual({ method: 'PUT', body: { ...subscription('https://fcm.googleapis.com/fcm/send/old'), categories: defaults } })
  expect(await log()).toEqual({ subscribes: 0, unsubscribes: 0 })
  const dialog = await openNotifications(page, 'push')
  await expect(dialog.getByRole('switch', { name: 'Notifications on this device' })).toHaveAttribute('aria-checked', 'true')
  expect(problems).toEqual([])
})

test('a subscription of a rotated key is made again with the new key on start@1280', async ({ page, baseURL }) => {
  const { problems, sent, log } = await open(page, baseURL, { stored: oldKey })
  await expect.poll(() => sent.length).toBe(2)
  expect(sent).toContainEqual({ method: 'DELETE', body: { endpoint: 'https://fcm.googleapis.com/fcm/send/old' } })
  expect(sent.find((s) => s.method === 'PUT')).toEqual({ method: 'PUT', body: { ...subscription('https://fcm.googleapis.com/fcm/send/new1'), categories: defaults } })
  expect(await log()).toEqual({ subscribes: 1, unsubscribes: 1 })
  expect(problems).toEqual([])
})

test('with push off on the Desk the dialog says why and keeps the in-tab switch@1280', async ({ page, baseURL }) => {
  const { problems, sent } = await open(page, baseURL, { info: off, stored: key })
  const dialog = await openNotifications(page, 'unavailable')
  await expect(dialog.locator('[data-notify-push-reason]')).toHaveText(off.reason!)
  await expect(dialog).toContainText('What raises a notification while a Desk tab is open.')
  const main = dialog.getByRole('switch', { name: 'Notifications in this browser' })
  await main.click()
  await expect(main).toHaveAttribute('aria-checked', 'true')
  expect(sent).toEqual([])
  expect(problems).toEqual([])
})

test('in Safari on iOS the dialog asks to add the Desk to the Home Screen@1280', async ({ page, baseURL }) => {
  const { problems } = await open(page, baseURL, { userAgent: iphone })
  const dialog = await openNotifications(page, 'iosHint')
  await expect(dialog.locator('[data-notify-ios]')).toHaveText('Add the Desk to your Home Screen to get notifications')
  await expect(dialog.getByRole('switch', { name: /^Notifications/ })).toHaveCount(0)
  await expect(dialog.getByRole('switch', { name: 'Problems' })).toBeEnabled()
  expect(problems).toEqual([])
})

for (const [state, opts] of [
  ['push', {}],
  ['iosHint', { userAgent: iphone }],
  ['unavailable', { info: off }],
] as const) {
  for (const colorScheme of ['light', 'dark'] as const) {
    test(`axe is green on the Notifications dialog in the ${state} state in ${colorScheme}@1280`, async ({ page, baseURL }) => {
      const { problems } = await open(page, baseURL, opts)
      await page.emulateMedia({ colorScheme })
      await openNotifications(page, state)
      const results = await new AxeBuilder({ page }).include('[role=dialog]').withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']).analyze()
      expect(results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)).toEqual([])
      expect(problems).toEqual([])
    })
  }
}
