import type { InboxItem } from './types'

type MilestoneRoute = {
  name: 'stream-milestone'
  params: { namespace: string; name: string; iid: string }
  query?: { tab: 'pilot' }
  hash?: string
}

export type InboxRoute =
  | { name: 'task'; params: { namespace: string; name: string; task: string }; hash?: string }
  | MilestoneRoute
  | { name: 'stream-project'; params: { namespace: string; name: string } }

// inboxRoute is where an Inbox item leads, for a click on its row and on its
// notification alike. Pauses, proposals and a milestone's prompts open the
// milestone's Pilot tab, which holds the full pause card, the prompt cards
// and the pilot chat with the proposal actions. A ready companion opens its
// home task, without one its milestone.
export function inboxRoute(item: InboxItem): InboxRoute {
  const base = { namespace: item.namespace, name: item.project }
  if (item.kind === 'prompt') {
    const hash = item.promptID ? `#prompt-${item.promptID}` : undefined
    // An agent's question sits on its task page, the others in the milestone.
    if (item.reason === 'needsInput' && item.task) return { name: 'task', params: { ...base, task: item.task }, hash }
    if (item.milestoneIID) return { ...pilotTab(base, item.milestoneIID), hash }
    if (item.task) return { name: 'task', params: { ...base, task: item.task }, hash }
  }
  if (item.milestoneIID && (item.kind === 'pause' || item.kind === 'proposal')) return pilotTab(base, item.milestoneIID)
  // A ready companion opens its home task (release, home MR, owners); the
  // Inbox row also links the companion MR itself.
  if ((item.kind === 'task' || item.kind === 'companionReady') && item.task) return { name: 'task', params: { ...base, task: item.task } }
  if (item.milestoneIID) return { name: 'stream-milestone', params: { ...base, iid: String(item.milestoneIID) } }
  return { name: 'stream-project', params: base }
}

function pilotTab(base: { namespace: string; name: string }, iid: number): MilestoneRoute {
  return { name: 'stream-milestone', params: { ...base, iid: String(iid) }, query: { tab: 'pilot' } }
}
