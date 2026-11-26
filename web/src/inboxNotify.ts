import { pauseTitle } from './pauseCard.ts'
import type { NotifyCategory } from './notify.ts'
import type { InboxItem } from './types'

// newNotifications returns the items of a category the user wants whose key
// isn't in seen, each key once. The server sets each item's key and its
// category (notify, empty for none).
export function newNotifications(seen: ReadonlySet<string>, items: InboxItem[], wants: (c: NotifyCategory) => boolean): InboxItem[] {
  const out: InboxItem[] = []
  const taken = new Set<string>()
  for (const item of items) {
    if (!item.notify || !wants(item.notify) || seen.has(item.key) || taken.has(item.key)) continue
    taken.add(item.key)
    out.push(item)
  }
  return out
}

// seenKeys is the seen set after a load: exactly the keys of its items. A
// key that left the Inbox is forgotten, so its item notifies again when it
// comes back.
export function seenKeys(items: InboxItem[]): Set<string> {
  return new Set(items.map((i) => i.key))
}

interface NotificationText {
  title: string
  body: string
}

// notificationText is the title and body of an item's notification, with
// the times as the server wrote them: showDeskNotification (deskWorker.ts)
// shows them in the browser's zone.
export function notificationText(item: InboxItem): NotificationText {
  switch (item.kind) {
    case 'problem': {
      // An open problem of an issue, stuck or held, or of the milestone
      // (issue 0), which pauses it.
      const name = item.title || item.project
      const title = item.issueIID ? `#${item.issueIID} ${item.hold ? 'held' : 'stuck'} · ${name}` : `Milestone paused · ${name}`
      return { title, body: item.reason }
    }
    case 'pause': {
      const cause = item.pauseKind ? pauseTitle(item.pauseKind) : ''
      // A hold stops one issue; the milestone may run on.
      if (item.hold) {
        return {
          title: `Issue${item.issueIID ? ` #${item.issueIID}` : ''} held · ${item.title || item.project}`,
          body: [cause, item.reason].filter(Boolean).join('\n'),
        }
      }
      const issue = item.issueIID ? `#${item.issueIID}` : ''
      const head = [cause, issue].filter(Boolean).join(' · ')
      return {
        title: `Autopilot paused · ${item.title || item.project}`,
        body: [head, item.reason].filter(Boolean).join('\n'),
      }
    }
    case 'proposal':
      return {
        title: item.proposalID != null ? `Proposal P${item.proposalID} · ${item.project}` : `Proposal · ${item.project}`,
        body: item.title,
      }
    case 'prompt':
      return {
        title: item.promptID ? `Question ${item.promptID} · ${item.project}` : `Question · ${item.project}`,
        body: item.title,
      }
    case 'companionReady':
      return {
        title: `Companion ready to merge · ${item.project}`,
        body: [[item.title, item.issueIID ? `companion of #${item.issueIID}` : ''].filter(Boolean).join(' · '), item.reason]
          .filter(Boolean)
          .join('\n'),
      }
    case 'budgetWarning':
      return {
        title: `Budget at 80 % · #${item.issueIID} · ${item.project}`,
        body: [item.title, item.reason].filter(Boolean).join('\n'),
      }
    case 'plan':
      // A plan agent's finished turn: the reason is the start of its reply,
      // or why the turn did not finish.
      return { title: `Plan ready · ${item.title || 'Untitled plan'}`, body: item.reason }
    default: {
      // A task that needs input, or one that failed or is blocked (the
      // failed category).
      const what = item.reason === 'failed' || item.reason === 'blocked' ? item.reason : 'needs input'
      return {
        title: item.issueIID ? `#${item.issueIID} ${what}` : what[0]!.toUpperCase() + what.slice(1),
        body: [item.project, item.title].filter(Boolean).join(' · '),
      }
    }
  }
}
