// Tests of src/milestoneRows.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  issueProgress,
  issueStateTone,
  issueTime,
  leftoversLabel,
  milestoneRowKey,
  progressPct,
  rowCost,
  rowTokens,
  sortInProgress,
  stateTone,
} from '../src/milestoneRows.ts'

const row = (iid, state, needsYou = false) => ({ namespace: 'ns', project: 'p', iid, state, needsYou })

test('sortInProgress: needs you, running, waiting, then paused and planned; server order within a rank', () => {
  const rows = [
    row(1, 'planned'),
    row(2, 'waiting'),
    row(3, 'paused'),
    row(4, 'running'),
    row(5, 'paused', true),
    row(6, 'running'),
    row(7, 'waiting'),
    row(8, 'running', true),
  ]
  assert.deepEqual(
    sortInProgress(rows).map((r) => r.iid),
    [5, 8, 4, 6, 2, 7, 1, 3],
  )
  // A new array: the input keeps its order.
  assert.deepEqual(
    rows.map((r) => r.iid),
    [1, 2, 3, 4, 5, 6, 7, 8],
  )
  assert.deepEqual(sortInProgress([]), [])
})

test('rowCost: a dash when the cost is unknown, never $0', () => {
  assert.equal(rowCost({ costUSD: '', costKnown: false }), '—')
  assert.equal(rowCost({ costUSD: '0.00', costKnown: false }), '—')
  assert.equal(rowCost({ costUSD: '12.346', costKnown: true }), '$12.35')
  assert.equal(rowCost({ costUSD: '0.004', costKnown: true }), '<$0.01')
  assert.equal(rowTokens({ tokens: 0 }), '—')
  assert.equal(rowTokens({ tokens: 1200 }), '1.2k')
})

test('progressPct: rounded, 0 without issues, capped', () => {
  assert.equal(progressPct({ progress: { merged: 0, total: 0, open: 0 } }), 0)
  assert.equal(progressPct({ progress: { merged: 1, total: 3, open: 2 } }), 33)
  assert.equal(progressPct({ progress: { merged: 2, total: 3, open: 1 } }), 67)
  assert.equal(progressPct({ progress: { merged: 1, total: 8, open: 7 } }), 13)
  assert.equal(progressPct({ progress: { merged: 4, total: 4, open: 0 } }), 100)
  assert.equal(progressPct({ progress: { merged: 5, total: 4, open: 0 } }), 100)
})

test('leftoversLabel: singular, plural, empty without any', () => {
  assert.equal(leftoversLabel({ followUpsOpen: 0 }), '')
  assert.equal(leftoversLabel({ followUpsOpen: 1 }), '1 leftover')
  assert.equal(leftoversLabel({ followUpsOpen: 3 }), '3 leftovers')
})

test('stateTone and milestoneRowKey', () => {
  assert.deepEqual(
    ['running', 'paused', 'waiting', 'done', 'planned'].map(stateTone),
    ['progress', 'warning', 'neutral', 'success', 'neutral'],
  )
  assert.equal(milestoneRowKey(row(3, 'running')), 'ns/p/3')
})

test('issueProgress: merged and skipped count as done', () => {
  const issues = [{ state: 'merged' }, { state: 'skipped' }, { state: 'active' }, { state: 'planned' }]
  assert.deepEqual(issueProgress(issues), { merged: 2, total: 4, open: 2 })
  assert.deepEqual(issueProgress([]), { merged: 0, total: 0, open: 0 })
})

test('issueStateTone: active runs, merged succeeded', () => {
  assert.equal(issueStateTone('active'), 'progress')
  assert.equal(issueStateTone('merged'), 'success')
  assert.equal(issueStateTone('skipped'), 'muted')
  assert.equal(issueStateTone('planned'), 'neutral')
})

test('issueTime: a dash without figures or a run', () => {
  const figures = { wallSec: 3900, agentSec: 600, pausedSec: 0, costUSD: '', costKnown: false, tokens: 0 }
  assert.equal(issueTime({ figures }, 'wallSec'), '1h 5m')
  assert.equal(issueTime({ figures }, 'agentSec'), '10m')
  assert.equal(issueTime({ figures }, 'pausedSec'), '0m')
  assert.equal(issueTime({ figures: { ...figures, wallSec: 0 } }, 'wallSec'), '—')
  assert.equal(issueTime({}, 'agentSec'), '—')
})
