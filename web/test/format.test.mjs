// Tests of src/format.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fullTime } from '../src/format.ts'

test('fullTime shows the date, time and time zone', () => {
  const s = fullTime('2026-11-02T13:59:00Z')
  assert.match(s, /2026/)
  assert.match(s, /\d{1,2}:59/)
  assert.ok(s.length > 'Nov 2, 2026, 13:59'.length, s)
})

test('fullTime is empty for a missing or invalid time', () => {
  assert.equal(fullTime(undefined), '')
  assert.equal(fullTime('not a time'), '')
})
