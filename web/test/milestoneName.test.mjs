// Tests of src/milestoneName.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { milestoneName } from '../src/milestoneName.ts'

test('milestoneName: the title with %n as secondary label, %n alone without a title', () => {
  assert.deepEqual(milestoneName(3, 'Guest checkout'), { title: 'Guest checkout', ref: '%3' })
  assert.deepEqual(milestoneName('3', '  Guest checkout '), { title: 'Guest checkout', ref: '%3' })
  assert.deepEqual(milestoneName(3, ''), { title: '%3', ref: '' })
  assert.deepEqual(milestoneName(3, '   '), { title: '%3', ref: '' })
  assert.deepEqual(milestoneName(3), { title: '%3', ref: '' })
  assert.deepEqual(milestoneName(3, null), { title: '%3', ref: '' })
})
