import type { InboxItem } from './types'

type MilestoneRoute = {
  name: 'stream-milestone'
  params: { namespace: string; name: string; iid: string }
  query?: { tab: 'needs' }
  hash?: string
}

// ProblemRoute is one problem's thread on the Needs you tab (issue-<iid>;
// issue-0 the milestone's own).
type ProblemRoute = {
  name: 'problem'
  params: { namespace: string; name: string; iid: string; problem: string }
}

export type InboxRoute =
  | { name: 'task'; params: { namespace: string; name: string; task: string }; hash?: string }
  | MilestoneRoute
  | ProblemRoute
  | { name: 'stream-project'; params: { namespace: string; name: string } }

// inboxRoute is where an Inbox item leads, for a click on its row and on its
// notification alike. Pauses, proposals and a milestone's prompts open the
// milestone's Needs you tab, a prompt or pause with an issue that issue's
// problem thread (where the bar acts on it); proposals the tab itself. A ready companion opens its
// home task, without one its milestone. An issue's budget warning opens its
// implement task, where the issue's budget is raised (chat `budget <usd>`),
// without one its milestone.
export function inboxRoute(item: InboxItem): InboxRoute {
  const base = { namespace: item.namespace, name: item.project }
  if (item.kind === 'prompt') {
    const hash = item.promptID ? `#prompt-${item.promptID}` : undefined
    // An agent's question sits on its task page, the others in the milestone.
    if (item.reason === 'needsInput' && item.task) return { name: 'task', params: { ...base, task: item.task }, hash }
    if (item.milestoneIID) return problemTab(base, item.milestoneIID, item.issueIID)
    if (item.task) return { name: 'task', params: { ...base, task: item.task }, hash }
  }
  if (item.milestoneIID && (item.kind === 'pause' || item.kind === 'problem')) return problemTab(base, item.milestoneIID, item.issueIID)
  if (item.milestoneIID && item.kind === 'proposal') return needsTab(base, item.milestoneIID)
  // A ready companion opens its home task (release, home MR, owners); the
  // Inbox row also links the companion MR itself.
  if ((item.kind === 'task' || item.kind === 'companionReady' || item.kind === 'budgetWarning') && item.task) return { name: 'task', params: { ...base, task: item.task } }
  if (item.milestoneIID) return { name: 'stream-milestone', params: { ...base, iid: String(item.milestoneIID) } }
  return { name: 'stream-project', params: base }
}

function needsTab(base: { namespace: string; name: string }, iid: number): MilestoneRoute {
  return { name: 'stream-milestone', params: { ...base, iid: String(iid) }, query: { tab: 'needs' } }
}

function problemTab(base: { namespace: string; name: string }, iid: number, issue = 0): ProblemRoute {
  return { name: 'problem', params: { ...base, iid: String(iid), problem: `issue-${issue}` } }
}
