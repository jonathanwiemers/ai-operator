// Tests of src/milestoneQueue.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  canRevisit,
  laneGroups,
  queueDone,
  queueRows,
  queueTone,
  resumeCommand,
  revisitCommand,
  revisitPromptID,
  waitingForTurn,
  waitingLabel,
} from '../src/milestoneQueue.ts'

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

test('revisitCommand: names the decision', () => {
  assert.deepEqual(revisitCommand(' d12-3 '), { cmd: 'revisit', decision: 'D12-3' })
})

test('canRevisit: owners on live decisions only', () => {
  const owner = { chat: true, merge: true }
  assert.equal(canRevisit({ id: 'D1-1' }, owner), true)
  assert.equal(canRevisit({ id: 'D1-1', superseded: true }, owner), false)
  assert.equal(canRevisit({ id: 'D1-1' }, { chat: true, merge: false }), false)
  assert.equal(canRevisit({ id: 'D1-1' }, { chat: false, merge: true }), false)
  assert.equal(canRevisit({ id: 'D1-1' }, undefined), false)
})

test('revisitPromptID: finds the prompt in the answer', () => {
  assert.equal(revisitPromptID('Q7 opened for D12-3'), 'Q7')
  assert.equal(revisitPromptID('nothing here'), '')
})

test('waitingForTurn: names the older milestones, nothing when not waiting', () => {
  assert.equal(waitingForTurn([7, 9]), 'Waiting for its turn: %7, %9 go first')
  assert.equal(waitingForTurn([]), '')
  assert.equal(waitingForTurn(undefined), '')
})

test('laneGroups: lanes in order, queue order within, ready by readyAt, Running always', () => {
  // Two running, one landing, one held, one waiting, two ready and one done.
  const items = [
    { iid: 1, state: 'Merged', lane: 'done' },
    { iid: 2, state: 'Active', lane: 'running' },
    { iid: 3, state: 'Active', lane: 'ready', readyAt: '2026-11-16T10:05:00Z' },
    { iid: 4, state: 'Active', lane: 'landing' },
    { iid: 5, state: 'Active', lane: 'held', hold: { kind: 'ciRed', reason: 'CI is red', since: '2026-11-16T09:00:00Z' } },
    { iid: 6, state: 'Active', lane: 'running' },
    { iid: 7, state: 'Active', lane: 'ready', readyAt: '2026-11-16T10:01:00Z' },
    { iid: 8, state: 'Pending', lane: 'waiting', waitingFor: [5] },
  ]
  const groups = laneGroups(items, 2, 3)
  assert.deepEqual(
    groups.map((g) => [g.label, g.items.map((i) => i.iid)]),
    [
      ['Running (2 of 3)', [2, 6]],
      ['Landing', [4]],
      ['Ready to land', [7, 3]],
      ['Waiting', [8]],
      ['Held', [5]],
      ['Done', [1]],
    ],
  )
  assert.deepEqual(laneGroups([], 0, 3).map((g) => g.label), ['Running (0 of 3)'])
  assert.deepEqual(laneGroups([], 1).map((g) => g.label), ['Running (1)'])
  // Unknown ready time last; an unknown lane goes by state.
  assert.deepEqual(
    laneGroups(
      [
        { iid: 1, state: 'Active', lane: 'ready' },
        { iid: 2, state: 'Active', lane: 'ready', readyAt: '2026-11-16T10:00:00Z' },
        { iid: 3, state: 'Skipped' },
        { iid: 4, state: 'Pending' },
      ],
      0,
      3,
    ).map((g) => [g.lane, g.items.map((i) => i.iid)]),
    [
      ['running', []],
      ['ready', [2, 1]],
      ['waiting', [4]],
      ['done', [3]],
    ],
  )
})

test('waitingLabel and resumeCommand', () => {
  assert.equal(waitingLabel([1, 2]), 'after #1, #2')
  assert.equal(waitingLabel([]), 'for a free slot')
  assert.equal(waitingLabel(undefined), 'for a free slot')
  assert.deepEqual(resumeCommand(12), { cmd: 'resume', issue: 12 })
})
