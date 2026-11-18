// Tests of src/decisions.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  decisionFromHash,
  decisionsQuery,
  decisionsSearch,
  filtered,
  groupByIssue,
  issueStateLabel,
  kindLabel,
  optionsOf,
  searchFromQuery,
  stateFromQuery,
} from '../src/decisions.ts'

function decision(id, milestoneIID, iid, extra = {}) {
  return {
    id,
    kind: 'implementer',
    title: id,
    question: '',
    choice: 'a',
    why: '',
    alternatives: [],
    postedAt: '2026-11-15T10:00:00Z',
    milestoneIID,
    issue: { iid, title: `Issue ${iid}`, state: 'closed' },
    superseded: false,
    revisits: [],
    revisit: { mode: '', text: '' },
    ...extra,
  }
}

test('groupByIssue: groups in the order the server returns them, decisions keep theirs', () => {
  const list = [
    decision('D42-P1', 28, 42),
    decision('D42-1', 28, 42),
    decision('D40-1', 28, 40),
    decision('D42-2', 28, 42),
  ]
  const groups = groupByIssue(list)
  assert.deepEqual(
    groups.map((g) => [g.key, g.issue.iid, g.milestoneIID, g.decisions.map((d) => d.id)]),
    [
      ['28/42', 42, 28, ['D42-P1', 'D42-1', 'D42-2']],
      ['28/40', 40, 28, ['D40-1']],
    ],
  )
  assert.equal(groups[0].issue.title, 'Issue 42')
})

test('groupByIssue: the same IID in two milestones is two groups; empty is empty', () => {
  const groups = groupByIssue([decision('D7-1', 28, 7), decision('D7-2', 27, 7)])
  assert.deepEqual(groups.map((g) => g.key), ['28/7', '27/7'])
  assert.deepEqual(groupByIssue([]), [])
})

test('optionsOf: the choice first and chosen, then the alternatives', () => {
  assert.deepEqual(optionsOf({ choice: 'Postgres', alternatives: ['SQLite', 'MySQL'] }), [
    { text: 'Postgres', chosen: true },
    { text: 'SQLite', chosen: false },
    { text: 'MySQL', chosen: false },
  ])
})

test('optionsOf: no alternatives, blank ones and a blank choice are left out', () => {
  assert.deepEqual(optionsOf({ choice: 'Only', alternatives: [] }), [{ text: 'Only', chosen: true }])
  assert.deepEqual(optionsOf({ choice: 'Only', alternatives: null }), [{ text: 'Only', chosen: true }])
  assert.deepEqual(optionsOf({ choice: ' ', alternatives: ['', 'B'] }), [{ text: 'B', chosen: false }])
})

test('stateFromQuery: active and superseded, anything else is all', () => {
  assert.equal(stateFromQuery('active'), 'active')
  assert.equal(stateFromQuery('superseded'), 'superseded')
  assert.equal(stateFromQuery(['superseded', 'active']), 'superseded')
  assert.equal(stateFromQuery(undefined), 'all')
  assert.equal(stateFromQuery(null), 'all')
  assert.equal(stateFromQuery(''), 'all')
  assert.equal(stateFromQuery('Active'), 'all')
  assert.equal(stateFromQuery('all'), 'all')
})

test('searchFromQuery: trimmed, the first of repeated values', () => {
  assert.equal(searchFromQuery('  currency '), 'currency')
  assert.equal(searchFromQuery(['D42-3', 'x']), 'D42-3')
  assert.equal(searchFromQuery(undefined), '')
  assert.equal(searchFromQuery([null]), '')
})

test('decisionsQuery: sets the search and state, keeps the tab, drops empty and All', () => {
  assert.deepEqual(decisionsQuery({ tab: 'decisions' }, { q: ' currency ' }), { tab: 'decisions', q: 'currency' })
  assert.deepEqual(decisionsQuery({ tab: 'decisions', q: 'currency' }, { state: 'active' }), {
    tab: 'decisions',
    q: 'currency',
    state: 'active',
  })
  assert.deepEqual(decisionsQuery({ tab: 'decisions', q: 'x', state: 'active' }, { q: '' }), { tab: 'decisions', state: 'active' })
  assert.deepEqual(decisionsQuery({ tab: 'decisions', state: 'superseded' }, { state: 'all' }), { tab: 'decisions' })
  assert.deepEqual(decisionsQuery({ tab: 'decisions', state: 'bogus' }, {}), { tab: 'decisions' })
})

test('decisionsSearch: the API query, empty without filters', () => {
  assert.equal(decisionsSearch({ milestone: 28, q: '', state: 'all' }), '?milestone=28')
  assert.equal(decisionsSearch({ milestone: 0, q: '', state: 'all' }), '')
  assert.equal(decisionsSearch({ milestone: 28, q: 'a b&c', state: 'superseded' }), '?milestone=28&q=a+b%26c&state=superseded')
  assert.equal(decisionsSearch({ milestone: 0, q: 'D42-3', state: 'active' }), '?q=D42-3&state=active')
})

test('filtered: a search or a chip other than All', () => {
  assert.equal(filtered({ q: '', state: 'all' }), false)
  assert.equal(filtered({ q: 'x', state: 'all' }), true)
  assert.equal(filtered({ q: '', state: 'active' }), true)
})

test('kindLabel and issueStateLabel', () => {
  assert.equal(kindLabel('planner'), 'Plan')
  assert.equal(kindLabel('implementer'), 'Implementation')
  assert.equal(issueStateLabel('opened'), 'open')
  assert.equal(issueStateLabel('closed'), 'closed')
})

test('decisionFromHash: the decoded ID, "" for none', () => {
  assert.equal(decisionFromHash('#D42-3'), 'D42-3')
  assert.equal(decisionFromHash('#D42-P1'), 'D42-P1')
  assert.equal(decisionFromHash('#a%20b'), 'a b')
  assert.equal(decisionFromHash(''), '')
  assert.equal(decisionFromHash('#'), '')
  assert.equal(decisionFromHash('#%E0%A4%A'), '')
})
