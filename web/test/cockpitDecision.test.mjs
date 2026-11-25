// Tests of src/cockpitDecision.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { decisionItem, decisionsByID, supersedingID } from '../src/cockpitDecision.ts'

const view = (id, extra = {}) => ({ id, title: `title ${id}`, choice: `choice ${id}`, superseded: false, revisit: { mode: 'none', text: '' }, ...extra })

test('supersedingID takes the view field, else the decision that supersedes the ID', () => {
  const byID = decisionsByID([view('D1', { superseded: true, supersededBy: 'D3' }), view('D2', { superseded: true }), view('D4', { supersedes: 'D2' })])
  assert.equal(supersedingID('D1', byID), 'D3')
  assert.equal(supersedingID('D2', byID), 'D4')
  assert.equal(supersedingID('D4', byID), undefined)
  assert.equal(supersedingID('D9', byID), undefined)
})

test('decisionItem uses the stored decision with its revisit', () => {
  const stored = view('D712-1', { superseded: true, supersededBy: 'D712-2' })
  const item = decisionItem({ id: 'D712-1', title: 'old', choice: 'old' }, decisionsByID([stored]))
  assert.deepEqual(item, { id: 'D712-1', title: 'title D712-1', choice: 'choice D712-1', superseded: true, supersededBy: 'D712-2', view: stored })
})

test('decisionItem falls back to the event data without Revisit', () => {
  const item = decisionItem({ id: 'D712-1', title: 'Refund currency', choice: 'Settlement' }, decisionsByID(undefined))
  assert.deepEqual(item, { id: 'D712-1', title: 'Refund currency', choice: 'Settlement', superseded: false, supersededBy: undefined, view: undefined })
})

test('decisionItem marks a decision a loaded one supersedes', () => {
  const item = decisionItem({ id: 'D1', title: 't', choice: 'c' }, decisionsByID([view('D2', { supersedes: 'D1' })]))
  assert.equal(item.superseded, true)
  assert.equal(item.supersededBy, 'D2')
})
