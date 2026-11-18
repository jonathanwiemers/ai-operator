// Tests of src/decisions.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  chainOf,
  commentBody,
  decisionFromHash,
  decisionsQuery,
  decisionsSearch,
  filtered,
  followUpLabel,
  groupByIssue,
  issueStateLabel,
  kindLabel,
  milestoneFromQuery,
  milestoneLabel,
  optionsOf,
  revisitAction,
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

test('decisionsQuery: the project tab milestone, kept unless patched; 0 drops it', () => {
  assert.deepEqual(decisionsQuery({ tab: 'decisions' }, { milestone: 14 }), { tab: 'decisions', milestone: '14' })
  assert.deepEqual(decisionsQuery({ tab: 'decisions', milestone: '14' }, { q: 'x' }), { tab: 'decisions', milestone: '14', q: 'x' })
  assert.deepEqual(decisionsQuery({ tab: 'decisions', milestone: '14', q: 'x' }, { milestone: 0 }), { tab: 'decisions', q: 'x' })
})

test('milestoneFromQuery: a positive iid, else 0 (all)', () => {
  assert.equal(milestoneFromQuery('14'), 14)
  assert.equal(milestoneFromQuery(['15', '14']), 15)
  assert.equal(milestoneFromQuery(undefined), 0)
  assert.equal(milestoneFromQuery(''), 0)
  assert.equal(milestoneFromQuery('all'), 0)
  assert.equal(milestoneFromQuery('-3'), 0)
  assert.equal(milestoneFromQuery('1.5'), 0)
})

test('milestoneLabel: the title, else %iid', () => {
  const ms = [{ iid: 14, title: 'Payments' }, { iid: 15, title: '' }]
  assert.equal(milestoneLabel(14, ms), 'Payments')
  assert.equal(milestoneLabel(15, ms), '%15')
  assert.equal(milestoneLabel(16), '%16')
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

function request(at, extra = {}) {
  return { source: 'comment', requestedBy: 'jo', instruction: 'Use X', at, applied: false, ...extra }
}

const ids = (steps) => steps.map((s) => [s.decision, s.request.at, s.successor ?? '', s.successorTitle ?? ''])

test('chainOf: one applied request and the decision that replaced it', () => {
  const old = decision('D42-3', 28, 42, {
    superseded: true,
    supersededBy: 'D57-1',
    revisits: [request('2026-11-15T10:00:00Z', { source: 'followUp', followUpIID: 57, followUpState: 'closed', applied: true })],
  })
  const next = decision('D57-1', 28, 57, { title: 'Order currency', supersedes: 'D42-3' })
  const byID = new Map([old, next].map((d) => [d.id, d]))
  assert.deepEqual(ids(chainOf(old, byID)), [['D42-3', '2026-11-15T10:00:00Z', 'D57-1', 'Order currency']])
  assert.equal(chainOf(old, byID)[0].request.followUpIID, 57)
  // The successor is linked without its title when it is not loaded.
  assert.deepEqual(ids(chainOf(old, new Map())), [['D42-3', '2026-11-15T10:00:00Z', 'D57-1', '']])
})

test('chainOf: two steps through the successor, requests oldest first', () => {
  const a = decision('D42-3', 28, 42, {
    superseded: true,
    supersededBy: 'D57-1',
    revisits: [
      request('2026-11-15T12:00:00Z', { source: 'followUp', followUpIID: 57, applied: true }),
      request('2026-11-15T11:00:00Z'),
    ],
  })
  const b = decision('D57-1', 28, 57, {
    superseded: true,
    supersededBy: 'D60-1',
    revisits: [request('2026-11-16T09:00:00Z', { source: 'followUp', followUpIID: 60, applied: true })],
  })
  const c = decision('D60-1', 28, 60, { title: 'Ask per refund' })
  const byID = new Map([a, b, c].map((d) => [d.id, d]))
  assert.deepEqual(ids(chainOf(a, byID)), [
    ['D42-3', '2026-11-15T11:00:00Z', '', ''],
    ['D42-3', '2026-11-15T12:00:00Z', 'D57-1', 'D57-1'],
    ['D57-1', '2026-11-16T09:00:00Z', 'D60-1', 'Ask per refund'],
  ])
  // The successor's own card starts at its own requests.
  assert.deepEqual(ids(chainOf(b, byID)), [['D57-1', '2026-11-16T09:00:00Z', 'D60-1', 'Ask per refund']])
  // A missing successor stops the walk after its step.
  const partial = new Map([a].map((d) => [d.id, d]))
  assert.equal(chainOf(a, partial).length, 2)
})

test('chainOf: an unapplied request has no successor; no requests, no chain', () => {
  const open = decision('D42-3', 28, 42, { revisits: [request('2026-11-15T10:00:00Z', { source: 'followUp', followUpIID: 57, followUpState: 'opened' })] })
  assert.deepEqual(ids(chainOf(open, new Map([[open.id, open]]))), [['D42-3', '2026-11-15T10:00:00Z', '', '']])
  assert.deepEqual(chainOf(decision('D1-1', 28, 1, { superseded: true, supersededBy: 'D2-1' }), new Map()), [])
  assert.deepEqual(chainOf(decision('D1-1', 28, 1, { revisits: null }), new Map()), [])
})

test('chainOf: a cycle stops', () => {
  const a = decision('D1-1', 28, 1, { supersededBy: 'D2-1', revisits: [request('2026-11-15T10:00:00Z', { applied: true })] })
  const b = decision('D2-1', 28, 2, { supersededBy: 'D1-1', revisits: [request('2026-11-15T11:00:00Z', { applied: true })] })
  const steps = chainOf(a, new Map([a, b].map((d) => [d.id, d])))
  assert.deepEqual(ids(steps), [
    ['D1-1', '2026-11-15T10:00:00Z', 'D2-1', 'D2-1'],
    ['D2-1', '2026-11-15T11:00:00Z', 'D1-1', 'D1-1'],
  ])
  const self = decision('D3-1', 28, 3, { supersededBy: 'D3-1', revisits: [request('2026-11-15T10:00:00Z', { applied: true })] })
  assert.equal(chainOf(self, new Map([[self.id, self]])).length, 1)
})

test('commentBody: the command and the instruction, "" without either', () => {
  assert.equal(commentBody('/agent revisit D42-3 ', ' Use the order currency '), '/agent revisit D42-3 Use the order currency')
  assert.equal(commentBody('/bot revisit D42-3', 'x'), '/bot revisit D42-3 x')
  assert.equal(commentBody('/agent revisit D42-3 ', '   '), '')
  assert.equal(commentBody(undefined, 'x'), '')
  assert.equal(commentBody(' ', 'x'), '')
})

test('revisitAction: one action per mode, none without what it needs', () => {
  const r = (revisit) => revisitAction({ revisit: { text: 'line', ...revisit } })
  assert.equal(r({ mode: 'prompt', task: 'shop-m14-pilot' }), 'button')
  assert.equal(r({ mode: 'comment', task: 'shop-281-implement', command: '/agent revisit D281-2 ' }), 'comment')
  assert.equal(r({ mode: 'command', command: '/agent revisit D281-2 ' }), 'copy')
  assert.equal(r({ mode: 'none' }), null)
  // A user who may not revisit: the text, no action.
  assert.equal(r({ mode: '', command: '/agent revisit D281-2 ' }), null)
  assert.equal(r({ mode: 'prompt' }), null)
  assert.equal(r({ mode: 'comment', task: 'shop-281-implement' }), null)
  assert.equal(r({ mode: 'command', command: ' ' }), null)
})

test('followUpLabel: the follow-up issue, "" for a comment', () => {
  assert.equal(followUpLabel({ source: 'followUp', followUpIID: 57 }), '#57')
  assert.equal(followUpLabel({ source: 'comment' }), '')
  assert.equal(followUpLabel({ source: 'followUp', followUpIID: 0 }), '')
})
