// Tests of src/streamTabs.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  dashboardTabs,
  milestoneAlias,
  milestoneTabFromRoute,
  milestoneTabs,
  milestoneTabsFor,
  panelFromQuery,
  panelQuery,
  panelViews,
  planPaneTab,
  planPaneTabs,
  planTabs,
  projectAlias,
  projectPanelViews,
  projectTabFromRoute,
  projectTabsFor,
  tabAlias,
  tabFromQuery,
  tabQuery,
  taskTabFromRoute,
  taskTabs,

} from '../src/streamTabs.ts'

const projectTabs = projectTabsFor(false)

test('tabFromQuery: the first tab is the default', () => {
  assert.equal(tabFromQuery(undefined, projectTabs), 'cockpit')
  assert.equal(tabFromQuery(null, projectTabs), 'cockpit')
  assert.equal(tabFromQuery('', projectTabs), 'cockpit')
  assert.equal(tabFromQuery('cockpit', projectTabs), 'cockpit')
})

test('tabFromQuery: known tabs are kept', () => {
  assert.equal(tabFromQuery('milestones', projectTabs), 'milestones')
  assert.equal(tabFromQuery('metrics', projectTabs), 'metrics')
})

test('tabFromQuery: unknown tabs fall back to the first tab', () => {
  assert.equal(tabFromQuery('cost', projectTabs), 'cockpit')
  assert.equal(tabFromQuery('Milestones', projectTabs), 'cockpit')
})

test('tabFromQuery: repeated values take the first known one', () => {
  assert.equal(tabFromQuery(['x', 'milestones'], projectTabs), 'milestones')
  assert.equal(tabFromQuery([null, 'x'], projectTabs), 'cockpit')
})

test('tabQuery: the first tab drops the parameter, other tabs set it', () => {
  assert.deepEqual(tabQuery({ tab: 'milestones', q: 'x' }, 'cockpit', projectTabs), { q: 'x' })
  assert.deepEqual(tabQuery({ q: 'x' }, 'milestones', projectTabs), { q: 'x', tab: 'milestones' })
  assert.deepEqual(tabQuery({}, 'cockpit', projectTabs), {})
})

test('dashboard tabs: Overview · Activity, Overview is the default, ?tab=stream opens Activity', () => {
  assert.deepEqual(dashboardTabs.map((t) => t.label), ['Overview', 'Activity'])
  assert.equal(tabFromQuery(undefined, dashboardTabs), 'overview')
  assert.equal(tabFromQuery('', dashboardTabs), 'overview')
  assert.equal(tabFromQuery('stream', dashboardTabs), 'activity')
  assert.equal(tabFromQuery('board', dashboardTabs), 'overview')
  assert.equal(tabFromQuery(['x', 'stream'], dashboardTabs), 'activity')
})

test('dashboard tabQuery: overview drops the parameter, activity sets it', () => {
  assert.deepEqual(tabQuery({ tab: 'activity', q: 'x' }, 'overview', dashboardTabs), { q: 'x' })
  assert.deepEqual(tabQuery({}, 'activity', dashboardTabs), { tab: 'activity' })
})

test('milestone tabs: Cockpit · Issues · Metrics · Retrospective, Cockpit is the default', () => {
  assert.deepEqual(milestoneTabs.map((t) => t.label), ['Cockpit', 'Issues', 'Metrics', 'Retrospective'])
  assert.equal(tabFromQuery(undefined, milestoneTabs), 'cockpit')
  assert.equal(tabFromQuery('', milestoneTabs), 'cockpit')
  assert.equal(tabFromQuery('cockpit', milestoneTabs), 'cockpit')
  assert.equal(tabFromQuery('issues', milestoneTabs), 'issues')
  assert.equal(milestoneTabsFor(null)[0].id, 'cockpit')
})

test('milestone tabs: unknown values fall back to Cockpit', () => {
  assert.equal(tabFromQuery('retro', milestoneTabs), 'cockpit')
  assert.equal(tabFromQuery('Stream', milestoneTabs), 'cockpit')
  assert.equal(tabFromQuery(['x', 'metrics'], milestoneTabs), 'metrics')
})

test('project tabs: issues is unknown there', () => {
  assert.equal(tabFromQuery('issues', projectTabsFor(true)), 'cockpit')
})

test('milestone tabQuery: Cockpit drops the parameter, Issues and Metrics set it', () => {
  assert.deepEqual(tabQuery({ tab: 'issues', q: 'x' }, 'cockpit', milestoneTabs), { q: 'x' })
  assert.deepEqual(tabQuery({}, 'issues', milestoneTabs), { tab: 'issues' })
  assert.deepEqual(tabQuery({}, 'metrics', milestoneTabs), { tab: 'metrics' })
})

test('milestoneTabsFor: Retrospective once one exists, hidden while the milestone loads', () => {
  const ids = (m) => milestoneTabsFor(m).map((t) => t.id)
  assert.deepEqual(ids({}), ['cockpit', 'issues', 'metrics'])
  assert.deepEqual(ids({ stored: true }), ['cockpit', 'issues', 'metrics'])
  assert.deepEqual(ids({ retrospective: { text: 'done' } }), ['cockpit', 'issues', 'metrics', 'retrospective'])
  assert.deepEqual(ids(null), ['cockpit', 'issues', 'metrics'])
})

test('milestoneTabFromRoute: a hidden tab opens the Cockpit once loaded, and waits while loading', () => {
  assert.equal(milestoneTabFromRoute('retrospective', milestoneTabsFor({})), 'cockpit')
  assert.equal(milestoneTabFromRoute('retrospective', milestoneTabsFor({ retrospective: {} })), 'retrospective')
  assert.equal(milestoneTabFromRoute('retrospective'), 'retrospective', 'read against all tabs while loading')
  assert.equal(milestoneTabFromRoute(undefined), 'cockpit')
  assert.equal(milestoneTabFromRoute('issues'), 'issues')
  assert.equal(milestoneTabFromRoute('spend'), 'metrics')
  assert.equal(milestoneTabFromRoute(['x', 'issues']), 'issues')
})

test('milestoneTabFromRoute: removed tabs open where their content moved (R14)', () => {
  for (const old of ['activity', 'stream', 'needs', 'pilot', 'decisions']) {
    assert.equal(milestoneTabFromRoute(old, milestoneTabsFor({})), 'cockpit', old)
  }
  assert.equal(milestoneTabFromRoute('board', milestoneTabsFor({})), 'issues')
  assert.equal(milestoneTabFromRoute(['needs', 'issues']), 'cockpit', 'the first value wins')
})

test('milestoneAlias: every removed tab rewrites to its new place, other parameters stay', () => {
  for (const old of ['activity', 'stream', 'needs', 'pilot']) {
    assert.deepEqual(milestoneAlias({ tab: old, q: 'x' }), { q: 'x' }, old)
  }
  assert.deepEqual(milestoneAlias({ tab: 'decisions' }), { panel: 'decisions' })
  assert.deepEqual(milestoneAlias({ tab: 'decisions', panel: 'queue' }), { panel: 'decisions' })
  assert.deepEqual(milestoneAlias({ tab: 'board', q: 'x' }), { q: 'x', tab: 'issues', view: 'board' })
  assert.deepEqual(milestoneAlias({ tab: ['needs', 'issues'] }), {})
})

test('milestoneAlias: current links need no change, the pilot drawer\'s ?pilot= goes', () => {
  assert.equal(milestoneAlias({}), null)
  assert.equal(milestoneAlias({ tab: 'metrics', q: 'x' }), null)
  assert.equal(milestoneAlias({ tab: 'issues', view: 'board' }), null)
  assert.deepEqual(milestoneAlias({ pilot: '1' }), {})
  assert.deepEqual(milestoneAlias({ tab: 'metrics', pilot: '1', q: 'x' }), { tab: 'metrics', q: 'x' })
  assert.deepEqual(milestoneAlias({ pilot: ['x', '1'] }), {})
  assert.deepEqual(milestoneAlias({ tab: 'pilot', pilot: '1', panel: 'queue' }), { panel: 'queue' })
})

test('tabAlias: the milestone aliases are not project aliases', () => {
  assert.equal(tabAlias('pilot'), 'pilot')
  assert.equal(tabAlias('stream'), 'activity')
})

test('task tabs: Overview is the default and drops the parameter', () => {
  assert.deepEqual(taskTabs.map((t) => t.id), ['overview', 'chat', 'live', 'logs', 'calls'])
  assert.equal(taskTabFromRoute(undefined, ''), 'overview')
  assert.equal(taskTabFromRoute('', ''), 'overview')
  assert.equal(taskTabFromRoute('details', ''), 'overview', 'the old Details tab opens Overview')
  assert.equal(taskTabFromRoute(['x', 'calls'], ''), 'calls')
  assert.equal(taskTabFromRoute('logs', '', taskTabs.filter((t) => t.id !== 'logs')), 'overview')
  assert.deepEqual(tabQuery({ tab: 'chat', q: 'x' }, 'overview', taskTabs), { q: 'x' })
  assert.deepEqual(tabQuery({}, 'calls', taskTabs), { tab: 'calls' })
})

test('taskTabFromRoute: unknown and unavailable tabs open Overview', () => {
  const noLive = taskTabs.filter((t) => t.id !== 'live')
  assert.equal(taskTabFromRoute('board', ''), 'overview')
  assert.equal(taskTabFromRoute('Live', ''), 'overview')
  assert.equal(taskTabFromRoute('live', '', noLive), 'overview')
  assert.equal(taskTabFromRoute('calls', '', noLive), 'calls')
})

test('taskTabFromRoute: #live-transcript without ?tab= opens Live where there is one', () => {
  const noLive = taskTabs.filter((t) => t.id !== 'live')
  assert.equal(taskTabFromRoute(undefined, '#live-transcript'), 'live')
  assert.equal(taskTabFromRoute('', '#live-transcript'), 'live')
  assert.equal(taskTabFromRoute(undefined, '#live-transcript', noLive), 'overview')
  assert.equal(taskTabFromRoute('calls', '#live-transcript'), 'calls')
  assert.equal(taskTabFromRoute('details', '#live-transcript'), 'overview')
  assert.equal(taskTabFromRoute(undefined, '#prompt-Q4'), 'overview')
})

test('planTabs: Chat is the default, then Decisions and Draft, kept in ?tab=', () => {
  assert.deepEqual(planTabs.map((t) => t.id), ['chat', 'decisions', 'draft'])
  assert.equal(tabFromQuery(undefined, planTabs), 'chat')
  assert.equal(tabFromQuery('draft', planTabs), 'draft')
  assert.equal(tabFromQuery('decisions', planTabs), 'decisions')
  assert.deepEqual(tabQuery({ tab: 'draft' }, 'chat', planTabs), {})
  assert.deepEqual(tabQuery({}, 'draft', planTabs), { tab: 'draft' })
  assert.deepEqual(tabQuery({}, 'decisions', planTabs), { tab: 'decisions' })
})

test('planPaneTab: the wide right pane shows Decisions only for ?tab=decisions, else Draft', () => {
  assert.deepEqual(planPaneTabs.map((t) => t.id), ['draft', 'decisions'])
  assert.equal(planPaneTab(tabFromQuery(undefined, planTabs)), 'draft')
  assert.equal(planPaneTab('chat'), 'draft')
  assert.equal(planPaneTab('draft'), 'draft')
  assert.equal(planPaneTab('decisions'), 'decisions')
})

test('project tabs: Cockpit · Milestones · Metrics, Plans only with the plans feature', () => {
  assert.deepEqual(projectTabsFor(false).map((t) => t.label), ['Cockpit', 'Milestones', 'Metrics'])
  assert.deepEqual(projectTabsFor(true).map((t) => t.label), ['Cockpit', 'Milestones', 'Metrics', 'Plans'])
  assert.deepEqual(projectTabsFor(true).map((t) => t.id), ['cockpit', 'milestones', 'metrics', 'plans'])
  assert.equal(tabFromQuery('plans', projectTabsFor(true)), 'plans')
  assert.deepEqual(tabQuery({}, 'plans', projectTabsFor(true)), { tab: 'plans' })
})

test('project tabs: the Cockpit is the default and drops ?tab=, Milestones sets it', () => {
  for (const plans of [false, true]) {
    const tabs = projectTabsFor(plans)
    assert.equal(tabFromQuery(undefined, tabs), 'cockpit')
    assert.equal(tabFromQuery('', tabs), 'cockpit')
    assert.equal(tabFromQuery('spend', tabs), 'metrics')
    assert.deepEqual(tabQuery({ tab: 'milestones', q: 'x' }, 'cockpit', tabs), { q: 'x' })
    assert.deepEqual(tabQuery({}, 'milestones', tabs), { tab: 'milestones' })
  }
})

test('projectTabFromRoute: removed tabs open where their content moved (R15)', () => {
  const tabs = projectTabsFor(true)
  assert.equal(projectTabFromRoute(undefined, tabs), 'cockpit')
  for (const old of ['activity', 'stream', 'decisions']) assert.equal(projectTabFromRoute(old, tabs), 'cockpit', old)
  assert.equal(projectTabFromRoute('board', tabs), 'milestones')
  assert.equal(projectTabFromRoute(['x', 'board'], tabs), 'milestones')
  assert.equal(projectTabFromRoute('plans', tabs), 'plans')
  assert.equal(projectTabFromRoute('spend', tabs), 'metrics')
  // Milestone-only aliases are unknown on a project.
  assert.equal(projectTabFromRoute('issues', tabs), 'cockpit')
  assert.equal(projectTabFromRoute('needs', tabs), 'cockpit')
})

test('projectAlias: every removed tab rewrites to its new place, other parameters stay', () => {
  for (const old of ['activity', 'stream']) assert.deepEqual(projectAlias({ tab: old, q: 'x' }), { q: 'x' }, old)
  assert.deepEqual(projectAlias({ tab: 'decisions', milestone: '7', q: 'x' }), { milestone: '7', q: 'x', panel: 'decisions' })
  assert.deepEqual(projectAlias({ tab: 'decisions', panel: 'queue' }), { panel: 'decisions' })
  assert.deepEqual(projectAlias({ tab: 'board' }), { tab: 'milestones', view: 'board' })
  assert.deepEqual(projectAlias({ tab: ['activity', 'board'] }), {})
})

test('projectAlias: current links need no change', () => {
  assert.equal(projectAlias({}), null)
  assert.equal(projectAlias({ tab: 'milestones', view: 'board' }), null)
  assert.equal(projectAlias({ tab: 'metrics', q: 'x' }), null)
  assert.equal(projectAlias({ tab: 'pilot' }), null)
  assert.equal(projectAlias({ pilot: '1' }), null)
})

test('project panel: Queue by default and drops ?panel=, Decisions sets it', () => {
  assert.deepEqual(projectPanelViews.map((v) => v.label), ['Queue', 'Decisions'])
  assert.equal(panelFromQuery(undefined, projectPanelViews), 'queue')
  assert.equal(panelFromQuery('issues', projectPanelViews), 'queue')
  assert.equal(panelFromQuery('decisions', projectPanelViews), 'decisions')
  assert.deepEqual(panelQuery({ panel: 'decisions', q: 'x' }, 'queue', projectPanelViews), { q: 'x' })
  assert.deepEqual(panelQuery({}, 'decisions', projectPanelViews), { panel: 'decisions' })
})

test('milestone tabs have no Plans', () => {
  assert.equal(tabFromQuery('plans', milestoneTabs), 'cockpit')
})

test('tabFromQuery: ?tab=plans without the feature falls back to the first tab', () => {
  assert.equal(tabFromQuery('plans', projectTabsFor(false)), 'cockpit')
})

test('Metrics tab: the Spend tab renamed, ?tab=spend still opens it', () => {
  assert.deepEqual(projectTabs.at(-1), { id: 'metrics', label: 'Metrics' })
  assert.equal(tabAlias('spend'), 'metrics')
  assert.equal(tabAlias('metrics'), 'metrics')
  assert.equal(tabAlias('board'), 'board')
  assert.equal(tabAlias('constructor'), 'constructor')
  assert.equal(tabAlias(null), null)
  assert.equal(tabFromQuery('spend', projectTabs), 'metrics')
  assert.equal(tabFromQuery('spend', milestoneTabs), 'metrics')
  assert.equal(tabFromQuery(['x', 'spend'], milestoneTabs), 'metrics')
  assert.equal(milestoneTabFromRoute('spend', milestoneTabs), 'metrics')
  // Lists without the tab ignore the alias.
  assert.equal(tabFromQuery('spend', dashboardTabs), 'overview')
  assert.equal(taskTabFromRoute('spend', ''), 'overview')
  // New links use the new id.
  assert.deepEqual(tabQuery({ tab: 'spend', q: 'x' }, 'metrics', milestoneTabs), { q: 'x', tab: 'metrics' })
})

test('panelFromQuery: issues by default, queue and decisions by ?panel=', () => {
  assert.deepEqual(
    panelViews.map((v) => v.id),
    ['issues', 'queue', 'decisions'],
  )
  assert.equal(panelFromQuery(undefined), 'issues')
  assert.equal(panelFromQuery(''), 'issues')
  assert.equal(panelFromQuery('nope'), 'issues')
  assert.equal(panelFromQuery('queue'), 'queue')
  assert.equal(panelFromQuery('decisions'), 'decisions')
  assert.equal(panelFromQuery(['nope', 'decisions']), 'decisions')
})

test('panelQuery: keeps the other parameters, Issues drops ?panel=', () => {
  assert.deepEqual(panelQuery({ tab: 'issues', panel: 'queue' }, 'decisions'), { tab: 'issues', panel: 'decisions' })
  assert.deepEqual(panelQuery({ tab: 'metrics', panel: 'queue' }, 'issues'), { tab: 'metrics' })
  assert.deepEqual(panelQuery({}, 'queue'), { panel: 'queue' })
})
