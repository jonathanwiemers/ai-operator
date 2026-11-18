// Tests of src/projectSets.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { groupProjects, setCountsLine, setProblem } from '../src/projectSets.ts'

const project = (name, over = {}) => ({ namespace: 'agents', name, ...over })
const set = (name, over = {}) => ({ namespace: 'agents', name, counts: {}, ...over })
const names = (ps) => ps.map((p) => p.name)

test('groupProjects: children go under their set, explicit ones stay ungrouped', () => {
  const g = groupProjects(
    [project('ai-operator'), project('b', { set: 'jo' }), project('infra'), project('a', { set: 'jo' }), project('c', { set: 'jo' })],
    [set('jo')],
  )
  assert.deepEqual(names(g.ungrouped), ['ai-operator', 'infra'])
  assert.equal(g.sets.length, 1)
  assert.equal(g.sets[0].set.name, 'jo')
  assert.deepEqual(names(g.sets[0].children), ['a', 'b', 'c'])
})

test('groupProjects: a child of an unknown set goes to ungrouped', () => {
  const g = groupProjects([project('a', { set: 'gone' }), project('b', { set: 'jo', namespace: 'other' })], [set('jo')])
  assert.deepEqual(names(g.ungrouped), ['a', 'b'])
  assert.deepEqual(names(g.sets[0].children), [])
})

test('groupProjects: a set without children is kept', () => {
  const g = groupProjects([project('x')], [set('empty')])
  assert.equal(g.sets.length, 1)
  assert.deepEqual(g.sets[0].children, [])
  assert.deepEqual(names(g.ungrouped), ['x'])
})

test('groupProjects: sets sorted by name, input order of ungrouped kept', () => {
  const g = groupProjects(
    [project('z'), project('a'), project('y', { set: 'beta' }), project('x', { set: 'alpha' })],
    [set('beta'), set('alpha')],
  )
  assert.deepEqual(g.sets.map((s) => s.set.name), ['alpha', 'beta'])
  assert.deepEqual(names(g.ungrouped), ['z', 'a'])
  assert.deepEqual(names(g.sets[0].children), ['x'])
  assert.deepEqual(names(g.sets[1].children), ['y'])
})

test('groupProjects: no sets leaves the list as it is', () => {
  const ps = [project('b'), project('a', { set: 'jo' })]
  const g = groupProjects(ps, [])
  assert.deepEqual(g.sets, [])
  assert.deepEqual(g.ungrouped, ps)
})

test('setCountsLine: zero counts hidden but active', () => {
  assert.equal(setCountsLine({ active: 3, dormant: 12, excluded: 2, explicit: 14 }), '3 active · 12 dormant · 2 excluded · 14 explicit')
  assert.equal(setCountsLine({ dormant: 4 }), '0 active · 4 dormant')
  assert.equal(setCountsLine({}), '0 active')
  assert.equal(setCountsLine(undefined), '0 active')
})

test('setProblem: Ready or Discovered False gives the reason', () => {
  assert.equal(setProblem({}), null)
  assert.equal(setProblem({ conditions: [{ type: 'Ready', status: 'True', reason: 'Ok' }] }), null)
  assert.equal(setProblem({ conditions: [{ type: 'Ready', status: 'False', reason: 'WebhookRequired', message: '' }] }), 'WebhookRequired')
  assert.equal(
    setProblem({ conditions: [{ type: 'Discovered', status: 'False', reason: 'GitLabError', message: '503' }] }),
    'GitLabError: 503',
  )
})
