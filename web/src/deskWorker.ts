import type { Router } from 'vue-router'
import { inboxRoute, milestoneRoute } from './inboxRoute.ts'
import { localTimesText } from './localTime.ts'
import { parseOpen, type NotifyOpen } from './notifyOpen.ts'
import { eventKey, eventPoller, eventText } from './eventNotify.ts'
import type { NotifyCategory } from './notify.ts'
import type { StreamPage } from './types'

// The page's side of the service worker (sw.ts, docs/contributing/web-ui.md
// "Service worker"). App.vue loads it after the shell, which keeps the
// initial bundle in its budget. Notifications go through the worker, so
// Chrome on Android shows them; a click on one focuses this window and the
// worker posts `{ open }` with a reply port, or it opens a new one on
// `/?open=…`.

// Web Push's start (push.ts) comes along: a second lazy import in App.vue
// would cost the initial bundle its preload list.
export { startPush } from './push.ts'

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
    if (open) openNotified(open, e.ports[0])
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

// Event notifications (eventNotify.ts): milestone started, done and
// releases. App.vue calls pollEvents while one of their categories is
// wanted, on task change events and every minute, and resetEvents when they
// are no longer wanted, so turning them back on seeds again. App.vue stores
// the page's history flag (notify.history), which stops the polls without a
// database. notify.ts comes in as arguments: importing it here would split
// Vue's reactivity out of the initial bundle into a chunk of its own.
const events = eventPoller()
let polling = false

type EventSource = {
  fetchPage: (q: URLSearchParams) => Promise<StreamPage>
  wants: (c: NotifyCategory) => boolean
  history: (on: boolean) => void
}

export async function pollEvents({ fetchPage, wants, history }: EventSource) {
  if (polling) return
  polling = true
  try {
    const page = await fetchPage(events.query())
    history(page.history)
    for (const e of events.step(page, wants)) {
      const { title, body } = eventText(e)
      showDeskNotification(title, body, eventKey(e), { event: e })
    }
  } catch {
    // The next change event or tick tries again.
  } finally {
    polling = false
  }
}

export function resetEvents() {
  events.reset()
}

// openNotified routes to a notification's target. With the worker's reply
// port it answers `{ opened: true }` once routed, also when the window was
// already there (a duplicated navigation); without an answer the worker
// navigates the window after clickReplyMs (notifyOpen.ts).
function openNotified(open: NotifyOpen, port?: MessagePort) {
  window.focus()
  void router?.push(openRoute(open))
    .then((failure) => {
      if (!port) return
      // A duplicated navigation (to is from) also leaves the window on it.
      if (!failure || failure.to.fullPath === failure.from.fullPath) port.postMessage({ opened: true })
      port.close()
    })
    .catch(() => undefined)
}

// openRoute is where a notification leads: an Inbox item's card, an
// event's milestone.
function openRoute(open: NotifyOpen) {
  return 'item' in open ? inboxRoute(open.item) : milestoneRoute(open.event.namespace, open.event.project, open.event.milestoneIID)
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
  await r.replace(open ? openRoute(open) : { query }).catch(() => undefined)
}
