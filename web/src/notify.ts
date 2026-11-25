import { reactive } from 'vue'

// Browser notifications: off until the user turns them on, remembered per
// browser with the categories picked in the Notifications dialog
// (components/NotifySettings.vue). Only while a Desk tab is open (no Web
// Push). Keep imports out of here: this module is in the initial bundle.

export type NotifyPermission = NotificationPermission | 'unsupported'

export type NotifyCategory =
  | 'problem'
  | 'question'
  | 'proposal'
  | 'companion'
  | 'budget'
  | 'failed'
  | 'milestoneStarted'
  | 'milestoneDone'
  | 'release'

type Categories = Record<NotifyCategory, boolean>

// Every category is on by default except failed. The dialog's labels and
// descriptions are in notifyCategories.ts, outside the initial bundle.
const defaults: Categories = {
  problem: true,
  question: true,
  proposal: true,
  companion: true,
  budget: true,
  milestoneStarted: true,
  milestoneDone: true,
  release: true,
  failed: false,
}

const storageKey = 'desk-notify'

function supported(): boolean {
  return typeof window !== 'undefined' && 'Notification' in window
}

function currentPermission(): NotifyPermission {
  return supported() ? Notification.permission : 'unsupported'
}

// Settings as stored: `{"on": boolean, "categories": {...}}` with only the
// categories that differ from their default. The old value `on` is on with
// the defaults; a missing or broken value, or a storage that throws, is off
// with the defaults.
function load(): { on: boolean; changed: Partial<Categories> } {
  const changed: Partial<Categories> = {}
  try {
    const raw = localStorage.getItem(storageKey)
    if (raw === 'on') return { on: true, changed }
    const o = JSON.parse(raw ?? 'null') as { on?: unknown; categories?: Partial<Record<NotifyCategory, unknown>> } | null
    for (const c of Object.keys(defaults) as NotifyCategory[]) {
      const x = o?.categories?.[c]
      if (typeof x === 'boolean' && x !== defaults[c]) changed[c] = x
    }
    return { on: o?.on === true, changed }
  } catch {
    return { on: false, changed: {} }
  }
}

const initial = load()
// The user's changes, what is stored besides `on`.
const changed = initial.changed

function resolve(): Categories {
  return { ...defaults, ...changed }
}

function store() {
  try {
    localStorage.setItem(storageKey, JSON.stringify({ on: notify.enabled, categories: changed }))
  } catch {
    // Private mode or storage disabled: the choice lasts for this tab.
  }
}

export const notify = reactive({
  enabled: initial.on,
  permission: currentPermission(),
  categories: resolve(),
  // Whether the history database is there: null until a stream page says.
  history: null as boolean | null,
})

// refreshPermission picks up a permission changed in the browser's settings.
export function refreshPermission() {
  notify.permission = currentPermission()
}

// notifyActive says whether new items raise notifications now.
export function notifyActive(): boolean {
  return notify.enabled && notify.permission === 'granted'
}

// wants says whether a new item of category c raises a notification now.
export function wants(c: NotifyCategory): boolean {
  return notifyActive() && notify.categories[c]
}

export function setCategory(c: NotifyCategory, on: boolean) {
  if (!(c in defaults)) return
  if (on === defaults[c]) delete changed[c]
  else changed[c] = on
  notify.categories[c] = on
  store()
}

// enableNotify asks for permission and turns notifications on when granted.
export async function enableNotify(): Promise<void> {
  if (!supported()) return
  let permission: NotificationPermission
  try {
    permission = await Notification.requestPermission()
  } catch {
    permission = Notification.permission
  }
  notify.permission = permission
  notify.enabled = permission === 'granted'
  store()
}

export function disableNotify() {
  notify.enabled = false
  store()
}

// The Notifications dialog's state, shared by the account menu, the Inbox
// and App.vue, which mounts the dialog on first open.
export const notifyDialog = reactive({ open: false })

export function openNotifyDialog() {
  notifyDialog.open = true
}
