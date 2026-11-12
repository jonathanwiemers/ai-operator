import assert from 'node:assert/strict'
import { test } from 'node:test'
import { loginURL, onBeforeSignIn, shouldSignInAgain, signInAgain } from '../src/signInAgain.ts'

const now = Date.parse('2026-11-12T10:00:00Z')

test('shouldSignInAgain: without a marker', () => {
  assert.equal(shouldSignInAgain(null, now, ''), true)
  assert.equal(shouldSignInAgain(null, now, '?tab=chat'), true)
})

test('shouldSignInAgain: a marker younger than 60s blocks', () => {
  assert.equal(shouldSignInAgain(String(now), now, ''), false)
  assert.equal(shouldSignInAgain(String(now - 59_999), now, ''), false)
})

test('shouldSignInAgain: a stale or unreadable marker does not block', () => {
  assert.equal(shouldSignInAgain(String(now - 60_000), now, ''), true)
  assert.equal(shouldSignInAgain('garbage', now, ''), true)
})

test('shouldSignInAgain: login_error in the URL blocks', () => {
  assert.equal(shouldSignInAgain(null, now, '?login_error=refused'), false)
  assert.equal(shouldSignInAgain(null, now, '?tab=chat&login_error='), false)
})

// fakeWindow stands in for the browser's location and sessionStorage.
function fakeWindow(search, items = {}, broken = false) {
  const store = new Map(Object.entries(items))
  const calls = []
  globalThis.window = {
    location: {
      pathname: '/projects/team-a/shop',
      search,
      assign: (url) => calls.push(`assign ${url}`),
    },
    sessionStorage: {
      getItem: (k) => {
        if (broken) throw new Error('SecurityError')
        return store.get(k) ?? null
      },
      setItem: (k, v) => store.set(k, v),
    },
  }
  return { store, calls }
}

test('loginURL returns to the current route', () => {
  fakeWindow('?tab=chat')
  assert.equal(loginURL(), '/oauth2/login?return=%2Fprojects%2Fteam-a%2Fshop%3Ftab%3Dchat')
})

test('signInAgain: a fresh marker leaves it to the sign-in page', () => {
  const { calls } = fakeWindow('', { 'desk-sign-in-again': String(Date.now()) })
  assert.equal(signInAgain(), false)
  assert.deepEqual(calls, [])
})

test('signInAgain: login_error leaves it to the sign-in page', () => {
  const { calls, store } = fakeWindow('?login_error=refused')
  assert.equal(signInAgain(), false)
  assert.deepEqual(calls, [])
  assert.equal(store.has('desk-sign-in-again'), false)
})

test('signInAgain: without sessionStorage there is no automatic sign-in', () => {
  const { calls } = fakeWindow('', {}, true)
  assert.equal(signInAgain(), false)
  assert.deepEqual(calls, [])
})

// Last: after it the module is navigating away for good.
test('signInAgain: runs the hooks, sets the marker, then navigates once', () => {
  const { calls, store } = fakeWindow('?tab=chat', { 'desk-sign-in-again': String(Date.now() - 61_000) })
  onBeforeSignIn(() => calls.push('hook 1'))
  onBeforeSignIn(() => {
    throw new Error('broken hook')
  })
  const off = onBeforeSignIn(() => calls.push('removed'))
  off()
  onBeforeSignIn(() => calls.push('hook 2'))

  const before = Date.now()
  assert.equal(signInAgain(), true)
  assert.deepEqual(calls, [
    'hook 1',
    'hook 2',
    'assign /oauth2/login?return=%2Fprojects%2Fteam-a%2Fshop%3Ftab%3Dchat',
  ])
  assert.ok(Number(store.get('desk-sign-in-again')) >= before)

  // A second 401 while the page leaves: no second navigation, no sign-in page.
  assert.equal(signInAgain(), true)
  assert.equal(calls.length, 3)
})
