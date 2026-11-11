// useBackClose lets the browser's back button close a sheet (docs/ui.md,
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

export function useBackClose(open: Ref<boolean>): void {
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
  // pending: where to go once the entry is gone.
  let pending: string | null = null

  function enter(): void {
    if (onEntry || leaving) return
    url = location.href
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
    const same = location.href === url
    const mine = same && (e.state as Record<string, unknown> | null)?.[key] === id
    if (!onEntry && !leaving && !mine) return false
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
  const stopGuard = router.beforeEach((to) => {
    if (!onEntry && !leaving) return true
    pending = to.fullPath
    open.value = false
    leave()
    return false
  })

  handlers.add(onPop)
  onBeforeUnmount(() => {
    stopGuard()
    handlers.delete(onPop)
  })
}
