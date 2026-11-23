import assert from 'node:assert/strict'
import test from 'node:test'
import { costsRedirect } from '../src/costsRedirect.ts'

test('costsRedirect: /costs goes to /metrics with the same query and hash', () => {
  const query = { range: '7d', project: 'team-a/shop' }
  assert.deepEqual(costsRedirect({ query, hash: '#costs-tokens' }), { path: '/metrics', query, hash: '#costs-tokens' })
})
