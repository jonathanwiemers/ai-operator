import type { InboxItem } from './types'

// MilestoneRoute is a milestone's Cockpit (the default tab), with a card's
// anchor in hash.
type MilestoneRoute = {
  name: 'stream-milestone'
  params: { namespace: string; name: string; iid: string }
  hash?: string
}

// ProblemRoute is one problem's thread in the Cockpit's pinned cards
// (issue-<iid>; issue-0 the milestone's own).
type ProblemRoute = {
  name: 'problem'
  params: { namespace: string; name: string; iid: string; problem: string }
}

export type InboxRoute =
  | { name: 'task'; params: { namespace: string; name: string; task: string }; hash?: string }
  | MilestoneRoute
  | ProblemRoute
  | { name: 'stream-project'; params: { namespace: string; name: string } }

// pinnedAnchor is the hash of the Cockpit's pinned cards (Cockpit.vue).
export const pinnedAnchor = '#pinned'

// inboxRoute is where an Inbox item leads, for a click on its row and on its
// notification alike. Every entry of a milestone opens its Cockpit with its
// card open and in view (R17 of "Desk: the Cockpit"): pauses and problems
// their problem thread, a milestone's prompts their card (#prompt-Q<n>, which
// opens the prompt's problem thread), proposals their prompt's card or the
// pinned cards. An agent's question opens its task page. A ready companion
// opens its home task, without one its milestone. An issue's budget warning
// opens its implement task, where the issue's budget is raised (chat
// `budget <usd>`), without one its milestone.
export function inboxRoute(item: InboxItem): InboxRoute {
  const base = { namespace: item.namespace, name: item.project }
  const promptHash = item.promptID ? `#prompt-${item.promptID}` : undefined
  if (item.kind === 'prompt') {
    // An agent's question sits on its task page, the others in the milestone.
    if (item.reason === 'needsInput' && item.task) return { name: 'task', params: { ...base, task: item.task }, hash: promptHash }
    if (item.milestoneIID && promptHash) return cockpit(base, item.milestoneIID, promptHash)
    if (item.milestoneIID) return problemThread(base, item.milestoneIID, item.issueIID)
    if (item.task) return { name: 'task', params: { ...base, task: item.task }, hash: promptHash }
  }
  if (item.milestoneIID && (item.kind === 'pause' || item.kind === 'problem')) return problemThread(base, item.milestoneIID, item.issueIID)
  if (item.milestoneIID && item.kind === 'proposal') return cockpit(base, item.milestoneIID, promptHash ?? pinnedAnchor)
  // A ready companion opens its home task (release, home MR, owners); the
  // Inbox row also links the companion MR itself.
  if ((item.kind === 'task' || item.kind === 'companionReady' || item.kind === 'budgetWarning') && item.task) return { name: 'task', params: { ...base, task: item.task } }
  return milestoneRoute(item.namespace, item.project, item.milestoneIID)
}

// milestoneRoute is a milestone's Cockpit, without a milestone (iid 0 or
// none) the project's stream. A click on an event's notification opens it.
export function milestoneRoute(namespace: string, name: string, iid?: number): InboxRoute {
  if (iid) return { name: 'stream-milestone', params: { namespace, name, iid: String(iid) } }
  return { name: 'stream-project', params: { namespace, name } }
}

function cockpit(base: { namespace: string; name: string }, iid: number, hash: string): MilestoneRoute {
  return { name: 'stream-milestone', params: { ...base, iid: String(iid) }, hash }
}

function problemThread(base: { namespace: string; name: string }, iid: number, issue = 0): ProblemRoute {
  return { name: 'problem', params: { ...base, iid: String(iid), problem: `issue-${issue}` } }
}
