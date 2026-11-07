// Tests of src/streamTabs.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { dashboardTabs, streamTabs, tabFromQuery, tabQuery } from '../src/streamTabs.ts'

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

test('streamTabs: Stream comes first', () => {
  assert.equal(streamTabs[0].id, 'stream')
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
