// The Desk's service worker, served as /sw.js (vite.sw.config.ts builds it
// into one file without imports). It exists so notifications show where the
// browser allows them only through a service worker (Chrome on Android), so a
// click on one opens its item in the open Desk window, and so it shows the
// Web Push notifications the server sends while the Desk is closed and
// subscribes again when the browser drops the subscription. It has no fetch
// handler and caches nothing (docs/contributing/web-ui.md, "Service worker").
import { clickTarget, openURL, parseOpen } from './notifyOpen'
import { pushNotification } from './pushNotify'
import { keyBytes, subscriptionBody } from './pushSubscribe'

declare const self: ServiceWorkerGlobalScope

// With nothing cached, a new version can take over at once, and the open
// tab gets the click messages right away.
self.addEventListener('install', () => void self.skipWaiting())
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()))

// Every push with userVisibleOnly must show a notification, or the browser
// may drop the subscription: an unreadable payload shows a generic one.
self.addEventListener('push', (e) => {
  let payload: unknown
  try {
    payload = e.data?.json()
  } catch {
    payload = undefined
  }
  const n = pushNotification(payload) ?? {
    title: 'Desk',
    options: { body: 'Something new in the Desk', tag: 'desk', icon: '/logo.svg', data: {} },
  }
  e.waitUntil(self.registration.showNotification(n.title, n.options).catch(() => undefined))
})

self.addEventListener('pushsubscriptionchange', (e) => e.waitUntil(resubscribe()))

// resubscribe makes a new subscription and sends it without categories (the
// server keeps the stored ones); errors are dropped, the next Desk start
// refreshes the subscription anyway.
async function resubscribe() {
  try {
    const info = (await (await fetch('/api/v1/push', { credentials: 'same-origin' })).json()) as { publicKey?: string }
    if (!info.publicKey) return
    const sub = await self.registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(info.publicKey) })
    await fetch('/api/v1/push/subscription', {
      method: 'PUT',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(subscriptionBody(sub.toJSON(), undefined)),
    })
  } catch {
    // See above.
  }
}

self.addEventListener('notificationclick', (e) => {
  e.notification.close()
  e.waitUntil(onClick(e.notification.data))
})

// onClick focuses a Desk window and posts it `{ open }`; the app routes to
// the item. Without a window, a new one opens on `/?open=…`.
async function onClick(data: unknown) {
  const open = parseOpen(data !== null && typeof data === 'object' ? (data as { open?: unknown }).open : undefined)
  const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
  const target = clickTarget(windows, self.location.origin)
  if ('focus' in target) {
    const w = windows[target.focus]!
    await w.focus().catch(() => undefined)
    if (open) w.postMessage({ open })
    return
  }
  await self.clients.openWindow(open ? openURL(open) : '/')
}
