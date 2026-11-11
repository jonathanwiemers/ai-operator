import assert from 'node:assert/strict'
import { test } from 'node:test'
import { keyboardInset } from '../src/ui/useKeyboardInset.ts'

test('keyboardInset: what the keyboard covers below the visual viewport', () => {
  // No visualViewport: nothing known, nothing covered.
  assert.equal(keyboardInset(800, null), 0)
  // No keyboard.
  assert.equal(keyboardInset(800, { height: 800, offsetTop: 0 }), 0)
  // iOS: the layout viewport keeps its height, the visual one shrinks and
  // may be scrolled down within it.
  assert.equal(keyboardInset(800, { height: 460, offsetTop: 0 }), 340)
  assert.equal(keyboardInset(800, { height: 460, offsetTop: 120 }), 220)
  assert.equal(keyboardInset(800, { height: 459.6, offsetTop: 0 }), 340)
  // Chrome with resizes-content: both shrink, nothing to lift. Pinch zoom
  // never makes it negative.
  assert.equal(keyboardInset(460, { height: 460, offsetTop: 0 }), 0)
  assert.equal(keyboardInset(800, { height: 400, offsetTop: 500 }), 0)
})
