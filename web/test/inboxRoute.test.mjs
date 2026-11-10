// Tests of src/inboxRoute.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { inboxRoute } from '../src/inboxRoute.ts'

const item = (kind, over = {}) => ({ namespace: 'team', project: 'shop', kind, ...over })

test('inboxRoute: pauses and proposals open the milestone Pilot tab', () => {
  const want = { name: 'stream-milestone', params: { namespace: 'team', name: 'shop', iid: '3' }, query: { tab: 'pilot' } }
  assert.deepEqual(inboxRoute(item('pause', { task: 'shop-spec-m3', milestoneIID: 3 })), want)
  assert.deepEqual(inboxRoute(item('proposal', { task: 'shop-pilot', milestoneIID: 3 })), want)
})

test('inboxRoute: tasks open the task page, even with a milestone', () => {
  assert.deepEqual(inboxRoute(item('task', { task: 'shop-impl-12', milestoneIID: 3 })), {
    name: 'task',
    params: { namespace: 'team', name: 'shop', task: 'shop-impl-12' },
  })
})

test('inboxRoute: other items with a milestone open its stream', () => {
  assert.deepEqual(inboxRoute(item('task', { milestoneIID: 3 })), {
    name: 'stream-milestone',
    params: { namespace: 'team', name: 'shop', iid: '3' },
  })
})

test('inboxRoute: the rest opens the project stream', () => {
  const want = { name: 'stream-project', params: { namespace: 'team', name: 'shop' } }
  assert.deepEqual(inboxRoute(item('task')), want)
  assert.deepEqual(inboxRoute(item('pause')), want)
  assert.deepEqual(inboxRoute(item('proposal', { task: 'shop-pilot' })), want)
})

test('inboxRoute: prompts open the milestone Pilot tab or the task page, with the prompt hash', () => {
  assert.deepEqual(inboxRoute(item('prompt', { task: 'shop-spec-m3', milestoneIID: 3, promptID: 'Q4', reason: 'mainHealth' })), {
    name: 'stream-milestone',
    params: { namespace: 'team', name: 'shop', iid: '3' },
    query: { tab: 'pilot' },
    hash: '#prompt-Q4',
  })
  assert.deepEqual(inboxRoute(item('prompt', { task: 'shop-impl-12', milestoneIID: 3, promptID: 'Q1', reason: 'needsInput' })), {
    name: 'task',
    params: { namespace: 'team', name: 'shop', task: 'shop-impl-12' },
    hash: '#prompt-Q1',
  })
})

test('inboxRoute: a ready companion opens its home task, without one its milestone', () => {
  const ready = (over = {}) =>
    item('companionReady', { issueIID: 12, milestoneIID: 3, title: 'group/infra!7', url: 'https://gitlab.example/group/infra/-/merge_requests/7', ...over })
  assert.deepEqual(inboxRoute(ready({ task: 'shop-impl-12' })), {
    name: 'task',
    params: { namespace: 'team', name: 'shop', task: 'shop-impl-12' },
  })
  assert.deepEqual(inboxRoute(ready()), { name: 'stream-milestone', params: { namespace: 'team', name: 'shop', iid: '3' } })
  assert.deepEqual(inboxRoute(ready({ milestoneIID: undefined })), { name: 'stream-project', params: { namespace: 'team', name: 'shop' } })
})

test('inboxRoute: an issue budget warning opens its implement task, without one its milestone', () => {
  const warning = (over = {}) =>
    item('budgetWarning', { issueIID: 12, milestoneIID: 3, title: 'Cart totals', reason: "80 % of the issue's budget of $20.00 spent", ...over })
  assert.deepEqual(inboxRoute(warning({ task: 'shop-impl-12' })), {
    name: 'task',
    params: { namespace: 'team', name: 'shop', task: 'shop-impl-12' },
  })
  assert.deepEqual(inboxRoute(warning()), { name: 'stream-milestone', params: { namespace: 'team', name: 'shop', iid: '3' } })
})

// R15: the Inbox row and the notification lead to the same route.
test('inboxRoute: the Inbox rows and the notification click both route with it', () => {
  const read = (path) => readFileSync(new URL(path, import.meta.url), 'utf8')
  const app = read('../src/App.vue')
  assert.match(app, /router\.push\(inboxRoute\(item\)\)/)
  assert.doesNotMatch(app, /router\.push\(\{/)
  const inbox = read('../src/views/Inbox.vue')
  const list = inbox.slice(inbox.indexOf('<ul'), inbox.indexOf('</ul>'))
  const links = list.match(/:to="[^"]*"/g)
  assert.ok(links.length >= 3)
  for (const to of links) assert.equal(to, ':to="inboxRoute(item)"')
})
