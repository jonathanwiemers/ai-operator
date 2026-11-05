import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  dayEndCursor,
  dayLabel,
  filterForKind,
  groupThreads,
  heatLevel,
  historyStreamKinds,
  kindsForFilter,
  projectDot,
  projectNeedsYou,
  sameLocalDay,
  streamEventKey,
} from '../src/stream.ts'

test('kindsForFilter maps chips to API kinds', () => {
  assert.equal(kindsForFilter('all'), undefined)
  assert.deepEqual(kindsForFilter('all', true), [...historyStreamKinds, 'chat.user', 'chat.agent'])
  assert.deepEqual(kindsForFilter('chat'), ['note', 'proposal', 'prompt.posted', 'prompt.answered', 'prompt.closed'])
  assert.deepEqual(kindsForFilter('runs'), ['run.started', 'run.finished', 'push'])
  assert.deepEqual(kindsForFilter('ci'), ['ci', 'review'])
  assert.deepEqual(kindsForFilter('merged'), ['merged', 'closed'])
  assert.deepEqual(kindsForFilter('needs'), ['needs'])
})

test('streamEventKey namespaces chat ids apart from history', () => {
  assert.equal(streamEventKey({ id: 1, kind: 'note' }), 'event:1')
  assert.equal(streamEventKey({ id: 1, kind: 'chat.user' }), 'chat:1')
  assert.equal(streamEventKey({ id: 1, kind: 'chat.agent' }), 'chat:1')
})

test('filterForKind reverse maps kinds', () => {
  assert.equal(filterForKind('note'), 'chat')
  assert.equal(filterForKind('proposal'), 'chat')
  assert.equal(filterForKind('run.finished'), 'runs')
  assert.equal(filterForKind('ci'), 'ci')
  assert.equal(filterForKind('merged'), 'merged')
  assert.equal(filterForKind('needs'), 'needs')
  assert.equal(filterForKind('autopilot'), null)
})

test('projectDot from stages', () => {
  assert.equal(projectDot({ working: 1 }), 'running')
  assert.equal(projectDot({ 'needs-input': 1 }), 'needs-you')
  assert.equal(projectDot({ failed: 2 }), 'needs-you')
  assert.equal(projectDot({ blocked: 1 }), 'needs-you')
  assert.equal(projectDot({ review: 3 }), 'idle')
  assert.equal(projectDot({}), 'idle')
  assert.equal(projectNeedsYou({ 'needs-input': 1, failed: 2, blocked: 1 }), 4)
})

test('dayLabel Today / Yesterday / weekday', () => {
  const now = Date.parse('2026-11-02T15:00:00')
  assert.match(dayLabel('2026-11-02T10:00:00', now), /^Today · /)
  assert.match(dayLabel('2026-11-01T10:00:00', now), /^Yesterday · /)
  assert.ok(!dayLabel('2026-10-30T10:00:00', now).startsWith('Today'))
  assert.ok(!dayLabel('2026-10-30T10:00:00', now).startsWith('Yesterday'))
})

test('sameLocalDay', () => {
  assert.equal(sameLocalDay('2026-11-02T01:00:00', '2026-11-02T23:00:00'), true)
  assert.equal(sameLocalDay('2026-11-02T01:00:00', '2026-11-01T23:00:00'), false)
})

test('dayEndCursor builds t: RFC3339 end of day', () => {
  const c = dayEndCursor('2026-11-02')
  assert.match(c, /^t:\d{4}-\d{2}-\d{2}T/)
  const iso = c.slice(2)
  const d = new Date(iso)
  assert.equal(d.getHours(), 23)
  assert.equal(d.getMinutes(), 59)
})

test('heatLevel buckets', () => {
  assert.equal(heatLevel(0, 10), 0)
  assert.equal(heatLevel(1, 10), 1)
  assert.equal(heatLevel(3, 10), 2)
  assert.equal(heatLevel(8, 10), 3)
})

test('groupThreads collapses notes on same issue at newest', () => {
  const events = [
    { id: 3, at: '2026-11-02T12:00:00Z', namespace: 'ns', project: 'p', issueIID: 10, kind: 'note', summary: 'c', data: { body: 'c' } },
    { id: 2, at: '2026-11-02T11:00:00Z', namespace: 'ns', project: 'p', kind: 'ci', summary: 'ci', data: {} },
    { id: 1, at: '2026-11-02T10:00:00Z', namespace: 'ns', project: 'p', issueIID: 10, kind: 'note', summary: 'a', data: { body: 'a' } },
  ]
  const items = groupThreads(events)
  assert.equal(items.length, 2)
  assert.equal(items[0].kind, 'thread')
  assert.equal(items[0].notes.length, 2)
  assert.equal(items[0].notes[0].id, 1)
  assert.equal(items[0].notes[1].id, 3)
  assert.equal(items[1].kind, 'event')
  assert.equal(items[1].event.kind, 'ci')
})

test('groupThreads keeps separate issues apart', () => {
  const events = [
    { id: 2, at: '2026-11-02T12:00:00Z', namespace: 'ns', project: 'p', issueIID: 2, kind: 'note', summary: 'b', data: {} },
    { id: 1, at: '2026-11-02T11:00:00Z', namespace: 'ns', project: 'p', issueIID: 1, kind: 'note', summary: 'a', data: {} },
  ]
  const items = groupThreads(events)
  assert.equal(items.length, 2)
  assert.equal(items[0].notes[0].issueIID, 2)
  assert.equal(items[1].notes[0].issueIID, 1)
})

test('prompt kinds are history kinds and show under the Chat chip', async () => {
  const { promptStreamKinds } = await import('../src/stream.ts')
  assert.deepEqual(promptStreamKinds, ['prompt.posted', 'prompt.answered', 'prompt.closed'])
  for (const k of promptStreamKinds) {
    assert.ok(historyStreamKinds.includes(k))
    assert.equal(filterForKind(k), 'chat')
  }
})

test('mirrored prompt notes are dropped from threads', async () => {
  const { isMirroredPrompt } = await import('../src/stream.ts')
  const note = (id, body) => ({
    id, at: '2026-01-01T10:0' + id + ':00Z', namespace: 'n', project: 'p', issueIID: 5,
    kind: 'note', summary: '', data: { body, bot: true, noteable: 'issue' },
  })
  const mirror = note(1, 'Question?\n<!-- ai-operator:prompt=Q3 posted=1700000000 -->')
  const reply = note(2, 'Answered\n<!-- ai-operator:prompt-answer=Q3 posted=1700000000 -->')
  const human = note(3, 'looks fine')
  assert.ok(isMirroredPrompt(mirror))
  assert.ok(isMirroredPrompt(reply))
  assert.ok(!isMirroredPrompt(human))
  assert.ok(!isMirroredPrompt({ kind: 'push', data: { body: mirror.data.body } }))
  const items = groupThreads([mirror, reply, human])
  assert.equal(items.length, 1)
  assert.equal(items[0].kind, 'thread')
  assert.deepEqual(items[0].notes.map((n) => n.id), [3])
  assert.deepEqual(groupThreads([mirror]), [])
})

test('promptForEvent finds the prompt by id, else falls back to the summary', async () => {
  const { promptForEvent } = await import('../src/stream.ts')
  const prompts = [{ id: 'Q1' }, { id: 'Q2' }]
  const posted = (id) => ({ kind: 'prompt.posted', data: { promptID: id } })
  assert.equal(promptForEvent(posted('Q2'), prompts), prompts[1])
  assert.equal(promptForEvent(posted('Q9'), prompts), undefined)
  assert.equal(promptForEvent(posted('Q1'), undefined), undefined)
  assert.equal(promptForEvent({ kind: 'prompt.answered', data: { promptID: 'Q1' } }, prompts), undefined)
})
