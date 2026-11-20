// Tests of src/routeFocus.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { movesFocus } from '../src/routeFocus.ts'

// A location as vue-router passes it: the start location matches no record.
const start = { path: '/', matched: [] }
const at = (path) => ({ path, matched: [{}] })

test('movesFocus keeps the first load as the browser has it', () => {
  assert.equal(movesFocus(start, at('/')), false)
  assert.equal(movesFocus(start, at('/projects/team-a/shop')), false)
})

test('movesFocus keeps focus on a new query or hash on the same path', () => {
  const milestone = '/projects/team-a/shop/milestones/14'
  // vue-router's path holds neither the query (?tab=) nor the hash.
  assert.equal(movesFocus(at(milestone), at(milestone)), false)
})

test('movesFocus moves focus on a new path', () => {
  assert.equal(movesFocus(at('/'), at('/projects/team-a/shop')), true)
  assert.equal(movesFocus(at('/projects/team-a/shop'), at('/projects/team-a/shop/milestones/14')), true)
  assert.equal(movesFocus(at('/inbox'), at('/')), true)
})
