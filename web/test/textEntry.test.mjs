// Tests of src/textEntry.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { textEntry } from '../src/textEntry.ts'

test('textEntry: text inputs, textareas and editable elements take typing', () => {
  for (const type of ['text', 'search', 'email', 'url', 'tel', 'password', 'number']) {
    assert.equal(textEntry({ tagName: 'INPUT', type }), true, type)
  }
  assert.equal(textEntry({ tagName: 'TEXTAREA' }), true)
  assert.equal(textEntry({ tagName: 'DIV', isContentEditable: true }), true)
})

test('textEntry: controls without a keyboard do not', () => {
  for (const type of ['checkbox', 'radio', 'button', 'submit', 'range', 'file']) {
    assert.equal(textEntry({ tagName: 'INPUT', type }), false, type)
  }
  assert.equal(textEntry({ tagName: 'INPUT', type: 'text', readOnly: true }), false)
  assert.equal(textEntry({ tagName: 'TEXTAREA', readOnly: true }), false)
  assert.equal(textEntry({ tagName: 'SELECT' }), false)
  assert.equal(textEntry({ tagName: 'BUTTON' }), false)
  assert.equal(textEntry({ tagName: 'A', isContentEditable: false }), false)
})

test('textEntry: nothing focused', () => {
  assert.equal(textEntry(null), false)
  assert.equal(textEntry(undefined), false)
})
