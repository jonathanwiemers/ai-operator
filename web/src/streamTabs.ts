// The tabs of a view. The selected tab lives in the ?tab= query parameter
// so it can be linked to and reloaded; the first tab of a list is the
// default and is left out.
//
// Project pages: Milestones (the default: queue, Finishing, the milestone
// sections) | Activity (the event feed) | Decisions (R14) | Board | Metrics,
// and Plans with the plans feature (R13). Milestone pages: Cockpit (the
// default, R10 of "Desk: the Cockpit") | Issues | Activity | Decisions (R11, D546-P1) | Board | Metrics, then Needs you while
// something needs you and Retrospective once one exists (Decision 7 of
// "Desk: one header and one row of tabs on every page"); the pilot's
// composer is at the bottom of the Cockpit (R12 of "Desk: the Cockpit"). The dashboard at /: Overview | Activity. The
// task page (and the issue page with a live task): Overview | Chat | Live |
// Logs | Calls (R10; Logs for the task's owners, run watchdog R8).
// Renamed and merged tabs keep their old ids as aliases (tabAlias): Metrics
// was named Spend (?tab=spend), Activity was named Stream (?tab=stream), a
// task's Details went into its Overview (?tab=details), and a milestone's
// Pilot tab, later a drawer, went into the Cockpit (?tab=pilot, see
// withoutPilot).
// BaseTab: the tabs project and milestone pages share.
export type BaseTab = 'activity' | 'board' | 'metrics'
export type StreamTab = 'milestones' | BaseTab | 'decisions' | 'plans'
export type MilestoneTab = 'cockpit' | 'issues' | 'needs' | 'decisions' | 'retrospective' | BaseTab
export type DashboardTab = 'overview' | 'activity'
export type TaskTab = 'overview' | 'chat' | 'live' | 'logs' | 'calls'
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
  { id: 'activity', label: 'Activity' },
  { id: 'board', label: 'Board' },
  { id: 'metrics', label: 'Metrics' },
]

// projectTabsFor are a project page's tabs: Milestones first and the
// default, Decisions right after Activity (R14), Plans only with the plans
// feature, so ?tab=plans without it opens Milestones.
export function projectTabsFor(plans: boolean): Tab<StreamTab>[] {
  const [activity, ...rest] = streamBaseTabs
  const tabs: Tab<StreamTab>[] = [
    { id: 'milestones', label: 'Milestones' },
    activity!,
    { id: 'decisions', label: 'Decisions' },
    ...rest,
  ]
  return plans ? [...tabs, { id: 'plans', label: 'Plans' }] : tabs
}

// milestoneTabs are all of a milestone's tabs, in their order: Cockpit
// first, the default.
export const milestoneTabs: Tab<MilestoneTab>[] = [
  { id: 'cockpit', label: 'Cockpit' },
  { id: 'issues', label: 'Issues' },
  streamBaseTabs[0]!,
  { id: 'decisions', label: 'Decisions' },
  ...streamBaseTabs.slice(1),
  { id: 'needs', label: 'Needs you' },
  { id: 'retrospective', label: 'Retrospective' },
]

// storedMilestoneTabs are a milestone's tabs once its tasks are gone: the
// Board needs them. Cockpit and Decisions stay, they read the history
// database.
export const storedMilestoneTabs: Tab<MilestoneTab>[] = milestoneTabs.filter((t) => t.id !== 'board')

// milestoneTabsFor are the tabs a milestone shows: no Board once its tasks
// are gone, Needs you only while something needs you (opts.needs, the
// count of needsOpen, D674-P1) or a problem thread is open or linked to
// (opts.problem: the problem route, a #prompt- or #problem- hash), and no
// Retrospective until one exists. Until the milestone has loaded (null)
// Needs you and Retrospective stay hidden, so they do not show and vanish;
// a link to one waits (Stream.vue reads ?tab= against milestoneTabs while
// loading). The other tabs stay: their content arrives while the milestone
// runs (D632-P3).
export function milestoneTabsFor(
  m: { stored?: boolean; retrospective?: unknown } | null,
  opts: { needs?: number; problem?: boolean } = {},
): Tab<MilestoneTab>[] {
  const list = m?.stored ? storedMilestoneTabs : milestoneTabs
  const needs = !!opts.problem || (!!m && (opts.needs ?? 0) > 0)
  return list.filter((t) => (t.id !== 'needs' || needs) && (t.id !== 'retrospective' || !!m?.retrospective))
}

export const dashboardTabs: Tab<DashboardTab>[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'activity', label: 'Activity' },
]

// taskTabs are all of a task's tabs; a task shows those it has content
// for, Overview always.
export const taskTabs: Tab<TaskTab>[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'chat', label: 'Chat' },
  { id: 'live', label: 'Live' },
  { id: 'logs', label: 'Logs' },
  { id: 'calls', label: 'Calls' },
]

type QueryValue = string | null | (string | null)[] | undefined

// tabAliases are the old ids of renamed or merged tabs, so old links keep
// working: the Spend tab became Metrics (R17 of the milestone "Desk: one
// Metrics page"), the Stream tab became Activity (R4 of "Desk: one header
// and one row of tabs on every page"), and a task's Details tab went into
// its Overview (R6 of the same milestone), and a milestone's Pilot tab,
// later the pilot drawer, went into the Cockpit's composer (R12 of "Desk:
// the Cockpit").
const tabAliases: Readonly<Record<string, string>> = { spend: 'metrics', stream: 'activity', details: 'overview', pilot: 'cockpit' }

// tabAlias is the current id of a ?tab= value: the renamed tab's id for an
// old one, the value itself otherwise.
export function tabAlias(v: string | null | undefined): string | null | undefined {
  return v != null && Object.hasOwn(tabAliases, v) ? tabAliases[v] : v
}

// tabFromQuery reads ?tab= against tabs (Activity, Board, Metrics by
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
// without ?tab= opens the Cockpit, where the card is pinned (R10).
export function milestoneTabFromRoute(v: QueryValue, hash: string, tabs: readonly Tab<string>[] = milestoneTabs): string {
  const none = v == null || v === '' || (Array.isArray(v) && !v.length)
  const card = hash.startsWith('#prompt-') || hash.startsWith('#problem-')
  if (none && card && tabs.some((t) => t.id === 'cockpit')) return 'cockpit'
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

// withoutPilot is the query of an old pilot link without its pilot
// parameters, null when it has none: the pilot drawer (?pilot=1) and the
// Pilot tab (?tab=pilot) are gone, the pilot's composer is at the bottom
// of the Cockpit (R12 of "Desk: the Cockpit"), so both open the Cockpit
// and Stream.vue drops them on load.
export function withoutPilot<V>(query: Record<string, V>): Record<string, V> | null {
  const { pilot: _, ...rest } = query
  const tab = rest.tab === 'pilot'
  if (tab) delete rest.tab
  return tab || Object.hasOwn(query, 'pilot') ? rest : null
}

// A milestone's context panel (R13 of "Desk: the Cockpit"): its views,
// switched at the top of the panel and kept in ?panel= (D786-P1), Issues
// the default and left out.
export type PanelView = 'issues' | 'queue' | 'decisions'
export const panelViews: Tab<PanelView>[] = [
  { id: 'issues', label: 'Issues' },
  { id: 'queue', label: 'Queue' },
  { id: 'decisions', label: 'Decisions' },
]

// panelFromQuery reads ?panel= as tabFromQuery reads ?tab=: unknown, empty
// or repeated values fall back to the first known one, else to Issues.
export function panelFromQuery(v: QueryValue): PanelView {
  return tabFromQuery(v, panelViews)
}

// panelQuery is the query with the panel on view, keeping the other
// parameters; Issues drops ?panel=.
export function panelQuery<V>(query: Record<string, V>, view: PanelView): Record<string, V | string> {
  const { panel: _, ...rest } = query
  return view === panelViews[0]!.id ? rest : { ...rest, panel: view }
}

// tabQuery is the query for a tab, keeping the other parameters; the
// list's first tab (Milestones on the project tabs, Activity on the
// default list) drops the parameter.
export function tabQuery<Q extends Record<string, unknown>>(
  query: Q,
  tab: string,
  tabs: readonly Tab<string>[] = streamBaseTabs,
): Omit<Q, 'tab'> & { tab?: string } {
  const { tab: _, ...rest } = query
  return tab === tabs[0]!.id ? rest : { ...rest, tab }
}
