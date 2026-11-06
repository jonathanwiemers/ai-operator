// Tests of src/pauses.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { pauseDuration, pausesLine, pausesSummary, resolverLabel } from '../src/pauses.ts'

const t0 = Date.parse('2026-11-05T10:00:00Z')
const at = (min) => new Date(t0 + min * 60000).toISOString()

// The view was made at t0 + 60 min: the open ciRed pause (since t0 + 30)
// counts 30 min there.
const history = {
  pauses: [
    { kind: 'ciRed', reason: 'CI of !735\n is red', item: 88, since: at(30), seconds: 1800 },
    { kind: 'stuck', reason: '#88 waits for the skipped #83', item: 88, since: at(-200), until: at(-100), resolvedBy: 'owner', seconds: 6000 },
    { kind: 'ciRed', since: at(-300), until: at(-280), resolvedBy: 'selfHealed', seconds: 1200 },
    { kind: 'unknown', reason: 'old', since: at(-400), until: at(-390), seconds: 600 },
  ],
  totals: {
    count: 4,
    pausedSec: 9600,
    byKind: { ciRed: { count: 2, sec: 3000 }, stuck: { count: 1, sec: 6000 }, unknown: { count: 1, sec: 600 } },
    meanResumeSec: 2600,
    byResolver: { owner: 1, selfHealed: 1, unknown: 1 },
  },
}

test('an open pause counts on up to now', () => {
  const s = pausesSummary(history, t0 + 60 * 60000)
  assert.equal(s.pausedSec, 9600)
  const later = pausesSummary(history, t0 + 120 * 60000)
  assert.equal(later.pausedSec, 9600 + 3600)
  assert.equal(later.rows[0].duration, '1h 30m')
  assert.equal(later.rows[0].resolver, 'open')
  assert.equal(later.rows[0].open, true)
  assert.equal(later.kinds.find((k) => k.kind === 'ciRed').sec, 3000 + 3600)
  assert.equal(pausesLine(later), 'paused 3h 40m in 4 pauses · mean 43m to resume')
})

test('a clock behind the view does not count back', () => {
  const s = pausesSummary(history, t0)
  assert.equal(s.pausedSec, 9600)
  assert.equal(s.rows[0].duration, '30m')
})

test('kinds are sorted by time paused', () => {
  const s = pausesSummary(history, t0 + 60 * 60000)
  assert.deepEqual(
    s.kinds.map((k) => [k.kind, k.count, k.sec]),
    [
      ['stuck', 1, 6000],
      ['ciRed', 2, 3000],
      ['unknown', 1, 600],
    ],
  )
  // Two more hours of the open ciRed pause put it first.
  const later = pausesSummary(history, t0 + 180 * 60000)
  assert.deepEqual(later.kinds.map((k) => k.kind), ['ciRed', 'stuck', 'unknown'])
})

test('resolvers are counted and named', () => {
  const s = pausesSummary(history, t0 + 60 * 60000)
  assert.deepEqual(
    s.resolvers.map((r) => [r.label, r.count]),
    [
      ['owner', 1],
      ['self-healed', 1],
      ['unknown', 1],
    ],
  )
  assert.equal(resolverLabel('pilot'), 'pilot')
  assert.equal(resolverLabel(undefined), 'unknown')
})

test('list rows have the kind, reason as one line, item and resolver', () => {
  const s = pausesSummary(history, t0 + 60 * 60000)
  assert.deepEqual(
    s.rows.map((r) => [r.kind, r.reason, r.item, r.duration, r.resolver]),
    [
      ['ciRed', 'CI of !735 is red', '#88', '30m', 'open'],
      ['stuck', '#88 waits for the skipped #83', '#88', '1h 40m', 'owner'],
      ['ciRed', '', '', '20m', 'self-healed'],
      ['unknown', 'old', '', '10m', 'unknown'],
    ],
  )
})

test('no mean while no pause has ended', () => {
  const s = pausesSummary(
    {
      pauses: [{ kind: 'owner', since: at(0), seconds: 60 }],
      totals: { count: 1, pausedSec: 60, byKind: { owner: { count: 1, sec: 60 } }, meanResumeSec: 0, byResolver: {} },
    },
    t0 + 60000,
  )
  assert.equal(s.meanResumeSec, undefined)
  assert.deepEqual(s.resolvers, [])
  assert.equal(pausesLine(s), 'paused 1m in 1 pause')
})

test('no summary without pauses', () => {
  assert.equal(pausesSummary(undefined, t0), undefined)
  assert.equal(pausesSummary(null, t0), undefined)
  assert.equal(
    pausesSummary({ pauses: [], totals: { count: 0, pausedSec: 0, byKind: {}, meanResumeSec: 0, byResolver: {} } }, t0),
    undefined,
  )
})

test('pause durations', () => {
  assert.equal(pauseDuration(0), '<1m')
  assert.equal(pauseDuration(59), '<1m')
  assert.equal(pauseDuration(47 * 60), '47m')
  assert.equal(pauseDuration(3 * 3600), '3h')
  assert.equal(pauseDuration(3 * 3600 + 600), '3h 10m')
  assert.equal(pauseDuration(2 * 86400 + 4 * 3600 + 59), '2d 4h')
  assert.equal(pauseDuration(86400), '1d')
})
