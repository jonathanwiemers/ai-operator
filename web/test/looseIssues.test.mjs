// Tests of src/looseIssues.ts and the Loose issues tab: npm test.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { commandLabel } from '../src/chatState.ts'
import { answerToast, filterIssues, itemActions, itemReason, itemState, ownersNote, selectionOrder, takeCommand } from '../src/looseIssues.ts'
import { projectTabFromRoute, projectTabsFor } from '../src/streamTabs.ts'

test('projectTabsFor: Loose issues right after Milestones only with the flag', () => {
  assert.deepEqual(projectTabsFor(false).map((t) => t.id), ['cockpit', 'milestones', 'decisions', 'metrics'])
  assert.deepEqual(projectTabsFor(false, true).map((t) => t.id), ['cockpit', 'milestones', 'loose', 'decisions', 'metrics'])
  assert.deepEqual(projectTabsFor(true, true).map((t) => t.label), ['Cockpit', 'Milestones', 'Loose issues', 'Decisions', 'Metrics', 'Plans'])
  assert.equal(projectTabFromRoute('loose', projectTabsFor(false, true)), 'loose')
  assert.equal(projectTabFromRoute('loose', projectTabsFor(true, false)), 'cockpit')
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
