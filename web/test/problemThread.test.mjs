import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  answered,
  localMessages,
  newestAt,
  noReaction,
  noReactionAfter,
  problemFromHash,
  problemParam,
  promptMessage,
  sameProblem,
  statusLine,
  withAnswer,
} from '../src/problemThread.ts'

const t0 = Date.parse('2026-11-20T10:00:00Z')
const local = (send, extra = {}) => ({ id: 'a', text: 'Retry', at: new Date(t0).toISOString(), sentAt: t0, send, ...extra })

test('problemParam is the row id, else issue-<iid>', () => {
  assert.equal(problemParam({ id: 7, issueIID: 42 }), '7')
  assert.equal(problemParam({ id: 0, issueIID: 42 }), 'issue-42')
  assert.equal(problemParam({ id: 0, issueIID: 0 }), 'issue-0')
})

test('sameProblem matches the row id and the issue form', () => {
  assert.equal(sameProblem('7', { id: 7, issueIID: 42 }), true)
  assert.equal(sameProblem('issue-42', { id: 7, issueIID: 42 }), true)
  assert.equal(sameProblem('issue-4', { id: 7, issueIID: 42 }), false)
  assert.equal(sameProblem('0', { id: 0, issueIID: 0 }), false)
})

test('old anchors name the problem of the issue or of the prompt', () => {
  const prompts = [{ id: 'Q3', issueIID: 42 }, { id: 'Q4' }]
  assert.equal(problemFromHash('#problem-42', prompts), 'issue-42')
  assert.equal(problemFromHash('#problem-0'), 'issue-0')
  assert.equal(problemFromHash('#prompt-Q3', prompts), 'issue-42')
  assert.equal(problemFromHash('#prompt-Q4', prompts), 'issue-0')
  assert.equal(problemFromHash('#prompt-Q9', prompts), undefined)
  assert.equal(problemFromHash('#live', prompts), undefined)
})

test('statusLine names each live status', () => {
  assert.equal(statusLine('waiting').text, 'Waiting for you')
  assert.equal(statusLine('resolved').tone, 'success')
  assert.equal(statusLine('other').text, 'other')
})

test('promptMessage shows question, chosen answer and who chose', () => {
  const m = promptMessage({
    id: 'Q3',
    question: 'CI is red. What now?',
    options: [{ id: 'retry', label: 'Retry' }],
    state: 'answered',
    answer: { optionID: 'retry', by: 'jo', at: '2026-11-20T10:00:00Z' },
    closedAt: '2026-11-20T10:00:00Z',
  })
  assert.equal(m.id, 'prompt:Q3')
  assert.match(m.parts[0].text, /CI is red/)
  assert.match(m.parts[0].text, /→ Retry \(@jo\)/)
})

test('a sent answer leaves once a newer event arrives', () => {
  assert.equal(answered(local('sent'), t0 + 1000), true)
  assert.equal(answered(local('sent'), t0 - 60_000), false)
  // Still on its way: stays whatever arrives.
  assert.equal(answered(local('sending'), t0 + 1000), false)
})

test('no reaction after 60 s without a new event', () => {
  assert.equal(noReaction(local('sent'), 0, t0 + noReactionAfter - 1), false)
  assert.equal(noReaction(local('sent'), 0, t0 + noReactionAfter), true)
  assert.equal(noReaction(local('sent'), t0 + 5000, t0 + 2 * noReactionAfter), false)
  assert.equal(noReaction(local('failed'), 0, t0 + 2 * noReactionAfter), false)
})

test('localMessages adds the note after the waiting answer', () => {
  const out = localMessages([local('sent')], 0, t0 + noReactionAfter)
  assert.deepEqual(out.map((m) => m.id), ['local:a', 'noreaction:a'])
  assert.equal(out[0].metadata.send, 'sent')
  assert.deepEqual(localMessages([local('sent')], t0 + 1, t0 + noReactionAfter), [])
  const failed = localMessages([local('failed', { error: 'forbidden' })], 0, t0)
  assert.equal(failed[0].metadata.error, 'forbidden')
})

test('newestAt is the latest event time', () => {
  assert.equal(newestAt([{ at: '2026-11-20T10:00:05Z' }, { at: '2026-11-20T10:00:01Z' }]), t0 + 5000)
  assert.equal(newestAt([]), 0)
})

test('withAnswer adds a new answer and moves a known one on', () => {
  let locals = withAnswer([], { id: 'a', label: 'Retry', send: 'sending', task: 'shop-pilot' }, t0)
  assert.equal(locals.length, 1)
  assert.equal(locals[0].task, 'shop-pilot')
  locals = withAnswer(locals, { id: 'a', label: 'Retry', send: 'sent' }, t0 + 3000)
  assert.equal(locals.length, 1)
  assert.equal(locals[0].send, 'sent')
  // Waiting for the operator counts from when the API answered.
  assert.equal(locals[0].sentAt, t0 + 3000)
  locals = withAnswer(locals, { id: 'b', label: 'Skip', send: 'failed', error: 'forbidden' }, t0 + 4000)
  assert.deepEqual(locals.map((a) => [a.id, a.send]), [['a', 'sent'], ['b', 'failed']])
})

test('the no-reaction note links the logs of the answered task', () => {
  const project = { namespace: 'team', name: 'shop' }
  const [, note] = localMessages([local('sent', { task: 'shop-pilot' })], 0, t0 + noReactionAfter, project)
  assert.deepEqual(note.metadata.link, {
    label: 'Task logs',
    to: { name: 'task', params: { namespace: 'team', name: 'shop', task: 'shop-pilot' }, query: { tab: 'logs' } },
  })
  const [, bare] = localMessages([local('sent')], 0, t0 + noReactionAfter, project)
  assert.equal(bare.metadata.link, undefined)
})
