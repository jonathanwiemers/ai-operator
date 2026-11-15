// Tests of src/taskHealth.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { restartsText, showHealthLine, stepAgeText } from '../src/taskHealth.ts'

test('showHealthLine hides when there is no health, pod or step', () => {
  assert.equal(showHealthLine(undefined), false)
  assert.equal(showHealthLine(null), false)
  assert.equal(showHealthLine({}), false)
  assert.equal(showHealthLine({ stalled: true, stalledReason: 'Silent' }), false)
})

test('showHealthLine shows when there is a pod phase or a step', () => {
  assert.equal(showHealthLine({ podPhase: 'Running' }), true)
  assert.equal(showHealthLine({ step: '18 push' }), true)
  assert.equal(showHealthLine({ podPhase: 'Pending', step: '1 clone' }), true)
})

test('restartsText counts and adds the last termination reason', () => {
  assert.equal(restartsText({}), '0 restarts')
  assert.equal(restartsText({ restarts: 1 }), '1 restart')
  assert.equal(restartsText({ restarts: 3, lastTermination: 'OOMKilled' }), '3 restarts (OOMKilled)')
})

test('stepAgeText is the short age from stepSince', () => {
  const now = Date.parse('2026-11-15T12:42:00Z')
  assert.equal(stepAgeText(undefined, now), '')
  assert.equal(stepAgeText('not a time', now), '')
  assert.equal(stepAgeText('2026-11-15T12:00:00Z', now), '42m')
  assert.equal(stepAgeText('2026-11-15T12:41:30Z', now), '<1m')
})
