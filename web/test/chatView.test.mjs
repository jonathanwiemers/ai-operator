import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  anchoredTop,
  atBottom,
  chatChange,
  chatMessages,
  eventMessage,
  eventMessages,
  eventRole,
  firstSentence,
  localMessage,
  replyAnnouncement,
  stripPrefix,
  streamMessages,
} from '../src/chatView.ts'

const ev = (id, kind, extra = {}) => ({
  id,
  at: `2026-11-20T10:0${id}:00Z`,
  namespace: 'team-a',
  project: 'shop',
  kind,
  summary: `${kind} ${id}`,
  data: {},
  ...extra,
})

test('stripPrefix hides the problem prefix and the blank line after it', () => {
  assert.equal(stripPrefix('problem: #42\n\nretry it', 'problem: #42'), 'retry it')
  assert.equal(stripPrefix('problem: #42', 'problem: #42'), '')
  assert.equal(stripPrefix('problem: milestone\n\nresume', 'problem: milestone'), 'resume')
})

test('stripPrefix keeps a longer issue number and other bodies', () => {
  assert.equal(stripPrefix('problem: #421\n\nx', 'problem: #42'), 'problem: #421\n\nx')
  assert.equal(stripPrefix('hello', 'problem: #42'), 'hello')
  assert.equal(stripPrefix('problem: #42\n\nx', undefined), 'problem: #42\n\nx')
})

test('eventRole: owner chat is user, pilot and agent are assistant, the rest system', () => {
  assert.equal(eventRole({ kind: 'chat.user' }), 'user')
  assert.equal(eventRole({ kind: 'chat.agent' }), 'assistant')
  assert.equal(eventRole({ kind: 'pilot.acted' }), 'assistant')
  assert.equal(eventRole({ kind: 'hold.set' }), 'system')
  assert.equal(eventRole({ kind: 'prompt.answered' }), 'system')
})

test('eventMessage shows a chat body without prefix, an event its summary', () => {
  const chat = eventMessage(ev(1, 'chat.user', { actor: 'jo', data: { body: 'problem: #42\n\nretry' } }), 'problem: #42')
  assert.deepEqual(chat.parts, [{ type: 'text', text: 'retry' }])
  assert.equal(chat.role, 'user')
  assert.equal(chat.metadata.author, 'jo')
  assert.equal(chat.id, 'chat.user:1')

  const hold = eventMessage(ev(2, 'hold.set', { data: { body: 'ignored' } }))
  assert.deepEqual(hold.parts, [{ type: 'text', text: 'hold.set 2' }])
  assert.equal(hold.role, 'system')

  const redacted = eventMessage(ev(3, 'chat.agent', { summary: 'message' }))
  assert.deepEqual(redacted.parts, [{ type: 'text', text: 'message' }])
})

test('eventMessages puts the oldest first', () => {
  const out = eventMessages([ev(3, 'merged'), ev(2, 'hold.set'), ev(1, 'problem.opened')])
  assert.deepEqual(out.map((m) => m.id), ['problem.opened:1', 'hold.set:2', 'merged:3'])
})

test('localMessage is a user message on its way', () => {
  const m = localMessage('a', 'retry', '2026-11-20T10:00:00Z')
  assert.equal(m.role, 'user')
  assert.equal(m.metadata.send, 'sending')
  assert.equal(m.id, 'local:a')
})

test('chatChange tells prepend from append', () => {
  assert.equal(chatChange(['b', 'c'], ['b', 'c']), 'none')
  assert.equal(chatChange(['b', 'c'], ['a', 'b', 'c']), 'prepend')
  assert.equal(chatChange(['b', 'c'], ['b', 'c', 'd']), 'append')
  assert.equal(chatChange([], ['a']), 'replace')
  assert.equal(chatChange(['b', 'c'], ['x', 'y']), 'replace')
  // A local message replaced by the recorded one is not an append.
  assert.equal(chatChange(['b', 'local:1'], ['b', 'chat.user:9']), 'replace')
})

test('atBottom follows only a reader at the end', () => {
  assert.equal(atBottom(500, 500, 1000), true)
  assert.equal(atBottom(460, 500, 1000), true)
  assert.equal(atBottom(400, 500, 1000), false)
})

test('anchoredTop keeps the same message in view after Load older', () => {
  // 300 px from the top before, 800 px of older messages added above.
  assert.equal(anchoredTop({ top: 300, height: 1000 }, 1800), 1100)
  assert.equal(anchoredTop({ top: 0, height: 1000 }, 900), 0)
})

test('chatMessages keeps the order and maps roles: owner right, agent left, rest system', () => {
  const m = chatMessages([
    { id: 1, role: 'user', body: 'go', at: '2026-11-20T10:00:00Z', kind: 'message' },
    { id: 2, role: 'agent', body: 'ok', at: '2026-11-20T10:01:00Z', kind: 'message' },
    { id: 3, role: 'system', at: '2026-11-20T10:02:00Z', kind: 'message' },
  ])
  assert.deepEqual(
    m.map((x) => [x.id, x.role, x.parts[0].text]),
    [
      ['chat:1', 'user', 'go'],
      ['chat:2', 'assistant', 'ok'],
      ['chat:3', 'system', ''],
    ],
  )
  assert.equal(m[0].metadata.chat.id, 1)
})

test('streamMessages turns newest-first items into oldest-first cards with a day label per day', () => {
  const now = Date.parse('2026-11-20T12:00:00Z')
  const a = { ...ev(1, 'merged'), at: '2026-11-19T10:00:00Z' }
  const b = { ...ev(2, 'opened'), at: '2026-11-20T09:00:00Z' }
  const c = { ...ev(3, 'note'), at: '2026-11-20T10:00:00Z' }
  const m = streamMessages(
    [
      { kind: 'thread', key: 't1', at: c.at, notes: [c] },
      { kind: 'event', event: b },
      { kind: 'event', event: a },
    ],
    now,
  )
  assert.deepEqual(
    m.map((x) => x.id),
    ['merged:1', 'opened:2', 'thread:t1'],
  )
  assert.ok(m.every((x) => x.metadata.card))
  assert.ok(m[0].metadata.day)
  assert.ok(m[1].metadata.day, 'a new day gets its label')
  assert.equal(m[2].metadata.day, undefined, 'the same day does not repeat it')
  assert.deepEqual(m[2].metadata.notes, [c])
})

const msg = (id, role, text, metadata = {}) => ({ id, role, parts: [{ type: 'text', text }], metadata })
const asked = msg('chat:1', 'user', 'How is it going?')

test('replyAnnouncement says nothing while the reply is written, once when it is complete', () => {
  const partial = msg('chat:2', 'assistant', 'All good so')
  const done = msg('chat:2', 'assistant', 'All good so far. Two issues merged.')
  const waiting = { messages: [asked], waiting: true }
  const streaming = { messages: [asked, partial], waiting: true }
  const complete = { messages: [asked, done], waiting: false }
  assert.equal(replyAnnouncement(waiting, streaming, 'Pilot'), '')
  assert.equal(replyAnnouncement(streaming, complete, 'Pilot'), 'Pilot replied: All good so far.')
  assert.equal(replyAnnouncement(complete, complete, 'Pilot'), '', 'the same reply again says nothing')
})

test('replyAnnouncement announces a reply that comes in complete', () => {
  const r = msg('chat:2', 'assistant', 'Done!', { author: 'pilot-bot' })
  assert.equal(replyAnnouncement({ messages: [asked], waiting: false }, { messages: [asked, r], waiting: false }), 'pilot-bot replied: Done!')
  const anon = msg('chat:3', 'assistant', 'Yes')
  assert.equal(replyAnnouncement({ messages: [asked], waiting: false }, { messages: [asked, anon], waiting: false }), 'Reply: Yes')
})

test('replyAnnouncement ignores the owner, operator events, the first load and older replies', () => {
  const reply = msg('chat:2', 'assistant', 'Sure.')
  const mine = msg('local:x', 'user', 'Thanks')
  const event = msg('note:5', 'system', 'Run restarted')
  const idle = { messages: [asked, reply], waiting: false }
  assert.equal(replyAnnouncement(idle, { messages: [asked, reply, mine], waiting: false }, 'Pilot'), '', 'a user message')
  assert.equal(replyAnnouncement(idle, { messages: [asked, reply, event], waiting: false }, 'Pilot'), '', 'an event after the reply')
  assert.equal(replyAnnouncement({ messages: [], waiting: false }, idle, 'Pilot'), '', 'the first load')
  // A send that failed ends the wait, with the old reply before the owner's message.
  assert.equal(replyAnnouncement({ messages: [asked, reply, mine], waiting: true }, { messages: [asked, reply, mine], waiting: false }, 'Pilot'), '')
})

test('firstSentence cuts at the first sentence, and a long one at about 140 characters', () => {
  assert.equal(firstSentence('One. Two.'), 'One.')
  assert.equal(firstSentence('Version 1.2 is out! More later'), 'Version 1.2 is out!')
  assert.equal(firstSentence('  no   end  '), 'no end')
  const long = firstSentence(`${'word '.repeat(60)}end.`)
  assert.ok(long.length <= 141, long)
  assert.ok(long.endsWith('word…'), long)
})
