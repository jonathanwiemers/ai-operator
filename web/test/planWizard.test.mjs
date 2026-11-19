// Tests of src/planWizard.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  answersFrom,
  changedIds,
  emptyOther,
  firstStep,
  mergePicks,
  preselect,
  readPicks,
  summary,
  wizardBlocked,
  wizardButton,
  wizardKey,
  writePicks,
} from '../src/planWizard.ts'

const dec = (n, over = {}) => ({
  id: `D${n}`,
  title: `Title ${n}`,
  question: 'Which?',
  options: ['A', 'B', 'C'],
  recommended: 1,
  why: 'Because',
  state: 'locked',
  version: 1,
  decidedBy: 'agent',
  decidedAt: '2026-11-18T10:00:00Z',
  ...over,
})
const seven = [1, 2, 3, 4, 5, 6, 7].map((n) => dec(n))

class MemoryStorage {
  m = new Map()
  getItem(k) { return this.m.has(k) ? this.m.get(k) : null }
  setItem(k, v) { this.m.set(k, String(v)) }
  removeItem(k) { this.m.delete(k) }
}

test('wizardButton: Review decisions, Answer 1 question, Answer N questions, hidden without decisions', () => {
  assert.deepEqual(wizardButton([]), { label: 'Review decisions', show: false })
  assert.deepEqual(wizardButton([dec(1), dec(2)]), { label: 'Review decisions', show: true })
  assert.deepEqual(wizardButton([dec(1, { state: 'open' }), dec(2)]), { label: 'Answer 1 question', show: true })
  const three = [dec(1, { state: 'open' }), dec(2, { state: 'open' }), dec(3, { state: 'open' })]
  assert.deepEqual(wizardButton(three), { label: 'Answer 3 questions', show: true })
})

test('wizardBlocked: a running turn and a created milestone disable the wizard', () => {
  assert.equal(wizardBlocked('Working'), 'The agent is answering…')
  assert.equal(wizardBlocked('Created'), 'The milestone was created')
  assert.equal(wizardBlocked('Idle'), '')
  assert.equal(wizardBlocked(undefined), '')
})

test('preselect: signed off, unsigned and open', () => {
  assert.deepEqual(preselect(dec(1, { signedOffBy: 'sub-ada' })), { index: 1, mark: 'Signed off' })
  assert.deepEqual(preselect(dec(1)), { index: 1, mark: 'Recommended' })
  assert.deepEqual(preselect(dec(1, { state: 'open', recommended: 2 })), { index: 2, mark: 'Recommended' })
})

test('firstStep: the first open or not signed-off step, else the first', () => {
  const signed = { signedOffBy: 'sub-ada' }
  assert.equal(firstStep([dec(1, signed), dec(2, signed), dec(3, { state: 'open' })]), 2)
  assert.equal(firstStep([dec(1, signed), dec(2), dec(3)]), 1)
  assert.equal(firstStep([dec(1, signed), dec(2, signed)]), 0)
  assert.equal(firstStep([]), 0)
})

test('answersFrom: keep, another option, Other, and a pick equal to the preselection', () => {
  const picks = {
    D2: { version: 1, pick: 0 },
    D3: { version: 1, pick: 1 }, // the preselection: a keep
    D6: { version: 1, pick: '  Use both  ' },
  }
  assert.deepEqual(answersFrom(seven, picks), [
    { id: 'D1', version: 1, keep: true },
    { id: 'D2', version: 1, option: 0 },
    { id: 'D3', version: 1, keep: true },
    { id: 'D4', version: 1, keep: true },
    { id: 'D5', version: 1, keep: true },
    { id: 'D6', version: 1, other: 'Use both' },
    { id: 'D7', version: 1, keep: true },
  ])
  // A pick made on another version is not this version's answer.
  assert.deepEqual(answersFrom([dec(1, { version: 2 })], { D1: { version: 1, pick: 0 } }), [{ id: 'D1', version: 2, keep: true }])
})

test('summary: keep five, change D2 and write Other for D6', () => {
  const s = summary(seven, { D2: { version: 1, pick: 2 }, D6: { version: 1, pick: 'Use both' } })
  assert.deepEqual(s.kept.map((r) => r.id), ['D1', 'D3', 'D4', 'D5', 'D7'])
  assert.deepEqual(s.changed, [
    { id: 'D2', title: 'Title 2', pick: 'C' },
    { id: 'D6', title: 'Title 6', pick: 'Other: Use both' },
  ])
})

test('emptyOther: the step whose Other has no text', () => {
  assert.equal(emptyOther(seven, { D6: { version: 1, pick: 'x' } }), -1)
  assert.equal(emptyOther(seven, { D4: { version: 1, pick: ' ' } }), 3)
})

test('readPicks and writePicks: restore picks and the clientKey per plan from a stubbed storage', () => {
  const storage = new MemoryStorage()
  const key = wizardKey('ns', 'proj', 'p1')
  assert.equal(key, 'plan-decisions:ns/proj/p1')
  assert.equal(readPicks(key, storage), null)
  const state = { clientKey: 'k1', picks: { D2: { version: 1, pick: 0 }, D6: { version: 1, pick: 'mine' } }, text: 'and more' }
  writePicks(key, state, storage)
  assert.deepEqual(readPicks(key, storage), state)
  assert.equal(readPicks(wizardKey('ns', 'proj', 'p2'), storage), null)

  // Restore drops the picks whose decision moved to another version.
  const now = [dec(2), dec(6, { version: 2 })]
  assert.deepEqual(mergePicks(readPicks(key, storage).picks, now), { D2: { version: 1, pick: 0 } })

  writePicks(key, null, storage)
  assert.equal(readPicks(key, storage), null)

  storage.setItem(key, 'not json')
  assert.equal(readPicks(key, storage), null)
  storage.setItem(key, JSON.stringify({ clientKey: 'k', picks: { D1: { version: 'x', pick: 0 }, D2: { version: 3, pick: 1 } } }))
  assert.deepEqual(readPicks(key, storage), { clientKey: 'k', picks: { D2: { version: 3, pick: 1 } }, text: '' })
  // No sessionStorage (node) is no state, not an error.
  assert.equal(readPicks(key), null)
  assert.doesNotThrow(() => writePicks(key, state))
})

test('mergePicks and changedIds: a 409 keeps unchanged versions and marks the changed steps', () => {
  const before = [dec(1), dec(2), dec(3), dec(4)]
  const picks = {
    D1: { version: 1, pick: 0 },
    D2: { version: 1, pick: 2 },
    D3: { version: 1, pick: 'other text' },
    D4: { version: 1, pick: 2 },
  }
  const after = [dec(1), dec(2, { version: 2 }), dec(3), dec(4, { options: ['A', 'B'] }), dec(8)]
  assert.deepEqual(mergePicks(picks, after), { D1: { version: 1, pick: 0 }, D3: { version: 1, pick: 'other text' } })
  assert.deepEqual(changedIds(before, after), ['D2', 'D8'])
})
