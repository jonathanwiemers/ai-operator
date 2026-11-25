import assert from 'node:assert/strict'
import { test } from 'node:test'
import { chatAnswer, cockpitClass, cockpitItems, cockpitStreamQuery, groupSummary, pilotStreamKinds, runtimeStreamKinds } from '../src/stream.ts'
import { localMinute, localTimes, localTimesText } from '../src/localTime.ts'

let id = 0
// ev builds an event at minute m of a day; the defaults are #712 in %5 of g/p.
const ev = (kind, m, extra = {}) => ({
  id: ++id,
  at: `2026-11-24T10:${String(m).padStart(2, '0')}:00Z`,
  namespace: 'ns',
  project: 'p',
  milestoneIID: 5,
  issueIID: 712,
  kind,
  summary: kind,
  data: {},
  ...extra,
})
// newest first, as GET /stream returns it
const stream = (...events) => [...events].reverse()
const shape = (items) =>
  items.map((i) => (i.type === 'group' ? `group:${i.key}:${i.events.length}` : i.type === 'thread' ? `thread:${i.notes.length}` : i.event.kind))

test('cockpitClass gives every kind exactly one class', () => {
  const kinds = [...pilotStreamKinds, ...runtimeStreamKinds]
  assert.deepEqual(Object.keys(cockpitClass).sort(), [...kinds].sort())
  const want = {
    pinned: ['prompt.posted', 'proposal', 'problem.opened', 'needs'],
    folded: ['run.started', 'run.finished', 'push', 'ci', 'review', 'checkpoint'],
  }
  for (const k of kinds) {
    const cls = want.pinned.includes(k) ? 'pinned' : want.folded.includes(k) ? 'folded' : 'shown'
    assert.equal(cockpitClass[k], cls, k)
  }
  assert.equal(cockpitClass['decision.recorded'], 'shown')
  assert.ok(pilotStreamKinds.includes('decision.recorded'))
})

test('cockpitItems groups folded events by issue between shown events, oldest first', () => {
  const items = cockpitItems(
    stream(
      ev('run.started', 1),
      ev('run.started', 2, { issueIID: 713 }),
      ev('ci', 3),
      ev('merged', 4, { issueIID: 713 }),
      ev('review', 5, { data: { blocking: 0 } }),
      ev('run.finished', 6),
    ),
  )
  assert.deepEqual(shape(items), ['group:ns/p/5/712:2', 'group:ns/p/5/713:1', 'merged', 'group:ns/p/5/712:2'])
  const [g] = items
  assert.equal(g.from, '2026-11-24T10:01:00Z')
  assert.equal(g.to, '2026-11-24T10:03:00Z')
  assert.equal(g.issueIID, 712)
  assert.equal(g.milestoneIID, 5)
  assert.equal(g.summary, '#712 · 1 run, 1 CI round')
  assert.equal(items[3].summary, '#712 · review clean')
})

test('cockpitItems keeps pinned kinds in the stream and splits groups at them', () => {
  const items = cockpitItems(stream(ev('ci', 1), ev('problem.opened', 2), ev('ci', 3), ev('pilot.acted', 4)))
  assert.deepEqual(shape(items), ['group:ns/p/5/712:1', 'problem.opened', 'group:ns/p/5/712:1', 'pilot.acted'])
})

test('cockpitItems keeps note threads together', () => {
  const note = (m) => ev('note', m, { data: { noteable: 'issue' } })
  const items = cockpitItems(stream(note(1), ev('run.started', 2), note(3)))
  assert.deepEqual(shape(items), ['group:ns/p/5/712:1', 'thread:2'])
  assert.deepEqual(
    items[1].notes.map((n) => n.at),
    ['2026-11-24T10:01:00Z', '2026-11-24T10:03:00Z'],
  )
})

test('cockpitItems with scope all folds all but merges and milestone state per project', () => {
  const q = { project: 'q' }
  const items = cockpitItems(
    stream(
      ev('run.started', 1),
      ev('pilot.acted', 2),
      ev('ci', 3, q),
      ev('problem.opened', 4, { ...q, issueIID: 9 }),
      ev('autopilot.paused', 5),
      ev('merged', 6, q),
    ),
    { scope: 'all' },
  )
  assert.deepEqual(shape(items), ['group:ns/p:2', 'group:ns/q:2', 'autopilot.paused', 'merged'])
  assert.equal(items[0].issueIID, undefined)
  assert.equal(items[1].summary, 'q · 1 CI round')
  assert.equal(items[0].summary, 'p · 1 run')
})

test('cockpitItems in project scope keys groups by milestone and issue', () => {
  const items = cockpitItems(stream(ev('ci', 1), ev('ci', 2, { milestoneIID: 6 })), { scope: 'project' })
  assert.deepEqual(shape(items), ['group:ns/p/5/712:1', 'group:ns/p/6/712:1'])
})

test('groupSummary counts runs, pushes, CI rounds and checkpoints and reads the last review', () => {
  assert.equal(
    groupSummary([
      ev('run.started', 1),
      ev('push', 2),
      ev('ci', 3),
      ev('review', 4, { data: { blocking: 0 } }),
      ev('run.started', 5),
      ev('ci', 6),
      ev('review', 7, { data: { blocking: 2 } }),
      ev('run.started', 8),
      ev('checkpoint', 9),
    ]),
    '#712 · 3 runs, 1 push, 2 CI rounds, review: 2 findings, 1 checkpoint',
  )
  assert.equal(groupSummary([ev('review', 1, { data: { blocking: 1 } }), ev('review', 2, { data: { blocking: 0 } })]), '#712 · review clean')
  assert.equal(groupSummary([ev('review', 1, { data: { blocking: 0, reviewErrors: 1 } })]), '#712 · review with errors')
  assert.equal(groupSummary([ev('run.finished', 1), ev('run.finished', 2)]), '#712 · 2 events')
  assert.equal(groupSummary([ev('ci', 1, { issueIID: undefined })]), '%5 · 1 CI round')
  assert.equal(groupSummary([ev('ci', 1)], 'p'), 'p · 1 CI round')
})

test('cockpitStreamQuery asks for all kinds of a scope, chat kinds only with the chat feature', () => {
  const kinds = (p) => p.get('kinds').split(',')
  const all = [...pilotStreamKinds, ...runtimeStreamKinds]
  const m = cockpitStreamQuery({ namespace: 'ns', name: 'p', milestoneIID: 5 })
  assert.equal(m.get('project'), 'ns/p')
  assert.equal(m.get('milestone'), '5')
  assert.equal(m.get('limit'), '50')
  assert.deepEqual(kinds(m).sort(), all.filter((k) => !k.startsWith('chat.')).sort())
  const chat = cockpitStreamQuery({ namespace: 'ns', name: 'p', milestoneIID: 5 }, { chat: true })
  assert.deepEqual(kinds(chat).sort(), [...all].sort())
  const p = cockpitStreamQuery({ namespace: 'ns', name: 'p' }, { q: '  refund ', before: 'c1' })
  assert.equal(p.get('milestone'), null)
  assert.equal(p.get('q'), 'refund')
  assert.equal(p.get('before'), 'c1')
  const everything = cockpitStreamQuery({}, { q: ' ', after: 'c2' })
  assert.equal(everything.get('project'), null)
  assert.equal(everything.get('q'), null)
  assert.equal(everything.get('after'), 'c2')
})

test('chatAnswer: only the complete pilot answer to the sent message', () => {
  const events = [
    { id: 12, kind: 'chat.agent', data: { replyTo: 11 } },
    { id: 10, kind: 'chat.agent', data: {} },
    { id: 9, kind: 'pilot.acted', data: { replyTo: 8 } },
    { id: 8, kind: 'chat.user', data: {} },
  ]
  // Another message's answer (12 answers 11) does not count, a reply still
  // being written (10) has no replyTo, and a history event is no answer.
  assert.equal(chatAnswer(events, 8), undefined)
  assert.equal(chatAnswer(events, 11), events[0])
  assert.equal(chatAnswer([{ id: 10, kind: 'chat.agent', data: { replyTo: 8 } }, ...events], 8)?.id, 10)
  assert.equal(chatAnswer([], 8), undefined)
})

test('cockpitItems shows the three delivery texts of pilot.acted as their own items, as EventCard renders them', () => {
  // The summaries reportDeliveries writes (describeDelivery in Go).
  const where = [
    "delivered to #712's running implementer at",
    "delivered to #712's implementer, the step restarted with it, at",
    "delivered with #712's next run at",
  ]
  const acted = where.map((w, i) => ev('pilot.acted', 2 + 2 * i, { summary: `Message from @alice ${w} 2026-11-24 14:05 UTC` }))
  const items = cockpitItems(stream(ev('run.started', 1), acted[0], ev('ci', 3), acted[1], ev('push', 5), acted[2], ev('run.finished', 7)))
  assert.deepEqual(shape(items), [
    'group:ns/p/5/712:1',
    'pilot.acted',
    'group:ns/p/5/712:1',
    'pilot.acted',
    'group:ns/p/5/712:1',
    'pilot.acted',
    'group:ns/p/5/712:1',
  ])
  const shown = items.filter((i) => i.type === 'event')
  const now = Date.parse('2026-11-24T15:00:00Z')
  const at = '2026-11-24T14:05:00.000Z'
  shown.forEach((item, i) => {
    assert.equal(item.event, acted[i])
    // EventCard prints the summary through LocalText: the text, then the UTC time as a local <time>.
    const parts = localTimes(item.event.summary, now)
    assert.deepEqual(parts, [
      { text: `Message from @alice ${where[i]} ` },
      { iso: at, label: localMinute(at, now), title: parts[1].title, written: '2026-11-24 14:05 UTC' },
    ])
    assert.equal(localTimesText(item.event.summary, now), `Message from @alice ${where[i]} ${localMinute(at, now)}`)
  })
})
