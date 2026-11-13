// Tests of src/searchItems.ts: npm test.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { paletteGroups, searchQuery } from '../src/searchItems.ts'

const data = {
  projects: [{ namespace: 'ns', name: 'app' }],
  milestones: [{ namespace: 'ns', project: 'app', iid: 19, title: 'Refresh' }],
  results: [
    { namespace: 'ns', project: 'app', task: 't-b', title: 'Second', issueIID: 12, mergeRequestIID: 34 },
    { namespace: 'ns', project: 'app', task: 't-a', title: 'First', issueIID: 5 },
  ],
  features: [],
}
const by = (g, id) => g.find((x) => x.id === id)

test('group order and per-kind labels and routes', () => {
  const g = paletteGroups(data)
  assert.deepEqual(g.map((x) => x.label), ['Projects', 'Milestones', 'Issues & MRs', 'Pages'])
  assert.deepEqual(by(g, 'projects').items[0].to, { name: 'stream-project', params: { namespace: 'ns', name: 'app' } })
  const m = by(g, 'milestones').items[0]
  assert.equal(m.label, '%19 Refresh')
  assert.equal(m.suffix, 'app')
  assert.deepEqual(m.to, { name: 'stream-milestone', params: { namespace: 'ns', name: 'app', iid: 19 } })
  for (const x of g.flatMap((y) => y.items)) assert.ok(x.icon && x.to)
})

test('issue results keep server order and are not re-filtered', () => {
  const i = by(paletteGroups(data), 'issues')
  assert.equal(i.ignoreFilter, true)
  assert.deepEqual(i.items.map((x) => x.label), ['#12 Second', '#5 First'])
  assert.equal(i.items[0].suffix, '!34 app')
  assert.equal(i.items[1].suffix, 'app')
  assert.deepEqual(i.items[0].to, { name: 'task', params: { namespace: 'ns', name: 'app', task: 't-b' } })
})

test('pages are gated by features', () => {
  const pages = (features) => by(paletteGroups({ ...data, features }), 'pages').items.map((x) => x.label)
  assert.deepEqual(pages([]), ['Inbox', 'Overview'])
  assert.deepEqual(pages([{ name: 'history' }, { name: 'research' }]), ['Inbox', 'Overview', 'Costs', 'Research'])
})

test('searchQuery needs two characters after trimming', () => {
  assert.equal(searchQuery(' a '), null)
  assert.equal(searchQuery(' ab '), 'ab')
  assert.equal(searchQuery(''), null)
})
