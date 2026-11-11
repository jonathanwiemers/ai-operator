// The tabs of a view. The selected tab lives in the ?tab= query parameter
// so it can be linked to and reloaded; the first tab of a list is the
// default and is left out.
//
// Project pages: Stream | Board | Spend. Milestone pages: Issues | Pilot |
// Retrospective | Stream | Board | Spend (decision 7), so an old
// milestone link with ?tab=stream still opens the stream. The dashboard
// at /: Overview | Stream (decision 3). The task page (and the issue page
// with a live task): Overview | Chat | Live | Calls | Details (R10).
export type StreamTab = 'stream' | 'board' | 'spend'
export type MilestoneTab = 'issues' | 'pilot' | 'retrospective' | StreamTab
export type DashboardTab = 'overview' | 'stream'
export type TaskTab = 'overview' | 'chat' | 'live' | 'calls' | 'details'

export interface Tab<T extends string> {
  id: T
  label: string
}

export const projectTabs: Tab<StreamTab>[] = [
  { id: 'stream', label: 'Stream' },
  { id: 'board', label: 'Board' },
  { id: 'spend', label: 'Spend' },
]

export const milestoneTabs: Tab<MilestoneTab>[] = [
  { id: 'issues', label: 'Issues' },
  { id: 'pilot', label: 'Pilot' },
  { id: 'retrospective', label: 'Retrospective' },
  ...projectTabs,
]

// storedMilestoneTabs are a milestone's tabs once its tasks are gone: the
// Board needs them.
export const storedMilestoneTabs: Tab<MilestoneTab>[] = milestoneTabs.filter((t) => t.id !== 'board')

export const dashboardTabs: Tab<DashboardTab>[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'stream', label: 'Stream' },
]

// taskTabs are all of a task's tabs; a task shows those it has content
// for, Overview always.
export const taskTabs: Tab<TaskTab>[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'chat', label: 'Chat' },
  { id: 'live', label: 'Live' },
  { id: 'calls', label: 'Calls' },
  { id: 'details', label: 'Details' },
]

type QueryValue = string | null | (string | null)[] | undefined

// tabFromQuery reads ?tab= against tabs (the project tabs by default):
// unknown, empty or repeated values fall back to the first known one, else
// to the list's first tab.
export function tabFromQuery(v: QueryValue): StreamTab
export function tabFromQuery<T extends string>(v: QueryValue, tabs: readonly Tab<T>[]): T
export function tabFromQuery(v: QueryValue, tabs: readonly Tab<string>[] = projectTabs): string {
  const list = Array.isArray(v) ? v : [v]
  for (const s of list) {
    const t = tabs.find((x) => x.id === s)
    if (t) return t.id
  }
  return tabs[0]!.id
}

// milestoneTabFromRoute is a milestone page's tab: ?tab= as tabFromQuery
// reads it, but a prompt link (#prompt-Q<n>) without ?tab= opens the Pilot
// tab, where the prompt's card is (D230-P1).
export function milestoneTabFromRoute(v: QueryValue, hash: string, tabs: readonly Tab<string>[] = milestoneTabs): string {
  const none = v == null || v === '' || (Array.isArray(v) && !v.length)
  if (none && hash.startsWith('#prompt-') && tabs.some((t) => t.id === 'pilot')) return 'pilot'
  return tabFromQuery(v, tabs)
}

// taskTabFromRoute is a task's tab among those available: ?tab= as
// tabFromQuery reads it (unknown or unavailable tabs open Overview), but an
// old #live-transcript link without ?tab= opens Live where the task has it.
export function taskTabFromRoute(v: QueryValue, hash: string, available: readonly Tab<TaskTab>[] = taskTabs): TaskTab {
  const none = v == null || v === '' || (Array.isArray(v) && !v.length)
  if (none && hash === '#live-transcript' && available.some((t) => t.id === 'live')) return 'live'
  return tabFromQuery(v, available)
}

// tabQuery is the query for a tab, keeping the other parameters; the
// list's first tab (Stream on the project tabs, the default list) drops
// the parameter.
export function tabQuery<Q extends Record<string, unknown>>(
  query: Q,
  tab: string,
  tabs: readonly Tab<string>[] = projectTabs,
): Omit<Q, 'tab'> & { tab?: string } {
  const { tab: _, ...rest } = query
  return tab === tabs[0]!.id ? rest : { ...rest, tab }
}

// carryTab is the tab a scope link keeps when it moves between tab sets
// (project and milestone): a chosen tab the target has, else the target's
// default. A default stays a default: the project's Stream opens a
// milestone's Issues.
export function carryTab(tab: string, from: readonly Tab<string>[], to: readonly Tab<string>[]): string {
  if (tab === from[0]!.id || !to.some((t) => t.id === tab)) return to[0]!.id
  return tab
}
