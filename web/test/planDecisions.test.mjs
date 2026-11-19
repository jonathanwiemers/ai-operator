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

test('answersSummary: all kept, mixed, singular', async () => {
  const { answersSummary } = await import('../src/planDecisions.ts')
  assert.deepEqual(answersSummary([{ id: 'D1', version: 1, keep: true }, { id: 'D2', version: 1, keep: true }]),
    { total: 2, kept: 2, changed: 0, label: 'Answered 2 decisions: 2 kept, 0 changed' })
  const mixed = [{ id: 'D1', version: 1, keep: true }, { id: 'D2', version: 1, option: 0 }, { id: 'D3', version: 1, other: 'x' }]
  assert.equal(answersSummary(mixed).label, 'Answered 3 decisions: 1 kept, 2 changed')
  assert.equal(answersSummary([{ id: 'D1', version: 1, keep: true }]).label, 'Answered 1 decision: 1 kept, 0 changed')
})

test('changedLines: option with and without version, other', async () => {
  const { changedLines } = await import('../src/planDecisions.ts')
  const cur = [{ id: 'D2', version: 2, options: ['A', 'B'] }]
  const lines = changedLines([
    { id: 'D1', version: 1, keep: true },
    { id: 'D2', version: 2, option: 1 },
    { id: 'D2', version: 1, option: 1 },
    { id: 'D6', version: 1, other: 'my\nlong ' + 'z'.repeat(100) },
  ], cur)
  assert.equal(lines.length, 3)
  assert.equal(lines[0].text, 'D2 → B')
  assert.equal(lines[1].text, 'D2 → option 2')
  assert.ok(lines[2].text.startsWith('D6 → "my long z'))
  assert.ok(!lines[2].text.includes('\n') && lines[2].text.length < 100)
  assert.ok(lines[2].full.includes('\n'))
})

test('createWarning: 0, 1, several', async () => {
  const { createWarning } = await import('../src/planDecisions.ts')
  assert.equal(createWarning(0), '')
  assert.equal(createWarning(1), '1 decision is open or not signed off')
  assert.equal(createWarning(2), '2 decisions are open or not signed off')
})
