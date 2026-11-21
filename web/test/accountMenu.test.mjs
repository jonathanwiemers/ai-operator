// Tests of src/accountMenu.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { accountItems, initials } from '../src/accountMenu.ts'

const about = { text: 'ai-operator 2026.11.3', href: 'https://gitlab.example.com/r/2026.11.3', note: 'Update to 2026.11.4 rolling out' }
const input = (over = {}) => ({
  me: { username: 'jo', roles: ['owner', 'viewer'], authMode: 'oidc' },
  lasts: 'Signed in until 8 Jan unless you are away longer',
  about,
  theme: 'auto',
  setTheme: () => {},
  signOut: () => {},
  ...over,
})
const labels = (groups) => groups.map((g) => g.map((i) => i.label))

test('accountItems with OIDC: who, Appearance, version, Sign out', () => {
  const g = accountItems(input())
  assert.deepEqual(labels(g), [['jo'], ['Appearance', 'Light', 'Dark', 'Auto'], ['ai-operator 2026.11.3'], ['Sign out']])
  assert.deepEqual(g[0][0], { type: 'label', slot: 'who', label: 'jo', roles: 'owner, viewer', lasts: input().lasts })
  assert.deepEqual(
    g[1].slice(1).map((i) => [i.type, i.theme, i.checked]),
    [
      ['checkbox', 'light', false],
      ['checkbox', 'dark', false],
      ['checkbox', 'auto', true],
    ],
  )
  assert.equal(g[2][0].to, about.href)
  assert.equal(g[2][0].target, '_blank')
  assert.equal(g[2][0].description, about.note)
})

test('accountItems with auth off: "No login (auth off)", no login line, no Sign out', () => {
  const g = accountItems(input({ me: { username: 'anonymous', roles: [], authMode: 'none' }, lasts: 'ignored' }))
  assert.deepEqual(labels(g), [['No login (auth off)'], ['Appearance', 'Light', 'Dark', 'Auto'], ['ai-operator 2026.11.3']])
  assert.equal(g[0][0].roles, 'no roles')
  assert.equal(g[0][0].lasts, null)
})

test('accountItems: the version without a link or note, and none before /about answers', () => {
  const plain = accountItems(input({ about: { text: 'ai-operator dev', href: '', note: null } }))[2][0]
  assert.equal(plain.to, undefined)
  assert.equal(plain.description, undefined)
  assert.equal(accountItems(input({ about: null })).length, 3)
})

test('accountItems: a theme item sets its theme and keeps the menu open; Sign out signs out', () => {
  const set = []
  let out = 0
  const g = accountItems(input({ theme: 'dark', setTheme: (t) => set.push(t), signOut: () => out++ }))
  const [light, dark] = g[1].slice(1)
  assert.equal(dark.checked, true)
  light.onUpdateChecked(true)
  dark.onUpdateChecked(false)
  assert.deepEqual(set, ['light', 'dark'])
  let prevented = false
  light.onSelect({ preventDefault: () => (prevented = true) })
  assert.equal(prevented, true)
  g[3][0].onSelect(new Event('select'))
  assert.equal(out, 1)
})

test('initials: two parts or the first two letters, upper case', () => {
  assert.equal(initials('jo.wiemers'), 'JW')
  assert.equal(initials('Ada Lovelace'), 'AL')
  assert.equal(initials('jo'), 'JO')
  assert.equal(initials('anonymous'), 'AN')
  assert.equal(initials(''), '?')
  assert.equal(initials(undefined), '?')
})
