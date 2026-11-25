// The rail state of the Now & next sidebar (R8 of "Desk: the Cockpit newest
// first by issue"): collapsed to a rail or open as a column, on wide
// screens. It is kept per browser in localStorage under one key, the same
// on every page (D821-P1): one shared ref for the pages of this tab, and
// the storage event for the other tabs of the browser.

import { computed, ref, type WritableComputedRef } from 'vue'

// The part of Storage used here, so tests can pass fakes.
interface RailStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

export const railKey = 'desk.sidebar'

// readCollapsed is the stored state; a missing, unknown or unreadable value
// (privacy modes throw) is the open column.
export function readCollapsed(storage: RailStorage | null): boolean {
  try {
    return storage?.getItem(railKey) === 'rail'
  } catch {
    return false
  }
}

// writeCollapsed stores the state; a failing write keeps it for this page.
export function writeCollapsed(storage: RailStorage | null, collapsed: boolean): void {
  try {
    storage?.setItem(railKey, collapsed ? 'rail' : 'column')
  } catch {
    // The state holds until the page reloads.
  }
}

function browserStorage(): Storage | null {
  try {
    return window.localStorage
  } catch {
    return null
  }
}

let shared: WritableComputedRef<boolean> | undefined

// useSidebarCollapsed is the shared state: read once, written on every
// change, and updated when another tab of the browser changes it.
export function useSidebarCollapsed(): WritableComputedRef<boolean> {
  if (shared) return shared
  const state = ref(readCollapsed(browserStorage()))
  window.addEventListener('storage', (e) => {
    if (e.key === railKey || e.key === null) state.value = readCollapsed(browserStorage())
  })
  shared = computed({
    get: () => state.value,
    set(v) {
      state.value = v
      writeCollapsed(browserStorage(), v)
    },
  })
  return shared
}
