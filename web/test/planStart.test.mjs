// Tests of src/planStart.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { dormantDescription, planNewTo, planStartGroups, planStartMenuItems, planStartProjects } from '../src/planStart.ts'

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

const ap = (namespace, name, path, extra = {}) => ({ namespace, name, path, steer: true, ...extra })
const dormant = (set, name, path, extra = {}) => ({ namespace: 'team-a', name, path, set, dormant: true, steer: true, ...extra })
const values = (groups) => groups.map((g) => [g.label, ...g.items.map((i) => i.value)])

test('planStartGroups: none without steer projects', () => {
  assert.deepEqual(planStartGroups([]), [])
  assert.deepEqual(planStartGroups([ap('ns', 'app', 'g/app', { steer: false }), dormant('s', 'foo', 'g/foo', { steer: false })]), [])
})

test('planStartGroups: Projects first by namespace and name, then sets by name, entries by path', () => {
  const groups = planStartGroups([
    dormant('zeta', 'z-b', 'g/b'),
    ap('team-b', 'app', 'g/app'),
    dormant('alpha', 'tools-foo', 'g/tools/foo'),
    ap('team-a', 'shop', 'g/shop', { set: 'alpha' }),
    dormant('alpha', 'docs', 'g/docs'),
    dormant('zeta', 'z-a', 'g/a'),
  ])
  assert.deepEqual(values(groups), [
    ['Projects', 'team-a/shop', 'team-b/app'],
    ['alpha', 'team-a/docs', 'team-a/tools-foo'],
    ['zeta', 'team-a/z-a', 'team-a/z-b'],
  ])
})

test('planStartGroups: steer only, an empty group left out', () => {
  const groups = planStartGroups([ap('ns', 'app', 'g/app', { steer: false }), dormant('s', 'foo', 'g/foo'), dormant('t', 'bar', 'g/bar', { steer: false })])
  assert.deepEqual(values(groups), [['s', 'team-a/foo']])
})

test('planStartGroups: a dormant entry with an AgentProject shows once, as the project', () => {
  const groups = planStartGroups([ap('team-a', 'foo', 'g/foo', { set: 's' }), dormant('s', 'foo', 'g/foo'), dormant('s', 'bar', 'g/bar')])
  assert.deepEqual(values(groups), [['Projects', 'team-a/foo'], ['s', 'team-a/bar']])
})

test('planStartGroups: labels and descriptions', () => {
  const [projects, set] = planStartGroups([ap('team-a', 'shop', 'team-a/shop'), dormant('s', 'tools-foo', 'jo.wiemers/tools/foo')])
  assert.deepEqual(projects.items, [{ value: 'team-a/shop', label: 'team-a/shop', description: 'team-a/shop', namespace: 'team-a', name: 'shop' }])
  assert.deepEqual(set.items, [
    { value: 'team-a/tools-foo', label: 'jo.wiemers/tools/foo', description: dormantDescription, namespace: 'team-a', name: 'tools-foo' },
  ])
  assert.equal(dormantDescription, 'dormant, starts with the plan')
})

test('planStartMenuItems: one array per group, led by its label', () => {
  const groups = planStartGroups([ap('team-a', 'shop', 'team-a/shop'), dormant('s', 'foo', 'g/foo')])
  const items = planStartMenuItems(groups)
  assert.deepEqual(items.map((g) => g[0]), [{ type: 'label', label: 'Projects' }, { type: 'label', label: 's' }])
  assert.deepEqual(items.map((g) => g.slice(1)), groups.map((g) => g.items))
})

test('planNewTo: the New plan page, with ?project= on a project', () => {
  assert.deepEqual(planNewTo(), { name: 'plan-new' })
  assert.deepEqual(planNewTo({ namespace: 'agents', name: 'shop' }), { name: 'plan-new', query: { project: 'agents/shop' } })
})
