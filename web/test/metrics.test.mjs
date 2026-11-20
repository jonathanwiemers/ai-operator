// Tests of src/metrics.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { activeFilters, byteLabel, filtersLabel, hasContext, issuesLabel, keyLabel, metricsTables, per100Label } from '../src/metrics.ts'

const group = (key, extra = {}) => ({ key, issues: 5, mergedLines: 500, costUSD: 11, tokens: 600, costPer100Lines: 2.2, low: false, ...extra })

test('metricsTables: one arm table per project, then the three breakdowns', () => {
  const v = {
    arms: [
      group('sonnet', { project: 'agents/billing' }),
      group('baseline', { project: 'agents/shop', experiment: 'cost-levers' }),
      group('old', { project: 'agents/shop' }),
    ],
    byModel: [group('claude-opus-5-5')],
    byEffort: [],
    byStepSession: [group('')],
    context: [],
  }
  const tables = metricsTables(v)
  assert.deepEqual(
    tables.map((t) => [t.id, t.title, t.keyLabel, t.rows.map((r) => r.key)]),
    [
      ['arms:agents/billing', 'Arms: agents/billing', 'Arm', ['sonnet']],
      ['arms:agents/shop', 'Arms of cost-levers: agents/shop', 'Arm', ['baseline', 'old']],
      ['model', 'By model', 'Model', ['claude-opus-5-5']],
      ['effort', 'By effort', 'Effort', []],
      ['stepSession', 'By step session', 'Step session', ['']],
    ],
  )
})

test('metricsTables: no arm table without experiment data', () => {
  const tables = metricsTables({ arms: [], byModel: [], byEffort: [], byStepSession: [], context: [] })
  assert.deepEqual(
    tables.map((t) => t.id),
    ['model', 'effort', 'stepSession'],
  )
})

test('keyLabel, issuesLabel, per100Label', () => {
  assert.equal(keyLabel(''), '—')
  assert.equal(keyLabel('fresh'), 'fresh')
  assert.equal(issuesLabel(1), '1 issue')
  assert.equal(issuesLabel(4), '4 issues')
  assert.equal(per100Label(group('a')), '$2.20')
  assert.equal(per100Label(group('a', { mergedLines: 0, costPer100Lines: 0 })), '—')
})

test('byteLabel: B, kB, MB', () => {
  assert.equal(byteLabel(950), '950 B')
  assert.equal(byteLabel(81234), '81.2 kB')
  assert.equal(byteLabel(1000), '1 kB')
  assert.equal(byteLabel(250000), '250 kB')
  assert.equal(byteLabel(1_200_000), '1.2 MB')
})

test('hasContext: any base context or tool', () => {
  assert.equal(hasContext([]), false)
  assert.equal(hasContext([{ project: 'a/b', baseContext: [], topTools: [] }]), false)
  assert.equal(hasContext([{ project: 'a/b', baseContext: [], topTools: [{ tool: 'Read', calls: 1, bytes: 4, tokens: 1 }] }]), true)
})

test('activeFilters counts the value filters not set to All', () => {
  assert.equal(activeFilters({}), 0)
  assert.equal(activeFilters({ project: '', kind: undefined }), 0)
  assert.equal(activeFilters({ project: 'team-a/shop' }), 1)
  assert.equal(activeFilters({ project: 'team-a/shop', kind: 'implement', engine: 'claude' }), 3)
  // The range and the grouping are no value filters.
  assert.equal(activeFilters({ range: '7d', group: 'kind', engine: 'cursor' }), 1)
})

test('filtersLabel: plain at 0, the count otherwise', () => {
  assert.equal(filtersLabel(0), 'Filters')
  assert.equal(filtersLabel(1), 'Filters (1)')
  assert.equal(filtersLabel(3), 'Filters (3)')
})
