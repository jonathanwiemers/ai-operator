// Tests of src/taskHealth.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { restartsText, runInProgress, stepAgeText, taskStatus, taskStep } from '../src/taskHealth.ts'

test('taskStatus is the phase, Suspended while suspended, Pending without a phase', () => {
  assert.deepEqual(taskStatus({ status: { phase: 'Running' } }), { label: 'Running', tone: 'progress' })
  assert.deepEqual(taskStatus({ status: { phase: 'Idle' } }), { label: 'Idle', tone: 'info' })
  assert.deepEqual(taskStatus({ status: { phase: 'Blocked' } }), { label: 'Blocked', tone: 'danger' })
  assert.deepEqual(taskStatus({ suspend: true, status: { phase: 'Running' } }), { label: 'Suspended', tone: 'warning' })
  assert.deepEqual(taskStatus({ status: {} }), { label: 'Pending', tone: 'muted' })
  assert.deepEqual(taskStatus({}), { label: 'Pending', tone: 'muted' })
})

test('taskStep takes the run status step, then the health step, with the age of the health step only', () => {
  assert.deepEqual(taskStep(undefined, undefined), { text: '' })
  assert.deepEqual(taskStep({}, {}), { text: '' })
  assert.deepEqual(taskStep(undefined, null), { text: '' })
  const since = '2026-11-15T12:00:00Z'
  assert.deepEqual(taskStep({ currentStep: '7 review' }, { step: '18 push', stepSince: since }), { text: '7 review' })
  assert.deepEqual(taskStep({}, { step: '18 push', stepSince: since }), { text: '18 push', since })
  assert.deepEqual(taskStep({ currentStep: '18 push' }, { step: '18 push', stepSince: since }), { text: '18 push', since })
})

test('restartsText is empty without restarts', () => {
  assert.equal(restartsText(undefined), '')
  assert.equal(restartsText(null), '')
  assert.equal(restartsText({}), '')
  assert.equal(restartsText({ restarts: 0, podPhase: 'Running' }), '')
})

test('restartsText counts and adds the last termination reason', () => {
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

test('runInProgress: a request ahead of observed, or a current step', () => {
  assert.equal(runInProgress(undefined), false)
  assert.equal(runInProgress({}), false)
  assert.equal(runInProgress({ activeRunRequest: 2, observedRunRequest: 2 }), false)
  assert.equal(runInProgress({ activeRunRequest: 3, observedRunRequest: 2 }), true)
  assert.equal(runInProgress({ observedRunRequest: 2, currentStep: '18 push' }), true)
})
