// Tests of src/spend.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  callCost,
  childLabel,
  childScope,
  costSourceLabel,
  legacyNote,
  modelsLine,
  pctLabel,
  runNumber,
  runsNewestFirst,
  spendHidden,
  tokenBreakdown,
  scopeFromQuery,
  shareLabel,
  shares,
  sortByCost,
  spendCrumbs,
  spendLocation,
  spendQuery,
  spendUSD,
  unknownNote,
} from '../src/spend.ts'

function totals(over = {}) {
  return {
    costUSD: '',
    costKnown: false,
    unknownCalls: 0,
    inputTokens: 0,
    outputTokens: 0,
    cacheReadTokens: 0,
    cacheWriteTokens: 0,
    calls: 0,
    subagentCalls: 0,
    durationMS: 0,
    ...over,
  }
}

const usd = (v, over = {}) => totals({ costUSD: v, costKnown: true, calls: 1, ...over })

test('scopeFromQuery: the milestone comes from the route, the rest from the query', () => {
  assert.deepEqual(scopeFromQuery(undefined, {}), {})
  assert.deepEqual(scopeFromQuery(3, {}), { milestone: 3 })
  assert.deepEqual(scopeFromQuery(3, { tab: 'spend', spendIssue: '12' }), { milestone: 3, issue: 12 })
  assert.deepEqual(scopeFromQuery(3, { spendIssue: '12', spendTask: 'issue-12', spendRun: '2' }), {
    milestone: 3,
    issue: 12,
    task: 'issue-12',
    run: 2,
  })
})

test('scopeFromQuery: invalid values are dropped, a run needs its task', () => {
  assert.deepEqual(scopeFromQuery(undefined, { spendIssue: '0' }), {})
  assert.deepEqual(scopeFromQuery(undefined, { spendIssue: '-1' }), {})
  assert.deepEqual(scopeFromQuery(undefined, { spendIssue: '1e3' }), {})
  assert.deepEqual(scopeFromQuery(undefined, { spendIssue: '99999999999999999999' }), {})
  assert.deepEqual(scopeFromQuery(undefined, { spendRun: '2' }), {})
  assert.deepEqual(scopeFromQuery(undefined, { spendTask: 'Bad/Name', spendRun: '2' }), {})
  assert.deepEqual(scopeFromQuery(undefined, { spendTask: '', spendRun: '2' }), {})
  assert.deepEqual(scopeFromQuery(undefined, { spendTask: 't', spendRun: 'x' }), { task: 't' })
})

test('scopeFromQuery: repeated values take the first', () => {
  assert.deepEqual(scopeFromQuery(undefined, { spendIssue: ['4', '5'] }), { issue: 4 })
  assert.deepEqual(scopeFromQuery(undefined, { spendIssue: [null, '5'] }), {})
})

test('spendQuery: replaces the drill-down, keeps other parameters, selects the tab', () => {
  const q = { tab: 'board', spendIssue: '4', spendTask: 't', spendRun: '1', other: 'x' }
  assert.deepEqual(spendQuery(q, {}), { tab: 'spend', other: 'x' })
  assert.deepEqual(spendQuery(q, { milestone: 3, issue: 5 }), { tab: 'spend', other: 'x', spendIssue: '5' })
  assert.deepEqual(spendQuery({}, { issue: 5, task: 'issue-5', run: 2 }), {
    tab: 'spend',
    spendIssue: '5',
    spendTask: 'issue-5',
    spendRun: '2',
  })
  assert.deepEqual(spendQuery({}, { run: 2 }), { tab: 'spend' })
})

test('scopeFromQuery reads back what spendQuery writes', () => {
  const s = { milestone: 3, issue: 5, task: 'issue-5', run: 2 }
  assert.deepEqual(scopeFromQuery(3, spendQuery({}, s)), s)
})

test('spendLocation: the milestone stream with a milestone, else the project stream', () => {
  assert.deepEqual(spendLocation('ns', 'p', {}), {
    name: 'stream-project',
    params: { namespace: 'ns', name: 'p' },
    query: { tab: 'spend' },
  })
  assert.deepEqual(spendLocation('ns', 'p', { milestone: 3, issue: 7 }), {
    name: 'stream-milestone',
    params: { namespace: 'ns', name: 'p', iid: '3' },
    query: { tab: 'spend', spendIssue: '7' },
  })
})

test('childScope drills one level down', () => {
  assert.deepEqual(childScope({}, { level: 'milestone', key: '3' }), { milestone: 3 })
  assert.deepEqual(childScope({ milestone: 3 }, { level: 'issue', key: '12' }), { milestone: 3, issue: 12 })
  assert.deepEqual(childScope({}, { level: 'issue', key: '12' }), { issue: 12 })
  assert.deepEqual(childScope({ milestone: 3, issue: 12 }, { level: 'run', key: 'issue-12/4' }), {
    milestone: 3,
    issue: 12,
    task: 'issue-12',
    run: 4,
  })
})

test('childScope: leaves and what the API cannot narrow to stay put', () => {
  assert.equal(childScope({}, { level: 'milestone', key: '' }), null)
  // A milestone's own calls among its issues.
  assert.equal(childScope({ milestone: 3 }, { level: 'milestone', key: '3' }), null)
  assert.equal(childScope({ milestone: 3 }, { level: 'issue', key: '' }), null)
  assert.equal(childScope({ task: 't', run: 1 }, { level: 'call', key: '7' }), null)
  assert.equal(childScope({ issue: 1 }, { level: 'run', key: 'no-run' }), null)
})

test('childScope: run 0 drills to the task', () => {
  assert.deepEqual(childScope({ issue: 12 }, { level: 'run', key: 'issue-12/0' }), { issue: 12, task: 'issue-12' })
})

test('spendCrumbs: project, milestone, issue, run', () => {
  assert.deepEqual(spendCrumbs({}, { project: 'p' }), [{ label: 'p', scope: {} }])
  assert.deepEqual(spendCrumbs({ milestone: 3, issue: 12, task: 'issue-12', run: 4 }, { project: 'p', milestone: 'Spend' }), [
    { label: 'p', scope: {} },
    { label: 'Spend', scope: { milestone: 3 } },
    { label: '#12', scope: { milestone: 3, issue: 12 } },
    { label: 'issue-12 run 4', scope: { milestone: 3, issue: 12, task: 'issue-12', run: 4 } },
  ])
})

test('spendCrumbs: an unknown milestone title is its IID, a task without run its name', () => {
  assert.deepEqual(spendCrumbs({ milestone: 3, task: 't' }, { project: 'p' }), [
    { label: 'p', scope: {} },
    { label: '%3', scope: { milestone: 3 } },
    { label: 't', scope: { milestone: 3, task: 't' } },
  ])
})

test('shares: by cost, a row without a known cost is n/a, not 0 %', () => {
  const r = shares([usd('3.00'), usd('1.00'), totals({ unknownCalls: 2, calls: 2, inputTokens: 1000 })])
  assert.equal(r.basis, 'cost')
  assert.deepEqual(r.pct, [75, 25, null])
})

test('shares: a partly known row counts with its known cost', () => {
  const r = shares([usd('1.00', { unknownCalls: 1, calls: 2 }), usd('1.00')])
  assert.deepEqual(r.pct, [50, 50])
})

test('shares: by tokens when no row has a known cost above zero', () => {
  const r = shares([totals({ inputTokens: 300 }), totals({ outputTokens: 100 }), usd('0.00')])
  assert.equal(r.basis, 'tokens')
  assert.deepEqual(r.pct, [75, 25, 0])
  assert.deepEqual(shares([totals(), totals()]).pct, [0, 0])
  assert.deepEqual(shares([]).pct, [])
})

test('shares: a malformed cost counts as 0', () => {
  assert.deepEqual(shares([usd('x'), usd('2.00')]).pct, [0, 100])
})

test('pctLabel and shareLabel', () => {
  assert.equal(pctLabel(null), 'n/a')
  assert.equal(pctLabel(0), '0%')
  assert.equal(pctLabel(0.4), '<1%')
  assert.equal(pctLabel(33.333), '33%')
  assert.equal(pctLabel(100), '100%')
  assert.equal(shareLabel(0.42), '42%')
  assert.equal(shareLabel(Number.NaN), '0%')
})

test('sortByCost: highest known cost first, unknown last, ties by tokens and key', () => {
  const rows = [
    { key: 'cursor', totals: totals({ unknownCalls: 1, calls: 1, inputTokens: 50 }) },
    { key: 'b', totals: usd('1.00', { inputTokens: 10 }) },
    { key: 'a', totals: usd('1.00', { inputTokens: 10 }) },
    { key: 'big', totals: usd('5.00') },
    { key: 'more', totals: usd('1.00', { inputTokens: 99 }) },
  ]
  assert.deepEqual(
    sortByCost(rows).map((r) => r.key),
    ['big', 'more', 'a', 'b', 'cursor'],
  )
  assert.equal(rows[0].key, 'cursor', 'sorts a copy')
})

test('spendUSD: unknown is n/a, never $0', () => {
  assert.equal(spendUSD(totals({ unknownCalls: 1, calls: 1 })), 'n/a')
  assert.equal(spendUSD(usd('12.34')), '$12.34')
})

test('callCost by source: reported, estimated with ~, unknown n/a', () => {
  assert.equal(callCost({ totals: usd('1.50'), costSource: 'reported' }), '$1.50')
  assert.equal(callCost({ totals: usd('0.42'), costSource: 'estimated' }), '~$0.42')
  assert.equal(callCost({ totals: totals({ calls: 1, unknownCalls: 1 }), costSource: 'unknown' }), 'n/a')
  // A cursor call is never $0, even if a known flag slipped through.
  assert.equal(callCost({ totals: usd('0.00'), costSource: 'unknown' }), 'n/a')
  assert.equal(costSourceLabel('reported'), 'reported')
  assert.equal(costSourceLabel('estimated'), 'estimated')
  assert.equal(costSourceLabel('unknown'), 'n/a')
  assert.equal(costSourceLabel(undefined), 'n/a')
})

test('unknownNote and legacyNote', () => {
  assert.equal(unknownNote(totals()), '')
  assert.equal(unknownNote(totals({ unknownCalls: 1 })), '+1 call without cost')
  assert.equal(unknownNote(totals({ unknownCalls: 3 })), '+3 calls without cost')
  assert.equal(legacyNote(usd('1.00')), '')
  assert.equal(legacyNote(usd('9.00', { legacyCostUSD: '4.20' })), 'includes $4.20 from runs before token tracking')
})

test('modelsLine: main models first, subagent models marked', () => {
  const m = (model, main, tokens) => ({ model, main, tokens, costUSD: '', costKnown: false })
  assert.equal(modelsLine([]), '')
  assert.equal(
    modelsLine([m('haiku', false, 900), m('opus', true, 100), m('sonnet', false, 1000)]),
    'opus, sonnet (subagent), haiku (subagent)',
  )
})

test('childLabel: milestone titles, and a milestone’s own calls', () => {
  const ms = [{ iid: 3, title: 'Spend you can see' }]
  assert.equal(childLabel({ level: 'milestone', key: '3', label: '%3' }, {}, ms), 'Spend you can see')
  assert.equal(childLabel({ level: 'milestone', key: '4', label: '%4' }, {}, ms), '%4')
  assert.equal(childLabel({ level: 'milestone', key: '', label: 'No milestone' }, {}, ms), 'No milestone')
  assert.equal(
    childLabel({ level: 'milestone', key: '3', label: 'Milestone %3' }, { milestone: 3 }, ms),
    'Milestone (spec, pilot, retro)',
  )
  assert.equal(childLabel({ level: 'issue', key: '12', label: '#12' }, { milestone: 3 }, ms), '#12')
})

test('runNumber and runsNewestFirst order runs by number, not text', () => {
  const runs = [{ key: 't/2' }, { key: 't/10' }, { key: 't/1' }]
  assert.deepEqual(runsNewestFirst(runs).map((r) => r.key), ['t/10', 't/2', 't/1'])
  assert.equal(runNumber({ key: 'x' }), 0)
})

test('spendHidden hides on 404 and 503 only', () => {
  assert.equal(spendHidden(new Error('not found')), true)
  assert.equal(spendHidden(new Error('The server answered 503.')), true)
  assert.equal(spendHidden(new Error('The server answered 500.')), false)
  assert.equal(spendHidden('x'), false)
})

test('tokenBreakdown lists all four token types', () => {
  const t = { ...totals(), inputTokens: 1200, outputTokens: 3, cacheReadTokens: 5, cacheWriteTokens: 7 }
  assert.match(tokenBreakdown(t), /Input 1,200 · output 3\nCache read 5 · cache write 7/)
})
