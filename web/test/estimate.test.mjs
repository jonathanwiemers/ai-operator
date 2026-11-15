// Tests of src/estimate.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { estimateLabel } from '../src/estimate.ts'

const e = { lowUSD: '3.2', typicalUSD: '4.5', highUSD: '7', typicalTokens: 1_200_000, notEnoughHistory: false }

test('estimateLabel: range, typical and tokens', () => {
  assert.equal(estimateLabel(e), '$3.20–$7.00 (typical $4.50), ~1.2M tokens')
  assert.equal(estimateLabel({ ...e, lowUSD: '0.004', typicalTokens: 950 }), '<$0.01–$7.00 (typical $4.50), ~950 tokens')
})

test('estimateLabel: not enough history', () => {
  assert.equal(estimateLabel({ ...e, notEnoughHistory: true }), 'not enough history')
})
