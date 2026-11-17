// Tests of src/planStart.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { planStartProjects } from '../src/planStart.ts'

const p = (namespace, name, steer = true) => ({ namespace, name, steer })

test('planStartProjects: none without projects or steer', () => {
  assert.deepEqual(planStartProjects([]), [])
  assert.deepEqual(planStartProjects([p('ns', 'app', false)]), [])
})

test('planStartProjects: one steer project, a steer-false one left out', () => {
  assert.deepEqual(planStartProjects([p('ns', 'web', false), p('ns', 'app')]), [p('ns', 'app')])
})

test('planStartProjects: two sorted by namespace and name, the input kept', () => {
  const input = [p('team-b', 'app'), p('team-a', 'web'), p('team-a', 'shop'), p('team-a', 'api', false)]
  assert.deepEqual(planStartProjects(input), [p('team-a', 'shop'), p('team-a', 'web'), p('team-b', 'app')])
  assert.equal(input[0].namespace, 'team-b')
})
