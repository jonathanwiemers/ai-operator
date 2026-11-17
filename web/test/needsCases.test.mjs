// Tests of src/needsCases.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { needsCases, problemAnchor } from '../src/needsCases.ts'

const at = (min) => new Date(Date.parse('2026-11-05T10:00:00Z') + min * 60000).toISOString()
const prompt = (id, o = {}) => ({ id, state: 'answered', postedAt: at(0), ...o })
const view = (o = {}) => ({
  issues: [{ iid: 12, title: 'Fix it' }, { iid: 13, title: 'Other' }],
  prompts: [],
  autopilot: { phase: 'Running', holds: [] },
  ...o,
})

test('an issue groups a prompt, two actions and a pause, newest first', () => {
  const m = view({
    prompts: [prompt('Q1', { issueIID: 12, closedAt: at(10) })],
    pilot: {
      acted: [
        { at: at(20), issues: [12] },
        { at: at(5), issues: [12] },
      ],
      interventions: [{ issueIID: 12, used: 2, max: 2 }],
    },
    pauses: { pauses: [{ kind: 'ciRed', item: 12, since: at(1), until: at(30) }] },
  })
  const ps = needsCases(m)
  assert.equal(ps.length, 1)
  assert.equal(ps[0].title, 'Fix it')
  assert.deepEqual(ps[0].entries.map((e) => e.at), [at(30), at(20), at(10), at(5)])
  assert.deepEqual(ps[0].attempts, { used: 2, max: 2 })
})

test('milestone-level prompt and acted entry go to problem 0', () => {
  const ps = needsCases(view({
    prompts: [prompt('Q1', { closedAt: at(1) })],
    pilot: { acted: [{ at: at(2), issues: [0] }], interventions: [] },
  }))
  assert.equal(ps.length, 1)
  assert.equal(ps[0].iid, 0)
  assert.equal(ps[0].title, '')
  assert.equal(ps[0].entries.length, 2)
})

test('an acted entry with issues 12 and 0 shows under both', () => {
  const ps = needsCases(view({ pilot: { acted: [{ at: at(2), issues: [12, 0] }], interventions: [] } }))
  assert.deepEqual(ps.map((p) => p.iid).sort(), [0, 12])
})

test('an open prompt sorts above a handled problem with a newer entry', () => {
  const ps = needsCases(view({
    prompts: [
      prompt('Q1', { state: 'open', issueIID: 12, postedAt: at(1) }),
      prompt('Q2', { issueIID: 13, closedAt: at(50) }),
    ],
  }))
  assert.deepEqual(ps.map((p) => [p.iid, p.open]), [[12, true], [13, false]])
})

test('a held issue and a paused milestone are open', () => {
  const held = needsCases(view({ autopilot: { phase: 'Running', holds: [{ iid: 13 }] } }))
  assert.deepEqual(held.map((p) => [p.iid, p.open]), [[13, true]])
  const paused = needsCases(view({ autopilot: { phase: 'Paused', holds: [] } }))
  assert.deepEqual(paused.map((p) => [p.iid, p.open]), [[0, true]])
})

test('a stale head shows 0 of max; a missing entry takes max from another', () => {
  const ps = needsCases(view({
    prompts: [prompt('Q1', { issueIID: 12, closedAt: at(1) }), prompt('Q2', { issueIID: 13, closedAt: at(2) })],
    pilot: { acted: [], interventions: [{ issueIID: 12, used: 0, max: 3 }] },
  }))
  assert.deepEqual(ps.find((p) => p.iid === 12).attempts, { used: 0, max: 3 })
  assert.deepEqual(ps.find((p) => p.iid === 13).attempts, { used: 0, max: 3 })
})

test('without pilot or pauses prompts still group, with no attempts', () => {
  const ps = needsCases(view({ prompts: [prompt('Q1', { issueIID: 12, closedAt: at(1) })] }))
  assert.equal(ps.length, 1)
  assert.equal(ps[0].attempts, undefined)
})

test('problemAnchor', () => {
  assert.equal(problemAnchor(0), 'problem-0')
  assert.equal(problemAnchor(12), 'problem-12')
})
