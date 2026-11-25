// Tests of src/nowAndNext.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { nowAndNext, nowStripText } from '../src/nowAndNext.ts'

const now = Date.parse('2026-11-25T10:00:00Z')
const milestone = { iid: 57, title: 'Desk: Now & next' }
const item = (iid, lane, extra = {}) => ({ iid, title: `Issue ${iid}`, state: lane === 'done' ? 'Merged' : 'Active', lane, task: `shop-${iid}`, ...extra })
const task = (iid, status) => ({ name: `shop-${iid}`, type: 'Implement', issue: { iid }, status })
const live = (iid, step, since = '2026-11-25T09:56:00Z') =>
  task(iid, { phase: 'Running', podName: `shop-${iid}-pod`, run: { activeRunRequest: 2, observedRunRequest: 1, currentStep: step, currentStepSince: since } })
const entry = (iid, done, total) => ({ iid, title: `M${iid}`, task: `spec-${iid}`, state: 'waiting', done, total, costUSD: '0' })
const withCI = (t, state) => ({ ...t, status: { ...t.status, ci: { sha: 'abc', state } } })

test('running: implementing with its step and minutes and the live run; landing says merging', () => {
  const nn = nowAndNext({
    milestone,
    items: [item(712, 'running'), item(713, 'landing', { task: undefined }), item(711, 'running')],
    tasks: [live(712, '3 run implementer'), task(711, { phase: 'Queued' })],
    queue: [],
    now,
  })
  assert.deepEqual(nn.running, [
    { iid: 712, title: 'Issue 712', text: '#712 implementing · 4 min', task: 'shop-712', pod: 'shop-712-pod', run: '2' },
    { iid: 713, title: 'Issue 713', text: '#713 merging' },
    { iid: 711, title: 'Issue 711', text: '#711 queued' },
  ])
  assert.deepEqual(nn.waiting, [])
  assert.equal(nn.counts.running, 3)
})

test('waiting: a held item with its reason, and a running item between runs while its pipeline runs', () => {
  const nn = nowAndNext({
    milestone,
    items: [
      item(714, 'running'),
      item(715, 'held', { hold: { kind: 'ci', reason: 'CI failed 3 times', since: '2026-11-25T08:00:00Z' } }),
      item(718, 'running'),
      item(719, 'held'),
    ],
    tasks: [
      // Between runs while the pushed head's pipeline runs.
      task(714, { phase: 'Idle', ci: { sha: 'abc', state: 'Running' } }),
      task(718, { phase: 'Idle', ci: { sha: 'def', state: 'Pending' } }),
    ],
    queue: [],
    now,
  })
  assert.deepEqual(nn.running, [])
  assert.deepEqual(
    nn.waiting.map((r) => r.text),
    ['#714 waiting for CI', '#715 held: CI failed 3 times', '#718 waiting for CI', '#719 held'],
  )
  assert.equal(nn.waiting[0].pod, undefined)
  assert.equal(nn.counts.waiting, 4)
})

test('an item is in exactly one of running and waiting: a run in progress works, reviewers too', () => {
  const nn = nowAndNext({
    milestone,
    items: [item(714, 'running'), item(716, 'running'), item(712, 'running')],
    tasks: [
      // The pipeline of an older head still runs: the new run works.
      withCI(live(714, '6 start pipeline', '2026-11-25T09:58:00Z'), 'Running'),
      live(716, '8 review round 1'),
      live(712, '4 read failed CI jobs'),
    ],
    queue: [],
    now,
  })
  assert.deepEqual(
    nn.running.map((r) => r.text),
    ['#714 fixing CI · 2 min', '#716 reviewing · 4 min', '#712 fixing CI · 4 min'],
  )
  assert.deepEqual(nn.waiting, [])
  assert.equal(nn.running[1].pod, 'shop-716-pod')
})

test('up next: ready, then waiting in queue order, "after #712", capped at 5 with moreNext', () => {
  const nn = nowAndNext({
    milestone,
    items: [
      item(720, 'waiting', { waitingFor: [712] }),
      item(721, 'waiting', { waitingFor: [712, 713] }),
      item(717, 'ready'),
      item(722, 'waiting'),
      item(723, 'waiting'),
      item(724, 'waiting'),
      item(725, 'waiting'),
      item(700, 'done'),
    ],
    tasks: [],
    queue: [],
    now,
  })
  assert.deepEqual(
    nn.next.map((r) => r.text),
    ['#717 ready to land', '#720 after #712', '#721 after #712, #713', '#722 waiting for a free slot', '#723 waiting for a free slot'],
  )
  assert.equal(nn.moreNext, 2)
  assert.deepEqual(nn.counts, { running: 0, waiting: 0, next: 7, nextIID: 717 })
})

test('milestones: this one with its merged count first, then the next 3 of the project queue', () => {
  const items = [
    ...[1, 2, 3].map((i) => item(i, 'done')),
    ...[4, 5, 6, 7, 8, 9].map((i) => item(i, 'waiting')),
  ]
  const queue = [entry(55, 4, 4), entry(57, 3, 9), entry(58, 0, 5), entry(59, 0, 0), entry(60, 0, 2), entry(61, 0, 1)]
  const nn = nowAndNext({ milestone, items, tasks: [], queue, now })
  assert.deepEqual(nn.milestones, [
    { iid: 57, title: 'Desk: Now & next', text: '3 of 9 merged', current: true },
    { iid: 58, title: 'M58', text: '0 of 5 merged', current: false },
    { iid: 59, title: 'M59', text: 'not planned yet', current: false },
    { iid: 60, title: 'M60', text: '0 of 2 merged', current: false },
  ])
  // Not in the queue: the first 3.
  const outside = nowAndNext({ milestone: { iid: 99, title: 'Loose' }, items: [], tasks: [], queue, now })
  assert.deepEqual(
    outside.milestones.map((m) => [m.iid, m.text, m.current]),
    [
      [99, 'not planned yet', true],
      [55, '4 of 4 merged', false],
      [57, '3 of 9 merged', false],
      [58, '0 of 5 merged', false],
    ],
  )
})

test('an empty milestone: no sections, nothing running', () => {
  const nn = nowAndNext({ milestone, items: undefined, tasks: undefined, queue: undefined, now })
  assert.deepEqual(nn, {
    running: [],
    waiting: [],
    next: [],
    moreNext: 0,
    milestones: [{ iid: 57, title: 'Desk: Now & next', text: 'not planned yet', current: true }],
    counts: { running: 0, waiting: 0, next: 0, nextIID: undefined },
  })
  assert.equal(nowStripText(nn.counts), 'Nothing running')
})

test('nowStripText leaves out the parts that are zero', () => {
  assert.equal(nowStripText({ running: 2, waiting: 1, next: 3, nextIID: 717 }), '2 running · 1 waiting · next #717')
  assert.equal(nowStripText({ running: 1, waiting: 0, next: 0 }), '1 running')
  assert.equal(nowStripText({ running: 0, waiting: 0, next: 1, nextIID: 717 }), 'next #717')
  assert.equal(nowStripText({ running: 0, waiting: 2, next: 0 }), 'Nothing running · 2 waiting')
})
