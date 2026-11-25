// Tests of src/inboxNotify.ts and deskWorker.ts's local times: npm test (node --test, no extra dependencies).
// The server sets each item's key and category (notify); their rules are
// tested in Go (internal/ui). The fixtures carry them as the server does.
process.env.TZ = 'Europe/Berlin'

import assert from 'node:assert/strict'
import { test } from 'node:test'
import { localTimesText } from '../src/localTime.ts'
import { showDeskNotification } from '../src/deskWorker.ts'
import { newNotifications, notificationText, seenKeys } from '../src/inboxNotify.ts'

const now = Date.parse('2026-11-21T13:00:00Z')

const all = () => true
const only = (...cs) => (c) => cs.includes(c)

const problem = (issueIID, over = {}) => ({
  namespace: 'team',
  project: 'shop',
  kind: 'problem',
  task: 'shop-spec-m3',
  milestoneIID: 3,
  issueIID,
  title: issueIID ? `Issue ${issueIID}` : 'Checkout',
  reason: 'CI failed',
  at: '2026-11-03T10:00:00Z',
  key: `team/shop/problem/3/${issueIID}/open`,
  notify: 'problem',
  ...over,
})
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
  key: 'team/shop/pause/3/12/ciRed',
  notify: 'problem',
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
  key: 'team/shop/proposal/3/3',
  notify: 'proposal',
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
  key: 'team/shop/task/shop-impl-12',
  notify: reason === 'needs-input' ? 'question' : 'failed',
  ...over,
})
const warning = (over = {}) => ({
  namespace: 'team',
  project: 'shop',
  kind: 'budgetWarning',
  task: 'shop-impl-12',
  milestoneIID: 3,
  issueIID: 12,
  title: 'Cart totals',
  reason: "80 % of the issue's budget of $20.00 spent",
  key: "team/shop/budgetWarning/3/12/80 % of the issue's budget of $20.00 spent",
  notify: 'budget',
  ...over,
})
// The server gives a pilot write error no category.
const pilotError = {
  namespace: 'team',
  project: 'shop',
  kind: 'pilotError',
  task: 'shop-pilot',
  title: 'runner/recordProposal',
  reason: 'bad',
  key: 'team/shop/pilotError/shop-pilot/runner/recordProposal/bad',
}

const keys = (items) => items.map((i) => i.key)

// loader runs App.vue's onInbox steps on a seeded tab: notify the new items,
// then the seen set becomes the keys of this load.
function loader(wants = all) {
  let seen = new Set()
  return (items) => {
    const fresh = newNotifications(seen, items, wants)
    seen = seenKeys(items)
    return keys(fresh)
  }
}

test('two problems in one project notify twice, a third later once more', () => {
  const load = loader()
  const two = [problem(12), problem(13)]
  assert.deepEqual(load(two), ['team/shop/problem/3/12/open', 'team/shop/problem/3/13/open'])
  assert.deepEqual(load(two), [])
  assert.deepEqual(load([...two, problem(14)]), ['team/shop/problem/3/14/open'])
})

test('a category turned off raises nothing', () => {
  const load = loader(only('problem'))
  assert.deepEqual(load([warning(), problem(12)]), ['team/shop/problem/3/12/open'])
  assert.deepEqual(newNotifications(new Set(), [warning()], only('question')), [])
  // wants is false for every category while the switch is off.
  assert.deepEqual(newNotifications(new Set(), [warning(), problem(12)], () => false), [])
})

test('an item that leaves the Inbox and comes back notifies again', () => {
  const load = loader()
  assert.deepEqual(load([problem(12), proposal()]), keys([problem(12), proposal()]))
  // Resolved: gone from the Inbox, and forgotten.
  assert.deepEqual(load([proposal()]), [])
  assert.deepEqual(load([problem(12, { reason: 'CI failed again' }), proposal()]), ['team/shop/problem/3/12/open'])
})

test('a key listed twice notifies once', () => {
  assert.deepEqual(keys(newNotifications(new Set(), [problem(12), problem(12, { reason: 'dup' })], all)), ['team/shop/problem/3/12/open'])
})

test('pilot errors never notify, failed tasks only when their category is picked', () => {
  assert.deepEqual(newNotifications(new Set(), [pilotError], all), [])
  assert.deepEqual(newNotifications(new Set(), [task('failed')], only('question', 'problem')), [])
  assert.deepEqual(keys(newNotifications(new Set(), [task('failed')], only('failed'))), ['team/shop/task/shop-impl-12'])
})

test('seenKeys: exactly the keys of the load', () => {
  assert.deepEqual([...seenKeys([problem(12), problem(12), proposal()])], ['team/shop/problem/3/12/open', 'team/shop/proposal/3/3'])
  assert.equal(seenKeys([]).size, 0)
})

test('problem texts: stuck issue, held issue and paused milestone', () => {
  assert.deepEqual(notificationText(problem(712, { title: 'Cart totals', reason: 'Review found 2 errors' })), {
    title: '#712 stuck · Cart totals',
    body: 'Review found 2 errors',
  })
  assert.deepEqual(notificationText(problem(712, { title: 'Cart totals', hold: true, pauseKind: 'ciRed', reason: 'job lint failed' })), {
    title: '#712 held · Cart totals',
    body: 'job lint failed',
  })
  assert.deepEqual(notificationText(problem(0, { pauseKind: 'stopped', reason: 'stopped by owner' })), {
    title: 'Milestone paused · Checkout',
    body: 'stopped by owner',
  })
  assert.equal(notificationText(problem(undefined)).title, 'Milestone paused · Checkout')
  // The title falls back to the project.
  assert.equal(notificationText(problem(12, { title: '' })).title, '#12 stuck · shop')
  assert.equal(notificationText(problem(0, { title: '' })).title, 'Milestone paused · shop')
})

test('a UTC time in the body shows in local time', (t) => {
  const reason = 'Retry at 2026-11-21 14:05 UTC'
  const item = problem(12, { reason })
  const { title, body } = notificationText(item)
  assert.equal(body, reason)
  // Without a service worker: new Notification, as the page shows it.
  const shown = []
  globalThis.Notification = class {
    constructor(title, options) {
      shown.push({ title, ...options })
    }
  }
  t.after(() => delete globalThis.Notification)
  t.mock.timers.enable({ apis: ['Date'], now })
  showDeskNotification(title, body, item.key, { item })
  assert.equal(shown.length, 1)
  assert.equal(shown[0].title, '#12 stuck · Issue 12')
  assert.equal(shown[0].tag, 'team/shop/problem/3/12/open')
  assert.equal(shown[0].body, localTimesText(reason, now))
  assert.doesNotMatch(shown[0].body, /UTC/)
})

test('texts of pauses, holds, proposals, questions, tasks, companions and budget warnings', () => {
  assert.deepEqual(notificationText(pause()), {
    title: 'Autopilot paused · Checkout',
    body: 'CI is red · #12\nCI failed on #12',
  })
  assert.deepEqual(notificationText(pause({ pauseKind: undefined, issueIID: undefined, reason: 'stopped' })), {
    title: 'Autopilot paused · Checkout',
    body: 'stopped',
  })
  assert.deepEqual(notificationText(pause({ hold: true, reason: 'job `lint` failed' })), {
    title: 'Issue #12 held · Checkout',
    body: 'CI is red\njob `lint` failed',
  })
  assert.deepEqual(notificationText(proposal()), { title: 'Proposal P3 · shop', body: 'Split the cart issue' })
  const prompt = { namespace: 'team', project: 'shop', kind: 'prompt', task: 'spec', promptID: 'Q1', title: 'Retry?', reason: 'mainHealth', key: 'k', notify: 'question' }
  assert.deepEqual(notificationText(prompt), { title: 'Question Q1 · shop', body: 'Retry?' })
  assert.deepEqual(notificationText(task('needs-input')), { title: '#12 needs input', body: 'shop · Cart totals' })
  assert.deepEqual(notificationText(task('needs-input', { issueIID: undefined, title: '' })), { title: 'Needs input', body: 'shop' })
  assert.deepEqual(notificationText(task('failed')), { title: '#12 failed', body: 'shop · Cart totals' })
  assert.deepEqual(notificationText(task('blocked', { issueIID: undefined })), { title: 'Blocked', body: 'shop · Cart totals' })
  const companion = {
    namespace: 'team',
    project: 'shop',
    kind: 'companionReady',
    issueIID: 12,
    title: 'group/infra!7',
    reason: 'ready to merge: release 2026.11.4 runs',
    key: 'team/shop/companionReady/12/group/infra!7',
    notify: 'companion',
  }
  assert.deepEqual(notificationText(companion), {
    title: 'Companion ready to merge · shop',
    body: 'group/infra!7 · companion of #12\nready to merge: release 2026.11.4 runs',
  })
  assert.deepEqual(notificationText({ ...companion, issueIID: undefined, reason: '' }), {
    title: 'Companion ready to merge · shop',
    body: 'group/infra!7',
  })
  assert.deepEqual(notificationText(warning()), {
    title: 'Budget at 80 % · #12 · shop',
    body: "Cart totals\n80 % of the issue's budget of $20.00 spent",
  })
})
