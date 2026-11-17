import type { SpendTotals } from '../../src/generated/store'
import type { CostPointView, CostsTopView, CostsView, CostTopRowView, MetricsGroup, MetricsView, SpendBreakdownView } from '../../src/generated/views'

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
  planUsage: [
    {
      namespace: 'team-a',
      project: 'shop',
      usage: {
        engine: 'claude',
        status: 'allowed',
        windows: [
          { name: 'five_hour', percent: 42, resetsAt: '2026-11-10T14:00:00Z' },
          { name: 'seven_day', percent: 83, resetsAt: '2026-11-14T09:00:00Z' },
        ],
        observedAt: '2026-11-10T11:50:00Z',
      },
    },
    {
      namespace: 'team-b',
      project: 'a-project-with-a-rather-long-name.example',
      usage: { engine: 'cursor', windows: [{ name: 'seven_day', percent: 12 }], observedAt: '2026-11-09T08:00:00Z' },
    },
  ],
  budgets: [
    { namespace: 'team-a', project: 'shop', milestoneIID: 3, issueIID: 0, title: 'Checkout rewrite', limit: '100.00', spent: '86.40', share: 0.864 },
    { namespace: 'team-a', project: 'shop', milestoneIID: 3, issueIID: 12, title: 'Payment provider with a rather long issue title', limit: '20.00', spent: '4.00', share: 0.2 },
    { namespace: 'team-b', project: 'a-project-with-a-rather-long-name.example', milestoneIID: 1, issueIID: 5, title: 'Unmetered issue', limit: '10.00', spent: '', share: 0 },
  ],
}

// GET /api/v1/costs/top: the most expensive milestones, issues and runs of
// the same range: a run with milestone and issue, a research run with
// neither, and an issue whose calls all have an unknown cost.
const topRow = (r: Partial<CostTopRowView> & Pick<CostTopRowView, 'label'>, usd: string, extra: Partial<SpendTotals> = {}): CostTopRowView => ({
  namespace: 'team-a',
  project: 'shop',
  at: '2026-11-09T10:00:00Z',
  totals: totals(usd, 900_000, 8, extra),
  ...r,
})

export const costsTop: CostsTopView = {
  milestones: [
    topRow({ label: 'Refunds, end to end', milestoneIID: 14 }, '48.20'),
    topRow({ label: 'Checkout rewrite', milestoneIID: 12 }, '21.10', { unknownCalls: 2 }),
  ],
  issues: [
    topRow({ label: 'Refund the whole order', milestoneIID: 14, issueIID: 281 }, '9.85'),
    topRow({ label: 'Unpriced cursor work', milestoneIID: 12, issueIID: 266 }, '', { unknownCalls: 8, unmeteredCalls: 8 }),
  ],
  runs: [
    topRow({ label: 'shop-i281 run 2', milestoneIID: 14, issueIID: 281, task: 'shop-i281', run: 2, kind: 'implement' }, '4.13'),
    topRow({ label: 'research/payments-news run 1', task: 'research/payments-news', run: 1, kind: 'research' }, '0.42'),
  ],
}

// GET /api/v1/costs/metrics: the cost per merged line of the same range. shop
// runs the experiment cost-levers with four arms, two of them below 5 merged
// issues (greyed out); the long-named project has arms of an experiment it no
// longer runs. A step session of none ("—") and an MCP tool with a long name.
const group = (key: string, issues: number, lines: number, usd: number, tokens: number, extra: Partial<MetricsGroup> = {}): MetricsGroup => ({
  key,
  issues,
  mergedLines: lines,
  costUSD: usd,
  tokens,
  costPer100Lines: lines > 0 ? (usd * 100) / lines : 0,
  low: issues < 5,
  ...extra,
})
const arm = (project: string, experiment: string | undefined, key: string, issues: number, lines: number, usd: number, tokens: number) =>
  group(key, issues, lines, usd, tokens, { project, ...(experiment ? { experiment } : {}) })

export const costsMetrics: MetricsView = {
  arms: [
    arm(long, undefined, 'baseline', 2, 140, 6.1, 3_100_000),
    arm(shop, 'cost-levers', 'baseline', 7, 1820, 61.4, 41_000_000),
    arm(shop, 'cost-levers', 'fresh', 6, 1544, 38.9, 26_500_000),
    arm(shop, 'cost-levers', 'medium', 4, 910, 22.75, 15_200_000),
    arm(shop, 'cost-levers', 'sonnet', 3, 0, 4.2, 6_800_000),
  ],
  byModel: [group('claude-opus-5-5', 19, 4414, 129.15, 85_800_000), group('claude-sonnet-5-5', 3, 0, 4.2, 6_800_000)],
  byEffort: [group('high', 15, 3504, 106.4, 70_600_000), group('medium', 4, 910, 22.75, 15_200_000), group('', 3, 0, 4.2, 6_800_000)],
  byStepSession: [group('resume', 9, 1960, 67.5, 44_100_000), group('fresh', 6, 1544, 38.9, 26_500_000), group('', 7, 910, 26.95, 22_000_000)],
  context: [
    {
      project: shop,
      baseContext: [
        { role: 'implement', medianTokens: 21_400, calls: 38 },
        { role: 'review', medianTokens: 18_900, calls: 41 },
      ],
      topTools: [
        { tool: 'Read', calls: 412, bytes: 8_123_400, tokens: 2_030_850 },
        { tool: 'Bash', calls: 230, bytes: 3_402_000, tokens: 850_500 },
        { tool: 'Grep', calls: 181, bytes: 1_200_000, tokens: 300_000 },
        { tool: 'mcp__gitlab__get_merge_request_discussions', calls: 12, bytes: 640_000, tokens: 160_000 },
        { tool: 'Task', calls: 9, bytes: 52_000, tokens: 13_000 },
      ],
    },
    { project: long, baseContext: [{ role: 'implement', medianTokens: 9_800, calls: 3 }], topTools: [] },
  ],
}
