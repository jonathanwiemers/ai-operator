// Tests of src/format.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { cost, costChip, costTooltip, deferringEngine, deskTitle, durationShort, fullTime, loginLasts, tokenCount } from '../src/format.ts'
import { cronText } from '../src/cron.ts'

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

test('deskTitle puts the page title before Desk, the Inbox count in front', () => {
  assert.equal(deskTitle(3, 'Milestone 14'), '(3) Milestone 14 · Desk')
  assert.equal(deskTitle(0, 'Milestone 14'), 'Milestone 14 · Desk')
  assert.equal(deskTitle(0, '\n  Milestone\n  14 '), 'Milestone 14 · Desk')
})

test('deskTitle is Desk on a page without a title', () => {
  for (const page of [undefined, null, '', '  ']) {
    assert.equal(deskTitle(0, page), 'Desk')
    assert.equal(deskTitle(2, page), '(2) Desk')
  }
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

test('cost groups thousands in the browser locale, keeps <$0.01 and $0', () => {
  const usd = (v) => new Intl.NumberFormat(undefined, { style: 'currency', currency: 'USD', minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(v)
  assert.equal(cost('2614.69'), usd(2614.69))
  // Under en-US (node's locale without LANG, as in CI) that is $2,614.69.
  if (new Intl.NumberFormat().resolvedOptions().locale === 'en-US') assert.equal(cost('2614.69'), '$2,614.69')
  assert.equal(cost('12.3'), usd(12.3))
  assert.equal(cost('0.004'), '<$0.01')
  assert.equal(cost('0'), '$0')
  assert.equal(cost(''), '$0')
  assert.equal(cost(undefined), '$0')
})

test('cronText: a fixed time daily, on weekdays, weekends or listed days, with the zone', () => {
  assert.equal(cronText('15 7 * * *', 'Europe/Berlin'), 'daily at 07:15 Europe/Berlin')
  assert.equal(cronText('15 7 * * *'), 'daily at 07:15 UTC')
  assert.equal(cronText('0 9 * * 1-5', ''), 'weekdays at 09:00 UTC')
  assert.equal(cronText('0 9 * * MON-FRI'), 'weekdays at 09:00 UTC')
  assert.equal(cronText('30 18 * * 0,6'), 'weekends at 18:30 UTC')
  assert.equal(cronText('30 18 * * 6,7'), 'weekends at 18:30 UTC', '7 is Sunday')
  assert.equal(cronText('0 9 * * 1,3'), 'Mon, Wed at 09:00 UTC')
  assert.equal(cronText('0 9 * * 1-4'), 'Mon–Thu at 09:00 UTC')
  assert.equal(cronText('0 9 * * 1-3,5'), 'Mon–Wed, Fri at 09:00 UTC')
  assert.equal(cronText('0 9 * * 0-6'), 'daily at 09:00 UTC')
  assert.equal(cronText('  5  23 * * 2 '), 'Tue at 23:05 UTC')
})

test('cronText: intervals of minutes and hours, without a zone', () => {
  assert.equal(cronText('*/15 * * * *', 'Europe/Berlin'), 'every 15 minutes')
  assert.equal(cronText('*/1 * * * *'), 'every minute')
  assert.equal(cronText('* * * * *'), 'every minute')
  assert.equal(cronText('0 */2 * * *'), 'every 2 hours')
  assert.equal(cronText('0 * * * *'), 'every hour')
})

test('cronText: other forms are null, so the caller shows the raw cron', () => {
  for (const expr of [
    '0 0 1 * *',
    '0 9 * 1 *',
    '0 9,17 * * *',
    '0 9-17 * * *',
    '*/15 9 * * *',
    '15 */2 * * *',
    '0 9 * * */2',
    '0 9 * * 5-1',
    '0 9 * * 8',
    '60 9 * * *',
    '0 24 * * *',
    '*/0 * * * *',
    '@daily',
    '0 9 * *',
    '',
    undefined,
  ]) {
    assert.equal(cronText(expr), null, String(expr))
  }
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

test('loginLasts: renewing names the date, fixed the time', () => {
  const now = new Date('2026-11-12T10:00:00').getTime()
  const day = (s) => new Date(s).toLocaleDateString(undefined, { day: 'numeric', month: 'short' })
  const renewing = loginLasts({ renewing: true, idleUntil: '2026-11-19T10:00:00' }, now)
  assert.equal(renewing, `Signed in until ${day('2026-11-19T10:00:00')} unless you are away longer`)
  const today = loginLasts({ renewing: false, idleUntil: '2026-11-12T22:30:00' }, now)
  assert.match(today, /^Signed in until \d{1,2}:30/)
  assert.ok(!today.includes(day('2026-11-12T22:30:00')), today)
  const other = loginLasts({ renewing: false, idleUntil: '2026-11-13T08:00:00' }, now)
  assert.ok(other.startsWith(`Signed in until ${day('2026-11-13T08:00:00')}, `), other)
  assert.equal(loginLasts({ renewing: true, idleUntil: null }, now), null)
  assert.equal(loginLasts({ renewing: false }, now), null)
})
