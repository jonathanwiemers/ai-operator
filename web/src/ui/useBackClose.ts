// useBackClose lets the browser's back button close a sheet (docs/contributing/web-ui.md,
// "Sheets"). Opening the sheet pushes a history entry for the same URL;
// back pops it and closes the sheet, and the route stays as it was.
// Closing by scrim, button or Escape pops the entry again, and a navigation
// while the sheet is open (a link inside it) first pops the entry, then
// goes on, so no stray entry is left behind.
//
// vue-router never sees the entry: it is pushed with history.pushState, and
// the popstate that leaves it (or returns to it by forward) is stopped before
// vue-router's listener runs, so the router does not navigate again and the
// view does not reload its data. Listeners on window run in the order they
// were added, the capture flag does not change that in Chromium: so
// listenForSheets must run before createWebHistory adds vue-router's.
//
// keepOnQuery (a sheet of filters, Metrics): a router.replace that changes
// only the query, while the sheet is open, goes through and keeps the sheet
// open. vue-router replaces the sheet's entry; the entry is marked again,
// and back carries the new address down to the entry below, so closing the
// sheet keeps the filters.
import { onBeforeUnmount, watch, type Ref } from 'vue'
import { useRouter } from 'vue-router'

// The key of the marker in history.state; its value is the sheet's id.
const key = '__sheet'
let lastID = 0

// The open sheets' popstate handlers; one returns true to keep the event
// from vue-router.
const handlers = new Set<(e: PopStateEvent) => boolean>()
let listening = false

// listenForSheets adds the one popstate listener of all sheets. main.ts
// calls it before createWebHistory().
export function listenForSheets(): void {
  if (listening) return
  listening = true
  window.addEventListener('popstate', (e) => {
    let stop = false
    for (const h of [...handlers]) stop = h(e) || stop
    if (stop) e.stopImmediatePropagation()
  })
}

export function useBackClose(open: Ref<boolean>, opts: { keepOnQuery?: boolean } = {}): void {
  if (!listening) throw new Error('useBackClose: listenForSheets() must run before createWebHistory()')
  const router = useRouter()
  const id = ++lastID
  // onEntry: the current history entry is the one this sheet pushed.
  let onEntry = false
  // leaving: history.back() was called, its popstate has not come yet.
  let leaving = false
  // url: the address of the entry, to tell a pop within the page apart from
  // one to another page (a jump of several entries).
  let url = ''
  // opened: the address when the sheet opened, which the entry below keeps;
  // state: vue-router's state of the entry after a query change.
  let opened = ''
  let state: object | null = null
  // pending: where to go once the entry is gone.
  let pending: string | null = null

  function enter(): void {
    if (onEntry || leaving) return
    url = location.href
    opened = url
    history.pushState({ ...(history.state as object | null), [key]: id }, '', url)
    onEntry = true
  }

  function leave(): void {
    if (!onEntry || leaving) return
    leaving = true
    history.back()
  }

  // onPop returns true when the pop stays within the page, so vue-router
  // must not see it.
  function onPop(e: PopStateEvent): boolean {
    // below: the entry under the sheet's, whose query is the old one.
    const below = opened !== url && location.href === opened
    const same = location.href === url || below
    const mine = location.href === url && (e.state as Record<string, unknown> | null)?.[key] === id
    if (!onEntry && !leaving && !mine) return false
    if (below) {
      history.replaceState(state, '', url)
      opened = url
    }
    // The sheet was opened again before the pop of its closing came.
    const reopen = leaving && open.value && pending === null
    leaving = false
    onEntry = mine
    if (mine || reopen) {
      // Forward to the entry opens the sheet again.
      open.value = true
      enter()
      return same
    }
    // Back closes the sheet.
    open.value = false
    if (pending !== null) {
      const to = pending
      pending = null
      void router.push(to)
    }
    return same
  }

  watch(open, (v) => (v ? enter() : leave()))

  // A navigation while the entry is on top waits for the entry to go: it
  // is cancelled here and started again once the popstate came.
  const stopGuard = router.beforeEach((to, from) => {
    if (!onEntry && !leaving) return true
    if (opts.keepOnQuery && onEntry && !leaving && to.path === from.path) return true
    pending = to.fullPath
    open.value = false
    leave()
    return false
  })

  // After a query change on the entry, vue-router's state replaced the
  // marker: mark the entry again.
  const stopAfter = router.afterEach((to, from, failure) => {
    if (!opts.keepOnQuery || !onEntry || leaving || failure || to.path !== from.path) return
    state = { ...(history.state as object | null) }
    history.replaceState({ ...state, [key]: id }, '', location.href)
    url = location.href
  })

  handlers.add(onPop)
  onBeforeUnmount(() => {
    stopGuard()
    stopAfter()
    handlers.delete(onPop)
  })
}
