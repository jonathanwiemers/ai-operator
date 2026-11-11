import type { Spend, SpendChild, SpendTotals } from '../../src/types'
import { ago } from './clock'

// GET /api/v1/spend?namespace=&project=[&milestone=|&issue=|&task=[&run=]]:
// what a scope cost, with its children at the next level.

// totals is the sum of calls with the given cost, tokens and calls.
function totals(usd: string, tokens: number, calls: number, durationMS: number): SpendTotals {
  return {
    costUSD: usd,
    costKnown: true,
    unknownCalls: 0,
    unmeteredCalls: 0,
    oldBasisCalls: 0,
    inputTokens: Math.round(tokens * 0.12),
    outputTokens: Math.round(tokens * 0.03),
    cacheReadTokens: Math.round(tokens * 0.8),
    cacheWriteTokens: Math.round(tokens * 0.05),
    calls,
    subagentCalls: Math.floor(calls / 3),
    durationMS,
    checkpoints: 0,
  }
}

function child(level: SpendChild['level'], key: string, label: string, usd: string, tokens: number, calls: number): SpendChild {
  return {
    level,
    key,
    label,
    at: ago(60 * 5),
    totals: totals(usd, tokens, calls, calls * 240_000),
    models: [
      { model: 'claude-opus-5-5', main: true, tokens: Math.round(tokens * 0.8), costUSD: usd, costKnown: true, unmetered: false },
      { model: 'claude-haiku-5-5', main: false, tokens: Math.round(tokens * 0.2), costUSD: '0.00', costKnown: true, unmetered: false },
    ],
  }
}

function spend(t: SpendTotals, children: SpendChild[]): Spend {
  const by = (key: string, usd: string, tokens: number, calls: number) => ({
    key,
    totals: totals(usd, tokens, calls, calls * 240_000),
    count: 0,
  })
  return {
    totals: t,
    children,
    byKind: [by('implement', '14.20', 6_100_000, 38), by('review', '4.10', 2_200_000, 22), by('ci-fix', '2.80', 1_100_000, 9)],
    byRole: [by('implementer', '14.20', 6_100_000, 38), by('reviewer', '2.60', 1_400_000, 14), by('security', '1.50', 800_000, 8)],
    byModel: [by('claude-opus-5-5', '19.40', 8_200_000, 52), by('claude-haiku-5-5', '1.70', 1_200_000, 17)],
    byKindModel: [by('implement/claude-opus-5-5', '14.20', 6_100_000, 38)],
    conflictRuns: [],
    byReviewRound: [by('1', '2.40', 1_300_000, 12), by('2', '1.70', 900_000, 10)],
    bySession: [by('fresh', '9.10', 4_000_000, 24), by('resumed', '5.10', 2_100_000, 14)],
    byCheckpoint: [],
    bySubagents: [
      { role: 'implementer', withSubagents: true, totals: totals('8.00', 3_400_000, 20, 4_800_000), subagentTokens: 610_000, readGateBlocks: 3 },
      { role: 'implementer', withSubagents: false, totals: totals('6.20', 2_700_000, 18, 4_100_000), subagentTokens: 0, readGateBlocks: 1 },
    ],
    cacheReadShare: 0.81,
    stepLines: { count: 31, median: 84, p90: 212, max: 390, nudges: 4 },
  }
}

// projectSpend: team-a/shop by milestone.
export const projectSpend = spend(totals('48.20', 9_120_000, 141, 33_000_000), [
  child('milestone', '14', 'Payment providers: refunds, partial captures and the provider switch', '9.85', 1_870_000, 31),
  child('milestone', '12', 'Checkout without account', '21.10', 4_050_000, 64),
  child('milestone', '9', 'Search', '17.30', 3_310_000, 46),
])

// milestoneSpend: %14 by issue, with a companion.
export const milestoneSpend = spend(totals('9.85', 1_870_000, 31, 7_400_000), [
  { ...child('issue', '279', 'Refund reasons in the order history', '2.30', 410_000, 7) },
  { ...child('issue', '280', 'Provider SDK 4 with refund support', '4.13', 820_000, 12), companion: 'team-a/payments-sdk' },
  child('issue', '281', 'Support refunds through the payment provider', '3.42', 640_000, 12),
])

// taskSpend: shop-281-implement by run.
export const taskSpend = spend(totals('3.42', 2_580_000, 12, 950_000), [
  child('run', 'shop-281-implement/2', 'Run 2', '1.32', 984_000, 5),
  child('run', 'shop-281-implement/1', 'Run 1', '2.10', 1_596_000, 7),
])

// runSpend: one run of shop-281-implement by call.
export const runSpend = spend(totals('1.32', 984_000, 5, 338_000), [
  {
    ...child('call', '2', 'implement · implementer', '1.32', 984_000, 1),
    kind: 'implement',
    role: 'implementer',
    engine: 'claude',
    model: 'claude-opus-5-5',
    resumed: true,
    costSource: 'reported',
    turns: 23,
    contextSeries: [42_000, 61_000, 88_000, 97_000],
    subagentTurns: [4, 6],
    readGateBlocks: 1,
    linesAdded: 182,
    linesDeleted: 24,
    stepGateNudges: 0,
  },
])

// answerSpend picks the fixture of a spend query.
export function answerSpend(q: URLSearchParams): Spend {
  if (q.has('run')) return runSpend
  if (q.has('task')) return taskSpend
  if (q.has('milestone') || q.has('issue')) return milestoneSpend
  return projectSpend
}
