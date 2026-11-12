// Tests of src/reconnect.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { isRenewalEnd, quickReconnectEveryMs, quietReconnectMs, reconnectAction } from '../src/reconnect.ts'

const now = Date.parse('2026-11-12T10:00:00Z')

test('reconnectAction: the first error after an open reconnects at once', () => {
  assert.equal(reconnectAction(true, false, -Infinity, now), 'now')
  assert.equal(reconnectAction(true, true, -Infinity, now), 'now')
  assert.equal(reconnectAction(true, false, now - quickReconnectEveryMs, now), 'now')
})

test('reconnectAction: repeated errors use the browser retry or the backoff', () => {
  assert.equal(reconnectAction(false, false, -Infinity, now), 'browser')
  assert.equal(reconnectAction(false, true, -Infinity, now), 'backoff')
})

test('reconnectAction: a stream that ends again soon after a quick reconnect waits', () => {
  assert.equal(reconnectAction(true, false, now - 1000, now), 'browser')
  assert.equal(reconnectAction(true, true, now - quickReconnectEveryMs + 1, now), 'backoff')
})

test('quietReconnectMs: disconnected shows after 5 seconds', () => {
  assert.equal(quietReconnectMs, 5000)
})

test('isRenewalEnd: only a session expiry reopens', () => {
  assert.equal(isRenewalEnd('session expired'), true)
  for (const reason of ['pod stopped', 'no pod', '', undefined]) assert.equal(isRenewalEnd(reason), false)
})
