// Tests of src/logTail.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { appendLog, maxFollowLines } from '../src/logTail.ts'

test('appendLog appends while under the cap', () => {
  assert.equal(appendLog('a\n', 'b\nc'), 'a\nb\nc')
  assert.equal(appendLog('a\nb', 'c\n'), 'a\nbc\n')
})

test('appendLog keeps the last max lines', () => {
  assert.equal(appendLog('a\nb\n', 'c\nd\n', 3), 'b\nc\nd\n')
  assert.equal(appendLog('a\nb\n', 'c\nd', 3), 'b\nc\nd')
  assert.equal(appendLog('a\nb\nc\n', '', 3), 'a\nb\nc\n')
})

test('appendLog caps at 2000 lines by default', () => {
  const text = 'x\n'.repeat(maxFollowLines)
  assert.equal(appendLog(text, 'y\n'), 'x\n'.repeat(maxFollowLines - 1) + 'y\n')
})
