// Tests of src/pushSubscribe.ts: npm test (node --test).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { isIOS, keyBytes, pushState, sameKey, subscriptionBody } from '../src/pushSubscribe.ts'

// A P-256 public key as the server sends it (base64url, 65 bytes).
const key = 'BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4'
const info = { available: true, publicKey: key }
const desktop = { info, supported: true, ios: false, standalone: false }

test('pushState: push with a key, a supporting browser and not iOS Safari', () => {
  assert.equal(pushState(desktop), 'push')
  assert.equal(pushState({ ...desktop, ios: true, standalone: true }), 'push')
})

test('pushState: in-tab without PushManager, without an answer or without a key', () => {
  assert.equal(pushState({ ...desktop, supported: false }), 'inTab')
  assert.equal(pushState({ ...desktop, info: null }), 'inTab')
  assert.equal(pushState({ ...desktop, info: { available: true } }), 'inTab')
})

test('pushState: the Home Screen hint on iOS outside the app, also without PushManager', () => {
  assert.equal(pushState({ ...desktop, ios: true }), 'iosHint')
  assert.equal(pushState({ ...desktop, ios: true, supported: false }), 'iosHint')
})

test('pushState: unavailable when the Desk says so, before the iOS hint', () => {
  const off = { available: false, reason: 'Push notifications need the Desk’s database (database.host).' }
  assert.equal(pushState({ ...desktop, info: off }), 'unavailable')
  assert.equal(pushState({ ...desktop, info: off, ios: true }), 'unavailable')
})

test('isIOS: iPhone, iPad, and an iPad with a Mac user agent and touch', () => {
  const iphone = 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1'
  const ipad = 'Mozilla/5.0 (iPad; CPU OS 16_4 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.4 Mobile/15E148 Safari/604.1'
  const mac = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15'
  const android = 'Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36'
  assert.equal(isIOS(iphone, 5), true)
  assert.equal(isIOS(ipad, 5), true)
  assert.equal(isIOS(mac, 5), true)
  assert.equal(isIOS(mac, 0), false)
  assert.equal(isIOS(android, 5), false)
})

test('keyBytes: round trip with base64url, padding restored', () => {
  const bytes = keyBytes(key)
  assert.equal(bytes.length, 65)
  assert.equal(bytes[0], 4)
  assert.equal(Buffer.from(bytes).toString('base64url'), key)
  assert.deepEqual([...keyBytes('-_8')], [0xfb, 0xff])
})

test('sameKey: equal, different, null', () => {
  const buf = keyBytes(key).buffer
  assert.equal(sameKey(buf, key), true)
  const other = keyBytes(key)
  other[64] ^= 1
  assert.equal(sameKey(other.buffer, key), false)
  assert.equal(sameKey(new Uint8Array(3).buffer, key), false)
  assert.equal(sameKey(null, key), false)
})

test('subscriptionBody: endpoint, keys and the picked categories', () => {
  const sub = { endpoint: 'https://fcm.googleapis.com/fcm/send/abc', expirationTime: null, keys: { p256dh: 'BPk', auth: 'c2Vj' } }
  assert.deepEqual(subscriptionBody(sub, { problem: true, failed: false, release: true }), {
    endpoint: 'https://fcm.googleapis.com/fcm/send/abc',
    keys: { p256dh: 'BPk', auth: 'c2Vj' },
    categories: ['problem', 'release'],
  })
  assert.deepEqual(subscriptionBody(sub, { problem: false }).categories, [])
})
