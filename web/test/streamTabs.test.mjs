// Tests of src/streamTabs.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { streamTabs, tabFromQuery, tabQuery } from '../src/streamTabs.ts'

test('tabFromQuery: stream is the default', () => {
  assert.equal(tabFromQuery(undefined), 'stream')
  assert.equal(tabFromQuery(null), 'stream')
  assert.equal(tabFromQuery(''), 'stream')
  assert.equal(tabFromQuery('stream'), 'stream')
})

test('tabFromQuery: known tabs are kept', () => {
  assert.equal(tabFromQuery('board'), 'board')
})

test('tabFromQuery: unknown tabs fall back to stream', () => {
  assert.equal(tabFromQuery('spend'), 'stream')
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
