import type { InboxItem } from './types'

export type InboxRoute =
  | { name: 'milestone'; params: { namespace: string; name: string; iid: string } }
  | { name: 'task'; params: { namespace: string; name: string; task: string } }
  | { name: 'stream-milestone'; params: { namespace: string; name: string; iid: string } }
  | { name: 'stream-project'; params: { namespace: string; name: string } }

// inboxRoute is where an Inbox item leads, for a click on its row and on its
// notification alike. Pauses and proposals open the milestone page, the only
// one with the full pause card and the proposal actions; the Desk
// consolidation repoints that route later.
export function inboxRoute(item: InboxItem): InboxRoute {
  const base = { namespace: item.namespace, name: item.project }
  if (item.milestoneIID) {
    const iid = String(item.milestoneIID)
    if (item.kind === 'pause' || item.kind === 'proposal') return { name: 'milestone', params: { ...base, iid } }
  }
  if (item.kind === 'task' && item.task) return { name: 'task', params: { ...base, task: item.task } }
  if (item.milestoneIID) return { name: 'stream-milestone', params: { ...base, iid: String(item.milestoneIID) } }
  return { name: 'stream-project', params: base }
}
