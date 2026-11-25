// Tests of src/notifyOpen.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { clickTarget, openURL, parseOpen } from '../src/notifyOpen.ts'

const origin = 'https://desk.example'
const win = (url, over = {}) => ({ url, focused: false, visibilityState: 'hidden', ...over })

test('clickTarget: without a window a new one opens', () => {
  assert.deepEqual(clickTarget([], origin), { openWindow: true })
})

test('clickTarget: one Desk window is focused', () => {
  assert.deepEqual(clickTarget([win(`${origin}/inbox`)], origin), { focus: 0 })
})

test('clickTarget: prefers the focused window, then a visible one', () => {
  const windows = [win(`${origin}/a`), win(`${origin}/b`, { visibilityState: 'visible' }), win(`${origin}/c`, { focused: true, visibilityState: 'visible' })]
  assert.deepEqual(clickTarget(windows, origin), { focus: 2 })
  assert.deepEqual(clickTarget(windows.slice(0, 2), origin), { focus: 1 })
})

test('clickTarget: ignores windows of other origins and broken URLs', () => {
  const windows = [win('https://other.example/', { focused: true }), win('not a url', { focused: true })]
  assert.deepEqual(clickTarget(windows, origin), { openWindow: true })
  assert.deepEqual(clickTarget([...windows, win(`${origin}/`)], origin), { focus: 2 })
})

test('openURL and parseOpen round-trip an item', () => {
  const open = { item: { namespace: 'team', project: 'shop', kind: 'problem', milestoneIID: 3, issueIID: 12, title: 'Refunds & "VAT"', reason: 'stuck', key: 'k' } }
  const url = new URL(openURL(open), origin)
  assert.equal(url.pathname, '/')
  assert.deepEqual(parseOpen(url.searchParams.get('open')), open)
  assert.deepEqual(parseOpen(open), open)
})

test('parseOpen: garbage is null', () => {
  for (const v of [undefined, null, '', '{', 'null', '42', '"x"', '{}', '{"item":null}', '{"item":"x"}', ['{}'], { item: { namespace: 'team', project: 'shop' } }, { item: { namespace: 1, project: 'shop', kind: 'pause' } }]) {
    assert.equal(parseOpen(v), null, JSON.stringify(v))
  }
})
