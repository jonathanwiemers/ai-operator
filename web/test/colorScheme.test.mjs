// Tests of src/colorScheme.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { darkQuery, readTheme, themeController, themeKey } from '../src/colorScheme.ts'

// fakeQuery stands in for matchMedia(darkQuery): set(matches) changes the
// system scheme and fires the change listeners.
function fakeQuery(matches) {
  const listeners = []
  return {
    matches,
    listeners,
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

// fakeStorage stands in for window.localStorage; throwRead and throwWrite
// make it fail like a blocked storage.
function fakeStorage(initial = {}, { throwRead = false, throwWrite = false } = {}) {
  const data = { ...initial }
  return {
    data,
    getItem(key) {
      if (throwRead) throw new Error('SecurityError')
      return key in data ? data[key] : null
    },
    setItem(key, value) {
      if (throwWrite) throw new Error('QuotaExceededError')
      data[key] = String(value)
    },
  }
}

test('darkQuery asks for the dark system scheme, the key is desk.theme', () => {
  assert.equal(darkQuery, '(prefers-color-scheme: dark)')
  assert.equal(themeKey, 'desk.theme')
})

test('readTheme: a stored choice, else Auto', () => {
  assert.equal(readTheme(fakeStorage({ 'desk.theme': 'light' })), 'light')
  assert.equal(readTheme(fakeStorage({ 'desk.theme': 'dark' })), 'dark')
  assert.equal(readTheme(fakeStorage({ 'desk.theme': 'auto' })), 'auto')
  assert.equal(readTheme(fakeStorage()), 'auto', 'missing')
  assert.equal(readTheme(fakeStorage({ 'desk.theme': 'Dark' })), 'auto', 'unknown')
  assert.equal(readTheme(fakeStorage({ 'desk.theme': '' })), 'auto', 'empty')
  assert.equal(readTheme(fakeStorage({}, { throwRead: true })), 'auto', 'throwing read')
  assert.equal(readTheme(null), 'auto', 'no storage')
})

test('auto follows the system at once, and clears a stale dark', () => {
  const dark = fakeClasses()
  assert.equal(themeController(dark, fakeQuery(true), fakeStorage()).get(), 'auto')
  assert.equal(dark.has('dark'), true)

  const stale = fakeClasses('dark')
  themeController(stale, fakeQuery(false), fakeStorage())
  assert.equal(stale.has('dark'), false)
})

test('a stored light or dark wins over the system', () => {
  const light = fakeClasses('dark')
  themeController(light, fakeQuery(true), fakeStorage({ 'desk.theme': 'light' }))
  assert.equal(light.has('dark'), false)

  const dark = fakeClasses()
  themeController(dark, fakeQuery(false), fakeStorage({ 'desk.theme': 'dark' }))
  assert.equal(dark.has('dark'), true)
})

test('in auto a system change switches dark live, through one listener', () => {
  const classes = fakeClasses()
  const query = fakeQuery(false)
  themeController(classes, query, fakeStorage())
  assert.equal(query.listeners.length, 1)

  query.set(true)
  assert.equal(classes.has('dark'), true)
  query.set(true)
  assert.equal(classes.has('dark'), true, 'a repeated event keeps dark')
  query.set(false)
  assert.equal(classes.has('dark'), false)
})

test('set stores the choice and applies it at once', () => {
  const classes = fakeClasses()
  const query = fakeQuery(false)
  const storage = fakeStorage()
  const theme = themeController(classes, query, storage)

  theme.set('dark')
  assert.equal(theme.get(), 'dark')
  assert.equal(storage.data['desk.theme'], 'dark')
  assert.equal(classes.has('dark'), true)

  theme.set('light')
  assert.equal(storage.data['desk.theme'], 'light')
  assert.equal(classes.has('dark'), false)
  assert.equal(query.listeners.length, 1, 'set adds no listener')
})

test('light and dark ignore system changes', () => {
  const classes = fakeClasses()
  const query = fakeQuery(false)
  const theme = themeController(classes, query, fakeStorage())

  theme.set('dark')
  query.set(false)
  assert.equal(classes.has('dark'), true, 'dark stays with a light system')

  theme.set('light')
  query.set(true)
  assert.equal(classes.has('dark'), false, 'light stays with a dark system')
})

test('back to auto applies the system at once and follows it again', () => {
  const classes = fakeClasses()
  const query = fakeQuery(false)
  const theme = themeController(classes, query, fakeStorage({ 'desk.theme': 'light' }))

  query.set(true)
  assert.equal(classes.has('dark'), false)
  theme.set('auto')
  assert.equal(classes.has('dark'), true, 'auto re-applies query.matches')
  query.set(false)
  assert.equal(classes.has('dark'), false)
})

test('a throwing write keeps the choice for the page', () => {
  const classes = fakeClasses()
  const theme = themeController(classes, fakeQuery(false), fakeStorage({}, { throwRead: true, throwWrite: true }))
  assert.equal(theme.get(), 'auto')
  theme.set('dark')
  assert.equal(theme.get(), 'dark')
  assert.equal(classes.has('dark'), true)
})

test('without storage the controller still switches', () => {
  const classes = fakeClasses()
  const theme = themeController(classes, fakeQuery(true), null)
  assert.equal(classes.has('dark'), true)
  theme.set('light')
  assert.equal(classes.has('dark'), false)
})
