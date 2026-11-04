// Tests of src/milestoneQueue.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { queueDone, queueRows, queueTone } from '../src/milestoneQueue.ts'

test('queueRows: joins decisions and revisits by issue, in queue order', () => {
  const items = [
    { iid: 7, title: 'B', state: 'Active' },
    { iid: 5, title: 'A', state: 'Merged', mergeRequestIID: 9 },
  ]
  const status = [
    { iid: 5, state: 'Merged', decisions: [{ id: 'D5-1', title: 'x' }] },
    { iid: 7, state: 'Active', revisitOf: 'D5-1' },
    { iid: 8, state: 'Pending', decisions: [{ id: 'D8-1' }] },
  ]
  assert.deepEqual(queueRows(items, status), [
    { iid: 7, title: 'B', state: 'Active', decisions: [], revisitOf: 'D5-1' },
    { iid: 5, title: 'A', state: 'Merged', mergeRequestIID: 9, decisions: [{ id: 'D5-1', title: 'x' }], revisitOf: undefined },
  ])
})

test('queueRows: without status the rows have no decisions', () => {
  assert.deepEqual(queueRows([{ iid: 1, title: '', state: 'Pending' }]), [
    { iid: 1, title: '', state: 'Pending', decisions: [], revisitOf: undefined },
  ])
  assert.deepEqual(queueRows([]), [])
})

test('queueDone: merged and skipped count as done', () => {
  const s = (state) => ({ state })
  assert.equal(queueDone([s('Merged'), s('Skipped'), s('Active'), s('Pending')]), 2)
  assert.equal(queueDone([]), 0)
})

test('queueTone: one tone per state', () => {
  assert.equal(queueTone('Merged'), 'success')
  assert.equal(queueTone('Active'), 'progress')
  assert.equal(queueTone('Skipped'), 'warning')
  assert.equal(queueTone('Pending'), 'muted')
})
