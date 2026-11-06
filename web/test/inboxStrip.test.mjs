// Tests of src/inboxStrip.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { pausedStrip } from '../src/inboxStrip.ts'

const now = Date.parse('2026-11-06T12:00:00Z')
const pause = (iid, pausedSince) => ({ namespace: 'team', project: 'shop', kind: 'pause', milestoneIID: iid, title: `M${iid}`, pausedSince })

test('pausedStrip: counts, longest first', () => {
  const s = pausedStrip(
    [pause(1, '2026-11-06T11:00:00Z'), { kind: 'task', namespace: 'team', project: 'shop' }, pause(2, '2026-11-06T09:00:00Z')],
    now,
  )
  assert.equal(s.count, 2)
  assert.equal(s.longestMs, 3 * 3600_000)
  assert.deepEqual(s.milestones.map((m) => m.milestoneIID), [2, 1])
})

test('pausedStrip: a pause without pausedSince counts but is not the longest', () => {
  const s = pausedStrip([pause(1), pause(2, '2026-11-06T11:30:00Z')], now)
  assert.equal(s.count, 2)
  assert.equal(s.longestMs, 30 * 60_000)
  assert.deepEqual(s.milestones.map((m) => m.milestoneIID), [2, 1])
  assert.equal(pausedStrip([pause(1)], now).longestMs, 0)
})

test('pausedStrip: nothing paused', () => {
  assert.deepEqual(pausedStrip([], now), { count: 0, longestMs: 0, milestones: [] })
})
