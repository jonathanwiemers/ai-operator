// Tests of src/inboxRoute.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { inboxRoute } from '../src/inboxRoute.ts'

const item = (kind, over = {}) => ({ namespace: 'team', project: 'shop', kind, ...over })

test('inboxRoute: pauses and proposals open the milestone stream', () => {
  const want = { name: 'stream-milestone', params: { namespace: 'team', name: 'shop', iid: '3' } }
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

test('inboxRoute: prompts open the milestone stream or the task page, with the prompt hash', () => {
  assert.deepEqual(inboxRoute(item('prompt', { task: 'shop-spec-m3', milestoneIID: 3, promptID: 'Q4', reason: 'mainHealth' })), {
    name: 'stream-milestone',
    params: { namespace: 'team', name: 'shop', iid: '3' },
    hash: '#prompt-Q4',
  })
  assert.deepEqual(inboxRoute(item('prompt', { task: 'shop-impl-12', milestoneIID: 3, promptID: 'Q1', reason: 'needsInput' })), {
    name: 'task',
    params: { namespace: 'team', name: 'shop', task: 'shop-impl-12' },
    hash: '#prompt-Q1',
  })
})
