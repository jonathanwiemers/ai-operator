// Focus and the tab title after a route change (Decision 4 of
// docs/specs/desk-accessible.spec.md); App.vue calls it once.
//
// - A navigation to a new path moves focus to the new page's h1 once it is
//   there: pages render their header after their data loads, so it waits up
//   to three seconds for `main h1`. A new query or hash on the same path (a
//   tab, a filter) and the first load keep focus where it is (routeFocus.ts).
// - The tab title is "<h1 text> · Desk" (format.ts deskTitle), read again,
//   at most once per frame, whenever the page changes, so a loaded or renamed
//   title shows up too.
// - onRouteDone tells listeners (the announcer) when a navigation has
//   settled: the page's title and whether focus moved.
import { nextTick, onBeforeUnmount, onMounted, watch, type WatchSource } from 'vue'
import type { Router } from 'vue-router'
import { deskTitle } from '../format'
import { movesFocus } from '../routeFocus'
import { textEntry } from '../textEntry'

export interface RouteDone {
  // title is the page's h1 text, '' on a page without one.
  title: string
  // focused says whether focus moved to the h1.
  focused: boolean
}

type Listener = (done: RouteDone) => void
const listeners = new Set<Listener>()

// onRouteDone calls fn after each in-app navigation, once its page has its
// h1 or after the wait for it ends; the result removes fn again.
export function onRouteDone(fn: Listener): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

const headingWaitMs = 3000

function heading(): HTMLElement | null {
  return document.querySelector<HTMLElement>('main h1')
}

// free says whether focus may move to the h1: not when the user has already
// moved it into the new page or into a text field while it loaded.
function free(was: Element | null): boolean {
  const active = document.activeElement
  if (!active || active === document.body || active === was) return true
  if (textEntry(active)) return false
  return !document.querySelector('main')?.contains(active)
}

export function useRouteFocus(router: Router, inboxCount: WatchSource<number>): void {
  let count = 0
  let frame = 0
  let timer: number | undefined
  // The navigation waiting for its h1.
  let pending: { focus: boolean; was: Element | null } | null = null
  // The h1 focus went to, while a page that swaps its header once loaded
  // may still take it away: focus then follows to the new h1. A sheet that
  // closes after the navigation (the phone's rail) gives focus back to its
  // opener: until the user presses a key or the pointer, focus goes back to
  // the h1.
  let held: { h: HTMLElement; until: number } | null = null

  function focusHeading(h: HTMLElement): boolean {
    if (!h.hasAttribute('tabindex')) h.setAttribute('tabindex', '-1')
    h.focus({ preventScroll: true })
    return document.activeElement === h
  }

  function settle(h: HTMLElement | null) {
    const p = pending
    pending = null
    window.clearTimeout(timer)
    if (!p) return
    const focused = p.focus && !!h && free(p.was) && focusHeading(h)
    held = focused && h ? { h, until: Date.now() + headingWaitMs } : null
    const title = h?.textContent?.replace(/\s+/g, ' ').trim() ?? ''
    for (const fn of listeners) fn({ title, focused })
  }

  function update() {
    frame = 0
    const h = heading()
    const title = deskTitle(count, h?.textContent)
    if (document.title !== title) document.title = title
    if (pending && h) settle(h)
    else if (held && h && h !== held.h && !held.h.isConnected && Date.now() < held.until) {
      const lost = !document.activeElement || document.activeElement === document.body
      held = lost && focusHeading(h) ? { h, until: held.until } : null
    }
  }

  function release() {
    held = null
  }

  function keep(e: FocusEvent) {
    const h = held?.h
    const to = e.target
    if (!h || !h.isConnected || to === h || !(to instanceof Element) || Date.now() >= held!.until) return
    if (to.closest('[role=dialog]') || document.querySelector('main')?.contains(to)) return
    window.setTimeout(() => {
      if (held?.h === h && document.activeElement === to) focusHeading(h)
    })
  }

  function schedule() {
    if (!frame) frame = window.requestAnimationFrame(update)
  }

  watch(
    inboxCount,
    (n) => {
      count = n
      // At once, not on the next frame: a hidden tab gets no frames, and its
      // title is where the count shows.
      update()
    },
    { immediate: true },
  )

  const stop = router.afterEach((to, from, failure) => {
    if (failure || from.matched.length === 0) return
    pending = { focus: movesFocus(from, to), was: document.activeElement }
    held = null
    window.clearTimeout(timer)
    timer = window.setTimeout(() => settle(heading()), headingWaitMs)
    // After the new page has rendered, even when nothing in it changed; an
    // h1 that comes later is caught by the observer.
    void nextTick(schedule)
  })

  const observer = new MutationObserver(schedule)
  onMounted(() => {
    observer.observe(document.body, { childList: true, subtree: true, characterData: true })
    document.addEventListener('focusin', keep)
    document.addEventListener('keydown', release, true)
    document.addEventListener('pointerdown', release, true)
  })
  onBeforeUnmount(() => {
    stop()
    document.removeEventListener('focusin', keep)
    document.removeEventListener('keydown', release, true)
    document.removeEventListener('pointerdown', release, true)
    observer.disconnect()
    window.cancelAnimationFrame(frame)
    window.clearTimeout(timer)
  })
}
