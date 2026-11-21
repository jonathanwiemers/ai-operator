// Tests of src/planCreateAll.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createAll, uncreated } from '../src/planCreateAll.ts'

const drafts = (...created) => created.map((iid, i) => ({ key: i + 1, ...(iid ? { createdMilestoneIID: iid } : {}) }))

// fake is a create endpoint giving out increasing IIDs from 41; fail names
// the keys it refuses.
function fake(fail = []) {
  const calls = []
  let iid = 41
  const create = async (d) => {
    calls.push(d.key)
    await new Promise((r) => setTimeout(r, 1))
    if (fail.includes(d.key)) throw new Error(`GitLab refused draft ${d.key}`)
    const n = iid++
    return { iid: n, webURL: `https://gitlab.example/m/${n}` }
  }
  return { calls, create }
}

test('uncreated keeps list order and drops created drafts', () => {
  assert.deepEqual(uncreated(drafts(40, 0, 0)).map((d) => d.key), [2, 3])
  assert.deepEqual(uncreated([{ key: 3 }, { key: 1, createdMilestoneIID: 0 }]).map((d) => d.key), [3, 1])
})

test('createAll creates every draft in list order', async () => {
  const f = fake()
  const list = [{ key: 3 }, { key: 1 }, { key: 2 }]
  const r = await createAll(list, f.create)
  assert.deepEqual(f.calls, [3, 1, 2])
  assert.deepEqual(r, {
    created: [
      { key: 3, iid: 41, url: 'https://gitlab.example/m/41' },
      { key: 1, iid: 42, url: 'https://gitlab.example/m/42' },
      { key: 2, iid: 43, url: 'https://gitlab.example/m/43' },
    ],
    skipped: [],
  })
})

test('createAll skips created drafts', async () => {
  const f = fake()
  const r = await createAll(drafts(40, 0, 0), f.create)
  assert.deepEqual(f.calls, [2, 3])
  assert.deepEqual(r.created.map((c) => c.key), [2, 3])
  assert.equal(r.failed, undefined)
})

test('createAll stops at the first error and reports created, failed and skipped', async () => {
  const f = fake([2])
  const r = await createAll(drafts(0, 0, 0), f.create)
  assert.deepEqual(f.calls, [1, 2])
  assert.deepEqual(r.created, [{ key: 1, iid: 41, url: 'https://gitlab.example/m/41' }])
  assert.deepEqual(r.failed, { key: 2, message: 'GitLab refused draft 2' })
  assert.deepEqual(r.skipped, [3])
})

test('createAll reports a non-Error rejection as text', async () => {
  const r = await createAll(drafts(0), () => Promise.reject('offline'))
  assert.deepEqual(r, { created: [], failed: { key: 1, message: 'offline' }, skipped: [] })
})

test('createAll calls create sequentially, never in parallel', async () => {
  let running = 0, most = 0
  const r = await createAll(drafts(0, 0, 0, 0), async (d) => {
    most = Math.max(most, ++running)
    await new Promise((res) => setTimeout(res, 2))
    running--
    return { iid: d.key, webURL: '' }
  })
  assert.equal(most, 1)
  assert.equal(r.created.length, 4)
})

test('createAll with nothing to create calls nothing', async () => {
  const f = fake()
  assert.deepEqual(await createAll(drafts(40, 41), f.create), { created: [], skipped: [] })
  assert.deepEqual(f.calls, [])
})
