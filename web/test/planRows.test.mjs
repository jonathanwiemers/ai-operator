// Tests of src/planRows.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { planPhaseTone, planTitle } from '../src/planRows.ts'

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
