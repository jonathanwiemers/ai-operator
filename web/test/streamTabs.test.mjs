// Tests of src/streamTabs.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  dashboardAlias,
  dashboardTabFromRoute,
  dashboardTabs,
  milestoneAlias,
  milestoneTabFromRoute,
  milestoneTabs,
  milestoneTabsFor,
  planPaneTab,
  planPaneTabs,
  planTabs,
  projectAlias,
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

test('dashboard tabs: Cockpit · Timeline, Cockpit is the default, the old Overview and Activity open it', () => {
  assert.deepEqual(dashboardTabs.map((t) => t.label), ['Cockpit', 'Timeline'])
  assert.equal(dashboardTabFromRoute(undefined), 'cockpit')
  assert.equal(dashboardTabFromRoute(''), 'cockpit')
  assert.equal(dashboardTabFromRoute('timeline'), 'timeline')
  for (const old of ['activity', 'stream', 'overview', 'board']) assert.equal(dashboardTabFromRoute(old), 'cockpit', old)
  assert.equal(dashboardTabFromRoute(['x', 'timeline']), 'timeline')
})

test('dashboardAlias: ?tab=activity, stream and overview go to the Cockpit, other parameters stay', () => {
  for (const old of ['activity', 'stream', 'overview']) assert.deepEqual(dashboardAlias({ tab: old, q: 'x' }), { q: 'x' }, old)
  assert.deepEqual(dashboardAlias({ tab: ['activity', 'timeline'] }), {})
  assert.equal(dashboardAlias({ tab: 'timeline' }), null)
  assert.equal(dashboardAlias({}), null)
  assert.equal(dashboardAlias({ tab: 'constructor' }), null)
})

test('dashboard tabQuery: the Cockpit drops the parameter, Timeline sets it', () => {
  assert.deepEqual(tabQuery({ tab: 'timeline', window: '7d' }, 'cockpit', dashboardTabs), { window: '7d' })
  assert.deepEqual(tabQuery({}, 'timeline', dashboardTabs), { tab: 'timeline' })
})

test('milestone tabs: Cockpit · Issues · Decisions · Metrics · Retrospective, Cockpit is the default', () => {
  assert.deepEqual(milestoneTabs.map((t) => t.label), ['Cockpit', 'Issues', 'Decisions', 'Metrics', 'Retrospective'])
  assert.equal(tabFromQuery('decisions', milestoneTabs), 'decisions')
  assert.deepEqual(tabQuery({}, 'decisions', milestoneTabs), { tab: 'decisions' })
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
  assert.deepEqual(ids({}), ['cockpit', 'issues', 'decisions', 'metrics'])
  assert.deepEqual(ids({ stored: true }), ['cockpit', 'issues', 'decisions', 'metrics'])
  assert.deepEqual(ids({ retrospective: { text: 'done' } }), ['cockpit', 'issues', 'decisions', 'metrics', 'retrospective'])
  assert.deepEqual(ids(null), ['cockpit', 'issues', 'decisions', 'metrics'])
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
  for (const old of ['activity', 'stream', 'needs', 'pilot']) {
    assert.equal(milestoneTabFromRoute(old, milestoneTabsFor({})), 'cockpit', old)
  }
  assert.equal(milestoneTabFromRoute('decisions', milestoneTabsFor({})), 'decisions', 'a tab again')
  assert.equal(milestoneTabFromRoute('board', milestoneTabsFor({})), 'issues')
  assert.equal(milestoneTabFromRoute(['needs', 'issues']), 'cockpit', 'the first value wins')
})

test('milestoneAlias: every removed tab rewrites to its new place, other parameters stay', () => {
  for (const old of ['activity', 'stream', 'needs', 'pilot']) {
    assert.deepEqual(milestoneAlias({ tab: old, q: 'x' }), { q: 'x' }, old)
  }
  assert.equal(milestoneAlias({ tab: 'decisions' }), null, 'a tab again')
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
  assert.deepEqual(milestoneAlias({ tab: 'pilot', pilot: '1', panel: 'queue' }), {})
})

test('tabAlias: the milestone aliases are not project aliases', () => {
  assert.equal(tabAlias('pilot'), 'pilot')
  assert.equal(tabAlias('stream'), 'activity')
})

test('task tabs: Overview is the default and drops the parameter', () => {
  assert.deepEqual(taskTabs.map((t) => t.id), ['overview', 'live', 'logs', 'calls'])
  assert.equal(taskTabFromRoute(undefined, ''), 'overview')
  assert.equal(taskTabFromRoute('', ''), 'overview')
  assert.equal(taskTabFromRoute('details', ''), 'overview', 'the old Details tab opens Overview')
  assert.equal(taskTabFromRoute(['x', 'calls'], ''), 'calls')
  assert.equal(taskTabFromRoute('logs', '', taskTabs.filter((t) => t.id !== 'logs')), 'overview')
  assert.deepEqual(tabQuery({ tab: 'chat', q: 'x' }, 'overview', taskTabs), { q: 'x' })
  assert.deepEqual(tabQuery({}, 'calls', taskTabs), { tab: 'calls' })
})

test('taskTabFromRoute: the removed Chat tab opens Overview, a plan keeps its Chat tab', () => {
  assert.equal(taskTabFromRoute('chat', ''), 'overview')
  assert.equal(taskTabFromRoute(['chat', 'calls'], ''), 'overview', 'the first value wins, as for other tabs')
  assert.equal(taskTabFromRoute('chat', '#live-transcript'), 'overview')
  assert.equal(tabAlias('chat'), 'chat', 'the alias is task-only')
  assert.equal(tabFromQuery('chat', planTabs), 'chat')
  assert.equal(tabFromQuery('chat', [...planTabs].reverse()), 'chat')
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

test('project tabs: Cockpit · Milestones · Decisions · Metrics, Plans only with the plans feature', () => {
  assert.deepEqual(projectTabsFor(false).map((t) => t.label), ['Cockpit', 'Milestones', 'Decisions', 'Metrics'])
  assert.deepEqual(projectTabsFor(true).map((t) => t.label), ['Cockpit', 'Milestones', 'Decisions', 'Metrics', 'Plans'])
  assert.deepEqual(projectTabsFor(true).map((t) => t.id), ['cockpit', 'milestones', 'decisions', 'metrics', 'plans'])
  assert.equal(tabFromQuery('decisions', projectTabsFor(false)), 'decisions')
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
  for (const old of ['activity', 'stream']) assert.equal(projectTabFromRoute(old, tabs), 'cockpit', old)
  assert.equal(projectTabFromRoute('decisions', tabs), 'decisions', 'a tab again')
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
  assert.deepEqual(projectAlias({ tab: 'board' }), { tab: 'milestones', view: 'board' })
  assert.deepEqual(projectAlias({ tab: ['activity', 'board'] }), {})
})

// The panel's old Decisions view is the Decisions tab again (R6 of "Desk:
// the Cockpit newest first by issue", D817-P2); Stream.vue keeps the hash.
test('?panel=decisions opens the Decisions tab, other parameters stay', () => {
  for (const alias of [milestoneAlias, projectAlias]) {
    assert.deepEqual(alias({ panel: 'decisions' }), { tab: 'decisions' })
    assert.deepEqual(alias({ panel: ['decisions', 'queue'] }), { tab: 'decisions' }, 'the first value wins')
    assert.deepEqual(alias({ milestone: '14', panel: 'decisions', q: 'cache', state: 'active' }), {
      milestone: '14',
      q: 'cache',
      state: 'active',
      tab: 'decisions',
    })
    assert.deepEqual(alias({ tab: 'issues', panel: 'decisions' }), { tab: 'decisions' }, '?tab= gives way')
    assert.equal(alias({ tab: 'decisions' }), null)
  }
  assert.equal(projectAlias({ panel: 'queue' }), null, "the project panel's Queue")
  assert.deepEqual(milestoneAlias({ panel: 'decisions', pilot: '1' }), { tab: 'decisions' }, 'the pilot drawer goes too')
})

test('projectAlias: current links need no change', () => {
  assert.equal(projectAlias({}), null)
  assert.equal(projectAlias({ tab: 'milestones', view: 'board' }), null)
  assert.equal(projectAlias({ tab: 'metrics', q: 'x' }), null)
  assert.equal(projectAlias({ tab: 'pilot' }), null)
  assert.equal(projectAlias({ pilot: '1' }), null)
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
  assert.equal(tabFromQuery('spend', dashboardTabs), 'cockpit')
  assert.equal(taskTabFromRoute('spend', ''), 'overview')
  // New links use the new id.
  assert.deepEqual(tabQuery({ tab: 'spend', q: 'x' }, 'metrics', milestoneTabs), { q: 'x', tab: 'metrics' })
})

// The milestone panel's old Issues and Queue views are the Now & next
// sidebar (R7 of "Desk: the Cockpit newest first by issue", D820-P2): the
// alias drops ?panel=, Stream.vue opens the sidebar.
test('milestoneAlias: ?panel=issues and ?panel=queue go, other parameters stay', () => {
  assert.deepEqual(milestoneAlias({ panel: 'issues' }), {})
  assert.deepEqual(milestoneAlias({ panel: 'queue' }), {})
  assert.deepEqual(milestoneAlias({ panel: ['queue', 'decisions'] }), {}, 'the first value wins')
  assert.deepEqual(milestoneAlias({ tab: 'metrics', panel: 'queue', q: 'x' }), { tab: 'metrics', q: 'x' })
  assert.deepEqual(milestoneAlias({ tab: 'board', panel: 'issues' }), { tab: 'issues', view: 'board' }, 'with a removed tab')
  assert.deepEqual(milestoneAlias({ panel: 'issues', pilot: '1' }), {}, 'the pilot drawer goes too')
  assert.equal(milestoneAlias({ panel: 'nope' }), null)
})
