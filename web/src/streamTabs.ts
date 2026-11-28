// The tabs of a view. The selected tab lives in the ?tab= query parameter
// so it can be linked to and reloaded; the first tab of a list is the
// default and is left out.
//
import { pinnedAnchor } from './inboxRoute.ts'

// Project pages: Milestones (the default, D1 of "Desk: Milestones and
// Issues first": queue, Finishing, the milestone sections, the Board as a
// view) | Cockpit (the stream across the project's milestones) | Loose
// issues (with spec.looseIssues: the loose issues queue and the issues
// without a milestone, R8 of "Loose issues") | Decisions | Metrics, and
// Plans with the plans feature. Milestone pages: Issues (the default, D1)
// | Timeline (with the history feature, D2) | Cockpit | Decisions |
// Metrics, then Retrospective once one exists (R14;
// Decisions is a tab again, R6 of "Desk: the Cockpit newest first by
// issue"); the pilot's composer is on top of the milestone's Cockpit (R3).
// Every link to a Cockpit card or thread says ?tab=cockpit; one without
// ?tab= opens the Cockpit by its hash (cockpitQuery, D6). The dashboard at /:
// Projects (the default, one card per project, R5 of "Desk: Milestones and
// Issues first") | Timeline | Cockpit (R16: the stream across all projects);
// its old Overview tab opens Projects, Activity the Cockpit (dashboardAlias).
// The task page (and the issue page with a live task):
// Overview | Chat | Live | Logs | Calls (R10; Logs for the task's owners,
// run watchdog R8).
// Renamed and merged tabs keep their old ids as aliases (tabAlias): Metrics
// was named Spend (?tab=spend), Activity was named Stream (?tab=stream), a
// task's Details went into its Overview (?tab=details). A project's and a
// milestone's removed tabs open where their content moved (projectAlias,
// milestoneAlias).
export type ProjectTab = 'cockpit' | 'milestones' | 'loose' | 'decisions' | 'metrics' | 'plans'
export type MilestoneTab = 'cockpit' | 'issues' | 'timeline' | 'decisions' | 'metrics' | 'retrospective'
export type DashboardTab = 'projects' | 'timeline' | 'cockpit'
export type TaskTab = 'overview' | 'live' | 'logs' | 'calls'
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

// projectTabsFor are a project page's tabs: Milestones first and the
// default, then the Cockpit, Loose issues after it only with the loose
// issues queue (ProjectDetail.looseIssues), Plans only with the plans
// feature, so ?tab=loose or ?tab=plans without them opens Milestones.
export function projectTabsFor(plans: boolean, loose = false): Tab<ProjectTab>[] {
  const tabs: Tab<ProjectTab>[] = [
    { id: 'milestones', label: 'Milestones' },
    { id: 'cockpit', label: 'Cockpit' },
    ...(loose ? [{ id: 'loose' as const, label: 'Loose issues' }] : []),
    { id: 'decisions', label: 'Decisions' },
    { id: 'metrics', label: 'Metrics' },
  ]
  return plans ? [...tabs, { id: 'plans', label: 'Plans' }] : tabs
}

// milestoneTabs are all of a milestone's tabs, in their order: Issues
// first, the default, then the Timeline of the issues' runs (D2, once a
// view of the Issues tab) and the Cockpit (D1). Needs you is the Cockpit's
// pinned cards and the Board a view of the Issues tab (R14 of "Desk: the
// Cockpit"; milestoneAlias).
export const milestoneTabs: Tab<MilestoneTab>[] = [
  { id: 'issues', label: 'Issues' },
  { id: 'timeline', label: 'Timeline' },
  { id: 'cockpit', label: 'Cockpit' },
  { id: 'decisions', label: 'Decisions' },
  { id: 'metrics', label: 'Metrics' },
  { id: 'retrospective', label: 'Retrospective' },
]

// milestoneTabsFor are the tabs a milestone shows: Timeline only with the
// history feature (history), no Retrospective until one exists. Until the
// milestone has loaded (null) Retrospective stays hidden, so it does not
// show and vanish; a link to either waits (Stream.vue reads ?tab= against
// milestoneTabs while loading), then falls back to Issues without it.
export function milestoneTabsFor(m: { retrospective?: unknown } | null, history: boolean): Tab<MilestoneTab>[] {
  return milestoneTabs.filter((t) => (t.id !== 'retrospective' || !!m?.retrospective) && (t.id !== 'timeline' || history))
}

export const dashboardTabs: Tab<DashboardTab>[] = [
  { id: 'projects', label: 'Projects' },
  { id: 'timeline', label: 'Timeline' },
  { id: 'cockpit', label: 'Cockpit' },
]

// taskTabs are all of a task's tabs; a task shows those it has content
// for, Overview always.
export const taskTabs: Tab<TaskTab>[] = [
  { id: 'overview', label: 'Overview' },
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
// in milestoneTabAliases, a project's in projectTabAliases.
const tabAliases: Readonly<Record<string, string>> = { spend: 'metrics', stream: 'activity', details: 'overview' }

type TabAliases = Readonly<Record<string, { tab?: string; panel?: string; view?: string }>>

// milestoneTabAliases are where a milestone's removed tabs went (R14 of
// "Desk: the Cockpit", D789-P2): Activity (once Stream), Needs you and the
// Pilot tab into the Cockpit, the Board into the Issues tab's view switch.
// The panel's old Decisions view is a tab again (panelAliasQuery).
const milestoneTabAliases: TabAliases = {
  activity: { tab: 'cockpit' },
  stream: { tab: 'cockpit' },
  needs: { tab: 'cockpit' },
  pilot: { tab: 'cockpit' },
  board: { tab: 'issues', view: 'board' },
}

// projectTabAliases are where a project's removed tabs went (R15 of "Desk:
// the Cockpit"): Activity (once Stream) into the Cockpit, the Board into
// the Milestones tab's view switch.
const projectTabAliases: TabAliases = {
  activity: { tab: 'cockpit' },
  stream: { tab: 'cockpit' },
  board: { tab: 'milestones', view: 'board' },
}

// dashboardTabAliases are where the dashboard's removed tabs went (R16 of
// "Desk: the Cockpit", D791-P2, and R5 of "Desk: Milestones and Issues
// first"): Activity (once Stream) into the Cockpit; Overview, whose Working
// now is in the Now & next sidebar, into the first tab, Projects.
const dashboardTabAliases: TabAliases = { activity: { tab: 'cockpit' }, stream: { tab: 'cockpit' }, overview: {} }

// tabAlias is the current id of a ?tab= value: the renamed tab's id for an
// old one, the value itself otherwise.
export function tabAlias(v: string | null | undefined): string | null | undefined {
  return v != null && Object.hasOwn(tabAliases, v) ? tabAliases[v] : v
}

// tabFromQuery reads ?tab= against tabs, old ids through tabAlias:
// unknown, empty or repeated values fall back to the first known one, else
// to the list's first tab.
export function tabFromQuery<T extends string>(v: QueryValue, tabs: readonly Tab<T>[]): T {
  const list = Array.isArray(v) ? v : [v]
  for (const s of list) {
    const id = tabAlias(s)
    const t = tabs.find((x) => x.id === id)
    if (t) return t.id
  }
  return tabs[0]!.id
}

// aliasedTab is ?tab= with each removed tab's id replaced by the tab its
// content moved to (aliases). An alias without a tab means the list's first
// tab: it becomes null, which tabFromQuery skips.
function aliasedTab(v: QueryValue, aliases: TabAliases): (string | null)[] {
  return (Array.isArray(v) ? v : [v ?? null]).map((s) => (s != null && Object.hasOwn(aliases, s) ? (aliases[s]!.tab ?? null) : s))
}

// hasTab tells whether a query names a tab: ?tab= is neither absent, empty
// nor an empty list.
function hasTab(query: Record<string, unknown>): boolean {
  const v = query.tab
  return !(v == null || v === '' || (Array.isArray(v) && !v.length))
}

// cockpitQuery is the query of a link that points at a Cockpit card or
// thread without ?tab= (D6 of "Desk: Milestones and Issues first"): a
// GitLab comment's #prompt-Q<n>, an old notification's #issue-<iid> or
// #pinned, an old problem link's #problem-<iid> (MilestoneNeeds opens its
// thread), an event's card (#event-<id>), an old Now & next row's ?live=1. It is the query with
// ?tab=cockpit, null for a link that names a tab or points at nothing in
// the Cockpit. ?live= stays; the caller keeps the hash.
export function cockpitQuery<V>(query: Record<string, V>, hash: string): Record<string, V | string> | null {
  if (hasTab(query)) return null
  const live: unknown = Array.isArray(query.live) ? query.live[0] : query.live
  const card = ['#prompt-', '#issue-', '#problem-', '#event-'].some((p) => hash.startsWith(p)) || hash === pinnedAnchor
  return card || live === '1' ? { ...query, tab: 'cockpit' } : null
}

// aliasQuery is the query of a link to a removed tab at the place its
// content moved to (aliases), null when ?tab= names none. A repeated ?tab=
// is read by its first value; other parameters stay.
function aliasQuery<V>(query: Record<string, V>, aliases: TabAliases): Record<string, V | string> | null {
  const tab: unknown = Array.isArray(query.tab) ? query.tab[0] : query.tab
  if (typeof tab !== 'string' || !Object.hasOwn(aliases, tab)) return null
  const { tab: _old, ...rest } = query
  return { ...rest, ...aliases[tab]! }
}

// projectTabFromRoute is a project page's tab: ?tab= as tabFromQuery reads
// it, a removed tab's id as the tab its content moved to
// (projectTabAliases), until Stream.vue has rewritten the URL.
export function projectTabFromRoute(v: QueryValue, tabs: readonly Tab<string>[]): string {
  return tabFromQuery(aliasedTab(v, projectTabAliases), tabs)
}

// projectAlias is the query of an old project link (?tab=activity, stream,
// board; ?panel=decisions) at the place its content moved to, null when it
// needs no change; Stream.vue replaces the URL with it on load. Given the
// link's hash, a link to a Cockpit card without ?tab= gets ?tab=cockpit
// (cockpitQuery). The old ?panel=queue goes (nowPanelQuery), as on a milestone: Stream.vue opens
// the Now & next sidebar for it (R9 of "Desk: the Cockpit newest first by
// issue").
export function projectAlias<V>(query: Record<string, V>, hash = ''): Record<string, V | string> | null {
  const dropped = nowPanelQuery(query)
  const base = dropped ?? query
  return panelAliasQuery(base) ?? aliasQuery(base, projectTabAliases) ?? cockpitQuery(base, hash) ?? dropped
}

// panelAliasQuery is the query of an old link to the panel's Decisions view
// (?panel=decisions: a decision link or the tracking issue's) at the
// Decisions tab (R6 of "Desk: the Cockpit newest first by issue", D817-P2),
// null for any other query. ?panel= goes, ?tab=decisions replaces ?tab=,
// the other parameters (?milestone=, ?q=) stay; the caller keeps the hash.
function panelAliasQuery<V>(query: Record<string, V>): Record<string, V | string> | null {
  const panel: unknown = Array.isArray(query.panel) ? query.panel[0] : query.panel
  if (panel !== 'decisions') return null
  const { panel: _p, tab: _t, ...rest } = query
  return { ...rest, tab: 'decisions' }
}

// dashboardTabFromRoute is the dashboard's tab: ?tab= as tabFromQuery reads
// it, a removed tab's id as its new tab (dashboardTabAliases), until
// Dashboard.vue has rewritten the URL.
export function dashboardTabFromRoute(v: QueryValue): DashboardTab {
  return tabFromQuery(aliasedTab(v, dashboardTabAliases), dashboardTabs)
}

// dashboardAlias is the query of an old dashboard link (?tab=activity,
// stream at the Cockpit, overview at Projects), null when it needs no change;
// Dashboard.vue replaces the URL with it on load.
export function dashboardAlias<V>(query: Record<string, V>): Record<string, V | string> | null {
  return aliasQuery(query, dashboardTabAliases)
}

// milestoneTabFromRoute is a milestone page's tab: ?tab= as tabFromQuery
// reads it, a removed tab's id as the tab its content moved to
// (milestoneTabAliases), until Stream.vue has rewritten the URL. A link to
// a Cockpit card without ?tab= (#prompt-Q<n>) gets ?tab=cockpit from
// milestoneAlias (cockpitQuery).
export function milestoneTabFromRoute(v: QueryValue, tabs: readonly Tab<string>[] = milestoneTabs): string {
  return tabFromQuery(aliasedTab(v, milestoneTabAliases), tabs)
}

// taskTabFromRoute is a task's tab among those available: ?tab= as
// tabFromQuery reads it (unknown or unavailable tabs open Overview), but an
// old #live-transcript link without ?tab= opens Live where the task has it.
// The removed Chat tab opens Overview (taskTabAliases).
export function taskTabFromRoute(v: QueryValue, hash: string, available: readonly Tab<TaskTab>[] = taskTabs): TaskTab {
  const none = v == null || v === '' || (Array.isArray(v) && !v.length)
  if (none && hash === '#live-transcript' && available.some((t) => t.id === 'live')) return 'live'
  return tabFromQuery(aliasedTab(v, taskTabAliases), available)
}

// taskTabAliases are where a task's removed tabs went: the Chat tab into
// Overview, as the pilot is the only chat on milestones (R18 of "Desk: the
// Cockpit"). Task-only, not in tabAliases: chat is a plan tab (D792-P2).
const taskTabAliases: TabAliases = { chat: { tab: 'overview' } }

// milestoneAlias is the query of an old milestone link at the place its
// content moved to (milestoneTabAliases), null when it needs no change;
// Stream.vue replaces the URL with it on load, so the address shows the
// new place (D789-P1). The pilot drawer's ?pilot= goes too: without ?tab=
// it opens the Cockpit, where the pilot's composer is.
// A repeated ?tab= is read by its first value; other parameters stay. An
// old ?panel=decisions opens the Decisions tab (panelAliasQuery); the old
// ?panel=issues and ?panel=queue go (nowPanelQuery). Given the link's
// hash, a link to a Cockpit card without ?tab= gets ?tab=cockpit
// (cockpitQuery). The Issues tab's old Timeline view opens the Timeline tab
// (timelineViewQuery).
export function milestoneAlias<V>(query: Record<string, V>, hash = ''): Record<string, V | string> | null {
  const { pilot: _, ...rest } = query
  const pilot = Object.hasOwn(query, 'pilot')
  const dropped = nowPanelQuery(rest)
  const timeline = timelineViewQuery(dropped ?? rest)
  const base = timeline ?? dropped ?? rest
  const cockpit = pilot && !hasTab(base) ? { ...base, tab: 'cockpit' } : cockpitQuery(base, hash)
  return panelAliasQuery(base) ?? aliasQuery(base, milestoneTabAliases) ?? cockpit ?? (dropped || pilot || timeline ? base : null)
}

// timelineViewQuery is the query of an old link to the Issues tab's
// Timeline view (?view=timeline, without ?tab= or with ?tab=issues) at the
// Timeline tab (D2 of "Desk: Milestones and Issues first"): ?view= goes,
// ?tab=timeline replaces ?tab=. Without the history feature that tab is
// not there and the page shows Issues, its first tab (D897-P2). null for
// any other query; ?view=lanes and ?view=board stay.
function timelineViewQuery<V>(query: Record<string, V>): Record<string, V | string> | null {
  const view: unknown = Array.isArray(query.view) ? query.view[0] : query.view
  const tab: unknown = Array.isArray(query.tab) ? query.tab[0] : query.tab
  if (view !== 'timeline' || (hasTab(query) && tab !== 'issues')) return null
  const { view: _v, tab: _t, ...rest } = query
  return { ...rest, tab: 'timeline' }
}

// nowPanelQuery is the query of an old link to the milestone panel's
// Issues or Queue view (?panel=issues, ?panel=queue) without ?panel=: the
// panel is the Now & next sidebar, one view (R7 of "Desk: the Cockpit
// newest first by issue", D820-P2). null for any other query. Stream.vue
// opens the sidebar for such a link, a slide-over below 1180 px.
function nowPanelQuery<V>(query: Record<string, V>): Record<string, V> | null {
  const panel: unknown = Array.isArray(query.panel) ? query.panel[0] : query.panel
  if (panel !== 'issues' && panel !== 'queue') return null
  const { panel: _p, ...rest } = query
  return rest
}

// liveless is the query without ?live=, the Cockpit's open Live transcript
// (a Now & next row's link): a link to another tab drops it, so the D6
// rewrite (cockpitQuery) does not send it back to the Cockpit.
export function liveless<Q extends Record<string, unknown>>(query: Q): Omit<Q, 'live'> {
  const { live: _, ...rest } = query
  return rest
}

// tabQuery is the query for a tab, keeping the other parameters; the
// list's first tab (Milestones on project pages, Issues on milestone
// pages) drops the parameter.
export function tabQuery<Q extends Record<string, unknown>>(
  query: Q,
  tab: string,
  tabs: readonly Tab<string>[],
): Omit<Q, 'tab'> & { tab?: string } {
  const { tab: _, ...rest } = query
  return tab === tabs[0]!.id ? rest : { ...rest, tab }
}
