// Tests of src/pushNotify.ts: npm test (node --test).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { pushNotification } from '../src/pushNotify.ts'

const item = {
  namespace: 'team',
  project: 'shop',
  kind: 'problem',
  task: 'shop-spec-m3',
  milestoneIID: 3,
  issueIID: 12,
  title: 'Checkout',
  reason: 'CI failed',
  at: '2026-11-03T10:00:00Z',
  key: 'team/shop/problem/3/12/open',
  notify: 'problem',
}
const ev = (data) => ({ id: 41, at: '2026-11-25T10:00:00Z', namespace: 'team', project: 'ai-operator', milestoneIID: 3, kind: 'release.ended', summary: 's', data })

test('an item: the in-tab title, the item key as tag, data.open', () => {
  const n = pushNotification({ item })
  assert.equal(n.title, '#12 stuck · Checkout')
  assert.equal(n.options.tag, item.key)
  assert.equal(n.options.icon, '/logo.svg')
  assert.deepEqual(n.options.data, { open: { item } })
  assert.match(n.options.body, /CI failed/)
})

test('a release event: Released and Failed', () => {
  const r = ev({ state: 'Released', version: '2026.12.1', title: 'Desk' })
  const n = pushNotification({ event: r })
  assert.equal(n.title, 'Released 2026.12.1 · ai-operator')
  assert.equal(n.options.tag, 'event/41')
  assert.deepEqual(n.options.data, { open: { event: r } })
  const f = pushNotification({ event: ev({ state: 'Failed', reason: 'pipeline failed' }) })
  assert.equal(f.title, 'Release failed · ai-operator')
})

test('a milestone.started event', () => {
  const e = { ...ev({ title: 'Checkout' }), kind: 'milestone.started' }
  const n = pushNotification({ event: e })
  assert.equal(n.title, 'Milestone started · ai-operator')
  assert.equal(n.options.body, 'Checkout')
})

test('null for an item without namespace, project or kind, and for a non-object', () => {
  for (const k of ['namespace', 'project', 'kind']) {
    const { [k]: _, ...rest } = item
    assert.equal(pushNotification({ item: rest }), null, k)
  }
  for (const v of [undefined, null, 'x', 3, [], {}]) assert.equal(pushNotification(v), null)
})
