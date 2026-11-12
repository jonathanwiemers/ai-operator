// Tests of src/chartData.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  OTHER,
  bucketLabel,
  colorSlots,
  formatTokens,
  formatUSD,
  seriesFromPoints,
  topKeys,
} from '../src/chartData.ts'

// norm drops the spaces ICU versions put around a range's dash.
const norm = (s) => s.replace(/\s*–\s*/g, '–')

function point(bucket, key, cost, extra = {}) {
  return {
    bucket,
    key,
    other: extra.other ?? false,
    totals: {
      costUSD: cost,
      costKnown: cost !== '',
      unknownCalls: extra.unknown ?? 0,
      unmeteredCalls: extra.unmetered ?? 0,
      oldBasisCalls: 0,
      inputTokens: extra.input ?? 0,
      outputTokens: extra.output ?? 0,
      cacheReadTokens: extra.cacheRead ?? 0,
      cacheWriteTokens: extra.cacheWrite ?? 0,
      calls: 1,
      subagentCalls: 0,
      durationMS: 0,
      checkpoints: 0,
    },
  }
}

test('topKeys: ranked by known cost over the range, ties by key, Other points left out', () => {
  const points = [
    point('2026-11-01', 'ns/a', '1.00'),
    point('2026-11-02', 'ns/a', '2.00'),
    point('2026-11-01', 'ns/b', '5.00'),
    point('2026-11-01', 'ns/c', '', { unknown: 3 }),
    point('2026-11-01', 'ns/d', '3.00'),
    point('2026-11-01', '', '9.00', { other: true }),
  ]
  assert.deepEqual(topKeys(points), ['ns/b', 'ns/a', 'ns/d', 'ns/c'])
  assert.deepEqual(topKeys(points, 2), ['ns/b', 'ns/a'])
  assert.deepEqual(topKeys([]), [])
})

test('topKeys: the legacy runs (key "", not other) are a key', () => {
  assert.deepEqual(topKeys([point('2026-11-01', '', '4.00'), point('2026-11-01', 'claude', '1.00')]), ['', 'claude'])
})

test('seriesFromPoints: buckets with gaps are 0, unknown and unmetered per bucket', () => {
  const buckets = ['2026-11-01', '2026-11-02', '2026-11-03', '2026-11-04']
  const points = [
    point('2026-11-01', 'review', '1.25', { unknown: 1 }),
    point('2026-11-03', 'review', '0.75'),
    point('2026-11-03', 'implement', '2.00', { unknown: 2, unmetered: 1 }),
    point('2026-10-31', 'review', '9.00'),
  ]
  assert.deepEqual(seriesFromPoints(buckets, points, 'cost'), {
    series: [
      { key: 'implement', values: [0, 0, 2, 0] },
      { key: 'review', values: [1.25, 0, 0.75, 0] },
    ],
    unknown: [1, 0, 2, 0],
    unmetered: [0, 0, 1, 0],
  })
})

test('seriesFromPoints: unknown cost counts 0, never NaN, and marks the bucket', () => {
  const set = seriesFromPoints(['2026-11-01'], [point('2026-11-01', 'cursor', '', { unknown: 4, unmetered: 4 })], 'cost')
  assert.deepEqual(set, { series: [{ key: 'cursor', values: [0] }], unknown: [4], unmetered: [4] })
})

test('seriesFromPoints: top 6 plus Other, which takes the rest and the server\'s other points', () => {
  const b = ['2026-11-01', '2026-11-02']
  const points = []
  for (let i = 1; i <= 8; i++) points.push(point(b[0], `k${i}`, `${i}.00`))
  points.push(point(b[1], '', '0.50', { other: true }))
  points.push(point(b[1], 'k1', '0.10'))
  const set = seriesFromPoints(b, points, 'cost')
  assert.deepEqual(
    set.series.map((s) => s.key),
    ['k8', 'k7', 'k6', 'k5', 'k4', 'k3', OTHER],
  )
  // k1 (1.00 + 0.10) and k2 (2.00) fold into Other, as does the server's.
  assert.deepEqual(set.series.at(-1).values, [3, 0.6])
  assert.deepEqual(set.series[0].values, [8, 0])
})

test('seriesFromPoints: no Other series when every key fits', () => {
  const set = seriesFromPoints(['2026-11-01'], [point('2026-11-01', 'a', '1.00')], 'cost')
  assert.deepEqual(set.series.map((s) => s.key), ['a'])
})

test('seriesFromPoints: tokens are the metered tokens of every type', () => {
  const set = seriesFromPoints(
    ['2026-11-01'],
    [point('2026-11-01', 'a', '', { input: 100, output: 20, cacheRead: 1000, cacheWrite: 5 })],
    'tokens',
  )
  assert.deepEqual(set.series, [{ key: 'a', values: [1125] }])
})

test('seriesFromPoints: float sums are rounded to 4 decimals', () => {
  const pts = [point('2026-11-01', 'a', '0.10'), point('2026-11-01', 'a', '0.20')]
  assert.deepEqual(seriesFromPoints(['2026-11-01'], pts, 'cost').series[0].values, [0.3])
})

test('bucketLabel: day, week and month', () => {
  assert.equal(bucketLabel('2026-11-03', 'day', 'en-GB'), '3 Nov')
  assert.equal(norm(bucketLabel('2026-11-02', 'week', 'en-GB')), '2–8 Nov')
  assert.equal(bucketLabel('2026-11-01', 'month', 'en-GB'), 'Nov 2026')
  // A week across two months names both.
  assert.equal(norm(bucketLabel('2026-10-26', 'week', 'en-GB')), '26 Oct–1 Nov')
  assert.equal(bucketLabel('nope', 'day', 'en-GB'), 'nope')
})

test('bucketLabel: a DST week is seven days in any browser zone', () => {
  const saved = process.env.TZ
  try {
    // 2026-10-25 ends summer time in Europe, 2026-11-01 in the US, and
    // 2026-10-04 begins it in Sydney.
    for (const tz of ['Europe/Berlin', 'America/New_York', 'Australia/Sydney', 'Pacific/Kiritimati', 'UTC']) {
      process.env.TZ = tz
      assert.equal(norm(bucketLabel('2026-10-19', 'week', 'en-GB')), '19–25 Oct', tz)
      assert.equal(norm(bucketLabel('2026-10-26', 'week', 'en-GB')), '26 Oct–1 Nov', tz)
      assert.equal(norm(bucketLabel('2026-09-28', 'week', 'en-GB')), '28 Sept–4 Oct', tz)
      assert.equal(bucketLabel('2026-10-25', 'day', 'en-GB'), '25 Oct', tz)
      assert.equal(bucketLabel('2026-03-01', 'month', 'en-GB'), 'Mar 2026', tz)
    }
  } finally {
    if (saved === undefined) delete process.env.TZ
    else process.env.TZ = saved
  }
})

test('colorSlots: one of six tokens per key, Other its own', () => {
  const slots = colorSlots(['ns/a', 'ns/b', 'ns/c', 'ns/d', 'ns/e', 'ns/f', OTHER])
  const tokens = [...slots.values()]
  assert.equal(slots.get(OTHER), '--chart-other')
  assert.equal(new Set(tokens).size, 7)
  for (const t of tokens) assert.match(t, /^--chart-([1-6]|other)$/)
})

test('colorSlots: stable across key orders and reloads', () => {
  const keys = ['implement', 'review', 'fix', 'ci', OTHER, 'spec']
  const a = colorSlots(keys)
  const b = colorSlots([...keys].reverse())
  const c = colorSlots(['spec', 'ci', 'implement', OTHER, 'fix', 'review'])
  for (const k of keys) {
    assert.equal(b.get(k), a.get(k), k)
    assert.equal(c.get(k), a.get(k), k)
  }
  // The same key, the same slot: it depends only on the key set.
  assert.deepEqual([...colorSlots(['claude', 'cursor'])], [...colorSlots(['claude', 'cursor'])])
})

test('colorSlots: adding a key keeps the others', () => {
  const before = colorSlots(['claude', 'cursor'])
  const after = colorSlots(['claude', 'cursor', 'research'])
  assert.equal(after.get('claude'), before.get('claude'))
  assert.equal(after.get('cursor'), before.get('cursor'))
  assert.notEqual(after.get('research'), after.get('claude'))
  assert.notEqual(after.get('research'), after.get('cursor'))
})

test('colorSlots: more than six keys share slots, no key is left out', () => {
  const keys = Array.from({ length: 9 }, (_, i) => `k${i}`)
  const slots = colorSlots(keys)
  assert.equal(slots.size, 9)
  assert.equal(new Set([...slots.values()]).size, 6)
})

test('formatUSD: two decimals, grouped', () => {
  assert.equal(formatUSD(0), '$0.00')
  assert.equal(formatUSD(0.004), '$0.00')
  assert.equal(formatUSD(12.345), '$12.35')
  assert.equal(formatUSD(1234.5), '$1,234.50')
  assert.equal(formatUSD(Number.NaN), '$0.00')
})

test('formatTokens: k and M', () => {
  assert.equal(formatTokens(950), '950')
  assert.equal(formatTokens(1200), '1.2k')
  assert.equal(formatTokens(3_400_000), '3.4M')
})
