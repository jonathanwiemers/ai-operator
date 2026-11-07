// Tests of src/followUpDraft.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { draftRefused, draftWritten, maxTitle, selectable, takenMessage, templateDraft } from '../src/followUpDraft.ts'

test('templateDraft: title from the milestone, a section per follow-up with its body', () => {
  const d = templateDraft('Checkout', [
    { title: 'Partial refunds', body: 'Refund part of an order.\n\n- keep the receipt' },
    { title: 'Receipts  in\nPDF' },
  ])
  assert.equal(d.title, 'Follow-ups of Checkout')
  assert.equal(d.description, '## Partial refunds\n\nRefund part of an order.\n\n- keep the receipt\n\n## Receipts in PDF')
})

test('templateDraft: the title is one line within GitLab’s limit', () => {
  const d = templateDraft(' a\nb ' + 'x'.repeat(400), [])
  assert.ok(!d.title.includes('\n'))
  assert.equal([...d.title].length, maxTitle)
  assert.ok(d.title.startsWith('Follow-ups of a b x'))
  assert.equal(d.description, '')
  assert.equal(templateDraft('', []).title, 'Follow-ups of the milestone')
})

test('templateDraft: HTML stays text for the markdown renderer', () => {
  const d = templateDraft('M', [{ title: '<b>x</b>', body: '<script>alert(1)</script>' }])
  assert.equal(d.description, '## <b>x</b>\n\n<script>alert(1)</script>')
})

test('selectable: only the open follow-ups', () => {
  const fs = [
    { number: 1, state: 'open' },
    { number: 2, state: 'issue' },
    { number: 3, state: 'milestone' },
    { number: 4, state: 'dismissed' },
    { number: 5, state: 'open' },
  ]
  assert.deepEqual(
    selectable(fs).map((f) => f.number),
    [1, 5],
  )
  assert.deepEqual(selectable([]), [])
})

test('draftWritten: a new draft or error, not the one seen before or a pending one', () => {
  assert.ok(!draftWritten(undefined, undefined))
  assert.ok(!draftWritten({ numbers: [1], pending: true }, undefined))
  assert.ok(!draftWritten({ numbers: [1], title: 'old', at: 't1' }, 't1'))
  assert.ok(draftWritten({ numbers: [1], title: 'new', at: 't2' }, 't1'))
  assert.ok(draftWritten({ numbers: [1], error: 'budget', at: 't2' }, undefined))
})

test('takenMessage: names the taken follow-ups', () => {
  const fs = [
    { number: 1, title: 'Refunds' },
    { number: 3, title: 'Receipts' },
  ]
  assert.equal(
    takenMessage([1, 3], fs),
    'Already opened, taken or dismissed: 1. Refunds; 3. Receipts. Nothing was created; a leftover goes to one place only.',
  )
  assert.match(takenMessage([7], fs), /dismissed: 7\./)
})

test('draftRefused: the operator’s answers to a draft command', () => {
  assert.ok(!draftRefused('The pilot writes a draft of a follow-up milestone from follow-ups 1, 3; it shows in the Retrospective tab.'))
  assert.ok(!draftRefused('The pilot is writing a draft already; ask again once it shows in the Retrospective tab.'))
  assert.ok(draftRefused('No draft: follow-up 1 was opened as #12.'))
  assert.ok(draftRefused('The follow-ups of this milestone are settled: there is nothing left to draft from.'))
})
