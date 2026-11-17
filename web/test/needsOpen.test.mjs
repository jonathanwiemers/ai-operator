// Tests of src/needsOpen.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { needsLabel, needsOpen } from '../src/needsOpen.ts'

const prompt = (id, kind, state) => ({ id, kind, question: `${id}?`, options: [], ...(state ? { state } : {}) })

test('needsOpen: one open assessment prompt counts 1, closed ones go to Handled', () => {
  const n = needsOpen({ iid: 3, prompts: [prompt('Q2', 'assessment'), prompt('Q1', 'mainHealth', 'answered')] })
  assert.deepEqual(n.prompts.map((p) => p.id), ['Q2'])
  assert.deepEqual(n.closed.map((p) => p.id), ['Q1'])
  assert.equal(n.count, 1)
  assert.equal(needsLabel(n.count), 'Needs you · 1')
})

test('needsOpen: the pause card counts once and its own prompt is left out', () => {
  const n = needsOpen({
    iid: 3,
    autopilot: { phase: 'Paused', pauseKind: 'mainRed', holds: [{ iid: 7 }, { iid: 8 }] },
    prompts: [prompt('Q5', 'mainHealth'), prompt('Q4', 'assessment')],
    pilot: { proposals: [{ id: 2, summary: 'retry #7' }] },
  })
  assert.equal(n.paused, true)
  assert.deepEqual(n.prompts.map((p) => p.id), ['Q4'])
  assert.equal(n.holds.length, 2)
  assert.equal(n.proposals.length, 1)
  assert.equal(n.count, 5)
})

test('needsOpen: a stored milestone has nothing open', () => {
  const n = needsOpen({ iid: 3, stored: true, autopilot: { phase: 'Paused' }, prompts: [prompt('Q2', 'assessment')] })
  assert.equal(n.count, 0)
  assert.equal(n.paused, false)
  assert.deepEqual(n.prompts, [])
  assert.equal(needsLabel(0), 'Needs you')
})
