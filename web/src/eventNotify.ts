import type { NotifyCategory } from './notify.ts'
import type { StreamEvent, StreamPage } from './types'

// Notifications from history events (R8 of "Desk: notifications"): a
// milestone started (its issues planned), its autopilot done, and a release
// published or failed. App.vue polls GET /stream for these kinds while one
// of their categories is wanted and the history database is there.

// The stream kinds the poller asks for; eventCategory narrows them.
const eventKinds = 'autopilot,milestone.started,release.ended'

// eventCategory is the category of an event that notifies, '' for none:
// an autopilot event only for phase Done, a release end only when it was
// released or failed.
export function eventCategory(e: StreamEvent): NotifyCategory | '' {
  switch (e.kind) {
    case 'milestone.started':
      return 'milestoneStarted'
    case 'autopilot':
      return e.data.phase === 'Done' ? 'milestoneDone' : ''
    case 'release.ended':
      return e.data.state === 'Released' || e.data.state === 'Failed' ? 'release' : ''
    default:
      return ''
  }
}

// eventNotifications keeps the events that notify, in their order.
export function eventNotifications(events: readonly StreamEvent[]): StreamEvent[] {
  return events.filter((e) => eventCategory(e) !== '')
}

// eventText is an event's title and body. The project is in the title, the
// milestone's title (data.title) in the body. The times are as the server
// wrote them: showDeskNotification (deskWorker.ts) shows them in the
// browser's zone.
export function eventText(e: StreamEvent): { title: string; body: string } {
  const name = e.data.title || e.summary
  if (e.kind === 'milestone.started') return { title: `Milestone started · ${e.project}`, body: name }
  if (e.kind === 'autopilot') return { title: `Milestone done · ${e.project}`, body: name }
  if (e.data.state === 'Failed') {
    return { title: `Release failed · ${e.project}`, body: [e.data.reason?.trim(), e.data.title].filter(Boolean).join('\n') }
  }
  return { title: e.data.version ? `Released ${e.data.version} · ${e.project}` : `Released · ${e.project}`, body: name }
}

// eventKey is the notification's tag.
export function eventKey(e: StreamEvent): string {
  return `event/${e.id}`
}

// eventPoller holds the stream's newest cursor. While it is empty (the
// first page, or a stream with no such events yet) a page only seeds it, so
// events from before the tab opened never notify. Then each page returns
// its wanted events, each id once, and moves the cursor forward. reset()
// seeds again, for when notifications are turned back on.
export function eventPoller() {
  let newest = ''
  const shown = new Set<number>()
  return {
    query(): URLSearchParams {
      const q = new URLSearchParams({ kinds: eventKinds })
      if (newest) q.set('after', newest)
      return q
    },
    step(page: Pick<StreamPage, 'events' | 'newest'>, wants: (c: NotifyCategory) => boolean): StreamEvent[] {
      const seeding = newest === ''
      if (page.newest) newest = page.newest
      if (seeding) return []
      const out: StreamEvent[] = []
      for (const e of eventNotifications(page.events ?? [])) {
        const c = eventCategory(e)
        if (c === '' || !wants(c) || shown.has(e.id)) continue
        shown.add(e.id)
        out.push(e)
      }
      return out
    },
    reset() {
      newest = ''
    },
  }
}
