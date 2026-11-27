// Tests of src/projectCards.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { projectCards } from '../src/projectCards.ts'

const proj = (name, extra = {}) => ({ namespace: 'acme', name, suspend: false, ...extra })
const ms = (project, iid, state, extra = {}) => ({
  namespace: 'acme',
  project,
  iid,
  title: `M${iid}`,
  state,
  progress: { merged: 1, total: 4, open: 3 },
  needsYou: 0,
  ...extra,
})
const work = (project, issueIID, extra = {}) => ({ namespace: 'acme', project, issueIID, title: `I${issueIID}`, ...extra })
const inb = (project, kind = 'prompt') => ({ namespace: 'acme', project, kind })
const names = (list) => list.map((c) => c.name)

test('needsYou counts inbox items without plan items', () => {
  const { cards } = projectCards([proj('shop')], [], [], [inb('shop'), inb('shop', 'problem'), inb('shop', 'plan')])
  assert.equal(cards[0].needsYou, 2)
})

test('a paused queue head is under building, in iid order', () => {
  const { cards } = projectCards([proj('shop')], [ms('shop', 9, 'running'), ms('shop', 5, 'paused')], [], [])
  assert.deepEqual(cards[0].building.map((m) => [m.iid, m.state]), [[5, 'paused'], [9, 'running']])
  assert.deepEqual(cards[0].building[0].progress, { merged: 1, total: 4, open: 3 })
})

test('upNext is sorted by queue position: three and the rest', () => {
  const rows = [4, 2, 1, 3].map((p) => ms('shop', 10 + p, 'waiting', { queuePosition: p }))
  rows.push(ms('shop', 99, 'waiting', { queuePosition: 0 }), ms('shop', 98, 'waiting'))
  const { cards } = projectCards([proj('shop')], rows, [], [])
  assert.deepEqual(cards[0].upNext.map((m) => m.iid), [11, 12, 13])
  assert.deepEqual(cards[0].upNext.map((m) => m.queuePosition), [1, 2, 3])
  assert.equal(cards[0].moreQueued, 1)
})

test('running issues group under their milestone, loose last; unknown milestone is #iid', () => {
  const rows = [ms('shop', 3, 'running'), ms('shop', 2, 'paused')]
  const w = [
    work('shop', 1, { step: 'build', stepSince: 't' }),
    work('shop', 2, { milestoneIID: 3 }),
    work('shop', 4, { milestoneIID: 2 }),
    work('shop', 5, { milestoneIID: 7 }),
    { namespace: 'acme', project: 'shop', title: 'project entry' },
  ]
  const { cards } = projectCards([proj('shop')], rows, w, [])
  assert.deepEqual(
    cards[0].runningIssues.map((g) => [g.title, g.issues.map((i) => i.iid)]),
    [['M2', [4]], ['M3', [2]], ['#7', [5]], ['Loose issues', [1]]],
  )
  assert.deepEqual(cards[0].runningIssues[3].issues[0], { iid: 1, title: 'I1', step: 'build', stepSince: 't' })
})

test('three groups, each by name', () => {
  const projects = ['d', 'c', 'b', 'a', 'z', 'y'].map((n) => proj(n))
  const rows = [ms('c', 1, 'running'), ms('b', 1, 'running'), ms('z', 1, 'waiting', { queuePosition: 1 })]
  const { cards, idle } = projectCards(projects, rows, [work('a', 1)], [inb('y'), inb('d')])
  assert.deepEqual(names(cards), ['d', 'y', 'a', 'b', 'c', 'z'])
  assert.deepEqual(names(idle), [])
})

test('idle: finishing only, planned only and empty projects', () => {
  const projects = [proj('c', { settlingMilestones: 2 }), proj('b'), proj('a')]
  const { cards, idle } = projectCards(projects, [ms('a', 1, 'planned')], [], [inb('b', 'plan')])
  assert.deepEqual(cards, [])
  assert.deepEqual(names(idle), ['a', 'b', 'c'])
  assert.equal(idle[0].planned, 1)
  assert.equal(idle[2].finishing, 2)
  assert.equal(idle[1].needsYou, 0)
})

test('suspended is kept', () => {
  const { idle } = projectCards([proj('a', { suspend: true })], [], [], [])
  assert.equal(idle[0].suspended, true)
})

test('data of an unknown project is left out; namespaces are told apart', () => {
  const other = { namespace: 'other', project: 'shop', iid: 1, title: 'x', state: 'running', progress: { merged: 0, total: 0, open: 0 } }
  const { cards, idle } = projectCards(
    [proj('shop')],
    [ms('ghost', 1, 'running'), other],
    [work('ghost', 1), { ...work('shop', 2), namespace: 'other' }],
    [inb('ghost'), { namespace: 'other', project: 'shop', kind: 'prompt' }],
  )
  assert.deepEqual(cards, [])
  assert.deepEqual(names(idle), ['shop'])
  assert.equal(idle[0].needsYou, 0)
})
