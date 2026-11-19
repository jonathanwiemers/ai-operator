// Tests of src/metricsPauses.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  longPauseRows,
  metricsFilterNote,
  pauseKindsChart,
  pausedHoursChart,
  pauseProjectRows,
  pausesChartSummary,
  pauseTables,
  pauseTiles,
} from '../src/metricsPauses.ts'

// The milestone's scenario: two CI pauses of 2 h and 30 min, resumed by the
// owner, and a budget pause open for 5 h.
const view = {
  range: '30d',
  from: '2026-11-01',
  to: '2026-11-03',
  bucket: 'day',
  tz: 'Europe/Berlin',
  buckets: [
    { bucket: '2026-11-01', count: 1, pausedSec: 7200, byKind: { ciRed: { count: 1, sec: 7200 } } },
    { bucket: '2026-11-02', count: 0, pausedSec: 0, byKind: {} },
    { bucket: '2026-11-03', count: 2, pausedSec: 1800 + 18000, byKind: { ciRed: { count: 1, sec: 1800 }, budget: { count: 1, sec: 18000 } } },
  ],
  totals: {
    count: 3,
    pausedSec: 27000,
    byKind: { ciRed: { count: 2, sec: 9000 }, budget: { count: 1, sec: 18000 } },
    meanResumeSec: 4500,
    byResolver: { owner: 2 },
    byVia: { command: 2 },
  },
  openNow: 1,
  byProject: [
    { namespace: 'team-a', project: 'shop', count: 2, pausedSec: 25200 },
    { namespace: 'team-b', project: 'api', count: 1, pausedSec: 1800 },
  ],
  longest: [
    { namespace: 'team-a', project: 'shop', milestoneIID: 14, kind: 'budget', reason: 'budget\n used', seconds: 18000, since: '2026-11-03T07:00:00Z', open: true, deskURL: '/projects/team-a/shop/milestones/14?tab=pilot' },
    { namespace: 'team-a', project: 'shop', milestoneIID: 14, kind: 'ciRed', seconds: 7200, since: '2026-11-01T07:00:00Z', open: false, deskURL: '/projects/team-a/shop/milestones/14?tab=pilot' },
  ],
  truncated: false,
}

test('pauseKindsChart stacks the pauses per bucket by kind, most first', () => {
  const c = pauseKindsChart(view, 'en-GB')
  assert.deepEqual(c.labels, ['1 Nov', '2 Nov', '3 Nov'])
  assert.deepEqual(
    c.series.map((s) => [s.key, s.label, s.values, s.total]),
    [
      ['ciRed', 'CI is red', [1, 0, 1], 2],
      ['budget', 'The budget is used', [0, 0, 1], 1],
    ],
  )
  assert.deepEqual(c.unknown, [0, 0, 0])
  assert.notEqual(c.series[0].token, c.series[1].token)
})

test('pauseKindsChart folds kinds beyond six into Other', () => {
  const kinds = ['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h']
  const byKind = Object.fromEntries(kinds.map((k, i) => [k, { count: 10 - i, sec: 60 }]))
  const c = pauseKindsChart({ ...view, buckets: [{ bucket: '2026-11-01', count: 0, pausedSec: 0, byKind }] }, 'en-GB')
  assert.equal(c.series.length, 7)
  assert.equal(c.series[6].label, 'Other')
  assert.equal(c.series[6].token, '--chart-other')
  assert.deepEqual(c.series[6].values, [4 + 3])
  assert.equal(c.series[0].label, 'a')
})

test('pauseKindsChart of no buckets has no series', () => {
  const c = pauseKindsChart({ ...view, buckets: [] })
  assert.deepEqual(c.series, [])
  assert.deepEqual(c.labels, [])
})

test('pausedHoursChart is one series of hours per bucket', () => {
  const c = pausedHoursChart(view, 'en-GB')
  assert.equal(c.series.length, 1)
  assert.deepEqual(c.series[0].values, [2, 0, 5.5])
  assert.equal(c.series[0].total, 7.5)
  assert.match(pausesChartSummary('Paused hours per day', c, 'hours'), /^Paused hours per day, 1 Nov to 3 Nov: 7\.5 h in total\. The table/)
  assert.match(pausesChartSummary('Pauses per day', pauseKindsChart(view, 'en-GB'), 'count'), /3 in total\. Most: CI is red, 2\./)
})

test('pauseTiles name pauses, paused time, mean time to resume and open now', () => {
  assert.deepEqual(
    pauseTiles(view).map((t) => [t.id, t.value]),
    [
      ['count', '3'],
      ['paused', '7h 30m'],
      ['mean', '1h 15m'],
      ['open', '1'],
    ],
  )
  const none = { ...view, totals: { count: 0, pausedSec: 0, byKind: {}, meanResumeSec: 0, byResolver: {}, byVia: {} }, openNow: 0 }
  assert.deepEqual(pauseTiles(none).map((t) => t.value), ['0', '—', '—', '0'])
})

test('pauseTables count by kind, resolver and via with the card labels', () => {
  const t = pauseTables(view)
  assert.deepEqual(t.kinds, [
    { kind: 'budget', label: 'The budget is used', count: 1, duration: '5h' },
    { kind: 'ciRed', label: 'CI is red', count: 2, duration: '2h 30m' },
  ])
  assert.deepEqual(t.resolvers, [{ resolver: 'owner', label: 'owner', count: 2 }])
  assert.deepEqual(t.vias, [{ via: 'command', label: 'command', count: 2 }])
  const labelled = pauseTables({ ...view, totals: { ...view.totals, byResolver: { selfHealed: 1, unknown: 1 }, byVia: { mergedByHand: 1, unknown: 1 } } })
  assert.deepEqual(labelled.resolvers.map((r) => r.label), ['self-healed', 'unknown'])
  assert.deepEqual(labelled.vias.map((r) => r.label), ['merged by hand', 'unknown'])
  assert.deepEqual(pauseTables({ ...view, totals: { ...view.totals, count: 0 } }), { kinds: [], resolvers: [], vias: [] })
})

test('pauseProjectRows and longPauseRows', () => {
  assert.deepEqual(pauseProjectRows(view), [
    { project: 'team-a/shop', count: 2, duration: '7h' },
    { project: 'team-b/api', count: 1, duration: '30m' },
  ])
  const rows = longPauseRows(view)
  assert.equal(rows.length, 2)
  assert.deepEqual(
    { ...rows[0], id: undefined },
    { id: undefined, project: 'team-a/shop', milestone: '%14', kind: 'The budget is used', reason: 'budget used', duration: '5h', open: true, to: '/projects/team-a/shop/milestones/14?tab=pilot' },
  )
  assert.equal(rows[1].reason, '')
  assert.notEqual(rows[0].id, rows[1].id)
})

test('metricsFilterNote', () => {
  assert.equal(metricsFilterNote, 'Kind and engine filter cost and tokens only')
})
