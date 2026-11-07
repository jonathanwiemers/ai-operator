// Tests of src/retrospective.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { driverShares, followUpStateLabel, followUpStateTone } from '../src/retrospective.ts'

test('followUpStateLabel: what came of a leftover', () => {
  assert.equal(followUpStateLabel({ state: 'open' }), 'open')
  assert.equal(followUpStateLabel({ state: 'issue', targetIID: 12 }), 'opened as #12')
  assert.equal(followUpStateLabel({ state: 'milestone', targetIID: 3 }), 'taken by %3')
  assert.equal(followUpStateLabel({ state: 'dismissed' }), 'dismissed')
})

test('followUpStateLabel: without a target it names the kind', () => {
  assert.equal(followUpStateLabel({ state: 'issue' }), 'opened as an issue')
  assert.equal(followUpStateLabel({ state: 'milestone', targetIID: 0 }), 'taken by a milestone')
})

test('followUpStateTone: open asks for attention, dismissed is muted', () => {
  assert.equal(followUpStateTone('open'), 'warning')
  assert.equal(followUpStateTone('issue'), 'success')
  assert.equal(followUpStateTone('milestone'), 'success')
  assert.equal(followUpStateTone('dismissed'), 'muted')
})

const d = (label, costUSD, tokens) => ({ label, kind: 'issue', costUSD, costKnown: costUSD !== '', tokens })

test('driverShares: by cost, unknown cost is n/a', () => {
  assert.deepEqual(driverShares([d('#1', '3', 10), d('#2', '1', 99), d('#3', '', 5)]), ['75%', '25%', 'n/a'])
})

test('driverShares: by tokens without a known cost', () => {
  assert.deepEqual(driverShares([d('#1', '', 300), d('#2', '', 100)]), ['75%', '25%'])
  assert.deepEqual(driverShares([]), [])
})
