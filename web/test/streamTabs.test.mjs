// Tests of src/streamTabs.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  carryTab,
  dashboardTabs,
  milestoneTabFromRoute,
  milestoneTabs,
  projectTabs,
  storedMilestoneTabs,
  tabFromQuery,
  tabQuery,
  taskTabFromRoute,
  taskTabs,
} from '../src/streamTabs.ts'

test('tabFromQuery: stream is the default', () => {
  assert.equal(tabFromQuery(undefined), 'stream')
  assert.equal(tabFromQuery(null), 'stream')
  assert.equal(tabFromQuery(''), 'stream')
  assert.equal(tabFromQuery('stream'), 'stream')
})

test('tabFromQuery: known tabs are kept', () => {
  assert.equal(tabFromQuery('board'), 'board')
  assert.equal(tabFromQuery('spend'), 'spend')
})

test('tabFromQuery: unknown tabs fall back to stream', () => {
  assert.equal(tabFromQuery('cost'), 'stream')
  assert.equal(tabFromQuery('Board'), 'stream')
})

test('tabFromQuery: repeated values take the first known one', () => {
  assert.equal(tabFromQuery(['x', 'board']), 'board')
  assert.equal(tabFromQuery([null, 'x']), 'stream')
})

test('tabQuery: stream drops the parameter, other tabs set it', () => {
  assert.deepEqual(tabQuery({ tab: 'board', q: 'x' }, 'stream'), { q: 'x' })
  assert.deepEqual(tabQuery({ q: 'x' }, 'board'), { q: 'x', tab: 'board' })
  assert.deepEqual(tabQuery({}, 'stream'), {})
})

test('projectTabs: Stream comes first', () => {
  assert.equal(projectTabs[0].id, 'stream')
})

test('dashboard tabs: Overview is the default, Stream by ?tab=stream', () => {
  assert.equal(dashboardTabs[0].id, 'overview')
  assert.equal(tabFromQuery(undefined, dashboardTabs), 'overview')
  assert.equal(tabFromQuery('', dashboardTabs), 'overview')
  assert.equal(tabFromQuery('stream', dashboardTabs), 'stream')
  assert.equal(tabFromQuery('board', dashboardTabs), 'overview')
  assert.equal(tabFromQuery(['x', 'stream'], dashboardTabs), 'stream')
})

test('dashboard tabQuery: overview drops the parameter, stream sets it', () => {
  assert.deepEqual(tabQuery({ tab: 'stream', q: 'x' }, 'overview', dashboardTabs), { q: 'x' })
  assert.deepEqual(tabQuery({}, 'stream', dashboardTabs), { tab: 'stream' })
})

test('milestone tabs: Issues is the default', () => {
  assert.deepEqual(milestoneTabs.map((t) => t.id), ['issues', 'pilot', 'retrospective', 'stream', 'board', 'spend'])
  assert.equal(tabFromQuery(undefined, milestoneTabs), 'issues')
  assert.equal(tabFromQuery('', milestoneTabs), 'issues')
  assert.equal(tabFromQuery('issues', milestoneTabs), 'issues')
})

test('milestone tabs: old ?tab=stream links open the stream', () => {
  assert.equal(tabFromQuery('stream', milestoneTabs), 'stream')
  assert.equal(tabFromQuery('board', milestoneTabs), 'board')
  assert.equal(tabFromQuery('spend', milestoneTabs), 'spend')
})

test('milestone tabs: unknown values fall back to Issues', () => {
  assert.equal(tabFromQuery('retro', milestoneTabs), 'issues')
  assert.equal(tabFromQuery('Stream', milestoneTabs), 'issues')
  assert.equal(tabFromQuery(['x', 'spend'], milestoneTabs), 'spend')
})

test('project tabs: issues is unknown there', () => {
  assert.equal(tabFromQuery('issues', projectTabs), 'stream')
})

test('milestone tabQuery: Issues drops the parameter, Stream sets it', () => {
  assert.deepEqual(tabQuery({ tab: 'board', q: 'x' }, 'issues', milestoneTabs), { q: 'x' })
  assert.deepEqual(tabQuery({}, 'stream', milestoneTabs), { tab: 'stream' })
})

test('stored milestone tabs: no Board, a board link falls back to Issues', () => {
  assert.deepEqual(storedMilestoneTabs.map((t) => t.id), ['issues', 'pilot', 'retrospective', 'stream', 'spend'])
  assert.equal(tabFromQuery('board', storedMilestoneTabs), 'issues')
})

test('carryTab: defaults stay defaults, chosen tabs are kept where they exist', () => {
  assert.equal(carryTab('stream', projectTabs, milestoneTabs), 'issues')
  assert.equal(carryTab('board', projectTabs, milestoneTabs), 'board')
  assert.equal(carryTab('issues', milestoneTabs, projectTabs), 'stream')
  assert.equal(carryTab('stream', milestoneTabs, projectTabs), 'stream')
  assert.equal(carryTab('stream', milestoneTabs, milestoneTabs), 'stream')
  assert.equal(carryTab('spend', milestoneTabs, milestoneTabs), 'spend')
})

test('milestone tabs: ?tab=pilot opens the Pilot tab', () => {
  assert.equal(tabFromQuery('pilot', milestoneTabs), 'pilot')
  assert.equal(milestoneTabFromRoute('pilot', '', milestoneTabs), 'pilot')
  assert.deepEqual(tabQuery({}, 'pilot', milestoneTabs), { tab: 'pilot' })
})

test('milestoneTabFromRoute: a #prompt-Q<n> hash without ?tab= opens Pilot', () => {
  assert.equal(milestoneTabFromRoute(undefined, '#prompt-Q4'), 'pilot')
  assert.equal(milestoneTabFromRoute('', '#prompt-Q4'), 'pilot')
  assert.equal(milestoneTabFromRoute(null, '#prompt-Q12', storedMilestoneTabs), 'pilot')
})

test('milestoneTabFromRoute: ?tab= wins over the hash, other hashes keep the default', () => {
  assert.equal(milestoneTabFromRoute('stream', '#prompt-Q4'), 'stream')
  assert.equal(milestoneTabFromRoute(undefined, ''), 'issues')
  assert.equal(milestoneTabFromRoute(undefined, '#live-transcript'), 'issues')
  assert.equal(milestoneTabFromRoute('board', ''), 'board')
})

test('task tabs: Overview is the default and drops the parameter', () => {
  assert.deepEqual(taskTabs.map((t) => t.id), ['overview', 'chat', 'live', 'calls', 'details'])
  assert.equal(taskTabFromRoute(undefined, ''), 'overview')
  assert.equal(taskTabFromRoute('', ''), 'overview')
  assert.equal(taskTabFromRoute('details', ''), 'details')
  assert.equal(taskTabFromRoute(['x', 'calls'], ''), 'calls')
  assert.deepEqual(tabQuery({ tab: 'chat', q: 'x' }, 'overview', taskTabs), { q: 'x' })
  assert.deepEqual(tabQuery({}, 'details', taskTabs), { tab: 'details' })
})

test('taskTabFromRoute: unknown and unavailable tabs open Overview', () => {
  const noLive = taskTabs.filter((t) => t.id !== 'live')
  assert.equal(taskTabFromRoute('board', ''), 'overview')
  assert.equal(taskTabFromRoute('Live', ''), 'overview')
  assert.equal(taskTabFromRoute('live', '', noLive), 'overview')
  assert.equal(taskTabFromRoute('details', '', noLive), 'details')
})

test('taskTabFromRoute: #live-transcript without ?tab= opens Live where there is one', () => {
  const noLive = taskTabs.filter((t) => t.id !== 'live')
  assert.equal(taskTabFromRoute(undefined, '#live-transcript'), 'live')
  assert.equal(taskTabFromRoute('', '#live-transcript'), 'live')
  assert.equal(taskTabFromRoute(undefined, '#live-transcript', noLive), 'overview')
  assert.equal(taskTabFromRoute('details', '#live-transcript'), 'details')
  assert.equal(taskTabFromRoute(undefined, '#prompt-Q4'), 'overview')
})
