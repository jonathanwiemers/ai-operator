import type { InboxItem } from './types'

export type InboxRoute =
  | { name: 'task'; params: { namespace: string; name: string; task: string }; hash?: string }
  | { name: 'stream-milestone'; params: { namespace: string; name: string; iid: string }; hash?: string }
  | { name: 'stream-project'; params: { namespace: string; name: string } }

// inboxRoute is where an Inbox item leads, for a click on its row and on its
// notification alike. Pauses and proposals open the milestone stream, whose
// context panel holds the full pause card and the proposal actions.
export function inboxRoute(item: InboxItem): InboxRoute {
  const base = { namespace: item.namespace, name: item.project }
  if (item.kind === 'prompt') {
    const hash = item.promptID ? `#prompt-${item.promptID}` : undefined
    // An agent's question sits on its task page, the others in the milestone.
    if (item.reason === 'needsInput' && item.task) return { name: 'task', params: { ...base, task: item.task }, hash }
    if (item.milestoneIID) return { name: 'stream-milestone', params: { ...base, iid: String(item.milestoneIID) }, hash }
    if (item.task) return { name: 'task', params: { ...base, task: item.task }, hash }
  }
  if (item.milestoneIID) {
    const iid = String(item.milestoneIID)
    if (item.kind === 'pause' || item.kind === 'proposal') return { name: 'stream-milestone', params: { ...base, iid } }
  }
  if (item.kind === 'task' && item.task) return { name: 'task', params: { ...base, task: item.task } }
  if (item.milestoneIID) return { name: 'stream-milestone', params: { ...base, iid: String(item.milestoneIID) } }
  return { name: 'stream-project', params: base }
}
