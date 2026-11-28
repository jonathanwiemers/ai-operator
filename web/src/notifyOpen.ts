import type { InboxItem, StreamEvent } from './types'

// What a click on a Desk notification opens, shared by the app (App.vue)
// and the service worker (sw.ts): the notification's `data.open`, the
// worker's `{ open }` message to an open Desk window, and the `?open=` query
// of a window the worker opens. Keep it free of DOM and Vue: the worker is
// built from it into one file (vite.sw.config.ts). The route comes from
// inboxRoute() in the app, for an event its card (eventRoute());
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

// clickReplyMs is how long a click waits for the picked window to answer
// that it has routed, before the worker navigates it.
export const clickReplyMs = 1000

// A window the click can use: WindowClient's focus and navigate.
export type ClickWindow = WindowInfo & {
  focus(): Promise<unknown>
  navigate(url: string): Promise<unknown>
}

export type ClickDeps<W> = {
  // post sends `{ open }` to w with a reply port; it resolves true when the
  // page answers `{ opened: true }`, and need not resolve otherwise.
  post: (w: W, message: { open: NotifyOpen }) => Promise<boolean>
  openWindow: (url: string) => Promise<unknown>
  ms?: number
}

// clickOpen runs a notification click (docs/contributing/web-ui.md, "Service
// worker"): without a Desk window a new one opens; without `open` the window
// only comes forward. Else the window comes forward and gets `{ open }`. A
// window that does not answer within ms (frozen, discarded, or its listener
// not loaded yet) is navigated to openURL(open), and where the browser
// refuses that (rejects or null, as for an uncontrolled window), a new
// window opens there.
export async function clickOpen<W extends ClickWindow>(
  windows: readonly W[],
  origin: string,
  open: NotifyOpen | null,
  { post, openWindow, ms = clickReplyMs }: ClickDeps<W>,
): Promise<void> {
  const target = clickTarget(windows, origin)
  if (!('focus' in target)) {
    await openWindow(open ? openURL(open) : '/')
    return
  }
  const w = windows[target.focus]!
  await w.focus().catch(() => undefined)
  if (!open) return
  if (await answered(post(w, { open }), ms)) return
  const url = openURL(open)
  const navigated = await w.navigate(url).then(
    (c) => c !== null && c !== undefined,
    () => false,
  )
  if (!navigated) await openWindow(url)
}

// answered is true when reply resolves true within ms; a reply that fails
// counts as none.
function answered(reply: Promise<boolean>, ms: number): Promise<boolean> {
  let timer: ReturnType<typeof setTimeout> | undefined
  const timeout = new Promise<boolean>((resolve) => (timer = setTimeout(() => resolve(false), ms)))
  return Promise.race([reply.then((ok) => ok === true, () => false), timeout]).finally(() => clearTimeout(timer))
}

function sameOrigin(url: string, origin: string): boolean {
  try {
    return new URL(url).origin === origin
  } catch {
    return false
  }
}
