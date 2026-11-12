import assert from 'node:assert/strict'
import { test } from 'node:test'
import { effectScope, ref } from 'vue'
import { signInAgain } from '../src/signInAgain.ts'
import { keepDraft, keepThroughSignIn, signInDraftKey, takeDraft } from '../src/signInDraft.ts'

// fakeBrowser stands in for the location and sessionStorage.
function fakeBrowser(pathname, search = '', items = {}, broken = false) {
  const store = new Map(Object.entries(items))
  const calls = []
  const check = () => {
    if (broken) throw new Error('SecurityError')
  }
  const storage = {
    getItem: (k) => (check(), store.get(k) ?? null),
    setItem: (k, v) => (check(), store.set(k, String(v))),
    removeItem: (k) => (check(), store.delete(k)),
  }
  globalThis.sessionStorage = storage
  globalThis.window = { location: { pathname, search, assign: (url) => calls.push(url) }, sessionStorage: storage }
  return { store, calls }
}

// mount runs the composable as a component's setup would; stop() unmounts.
function mount(name, text) {
  const scope = effectScope()
  scope.run(() => keepThroughSignIn(name, text))
  return scope
}

const chatKey =signInDraftKey('/projects/team-a/shop/tasks/t1?tab=chat', 'chat:team-a/shop/t1')

test('signInDraftKey: per route and composer', () => {
  assert.equal(chatKey, 'desk-sign-in-draft:/projects/team-a/shop/tasks/t1?tab=chat:chat:team-a/shop/t1')
  assert.notEqual(signInDraftKey('/a', 'note'), signInDraftKey('/b', 'note'))
  assert.notEqual(signInDraftKey('/a', 'note'), signInDraftKey('/a', 'comment:x'))
})

test('keepDraft: blank text is not stored', () => {
  const { store } = fakeBrowser('/')
  keepDraft('k', '')
  keepDraft('k', '  \n ')
  assert.equal(store.size, 0)
  keepDraft('k', ' hello\n')
  assert.equal(store.get('k'), ' hello\n')
})

test('takeDraft: once, then gone', () => {
  const { store } = fakeBrowser('/', '', { k: 'hello' })
  assert.equal(takeDraft('k'), 'hello')
  assert.equal(store.has('k'), false)
  assert.equal(takeDraft('k'), null)
})

test('keepDraft and takeDraft without storage', () => {
  fakeBrowser('/', '', {}, true)
  keepDraft('k', 'hello')
  assert.equal(takeDraft('k'), null)
})

test('keepThroughSignIn: restores into an empty composer and drops the copy', () => {
  const { store } = fakeBrowser('/projects/team-a/shop/tasks/t1', '?tab=chat', { [chatKey]: 'half a sentence' })
  const body = ref('')
  const scope = mount('chat:team-a/shop/t1', body)
  assert.equal(body.value, 'half a sentence')
  assert.equal(store.size, 0)
  scope.stop()
})

test('keepThroughSignIn: text in the composer stays, the copy is dropped', () => {
  const { store } = fakeBrowser('/projects/team-a/shop/tasks/t1', '?tab=chat', { [chatKey]: 'old' })
  const body = ref('typed already')
  const scope = mount('chat:team-a/shop/t1', body)
  assert.equal(body.value, 'typed already')
  assert.equal(store.size, 0)
  scope.stop()
})

test('keepThroughSignIn: no restore on another route', () => {
  const { store } = fakeBrowser('/projects/team-a/shop/tasks/t1', '', { [chatKey]: 'elsewhere' })
  const body = ref('')
  const scope = mount('chat:team-a/shop/t1', body)
  assert.equal(body.value, '')
  assert.equal(store.get(chatKey), 'elsewhere')
  scope.stop()
})

// Last: after signInAgain the module is navigating away for good.
test('keepThroughSignIn: keeps non-blank text before the sign-in, until disposed', () => {
  const { store, calls } = fakeBrowser('/projects/team-a/shop', '?tab=stream')
  const note = ref('')
  const empty = ref('  ')
  const gone = ref('unmounted')
  mount(() => 'note:team-a/shop', note)
  mount('prompt:team-a/shop/P1', empty)
  mount('comment:team-a/shop/t1/', gone).stop()

  note.value = 'typed, not sent'
  assert.equal(signInAgain(), true)
  assert.deepEqual(calls, ['/oauth2/login?return=%2Fprojects%2Fteam-a%2Fshop%3Ftab%3Dstream'])
  assert.deepEqual(
    [...store.keys()].filter((k) => k.startsWith('desk-sign-in-draft:')),
    [signInDraftKey('/projects/team-a/shop?tab=stream', 'note:team-a/shop')],
  )
  assert.equal(store.get(signInDraftKey('/projects/team-a/shop?tab=stream', 'note:team-a/shop')), 'typed, not sent')
})
