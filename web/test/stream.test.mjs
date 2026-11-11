import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  dayEndCursor,
  dayLabel,
  filterForKind,
  groupThreads,
  heatLevel,
  historyStreamKinds,
  pilotStreamKinds,
  runtimeStreamKinds,
  keepNoteTarget,
  kindsForFilter,
  noteDisabledReason,
  noteHint,
  notePlaceholder,
  noteTargetLabel,
  noteTargets,
  projectDot,
  projectNeedsYou,
  sameLocalDay,
  slashOpens,
  slashPick,
  streamEventKey,
} from '../src/stream.ts'

test('kindsForFilter maps chips to API kinds', () => {
  assert.equal(kindsForFilter('all'), undefined)
  assert.deepEqual(kindsForFilter('all', true), [...historyStreamKinds, 'chat.user', 'chat.agent'])
  assert.deepEqual(kindsForFilter('chat'), ['note', 'proposal', 'prompt.posted', 'prompt.answered', 'prompt.closed'])
  assert.deepEqual(kindsForFilter('runs'), ['run.started', 'run.finished', 'push', 'checkpoint'])
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

test('pilot and runtime kinds partition every StreamKind', () => {
  // Every StreamKind (web/src/types.ts). Add new ones here, and to
  // pilotStreamKinds or runtimeStreamKinds.
  const all = [
    'note', 'run.started', 'run.finished', 'push', 'ci', 'review', 'needs', 'merged', 'closed',
    'autopilot', 'proposal', 'autopilot.paused', 'autopilot.resumed', 'pilot.writeFailed',
    'retrospective', 'prompt.posted', 'prompt.answered', 'prompt.closed', 'chat.user', 'chat.agent', 'checkpoint',
    'release.decided', 'release.started', 'release.ended',
  ]
  for (const k of pilotStreamKinds) assert.ok(!runtimeStreamKinds.includes(k), `${k} is in both lists`)
  const union = [...pilotStreamKinds, ...runtimeStreamKinds]
  assert.equal(new Set(union).size, union.length)
  assert.deepEqual([...union].sort(), [...all].sort())
  assert.ok(!historyStreamKinds.includes('chat.user') && historyStreamKinds.includes('retrospective'))
})

test('the milestone Stream tab has runtime chips and kinds only', async () => {
  const { runtimeStreamFilters, runtimeKindsForFilter } = await import('../src/stream.ts')
  assert.deepEqual(runtimeStreamFilters.map((f) => f.id), ['all', 'runs', 'ci', 'merged'])
  assert.deepEqual(runtimeKindsForFilter('all'), [...runtimeStreamKinds])
  assert.deepEqual(runtimeKindsForFilter('runs'), ['run.started', 'run.finished', 'push', 'checkpoint'])
  assert.deepEqual(runtimeKindsForFilter('merged'), ['merged', 'closed'])
  // A pilot chip carried over from a project stream falls back to all.
  assert.deepEqual(runtimeKindsForFilter('chat'), [...runtimeStreamKinds])
  for (const f of ['all', 'runs', 'ci', 'merged', 'chat', 'needs']) {
    for (const k of runtimeKindsForFilter(f)) assert.ok(!pilotStreamKinds.includes(k), `${k} is a pilot kind`)
  }
})

// A project with an idle milestone (8), a running one (9), a stored one
// without tracking issue or spec (10), and tasks with and without an issue.
const noteProject = {
  milestones: [
    { iid: 8, title: 'Idle', trackingIssueIID: 80, tasks: [{ name: 't-a', issue: { iid: 1, title: 'A' } }] },
    {
      iid: 9,
      title: 'Running',
      spec: { status: { autopilot: { phase: 'Running' } } },
      tasks: [{ name: 't-b', issue: { iid: 2, title: 'B' } }, { name: 't-none' }],
    },
    { iid: 10, title: 'Stored', tasks: [] },
  ],
  unplanned: [{ name: 't-c', issue: { iid: 3, title: 'C' } }],
}

test('noteTargets: active pilots (the preferred one first), then tasks with an issue', () => {
  assert.deepEqual(noteTargets(null), [])
  const label = (ts) => ts.map((t) => (t.type === 'pilot' ? `%${t.milestoneIID}` : `#${t.issueIID}`))
  assert.deepEqual(label(noteTargets(noteProject)), ['%9', '#1', '#2', '#3'])
  assert.deepEqual(label(noteTargets(noteProject, 8)), ['%8', '%9', '#1', '#2', '#3'])
  assert.deepEqual(label(noteTargets(noteProject, 10)), ['%9', '#1', '#2', '#3'])
  assert.deepEqual(noteTargets(noteProject, 8)[0], { type: 'pilot', milestoneIID: 8, title: 'Idle', trackingIssueIID: 80 })
  assert.deepEqual(noteTargets(noteProject)[1], { type: 'task', task: 't-a', issueIID: 1, title: 'A' })
})

test('keepNoteTarget keeps an offered target, else takes the first', () => {
  const list = noteTargets(noteProject)
  assert.equal(keepNoteTarget([], list[0]), null)
  assert.equal(keepNoteTarget(list, null), list[0])
  const copy = { ...list[2] }
  assert.equal(keepNoteTarget(list, copy), copy)
  assert.equal(keepNoteTarget(list, { type: 'task', task: 'gone', issueIID: 99, title: 'x' }), list[0])
  assert.equal(keepNoteTarget(list, { type: 'pilot', milestoneIID: 8, title: 'Idle' }), list[0])
})

test('noteDisabledReason: loading, GitLab, roles, targets, in that order', () => {
  assert.equal(noteDisabledReason(null, true, 3), 'Loading…')
  assert.equal(noteDisabledReason({ enabled: true, connected: false, login: '/x' }, true, 3), 'Connect GitLab to post.')
  assert.equal(noteDisabledReason({ enabled: true, connected: false }, true, 3), 'GitLab is not set up for your user here.')
  assert.equal(noteDisabledReason({ enabled: true, connected: true }, false, 3), 'Your roles may not post comments on this project.')
  assert.equal(noteDisabledReason({ enabled: true, connected: true }, undefined, 0), 'No pilot or issue to post to yet.')
  assert.equal(noteDisabledReason({ enabled: true, connected: true }, undefined, 1), '')
})

test('noteHint, noteTargetLabel and notePlaceholder name the target', () => {
  const pilot = { type: 'pilot', milestoneIID: 9, title: 'Running' }
  const task = { type: 'task', task: 't-a', issueIID: 1, title: 'A' }
  assert.equal(noteHint(null), 'Pick where to post')
  assert.equal(noteHint(pilot), 'Posts on milestone %9 as /pilot … in your GitLab name')
  assert.equal(noteHint({ ...pilot, trackingIssueIID: 90 }), 'Posts on tracking issue #90 as /pilot … in your GitLab name')
  assert.equal(noteHint(task), 'Comment on #1; start with /agent to start a run')
  assert.equal(noteTargetLabel(null), '→ Pick target')
  assert.equal(noteTargetLabel(pilot), '→ Pilot · Running')
  assert.equal(noteTargetLabel(task), '→ #1 A')
  assert.equal(notePlaceholder(pilot), 'Message the pilot…  /  for commands')
  assert.equal(notePlaceholder(task), 'Comment…  /  for commands')
  assert.equal(notePlaceholder(null), 'Comment…  /  for commands')
})

test('slashOpens and slashPick', () => {
  for (const b of ['/', '/pil', 'yes ', 'yes P']) assert.equal(slashOpens(b), true, b)
  for (const b of ['', 'hi', '/pilot pause', 'yes P1', ' /x']) assert.equal(slashOpens(b), false, b)
  assert.equal(slashPick('/pilot pause'), '/pilot pause ')
  assert.equal(slashPick('/pilot skip #'), '/pilot skip #')
  assert.equal(slashPick('/agent revisit '), '/agent revisit ')
})
