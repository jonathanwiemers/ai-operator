import { eventKey, eventText } from './eventNotify.ts'
import { notificationText } from './inboxNotify.ts'
import { localTimesText } from './localTime.ts'
import { parseOpen, type NotifyOpen } from './notifyOpen.ts'

// Turns a Web Push payload (`{ item }` or `{ event }`, as the push sender
// writes it) into a notification, with the texts of the in-tab ones. Pure,
// and free of DOM and Vue: the service worker (sw.ts) is built from it.
export interface PushNotification {
  title: string
  options: { body: string; tag: string; icon: string; data: { open: NotifyOpen } }
}

// pushNotification is null for a payload that is neither an item nor an
// event with its namespace, project and kind.
export function pushNotification(payload: unknown): PushNotification | null {
  const open = parseOpen(payload)
  if (!open) return null
  const [{ title, body }, tag] = 'item' in open ? [notificationText(open.item), open.item.key] : [eventText(open.event), eventKey(open.event)]
  return { title, options: { body: localTimesText(body, Date.now()), tag, icon: '/logo.svg', data: { open } } }
}
