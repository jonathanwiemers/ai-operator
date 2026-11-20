import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createAnnouncer, inboxAnnouncement, joinAnnouncements } from '../src/announce.ts'

// A fake clock: schedule keeps the timers, advance runs the due ones in order.
function setup(opts = {}) {
  let t = 0
  let timers = []
  const out = []
  const a = createAnnouncer({
    now: () => t,
    schedule: (fn, ms) => timers.push({ at: t + ms, fn }),
    emit: (text, politeness) => out.push([t, politeness, text]),
    ...opts,
  })
  function advance(ms) {
    const end = t + ms
    for (;;) {
      timers.sort((x, y) => x.at - y.at)
      const next = timers[0]
      if (!next || next.at > end) break
      timers = timers.slice(1)
      t = next.at
      next.fn()
    }
    t = end
  }
  return { a, out, advance }
}

test('a polite message goes out at once, the next one 2 s later', () => {
  const { a, out, advance } = setup()
  a.say('Task now Running')
  a.say('Inbox: 3 items')
  assert.deepEqual(out, [[0, 'polite', 'Task now Running']])
  advance(1999)
  assert.equal(out.length, 1)
  advance(1)
  assert.deepEqual(out[1], [2000, 'polite', 'Inbox: 3 items'])
})

test('after a quiet 2 s a polite message goes out at once', () => {
  const { a, out, advance } = setup()
  a.say('one')
  advance(2500)
  a.say('two')
  assert.deepEqual(out, [
    [0, 'polite', 'one'],
    [2500, 'polite', 'two'],
  ])
})

test('pending polite messages keep their order and only the newest 3', () => {
  const { a, out, advance } = setup()
  for (const s of ['a', 'b', 'c', 'd', 'e']) a.say(s)
  advance(10000)
  assert.deepEqual(out, [
    [0, 'polite', 'a'],
    [2000, 'polite', 'c'],
    [4000, 'polite', 'd'],
    [6000, 'polite', 'e'],
  ])
})

test('a message dropped from the queue may come again', () => {
  const { a, out, advance } = setup()
  for (const s of ['a', 'b', 'c', 'd', 'e']) a.say(s)
  a.say('b')
  advance(10000)
  assert.deepEqual(
    out.map((x) => x[2]),
    ['a', 'd', 'e', 'b'],
  )
})

test('the same text within 10 s is dropped, and said again after', () => {
  const { a, out, advance } = setup()
  a.say('Task now Waiting for you')
  advance(5000)
  a.say('Task now  Waiting for you ')
  advance(4999)
  a.say('Task now Waiting for you')
  assert.equal(out.length, 1)
  advance(1)
  a.say('Task now Waiting for you')
  assert.deepEqual(out[1], [10000, 'polite', 'Task now Waiting for you'])
})

test('dedupe is per politeness', () => {
  const { a, out } = setup()
  a.say('Offline', 'assertive')
  a.say('Offline', 'polite')
  assert.deepEqual(
    out.map((x) => x[1]),
    ['assertive', 'polite'],
  )
})

test('assertive messages are not throttled but are deduped', () => {
  const { a, out, advance } = setup()
  a.say('one')
  a.say('Send failed', 'assertive')
  a.say('Offline', 'assertive')
  a.say('Offline', 'assertive')
  assert.deepEqual(out, [
    [0, 'polite', 'one'],
    [0, 'assertive', 'Send failed'],
    [0, 'assertive', 'Offline'],
  ])
  advance(10000)
  a.say('Offline', 'assertive')
  assert.equal(out.length, 4)
})

test('empty text is ignored', () => {
  const { a, out, advance } = setup()
  a.say('')
  a.say('   ', 'assertive')
  a.say('now')
  advance(0)
  assert.deepEqual(out, [[0, 'polite', 'now']])
})

test('inboxAnnouncement says the count, or empty', () => {
  assert.equal(inboxAnnouncement(0), 'Inbox empty')
  assert.equal(inboxAnnouncement(1), 'Inbox: 1 item')
  assert.equal(inboxAnnouncement(3), 'Inbox: 3 items')
})

test('joinAnnouncements says texts of one tick as sentences, one alone as it is', () => {
  assert.equal(joinAnnouncements(['Offline']), 'Offline')
  assert.equal(joinAnnouncements(['Too many messages; try again in a minute.', 'Offline']), 'Too many messages; try again in a minute. Offline.')
})
