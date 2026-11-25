import type { Router } from 'vue-router'
import { inboxRoute } from './inboxRoute.ts'
import { localTimesText } from './localTime.ts'
import { parseOpen, type NotifyOpen } from './notifyOpen.ts'

// The page's side of the service worker (sw.ts, docs/contributing/web-ui.md
// "Service worker"). App.vue loads it after the shell, which keeps the
// initial bundle in its budget. Notifications go through the worker, so
// Chrome on Android shows them; a click on one focuses this window and the
// worker posts `{ open }`, or it opens a new one on `/?open=…`.

let router: Router | null = null
// The worker's registration once it is active; null without one.
let worker: ServiceWorkerRegistration | null = null

// startWorker registers /sw.js in a secure context, listens for its click
// messages and opens the item of a `?open=` query once.
export function startWorker(r: Router) {
  router = r
  void openFromQuery(r)
  if (!window.isSecureContext || !('serviceWorker' in navigator)) return
  navigator.serviceWorker.addEventListener('message', (e: MessageEvent) => {
    const open = parseOpen((e.data as { open?: unknown } | null)?.open)
    if (open) openNotified(open)
  })
  navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch(() => undefined)
  void navigator.serviceWorker.ready.then((reg) => (worker = reg))
}

// showDeskNotification shows a notification through the worker, else with
// `new Notification`, whose click opens the same way. The UTC times in the
// body show in the browser's zone.
export function showDeskNotification(title: string, text: string, tag: string, open: NotifyOpen) {
  const body = localTimesText(text, Date.now())
  if (worker) {
    worker.showNotification(title, { body, tag, icon: '/logo.svg', data: { open } }).catch(() => undefined)
    return
  }
  try {
    const n = new Notification(title, { body, tag, icon: '/logo.svg' })
    n.onclick = () => {
      openNotified(open)
      n.close()
    }
  } catch {
    // Browsers that show notifications only from a service worker.
  }
}

function openNotified(open: NotifyOpen) {
  window.focus()
  router?.push(inboxRoute(open.item)).catch(() => undefined)
}

// A window the worker opened on `/?open=…` lands on the item, and the query
// goes, so a reload does not open it again.
async function openFromQuery(r: Router) {
  await r.isReady()
  const q = r.currentRoute.value.query
  if (q.open === undefined) return
  const open = parseOpen(q.open)
  const query = { ...q }
  delete query.open
  await r.replace(open ? inboxRoute(open.item) : { query }).catch(() => undefined)
}
