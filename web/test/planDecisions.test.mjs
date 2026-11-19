// Tests of src/planDecisions.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { decisionBadge, decisionState, markedOption, personLabel, whoLine } from '../src/planDecisions.ts'

const d = {
  id: 'D1',
  title: 'Storage',
  question: 'Where?',
  options: ['Postgres', 'S3'],
  recommended: 1,
  why: 'Cheap',
  state: 'locked',
  version: 2,
  decidedBy: 'agent',
  decidedAt: '2026-11-18T10:00:00Z',
}

test('decisionState and decisionBadge: open, not signed off, signed off', () => {
  assert.equal(decisionState({ ...d, state: 'open' }), 'open')
  assert.equal(decisionState({ ...d, state: 'open', signedOffBy: 'u1' }), 'open')
  assert.equal(decisionState(d), 'unsigned')
  assert.equal(decisionState({ ...d, signedOffBy: '' }), 'unsigned')
  assert.equal(decisionState({ ...d, signedOffBy: 'u1' }), 'signed')
  assert.deepEqual(decisionBadge('open'), { label: 'Open', tone: 'info' })
  assert.deepEqual(decisionBadge('unsigned'), { label: 'Not signed off', tone: 'warning' })
  assert.deepEqual(decisionBadge('signed'), { label: 'Signed off', tone: 'success' })
})

test('whoLine: the agent, an owner by name, the subject without a name, no sign-off', () => {
  assert.equal(whoLine(d), 'Decided by the agent · not signed off')
  assert.equal(
    whoLine({ ...d, signedOffBy: 'sub-ada', signedOffByName: 'Ada' }),
    'Decided by the agent · signed off by Ada',
  )
  assert.equal(
    whoLine({ ...d, decidedBy: 'sub-ada', decidedByName: 'Ada', signedOffBy: 'sub-bob' }),
    'Decided by Ada · signed off by sub-bob',
  )
  assert.equal(whoLine({ ...d, decidedBy: 'sub-ada', decidedByName: '' }), 'Decided by sub-ada · not signed off')
  assert.equal(personLabel('agent', 'Agent Smith'), 'the agent')
})

test('markedOption: Recommended until signed off, then Signed off; out of range marks nothing', () => {
  assert.deepEqual(markedOption(d), { index: 1, label: 'Recommended' })
  assert.deepEqual(markedOption({ ...d, signedOffBy: 'u1' }), { index: 1, label: 'Signed off' })
  assert.equal(markedOption({ ...d, recommended: 2 }), null)
  assert.equal(markedOption({ ...d, recommended: -1 }), null)
  assert.equal(markedOption({ ...d, options: [] }), null)
})
