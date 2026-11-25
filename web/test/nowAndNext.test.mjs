// Tests of src/nowAndNext.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { nowAndNext, nowStripText, overviewNowAndNext, projectNowAndNext } from '../src/nowAndNext.ts'

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

// projectNowAndNext (R9): the project page's milestones with their tasks and
// spec task, and the loaded view of the milestone that runs first.
const spec = (items) => ({ name: 'spec', type: 'Spec', status: { autopilot: { phase: 'Running', items } } })
const issueTask = (iid, status) => ({ ...task(iid, status), issue: { iid, title: `Issue ${iid}` } })

test('projectNowAndNext: Running and Waiting grouped by milestone, holds from spec items', () => {
  const nn = projectNowAndNext({
    milestones: [
      {
        iid: 58,
        title: 'Second',
        tasks: [{ ...live(812, '3 run implementer'), issue: { iid: 812, title: 'Issue 812' } }, issueTask(813, { phase: 'Idle' })],
        spec: spec([
          { iid: 812, state: 'Active' },
          { iid: 813, state: 'Active', hold: { kind: 'ci', reason: 'CI failed 3 times', since: '2026-11-25T08:00:00Z' } },
          { iid: 814, state: 'Active', hold: { kind: 'review', reason: 'Review failed', since: '2026-11-25T08:00:00Z' } },
        ]),
      },
      {
        iid: 57,
        title: 'First',
        tasks: [
          issueTask(714, { phase: 'Idle', ci: { sha: 'abc', state: 'Running' } }),
          { ...live(712, '8 review round 1'), issue: { iid: 712, title: 'Issue 712' } },
          // Merged: neither running nor waiting, even with a hold left.
          issueTask(700, { phase: 'Running', ci: { sha: 'old', state: 'Running' } }),
          issueTask(701, { phase: 'Idle' }),
        ],
        spec: spec([{ iid: 700, state: 'Merged', hold: { kind: 'ci', reason: 'old', since: '2026-11-25T08:00:00Z' } }]),
      },
      { iid: 56, title: 'Done', tasks: [issueTask(600, { phase: 'Idle' })] },
    ],
    first: undefined,
    queue: [],
    now,
  })
  assert.deepEqual(
    nn.running.map((g) => [g.key, g.heading, g.rows.map((r) => r.text)]),
    [
      ['58', 'Second', ['#812 implementing · 4 min']],
      ['57', 'First', ['#712 reviewing · 4 min']],
    ],
  )
  assert.equal(nn.running[0].rows[0].pod, 'shop-812-pod')
  assert.deepEqual(
    nn.waiting.map((g) => [g.key, g.rows.map((r) => r.text)]),
    [
      ['58', ['#813 held: CI failed 3 times', '#814 held: Review failed']],
      ['57', ['#714 waiting for CI']],
    ],
  )
  assert.deepEqual(nn.counts, { running: 2, waiting: 3, next: 0, nextIID: undefined })
  assert.deepEqual(nn.next, [])
})

test('projectNowAndNext: Up next from the milestone that runs first, capped; Milestones is the project queue', () => {
  const first = {
    iid: 57,
    tasks: [],
    autopilot: {
      items: [
        item(717, 'ready'),
        item(720, 'waiting', { waitingFor: [712] }),
        ...[721, 722, 723, 724].map((i) => item(i, 'waiting')),
        item(712, 'running'),
      ],
    },
  }
  const queue = [entry(57, 3, 9), entry(58, 0, 5), entry(59, 0, 0)]
  queue[0].state = 'running'
  const nn = projectNowAndNext({ milestones: [], first, queue, now })
  assert.deepEqual(
    nn.next.map((r) => r.text),
    ['#717 ready to land', '#720 after #712', '#721 waiting for a free slot', '#722 waiting for a free slot', '#723 waiting for a free slot'],
  )
  assert.equal(nn.moreNext, 1)
  assert.equal(nn.nextOf, 57)
  assert.deepEqual(nn.counts, { running: 0, waiting: 0, next: 6, nextIID: 717 })
  assert.deepEqual(nn.milestones, [
    { iid: 57, title: 'M57', text: 'running · 3 of 9 merged', current: false },
    { iid: 58, title: 'M58', text: 'waiting · 0 of 5 merged', current: false },
    { iid: 59, title: 'M59', text: 'waiting · not planned yet', current: false },
  ])
  assert.equal(nowStripText(nn.counts), 'next #717')
})

// overviewNowAndNext: the Overview's Working now entries (GET /api/overview),
// the project-level fields repeated on each of a project's entries.
const shop = {
  namespace: 'team-a',
  project: 'shop',
  finishing: 1,
  next: { issueIID: 717, title: 'Refund mails', milestoneIID: 57 },
  held: [{ issueIID: 715, title: 'Coupons', milestoneIID: 57, reason: 'CI failed 3 times' }],
  ciWaits: [{ issueIID: 714, title: 'Invoices', milestoneIID: 57 }],
  milestones: [
    { iid: 57, title: 'Desk', state: 'running' },
    { iid: 58, title: 'Billing', state: 'next' },
  ],
}

test('overviewNowAndNext: per project, running with its step and minutes, CI waits and holds, the milestones', () => {
  const working = [
    { ...shop, issueIID: 712, title: 'Now & next', milestoneIID: 57, task: 'shop-712', step: '3 run implementer', stepSince: '2026-11-25T09:56:00Z' },
    // Running without a step while its pipeline runs: a CI wait.
    { ...shop, issueIID: 713, title: 'Rail', milestoneIID: 57, task: 'shop-713', ci: 'Running' },
    // Running without a step and no pipeline to wait for, without a milestone.
    { ...shop, issueIID: 9, title: 'Loose', task: 'shop-9' },
    // A project with nothing running.
    { namespace: 'team-a', project: 'api', milestones: [{ iid: 3, title: 'Auth', state: 'next' }] },
  ]
  const { projects, counts } = overviewNowAndNext(working, now)
  assert.deepEqual(
    projects.map((p) => p.key),
    ['team-a/shop', 'team-a/api'],
  )
  const [s, a] = projects
  assert.equal(s.finishing, 1)
  assert.deepEqual(s.running, [
    { key: '0', rows: [{ iid: 9, title: 'Loose', text: '#9 starting', task: 'shop-9' }] },
    { key: '57', rows: [{ iid: 712, title: 'Now & next', text: '#712 implementing · 4 min', task: 'shop-712' }] },
  ])
  assert.deepEqual(s.waiting, [
    {
      key: '57',
      rows: [
        { iid: 713, title: 'Rail', text: '#713 waiting for CI', task: 'shop-713' },
        { iid: 714, title: 'Invoices', text: '#714 waiting for CI' },
        { iid: 715, title: 'Coupons', text: '#715 held: CI failed 3 times' },
      ],
    },
  ])
  assert.deepEqual(s.next, [{ iid: 717, title: 'Refund mails', text: '#717' }])
  assert.deepEqual(s.milestones, [
    { iid: 57, title: 'Desk', text: 'running', current: false },
    { iid: 58, title: 'Billing', text: 'next', current: false },
  ])
  assert.deepEqual([a.running, a.waiting, a.next, a.finishing], [[], [], [], 0])
  assert.deepEqual(a.milestones, [{ iid: 3, title: 'Auth', text: 'next', current: false }])
  assert.deepEqual(counts, { running: 2, waiting: 3, next: 1, nextIID: 717 })
})

test('overviewNowAndNext: counts across projects, nothing without entries', () => {
  const other = { namespace: 'team-b', project: 'web', held: [{ issueIID: 4, milestoneIID: 2 }], next: { issueIID: 5, title: 'Five' } }
  const { projects, counts } = overviewNowAndNext([{ ...shop, held: [], ciWaits: [] }, other], now)
  assert.deepEqual(projects[1].waiting, [{ key: '2', rows: [{ iid: 4, title: '', text: '#4 held' }] }])
  assert.deepEqual(counts, { running: 0, waiting: 1, next: 2, nextIID: 717 })
  assert.deepEqual(overviewNowAndNext(undefined, now), { projects: [], counts: { running: 0, waiting: 0, next: 0, nextIID: undefined } })
})

test('overviewNowAndNext: a held issue is only in Waiting, as lane held on a milestone', () => {
  const held = { ...shop, ciWaits: [{ issueIID: 715, title: 'Coupons', milestoneIID: 57 }] }
  const working = [{ ...held, issueIID: 715, title: 'Coupons', milestoneIID: 57, task: 'shop-715', step: '3 run implementer' }]
  const { projects, counts } = overviewNowAndNext(working, now)
  assert.deepEqual(projects[0].running, [])
  assert.deepEqual(projects[0].waiting, [{ key: '57', rows: [{ iid: 715, title: 'Coupons', text: '#715 held: CI failed 3 times' }] }])
  assert.deepEqual([counts.running, counts.waiting], [0, 1])
})
