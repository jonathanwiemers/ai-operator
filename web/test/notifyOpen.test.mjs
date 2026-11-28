// Tests of src/notifyOpen.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { clickOpen, clickReplyMs, clickTarget, openURL, parseOpen } from '../src/notifyOpen.ts'

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

test('openURL and parseOpen round-trip an event', () => {
  const open = {
    event: { id: 9, at: '2026-11-25T10:00:00Z', namespace: 'team', project: 'shop', milestoneIID: 3, kind: 'release.ended', summary: 'Released', data: { state: 'Failed', reason: 'a & "b"', title: 'Checkout' } },
  }
  const url = new URL(openURL(open), origin)
  assert.deepEqual(parseOpen(url.searchParams.get('open')), open)
  assert.deepEqual(parseOpen(open), open)
  assert.equal(parseOpen({ event: { namespace: 'team', project: 'shop' } }), null)
})

test('parseOpen: garbage is null', () => {
  for (const v of [undefined, null, '', '{', 'null', '42', '"x"', '{}', '{"item":null}', '{"item":"x"}', ['{}'], { item: { namespace: 'team', project: 'shop' } }, { item: { namespace: 1, project: 'shop', kind: 'pause' } }]) {
    assert.equal(parseOpen(v), null, JSON.stringify(v))
  }
})

// A fake Desk window and click deps that record every call. answer is what
// post's reply does: true answers, undefined stays silent.
function clickFakes({ windows = [win(`${origin}/inbox`)], answer, navigate = async (url) => ({ url }) } = {}) {
  const calls = []
  const ws = windows.map((w, i) => ({
    ...w,
    focus: async () => {
      calls.push(['focus', i])
      throw new Error('focus refused') // errors are ignored
    },
    navigate: async (url) => {
      calls.push(['navigate', i, url])
      return navigate(url)
    },
  }))
  const deps = {
    ms: 20,
    post: (w, message) => {
      calls.push(['post', ws.indexOf(w), message])
      return answer === undefined ? new Promise(() => {}) : Promise.resolve(answer)
    },
    openWindow: async (url) => calls.push(['openWindow', url]),
  }
  return { ws, deps, calls }
}

const clickItem = { item: { namespace: 'team', project: 'shop', kind: 'problem', milestoneIID: 3, issueIID: 12, title: 'Refunds', reason: 'stuck', key: 'k' } }

test('clickOpen: the timeout is 1000 ms', () => {
  assert.equal(clickReplyMs, 1000)
})

test('clickOpen: an answering window gets no navigate and no openWindow', async () => {
  const { ws, deps, calls } = clickFakes({ answer: true })
  await clickOpen(ws, origin, clickItem, deps)
  assert.deepEqual(calls, [['focus', 0], ['post', 0, { open: clickItem }]])
})

test('clickOpen: a silent window is navigated after the timeout', async () => {
  const { ws, deps, calls } = clickFakes()
  const start = Date.now()
  await clickOpen(ws, origin, clickItem, deps)
  assert.ok(Date.now() - start >= 15)
  assert.deepEqual(calls, [['focus', 0], ['post', 0, { open: clickItem }], ['navigate', 0, openURL(clickItem)]])
})

test('clickOpen: a reply other than opened counts as silence', async () => {
  const { ws, deps, calls } = clickFakes({ answer: false })
  await clickOpen(ws, origin, clickItem, deps)
  assert.deepEqual(calls.at(-1), ['navigate', 0, openURL(clickItem)])
})

test('clickOpen: a navigate that rejects opens a window', async () => {
  const { ws, deps, calls } = clickFakes({ navigate: async () => { throw new TypeError('not controlled') } })
  await clickOpen(ws, origin, clickItem, deps)
  assert.deepEqual(calls.slice(2), [['navigate', 0, openURL(clickItem)], ['openWindow', openURL(clickItem)]])
})

test('clickOpen: a navigate that resolves null opens a window', async () => {
  const { ws, deps, calls } = clickFakes({ navigate: async () => null })
  await clickOpen(ws, origin, clickItem, deps)
  assert.deepEqual(calls.slice(2), [['navigate', 0, openURL(clickItem)], ['openWindow', openURL(clickItem)]])
})

test('clickOpen: a post that throws is a silent window', async () => {
  const { ws, deps, calls } = clickFakes()
  deps.post = () => Promise.reject(new Error('gone'))
  await clickOpen(ws, origin, clickItem, deps)
  assert.deepEqual(calls, [['focus', 0], ['navigate', 0, openURL(clickItem)]])
})

test('clickOpen: without a window a new one opens on the target, or on /', async () => {
  for (const [open, url] of [[clickItem, openURL(clickItem)], [null, '/']]) {
    const { ws, deps, calls } = clickFakes({ windows: [win('https://other.example/')] })
    await clickOpen(ws, origin, open, deps)
    assert.deepEqual(calls, [['openWindow', url]])
  }
})

test('clickOpen: without open the window only comes forward', async () => {
  const { ws, deps, calls } = clickFakes({ windows: [win(`${origin}/a`), win(`${origin}/b`, { visibilityState: 'visible' })] })
  await clickOpen(ws, origin, null, deps)
  assert.deepEqual(calls, [['focus', 1]])
})
