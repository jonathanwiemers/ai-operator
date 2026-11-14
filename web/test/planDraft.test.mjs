// Tests of src/planDraft.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  draftConflictFrom,
  DraftConflict,
  keepMine,
  lineDiff,
  onServerDraft,
  readDraftEdit,
  takeAgent,
  writeDraftEdit,
} from '../src/planDraft.ts'

const view = {
  name: 'shop-plan-new',
  title: 'Refunds',
  phase: 'Ready',
  createdBy: 'ada',
  createdAt: '2026-11-01T00:00:00Z',
  lastActivityAt: '2026-11-01T00:00:00Z',
  spendUSD: '0.00',
  tokens: 0,
  budgetUSD: '5',
  budgetLimitUSD: '20',
  permissions: { steer: true },
  draft: '# Refunds',
  draftRevision: 4,
}

test('draftConflictFrom: 409 with a plan view is DraftConflict', () => {
  const c = draftConflictFrom(409, view)
  assert.ok(c instanceof DraftConflict)
  assert.equal(c.view.name, 'shop-plan-new')
  assert.equal(c.view.draftRevision, 4)
  assert.equal(c.view.draft, '# Refunds')
})

test('draftConflictFrom: other statuses and error envelopes are not', () => {
  assert.equal(draftConflictFrom(200, view), null)
  assert.equal(draftConflictFrom(409, { error: 'the plan has created its milestone' }), null)
  assert.equal(draftConflictFrom(409, { name: 'x' }), null)
  assert.equal(draftConflictFrom(409, {}), null)
})

const d = (kind, text) => ({ kind, text })
// sides rebuilds both texts from a diff.
const sides = (diff) => [
  diff.filter((l) => l.kind !== 'add').map((l) => l.text).join('\n'),
  diff.filter((l) => l.kind !== 'del').map((l) => l.text).join('\n'),
]

test('lineDiff: identical texts are all same', () => {
  assert.deepEqual(lineDiff('a\nb', 'a\nb'), [d('same', 'a'), d('same', 'b')])
  assert.deepEqual(lineDiff('', ''), [])
})

test('lineDiff: empty sides', () => {
  assert.deepEqual(lineDiff('', 'a\nb'), [d('add', 'a'), d('add', 'b')])
  assert.deepEqual(lineDiff('a\nb', ''), [d('del', 'a'), d('del', 'b')])
})

test('lineDiff: typical edits', () => {
  assert.deepEqual(lineDiff('a\nb\nc', 'a\nB\nc'), [d('same', 'a'), d('del', 'b'), d('add', 'B'), d('same', 'c')])
  assert.deepEqual(lineDiff('a\nc', 'a\nb\nc'), [d('same', 'a'), d('add', 'b'), d('same', 'c')])
  assert.deepEqual(lineDiff('a\nb\nc', 'a\nc'), [d('same', 'a'), d('del', 'b'), d('same', 'c')])
  const a = '# Goal\nx\n## R\n- R1 one\n- R2 two\n## End'
  const b = '# Goal\ny\n## R\n- R1 one\n- R1b new\n- R2 two\n## End\n'
  const diff = lineDiff(a, b)
  assert.deepEqual(sides(diff), [a, b])
  assert.equal(diff.filter((l) => l.kind === 'same').length, 5)
  assert.deepEqual(
    diff.filter((l) => l.kind !== 'same').map((l) => l.kind + ':' + l.text),
    ['del:x', 'add:y', 'add:- R1b new', 'add:'],
  )
})

test('lineDiff: above the size bound is one block replace', () => {
  const a = Array.from({ length: 2100 }, (_, i) => `a${i}`).join('\n')
  const b = Array.from({ length: 2100 }, (_, i) => `b${i}`).join('\n')
  const diff = lineDiff(`head\n${a}\ntail`, `head\n${b}\ntail`)
  assert.equal(diff.length, 2 + 4200)
  assert.deepEqual(diff[0], d('same', 'head'))
  assert.ok(diff.slice(1, 2101).every((l) => l.kind === 'del'))
  assert.ok(diff.slice(2101, 4201).every((l) => l.kind === 'add'))
  assert.deepEqual(diff[4201], d('same', 'tail'))
})

test('lineDiff: a 64 KiB draft with a few edits diffs line by line', () => {
  const a = Array.from({ length: 1500 }, (_, i) => `line ${i} ${'x'.repeat(30)}`)
  const b = a.map((l, i) => (i % 300 === 7 ? l + ' edited' : l))
  const diff = lineDiff(a.join('\n'), b.join('\n'))
  assert.deepEqual(sides(diff), [a.join('\n'), b.join('\n')])
  assert.equal(diff.filter((l) => l.kind === 'add').length, 5)
  assert.equal(diff.filter((l) => l.kind === 'del').length, 5)
})

const server = { draft: 'agent\ntext', draftRevision: 5 }

test('onServerDraft: clean editor or equal text is replaced', () => {
  assert.deepEqual(onServerDraft({ base: 3, text: 'old', dirty: false }, server), { action: 'replace' })
  assert.deepEqual(onServerDraft({ base: 3, text: 'agent\ntext', dirty: true }, server), { action: 'replace' })
})

test('onServerDraft: dirty and newer is a conflict with the diff mine → agent', () => {
  assert.deepEqual(onServerDraft({ base: 4, text: 'mine\ntext', dirty: true }, server), {
    action: 'conflict',
    diff: [d('del', 'mine'), d('add', 'agent'), d('same', 'text')],
  })
})

test('onServerDraft: dirty and not newer keeps the edit', () => {
  assert.deepEqual(onServerDraft({ base: 5, text: 'mine', dirty: true }, server), { action: 'keep' })
})

test('takeAgent and keepMine', () => {
  assert.deepEqual(takeAgent(server), { base: 5, text: 'agent\ntext', dirty: false })
  const mine = keepMine({ base: 4, text: 'mine', dirty: true }, server)
  assert.deepEqual(mine, { base: 5, text: 'mine', dirty: true })
  assert.deepEqual(onServerDraft(mine, server), { action: 'keep' })
})

test('readDraftEdit/writeDraftEdit: per plan, clean removes, bad data is null', () => {
  const store = new Map()
  globalThis.localStorage = {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => store.set(k, String(v)),
    removeItem: (k) => store.delete(k),
  }
  try {
    assert.equal(readDraftEdit('ns', 'p1'), null)
    writeDraftEdit('ns', 'p1', { base: 2, text: 'edit', dirty: true })
    assert.deepEqual(readDraftEdit('ns', 'p1'), { base: 2, text: 'edit', dirty: true })
    assert.equal(readDraftEdit('ns', 'p2'), null)
    assert.equal(readDraftEdit('other', 'p1'), null)
    writeDraftEdit('ns', 'p1', { base: 3, text: 'edit', dirty: false })
    assert.equal(readDraftEdit('ns', 'p1'), null)
    store.set('plan-draft:ns/p1', '{bad')
    assert.equal(readDraftEdit('ns', 'p1'), null)
    store.set('plan-draft:ns/p1', '{"base":"x","text":"t"}')
    assert.equal(readDraftEdit('ns', 'p1'), null)
  } finally {
    delete globalThis.localStorage
  }
  // No storage at all: nothing read, writes are dropped.
  assert.equal(readDraftEdit('ns', 'p1'), null)
  writeDraftEdit('ns', 'p1', { base: 1, text: 't', dirty: true })
})
