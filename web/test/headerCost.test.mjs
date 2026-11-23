// Tests of src/headerCost.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { headerCost, keepCost, totalsCost } from '../src/headerCost.ts'

const calls = (usd, tokens = 10) => ({ costUSD: usd, costKnown: true, tokens, costFrom: 'calls' })
const tasks = (usd) => ({ costUSD: usd, costKnown: true, tokens: 0, costFrom: 'tasks' })
const project = { ...calls('40.00'), milestones: [{ iid: 14, ...calls('12.00') }, { iid: 15, ...calls('3.00') }] }

test('headerCost: a project page shows the project', () => {
  assert.equal(headerCost(project, null, undefined), project)
  assert.equal(headerCost(null, null, undefined), null)
})

test('headerCost: a milestone page shows the loaded milestone', () => {
  const m = { iid: 14, ...calls('12.50') }
  assert.equal(headerCost(project, m, 14), m)
})

test('headerCost: while the milestone loads, the project list copy', () => {
  assert.equal(headerCost(project, null, 14), project.milestones[0])
  // A milestone loaded earlier, another one: not this page's.
  assert.equal(headerCost(project, { iid: 15, ...calls('3.00') }, 14), project.milestones[0])
  assert.equal(headerCost(project, null, 99), null)
  assert.equal(headerCost(null, null, 14), null)
})

test('headerCost: a tasks-based milestone gives way to the calls-based list copy', () => {
  assert.equal(headerCost(project, { iid: 14, ...tasks('2.00') }, 14), project.milestones[0])
  const listTasks = { ...project, milestones: [{ iid: 14, ...tasks('2.00') }] }
  const m = { iid: 14, ...calls('12.50') }
  assert.equal(headerCost(listTasks, m, 14), m)
})

test('keepCost: a refresh that fell back to the tasks never replaces a cost from calls', () => {
  const shown = calls('40.00')
  assert.equal(keepCost(shown, tasks('8.00')), shown)
  // A larger sum from calls replaces it.
  const newer = calls('41.20')
  assert.equal(keepCost(shown, newer), newer)
  assert.equal(keepCost(shown, calls('40.00', 11)).tokens, 11)
  assert.equal(keepCost(tasks('8.00'), shown), shown)
  assert.equal(keepCost(shown, null), shown)
  assert.equal(keepCost(null, null), null)
})

test('keepCost: a lagging copy from calls never replaces a larger sum', () => {
  // The Metrics totals came first, then a project reload from before the
  // server recorded the last calls: the header keeps the Metrics number.
  const metrics = calls('41.20', 500)
  assert.equal(keepCost(metrics, calls('40.00', 480)), metrics)
  // More tokens wins over a higher cost (legacy runs carry no tokens).
  assert.equal(keepCost(metrics, calls('41.50', 490)), metrics)
  // An equal sum is the newer copy.
  const same = calls('41.20', 500)
  assert.equal(keepCost(metrics, same), same)
  // Unknown costs ("") count as 0.
  assert.equal(keepCost(calls('', 5), calls('1.00', 5)).costUSD, '1.00')
})

test('totalsCost: the Metrics totals as the header shows them', () => {
  const t = {
    costUSD: '41.20',
    costKnown: true,
    inputTokens: 1,
    outputTokens: 2,
    cacheReadTokens: 30,
    cacheWriteTokens: 400,
    calls: 3,
  }
  assert.deepEqual(totalsCost(t), { costUSD: '41.20', costKnown: true, tokens: 433, costFrom: 'calls', spend: t })
  // The Metrics tile's larger number wins over the project's lagging copy.
  assert.equal(keepCost(calls('40.00', 400), totalsCost(t)).costUSD, '41.20')
  assert.equal(keepCost(totalsCost(t), calls('40.00', 400)).costUSD, '41.20')
})
