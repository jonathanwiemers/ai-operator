// Tests of src/metricsFlow.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { flowChart, flowTiles } from '../src/metricsFlow.ts'

const totals = {
  mergedIssues: 4,
  wallIssues: 4,
  medianWallSec: 3 * 3600 + 20 * 60,
  agentIssues: 3,
  medianAgentSec: 52 * 60,
  reviewCallsPerIssue: 1.5,
  fixCallsPerIssue: 0.25,
}
const view = {
  range: '7d', from: '2026-11-01', to: '2026-11-03', bucket: 'day', tz: 'Europe/Berlin',
  buckets: [
    { bucket: '2026-11-01', merged: 1 },
    { bucket: '2026-11-02', merged: 0 },
    { bucket: '2026-11-03', merged: 3 },
  ],
  totals,
}

test('flowChart is the merged issues per bucket', () => {
  const c = flowChart(view, 'en-GB')
  assert.deepEqual(c.labels, ['1 Nov', '2 Nov', '3 Nov'])
  assert.equal(c.series.length, 1)
  assert.deepEqual(c.series[0].values, [1, 0, 3])
  assert.equal(c.series[0].total, 4)
  assert.equal(c.series[0].label, 'Merged issues')
  assert.deepEqual(c.unknown, [0, 0, 0])
})

test('flowTiles: median times and calls per merged issue', () => {
  assert.deepEqual(
    flowTiles(totals).map((t) => [t.id, t.value]),
    [
      ['wall', '3h 20m'],
      ['agent', '52m'],
      ['review', '1.5'],
      ['fix', '0.3'],
    ],
  )
})

test('flowTiles without merged issues are dashes, never 0', () => {
  const none = { mergedIssues: 0, wallIssues: 0, medianWallSec: 0, agentIssues: 0, medianAgentSec: 0, reviewCallsPerIssue: 0, fixCallsPerIssue: 0 }
  assert.deepEqual(flowTiles(none).map((t) => t.value), ['—', '—', '—', '—'])
})
