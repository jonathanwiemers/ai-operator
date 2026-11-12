// Tests of src/costs.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  breakdownLink,
  breakdownsCsv,
  csvCell,
  seriesCsv,
  changeLabel,
  costChange,
  costFiltersQuery,
  costKeyLabel,
  costParams,
  monthToDate,
  parseCostFilters,
  topRowLocation,
} from '../src/costs.ts'

test('parseCostFilters: defaults 30d and project', () => {
  assert.deepEqual(parseCostFilters({}), { range: '30d', group: 'project' })
})

test('parseCostFilters: reads every filter', () => {
  assert.deepEqual(
    parseCostFilters({ range: '90d', group: 'model', project: 'ns/p', kind: 'review', engine: 'cursor' }),
    { range: '90d', group: 'model', project: 'ns/p', kind: 'review', engine: 'cursor' },
  )
  // A repeated parameter counts with its first value.
  assert.equal(parseCostFilters({ range: ['7d', '12m'] }).range, '7d')
})

test('parseCostFilters: unknown values fall back to the defaults', () => {
  assert.deepEqual(
    parseCostFilters({ range: '1d', group: 'role', project: 'p', kind: 'nope', engine: 'gpt' }),
    { range: '30d', group: 'project' },
  )
  assert.equal(parseCostFilters({ range: 'custom' }).range, '30d')
  assert.equal(parseCostFilters({ project: 'ns/p/x' }).project, undefined)
  assert.equal(parseCostFilters({ range: null }).range, '30d')
})

test('parseCostFilters: from and to together, in order, real dates', () => {
  assert.deepEqual(parseCostFilters({ range: '7d', from: '2026-10-01', to: '2026-10-31' }), {
    range: 'custom',
    from: '2026-10-01',
    to: '2026-10-31',
    group: 'project',
  })
  for (const q of [
    { from: '2026-10-01' },
    { to: '2026-10-31' },
    { from: '2026-10-31', to: '2026-10-01' },
    { from: '2026-02-30', to: '2026-03-01' },
    { from: '2026-1-1', to: '2026-01-02' },
  ]) {
    assert.equal(parseCostFilters({ ...q, range: '7d' }).range, '7d', JSON.stringify(q))
  }
})

test('costFiltersQuery: leaves the defaults out, round-trips', () => {
  assert.deepEqual(costFiltersQuery({ range: '30d', group: 'project' }), {})
  const f = { range: '7d', group: 'kind', project: 'ns/p', kind: 'fix', engine: 'claude' }
  assert.deepEqual(costFiltersQuery(f), f)
  assert.deepEqual(parseCostFilters(costFiltersQuery(f)), f)
  const custom = { range: 'custom', from: '2026-10-01', to: '2026-10-31', group: 'engine' }
  assert.deepEqual(costFiltersQuery(custom), { from: '2026-10-01', to: '2026-10-31', group: 'engine' })
  assert.deepEqual(parseCostFilters(costFiltersQuery(custom)), custom)
})

test('costParams: the range or from and to, with the time zone', () => {
  assert.deepEqual(costParams({ range: '7d', group: 'project', project: 'ns/p' }, 'Europe/Berlin'), {
    range: '7d',
    group: 'project',
    project: 'ns/p',
    kind: undefined,
    engine: undefined,
    tz: 'Europe/Berlin',
  })
  const p = costParams({ range: 'custom', from: '2026-10-01', to: '2026-10-31', group: 'kind' }, 'UTC')
  assert.equal(p.range, undefined)
  assert.equal(p.from, '2026-10-01')
  assert.equal(p.to, '2026-10-31')
})

const known = (usd) => ({ costUSD: usd, costKnown: true })
const unknown = { costUSD: '', costKnown: false }

test('costChange: against the previous period', () => {
  assert.equal(costChange(known('15.00'), known('10.00')), 50)
  assert.equal(costChange(known('5.00'), known('10.00')), -50)
  assert.equal(costChange(known('0.00'), known('10.00')), -100)
})

test('costChange: null without a previous known cost or with unknown calls only', () => {
  assert.equal(costChange(known('15.00'), known('0.00')), null)
  assert.equal(costChange(known('15.00'), unknown), null)
  assert.equal(costChange(unknown, known('10.00')), null)
})

test('changeLabel', () => {
  assert.equal(changeLabel(null), '')
  assert.equal(changeLabel(12.4), '+12%')
  assert.equal(changeLabel(-5.6), '−6%')
  assert.equal(changeLabel(0.3), '±0%')
})

const totals = (usd, calls = 1, unknownCalls = 0) => ({
  costUSD: usd,
  costKnown: usd !== '',
  calls,
  unknownCalls,
})
const day = (bucket, usd, calls, unknownCalls) => ({ bucket, key: '', other: false, totals: totals(usd, calls, unknownCalls) })

test('monthToDate: the same days of last month', () => {
  const m = {
    today: 12,
    current: [day('2026-11-01', '2.00'), day('2026-11-12', '3.50', 2)],
    previous: [day('2026-10-02', '1.00'), day('2026-10-12', '1.50'), day('2026-10-13', '9.00'), day('2026-10-31', '9.00')],
  }
  const r = monthToDate(m)
  assert.deepEqual(r.current, { costUSD: '5.50', costKnown: true, calls: 3, unknownCalls: 0 })
  assert.deepEqual(r.previous, { costUSD: '2.50', costKnown: true, calls: 2, unknownCalls: 0 })
  assert.equal(r.change, 120)
})

test('monthToDate: unknown is not $0, no change without last month', () => {
  const r = monthToDate({ today: 3, current: [day('2026-11-01', '', 2, 2)], previous: [] })
  assert.deepEqual(r.current, { costUSD: '', costKnown: false, calls: 2, unknownCalls: 2 })
  assert.equal(r.previous.costKnown, false)
  assert.equal(r.change, null)
  // A known day next to an unknown one: the known sum, the rest counted.
  const mixed = monthToDate({ today: 3, current: [day('2026-11-01', '', 1, 1), day('2026-11-02', '1.25')], previous: [] })
  assert.deepEqual(mixed.current, { costUSD: '1.25', costKnown: true, calls: 2, unknownCalls: 1 })
})

test('costKeyLabel: the legacy key is "before calls"', () => {
  assert.equal(costKeyLabel(''), 'before calls')
  assert.equal(costKeyLabel('ns/p'), 'ns/p')
})

test('breakdownLink: project, kind and engine rows set their filter and keep the rest', () => {
  const f = { range: '90d', group: 'kind', kind: 'review' }
  assert.deepEqual(breakdownLink('project', 'ns/p', f), { path: '/costs', query: { range: '90d', group: 'kind', kind: 'review', project: 'ns/p' } })
  assert.deepEqual(breakdownLink('kind', 'fix', f), { path: '/costs', query: { range: '90d', group: 'kind', kind: 'fix' } })
  assert.deepEqual(breakdownLink('engine', 'cursor', { range: '30d', group: 'engine' }), { path: '/costs', query: { group: 'engine', engine: 'cursor' } })
})

test('breakdownLink: model rows, the legacy key and keys the filters would drop are no links', () => {
  const f = { range: '30d', group: 'project' }
  assert.equal(breakdownLink('model', 'claude-opus-5-5', f), null)
  assert.equal(breakdownLink('project', '', f), null)
  assert.equal(breakdownLink('kind', '', f), null)
  assert.equal(breakdownLink('project', 'noslash', f), null)
  assert.equal(breakdownLink('kind', 'nope', f), null)
  assert.equal(breakdownLink('engine', 'gpt', f), null)
})

const row = { namespace: 'team-a', project: 'shop', label: 'x', at: '', totals: {} }

test('topRowLocation: milestone opens the milestone tab', () => {
  assert.deepEqual(topRowLocation('milestone', { ...row, milestoneIID: 14 }), {
    name: 'stream-milestone',
    params: { namespace: 'team-a', name: 'shop', iid: '14' },
    query: { tab: 'spend' },
  })
})

test('topRowLocation: issue keeps its milestone', () => {
  assert.deepEqual(topRowLocation('issue', { ...row, milestoneIID: 14, issueIID: 281 }), {
    name: 'stream-milestone',
    params: { namespace: 'team-a', name: 'shop', iid: '14' },
    query: { tab: 'spend', spendIssue: '281' },
  })
})

test('topRowLocation: run keeps milestone, issue, task and run', () => {
  assert.deepEqual(topRowLocation('run', { ...row, milestoneIID: 14, issueIID: 281, task: 'shop-i281', run: 3 }).query, {
    tab: 'spend',
    spendIssue: '281',
    spendTask: 'shop-i281',
    spendRun: '3',
  })
})

test('topRowLocation: a research run has no milestone, the project tab', () => {
  assert.deepEqual(topRowLocation('run', { ...row, task: 'research/payments-news', run: 2, kind: 'research' }), {
    name: 'stream-project',
    params: { namespace: 'team-a', name: 'shop' },
    query: { tab: 'spend', spendTask: 'research/payments-news', spendRun: '2' },
  })
})

const T = (o = {}) => ({
  costUSD: '1.50', costKnown: true, unknownCalls: 0, unmeteredCalls: 0, oldBasisCalls: 0,
  inputTokens: 10, outputTokens: 20, cacheReadTokens: 30, cacheWriteTokens: 40,
  calls: 2, subagentCalls: 0, durationMS: 500, checkpoints: 0, ...o,
})

test('csvCell: RFC 4180 quoting', () => {
  assert.equal(csvCell('plain'), 'plain')
  assert.equal(csvCell('a,b'), '"a,b"')
  assert.equal(csvCell('say "hi"'), '"say ""hi"""')
  assert.equal(csvCell('two\nlines'), '"two\nlines"')
  assert.equal(csvCell(7), '7')
  assert.equal(csvCell(null), '')
})

test('csvCell: formula injection guard', () => {
  for (const c of ['=SUM(A1)', '+1', '-1', '@x']) assert.equal(csvCell(c), `'${c}`)
  assert.equal(csvCell('=a,b'), `"'=a,b"`)
  assert.equal(csvCell('a=b'), 'a=b')
})

const view = () => ({
  from: '2026-11-01', to: '2026-11-12', group: 'project',
  series: [
    { bucket: '2026-11-01', key: '=evil', other: false, totals: T() },
    { bucket: '2026-11-01', key: '', other: false, totals: T({ costUSD: '', costKnown: false, unknownCalls: 2, unmeteredCalls: 2 }) },
    { bucket: '2026-11-02', key: '', other: true, totals: T() },
  ],
  byProject: [{ key: 'ns/a', totals: T(), count: 0 }],
  byKind: [{ key: 'review', totals: T(), count: 0 }],
  byModel: [{ key: '', totals: T(), count: 0 }],
  byEngine: [{ key: 'claude', totals: T({ costUSD: '', costKnown: false }), count: 0 }],
})

test('seriesCsv: columns, guard, legacy key and unknown cost', () => {
  const lines = seriesCsv(view()).split('\r\n')
  assert.equal(
    lines[0],
    'bucket,group,key,other,known cost USD,unknown calls,unmetered calls,input tokens,output tokens,cache read tokens,cache write tokens,calls,duration ms',
  )
  assert.equal(lines[1], "2026-11-01,project,'=evil,false,1.50,0,0,10,20,30,40,2,500")
  assert.equal(lines[2], '2026-11-01,project,before calls,false,,2,2,10,20,30,40,2,500')
  assert.equal(lines[3], '2026-11-02,project,,true,1.50,0,0,10,20,30,40,2,500')
  assert.equal(lines[4], '')
})

test('breakdownsCsv: one row per dimension and key', () => {
  const lines = breakdownsCsv(view()).split('\r\n')
  assert.equal(lines[0].split(',').slice(0, 3).join(','), 'dimension,key,known cost USD')
  assert.equal(lines[1], 'project,ns/a,1.50,0,0,10,20,30,40,2,500')
  assert.equal(lines[3], 'model,before calls,1.50,0,0,10,20,30,40,2,500')
  assert.equal(lines[4], 'engine,claude,,0,0,10,20,30,40,2,500')
})
