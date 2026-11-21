// Tests of src/planDraft.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  canMove,
  draftConflictFrom,
  draftLabel,
  draftLocked,
  movedKeys,
  nextToCreate,
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
  draftsTotal: 1,
  draftsCreated: 0,
  createdMilestones: [],
  permissions: { steer: true },
  drafts: [{ key: 1, title: 'Refunds', draft: '# Refunds', revision: 4 }],
  decisions: [],
  decisionsPending: 0,
}

test('draftConflictFrom: 409 with a plan view is DraftConflict', () => {
  const c = draftConflictFrom(409, view)
  assert.ok(c instanceof DraftConflict)
  assert.equal(c.view.name, 'shop-plan-new')
  assert.equal(c.view.drafts[0].revision, 4)
  assert.equal(c.view.drafts[0].draft, '# Refunds')
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

const server = { key: 1, title: '', draft: 'agent\ntext', revision: 5 }

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

test('readDraftEdit/writeDraftEdit: per draft, clean removes, bad data is null', () => {
  const store = new Map()
  globalThis.localStorage = {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => store.set(k, String(v)),
    removeItem: (k) => store.delete(k),
  }
  try {
    assert.equal(readDraftEdit('ns', 'p1', 1), null)
    writeDraftEdit('ns', 'p1', 1, { base: 2, text: 'edit', dirty: true })
    writeDraftEdit('ns', 'p1', 2, { base: 7, text: 'two', dirty: true })
    assert.deepEqual(readDraftEdit('ns', 'p1', 1), { base: 2, text: 'edit', dirty: true })
    assert.deepEqual(readDraftEdit('ns', 'p1', 2), { base: 7, text: 'two', dirty: true })
    assert.equal(readDraftEdit('ns', 'p1', 3), null)
    assert.equal(readDraftEdit('ns', 'p2', 1), null)
    assert.equal(readDraftEdit('other', 'p1', 1), null)
    writeDraftEdit('ns', 'p1', 1, { base: 3, text: 'edit', dirty: false })
    assert.equal(readDraftEdit('ns', 'p1', 1), null)
    assert.deepEqual(readDraftEdit('ns', 'p1', 2), { base: 7, text: 'two', dirty: true })
    store.set('plan-draft:ns/p1/1', '{bad')
    assert.equal(readDraftEdit('ns', 'p1', 1), null)
    store.set('plan-draft:ns/p1/1', '{"base":"x","text":"t"}')
    assert.equal(readDraftEdit('ns', 'p1', 1), null)
  } finally {
    delete globalThis.localStorage
  }
  // No storage at all: nothing read, writes are dropped.
  assert.equal(readDraftEdit('ns', 'p1', 1), null)
  writeDraftEdit('ns', 'p1', 1, { base: 1, text: 't', dirty: true })
})

const dr = (key, createdMilestoneIID, creating) => ({ key, title: '', draft: '', revision: 1, createdMilestoneIID, creating })

test('nextToCreate: the first draft without a milestone', () => {
  assert.equal(nextToCreate([]), null)
  assert.equal(nextToCreate([dr(1), dr(2), dr(3)])?.key, 1)
  assert.equal(nextToCreate([dr(1, 41), dr(3), dr(2)])?.key, 3)
  assert.equal(nextToCreate([dr(1, 41), dr(2, 42), dr(3, 43)]), null)
})

test('nextToCreate: none while the first uncreated one is being created', () => {
  assert.equal(nextToCreate([dr(1, 41), dr(2, 0, true), dr(3)]), null)
  assert.equal(nextToCreate([dr(1, -1), dr(2)]), null) // a raw "creating" IID
})

test('draftLocked: created or being created', () => {
  assert.equal(draftLocked(dr(1)), false)
  assert.equal(draftLocked(dr(1, 41)), true)
  assert.equal(draftLocked(dr(1, 0, true)), true)
  assert.equal(draftLocked(dr(1, -1)), true)
})

test('canMove and movedKeys: uncreated drafts swap with a neighbour', () => {
  const list = [dr(1), dr(2), dr(3)]
  assert.equal(canMove(list, 1, -1), false)
  assert.equal(canMove(list, 1, 1), true)
  assert.equal(canMove(list, 3, 1), false)
  assert.equal(canMove(list, 9, 1), false)
  assert.deepEqual(movedKeys(list, 1, 1), [2, 1, 3])
  assert.deepEqual(movedKeys(list, 3, -1), [1, 3, 2])
  assert.deepEqual(movedKeys(list, 1, -1), [1, 2, 3])
})

test('canMove and movedKeys: created drafts stay where they are', () => {
  const list = [dr(1, 41), dr(2), dr(3, 0, true), dr(4)]
  assert.equal(canMove(list, 1, 1), false)
  assert.equal(canMove(list, 2, -1), false)
  assert.equal(canMove(list, 2, 1), false)
  assert.equal(canMove(list, 3, 1), false)
  assert.equal(canMove(list, 4, -1), false)
  assert.deepEqual(movedKeys(list, 2, -1), [1, 2, 3, 4])
  assert.deepEqual(movedKeys([dr(1, 41), dr(2), dr(3)], 3, -1), [1, 3, 2])
})

test('draftLabel: the title, else Draft <key>', () => {
  assert.equal(draftLabel({ key: 2, title: ' Refunds ' }), 'Refunds')
  assert.equal(draftLabel({ key: 2, title: '' }), 'Draft 2')
})
