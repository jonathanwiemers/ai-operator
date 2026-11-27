// Tests of src/looseIssues.ts and the Loose issues tab: npm test.
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { test } from 'node:test'
import { commandLabel } from '../src/chatState.ts'
import { answerToast, filterIssues, itemActions, itemReason, itemState, ownersNote, queueNeeds, selectionOrder, takeCommand } from '../src/looseIssues.ts'
import { projectTabFromRoute, projectTabsFor } from '../src/streamTabs.ts'

test('projectTabsFor: Loose issues after the Cockpit only with the flag', () => {
  assert.deepEqual(projectTabsFor(false).map((t) => t.id), ['milestones', 'cockpit', 'decisions', 'metrics'])
  assert.deepEqual(projectTabsFor(false, true).map((t) => t.id), ['milestones', 'cockpit', 'loose', 'decisions', 'metrics'])
  assert.deepEqual(projectTabsFor(true, true).map((t) => t.label), ['Milestones', 'Cockpit', 'Loose issues', 'Decisions', 'Metrics', 'Plans'])
  assert.equal(projectTabFromRoute('loose', projectTabsFor(false, true)), 'loose')
  assert.equal(projectTabFromRoute('loose', projectTabsFor(true, false)), 'milestones')
})

const items = [
  { iid: 50, title: 'a', webURL: '', state: 'Merged', mergedAt: '2026-11-10T11:00:00Z' },
  { iid: 51, title: 'b', webURL: '', state: 'Active', headSHA: 'abc123' },
  { iid: 52, title: 'c', webURL: '', state: 'Active', holdReason: 'CI stays red', headSHA: 'def456' },
  { iid: 53, title: 'd', webURL: '', state: 'Pending' },
  { iid: 54, title: 'e', webURL: '', state: 'Pending', holdReason: 'Budget spent' },
  { iid: 55, title: 'f', webURL: '', state: 'Pending' },
  { iid: 56, title: 'g', webURL: '', state: 'Left', leftReason: 'dropped by @ada' },
  { iid: 57, title: 'h', webURL: '', state: 'Active' },
]

test('itemState: pending with its place, held over its state', () => {
  assert.deepEqual(items.map((it) => itemState(it, items).label), ['Merged', 'Active', 'Held', 'Pending · 1', 'Held', 'Pending · 2', 'Left', 'Active'])
  assert.equal(itemState(items[2], items).tone, 'warning')
})

test('itemReason: the hold or the leave', () => {
  assert.deepEqual(items.map(itemReason), ['', '', 'CI stays red', '', 'Budget spent', '', 'dropped by @ada', ''])
})

test('itemActions: resume held, drop unfinished, merge an active pushed head', () => {
  const ids = (i) => itemActions(items[i]).map((a) => a.id)
  assert.deepEqual(ids(0), [])
  assert.deepEqual(ids(1), ['drop', 'merge'])
  assert.deepEqual(ids(2), ['resume', 'drop', 'merge'])
  assert.deepEqual(ids(3), ['drop'])
  assert.deepEqual(ids(4), ['resume', 'drop'])
  assert.deepEqual(ids(6), [])
  assert.deepEqual(ids(7), ['drop'])
  assert.deepEqual(itemActions(items[2]).map((a) => a.cmd), [
    { cmd: 'resume', issue: 52 },
    { cmd: 'drop', issue: 52 },
    { cmd: 'merge', issue: 52, sha: 'def456' },
  ])
  assert.deepEqual(itemActions(items[3]).map((a) => commandLabel(JSON.stringify(a.cmd))), ['drop #53'])
})

const issues = [
  { iid: 421, title: 'Runner timeout on e2e', labels: ['ci::flaky'], author: 'ada', webURL: '', inQueue: false },
  { iid: 350, title: 'Flaky helm-lint', labels: ['ci::flaky', 'agent::ready'], author: 'operator-bot', webURL: '', inQueue: false },
  { iid: 128, title: 'Docs typo', labels: [], author: 'grace', webURL: '', inQueue: true },
  { iid: 46, title: 'Retry webhook', labels: ['bug'], author: 'linus', webURL: '', inQueue: false },
]

test('takeCommand: the IIDs in selection order', () => {
  assert.equal(takeCommand([]), null)
  const cmd = takeCommand([350, 421])
  assert.deepEqual(cmd, { cmd: 'take', issues: [350, 421] })
  assert.equal(commandLabel(JSON.stringify(cmd)), 'take #350 #421')
})

test('selectionOrder: keeps the order ticked, appends new ones in row order, skips queued', () => {
  let order = selectionOrder([], { 350: true }, issues)
  assert.deepEqual(order, [350])
  order = selectionOrder(order, { 350: true, 421: true }, issues)
  assert.deepEqual(order, [350, 421])
  assert.deepEqual(takeCommand(order), { cmd: 'take', issues: [350, 421] })
  // Unticking keeps the rest in order; a select-all adds top down.
  assert.deepEqual(selectionOrder([350, 421], { 421: true }, issues), [421])
  assert.deepEqual(selectionOrder([46], { 46: true, 421: true, 350: true, 128: true }, issues), [46, 421, 350])
  assert.deepEqual(selectionOrder([350], { 350: false }, issues), [])
})

test('filterIssues: by IID, title, label or author, every word', () => {
  const iids = (q) => filterIssues(issues, q).map((i) => i.iid)
  assert.deepEqual(iids(''), [421, 350, 128, 46])
  assert.deepEqual(iids('  '), [421, 350, 128, 46])
  assert.deepEqual(iids('#350'), [350])
  assert.deepEqual(iids('46'), [46])
  assert.deepEqual(iids('FLAKY'), [421, 350])
  assert.deepEqual(iids('agent::ready'), [350])
  assert.deepEqual(iids('grace'), [128])
  assert.deepEqual(iids('flaky ada'), [421])
  assert.deepEqual(iids('nothing'), [])
})

test('ownersNote: names the owner subjects, or nobody', () => {
  assert.match(ownersNote(['ada@example.com', 'grace']), /ada@example\.com, grace\.$/)
  assert.match(ownersNote([]), /Who may act: nobody: the queue has no ownerSubjects\.$/)
})

test('answerToast: the answer as it is, refusals as a warning', () => {
  assert.equal(answerToast({ state: 'pending' }), null)
  assert.deepEqual(answerToast({ state: 'failed', reason: 'chat is off' }), { title: 'The command failed', description: 'chat is off', color: 'error' })
  const some = answerToast({ state: 'answered', text: 'Taken into the loose issues queue: #350. Not taken: #564 is in milestone "M".', redacted: false })
  assert.equal(some.color, 'warning')
  assert.match(some.description, /#564/)
  assert.equal(answerToast({ state: 'answered', text: '#46 left the queue: dropped by @ada.', redacted: false }).color, 'info')
  assert.equal(answerToast({ state: 'answered', text: '', redacted: true }).description, undefined)
})

// roundLines is roundLine of each round at now, in a fresh node in
// Europe/Berlin and en_US: ICU takes the locale from the environment.
function roundLines(rounds, now) {
  const mod = new URL('../src/looseIssues.ts', import.meta.url).href
  const code = `import { roundLine } from ${JSON.stringify(mod)}; console.log(JSON.stringify(${JSON.stringify(rounds)}.map((r) => roundLine(r ?? undefined, ${now}))))`
  const out = execFileSync(process.execPath, ['--input-type=module', '-e', code], {
    env: { ...process.env, TZ: 'Europe/Berlin', LANG: 'en_US.UTF-8', LC_ALL: 'en_US.UTF-8' },
    encoding: 'utf8',
  })
  return JSON.parse(out)
}

test('roundLine: local start time, the date when not today, the issue limit and the spend; nothing without a round', () => {
  const round = { number: 3, startedAt: '2026-11-26T08:12:00Z', finished: 2, roundIssues: 5, roundHours: 24, costUSD: '1.40' }
  const lines = roundLines(
    [
      round,
      { ...round, startedAt: '2026-11-25T22:30:00Z', finished: 1 },
      { ...round, startedAt: '2025-12-31T08:12:00Z' },
      { ...round, roundIssues: 0, finished: 1 },
      { ...round, roundIssues: 0, finished: 0, costUSD: '0.00' },
      null,
    ],
    Date.parse('2026-11-26T14:00:00Z'),
  )
  assert.deepEqual(lines, [
    'Round 3 · started 09:12 AM · 2 of 5 issues · $1.40',
    // 23:30 in Berlin is the day before.
    'Round 3 · started Nov 25, 11:30 PM · 1 of 5 issues · $1.40',
    'Round 3 · started Dec 31, 2025, 09:12 AM · 2 of 5 issues · $1.40',
    'Round 3 · started 09:12 AM · 1 issue, no issue limit · $1.40',
    'Round 3 · started 09:12 AM · 0 issues, no issue limit · $0',
    '',
  ])
})

test("queueNeeds: one card per issue with facts or an open prompt, the queue's own first, then the proposals", () => {
  const facts = (iid, over = {}) => ({ iid, issue: { iid, title: `Facts ${iid}`, url: `https://gl/${iid}` }, ...over })
  const prompt = (id, over = {}) => ({ id, state: 'open', kind: 'decision', canAnswer: true, target: { task: 'shop-queue-pilot' }, ...over })
  const view = {
    items: [
      { iid: 421, title: 'Flaky helm-lint', webURL: 'https://gl/421', state: 'Active', holdReason: 'CI stays red' },
      { iid: 430, title: 'Docs', webURL: 'https://gl/430', state: 'Active' },
      { iid: 50, title: 'Gone', webURL: 'https://gl/50', state: 'Merged', holdReason: 'old hold' },
    ],
    problems: [facts(430, { run: { task: 't', number: 2, cause: 'tests time out' } }), facts(421)],
    prompts: [prompt('Q2', { issueIID: 421 }), prompt('Q1'), prompt('Q0', { issueIID: 50, state: 'answered' }), prompt('Q3', { issueIID: 50 })],
    proposals: [{ id: 3, summary: 'Retry #421' }],
  }
  const n = queueNeeds(view)
  assert.deepEqual(
    n.problems.map((p) => [p.iid, p.title, p.cause, p.prompts.map((q) => q.id)]),
    [
      [0, 'The queue', '', ['Q1']],
      [50, 'Gone', '', ['Q3']],
      [421, 'Facts 421', 'CI stays red', ['Q2']],
      [430, 'Facts 430', 'tests time out', []],
    ],
  )
  assert.equal(n.problems[2].url, 'https://gl/421')
  assert.equal(n.problems[3].facts, view.problems[0])
  assert.deepEqual(n.proposals, view.proposals)
  assert.equal(n.count, 5)
  assert.deepEqual(queueNeeds({ items: [], problems: [], prompts: [], proposals: [] }), { problems: [], proposals: [], count: 0 })
})
