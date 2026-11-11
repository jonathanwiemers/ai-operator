// Tests of src/colorScheme.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { darkQuery, followColorScheme } from '../src/colorScheme.ts'

// fakeQuery stands in for matchMedia(darkQuery): set(matches) changes the
// system scheme and fires the change listeners.
function fakeQuery(matches) {
  const listeners = []
  return {
    matches,
    addEventListener(type, listener) {
      assert.equal(type, 'change')
      listeners.push(listener)
    },
    set(next) {
      this.matches = next
      for (const l of listeners) l({ matches: next })
    },
  }
}

// fakeClasses stands in for document.documentElement.classList.
function fakeClasses(...initial) {
  const set = new Set(initial)
  return {
    has: (t) => set.has(t),
    toggle(token, force) {
      const on = force ?? !set.has(token)
      if (on) set.add(token)
      else set.delete(token)
      return on
    },
  }
}

test('darkQuery asks for the dark system scheme', () => {
  assert.equal(darkQuery, '(prefers-color-scheme: dark)')
})

test('a dark system sets dark at once', () => {
  const classes = fakeClasses()
  followColorScheme(classes, fakeQuery(true))
  assert.equal(classes.has('dark'), true)
})

test('a light system leaves dark off, and clears a stale one', () => {
  const fresh = fakeClasses()
  followColorScheme(fresh, fakeQuery(false))
  assert.equal(fresh.has('dark'), false)

  const stale = fakeClasses('dark')
  followColorScheme(stale, fakeQuery(false))
  assert.equal(stale.has('dark'), false)
})

test('a change of the system scheme switches dark live', () => {
  const classes = fakeClasses()
  const query = fakeQuery(false)
  followColorScheme(classes, query)

  query.set(true)
  assert.equal(classes.has('dark'), true)
  query.set(true)
  assert.equal(classes.has('dark'), true, 'a repeated event keeps dark')
  query.set(false)
  assert.equal(classes.has('dark'), false)
})
