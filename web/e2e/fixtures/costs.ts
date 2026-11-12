import type { SpendTotals } from '../../src/generated/store'
import type { CostPointView, CostsView, SpendBreakdownView } from '../../src/generated/views'

// GET /api/v1/costs?range=&from=&to=&group=&project=&kind=&engine=&tz=: what
// the viewer's two projects and research cost in the last 30 days up to the
// checks' now (2026-11-10), with unknown and unmetered calls, a mixed token
// basis and research counted since 2026-10-20. Filters change the URL, not
// this answer.

const shop = 'team-a/shop'
const long = 'team-b/a-project-with-a-rather-long-name.example'

function totals(usd: string, tokens: number, calls: number, extra: Partial<SpendTotals> = {}): SpendTotals {
  return {
    costUSD: usd,
    costKnown: usd !== '',
    unknownCalls: 0,
    unmeteredCalls: 0,
    oldBasisCalls: 0,
    inputTokens: Math.round(tokens * 0.12),
    outputTokens: Math.round(tokens * 0.03),
    cacheReadTokens: Math.round(tokens * 0.8),
    cacheWriteTokens: Math.round(tokens * 0.05),
    calls,
    subagentCalls: Math.floor(calls / 4),
    durationMS: calls * 240_000,
    checkpoints: 0,
    ...extra,
  }
}

const days = Array.from({ length: 30 }, (_, i) => new Date(Date.UTC(2026, 9, 12 + i)).toISOString().slice(0, 10))

const point = (bucket: string, key: string, t: SpendTotals): CostPointView => ({ bucket, key, other: false, totals: t })
const by = (key: string, t: SpendTotals): SpendBreakdownView => ({ key, totals: t, count: 0 })

const series: CostPointView[] = days.flatMap((d, i) => [
  point(d, shop, totals((1.5 + (i % 5)).toFixed(2), 400_000, 6)),
  ...(i % 3 === 0 ? [point(d, long, totals('0.80', 120_000, 2, { unknownCalls: 1 }))] : []),
  ...(i >= 8 && i % 7 === 1 ? [point(d, 'research/payments-news', totals('0.42', 60_000, 1))] : []),
])

const sum = totals('132.65', 14_200_000, 208, { unknownCalls: 10, unmeteredCalls: 3, oldBasisCalls: 12 })

export const costs: CostsView = {
  range: '30d',
  from: days[0]!,
  to: days[days.length - 1]!,
  bucket: 'day',
  tz: 'UTC',
  group: 'project',
  earliest: '2025-11-10',
  buckets: days,
  series,
  totals: sum,
  previous: totals('101.20', 11_000_000, 170, { unknownCalls: 4 }),
  cacheReadShare: 0.81,
  previousCacheReadShare: 0.74,
  stepLines: { count: 40, median: 180, p90: 420, max: 900, nudges: 2 },
  byProject: [by(shop, totals('125.85', 13_500_000, 180)), by(long, totals('8.00', 1_200_000, 20, { unknownCalls: 10 }))],
  byKind: [by('implement', totals('88.10', 9_000_000, 120)), by('review', totals('31.40', 3_800_000, 70)), by('research', totals('1.68', 240_000, 4))],
  byModel: [by('claude-opus-5-5', totals('120.00', 12_000_000, 160)), by('cursor/auto', totals('', 0, 3, { unknownCalls: 3, unmeteredCalls: 3 }))],
  byEngine: [by('claude', totals('130.97', 13_900_000, 190)), by('cursor', totals('1.68', 300_000, 18, { unknownCalls: 10 }))],
  month: {
    today: 10,
    current: days.slice(-10).map((d) => point(d, '', totals('4.20', 420_000, 7))),
    previous: Array.from({ length: 31 }, (_, i) =>
      point(new Date(Date.UTC(2026, 9, 1 + i)).toISOString().slice(0, 10), '', totals('3.10', 380_000, 6)),
    ),
  },
  researchSince: '2026-10-20T07:00:00Z',
  planUsage: [],
  budgets: [],
}
