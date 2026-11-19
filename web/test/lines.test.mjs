// Tests of src/lines.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  linesLabel,
  measuredLabel,
  mergedLabel,
  mergedTooltip,
  totalsLabel,
  totalsTooltip,
  writtenLabel,
} from '../src/lines.ts'

const lc = (added, deleted) => ({ added, deleted })
const merged = (prod, extra = {}) => ({
  production: prod,
  tests: lc(60, 0),
  docs: lc(10, 0),
  generated: lc(0, 0),
  ...extra,
})

test('linesLabel: +A/−D with a real minus sign', () => {
  assert.equal(linesLabel(40, 5), '+40/−5')
  assert.equal(linesLabel(0, 0), '+0/−0')
})

test('mergedLabel: production lines, ≥ when partial, — when unmeasured', () => {
  assert.equal(mergedLabel(merged(lc(52, 8))), '+52/−8')
  assert.equal(mergedLabel(merged(lc(52, 8), { partial: true })), '≥ +52/−8')
  assert.equal(mergedLabel(undefined), '—')
  // Only tests changed: production is a real 0, not unmeasured (D607-P1).
  assert.equal(mergedLabel(merged(lc(0, 0))), '+0/−0')
})

test('mergedTooltip: the four categories and each companion', () => {
  const m = merged(lc(52, 8))
  const companions = [
    { project: 'group/infra', mrIID: 7, lines: merged(lc(12, 3)) },
    { project: 'group/docs', mrIID: 8 },
  ]
  assert.equal(
    mergedTooltip(m, companions),
    'production +52/−8, tests +60/−0, docs +10/−0, generated +0/−0, group/infra +12/−3, group/docs not measured',
  )
  assert.equal(mergedTooltip(m), 'production +52/−8, tests +60/−0, docs +10/−0, generated +0/−0')
  assert.match(mergedTooltip(merged(lc(1, 1), { partial: true })), /partial/)
  assert.equal(mergedTooltip(undefined, companions.slice(0, 1)), 'Merged lines not measured, group/infra +12/−3')
})

test('measuredLabel: M of N while some merged issues are unmeasured', () => {
  assert.equal(measuredLabel({ merged: 3, measured: 2 }), '2 of 3 measured')
  assert.equal(measuredLabel({ merged: 3, measured: 3 }), '')
  assert.equal(measuredLabel({ merged: 0, measured: 0 }), '')
  assert.equal(measuredLabel(undefined), '')
})

test('totalsLabel and totalsTooltip: a milestone row', () => {
  const t = { lines: merged(lc(40, 5)), merged: 3, measured: 2 }
  assert.equal(totalsLabel(t), '+40/−5')
  assert.equal(totalsTooltip(t), 'production +40/−5, tests +60/−0, docs +10/−0, generated +0/−0 · 2 of 3 measured')
  assert.equal(totalsLabel({ ...t, measured: 0 }), '—')
  assert.equal(totalsTooltip({ ...t, measured: 0 }), 'Merged lines not measured (0 of 3)')
  assert.equal(totalsLabel(undefined), '—')
  assert.equal(totalsTooltip(undefined), 'Nothing merged')
})

test('writtenLabel: missing fields are 0 written, no spend is —', () => {
  assert.equal(writtenLabel({ linesAdded: 120, linesDeleted: 30 }), '+120/−30')
  assert.equal(writtenLabel({}), '+0/−0')
  assert.equal(writtenLabel(undefined), '—')
})
