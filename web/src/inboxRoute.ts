import type { InboxItem, StreamEvent } from './types'

// MilestoneRoute is a milestone's Cockpit (?tab=cockpit, its second tab),
// with a card's anchor in hash.
type MilestoneRoute = {
  name: 'stream-milestone'
  params: { namespace: string; name: string; iid: string }
  query: { tab: 'cockpit' }
  hash?: string
}

// ProblemRoute is one problem's thread in the Cockpit's pinned cards
// (issue-<iid>; issue-0 the milestone's own).
type ProblemRoute = {
  name: 'problem'
  params: { namespace: string; name: string; iid: string; problem: string }
}

// LooseRoute is a project's Loose issues tab, with a queue card's anchor in
// hash (looseAnchor).
type LooseRoute = {
  name: 'stream-project'
  params: { namespace: string; name: string }
  query: { tab: 'loose' }
  hash: string
}

export type InboxRoute =
  | { name: 'task'; params: { namespace: string; name: string; task: string }; hash?: string }
  | MilestoneRoute
  | ProblemRoute
  | LooseRoute
  | { name: 'plans'; params: { namespace: string; name: string; plan: string } }
  | { name: 'stream-project'; params: { namespace: string; name: string } }
  | ProjectCockpitRoute

// ProjectCockpitRoute is a project's Cockpit tab (its default tab is
// Milestones), with an event card's anchor in hash.
type ProjectCockpitRoute = {
  name: 'stream-project'
  params: { namespace: string; name: string }
  query: { tab: 'cockpit' }
  hash: string
}

// pinnedAnchor is the hash of the Cockpit's pinned cards (Cockpit.vue).
export const pinnedAnchor = '#pinned'

// inboxRoute is where an Inbox item leads, for a click on its row and on its
// notification alike. Every entry of a milestone opens its Cockpit with its
// card open and in view (R17 of "Desk: the Cockpit"): pauses and problems
// their problem thread, a milestone's prompts their card (#prompt-Q<n>, which
// opens the prompt's problem thread), proposals their prompt's card or the
// pinned cards. An agent's question opens its task page. Every other entry
// of the loose issues queue (queue) opens the project's Loose issues tab at
// its card: a prompt's (#prompt-Q<n>), a proposal's, else its issue's
// (looseAnchor in looseIssues.ts). A ready companion
// opens its home task, without one its milestone. An issue's budget warning
// opens its implement task, where the issue's budget is raised (chat
// `budget <usd>`), without one its milestone. A ready plan opens its plan
// page.
export function inboxRoute(item: InboxItem): InboxRoute {
  const base = { namespace: item.namespace, name: item.project }
  if (item.kind === 'plan' && item.plan) return { name: 'plans', params: { ...base, plan: item.plan } }
  const promptHash = item.promptID ? `#prompt-${item.promptID}` : undefined
  // An agent's question sits on its task page, the others in the milestone.
  if (item.kind === 'prompt' && item.reason === 'needsInput' && item.task) return { name: 'task', params: { ...base, task: item.task }, hash: promptHash }
  if (item.queue) {
    return { name: 'stream-project', params: base, query: { tab: 'loose' }, hash: promptHash ?? `#loose-${item.proposalID ? `P${item.proposalID}` : item.issueIID ?? 0}` }
  }
  if (item.kind === 'prompt') {
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

// milestoneRoute is a milestone's Cockpit, where its events are; without a
// milestone (iid 0 or none) the project's page, its Milestones tab.
export function milestoneRoute(namespace: string, name: string, iid?: number): InboxRoute {
  if (iid) return { name: 'stream-milestone', params: { namespace, name, iid: String(iid) }, query: { tab: 'cockpit' } }
  return { name: 'stream-project', params: { namespace, name } }
}

// eventRoute is where an event's notification leads (milestone started,
// done, a release): its own card in the milestone's Cockpit (#event-<id>),
// without a milestone in the project's Cockpit. The thread that holds the
// card opens, scrolls to and highlights it (CockpitThread.vue).
export function eventRoute(e: StreamEvent): InboxRoute {
  const base = { namespace: e.namespace, name: e.project }
  const hash = `#event-${e.id}`
  if (e.milestoneIID) return cockpit(base, e.milestoneIID, hash)
  return { name: 'stream-project', params: base, query: { tab: 'cockpit' }, hash }
}

function cockpit(base: { namespace: string; name: string }, iid: number, hash: string): MilestoneRoute {
  return { name: 'stream-milestone', params: { ...base, iid: String(iid) }, query: { tab: 'cockpit' }, hash }
}

function problemThread(base: { namespace: string; name: string }, iid: number, issue = 0): ProblemRoute {
  return { name: 'problem', params: { ...base, iid: String(iid), problem: `issue-${issue}` } }
}
