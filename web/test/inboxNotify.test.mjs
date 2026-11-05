// Tests of src/inboxNotify.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  inboxKey,
  newNotifications,
  notifiable,
  notificationText,
} from '../src/inboxNotify.ts'

const pause = (over = {}) => ({
  namespace: 'team',
  project: 'shop',
  kind: 'pause',
  task: 'shop-spec-m3',
  milestoneIID: 3,
  issueIID: 12,
  pauseKind: 'ciRed',
  title: 'Checkout',
  reason: 'CI failed on #12',
  at: '2026-11-03T10:00:00Z',
  ...over,
})
const proposal = (over = {}) => ({
  namespace: 'team',
  project: 'shop',
  kind: 'proposal',
  task: 'shop-pilot',
  milestoneIID: 3,
  proposalID: 3,
  title: 'Split the cart issue',
  reason: 'proposal',
  ...over,
})
const task = (reason, over = {}) => ({
  namespace: 'team',
  project: 'shop',
  kind: 'task',
  task: 'shop-impl-12',
  milestoneIID: 3,
  issueIID: 12,
  title: 'Cart totals',
  reason,
  ...over,
})

const keys = (items) => items.map(inboxKey)

test('inboxKey: stable, and ignores text and time', () => {
  assert.equal(inboxKey(pause()), inboxKey(pause()))
  assert.equal(inboxKey(pause()), inboxKey(pause({ reason: 'other text', at: '2026-11-04T00:00:00Z', title: 'x' })))
  assert.equal(inboxKey(proposal()), inboxKey(proposal({ title: 'reworded' })))
  assert.equal(inboxKey(task('needs-input')), inboxKey(task('needs-input', { at: '2026-11-04T00:00:00Z' })))
})

test('inboxKey: a different pause cause or paused issue is a new item', () => {
  assert.notEqual(inboxKey(pause()), inboxKey(pause({ pauseKind: 'findings' })))
  assert.notEqual(inboxKey(pause()), inboxKey(pause({ issueIID: 13 })))
  assert.notEqual(inboxKey(pause()), inboxKey(pause({ milestoneIID: 4 })))
  assert.notEqual(inboxKey(pause()), inboxKey(pause({ pauseKind: undefined })))
})

test('inboxKey: distinguishes kinds, projects, namespaces, proposals and tasks', () => {
  const all = [
    pause(),
    pause({ project: 'blog' }),
    pause({ namespace: 'other' }),
    proposal(),
    proposal({ proposalID: 4 }),
    proposal({ milestoneIID: 4 }),
    task('needs-input'),
    task('needs-input', { task: 'shop-impl-13' }),
  ]
  assert.equal(new Set(keys(all)).size, all.length)
})

test('notifiable: pauses, proposals and needs-input tasks only', () => {
  assert.equal(notifiable(pause()), true)
  assert.equal(notifiable(pause({ pauseKind: undefined })), true)
  assert.equal(notifiable(proposal()), true)
  assert.equal(notifiable(task('needs-input')), true)
  assert.equal(notifiable(task('failed')), false)
  assert.equal(notifiable(task('blocked')), false)
})

test('newNotifications: seed, add, repeat, removal and re-add after the cause changes', () => {
  const seen = new Set()
  const remember = (items) => items.forEach((i) => seen.add(inboxKey(i)))

  // First load: everything is new to an empty set; the app only seeds then.
  const first = [pause(), task('failed'), proposal()]
  assert.deepEqual(keys(newNotifications(seen, first)), keys([pause(), proposal()]))
  remember(first)

  // A reload with the same items notifies nothing.
  assert.deepEqual(newNotifications(seen, first), [])

  // A new needs-input task notifies once.
  const second = [...first, task('needs-input', { task: 'shop-impl-14', issueIID: 14 })]
  assert.deepEqual(keys(newNotifications(seen, second)), [inboxKey(second[3])])
  remember(second)
  assert.deepEqual(newNotifications(seen, second), [])

  // The pause goes away (resumed) and comes back with the same cause: seen.
  assert.deepEqual(newNotifications(seen, [proposal()]), [])
  assert.deepEqual(newNotifications(seen, [pause()]), [])

  // It comes back with another cause: new.
  const changed = pause({ pauseKind: 'findings', reason: 'Review findings on #12' })
  assert.deepEqual(keys(newNotifications(seen, [changed])), [inboxKey(changed)])

  // A failed task never notifies, also when new.
  assert.deepEqual(newNotifications(seen, [task('failed', { task: 'shop-impl-15' })]), [])
})

test('newNotifications: a key listed twice notifies once', () => {
  assert.equal(newNotifications(new Set(), [pause(), pause({ reason: 'dup' })]).length, 1)
})

test('notificationText: pause, kind-less pause, proposal and needs-input', () => {
  assert.deepEqual(notificationText(pause()), {
    title: 'Autopilot paused · Checkout',
    body: 'CI is red · #12\nCI failed on #12',
  })
  assert.deepEqual(notificationText(pause({ pauseKind: undefined, issueIID: undefined, reason: 'stopped' })), {
    title: 'Autopilot paused · Checkout',
    body: 'stopped',
  })
  assert.deepEqual(notificationText(proposal()), {
    title: 'Proposal P3 · shop',
    body: 'Split the cart issue',
  })
  assert.deepEqual(notificationText(task('needs-input')), {
    title: '#12 needs input',
    body: 'shop · Cart totals',
  })
  assert.deepEqual(notificationText(task('needs-input', { issueIID: undefined, title: '' })), {
    title: 'Needs input',
    body: 'shop',
  })
})

test('prompts notify once per task and prompt id', () => {
  const p = (task, promptID) => ({ namespace: 'team', project: 'shop', kind: 'prompt', task, promptID, title: 'Retry?', reason: 'mainHealth' })
  const a = p('spec', 'Q1')
  assert.notEqual(inboxKey(a), inboxKey(p('impl', 'Q1')))
  assert.deepEqual(newNotifications(new Set(), [a, { ...a }]), [a])
  assert.deepEqual(newNotifications(new Set([inboxKey(a)]), [a]), [])
  assert.deepEqual(notificationText(a), { title: 'Question Q1 · shop', body: 'Retry?' })
})

test('inboxKey: pilot write errors differ by source and error, and raise no notification', () => {
  const err = (over = {}) => ({ namespace: 'team', project: 'shop', kind: 'pilotError', task: 'shop-pilot', title: 'runner/recordProposal', reason: 'bad', ...over })
  assert.notEqual(inboxKey(err()), inboxKey(err({ reason: 'other' })))
  assert.equal(notifiable(err()), false)
})
