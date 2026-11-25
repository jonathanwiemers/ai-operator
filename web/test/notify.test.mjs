// Tests of src/notify.ts: npm test (node --test, no extra dependencies).
// The module reads localStorage and Notification when it loads, so each
// test stubs them and imports a fresh copy.
import assert from 'node:assert/strict'
import { test } from 'node:test'

let n = 0
// load stubs the browser and imports notify.ts anew. storage: the stored
// value, or 'throw' for a storage that throws (private mode).
async function load({ storage = null, permission = 'granted', grant = permission } = {}) {
  const data = new Map()
  if (storage !== null && storage !== 'throw') data.set('desk-notify', storage)
  const fail = () => {
    throw new Error('SecurityError')
  }
  globalThis.window = globalThis
  globalThis.localStorage =
    storage === 'throw'
      ? { getItem: fail, setItem: fail, removeItem: fail }
      : { getItem: (k) => data.get(k) ?? null, setItem: (k, v) => data.set(k, String(v)), removeItem: (k) => data.delete(k) }
  globalThis.Notification = { permission, requestPermission: async () => (globalThis.Notification.permission = grant) }
  const m = await import(`../src/notify.ts?case=${n++}`)
  return { m, saved: () => (data.has('desk-notify') ? JSON.parse(data.get('desk-notify')) : null) }
}

const defaults = {
  problem: true,
  question: true,
  proposal: true,
  companion: true,
  budget: true,
  failed: false,
  milestoneStarted: true,
  milestoneDone: true,
  release: true,
}

test('notifyCategories: the dialog order, the event categories need history', async () => {
  const { notifyCategories } = await import('../src/notifyCategories.ts')
  assert.deepEqual(
    notifyCategories.map((c) => c.label),
    ['Problems', 'Questions', 'Proposals', 'Companions ready', 'Budget warnings', 'Milestone started', 'Milestone done', 'Releases', 'Failed or blocked tasks'],
  )
  assert.deepEqual(new Set(notifyCategories.map((c) => c.category)), new Set(Object.keys(defaults)))
  assert.deepEqual(notifyCategories.filter((c) => c.history).map((c) => c.category), ['milestoneStarted', 'milestoneDone', 'release'])
  for (const c of notifyCategories) assert.ok(c.description.endsWith('.'), c.category)
})

test('nothing stored: off with the defaults, history unknown', async () => {
  const { m } = await load()
  assert.equal(m.notify.enabled, false)
  assert.deepEqual({ ...m.notify.categories }, defaults)
  assert.equal(m.notify.history, null)
})

test('the old value "on" reads as on with the defaults', async () => {
  const { m } = await load({ storage: 'on' })
  assert.equal(m.notify.enabled, true)
  assert.deepEqual({ ...m.notify.categories }, defaults)
  assert.equal(m.wants('problem'), true)
  assert.equal(m.wants('failed'), false)
})

test('a value that fails to parse reads as off with the defaults', async () => {
  for (const storage of ['{"on": tru', 'off', '42', 'null', '[]']) {
    const { m } = await load({ storage })
    assert.equal(m.notify.enabled, false, storage)
    assert.deepEqual({ ...m.notify.categories }, defaults, storage)
  }
})

test('stored JSON: on and the changed categories; unknown or non-boolean ones are ignored', async () => {
  const { m } = await load({ storage: JSON.stringify({ on: true, categories: { failed: true, budget: false, nope: true, release: 'no' } }) })
  assert.equal(m.notify.enabled, true)
  assert.deepEqual({ ...m.notify.categories }, { ...defaults, failed: true, budget: false })
})

test('setCategory stores only the changes, and they survive a reload', async () => {
  const { m, saved } = await load()
  m.setCategory('failed', true)
  m.setCategory('budget', false)
  m.setCategory('problem', true)
  assert.deepEqual(saved(), { on: false, categories: { failed: true, budget: false } })
  m.setCategory('budget', true)
  assert.deepEqual(saved(), { on: false, categories: { failed: true } })
  const again = await load({ storage: JSON.stringify(saved()) })
  assert.deepEqual({ ...again.m.notify.categories }, { ...defaults, failed: true })
})

test('enableNotify turns on only when granted; disableNotify keeps the categories', async () => {
  const denied = await load({ permission: 'default', grant: 'denied' })
  await denied.m.enableNotify()
  assert.equal(denied.m.notify.permission, 'denied')
  assert.equal(denied.m.notifyActive(), false)

  const { m, saved } = await load({ permission: 'default', grant: 'granted' })
  m.setCategory('failed', true)
  await m.enableNotify()
  assert.equal(m.notifyActive(), true)
  assert.deepEqual(saved(), { on: true, categories: { failed: true } })
  m.disableNotify()
  assert.equal(m.notifyActive(), false)
  assert.deepEqual(saved(), { on: false, categories: { failed: true } })
})

test('a storage that throws keeps the choices for the tab only', async () => {
  const { m } = await load({ storage: 'throw' })
  assert.equal(m.notify.enabled, false)
  await m.enableNotify()
  m.setCategory('failed', true)
  assert.equal(m.notifyActive(), true)
  assert.equal(m.wants('failed'), true)
})

test('wants: false when off, without permission or with the category off', async () => {
  const off = await load({ storage: JSON.stringify({ on: false }) })
  assert.equal(off.m.wants('problem'), false)

  for (const permission of ['default', 'denied']) {
    const { m } = await load({ storage: 'on', permission })
    assert.equal(m.wants('problem'), false, permission)
  }

  const { m } = await load({ storage: 'on' })
  assert.equal(m.wants('question'), true)
  m.setCategory('question', false)
  assert.equal(m.wants('question'), false)
  assert.equal(m.wants('failed'), false)
})

test('wantsEvents: one event category, unless there is no history database', async () => {
  const off = await load({ storage: JSON.stringify({ on: false }) })
  assert.equal(off.m.wantsEvents(), false)

  const { m } = await load({ storage: 'on' })
  assert.equal(m.wantsEvents(), true)
  m.setCategory('milestoneStarted', false)
  m.setCategory('milestoneDone', false)
  assert.equal(m.wantsEvents(), true)
  m.setCategory('release', false)
  assert.equal(m.wantsEvents(), false, 'only Inbox categories left')
  m.setCategory('release', true)
  m.notify.history = true
  assert.equal(m.wantsEvents(), true)
  m.notify.history = false
  assert.equal(m.wantsEvents(), false, 'no history database')
})

test('without Notification the permission is unsupported and nothing turns on', async () => {
  const { m } = await load()
  delete globalThis.Notification
  m.refreshPermission()
  assert.equal(m.notify.permission, 'unsupported')
  await m.enableNotify()
  assert.equal(m.notifyActive(), false)
})

test('openNotifyDialog opens the shared dialog state', async () => {
  const { m } = await load()
  assert.equal(m.notifyDialog.open, false)
  m.openNotifyDialog()
  assert.equal(m.notifyDialog.open, true)
})
