// Tests of src/format.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { costChip, costTooltip, deferringEngine, deskTitle, durationShort, fullTime, tokenCount } from '../src/format.ts'

test('fullTime shows the date, time and time zone', () => {
  const s = fullTime('2026-11-02T13:59:00Z')
  assert.match(s, /2026/)
  assert.match(s, /\d{1,2}:59/)
  assert.ok(s.length > 'Nov 2, 2026, 13:59'.length, s)
})

test('fullTime is empty for a missing or invalid time', () => {
  assert.equal(fullTime(undefined), '')
  assert.equal(fullTime('not a time'), '')
})

test('deskTitle shows the Inbox count, and the base title at 0', () => {
  assert.equal(deskTitle(3, 'ai-operator'), '(3) Desk')
  assert.equal(deskTitle(0, 'ai-operator'), 'ai-operator')
})

test('tokenCount abbreviates with k, M and B', () => {
  assert.equal(tokenCount(0), '0')
  assert.equal(tokenCount(950), '950')
  assert.equal(tokenCount(1000), '1k')
  assert.equal(tokenCount(1234), '1.2k')
  assert.equal(tokenCount(12_345), '12k')
  assert.equal(tokenCount(999_950), '1M')
  assert.equal(tokenCount(1_234_567), '1.2M')
  assert.equal(tokenCount(45_600_000), '46M')
  assert.equal(tokenCount(2_500_000_000), '2.5B')
})

const spend = {
  costUSD: '12.34',
  costKnown: true,
  unknownCalls: 0,
  inputTokens: 1000,
  outputTokens: 200,
  cacheReadTokens: 1_200_000,
  cacheWriteTokens: 0,
  calls: 3,
  subagentCalls: 0,
  durationMS: 1000,
}

test('costChip shows USD and tokens, n/a when the cost is unknown, never $0', () => {
  assert.equal(costChip({ costUSD: '12.34', costKnown: true, tokens: 1_201_200 }), '$12.34 · 1.2M tok')
  assert.equal(costChip({ costUSD: '', costKnown: false, tokens: 1500 }), 'n/a · 1.5k tok')
  assert.equal(costChip({ costUSD: '', costKnown: false, tokens: 0 }), 'n/a')
  // Summed over tasks, or legacy runs only: no tokens to show.
  assert.equal(costChip({ costUSD: '1.75', costKnown: true, tokens: 0 }), '$1.75')
  assert.equal(costChip(undefined), '')
})

test('costTooltip breaks down what is known', () => {
  const tip = costTooltip({ costUSD: '12.34', costKnown: true, tokens: 1_201_200, costFrom: 'calls', spend })
  assert.match(tip, /Input 1,000 · output 200 tokens/)
  assert.match(tip, /Cache read 1,200,000 · cache write 0 tokens/)
  assert.match(tip, /3 calls/)
  assert.doesNotMatch(tip, /without a known cost|before calls/)

  const partial = costTooltip({
    costUSD: '2.00',
    costKnown: true,
    tokens: 10,
    costFrom: 'calls',
    spend: { ...spend, calls: 1, unknownCalls: 1, legacyCostUSD: '2.00' },
  })
  assert.match(partial, /1 call without a known cost/)
  assert.match(partial, /\$2\.00 from runs before calls were recorded/)

  assert.match(costTooltip({ costUSD: '1.75', costKnown: true, tokens: 0, costFrom: 'tasks' }), /current tasks/)
})

test('deferringEngine reads the usage limit marked deferred', () => {
  const until = '2026-11-05T15:00:00Z'
  const routed = { engine: 'claude', until }
  assert.equal(deferringEngine({ usageLimits: [routed, { engine: 'cursor', until, deferred: true }] }), 'cursor')
  assert.equal(deferringEngine({ usageLimits: [routed] }), undefined, 'a routed limit defers nothing')
  assert.equal(deferringEngine({}), undefined, 'a task an older release deferred names no engine')
})

test('durationShort: minutes, hours and days in at most two units', () => {
  assert.equal(durationShort(0), '0m')
  assert.equal(durationShort(59), '<1m')
  assert.equal(durationShort(60), '1m')
  assert.equal(durationShort(45 * 60 + 59), '45m')
  assert.equal(durationShort(3600), '1h')
  assert.equal(durationShort(3 * 3600 + 5 * 60), '3h 5m')
  assert.equal(durationShort(86400), '1d')
  assert.equal(durationShort(2 * 86400 + 4 * 3600 + 59 * 60), '2d 4h')
})

test('durationShort: a negative or invalid span is a dash', () => {
  assert.equal(durationShort(-1), '—')
  assert.equal(durationShort(Number.NaN), '—')
  assert.equal(durationShort(Number.POSITIVE_INFINITY), '—')
})
