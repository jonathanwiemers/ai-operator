// Tests of src/projectQueue.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  confirmText,
  inOrder,
  menuMoves,
  moveCommand,
  moved,
  needsConfirm,
  needsEstimate,
  queueActions,
  queueStateLabel,
  queueTone,
  reports,
} from '../src/projectQueue.ts'

const entry = (over = {}) => ({ state: 'running', done: 0, costUSD: '0.00', tokens: 0, ...over })

test('needsEstimate: waiting/holding with no done and no spend', () => {
  assert.equal(needsEstimate(entry({ state: 'waiting' })), true)
  assert.equal(needsEstimate(entry({ state: 'holding' })), true)
  assert.equal(needsEstimate(entry({ state: 'waiting', costUSD: '' })), true)
})

test('needsEstimate: running, done, or spent do not', () => {
  assert.equal(needsEstimate(entry({ state: 'running' })), false)
  assert.equal(needsEstimate(entry({ state: 'paused' })), false)
  assert.equal(needsEstimate(entry({ state: 'budget' })), false)
  assert.equal(needsEstimate(entry({ state: 'waiting', done: 1 })), false)
  assert.equal(needsEstimate(entry({ state: 'holding', costUSD: '1.25' })), false)
  assert.equal(needsEstimate(entry({ state: 'waiting', tokens: 100 })), false)
})

test('queueStateLabel: one label per state', () => {
  assert.equal(queueStateLabel(entry({ state: 'running' })), 'Running')
  assert.equal(queueStateLabel(entry({ state: 'holding', holdingFor: 3 })), 'Holding for %3')
  assert.equal(queueStateLabel(entry({ state: 'waiting', holdingFor: 3 })), 'Waiting to plan')
  assert.equal(queueStateLabel(entry({ state: 'paused', pausedReason: 'CI is red' })), 'Paused')
  assert.equal(queueStateLabel(entry({ state: 'paused' })), 'Paused')
  assert.equal(queueStateLabel(entry({ state: 'budget' })), 'Budget')
})

test('queueTone: one tone per state', () => {
  assert.equal(queueTone(entry({ state: 'running' })), 'progress')
  assert.equal(queueTone(entry({ state: 'holding' })), 'muted')
  assert.equal(queueTone(entry({ state: 'waiting' })), 'muted')
  assert.equal(queueTone(entry({ state: 'paused' })), 'warning')
  assert.equal(queueTone(entry({ state: 'budget' })), 'danger')
})

test('queueActions: Pause, or Resume when paused or over budget; no Run next', () => {
  const pause = [{ id: 'pause', label: 'Pause', cmd: { cmd: 'pause' } }]
  const resume = [{ id: 'resume', label: 'Resume', cmd: { cmd: 'resume' } }]
  assert.deepEqual(queueActions(entry({ state: 'running' })), pause)
  assert.deepEqual(queueActions(entry({ state: 'holding', holdingFor: 5 })), pause)
  assert.deepEqual(queueActions(entry({ state: 'waiting' })), pause)
  assert.deepEqual(queueActions(entry({ state: 'paused', pausedReason: 'CI is red' })), resume)
  assert.deepEqual(queueActions(entry({ state: 'budget' })), resume)
})

// %5, %6, %7 and %8 in run order.
const four = [5, 6, 7, 8].map((iid) => ({ iid, state: 'waiting' }))

test('moveCommand: the first place is top, the last bottom', () => {
  assert.deepEqual(moveCommand(four, 2, 0), { cmd: 'move', to: 'top' })
  assert.deepEqual(moveCommand(four, 1, 3), { cmd: 'move', to: 'bottom' })
})

test('moveCommand: up goes before the entry now below, down after the one now above', () => {
  assert.deepEqual(moveCommand(four, 2, 1), { cmd: 'move', before: 6 })
  assert.deepEqual(moveCommand(four, 3, 1), { cmd: 'move', before: 6 })
  assert.deepEqual(moveCommand(four, 0, 1), { cmd: 'move', after: 6 })
  assert.deepEqual(moveCommand(four, 0, 2), { cmd: 'move', after: 7 })
  // Three entries: %7 dragged above %6 (the acceptance case).
  assert.deepEqual(moveCommand(four.slice(0, 3), 2, 1), { cmd: 'move', before: 6 })
})

test('moveCommand: no move, or out of range, is null', () => {
  assert.equal(moveCommand(four, 1, 1), null)
  assert.equal(moveCommand(four, -1, 0), null)
  assert.equal(moveCommand(four, 1, 4), null)
  assert.equal(moveCommand([], 0, 0), null)
})

test('moved: takes the entry out and puts it in at the new index', () => {
  assert.deepEqual(moved([5, 6, 7], 2, 1), [5, 7, 6])
  assert.deepEqual(moved([5, 6, 7], 0, 2), [6, 7, 5])
  assert.deepEqual(moved([5, 6, 7], 1, 1), [5, 6, 7])
})

test('menuMoves: none that do nothing at the ends', () => {
  const ids = (i, q = four) => menuMoves(q, i).map((m) => `${m.id}:${m.to}`)
  assert.deepEqual(ids(0), ['down:1'])
  assert.deepEqual(ids(1), ['top:0', 'up:0', 'down:2'])
  assert.deepEqual(ids(2), ['top:0', 'up:1', 'down:3'])
  assert.deepEqual(ids(3), ['top:0', 'up:2'])
  assert.deepEqual(ids(0, [four[0]]), [])
  assert.deepEqual(ids(4), [])
  assert.deepEqual(menuMoves(four, 2).map((m) => m.label), ['Move to top', 'Move up', 'Move down'])
})

test('menuMoves: Move up sends the same command as dragging one place up', () => {
  const up = menuMoves(four.slice(0, 3), 2).find((m) => m.id === 'up')
  assert.deepEqual(moveCommand(four.slice(0, 3), 2, up.to), { cmd: 'move', before: 6 })
})

const running = [entry({ iid: 5, state: 'running' }), entry({ iid: 6, state: 'waiting' }), entry({ iid: 7, state: 'paused' })]

test('needsConfirm: only a move above the running entry', () => {
  assert.equal(needsConfirm(running, 2, 0), true)
  assert.equal(needsConfirm(running, 1, 0), true)
  assert.equal(needsConfirm(running, 2, 1), false)
  assert.equal(needsConfirm(running, 0, 2), false)
  assert.equal(needsConfirm(running, 1, 2), false)
  assert.equal(needsConfirm(running, 1, 1), false)
  assert.equal(needsConfirm(four, 3, 0), false)
})

test('confirmText: names the moved and the running milestone', () => {
  const q = [entry({ iid: 3, state: 'running' }), entry({ iid: 4, state: 'waiting' }), entry({ iid: 5, state: 'waiting' })]
  assert.equal(
    confirmText(q, 2, 0),
    "Move %5 above %3? %3's running issues finish, then %5 runs. %3 starts no new issues until %5 is done.",
  )
  assert.equal(confirmText(q, 2, 1), '')
  assert.equal(confirmText(q, 0, 2), '')
})

test('inOrder: the pending order first, unnamed entries after in their order', () => {
  const q = [5, 6, 7, 8].map((iid) => ({ iid }))
  assert.deepEqual(inOrder(q, [5, 7, 6]).map((e) => e.iid), [5, 7, 6, 8])
  assert.deepEqual(inOrder(q, [8, 5, 6, 7]).map((e) => e.iid), [8, 5, 6, 7])
  assert.deepEqual(inOrder(q, []).map((e) => e.iid), [5, 6, 7, 8])
})

test('reports: the queue shows the pending order, others may join or leave', () => {
  const q = (...iids) => iids.map((iid) => ({ iid }))
  assert.equal(reports(q(5, 7, 6), [5, 7, 6]), true)
  assert.equal(reports(q(5, 7, 6, 9), [5, 7, 6]), true)
  assert.equal(reports(q(7, 6), [5, 7, 6]), true)
  assert.equal(reports(q(5, 6, 7), [5, 7, 6]), false)
})
