// Tests of src/spend.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  avgCost,
  avgTokens,
  callCost,
  childLabel,
  companionNote,
  childScope,
  costSourceLabel,
  legacyNote,
  modelBars,
  modelOrder,
  modelSlots,
  modelsLine,
  pctLabel,
  reviewRounds,
  runNumber,
  runsNewestFirst,
  spendHidden,
  tokenBreakdown,
  scopeFromQuery,
  sessionRows,
  shareLabel,
  shares,
  sortByCost,
  splitPct,
  spendCrumbs,
  spendLocation,
  spendQuery,
  spendUSD,
  subagentRoles,
  gateBlocksPerCall,
  subagentShare,
  unitFromQuery,
  unitQuery,
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

const split = (model, main, tokens, costUSD = '', over = {}) => ({ model, main, tokens, costUSD, costKnown: costUSD !== '', ...over })
const row = (models, over = {}) => ({
  models,
  totals: totals({ costKnown: models.some((m) => m.costKnown), costUSD: '1' }),
  ...over,
})

test('unitFromQuery and unitQuery: tokens unless spendUnit=usd, other parameters kept', () => {
  assert.equal(unitFromQuery({}), 'tokens')
  assert.equal(unitFromQuery({ spendUnit: 'usd' }), 'usd')
  assert.equal(unitFromQuery({ spendUnit: 'eur' }), 'tokens')
  assert.deepEqual(unitQuery({ tab: 'spend', spendIssue: '3' }, 'usd'), { tab: 'spend', spendIssue: '3', spendUnit: 'usd' })
  assert.deepEqual(unitQuery({ tab: 'spend', spendUnit: 'usd' }, 'tokens'), { tab: 'spend' })
  // The unit survives drilling down.
  assert.equal(spendQuery({ spendUnit: 'usd' }, { issue: 4 }).spendUnit, 'usd')
})

test('modelOrder: main models first, then by tokens, then by name', () => {
  const got = modelOrder([split('haiku', false, 50), split('sonnet', false, 50), split('opus', true, 10), split('x', false, 90)])
  assert.deepEqual(got.map((m) => m.model), ['opus', 'x', 'haiku', 'sonnet'])
})

test('modelSlots: subagent models by name, a model keeps its slot across rows', () => {
  const slots = modelSlots([
    { models: [split('opus', true, 1), split('sonnet', false, 1)] },
    { models: [split('haiku', false, 1), split('z1', false, 1), split('z2', false, 1)] },
  ])
  assert.deepEqual([...slots], [['haiku', 1], ['sonnet', 2], ['z1', 3], ['z2', 3]])
})

test('splitPct: tenths that sum to exactly 100, a small value stays visible', () => {
  const sum = (a) => Math.round(a.reduce((s, x) => s + x, 0) * 10)
  assert.deepEqual(splitPct([1, 1, 1]), [33.4, 33.3, 33.3])
  assert.equal(sum(splitPct([1, 1, 1])), 1000)
  assert.deepEqual(splitPct([100000, 1]), [99.9, 0.1])
  assert.deepEqual(splitPct([0, 5]), [0, 100])
  assert.deepEqual(splitPct([0, 0]), [0, 0])
  for (const v of [[2, 3, 7], [1, 2, 3, 4, 5, 6, 7], [999, 1, 1, 1], [0.3, 0.3, 0.4]]) assert.equal(sum(splitPct(v)), 1000, String(v))
  assert.deepEqual(splitPct([1e9, ...Array(5).fill(1)]).slice(1), Array(5).fill(0.1))
  assert.equal(sum(splitPct([1e9, ...Array(5).fill(1)])), 1000)
})

test('modelBars: main first, subagents after, bars share the table scale', () => {
  const bars = modelBars(
    [
      row([split('haiku', false, 250), split('opus', true, 750)]),
      row([split('opus', true, 500)]),
      row([]),
    ],
    'tokens',
  )
  assert.deepEqual(bars[0].segments.map((s) => [s.model, s.main, s.slot, s.x, s.width, s.share]), [
    ['opus', true, 0, 0, 75, 75],
    ['haiku', false, 1, 75, 25, 25],
  ])
  assert.equal(bars[0].width, 100)
  assert.equal(bars[0].label, '1k')
  assert.equal(bars[1].width, 50)
  assert.equal(bars[1].segments[0].width, 50)
  assert.equal(bars[2].width, 0)
  assert.deepEqual(bars[2].segments, [])
  assert.equal(bars[2].label, '—')
  assert.match(bars[0].segments[1].title, /^haiku \(subagent\)\n250 tokens · n\/a$/)
})

test('modelBars: USD bars, unknown costs fall back to tokens labelled n/a, never $0', () => {
  const bars = modelBars(
    [
      row([split('opus', true, 100, '3.00'), split('haiku', false, 900, '1.00')]),
      row([split('opus', true, 100, '2.00')]),
      row([split('gpt', true, 400)], { costSource: 'unknown' }),
      row([split('gpt', true, 200, '0')]),
    ],
    'usd',
  )
  assert.equal(bars[0].label, '$4.00')
  assert.equal(bars[0].width, 100)
  assert.deepEqual(bars[0].segments.map((s) => [s.model, s.width]), [['opus', 75], ['haiku', 25]])
  assert.equal(bars[1].width, 50)
  // Fallback rows: tokens, on the tokens' scale among themselves.
  assert.equal(bars[2].fallback, true)
  assert.equal(bars[2].label, 'n/a')
  assert.equal(bars[2].width, 100)
  assert.equal(bars[3].fallback, true)
  assert.equal(bars[3].label, 'n/a')
  assert.equal(bars[3].width, 50)
  assert.match(bars[2].title, /tokens/)
  assert.ok(!bars.some((b) => b.label === '$0'))
})

test('modelBars: estimated costs are marked ~, models without a cost left out of a USD bar', () => {
  const [b] = modelBars([row([split('gpt', true, 100, '0.42'), split('mini', false, 50)], { costSource: 'estimated' })], 'usd')
  assert.equal(b.fallback, false)
  assert.equal(b.label, '~$0.42')
  assert.deepEqual(b.segments.map((s) => s.model), ['gpt'])
  assert.match(b.segments[0].title, /~\$0\.42$/)
  assert.match(b.title, /mini/)
  const [t] = modelBars([row([split('gpt', true, 100, '0.42')], { costSource: 'estimated' })], 'tokens')
  assert.equal(t.label, '100')
  assert.match(t.segments[0].title, /100 tokens · ~\$0\.42$/)
})

test('avgCost: per call with a known cost, n/a when none has one, never $0', () => {
  assert.equal(avgCost(usd('6.00', { calls: 3 })), '$2.00')
  // A call without a cost is not in the average.
  assert.equal(avgCost(usd('6.00', { calls: 4, unknownCalls: 1 })), '$2.00')
  assert.equal(avgCost(totals({ calls: 2, unknownCalls: 2 })), 'n/a')
  assert.equal(avgCost(totals()), 'n/a')
  assert.equal(avgCost(usd('0.01', { calls: 3 })), '<$0.01')
})

test('avgTokens: all token types per call, 0 without calls', () => {
  assert.equal(avgTokens(totals({ calls: 4, inputTokens: 100, outputTokens: 20, cacheReadTokens: 250, cacheWriteTokens: 30 })), 100)
  assert.equal(avgTokens(totals({ inputTokens: 5 })), 0)
})

test('reviewRounds by number, sessionRows fresh first', () => {
  const rows = (...keys) => keys.map((key) => ({ key, totals: usd('1.00') }))
  assert.deepEqual(
    reviewRounds(rows('10', '2', '1')).map((r) => r.key),
    ['1', '2', '10'],
  )
  assert.deepEqual(
    sessionRows(rows('resumed', 'fresh')).map((r) => r.key),
    ['fresh', 'resumed'],
  )
})

test('subagentRoles: roles where a subagent ran, with their calls without', () => {
  const row = (role, withSubagents, over = {}) => ({ role, withSubagents, totals: usd('1.00'), subagentTokens: 0, ...over })
  const got = subagentRoles([
    row('review', false),
    row('implement', true, { subagentTokens: 25, totals: usd('3.00', { inputTokens: 80, outputTokens: 20 }) }),
    row('implement', false),
    row('fix', false),
    row('scout-only', true),
  ])
  assert.deepEqual(
    got.map((r) => [r.role, r.rows.map((x) => x.withSubagents)]),
    [
      ['implement', [true, false]],
      ['scout-only', [true]],
    ],
  )
  assert.equal(subagentShare(got[0].rows[0]), '25%')
  assert.equal(subagentShare(got[1].rows[0]), '—')
})

test('gateBlocksPerCall: blocks per call, empty without calls or blocks', () => {
  const row = (calls, readGateBlocks) => ({ role: 'implement', withSubagents: true, totals: { calls }, subagentTokens: 0, readGateBlocks })
  assert.equal(gateBlocksPerCall(row(2, 3)), '1.5')
  assert.equal(gateBlocksPerCall(row(2, 0)), '')
  assert.equal(gateBlocksPerCall(row(0, 3)), '')
})

test('companionNote: issue rows with a companion project only', () => {
  assert.equal(companionNote({ level: 'issue', companion: 'group/infra' }), 'Also changes group/infra: its calls count to this issue')
  assert.equal(companionNote({ level: 'issue' }), '')
  assert.equal(companionNote({ level: 'issue', companion: ' ' }), '')
  assert.equal(companionNote({ level: 'run', companion: 'group/infra' }), '')
})
