// Tests of src/cockpitThread.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { eventPlace, threadHeader } from '../src/cockpitThread.ts'

const now = Date.parse('2026-11-24T10:00:00Z')
const thread = (extra) => ({ key: 'k', namespace: 'ns', project: 'shop', milestoneIID: 5, at: '', items: [], ...extra })
const running = (iid, title = '') => ({
  name: `shop-${iid}`,
  type: 'Implement',
  issue: { iid, title },
  status: {
    phase: 'Running',
    podName: `shop-${iid}`,
    run: { activeRunRequest: 3, observedRunRequest: 2, currentStep: '3 run implementer', currentStepSince: '2026-11-24T09:56:00Z' },
  },
})
const idle = (iid, title) => ({ name: `shop-${iid}`, type: 'Implement', issue: { iid, title }, status: { phase: 'Idle' } })
const issues = [
  { iid: 712, title: 'Add the provider', state: 'active' },
  { iid: 713, title: 'Held one', state: 'active' },
  { iid: 714, title: 'Waits on #712', state: 'pending' },
  { iid: 715, title: 'Done one', state: 'merged' },
  { iid: 716, title: 'Next one', state: 'planned' },
]
const items = [
  { iid: 712, lane: 'running' },
  { iid: 713, lane: 'held' },
  { iid: 714, lane: 'waiting' },
  { iid: 715, lane: 'done' },
  { iid: 716, lane: 'ready' },
]
const ctx = { issues, items, tasks: [running(712), idle(715, 'Done one')], now }
// text: the header as the card reads it.
const text = (h) => [h.ref, h.title, h.state?.label, h.step].filter(Boolean).join(' · ')

test('a running issue reads its ref, title, state and step with the minutes', () => {
  const h = threadHeader(thread({ issueIID: 712 }), ctx)
  assert.equal(text(h), '#712 · Add the provider · Running · Implementing · 4 min')
  assert.equal(h.state.tone, 'progress')
  assert.equal(h.anchor, 'issue-712')
  assert.equal(h.label, '#712 · Add the provider')
  assert.deepEqual(h.live, { task: 'shop-712', iid: 712, text: '#712 implementing · 4 min', pod: 'shop-712', run: '3' })
  assert.equal(h.milestoneTitle, undefined)
})

test('held, waiting, next and done issues have a state and no step', () => {
  assert.equal(text(threadHeader(thread({ issueIID: 713 }), ctx)), '#713 · Held one · Waiting')
  assert.equal(threadHeader(thread({ issueIID: 713 }), ctx).state.tone, 'warning')
  assert.equal(text(threadHeader(thread({ issueIID: 714 }), ctx)), '#714 · Waits on #712 · Waiting')
  assert.equal(text(threadHeader(thread({ issueIID: 716 }), ctx)), '#716 · Next one · Next')
  const done = threadHeader(thread({ issueIID: 715 }), ctx)
  assert.equal(text(done), '#715 · Done one · Merged')
  assert.equal(done.state.tone, 'success')
  assert.equal(done.live, undefined)
})

test('the title falls back to the task, else stays empty', () => {
  const tasks = [idle(720, 'From the task')]
  assert.equal(threadHeader(thread({ issueIID: 720 }), { ...ctx, tasks }).title, 'From the task')
  const h = threadHeader(thread({ issueIID: 721 }), ctx)
  assert.equal(text(h), '#721')
  assert.equal(h.label, '#721')
})

test("the Milestone thread's header is the milestone's status", () => {
  const status = { label: 'Autopilot running', tone: 'progress' }
  const h = threadHeader(thread({}), { ...ctx, status })
  assert.equal(h.ref, 'Milestone')
  assert.deepEqual(h.state, status)
  assert.equal(h.anchor, undefined)
  assert.equal(h.label, 'Milestone')
})

test('the project scope shows the milestone title, and a running issue without rows', () => {
  const p = { scope: 'project', tasks: [running(712, 'From the task')], milestones: { 5: 'Payments' }, now }
  const h = threadHeader(thread({ issueIID: 712 }), p)
  assert.equal(text(h), '#712 · From the task · Running · Implementing · 4 min')
  assert.equal(h.milestoneTitle, 'Payments')
  assert.equal(h.label, '#712 · From the task · Payments')
  const quiet = threadHeader(thread({ issueIID: 715, milestoneIID: 6 }), p)
  assert.equal(quiet.state, undefined)
  assert.equal(quiet.milestoneTitle, '%6')
  const ms = threadHeader(thread({}), { ...p, status: { label: 'Running', tone: 'progress' } })
  assert.deepEqual([ms.ref, ms.milestoneTitle, ms.state, ms.label], ['Milestone', 'Payments', undefined, 'Milestone · Payments'])
  assert.equal(threadHeader(thread({ milestoneIID: undefined }), p).ref, 'shop')
})

test('with scope all the header is the project', () => {
  const h = threadHeader(thread({ issueIID: 712 }), { ...ctx, scope: 'all' })
  assert.deepEqual(h, { ref: 'shop', title: '', label: 'shop' })
})

// R4 of "Desk: a notification click opens the event it is about": where an
// #event-<id> link's card sits in a thread.
test('eventPlace: a shown event, one behind Show N more, one in a group, a note thread and an absent id', () => {
  const ev = (id, kind = 'release.ended') => ({ id, kind, namespace: 'ns', project: 'shop', at: '' })
  const group = { type: 'group', key: 'g', namespace: 'ns', project: 'shop', from: '', to: '', events: [ev(7, 'ci'), ev(8, 'ci')], summary: '' }
  const items = [
    { type: 'event', event: ev(1) },
    { type: 'thread', key: 't', at: '', notes: [ev(2, 'note')] },
    group,
    { type: 'event', event: ev(3) },
    { type: 'event', event: ev(4) },
  ]
  assert.deepEqual(eventPlace(items, 1), { index: 0 })
  assert.deepEqual(eventPlace(items, 4), { index: 4 }, 'behind Show N more')
  assert.deepEqual(eventPlace(items, 8), { index: 2, group })
  assert.equal(eventPlace(items, 2), null, 'a note thread is no target')
  assert.equal(eventPlace(items, 99), null)
  assert.equal(eventPlace([], 1), null)
})
