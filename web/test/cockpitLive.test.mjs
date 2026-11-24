// Tests of src/cockpitLive.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { liveLines, stepWords } from '../src/cockpitLive.ts'

const now = Date.parse('2026-11-24T10:00:00Z')
const task = (name, iid, run, extra = {}) => ({
  name,
  type: 'Implement',
  issue: { iid },
  status: { phase: 'Running', podName: name, run },
  ...extra,
})

test('steps read in words, an unknown step by its name', () => {
  assert.equal(stepWords('3 run implementer'), 'implementing')
  assert.equal(stepWords('7 run implementer, step 2'), 'implementing')
  assert.equal(stepWords('9 fix round 1'), 'fixing review findings')
  assert.equal(stepWords('8 review round 2'), 'reviewing')
  assert.equal(stepWords('4 read failed CI jobs'), 'fixing CI')
  assert.equal(stepWords('2 prepare checkout'), 'preparing')
  assert.equal(stepWords('11 mark ready'), 'updating the merge request')
  assert.equal(stepWords('12 update issue labels'), 'update issue labels')
  assert.equal(stepWords(undefined), 'starting')
})

test('a running implement task is one line with the minutes of its step', () => {
  const lines = liveLines(
    [
      task('shop-712', 712, { activeRunRequest: 3, observedRunRequest: 2, currentStep: '3 run implementer', currentStepSince: '2026-11-24T09:56:00Z' }),
      task('shop-700', 700, { activeRunRequest: 1, observedRunRequest: 0 }),
    ],
    now,
  )
  assert.deepEqual(lines, [
    { task: 'shop-700', iid: 700, text: '#700 starting', pod: 'shop-700', run: '1' },
    { task: 'shop-712', iid: 712, text: '#712 implementing · 4 min', pod: 'shop-712', run: '3' },
  ])
})

test('a finished run, the pilot and a task without an issue have no line', () => {
  const lines = liveLines(
    [
      task('shop-701', 701, { activeRunRequest: 2, observedRunRequest: 2 }),
      // An idle task keeps its last step: its run is over.
      { ...task('shop-702', 702, { currentStep: '9 comment' }), status: { phase: 'Idle', run: { currentStep: '9 comment' } } },
      task('shop-pilot', 14, { currentStep: '1 run pilot' }, { type: 'Coordinate' }),
      { ...task('shop-x', 0, { currentStep: '1 read issue' }), issue: undefined },
    ],
    now,
  )
  assert.deepEqual(lines, [])
  assert.deepEqual(liveLines(undefined, now), [])
})
