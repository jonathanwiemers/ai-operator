import { pauseTitle } from './pauseCard.ts'
import type { NotifyCategory } from './notify.ts'
import type { InboxItem } from './types'

// inboxKey identifies an Inbox item across reloads. A pause includes the
// paused issue and its kind, so a different cause is a new item.
export function inboxKey(item: InboxItem): string {
  const parts: (string | number | undefined)[] = [item.namespace, item.project, item.kind]
  switch (item.kind) {
    case 'task':
      parts.push(item.task)
      break
    case 'proposal':
      parts.push(item.milestoneIID, item.proposalID)
      break
    case 'prompt':
      parts.push(item.task, item.promptID)
      break
    case 'pause':
      parts.push(item.milestoneIID, item.issueIID, item.pauseKind)
      // A hold of one issue is not the milestone's pause over it.
      if (item.hold) parts.push('hold')
      break
    case 'pilotError':
      parts.push(item.task, item.title, item.reason)
      break
    case 'companionReady':
      parts.push(item.issueIID, item.title)
      break
    case 'budgetWarning':
      // The reason names the budget: a raised one that is crossed again is
      // a new item.
      parts.push(item.milestoneIID, item.issueIID, item.reason)
      break
  }
  return parts.map((p) => (p == null ? '' : String(p))).join('/')
}

// notifiable says whether an item raises a browser notification: pauses,
// proposals, prompts, companions ready for the owners to merge, issues at
// 80 % of their budget and tasks that need input. Failed and blocked tasks
// only count.
export function notifiable(item: InboxItem): boolean {
  return (
    item.kind === 'pause' ||
    item.kind === 'proposal' ||
    item.kind === 'prompt' ||
    item.kind === 'companionReady' ||
    item.kind === 'budgetWarning' ||
    (item.kind === 'task' && item.reason === 'needs-input')
  )
}

// notifyCategory is a notifiable item's category in the Notifications
// dialog (notify.ts).
export function notifyCategory(item: InboxItem): NotifyCategory {
  switch (item.kind) {
    case 'proposal':
      return 'proposal'
    case 'companionReady':
      return 'companion'
    case 'budgetWarning':
      return 'budget'
    case 'prompt':
    case 'task':
      return 'question'
    default:
      return 'problem'
  }
}

// newNotifications returns the notifiable items whose key isn't in seen,
// each key once.
export function newNotifications(seen: ReadonlySet<string>, items: InboxItem[]): InboxItem[] {
  const out: InboxItem[] = []
  const taken = new Set<string>()
  for (const item of items) {
    const key = inboxKey(item)
    if (!notifiable(item) || seen.has(key) || taken.has(key)) continue
    taken.add(key)
    out.push(item)
  }
  return out
}

export interface NotificationText {
  title: string
  body: string
}

// notificationText is the title and body of an item's notification.
export function notificationText(item: InboxItem): NotificationText {
  switch (item.kind) {
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
    default:
      return {
        title: item.issueIID ? `#${item.issueIID} needs input` : 'Needs input',
        body: [item.project, item.title].filter(Boolean).join(' · '),
      }
  }
}
