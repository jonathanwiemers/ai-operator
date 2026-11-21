// Tests of src/refMenu.ts, the plan chat's % menu: npm test (node --test).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { refItems, refPick, refQuery } from '../src/stream.ts'

test('refQuery opens on % at the start, mid-text and after (', () => {
  assert.deepEqual(refQuery('%', 1), { start: 0, query: '' })
  assert.deepEqual(refQuery('%3', 2), { start: 0, query: '3' })
  assert.deepEqual(refQuery('build on %36', 12), { start: 9, query: '36' })
  assert.deepEqual(refQuery('see (%draft-1', 13), { start: 5, query: 'draft-1' })
  assert.deepEqual(refQuery('a\n%x', 4), { start: 2, query: 'x' })
  // The caret decides, not the end of the text.
  assert.deepEqual(refQuery('on %3 and more', 5), { start: 3, query: '3' })
})

test('refQuery stays closed inside a word and after the reference', () => {
  assert.equal(refQuery('50%', 3), null)
  assert.equal(refQuery('a%3', 3), null)
  assert.equal(refQuery('%36 ', 4), null)
  assert.equal(refQuery('no ref', 6), null)
  assert.equal(refQuery('%3.', 3), null)
})

const src = {
  milestones: [
    { iid: 31, title: 'Settling', state: 'done' },
    { iid: 32, title: 'Metrics page', state: 'done' },
    { iid: 34, title: 'Decisions wizard', state: 'waiting', queuePosition: 3 },
    { iid: 36, title: 'Nuxt UI layout', state: 'waiting', queuePosition: 2 },
    { iid: 37, title: 'Accessibility', state: 'running', queuePosition: 1 },
    { iid: 38, title: 'One language', state: 'planned' },
    { iid: 12, title: 'Old', state: 'planned' },
  ],
  drafts: [
    { key: 1, title: 'Plan context', createdMilestoneIID: 41 },
    { key: 2, title: 'Draft list' },
    { key: 3, title: '' },
  ],
}

test('refItems: open in queue order, then done newest first, then uncreated drafts', () => {
  assert.deepEqual(
    refItems(src, '').map((i) => i.ref),
    ['%37', '%36', '%34', '%12', '%38', '%32', '%31', '%draft-2', '%draft-3'],
  )
  assert.deepEqual(refItems(src, '')[0], { ref: '%37', label: 'Accessibility', state: 'running' })
  assert.deepEqual(refItems(src, 'draft').at(-1), { ref: '%draft-3', label: 'Untitled draft', state: 'draft' })
})

test('refItems filters by number prefix and by title', () => {
  assert.deepEqual(
    refItems(src, '3').map((i) => i.ref),
    ['%37', '%36', '%34', '%38', '%32', '%31'],
  )
  assert.deepEqual(
    refItems(src, '36').map((i) => i.ref),
    ['%36'],
  )
  // A number does not match a draft's key or a title.
  assert.deepEqual(
    refItems(src, '2').map((i) => i.ref),
    [],
  )
  assert.deepEqual(
    refItems(src, 'METRICS').map((i) => i.ref),
    ['%32'],
  )
  assert.deepEqual(
    refItems(src, 'draft').map((i) => i.ref),
    ['%draft-2', '%draft-3'],
  )
  assert.deepEqual(
    refItems(src, 'list').map((i) => i.ref),
    ['%draft-2'],
  )
})

test('refItems caps done milestones at 10 and all options at 20', () => {
  const done = Array.from({ length: 15 }, (_, i) => ({ iid: i + 1, title: `D${i + 1}`, state: 'done' }))
  assert.deepEqual(
    refItems({ milestones: done, drafts: [] }, '').map((i) => i.ref),
    ['%15', '%14', '%13', '%12', '%11', '%10', '%9', '%8', '%7', '%6'],
  )
  // The cap applies after the filter: %1 is old but still found.
  assert.deepEqual(
    refItems({ milestones: done, drafts: [] }, '1').map((i) => i.ref),
    ['%15', '%14', '%13', '%12', '%11', '%10', '%1'],
  )
  const open = Array.from({ length: 30 }, (_, i) => ({ iid: i + 100, title: 'x', state: 'planned' }))
  assert.equal(refItems({ milestones: open, drafts: [{ key: 1, title: 'd' }] }, '').length, 20)
})

test('refItems lists a milestone once when it has two rows', () => {
  const twice = [
    { iid: 5, title: 'A', state: 'planned' },
    { iid: 5, title: 'A', state: 'planned' },
  ]
  assert.equal(refItems({ milestones: twice, drafts: [] }, '').length, 1)
})

test('refPick puts the reference and a space in place of the query', () => {
  assert.deepEqual(refPick('%3', 0, 2, '%36'), { body: '%36 ', caret: 4 })
  assert.deepEqual(refPick('build on %3 please', 9, 11, '%36'), { body: 'build on %36  please', caret: 13 })
  assert.deepEqual(refPick('(%dr', 1, 4, '%draft-2'), { body: '(%draft-2 ', caret: 10 })
})
