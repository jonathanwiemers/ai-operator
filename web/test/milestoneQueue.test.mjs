// Tests of src/milestoneQueue.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  canRevisit,
  decisionHref,
  laneGroups,
  parallelCommand,
  queueDone,
  queueHeld,
  queueRows,
  queueTone,
  resumeCommand,
  revisitCommand,
  revisitPromptID,
  sentLine,
  waitingForTurn,
  waitingLabel,
  waveChip,
  wavesLabel,
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

test('decisionHref: the Decisions tab with the history feature, GitLab without', () => {
  const d = { id: 'D42-3', url: 'https://gitlab.example/g/p/-/issues/42#note_7' }
  const path = '/projects/team-a/shop/milestones/14'
  assert.equal(decisionHref(d, path, true), '/projects/team-a/shop/milestones/14?tab=decisions#D42-3')
  assert.equal(decisionHref(d, path, false), d.url)
  assert.equal(decisionHref({ id: 'D42-3' }, path, false), '')
  assert.equal(decisionHref({ id: ' D42-P1 ' }, path, true), `${path}?tab=decisions#D42-P1`)
  // No ID or no milestone page: GitLab.
  assert.equal(decisionHref({ id: '', url: d.url }, path, true), d.url)
  assert.equal(decisionHref(d, '', true), d.url)
})

test('revisitPromptID: finds the prompt in the answer', () => {
  assert.equal(revisitPromptID('Q7 opened for D12-3'), 'Q7')
  assert.equal(revisitPromptID('nothing here'), '')
})

test('waitingForTurn: names the older milestones, nothing when not waiting', () => {
  assert.equal(waitingForTurn([7, 9]), 'Waiting for its turn: %7, %9 go first')
  assert.equal(
    waitingForTurn([7, 9], [{ iid: 7, title: 'Guest checkout' }, { iid: 8, title: 'Other' }]),
    'Waiting for its turn: Guest checkout (%7), %9 go first',
  )
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

test('parallelCommand: the limit one up or down, within 1 to 10', () => {
  assert.deepEqual(parallelCommand(3, 1), { cmd: 'parallel', parallel: 4 })
  assert.deepEqual(parallelCommand(3, -1), { cmd: 'parallel', parallel: 2 })
  assert.deepEqual(parallelCommand(9, 1), { cmd: 'parallel', parallel: 10 })
  assert.equal(parallelCommand(10, 1), null)
  assert.deepEqual(parallelCommand(2, -1), { cmd: 'parallel', parallel: 1 })
  assert.equal(parallelCommand(1, -1), null)
})

test('waveChip and wavesLabel: chips on planned rows, a count for several waves', () => {
  const items = [
    { iid: 1, wave: 1 },
    { iid: 2, wave: 1 },
    { iid: 3, wave: 2 },
    { iid: 4, wave: 3 },
    { iid: 9, wave: 0 },
    { iid: 10 },
  ]
  assert.deepEqual(
    items.map((i) => waveChip(i.wave)),
    ['W1', 'W1', 'W2', 'W3', '', ''],
  )
  assert.equal(wavesLabel(items), '3 waves')
  assert.equal(wavesLabel([{ wave: 1 }, { wave: 1 }]), '')
  assert.equal(wavesLabel([{}, { wave: 0 }]), '')
  assert.equal(wavesLabel([]), '')
})

test('queueHeld: counts the Active items with a hold', () => {
  const hold = { kind: 'ciRed', reason: 'red', since: 't' }
  const items = [
    { iid: 1, state: 'Active', hold },
    { iid: 2, state: 'Active' },
    { iid: 3, state: 'Active', hold },
    { iid: 4, state: 'Merged', hold },
  ]
  assert.equal(queueHeld(items), 2)
  assert.equal(queueHeld([]), 0)
})

test('sentLine: pending and sent per command', () => {
  assert.equal(sentLine('revisit', 'D42-3', true), 'Revisiting D42-3…')
  assert.equal(sentLine('revisit', 'D42-3', false), 'Revisit of D42-3 opened.')
  assert.equal(sentLine('parallel', '3', true), 'Setting the limit to 3…')
  assert.equal(sentLine('parallel', '3', false), 'Limit 3 sent.')
  assert.equal(sentLine('resume', '#42', true), 'Resuming #42…')
  assert.equal(sentLine('resume', '#42', false), 'Resume #42 sent.')
})
