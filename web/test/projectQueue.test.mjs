// Tests of src/projectQueue.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { needsEstimate, queueActions, queueStateLabel, queueTone } from '../src/projectQueue.ts'

const entry = (over = {}) => ({ state: 'running', done: 0, costUSD: '0.00', tokens: 0, ...over })

test('needsEstimate: waiting/holding with no done and no spend', () => {
  assert.equal(needsEstimate(entry({ state: 'waiting' })), true)
  assert.equal(needsEstimate(entry({ state: 'holding' })), true)
  assert.equal(needsEstimate(entry({ state: 'waiting', costUSD: '' })), true)
})

test('needsEstimate: running, done, or spent do not', () => {
  assert.equal(needsEstimate(entry({ state: 'running' })), false)
  assert.equal(needsEstimate(entry({ state: 'paused' })), false)
  assert.equal(needsEstimate(entry({ state: 'budget' })), false)
  assert.equal(needsEstimate(entry({ state: 'waiting', done: 1 })), false)
  assert.equal(needsEstimate(entry({ state: 'holding', costUSD: '1.25' })), false)
  assert.equal(needsEstimate(entry({ state: 'waiting', tokens: 100 })), false)
})

test('queueStateLabel: one label per state', () => {
  assert.equal(queueStateLabel(entry({ state: 'running' })), 'Running')
  assert.equal(queueStateLabel(entry({ state: 'holding', holdingFor: 3 })), 'Holding for %3')
  assert.equal(queueStateLabel(entry({ state: 'waiting', holdingFor: 3 })), 'Waiting to plan')
  assert.equal(queueStateLabel(entry({ state: 'paused', pausedReason: 'CI is red' })), 'Paused: CI is red')
  assert.equal(queueStateLabel(entry({ state: 'paused' })), 'Paused')
  assert.equal(queueStateLabel(entry({ state: 'budget' })), 'Budget')
})

test('queueTone: one tone per state', () => {
  assert.equal(queueTone(entry({ state: 'running' })), 'progress')
  assert.equal(queueTone(entry({ state: 'holding' })), 'muted')
  assert.equal(queueTone(entry({ state: 'waiting' })), 'muted')
  assert.equal(queueTone(entry({ state: 'paused' })), 'warning')
  assert.equal(queueTone(entry({ state: 'budget' })), 'danger')
})

test('queueActions: first unmarked gets Run next and Pause', () => {
  assert.deepEqual(queueActions(entry({ state: 'running' }), 0), [
    { id: 'runNext', label: 'Run next', cmd: { cmd: 'start' } },
    { id: 'pause', label: 'Pause', cmd: { cmd: 'pause' } },
  ])
})

test('queueActions: marked gets Clear mark instead of Run next', () => {
  assert.deepEqual(queueActions(entry({ state: 'running', markedBy: '@ada' }), 0), [
    { id: 'clearMark', label: 'Clear mark', cmd: { cmd: 'unstart' } },
    { id: 'pause', label: 'Pause', cmd: { cmd: 'pause' } },
  ])
})

test('queueActions: paused gets Resume; waiting still Run next', () => {
  assert.deepEqual(queueActions(entry({ state: 'paused', pausedReason: 'CI is red' }), 2), [
    { id: 'runNext', label: 'Run next', cmd: { cmd: 'start' } },
    { id: 'resume', label: 'Resume', cmd: { cmd: 'resume' } },
  ])
})

test('queueActions: budget gets Resume; holding behind first gets Run next', () => {
  assert.deepEqual(queueActions(entry({ state: 'budget' }), 1), [
    { id: 'runNext', label: 'Run next', cmd: { cmd: 'start' } },
    { id: 'resume', label: 'Resume', cmd: { cmd: 'resume' } },
  ])
  assert.deepEqual(queueActions(entry({ state: 'holding', holdingFor: 5 }), 1), [
    { id: 'runNext', label: 'Run next', cmd: { cmd: 'start' } },
    { id: 'pause', label: 'Pause', cmd: { cmd: 'pause' } },
  ])
})
