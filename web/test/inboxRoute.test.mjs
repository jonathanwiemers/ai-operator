// Tests of src/inboxRoute.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { inboxRoute, pinnedAnchor } from '../src/inboxRoute.ts'

const item = (kind, over = {}) => ({ namespace: 'team', project: 'shop', kind, ...over })

const thread = (problem) => ({ name: 'problem', params: { namespace: 'team', name: 'shop', iid: '3', problem } })

const cockpit = (hash) => ({ name: 'stream-milestone', params: { namespace: 'team', name: 'shop', iid: '3' }, hash })

test('inboxRoute: pauses and problems open their problem thread in the Cockpit', () => {
  assert.deepEqual(inboxRoute(item('pause', { task: 'shop-spec-m3', milestoneIID: 3 })), thread('issue-0'))
  assert.deepEqual(inboxRoute(item('pause', { milestoneIID: 3, issueIID: 12 })), thread('issue-12'))
  assert.deepEqual(inboxRoute(item('problem', { milestoneIID: 3, issueIID: 12 })), thread('issue-12'))
})

test('inboxRoute: proposals open the Cockpit at their prompt, else at the pinned cards', () => {
  assert.deepEqual(inboxRoute(item('proposal', { task: 'shop-pilot', milestoneIID: 3 })), cockpit(pinnedAnchor))
  assert.equal(pinnedAnchor, '#pinned')
  assert.deepEqual(inboxRoute(item('proposal', { task: 'shop-pilot', milestoneIID: 3, promptID: 'Q7' })), cockpit('#prompt-Q7'))
})

test('inboxRoute: no milestone entry leads to a removed tab', () => {
  for (const kind of ['pause', 'problem', 'proposal', 'prompt']) {
    const r = inboxRoute(item(kind, { task: 'shop-spec-m3', milestoneIID: 3, issueIID: 12, promptID: 'Q4' }))
    assert.equal(r.query, undefined, kind)
  }
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

test('inboxRoute: prompts open the Cockpit at their card, without an ID their problem thread, an agent question its task page', () => {
  assert.deepEqual(
    inboxRoute(item('prompt', { task: 'shop-spec-m3', milestoneIID: 3, issueIID: 12, promptID: 'Q4', reason: 'mainHealth' })),
    cockpit('#prompt-Q4'),
  )
  assert.deepEqual(inboxRoute(item('prompt', { milestoneIID: 3, issueIID: 12, reason: 'mainHealth' })), thread('issue-12'))
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
  // A notification click, from the service worker's message or a plain
  // Notification, and a window opened on ?open= (deskWorker.ts).
  const worker = read('../src/deskWorker.ts')
  assert.match(worker, /router\?\.push\(inboxRoute\(open\.item\)\)/)
  assert.match(worker, /r\.replace\(open \? inboxRoute\(open\.item\)/)
  assert.doesNotMatch(read('../src/App.vue'), /router\.push\(/)
  // The open items are InboxList's rows (the Inbox and the Overview Cockpit).
  const inbox = read('../src/components/InboxList.vue')
  const list = inbox.slice(inbox.indexOf('<ul'), inbox.indexOf('</ul>'))
  const links = list.match(/:to="[^"]*"/g)
  assert.ok(links.length >= 3)
  for (const to of links) assert.equal(to, ':to="inboxRoute(item)"')
})
