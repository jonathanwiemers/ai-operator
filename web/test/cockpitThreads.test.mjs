import assert from 'node:assert/strict'
import { test } from 'node:test'
import { cockpitThreads } from '../src/stream.ts'

let id = 0
// ev builds an event at minute m of a day; the defaults are #712 in %5 of ns/p.
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
const stream = (...events) => [...events].sort((a, b) => Date.parse(b.at) - Date.parse(a.at) || b.id - a.id)
const shape = (items) =>
  items.map((i) => (i.type === 'group' ? `group:${i.events.length}` : i.type === 'thread' ? `thread:${i.notes.length}` : i.event.kind))
const keys = (r) => r.threads.map((t) => t.key)
const thread = (r, key) => r.threads.find((t) => t.key === key)
const noIssue = { issueIID: undefined }

test('cockpitThreads sorts threads by their latest event, newest first', () => {
  const r = cockpitThreads(
    stream(ev('merged', 1, { issueIID: 713 }), ev('pilot.acted', 2), ev('ci', 3, { issueIID: 714 }), ev('closed', 4, { issueIID: 713 })),
  )
  assert.deepEqual(keys(r), ['ns/p/5/713', 'ns/p/5/714', 'ns/p/5/712'])
  const t = thread(r, 'ns/p/5/713')
  assert.equal(t.at, '2026-11-24T10:04:00Z')
  assert.equal(t.namespace, 'ns')
  assert.equal(t.project, 'p')
  assert.equal(t.milestoneIID, 5)
  assert.equal(t.issueIID, 713)
  assert.deepEqual(shape(t.items), ['closed', 'merged'])
  assert.deepEqual(r.conversation, [])
})

test('cockpitThreads folds runs, CI and reviews into groups inside a thread, newest item first', () => {
  const r = cockpitThreads(
    stream(
      ev('run.started', 1),
      ev('ci', 2),
      ev('review', 3, { data: { blocking: 2 } }),
      ev('pilot.acted', 4),
      ev('run.started', 5),
      ev('ci', 6),
      ev('review', 7, { data: { blocking: 0 } }),
      ev('merged', 8),
    ),
  )
  assert.deepEqual(keys(r), ['ns/p/5/712'])
  const { items } = r.threads[0]
  assert.deepEqual(shape(items), ['merged', 'group:3', 'pilot.acted', 'group:3'])
  // Inside a group events stay oldest first, so from–to and the summary read as today.
  const [, newer, , older] = items
  assert.deepEqual(
    newer.events.map((e) => e.kind),
    ['run.started', 'ci', 'review'],
  )
  assert.equal(newer.from, '2026-11-24T10:05:00Z')
  assert.equal(newer.to, '2026-11-24T10:07:00Z')
  assert.equal(newer.summary, '#712 · 1 run, 1 CI round, review clean')
  assert.equal(older.summary, '#712 · 1 run, 1 CI round, review: 2 findings')
})

test('cockpitThreads keeps note threads inside their issue thread', () => {
  const note = (m) => ev('note', m, { data: { noteable: 'issue' } })
  const r = cockpitThreads(stream(note(1), ev('run.started', 2), note(3)))
  assert.deepEqual(shape(r.threads[0].items), ['thread:2', 'group:1'])
})

test('cockpitThreads puts events without an issue into the Milestone thread', () => {
  const r = cockpitThreads(
    stream(
      ev('milestone.started', 1, noIssue),
      ev('merged', 2),
      ev('autopilot.paused', 3, noIssue),
      ev('note', 4, { ...noIssue, data: { noteable: 'milestone' } }),
      ev('release.ended', 5, noIssue),
      ev('retrospective', 6, noIssue),
    ),
  )
  assert.deepEqual(keys(r), ['ns/p/5/', 'ns/p/5/712'])
  const m = r.threads[0]
  assert.equal(m.milestoneIID, 5)
  assert.equal('issueIID' in m, false)
  assert.deepEqual(shape(m.items), ['retrospective', 'release.ended', 'note', 'autopilot.paused', 'milestone.started'])
})

test('cockpitThreads puts pilot.acted into its issue thread, else the Milestone thread', () => {
  const r = cockpitThreads(stream(ev('pilot.acted', 1), ev('pilot.acted', 2, { issueIID: 713 }), ev('pilot.acted', 3, noIssue)))
  assert.deepEqual(keys(r), ['ns/p/5/', 'ns/p/5/713', 'ns/p/5/712'])
  for (const t of r.threads) assert.deepEqual(shape(t.items), ['pilot.acted'])
  assert.equal(r.threads[1].items[0].event.issueIID, 713)
})

test('cockpitThreads keeps pinned kinds as shown items of their thread and drops mirrored prompt notes', () => {
  const mirrored = ev('note', 3, { data: { noteable: 'issue', body: '<!-- ai-operator:prompt=Q1 -->' } })
  const r = cockpitThreads(stream(ev('ci', 1), ev('problem.opened', 2), mirrored, ev('decision.recorded', 4, { issueIID: 713 })))
  assert.deepEqual(keys(r), ['ns/p/5/713', 'ns/p/5/712'])
  assert.deepEqual(shape(r.threads[1].items), ['problem.opened', 'group:1'])
})

test('cockpitThreads pairs the pilot conversation by replyTo, newest exchange first, never in threads', () => {
  const chat = { milestoneIID: 5, issueIID: undefined }
  const orphan = ev('chat.agent', 1, { ...chat, data: { replyTo: 999 } })
  const ask = ev('chat.user', 2, chat)
  const answer = ev('chat.agent', 4, { ...chat, data: { replyTo: ask.id } })
  const followUp = ev('chat.agent', 6, { ...chat, data: { replyTo: ask.id } })
  const ask2 = ev('chat.user', 5, chat)
  const answer2 = ev('chat.agent', 7, { ...chat, data: { replyTo: ask2.id } })
  const streaming = ev('chat.agent', 8, chat)
  const r = cockpitThreads(stream(orphan, ask, ev('merged', 3), answer, ask2, followUp, answer2, streaming))
  assert.deepEqual(keys(r), ['ns/p/5/712'])
  assert.deepEqual(
    r.conversation.map((x) => [x.message?.id, x.answers.map((a) => a.id), x.at]),
    [
      [undefined, [streaming.id], streaming.at],
      [ask2.id, [answer2.id], answer2.at],
      [ask.id, [answer.id, followUp.id], followUp.at],
      [undefined, [orphan.id], orphan.at],
    ],
  )
  assert.equal(r.conversation[2].key, `chat:${ask.id}`)
  assert.equal(r.conversation[0].key, `chat:${streaming.id}`)
})

test('cockpitThreads joins an answer with its message once the older page brings it', () => {
  const ask = ev('chat.user', 1, { issueIID: undefined })
  const answer = ev('chat.agent', 2, { issueIID: undefined, data: { replyTo: ask.id } })
  assert.deepEqual(
    cockpitThreads([answer]).conversation.map((x) => x.message),
    [undefined],
  )
  const r = cockpitThreads([answer, ask])
  assert.equal(r.conversation.length, 1)
  assert.equal(r.conversation[0].message, ask)
  assert.deepEqual(r.conversation[0].answers, [answer])
})

test('cockpitThreads with scope project keys threads by milestone and issue', () => {
  const r = cockpitThreads(stream(ev('merged', 1), ev('merged', 2, { milestoneIID: 6 }), ev('autopilot', 3, { milestoneIID: 6, ...noIssue })), {
    scope: 'project',
  })
  assert.deepEqual(keys(r), ['ns/p/6/', 'ns/p/6/712', 'ns/p/5/712'])
})

test('cockpitThreads with scope all keys threads per project and shows only merges and milestone state', () => {
  const q = { project: 'q' }
  const r = cockpitThreads(
    stream(
      ev('run.started', 1),
      ev('pilot.acted', 2),
      ev('merged', 3, q),
      ev('ci', 4, q),
      ev('problem.opened', 5, { ...q, issueIID: 9 }),
      ev('autopilot.paused', 6, noIssue),
      ev('note', 7, { data: { noteable: 'issue' } }),
    ),
    { scope: 'all' },
  )
  assert.deepEqual(keys(r), ['ns/p', 'ns/q'])
  const [p, qt] = r.threads
  assert.equal('milestoneIID' in p, false)
  assert.equal('issueIID' in p, false)
  assert.equal(p.at, '2026-11-24T10:07:00Z')
  assert.deepEqual(shape(p.items), ['group:1', 'autopilot.paused', 'group:2'])
  assert.equal(p.items[2].summary, 'p · 1 run')
  assert.deepEqual(shape(qt.items), ['group:2', 'merged'])
  assert.equal(qt.items[0].summary, 'q · 1 CI round')
})

test('cockpitThreads: an older page adds items below a thread\'s newer ones and threads below the others', () => {
  const newer = stream(ev('merged', 20), ev('pilot.acted', 21, { issueIID: 713 }), ev('closed', 22))
  const older = stream(ev('pilot.acted', 10), ev('ci', 11), ev('merged', 12, { issueIID: 714 }))
  const first = cockpitThreads(newer)
  const both = cockpitThreads([...newer, ...older])
  assert.deepEqual(keys(first), ['ns/p/5/712', 'ns/p/5/713'])
  assert.deepEqual(keys(both), ['ns/p/5/712', 'ns/p/5/713', 'ns/p/5/714'])
  const before = first.threads[0].items
  const after = both.threads[0].items
  assert.deepEqual(shape(after), ['closed', 'merged', 'group:1', 'pilot.acted'])
  assert.deepEqual(after.slice(0, before.length), before)
  assert.equal(both.threads[0].at, first.threads[0].at)
})
