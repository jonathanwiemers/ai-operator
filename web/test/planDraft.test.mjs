// Tests of src/planDraft.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { draftConflictFrom, DraftConflict } from '../src/planDraft.ts'

const view = {
  name: 'shop-plan-new',
  title: 'Refunds',
  phase: 'Ready',
  createdBy: 'ada',
  createdAt: '2026-11-01T00:00:00Z',
  lastActivityAt: '2026-11-01T00:00:00Z',
  spendUSD: '0.00',
  tokens: 0,
  budgetUSD: '5',
  budgetLimitUSD: '20',
  permissions: { steer: true },
  draft: '# Refunds',
  draftRevision: 4,
}

test('draftConflictFrom: 409 with a plan view is DraftConflict', () => {
  const c = draftConflictFrom(409, view)
  assert.ok(c instanceof DraftConflict)
  assert.equal(c.view.name, 'shop-plan-new')
  assert.equal(c.view.draftRevision, 4)
  assert.equal(c.view.draft, '# Refunds')
})

test('draftConflictFrom: other statuses and error envelopes are not', () => {
  assert.equal(draftConflictFrom(200, view), null)
  assert.equal(draftConflictFrom(409, { error: 'the plan has created its milestone' }), null)
  assert.equal(draftConflictFrom(409, { name: 'x' }), null)
  assert.equal(draftConflictFrom(409, {}), null)
})
