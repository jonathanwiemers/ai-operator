// Tests of src/sidebarRail.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { railKey, readCollapsed, writeCollapsed } from '../src/sidebarRail.ts'

// fakeStorage stands in for window.localStorage; throwRead and throwWrite
// make it fail like a blocked storage.
function fakeStorage(initial = {}, { throwRead = false, throwWrite = false } = {}) {
  const items = new Map(Object.entries(initial))
  return {
    items,
    getItem(key) {
      if (throwRead) throw new Error('SecurityError')
      return items.has(key) ? items.get(key) : null
    },
    setItem(key, value) {
      if (throwWrite) throw new Error('QuotaExceededError')
      items.set(key, value)
    },
  }
}

test('a missing, invalid or unreadable value is the open column', () => {
  assert.equal(readCollapsed(null), false)
  assert.equal(readCollapsed(fakeStorage()), false)
  assert.equal(readCollapsed(fakeStorage({ [railKey]: 'yes' })), false)
  assert.equal(readCollapsed(fakeStorage({ [railKey]: '' })), false)
  assert.equal(readCollapsed(fakeStorage({ [railKey]: 'rail' }, { throwRead: true })), false)
})

test('the stored rail is read back', () => {
  assert.equal(readCollapsed(fakeStorage({ [railKey]: 'rail' })), true)
  assert.equal(readCollapsed(fakeStorage({ [railKey]: 'column' })), false)
})

test('a write round-trips under the one key', () => {
  const s = fakeStorage()
  writeCollapsed(s, true)
  assert.deepEqual([...s.items], [[railKey, 'rail']])
  assert.equal(readCollapsed(s), true)
  writeCollapsed(s, false)
  assert.deepEqual([...s.items], [[railKey, 'column']])
  assert.equal(readCollapsed(s), false)
})

test('a failing or missing storage does not throw on write', () => {
  assert.doesNotThrow(() => writeCollapsed(fakeStorage({}, { throwWrite: true }), true))
  assert.doesNotThrow(() => writeCollapsed(null, true))
})
