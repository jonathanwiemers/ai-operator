// The Desk's service worker, served as /sw.js (vite.sw.config.ts builds it
// into one file without imports). It exists so notifications show where the
// browser allows them only through a service worker (Chrome on Android), and
// so a click on one opens its item in the open Desk window. It has no fetch
// handler and caches nothing (docs/contributing/web-ui.md, "Service worker").
import { clickTarget, openURL, parseOpen } from './notifyOpen'

declare const self: ServiceWorkerGlobalScope

// With nothing cached, a new version can take over at once, and the open
// tab gets the click messages right away.
self.addEventListener('install', () => void self.skipWaiting())
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()))

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
