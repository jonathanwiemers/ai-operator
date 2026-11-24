import assert from 'node:assert/strict'
import { test } from 'node:test'
import { cockpitClass, cockpitItems, groupSummary, pilotStreamKinds, runtimeStreamKinds } from '../src/stream.ts'

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
