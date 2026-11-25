import type { InboxItem } from './types'

// What a click on a Desk notification opens, shared by the app (App.vue)
// and the service worker (sw.ts): the notification's `data.open`, the
// worker's `{ open }` message to an open Desk window, and the `?open=` query
// of a window the worker opens. Keep it free of DOM and Vue: the worker is
// built from it into one file (vite.sw.config.ts). The route comes from
// inboxRoute() in the app; the worker has no route logic.
export type NotifyOpen = { item: InboxItem }

// openURL is the Desk URL a new window opens on.
export function openURL(open: NotifyOpen): string {
  return `/?open=${encodeURIComponent(JSON.stringify(open))}`
}

// parseOpen reads an open target from the `?open=` query (JSON text) or a
// message's `open` (an object). Anything else, or an item without its
// namespace, project or kind, is null.
export function parseOpen(v: unknown): NotifyOpen | null {
  let o: unknown = v
  if (typeof v === 'string') {
    try {
      o = JSON.parse(v)
    } catch {
      return null
    }
  }
  const item: unknown = o !== null && typeof o === 'object' ? (o as { item?: unknown }).item : undefined
  if (item === null || typeof item !== 'object') return null
  const { namespace, project, kind } = item as Record<string, unknown>
  if (typeof namespace !== 'string' || typeof project !== 'string' || typeof kind !== 'string') return null
  return { item: item as InboxItem }
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
