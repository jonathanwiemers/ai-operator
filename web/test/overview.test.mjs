// Tests of src/overview.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { activeLimits, overviewStrip } from '../src/overview.ts'

const base = { running: 2, paused: 1, waiting: 0, needsYou: 3, working: [], history: true }
const spend = (usd, tokens) => ({ costUSD: usd, costKnown: true, tokens, costFrom: 'calls' })

test('overviewStrip: counts, needs you links to the Inbox, spend today and 7 days', () => {
  const chips = overviewStrip({ ...base, spendToday: spend('1.5', 1200), spend7d: spend('12.34', 0) })
  assert.deepEqual(
    chips.map((c) => [c.id, c.value, c.label]),
    [
      ['running', '2', 'running'],
      ['paused', '1', 'paused'],
      ['waiting', '0', 'waiting'],
      ['needsYou', '3', 'need you'],
      ['spendToday', '$1.50 · 1.2k tok', 'today'],
      ['spend7d', '$12.34', 'last 7 days'],
    ],
  )
  assert.deepEqual(chips.filter((c) => c.inbox).map((c) => c.id), ['needsYou'])
  assert.deepEqual(chips.filter((c) => c.warn).map((c) => c.id), ['paused', 'needsYou'])
})

test('overviewStrip: no spend without the database, never $0', () => {
  const chips = overviewStrip({ ...base, paused: 0, needsYou: 0, history: false })
  assert.deepEqual(chips.map((c) => c.id), ['running', 'paused', 'waiting', 'needsYou'])
  assert.ok(!chips.some((c) => c.warn))
})

test('overviewStrip: an unknown spend says n/a', () => {
  const chips = overviewStrip({ ...base, spendToday: { ...spend('0', 0), costKnown: false } })
  assert.equal(chips.find((c) => c.id === 'spendToday').value, 'n/a')
})

test('activeLimits: only limits still in force, soonest first', () => {
  const now = Date.parse('2026-11-07T12:00:00Z')
  const limits = [
    { engine: 'claude', until: '2026-11-07T15:00:00Z' },
    { engine: 'codex', until: '2026-11-07T11:00:00Z' },
    { engine: 'cursor', until: '2026-11-07T13:00:00Z' },
  ]
  assert.deepEqual(activeLimits(limits, now).map((l) => l.engine), ['cursor', 'claude'])
  assert.deepEqual(activeLimits(undefined, now), [])
})
