// Tests of src/chartData.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  OTHER,
  bucketLabel,
  chartSummary,
  breakdownRows,
  breakdownSummary,
  chartTokens,
  colorSlots,
  cumulativeMonth,
  formatTokens,
  formatUSD,
  monthSummary,
  readChartColors,
  seriesFromPoints,
  seriesLabel,
  stackedChart,
  thisMonth,
  tokenSeries,
  tokensChart,
  tokensSummary,
  topKeys,
  unknownNote,
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

test('seriesLabel: Other, legacy runs and keys', () => {
  assert.equal(seriesLabel(OTHER), 'Other')
  assert.equal(seriesLabel(''), 'before calls')
  assert.equal(seriesLabel('team-a/shop'), 'team-a/shop')
})

test('unknownNote: marks unknown calls, nothing without', () => {
  assert.equal(unknownNote(0), '')
  assert.equal(unknownNote(1), '+ 1 unknown call')
  assert.equal(unknownNote(3), '+ 3 unknown calls')
})

test('stackedChart: labels, tokens, totals and unknown per bucket', () => {
  const buckets = ['2026-11-01', '2026-11-02']
  const points = [
    point('2026-11-01', 'ns/a', '1.10'),
    point('2026-11-02', 'ns/a', '2.20', { unknown: 1 }),
    point('2026-11-02', 'ns/b', '0.50'),
    point('2026-11-02', '', '0.25', { other: true }),
  ]
  const c = stackedChart(buckets, 'day', points, 'cost', 'en-GB')
  assert.deepEqual(c.labels, ['1 Nov', '2 Nov'])
  assert.deepEqual(
    c.series.map(({ key, label, values, total }) => ({ key, label, values, total })),
    [
      { key: 'ns/a', label: 'ns/a', values: [1.1, 2.2], total: 3.3 },
      { key: 'ns/b', label: 'ns/b', values: [0, 0.5], total: 0.5 },
      { key: OTHER, label: 'Other', values: [0, 0.25], total: 0.25 },
    ],
  )
  const slots = colorSlots(['ns/a', 'ns/b', OTHER])
  for (const s of c.series) assert.equal(s.token, slots.get(s.key))
  assert.deepEqual(c.unknown, [0, 1])
})

test('chartSummary: range, known total, unknown calls and the largest series', () => {
  const c = stackedChart(
    ['2026-11-01', '2026-11-02'],
    'day',
    [point('2026-11-01', 'ns/a', '1.00', { unknown: 2 }), point('2026-11-02', 'ns/b', '3.50')],
    'cost',
    'en-GB',
  )
  assert.equal(
    chartSummary('Known cost per day by project', c),
    'Known cost per day by project, 1 Nov to 2 Nov: $4.50 known in total, 2 calls of unknown cost. Largest: ns/b, $3.50. The table view lists every value.',
  )
  assert.equal(
    chartSummary('Known cost', { labels: [], series: [], unknown: [] }),
    'Known cost: $0.00 known in total. The table view lists every value.',
  )
})

test('readChartColors: every series token and the text and grid tokens, trimmed', () => {
  const asked = []
  const c = readChartColors((name) => {
    asked.push(name)
    return ` v${name} `
  })
  assert.deepEqual(Object.keys(c.series), chartTokens)
  assert.equal(c.series['--chart-other'], 'v--chart-other')
  assert.deepEqual([c.text, c.muted, c.grid, c.surface], ['v--text', 'v--text-2', 'v--border', 'v--surface'])
  assert.ok(chartTokens.every((t) => asked.includes(t)))
})

test('cumulativeMonth: a 31-day month against February, by day of month', () => {
  const month = {
    today: 30,
    current: [
      point('2026-03-01', '', '1.00'),
      point('2026-03-02', '', '', { unknown: 1 }),
      point('2026-03-29', '', '2.50'),
      point('2026-03-30', '', '0.25'),
    ],
    previous: [point('2026-02-01', '', '2.00'), point('2026-02-14', '', '1.00', { unknown: 2 }), point('2026-02-28', '', '0.50')],
  }
  const c = cumulativeMonth(month, '2026-03', 'en-GB')
  assert.equal(c.labels.length, 31)
  assert.deepEqual([c.labels[0], c.labels[30]], ['1', '31'])
  assert.equal(c.today, 30)
  // Today is capped at February's 28 days for the comparison.
  assert.equal(c.sameDays, 28)
  const [cur, prev] = c.series
  assert.equal(cur.key, 'current')
  assert.equal(cur.dashed, undefined)
  assert.equal(cur.label, 'This month (Mar 2026)')
  // Cumulative to today, no point after it.
  assert.deepEqual(cur.values.slice(0, 3), [1, 1, 1])
  assert.equal(cur.values[28], 3.5)
  assert.equal(cur.values[29], 3.75)
  assert.equal(cur.values[30], null)
  assert.equal(cur.total, 3.75)
  // Last month over its own 28 days, dashed and muted.
  assert.equal(prev.dashed, true)
  assert.equal(prev.token, '--chart-other')
  assert.equal(prev.label, 'Last month (Feb 2026), dashed')
  assert.equal(prev.values[27], 3.5)
  assert.deepEqual(prev.values.slice(28), [null, null, null])
  assert.equal(prev.total, 3.5)
  assert.match(prev.note, /1 to 28/)
  assert.match(prev.note, /\+ 2 unknown calls/)
  // The comparison: month to date against days 1 to 28 of February.
  assert.equal(c.compare.current.costUSD, '3.75')
  assert.equal(c.compare.previous.costUSD, '3.50')
  assert.equal(c.compare.current.unknownCalls, 1)
  assert.equal(Math.round(c.compare.change), 7)
  assert.match(cur.note, /day 30, \+7%, \+ 1 unknown call$/)
  // Unknown counts per day, both months, and their table notes.
  assert.equal(c.unknown[1], 1)
  assert.equal(c.unknown[13], 2)
  assert.equal(c.notes[1], '+ 1 unknown call this month')
  assert.equal(c.notes[13], '+ 2 unknown calls last month')
  assert.equal(c.notes[0], '')
  assert.match(monthSummary(c), /day 30: \$3\.75 known; the same days of last month, 1 to 28: \$3\.50 known \(\+7%\)\. The dashed line is last month/)
})

test('cumulativeMonth: the same days early in the month, unknown is not $0', () => {
  const month = {
    today: 2,
    current: [point('2026-03-01', '', '', { unknown: 1 })],
    previous: [point('2026-02-01', '', '1.00'), point('2026-02-03', '', '5.00')],
  }
  const c = cumulativeMonth(month, '2026-03')
  assert.equal(c.sameDays, 2)
  assert.equal(c.series[0].total, null)
  assert.deepEqual(c.series[0].values.slice(0, 3), [0, 0, null])
  // Days after today count no unknown calls.
  assert.equal(c.series[1].total, 1)
  assert.equal(c.compare.change, null)
  assert.match(monthSummary(c), /day 2: no known cost; the same days of last month, 1 to 2: \$1\.00 known/)
})

test('cumulativeMonth: January against December, no calls is $0', () => {
  const c = cumulativeMonth({ today: 5, current: [], previous: [] }, '2027-01', 'en-GB')
  assert.equal(c.labels.length, 31)
  assert.equal(c.series[1].label, 'Last month (Dec 2026), dashed')
  assert.equal(c.series[0].total, 0)
  assert.equal(c.series[1].total, 0)
  assert.equal(c.series[1].values[30], 0)
})

test('thisMonth: from the points, else last month, else now in the zone', () => {
  assert.equal(thisMonth({ today: 3, current: [point('2026-11-02', '', '1')], previous: [] }, new Date(0), 'UTC'), '2026-11')
  assert.equal(thisMonth({ today: 3, current: [], previous: [point('2026-12-30', '', '1')] }, new Date(0), 'UTC'), '2027-01')
  const now = new Date('2026-11-30T23:30:00Z')
  assert.equal(thisMonth({ today: 1, current: [], previous: [] }, now, 'Europe/Berlin'), '2026-12')
  assert.equal(thisMonth({ today: 30, current: [], previous: [] }, now, 'UTC'), '2026-11')
})

function by(key, cost, unknown = 0) {
  return { key, count: 0, totals: { ...point('', key, cost, { unknown }).totals } }
}

test('breakdownRows: top six by known cost, Other sums the rest, links by dimension', () => {
  const rows = ['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h'].map((k, i) => by(`ns/${k}`, String(10 - i)))
  rows.push(by('ns/unknown', '', 2))
  const out = breakdownRows(rows, 'project', { range: '30d', group: 'project' })
  assert.equal(out.length, 7)
  assert.deepEqual(out.slice(0, 6).map((r) => r.key), ['ns/a', 'ns/b', 'ns/c', 'ns/d', 'ns/e', 'ns/f'])
  const other = out[6]
  assert.equal(other.key, OTHER)
  assert.equal(other.label, 'Other')
  // g (4) + h (3) + the row of unknown cost (0).
  assert.equal(other.value, 7)
  assert.equal(other.unknown, 2)
  assert.equal(other.link, null)
  assert.deepEqual(out[0].link, { path: '/costs', query: { project: 'ns/a' } })
  assert.equal(out.reduce((a, r) => a + r.value, 0), 10 + 9 + 8 + 7 + 6 + 5 + 4 + 3)
})

test('breakdownRows: a row of unknown cost is n/a, never $0; the legacy key and models are no links', () => {
  const f = { range: '30d', group: 'kind' }
  const out = breakdownRows([by('', '3.00'), by('implement', '', 4)], 'kind', f)
  assert.equal(out.length, 2)
  assert.equal(out[0].label, 'before calls')
  assert.equal(out[0].link, null)
  assert.equal(out[1].total, null)
  assert.equal(out[1].unknown, 4)
  assert.deepEqual(out[1].link, { path: '/costs', query: { group: 'kind', kind: 'implement' } })
  assert.equal(breakdownRows([by('claude-opus-5-5', '1.00')], 'model', f)[0].link, null)
  assert.deepEqual(breakdownRows([], 'engine', f), [])
})

test('breakdownSummary: names the total, the unknown calls and the largest row', () => {
  const rows = breakdownRows([by('claude', '5.00', 1), by('cursor', '2.00')], 'engine', { range: '30d', group: 'engine' })
  assert.equal(
    breakdownSummary('engine', rows),
    'Known cost by engine: $7.00 in total, 1 call of unknown cost. Largest: claude, $5.00. The list below has every value.',
  )
})

test('tokenSeries sums groups per bucket and gives the cache-read share', () => {
  const b = ['2026-11-01', '2026-11-02', '2026-11-03']
  const pts = [
    point('2026-11-01', 'a', '1.00', { input: 100, output: 10, cacheRead: 300, cacheWrite: 100 }),
    point('2026-11-01', 'b', '1.00', { input: 100, output: 20, cacheRead: 100, cacheWrite: 0, unmetered: 1 }),
    point('2026-11-03', 'a', '', { unmetered: 2 }),
    point('2026-12-01', 'a', '1.00', { input: 999 }),
  ]
  const t = tokenSeries(b, pts)
  assert.deepEqual(t.input, [200, 0, 0])
  assert.deepEqual(t.output, [30, 0, 0])
  assert.deepEqual(t.cacheRead, [400, 0, 0])
  assert.deepEqual(t.cacheWrite, [100, 0, 0])
  assert.deepEqual(t.share, [400 / 700, null, null])
  assert.deepEqual(t.unmetered, [1, 0, 2])
})

test('tokensChart: bars equal the sums, share matches the range, unmetered is named', () => {
  const b = ['2026-11-01']
  const pts = [point('2026-11-01', 'a', '1.00', { input: 100, output: 10, cacheRead: 300, cacheWrite: 100, unmetered: 1 })]
  const c = tokensChart(b, 'day', pts, 'en-GB')
  assert.deepEqual(c.bars.series.map((s) => s.total), [100, 10, 300, 100])
  assert.equal(c.share, 300 / 500)
  assert.deepEqual(c.line.values, [60])
  assert.equal(c.view.series.at(-1).total, 60)
  assert.deepEqual(c.bars.notes, ['+ 1 call not metered'])
  assert.match(tokensSummary(c), /cache-read share 60%/)
  const empty = tokensChart(b, 'day', [], 'en-GB')
  assert.equal(empty.share, null)
  assert.deepEqual(empty.line.values, [null])
})
