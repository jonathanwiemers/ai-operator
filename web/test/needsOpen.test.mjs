// Tests of src/needsOpen.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { needsLabel, needsOpen } from '../src/needsOpen.ts'

const prompt = (id, kind, state, issueIID) => ({
  id,
  kind,
  question: `${id}?`,
  options: [],
  ...(state ? { state } : {}),
  ...(issueIID ? { issueIID } : {}),
})

test('needsOpen: one open assessment prompt counts 1, closed ones are not under Open', () => {
  const n = needsOpen({ iid: 3, prompts: [prompt('Q2', 'assessment'), prompt('Q1', 'mainHealth', 'answered')] })
  assert.deepEqual(n.problems.map((p) => [p.iid, p.prompts.map((q) => q.id)]), [[0, ['Q2']]])
  assert.equal(n.count, 1)
  assert.equal(needsLabel(n.count), 'Needs you · 1')
})

test('needsOpen: a pause, a hold and two prompts on one issue are one problem (R11)', () => {
  const n = needsOpen({
    iid: 29,
    issues: [{ iid: 558, title: 'Desk facts' }],
    autopilot: { phase: 'Paused', pauseKind: 'cycle', pauseIssue: 558, holds: [{ iid: 558, kind: 'needsInput' }] },
    prompts: [prompt('Q1', 'assessment', 'open', 558), prompt('Q2', 'assessment', 'open', 558)],
  })
  assert.equal(n.problems.length, 1)
  const [p] = n.problems
  assert.equal(p.iid, 558)
  assert.equal(p.title, 'Desk facts')
  assert.equal(p.paused, true)
  assert.equal(p.hold?.iid, 558)
  assert.deepEqual(p.prompts.map((q) => q.id), ['Q1', 'Q2'])
  assert.equal(n.count, 1)
  assert.equal(needsLabel(n.count), 'Needs you · 1')
})

test('needsOpen: a milestone-wide pause and a held issue are two problems; the pause card keeps its prompt', () => {
  const n = needsOpen({
    iid: 3,
    autopilot: { phase: 'Paused', pauseKind: 'mainRed', holds: [{ iid: 7, kind: 'ciRed' }] },
    prompts: [prompt('Q5', 'mainHealth'), prompt('Q4', 'assessment', 'open', 7)],
  })
  assert.deepEqual(
    n.problems.map((p) => [p.iid, p.paused, p.hold?.iid, p.prompts.map((q) => q.id)]),
    [
      [0, true, undefined, []],
      [7, false, 7, ['Q4']],
    ],
  )
  assert.equal(n.count, 2)
})

test('needsOpen: an older view without pauseIssue puts the pause on pausedFor', () => {
  const n = needsOpen({ iid: 3, autopilot: { phase: 'Paused', pauseKind: 'needsInput', pausedFor: 9 } })
  assert.deepEqual(n.problems.map((p) => [p.iid, p.paused]), [[9, true]])
})

test('needsOpen: proposals are counted beside the problems', () => {
  const n = needsOpen({
    iid: 3,
    autopilot: { phase: 'Running', holds: [{ iid: 7, kind: 'ciRed' }, { iid: 8, kind: 'runFailed' }] },
    prompts: [prompt('Q4', 'assessment', 'open', 8)],
    pilot: { proposals: [{ id: 2, summary: 'retry #7' }], acted: [], interventions: [] },
  })
  assert.deepEqual(n.problems.map((p) => p.iid).sort(), [7, 8])
  assert.equal(n.proposals.length, 1)
  assert.equal(n.count, 3)
})

test('needsOpen: a problem with only handled entries is not under Open', () => {
  const n = needsOpen({ iid: 3, prompts: [prompt('Q1', 'assessment', 'answered', 7)] })
  assert.deepEqual(n.problems, [])
  assert.equal(n.count, 0)
})

test('needsOpen: a stored milestone has nothing open', () => {
  const n = needsOpen({ iid: 3, stored: true, autopilot: { phase: 'Paused' }, prompts: [prompt('Q2', 'assessment')] })
  assert.equal(n.count, 0)
  assert.deepEqual(n.problems, [])
  assert.equal(needsLabel(0), 'Needs you')
})
