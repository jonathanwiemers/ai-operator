// Tests of src/streamTabs.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  dashboardTabs,
  milestoneTabFromRoute,
  milestoneTabs,
  milestoneTabsFor,
  panelFromQuery,
  panelQuery,
  panelViews,
  pilotFromQuery,
  pilotQuery,
  planPaneTab,
  planPaneTabs,
  planTabs,
  projectTabsFor,
  streamBaseTabs,
  storedMilestoneTabs,
  tabAlias,
  tabFromQuery,
  tabQuery,
  taskTabFromRoute,
  taskTabs,
} from '../src/streamTabs.ts'

test('tabFromQuery: activity is the default', () => {
  assert.equal(tabFromQuery(undefined), 'activity')
  assert.equal(tabFromQuery(null), 'activity')
  assert.equal(tabFromQuery(''), 'activity')
  assert.equal(tabFromQuery('activity'), 'activity')
})

test('tabFromQuery: known tabs are kept', () => {
  assert.equal(tabFromQuery('board'), 'board')
  assert.equal(tabFromQuery('metrics'), 'metrics')
})

test('tabFromQuery: unknown tabs fall back to activity', () => {
  assert.equal(tabFromQuery('cost'), 'activity')
  assert.equal(tabFromQuery('Board'), 'activity')
})

test('tabFromQuery: repeated values take the first known one', () => {
  assert.equal(tabFromQuery(['x', 'board']), 'board')
  assert.equal(tabFromQuery([null, 'x']), 'activity')
})

test('tabQuery: activity drops the parameter, other tabs set it', () => {
  assert.deepEqual(tabQuery({ tab: 'board', q: 'x' }, 'activity'), { q: 'x' })
  assert.deepEqual(tabQuery({ q: 'x' }, 'board'), { q: 'x', tab: 'board' })
  assert.deepEqual(tabQuery({}, 'activity'), {})
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

test('milestone tabs: Cockpit is the default', () => {
  assert.deepEqual(milestoneTabs.map((t) => t.id), ['cockpit', 'issues', 'activity', 'decisions', 'board', 'metrics', 'needs', 'retrospective'])
  assert.equal(tabFromQuery(undefined, milestoneTabs), 'cockpit')
  assert.equal(tabFromQuery('', milestoneTabs), 'cockpit')
  assert.equal(tabFromQuery('cockpit', milestoneTabs), 'cockpit')
  assert.equal(tabFromQuery('issues', milestoneTabs), 'issues')
  assert.equal(milestoneTabsFor(null)[0].id, 'cockpit')
  assert.equal(milestoneTabsFor({ stored: true })[0].id, 'cockpit')
})

test('milestone tabs: old ?tab=stream links open Activity', () => {
  assert.equal(tabFromQuery('stream', milestoneTabs), 'activity')
  assert.equal(tabFromQuery('board', milestoneTabs), 'board')
  assert.equal(tabFromQuery('metrics', milestoneTabs), 'metrics')
})

test('milestone tabs: unknown values fall back to Cockpit', () => {
  assert.equal(tabFromQuery('retro', milestoneTabs), 'cockpit')
  assert.equal(tabFromQuery('Stream', milestoneTabs), 'cockpit')
  assert.equal(tabFromQuery(['x', 'metrics'], milestoneTabs), 'metrics')
})

test('project tabs: issues and cockpit are unknown there', () => {
  assert.equal(tabFromQuery('issues', projectTabsFor(true)), 'milestones')
  assert.equal(tabFromQuery('cockpit', projectTabsFor(true)), 'milestones')
})

test('milestone tabQuery: Cockpit drops the parameter, Issues and Activity set it', () => {
  assert.deepEqual(tabQuery({ tab: 'board', q: 'x' }, 'cockpit', milestoneTabs), { q: 'x' })
  assert.deepEqual(tabQuery({}, 'issues', milestoneTabs), { tab: 'issues' })
  assert.deepEqual(tabQuery({}, 'activity', milestoneTabs), { tab: 'activity' })
})

test('stored milestone tabs: Cockpit, no Board, a board link falls back to Cockpit', () => {
  assert.deepEqual(storedMilestoneTabs.map((t) => t.id), ['cockpit', 'issues', 'activity', 'decisions', 'metrics', 'needs', 'retrospective'])
  assert.equal(tabFromQuery('board', storedMilestoneTabs), 'cockpit')
})

test('milestone tabs: ?tab=decisions opens Decisions on live and stored milestones, also with a #<id> hash', () => {
  assert.equal(milestoneTabFromRoute('decisions', '', milestoneTabs), 'decisions')
  assert.equal(milestoneTabFromRoute('decisions', '#D42-3', storedMilestoneTabs), 'decisions')
  assert.deepEqual(tabQuery({ q: 'x' }, 'decisions', milestoneTabs), { q: 'x', tab: 'decisions' })
})

test('milestone tabs: an old ?tab=pilot link opens the Cockpit (the drawer opens over it)', () => {
  assert.equal(tabAlias('pilot'), 'cockpit')
  assert.equal(tabFromQuery('pilot', milestoneTabs), 'cockpit')
  assert.equal(milestoneTabFromRoute('pilot', '', milestoneTabsFor({})), 'cockpit')
  assert.equal(milestoneTabFromRoute('pilot', '#prompt-Q3'), 'cockpit')
})

test('pilotFromQuery and pilotQuery: ?pilot=1 is the drawer, ?tab=pilot becomes it', () => {
  assert.equal(pilotFromQuery({}), false)
  assert.equal(pilotFromQuery({ pilot: '1' }), true)
  assert.equal(pilotFromQuery({ pilot: '0' }), false)
  assert.equal(pilotFromQuery({ pilot: ['x', '1'] }), true)
  assert.equal(pilotFromQuery({ tab: 'pilot' }), false, 'Stream.vue replaces it first')
  assert.deepEqual(pilotQuery({ tab: 'metrics', q: 'x' }, true), { tab: 'metrics', q: 'x', pilot: '1' })
  assert.deepEqual(pilotQuery({ tab: 'metrics', pilot: '1' }, false), { tab: 'metrics' })
  assert.deepEqual(pilotQuery({ tab: 'pilot', q: 'x' }, true), { q: 'x', pilot: '1' })
  assert.deepEqual(pilotQuery({ tab: 'pilot' }, false), {})
})

test('milestoneTabFromRoute: a #prompt-Q<n> or #problem-<iid> hash without ?tab= opens the Cockpit', () => {
  assert.equal(milestoneTabFromRoute(undefined, '#prompt-Q4'), 'cockpit')
  assert.equal(milestoneTabFromRoute(undefined, '#problem-12'), 'cockpit')
  assert.equal(milestoneTabFromRoute('', '#prompt-Q4'), 'cockpit')
  assert.equal(milestoneTabFromRoute(null, '#prompt-Q12', storedMilestoneTabs), 'cockpit')
  // Needs you is not among the shown tabs: the hash still opens the Cockpit.
  assert.equal(milestoneTabFromRoute(undefined, '#problem-12', milestoneTabsFor({}, { needs: 0 })), 'cockpit')
  // ?tab=needs with a hash still opens Needs you while it shows.
  assert.equal(milestoneTabFromRoute('needs', '#problem-12'), 'needs')
})

test('milestoneTabFromRoute: ?tab= wins over the hash, other hashes keep the default', () => {
  assert.equal(milestoneTabFromRoute('stream', '#prompt-Q4'), 'activity')
  assert.equal(milestoneTabFromRoute(undefined, ''), 'cockpit')
  assert.equal(milestoneTabFromRoute(undefined, '#live-transcript'), 'cockpit')
  assert.equal(milestoneTabFromRoute('issues', '#problem-12'), 'issues')
  assert.equal(milestoneTabFromRoute('board', ''), 'board')
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

test('milestone tabs: Cockpit · Issues · Activity · Decisions · Board · Metrics, then Needs you and Retrospective, also when stored', () => {
  assert.deepEqual(milestoneTabs.map((t) => t.label), ['Cockpit', 'Issues', 'Activity', 'Decisions', 'Board', 'Metrics', 'Needs you', 'Retrospective'])
  assert.deepEqual(storedMilestoneTabs.map((t) => t.label), ['Cockpit', 'Issues', 'Activity', 'Decisions', 'Metrics', 'Needs you', 'Retrospective'])
})

test('project tabs: Milestones · Activity · Decisions · Board · Metrics, Plans only with the plans feature', () => {
  assert.deepEqual(projectTabsFor(false).map((t) => t.label), ['Milestones', 'Activity', 'Decisions', 'Board', 'Metrics'])
  assert.deepEqual(projectTabsFor(true).map((t) => t.label), ['Milestones', 'Activity', 'Decisions', 'Board', 'Metrics', 'Plans'])
  assert.deepEqual(projectTabsFor(true).map((t) => t.id), ['milestones', 'activity', 'decisions', 'board', 'metrics', 'plans'])
  assert.equal(tabFromQuery('decisions', projectTabsFor(false)), 'decisions')
  assert.deepEqual(tabQuery({ q: 'x' }, 'decisions', projectTabsFor(false)), { q: 'x', tab: 'decisions' })
  assert.deepEqual(tabQuery({ tab: 'decisions', q: 'x' }, 'milestones', projectTabsFor(true)), { q: 'x' })
  assert.equal(tabFromQuery('plans', projectTabsFor(true)), 'plans')
  assert.deepEqual(tabQuery({}, 'plans', projectTabsFor(true)), { tab: 'plans' })
})

test('project tabs: Milestones is the default and drops ?tab=, Activity sets it, ?tab=stream opens Activity', () => {
  for (const plans of [false, true]) {
    const tabs = projectTabsFor(plans)
    assert.equal(tabFromQuery(undefined, tabs), 'milestones')
    assert.equal(tabFromQuery('', tabs), 'milestones')
    assert.equal(tabFromQuery('milestones', tabs), 'milestones')
    assert.equal(tabFromQuery('activity', tabs), 'activity')
    assert.equal(tabFromQuery('stream', tabs), 'activity')
    assert.equal(tabFromQuery('spend', tabs), 'metrics')
    assert.deepEqual(tabQuery({ tab: 'activity', q: 'x' }, 'milestones', tabs), { q: 'x' })
    assert.deepEqual(tabQuery({}, 'activity', tabs), { tab: 'activity' })
  }
  assert.equal(tabAlias('stream'), 'activity')
  assert.equal(tabFromQuery('stream'), 'activity')
})

test('milestone tabs have no Plans', () => {
  assert.equal(tabFromQuery('plans', milestoneTabs), 'cockpit')
})

test('tabFromQuery: ?tab=plans without the feature falls back to the first tab', () => {
  assert.equal(tabFromQuery('plans', projectTabsFor(false)), 'milestones')
  assert.equal(tabFromQuery('plans'), 'activity')
})

test('Metrics tab: the Spend tab renamed, ?tab=spend still opens it', () => {
  assert.deepEqual(streamBaseTabs.at(-1), { id: 'metrics', label: 'Metrics' })
  assert.equal(tabAlias('spend'), 'metrics')
  assert.equal(tabAlias('metrics'), 'metrics')
  assert.equal(tabAlias('board'), 'board')
  assert.equal(tabAlias('constructor'), 'constructor')
  assert.equal(tabAlias(null), null)
  assert.equal(tabFromQuery('spend'), 'metrics')
  assert.equal(tabFromQuery('spend', milestoneTabs), 'metrics')
  assert.equal(tabFromQuery('spend', storedMilestoneTabs), 'metrics')
  assert.equal(tabFromQuery(['x', 'spend'], milestoneTabs), 'metrics')
  assert.equal(milestoneTabFromRoute('spend', '', milestoneTabs), 'metrics')
  // Lists without the tab ignore the alias.
  assert.equal(tabFromQuery('spend', dashboardTabs), 'overview')
  assert.equal(taskTabFromRoute('spend', ''), 'overview')
  // New links use the new id.
  assert.deepEqual(tabQuery({ tab: 'spend', q: 'x' }, 'metrics', milestoneTabs), { q: 'x', tab: 'metrics' })
})

test('milestoneTabsFor: Needs you only while something needs you, Retrospective once one exists, no Board once stored', () => {
  const ids = (m, opts) => milestoneTabsFor(m, opts).map((t) => t.id)
  assert.deepEqual(ids({}), ['cockpit', 'issues', 'activity', 'decisions', 'board', 'metrics'], 'running, nothing open')
  assert.deepEqual(ids({}, { needs: 0 }), ['cockpit', 'issues', 'activity', 'decisions', 'board', 'metrics'])
  assert.deepEqual(ids({}, { needs: 2 }), ['cockpit', 'issues', 'activity', 'decisions', 'board', 'metrics', 'needs'], 'paused')
  assert.deepEqual(ids({}, { problem: true }), ['cockpit', 'issues', 'activity', 'decisions', 'board', 'metrics', 'needs'], 'problem route')
  assert.deepEqual(ids({ retrospective: { text: 'done' } }), ['cockpit', 'issues', 'activity', 'decisions', 'board', 'metrics', 'retrospective'])
  assert.deepEqual(ids({ retrospective: {} }, { needs: 1 }), ['cockpit', 'issues', 'activity', 'decisions', 'board', 'metrics', 'needs', 'retrospective'])
  assert.deepEqual(ids({ stored: true }), ['cockpit', 'issues', 'activity', 'decisions', 'metrics'])
  assert.deepEqual(ids({ stored: true, retrospective: { text: 'done' } }), ['cockpit', 'issues', 'activity', 'decisions', 'metrics', 'retrospective'])
  assert.deepEqual(ids(null), ['cockpit', 'issues', 'activity', 'decisions', 'board', 'metrics'], 'hidden while the milestone loads')
  assert.deepEqual(ids(null, { needs: 3 }), ['cockpit', 'issues', 'activity', 'decisions', 'board', 'metrics'], 'no count before it loads')
  assert.deepEqual(milestoneTabsFor({}, { needs: 1 }).find((t) => t.id === 'needs').label, 'Needs you', 'the count is a badge')
})

test('milestoneTabsFor: a link to a hidden tab opens the Cockpit once loaded, and waits while loading', () => {
  assert.equal(milestoneTabFromRoute('retrospective', '', milestoneTabsFor({})), 'cockpit')
  assert.equal(milestoneTabFromRoute('retrospective', '', milestoneTabsFor({ retrospective: {} })), 'retrospective')
  assert.equal(milestoneTabFromRoute('needs', '', milestoneTabsFor({})), 'cockpit', 'nothing open')
  assert.equal(milestoneTabFromRoute('needs', '', milestoneTabsFor({}, { needs: 1 })), 'needs')
  assert.equal(milestoneTabFromRoute('needs', ''), 'needs', 'read against all tabs while loading')
  assert.equal(milestoneTabFromRoute(undefined, '#prompt-Q3', milestoneTabsFor({})), 'cockpit')
  assert.equal(milestoneTabFromRoute(undefined, '#prompt-Q3', milestoneTabsFor({}, { needs: 1 })), 'cockpit')
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
