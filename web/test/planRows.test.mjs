// Tests of src/planRows.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { planHeaderLines, planMilestoneTitle, planPhaseTone, planSpendLabel, planTitle, raiseBudgetError } from '../src/planRows.ts'

test('planTitle: trimmed title, else Untitled plan with the creation date', () => {
  assert.equal(planTitle('Refunds', '2026-11-01T00:00:00Z'), 'Refunds')
  assert.equal(planTitle('  Desk  ', '2026-11-01T00:00:00Z'), 'Desk')
  assert.equal(planTitle('', '2026-11-01T00:00:00Z'), `Untitled plan · ${new Date('2026-11-01T00:00:00Z').toLocaleDateString()}`)
  assert.equal(planTitle('   ', 'not-a-date'), 'Untitled plan')
})

test('planPhaseTone: each plan phase', () => {
  assert.equal(planPhaseTone('Starting'), 'info')
  assert.equal(planPhaseTone('Ready'), 'info')
  assert.equal(planPhaseTone('Working'), 'progress')
  assert.equal(planPhaseTone('Idle'), 'muted')
  assert.equal(planPhaseTone('Created'), 'success')
  assert.equal(planPhaseTone('Failed'), 'danger')
  assert.equal(planPhaseTone(undefined), 'muted')
  assert.equal(planPhaseTone(''), 'muted')
})

test('planSpendLabel: spend of cap', () => {
  assert.equal(planSpendLabel('1.2', '5'), '$1.20 of $5.00')
  assert.equal(planSpendLabel('0', '5.00'), '$0 of $5.00')
})

test('raiseBudgetError: above the cap, at most the limit', () => {
  assert.equal(raiseBudgetError('10', '5', '20'), '')
  assert.equal(raiseBudgetError('$20', '5', '20'), '')
  assert.equal(raiseBudgetError('', '5', '20'), 'Enter an amount in USD.')
  assert.equal(raiseBudgetError('ten', '5', '20'), 'Enter an amount in USD.')
  assert.equal(raiseBudgetError('5', '5', '20'), 'Enter more than $5.00 and at most $20.00.')
  assert.equal(raiseBudgetError('20.01', '5', '20'), 'Enter more than $5.00 and at most $20.00.')
})

test("planMilestoneTitle: the plan's title, else the draft's first heading", () => {
  assert.equal(planMilestoneTitle(' Refunds ', '# Other'), 'Refunds')
  assert.equal(planMilestoneTitle('', 'intro\n\n## Desk: plan milestones ##\n\n# Later'), 'Desk: plan milestones')
  assert.equal(planMilestoneTitle('', 'no heading\n#hashtag'), '')
  assert.equal([...planMilestoneTitle('', `# ${'é'.repeat(300)}`)].length, 255)
})

test('planHeaderLines: markers per toggle, then the owners lines', () => {
  assert.deepEqual(planHeaderLines(true, true).slice(0, 2), ['/agent spec', '/agent autopilot'])
  assert.deepEqual(planHeaderLines(false, true)[0], '/agent autopilot')
  assert.equal(planHeaderLines(false, false).length, 2)
  assert.match(planHeaderLines(true, false)[1], /^\/agent owners /)
})
