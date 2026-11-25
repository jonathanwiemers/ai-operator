import type { InboxItem, StreamEvent } from './types'

// What a click on a Desk notification opens, shared by the app (App.vue)
// and the service worker (sw.ts): the notification's `data.open`, the
// worker's `{ open }` message to an open Desk window, and the `?open=` query
// of a window the worker opens. Keep it free of DOM and Vue: the worker is
// built from it into one file (vite.sw.config.ts). The route comes from
// inboxRoute() in the app, for an event its milestone (milestoneRoute());
// the worker has no route logic.
export type NotifyOpen = { item: InboxItem } | { event: StreamEvent }

// openURL is the Desk URL a new window opens on.
export function openURL(open: NotifyOpen): string {
  return `/?open=${encodeURIComponent(JSON.stringify(open))}`
}

// parseOpen reads an open target from the `?open=` query (JSON text) or a
// message's `open` (an object). Anything else, or an item or event without
// its namespace, project or kind, is null.
export function parseOpen(v: unknown): NotifyOpen | null {
  let o: unknown = v
  if (typeof v === 'string') {
    try {
      o = JSON.parse(v)
    } catch {
      return null
    }
  }
  if (o === null || typeof o !== 'object') return null
  const { item, event } = o as { item?: unknown; event?: unknown }
  if (located(item)) return { item: item as InboxItem }
  if (located(event)) return { event: event as StreamEvent }
  return null
}

// located says whether x has the namespace, project and kind a route needs.
function located(x: unknown): boolean {
  if (x === null || typeof x !== 'object') return false
  const { namespace, project, kind } = x as Record<string, unknown>
  return typeof namespace === 'string' && typeof project === 'string' && typeof kind === 'string'
}

// A window as the service worker sees it (WindowClient's fields).
type WindowInfo = { url: string; focused: boolean; visibilityState: string }

export type ClickTarget = { focus: number } | { openWindow: true }

// clickTarget picks the window a notification click uses: a focused Desk
// window first, then a visible one, then any, all of the worker's origin.
// Without one a new window opens.
export function clickTarget(windows: readonly WindowInfo[], origin: string): ClickTarget {
  let best = -1
  let bestRank = -1
  windows.forEach((w, i) => {
    if (!sameOrigin(w.url, origin)) return
    const rank = w.focused ? 2 : w.visibilityState === 'visible' ? 1 : 0
    if (rank > bestRank) [best, bestRank] = [i, rank]
  })
  return best < 0 ? { openWindow: true } : { focus: best }
}

function sameOrigin(url: string, origin: string): boolean {
  try {
    return new URL(url).origin === origin
  } catch {
    return false
  }
}
