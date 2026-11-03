import { reactive } from 'vue'

// Browser notifications for new Inbox items: off until the user turns them
// on, remembered per browser. Only while a Desk tab is open (no service
// worker, no Web Push).

export type NotifyPermission = NotificationPermission | 'unsupported'

const storageKey = 'desk-notify'

function supported(): boolean {
  return typeof window !== 'undefined' && 'Notification' in window
}

function currentPermission(): NotifyPermission {
  return supported() ? Notification.permission : 'unsupported'
}

function stored(): boolean {
  try {
    return localStorage.getItem(storageKey) === 'on'
  } catch {
    return false
  }
}

function store(on: boolean) {
  try {
    if (on) localStorage.setItem(storageKey, 'on')
    else localStorage.removeItem(storageKey)
  } catch {
    // Private mode or storage disabled: the choice lasts for this tab.
  }
}

export const notify = reactive({
  enabled: stored(),
  permission: currentPermission(),
})

// refreshPermission picks up a permission changed in the browser's settings.
export function refreshPermission() {
  notify.permission = currentPermission()
}

// notifyActive says whether new items raise notifications now.
export function notifyActive(): boolean {
  return notify.enabled && notify.permission === 'granted'
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
  store(notify.enabled)
}

export function disableNotify() {
  notify.enabled = false
  store(false)
}
