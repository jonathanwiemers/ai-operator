// The tabs of a view. The selected tab lives in the ?tab= query parameter
// so it can be linked to and reloaded; the first tab of a list is the
// default and is left out.
//
// Project pages: Stream | Decisions (R14) | Board | Metrics, and Plans with
// the plans feature (R13). Milestone pages: Issues | Needs you | Decisions (R11, D546-P1) |
// Pilot | Retrospective | Stream | Board | Metrics (decision 7, R12), so an old
// milestone link with ?tab=stream still opens the stream. The dashboard
// at /: Overview | Stream (decision 3). The task page (and the issue page
// with a live task): Overview | Chat | Live | Logs | Calls | Details (R10;
// Logs for the task's owners, run watchdog R8). Metrics was named Spend:
// ?tab=spend still opens it (tabAlias).
// BaseTab: the tabs project and milestone pages share.
export type BaseTab = 'stream' | 'board' | 'metrics'
export type StreamTab = BaseTab | 'decisions' | 'plans'
export type MilestoneTab = 'issues' | 'needs' | 'decisions' | 'pilot' | 'retrospective' | BaseTab
export type DashboardTab = 'overview' | 'stream'
export type TaskTab = 'overview' | 'chat' | 'live' | 'logs' | 'calls' | 'details'
// A plan's tabs on phones: Chat, Decisions, Draft. Tablet and wider keep
// the chat left and show planPaneTabs (Draft, Decisions) on the right; one
// ?tab= serves both layouts (D617-P1).
export type PlanTab = 'chat' | 'decisions' | 'draft'
export type PlanPaneTab = 'draft' | 'decisions'
export const planTabs: Tab<PlanTab>[] = [
  { id: 'chat', label: 'Chat' },
  { id: 'decisions', label: 'Decisions' },
  { id: 'draft', label: 'Draft' },
]
export const planPaneTabs: Tab<PlanPaneTab>[] = [
  { id: 'draft', label: 'Draft' },
  { id: 'decisions', label: 'Decisions' },
]

// planPaneTab is the right pane's tab on wide screens: Decisions for
// ?tab=decisions, Draft for chat (or none) and draft.
export function planPaneTab(tab: PlanTab): PlanPaneTab {
  return tab === 'decisions' ? 'decisions' : 'draft'
}

export interface Tab<T extends string> {
  id: T
  label: string
}

export const streamBaseTabs: Tab<BaseTab>[] = [
  { id: 'stream', label: 'Stream' },
  { id: 'board', label: 'Board' },
  { id: 'metrics', label: 'Metrics' },
]

// projectTabsFor are a project page's tabs: Decisions right after Stream
// (R14), Plans only with the plans feature, so ?tab=plans without it opens
// Stream.
export function projectTabsFor(plans: boolean): Tab<StreamTab>[] {
  const [stream, ...rest] = streamBaseTabs
  const tabs: Tab<StreamTab>[] = [stream!, { id: 'decisions', label: 'Decisions' }, ...rest]
  return plans ? [...tabs, { id: 'plans', label: 'Plans' }] : tabs
}

export const milestoneTabs: Tab<MilestoneTab>[] = [
  { id: 'issues', label: 'Issues' },
  { id: 'needs', label: 'Needs you' },
  { id: 'decisions', label: 'Decisions' },
  { id: 'pilot', label: 'Pilot' },
  { id: 'retrospective', label: 'Retrospective' },
  ...streamBaseTabs,
]

// storedMilestoneTabs are a milestone's tabs once its tasks are gone: the
// Board needs them. Decisions stays, it reads the history database.
export const storedMilestoneTabs: Tab<MilestoneTab>[] = milestoneTabs.filter((t) => t.id !== 'board')

// milestoneTabsFor are the tabs a milestone shows (Decision 12 of "Desk:
// one look on every page"): no Board once its tasks are gone, and no
// Retrospective until one exists. Until the milestone has loaded (null)
// the Retrospective stays hidden too, so it does not show and vanish. The
// other tabs stay: their content arrives while the milestone runs (D632-P3).
export function milestoneTabsFor(m: { stored?: boolean; retrospective?: unknown } | null): Tab<MilestoneTab>[] {
  const list = m?.stored ? storedMilestoneTabs : milestoneTabs
  return m?.retrospective ? list : list.filter((t) => t.id !== 'retrospective')
}

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
  { id: 'logs', label: 'Logs' },
  { id: 'calls', label: 'Calls' },
  { id: 'details', label: 'Details' },
]

type QueryValue = string | null | (string | null)[] | undefined

// tabAliases are the old ids of renamed tabs, so old links keep working:
// the Spend tab became Metrics (R17 of the milestone "Desk: one Metrics
// page").
const tabAliases: Readonly<Record<string, string>> = { spend: 'metrics' }

// tabAlias is the current id of a ?tab= value: the renamed tab's id for an
// old one, the value itself otherwise.
export function tabAlias(v: string | null | undefined): string | null | undefined {
  return v != null && Object.hasOwn(tabAliases, v) ? tabAliases[v] : v
}

// tabFromQuery reads ?tab= against tabs (Stream, Board, Metrics by
// default), old ids through tabAlias: unknown, empty or repeated values
// fall back to the first known one, else to the list's first tab.
export function tabFromQuery(v: QueryValue): BaseTab
export function tabFromQuery<T extends string>(v: QueryValue, tabs: readonly Tab<T>[]): T
export function tabFromQuery(v: QueryValue, tabs: readonly Tab<string>[] = streamBaseTabs): string {
  const list = Array.isArray(v) ? v : [v]
  for (const s of list) {
    const id = tabAlias(s)
    const t = tabs.find((x) => x.id === id)
    if (t) return t.id
  }
  return tabs[0]!.id
}

// milestoneTabFromRoute is a milestone page's tab: ?tab= as tabFromQuery
// reads it, but a prompt or problem link (#prompt-Q<n>, #problem-<iid>)
// without ?tab= opens the Needs you tab, where the card is (R12).
export function milestoneTabFromRoute(v: QueryValue, hash: string, tabs: readonly Tab<string>[] = milestoneTabs): string {
  const none = v == null || v === '' || (Array.isArray(v) && !v.length)
  const card = hash.startsWith('#prompt-') || hash.startsWith('#problem-')
  if (none && card && tabs.some((t) => t.id === 'needs')) return 'needs'
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
  tabs: readonly Tab<string>[] = streamBaseTabs,
): Omit<Q, 'tab'> & { tab?: string } {
  const { tab: _, ...rest } = query
  return tab === tabs[0]!.id ? rest : { ...rest, tab }
}
