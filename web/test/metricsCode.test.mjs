// Tests of src/metricsCode.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { categoryLabel, codeChart, codeProjectRows, codeTiles, per100Label, per100Note } from '../src/metricsCode.ts'

const lines = (added, deleted) => ({ added, deleted })
const merged = (p, extra = {}) => ({ production: p, tests: lines(60, 0), docs: lines(10, 0), generated: lines(0, 0), ...extra })
const spend = (extra = {}) => ({
  costUSD: '12.00', costKnown: true, unknownCalls: 0, unmeteredCalls: 0, oldBasisCalls: 0,
  inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0,
  calls: 10, subagentCalls: 0, durationMS: 0, checkpoints: 0, ...extra,
})
const totals = (extra = {}) => ({
  written: lines(300, 50),
  merged: merged(lines(40, 5)),
  issues: { lines: merged(lines(40, 5)), merged: 1, measured: 1, costPer100USD: '26.67' },
  cost: spend(),
  ...extra,
})

const view = {
  range: '7d', from: '2026-11-01', to: '2026-11-02', bucket: 'day', tz: 'Europe/Berlin',
  buckets: [
    { bucket: '2026-11-01', written: lines(100, 20), merged: merged(lines(40, 5)) },
    { bucket: '2026-11-02', written: lines(200, 30), merged: merged(lines(0, 0)) },
  ],
  totals: totals(),
  byProject: [
    { namespace: 'team-a', project: 'shop', totals: totals() },
    { namespace: 'group', project: 'infra', totals: totals({ merged: merged(lines(12, 3), { partial: true }), issues: { lines: merged(lines(12, 3), { partial: true }), merged: 2, measured: 1, costPer100USD: '3.10' } }) },
  ],
}

test('codeChart is the written and merged production lines per bucket', () => {
  const c = codeChart(view, 'en-GB')
  assert.deepEqual(c.labels, ['1 Nov', '2 Nov'])
  assert.deepEqual(
    c.series.map((s) => [s.key, s.label, s.values, s.total]),
    [
      ['written', 'Written lines', [120, 230], 350],
      ['merged', 'Merged production lines', [45, 0], 45],
    ],
  )
  assert.deepEqual(c.unknown, [0, 0])
  assert.notEqual(c.series[0].token, c.series[1].token)
})

test('codeChart of no buckets has empty series', () => {
  const c = codeChart({ ...view, buckets: [] })
  assert.deepEqual(c.labels, [])
  assert.deepEqual(c.series.map((s) => s.total), [0, 0])
})

test('categoryLabel is +A/−D, ≥ when partial', () => {
  assert.equal(categoryLabel(lines(40, 5)), '+40/−5')
  assert.equal(categoryLabel(lines(40, 5), true), '≥ +40/−5')
  assert.equal(categoryLabel(undefined), '+0/−0')
})

test('per100Label is the cost, ≤ when partial, — when unknown or without production lines', () => {
  const t = view.totals.issues
  assert.equal(per100Label(t), '$26.67')
  assert.equal(per100Label({ ...t, lines: { ...t.lines, partial: true } }), '≤ $26.67')
  assert.equal(per100Label({ ...t, costPer100USD: undefined }), '—')
  assert.equal(per100Label({ ...t, costPer100USD: '' }), '—')
  assert.equal(per100Label({ ...t, lines: merged(lines(0, 0)) }), '—')
  assert.equal(per100Label(undefined), '—')
})

test('codeTiles: written, merged by category and the cost per 100', () => {
  assert.deepEqual(
    codeTiles(view.totals).map((t) => [t.id, t.value, t.note]),
    [
      ['written', '+300/−50', undefined],
      ['production', '+40/−5', undefined],
      ['tests', '+60/−0', undefined],
      ['docs', '+10/−0', undefined],
      ['generated', '+0/−0', undefined],
      ['per100', '$26.67', undefined],
    ],
  )
  const partial = codeTiles(view.byProject[1].totals)
  assert.equal(partial.find((t) => t.id === 'production').value, '≥ +12/−3')
  assert.equal(partial.find((t) => t.id === 'tests').value, '≥ +60/−0')
  assert.equal(partial.find((t) => t.id === 'per100').value, '≤ $3.10')
})

test('per100Note carries the measured count and the cost notes', () => {
  assert.equal(per100Note(view.totals), '')
  const t = totals({ issues: { ...view.totals.issues, merged: 3, measured: 2 }, cost: spend({ oldBasisCalls: 4, unmeteredCalls: 1 }) })
  assert.equal(per100Note(t), '2 of 3 measured · 4 of 10 calls on the old token basis · 1 call not metered')
  assert.equal(codeTiles(t).at(-1).note, per100Note(t))
})

test('codeProjectRows: merged production lines and cost per 100 per project', () => {
  assert.deepEqual(codeProjectRows(view), [
    { project: 'team-a/shop', production: '+40/−5', per100: '$26.67' },
    { project: 'group/infra', production: '≥ +12/−3', per100: '≤ $3.10' },
  ])
  assert.deepEqual(codeProjectRows({ ...view, byProject: [] }), [])
})
