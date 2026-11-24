// The tabs of a view. The selected tab lives in the ?tab= query parameter
// so it can be linked to and reloaded; the first tab of a list is the
// default and is left out.
//
// Project pages: Milestones (the default: queue, Finishing, the milestone
// sections) | Activity (the event feed) | Decisions (R14) | Board | Metrics,
// and Plans with the plans feature (R13). Milestone pages: Cockpit (the
// default, R10 of "Desk: the Cockpit") | Issues | Metrics, then
// Retrospective once one exists (R14 of "Desk: the Cockpit"); the pilot's
// composer is at the bottom of the Cockpit (R12). The dashboard at /:
// Overview | Activity. The task page (and the issue page with a live task):
// Overview | Chat | Live | Logs | Calls (R10; Logs for the task's owners,
// run watchdog R8).
// Renamed and merged tabs keep their old ids as aliases (tabAlias): Metrics
// was named Spend (?tab=spend), Activity was named Stream (?tab=stream), a
// task's Details went into its Overview (?tab=details). A milestone's
// removed tabs open where their content moved (milestoneAlias).
// BaseTab: a project page's Activity, Board and Metrics tabs.
export type BaseTab = 'activity' | 'board' | 'metrics'
export type StreamTab = 'milestones' | BaseTab | 'decisions' | 'plans'
export type MilestoneTab = 'cockpit' | 'issues' | 'metrics' | 'retrospective'
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
// first, the default. Needs you is the Cockpit's pinned cards, Decisions a
// view of the panel and the Board a view of the Issues tab (R14 of "Desk:
// the Cockpit"; milestoneAlias).
export const milestoneTabs: Tab<MilestoneTab>[] = [
  { id: 'cockpit', label: 'Cockpit' },
  { id: 'issues', label: 'Issues' },
  { id: 'metrics', label: 'Metrics' },
  { id: 'retrospective', label: 'Retrospective' },
]

// milestoneTabsFor are the tabs a milestone shows: no Retrospective until
// one exists. Until the milestone has loaded (null) it stays hidden, so it
// does not show and vanish; a link to it waits (Stream.vue reads ?tab=
// against milestoneTabs while loading).
export function milestoneTabsFor(m: { retrospective?: unknown } | null): Tab<MilestoneTab>[] {
  return milestoneTabs.filter((t) => t.id !== 'retrospective' || !!m?.retrospective)
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
// its Overview (R6 of the same milestone). A milestone's removed tabs are
// in milestoneTabAliases.
const tabAliases: Readonly<Record<string, string>> = { spend: 'metrics', stream: 'activity', details: 'overview' }

// milestoneTabAliases are where a milestone's removed tabs went (R14 of
// "Desk: the Cockpit", D789-P2): Activity (once Stream), Needs you and the
// Pilot tab into the Cockpit, Decisions into the Cockpit's panel, the Board
// into the Issues tab's view switch.
const milestoneTabAliases: Readonly<Record<string, { tab?: string; panel?: string; view?: string }>> = {
  activity: {},
  stream: {},
  needs: {},
  pilot: {},
  decisions: { panel: 'decisions' },
  board: { tab: 'issues', view: 'board' },
}

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
// reads it, a removed tab's id as the tab its content moved to
// (milestoneTabAliases), until Stream.vue has rewritten the URL. A prompt
// or problem link (#prompt-Q<n>, #problem-<iid>) without ?tab= opens the
// Cockpit, the default, where the card is pinned (R10).
export function milestoneTabFromRoute(v: QueryValue, tabs: readonly Tab<string>[] = milestoneTabs): string {
  const list = (Array.isArray(v) ? v : [v ?? null]).map((s) => (s != null && Object.hasOwn(milestoneTabAliases, s) ? (milestoneTabAliases[s]!.tab ?? 'cockpit') : s))
  return tabFromQuery(list, tabs)
}

// taskTabFromRoute is a task's tab among those available: ?tab= as
// tabFromQuery reads it (unknown or unavailable tabs open Overview), but an
// old #live-transcript link without ?tab= opens Live where the task has it.
export function taskTabFromRoute(v: QueryValue, hash: string, available: readonly Tab<TaskTab>[] = taskTabs): TaskTab {
  const none = v == null || v === '' || (Array.isArray(v) && !v.length)
  if (none && hash === '#live-transcript' && available.some((t) => t.id === 'live')) return 'live'
  return tabFromQuery(v, available)
}

// milestoneAlias is the query of an old milestone link at the place its
// content moved to (milestoneTabAliases), null when it needs no change;
// Stream.vue replaces the URL with it on load, so the address shows the
// new place (D789-P1). The pilot drawer's ?pilot= goes too: the pilot's
// composer is at the bottom of the Cockpit (R12 of "Desk: the Cockpit").
// A repeated ?tab= is read by its first value; other parameters stay.
export function milestoneAlias<V>(query: Record<string, V>): Record<string, V | string> | null {
  const { pilot: _, ...rest } = query
  const tab: unknown = Array.isArray(rest.tab) ? rest.tab[0] : rest.tab
  const to = typeof tab === 'string' && Object.hasOwn(milestoneTabAliases, tab) ? milestoneTabAliases[tab]! : null
  if (!to) return Object.hasOwn(query, 'pilot') ? rest : null
  const { tab: _old, ...next } = rest
  return { ...next, ...to }
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
