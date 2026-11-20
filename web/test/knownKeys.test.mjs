// Tests of e2e/knownKeys.ts and the known-failure maps of the browser checks:
// npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { knownKeyProblems, knownRules } from '../e2e/knownKeys.ts'
import { knownOverflow } from '../e2e/known-overflow.ts'
import { knownA11y } from '../e2e/known-a11y.ts'

const link = 'https://gitlab.com/jo.wiemers/ai-operator/-/issues/123'

test('the real maps pass', () => {
  assert.deepEqual(knownKeyProblems(knownOverflow, { dark: false }), [])
  assert.deepEqual(knownKeyProblems(knownA11y, { dark: true, rules: true }), [])
})

test('good keys and values are accepted', () => {
  assert.deepEqual(knownKeyProblems({ 'overview@375': `${link} body 12 px too wide` }, { dark: false }), [])
  assert.deepEqual(knownKeyProblems({ 'overview-dark@1280': '#123 the queue, 4 px' }, { dark: false }), [])
  assert.deepEqual(
    knownKeyProblems(
      {
        'overview@1280': `${link} R3: heading-order`,
        'stream-milestone-needs@1180-dark': `${link} R4: scrollable-region-focusable; #124 R6: color-contrast, target-size`,
      },
      { dark: true, rules: true },
    ),
    [],
  )
})

test('bad keys are rejected', () => {
  assert.deepEqual(knownKeyProblems({ 'overview@abc': link }, { dark: true }), ['overview@abc: key is not "<route>@<width>[-dark]"'])
  assert.deepEqual(knownKeyProblems({ 'overview@1280-dark': link }, { dark: false }), ['overview@1280-dark: key is not "<route>@<width>"'])
  assert.deepEqual(knownKeyProblems({ 'Overview@1280': link }, { dark: false }), ['Overview@1280: key is not "<route>@<width>"'])
  assert.deepEqual(knownKeyProblems({ 'overview@1280-light': link }, { dark: true }), ['overview@1280-light: key is not "<route>@<width>[-dark]"'])
})

test('a value without an issue link is rejected', () => {
  assert.deepEqual(knownKeyProblems({ 'overview@375': 'R3: heading-order' }, { dark: true }), [
    'overview@375: value has no issue link (".../issues/<n>" or "#<n>")',
  ])
  assert.deepEqual(knownKeyProblems({ 'overview@375': 'https://gitlab.com/jo.wiemers/ai-operator/-/issues/ body' }, { dark: false }), [
    'overview@375: value has no issue link (".../issues/<n>" or "#<n>")',
  ])
})

test('an a11y value without rule ids is rejected', () => {
  assert.deepEqual(knownKeyProblems({ 'overview@375': `${link} headings` }, { dark: true, rules: true }), [
    'overview@375: value lists no axe rule ids ("R<n>: <rule id>, ...")',
  ])
})

test('knownRules reads the rule ids of every requirement', () => {
  assert.deepEqual(knownRules(`${link} R3: heading-order`), ['heading-order'])
  assert.deepEqual(knownRules(`${link} R4: scrollable-region-focusable; #124 R6: color-contrast, target-size`), [
    'scrollable-region-focusable',
    'color-contrast',
    'target-size',
  ])
  assert.deepEqual(knownRules(link), [])
})
