import assert from 'node:assert/strict'
import { test } from 'node:test'
import { foldNotes, fromGitLabNotes, fromStreamEvents, noteInitials, noteWho } from '../src/noteThread.ts'

const ev = (over) => ({
  id: 1,
  at: '2026-11-11T10:00:00Z',
  namespace: 'team-a',
  project: 'shop',
  kind: 'note',
  actor: 'jo',
  summary: 'hi',
  data: { body: '**hi**', noteable: 'issue' },
  issueIID: 281,
  ...over,
})

test('fromStreamEvents marks mine, the pilot and the context', () => {
  const [mine, pilot, mr, other] = fromStreamEvents(
    [
      ev({ id: 1 }),
      ev({ id: 2, data: { body: 'x', bot: true } }),
      ev({ id: 3, actor: 'rev', mergeRequestIID: 305, data: { noteable: 'mr', url: 'u' }, redacted: true }),
      ev({ id: 4, actor: undefined, issueIID: undefined }),
    ],
    'jo',
  )
  assert.deepEqual([mine.mine, mine.bot, mine.ctx, mine.body], [true, false, '#281', '**hi**'])
  assert.deepEqual([pilot.mine, pilot.bot], [false, true])
  assert.deepEqual([mr.ctx, mr.redacted, mr.url, mr.body], ['!305', true, 'u', undefined])
  assert.equal(other.ctx, undefined)
  assert.equal(fromStreamEvents([ev({})], undefined)[0].mine, false)
})

test('fromGitLabNotes keeps author, time, body and system', () => {
  const [n, s] = fromGitLabNotes(
    [
      { id: 7, author: 'jo', body: 'b', createdAt: 't' },
      { id: 8, author: 'bot', body: 'x', system: true },
    ],
    'jo',
  )
  assert.deepEqual(n, { id: 7, author: 'jo', at: 't', body: 'b', mine: true, bot: false, system: undefined })
  assert.equal(s.mine, false)
  assert.equal(s.system, true)
})

test('noteWho and noteInitials', () => {
  const base = { id: 1, mine: false, bot: false }
  assert.equal(noteWho({ ...base, bot: true, author: 'x' }), 'Pilot')
  assert.equal(noteWho({ ...base, mine: true, author: 'jo' }), 'you')
  assert.equal(noteWho({ ...base, author: 'rev' }), '@rev')
  assert.equal(noteWho(base), 'Comment')
  assert.equal(noteInitials({ ...base, bot: true }), 'P')
  assert.equal(noteInitials({ ...base, mine: true }), 'me')
  assert.equal(noteInitials({ ...base, author: 'reviewer' }), 'RE')
  assert.equal(noteInitials(base), '?')
})

test('foldNotes keeps the last ones until expanded', () => {
  const xs = [1, 2, 3, 4, 5]
  assert.deepEqual(foldNotes(xs, 3, false), { visible: [3, 4, 5], hidden: 2 })
  assert.deepEqual(foldNotes(xs, 3, true), { visible: xs, hidden: 0 })
  assert.deepEqual(foldNotes(xs, undefined, false), { visible: xs, hidden: 0 })
  assert.deepEqual(foldNotes([1, 2], 3, false), { visible: [1, 2], hidden: 0 })
})
