// Tests of src/pauseCard.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  pauseActions,
  pauseCommand,
  pauseEndsBy,
  pauseTitle,
  reviewLimitLines,
  commandAnswer,
} from '../src/pauseCard.ts'

const ids = (kind) => pauseActions(kind).map((a) => a.id)
const targets = (kind) => pauseActions(kind).map((a) => a.target)

const allKinds = [
  'owner',
  'cycle',
  'stuck',
  'needsInput',
  'runFailed',
  'findings',
  'reviewErrors',
  'notReviewed',
  'ciRed',
  'ciCanceled',
  'mergeRequest',
  'mainRed',
  'config',
]

test('pauseActions: reviewer kinds offer review, merge anyway and skip', () => {
  for (const kind of ['reviewErrors', 'notReviewed']) {
    assert.deepEqual(ids(kind), ['review', 'merge', 'skip'], kind)
    assert.deepEqual(targets(kind), ['implement', 'spec', 'spec'], kind)
    const merge = pauseActions(kind)[1]
    assert.equal(merge.label, 'Merge anyway')
    assert.equal(merge.confirm, true)
    assert.equal(merge.ownerOnly, true)
    assert.equal(pauseActions(kind)[0].label, 'Review again')
  }
})

test('pauseActions: run kinds offer retry, the chat and skip', () => {
  for (const kind of ['findings', 'runFailed', 'needsInput']) {
    assert.deepEqual(ids(kind), ['retry', 'openChat', 'skip'], kind)
    assert.deepEqual(targets(kind), ['implement', 'link', 'spec'], kind)
  }
  assert.equal(pauseActions('findings', 12)[1].label, 'Open #12 chat')
  assert.equal(pauseActions('findings')[1].label, 'Open chat')
})

test('pauseActions: CI kinds offer retry, the pipeline and skip', () => {
  for (const kind of ['ciRed', 'ciCanceled']) {
    assert.deepEqual(ids(kind), ['retry', 'openPipeline', 'skip'], kind)
    assert.deepEqual(targets(kind), ['implement', 'link', 'spec'], kind)
  }
})

test('pauseActions: mergeRequest offers the MR, resume and skip', () => {
  assert.deepEqual(ids('mergeRequest'), ['openMR', 'resume', 'skip'])
  assert.deepEqual(targets('mergeRequest'), ['link', 'spec', 'spec'])
})

test('pauseActions: plan kinds offer the plan and skip', () => {
  for (const kind of ['cycle', 'stuck']) {
    assert.deepEqual(ids(kind), ['openPlan', 'skip'], kind)
    assert.deepEqual(targets(kind), ['link', 'spec'], kind)
  }
})

test('pauseActions: owner, mainRed and config offer resume only', () => {
  for (const kind of ['owner', 'mainRed', 'config']) {
    assert.deepEqual(ids(kind), ['resume'], kind)
    assert.deepEqual(targets(kind), ['spec'], kind)
  }
})

test('pauseActions: a kind-less or unknown pause offers resume only', () => {
  assert.deepEqual(ids(undefined), ['resume'])
  assert.deepEqual(ids('somethingNew'), ['resume'])
  assert.deepEqual(ids('toString'), ['resume'])
})

test('pauseActions: only merge anyway asks and is owner-only', () => {
  for (const kind of allKinds) {
    for (const a of pauseActions(kind)) {
      assert.equal(!!a.confirm, a.id === 'merge', `${kind} ${a.id}`)
      assert.equal(!!a.ownerOnly, a.id === 'merge', `${kind} ${a.id}`)
    }
  }
})

test('pauseActions: callers cannot change the table', () => {
  pauseActions('owner')[0].label = 'changed'
  assert.equal(pauseActions('owner')[0].label, 'Resume')
})

const action = (id) => ({ id, label: id, target: 'spec' })

test('pauseCommand: the commands of each action', () => {
  const ctx = { issue: 7, head: 'abcdef0123456789' }
  assert.deepEqual(pauseCommand(action('retry'), ctx), { cmd: 'retry' })
  assert.deepEqual(pauseCommand(action('review'), ctx), { cmd: 'review' })
  assert.deepEqual(pauseCommand(action('resume'), ctx), { cmd: 'resume' })
  assert.deepEqual(pauseCommand(action('skip'), ctx), { cmd: 'skip', issue: 7 })
  assert.deepEqual(pauseCommand(action('merge'), ctx), {
    cmd: 'merge',
    issue: 7,
    sha: 'abcdef0123456789',
  })
  assert.deepEqual(pauseCommand(action('resume'), {}), { cmd: 'resume' })
})

test('pauseCommand: null for links and missing issue or head', () => {
  const ctx = { issue: 7, head: 'abc' }
  for (const id of ['openChat', 'openPipeline', 'openMR', 'openPlan']) {
    assert.equal(pauseCommand(action(id), ctx), null, id)
  }
  assert.equal(pauseCommand(action('skip'), {}), null)
  assert.equal(pauseCommand(action('skip'), { issue: 0 }), null)
  assert.equal(pauseCommand(action('merge'), { head: 'abc' }), null)
  assert.equal(pauseCommand(action('merge'), { issue: 7 }), null)
  assert.equal(pauseCommand(action('merge'), { issue: 7, head: '  ' }), null)
})

test('pauseEndsBy: every kind', () => {
  const gate = 'Continues by itself once a later run of #5 passes the gate.'
  for (const kind of ['needsInput', 'runFailed', 'findings', 'notReviewed', 'ciRed', 'ciCanceled']) {
    assert.equal(pauseEndsBy(kind, 5), gate, kind)
  }
  assert.equal(
    pauseEndsBy('reviewErrors', 5),
    `${gate} After a transient reviewer failure one re-review runs by itself.`,
  )
  assert.equal(pauseEndsBy('mergeRequest', 5), 'Continues by itself when the MR is merged by hand.')
  assert.equal(pauseEndsBy('mainRed'), 'Continues by itself when main is green.')
  for (const kind of ['owner', 'cycle', 'stuck', 'config', undefined, 'somethingNew']) {
    assert.equal(pauseEndsBy(kind, 5), '', String(kind))
  }
  assert.equal(pauseEndsBy('ciRed'), 'Continues by itself once a later run passes the gate.')
})

test('pauseTitle: every kind has its own heading', () => {
  const seen = new Set()
  for (const kind of allKinds) {
    const t = pauseTitle(kind)
    assert.ok(t && t !== 'Autopilot paused', kind)
    seen.add(t)
  }
  assert.equal(seen.size, allKinds.length)
  assert.equal(pauseTitle('reviewErrors'), 'Reviewers could not run')
  assert.equal(pauseTitle('ciRed'), 'CI is red')
  assert.equal(pauseTitle('owner'), 'Paused by an owner')
  assert.equal(pauseTitle(undefined), 'Autopilot paused')
  assert.equal(pauseTitle('somethingNew'), 'Autopilot paused')
})

test('reviewLimitLines: usage limits with and without a reset', () => {
  assert.deepEqual(
    reviewLimitLines([
      { reviewer: 'bugbot', kind: 'usageLimit', engine: 'cursor', resetAt: '2026-11-13T12:00:00Z' },
    ]),
    ['Cursor is limited until 13 Nov'],
  )
  assert.deepEqual(
    reviewLimitLines([{ reviewer: 'security', kind: 'usageLimit', engine: 'claude' }]),
    ['Claude is limited'],
  )
  assert.deepEqual(
    reviewLimitLines([{ reviewer: 'security', kind: 'usageLimit', resetAt: 'garbage' }]),
    ['Security is limited'],
  )
})

test('reviewLimitLines: transient and other failures', () => {
  assert.deepEqual(
    reviewLimitLines([
      { reviewer: 'bugbot', kind: 'transient' },
      { reviewer: 'security', kind: 'other' },
    ]),
    ['bugbot failed transiently', 'security failed'],
  )
})

test('reviewLimitLines: one line per engine or reviewer', () => {
  assert.deepEqual(
    reviewLimitLines([
      { reviewer: 'bugbot', kind: 'usageLimit', engine: 'cursor', resetAt: '2026-11-13T12:00:00Z' },
      { reviewer: 'security', kind: 'usageLimit', engine: 'cursor', resetAt: '2026-11-14T12:00:00Z' },
      { reviewer: 'bugbot', kind: 'transient' },
      { reviewer: 'bugbot', kind: 'other' },
      { reviewer: 'security', kind: 'usageLimit', engine: 'cursor' },
    ]),
    ['Cursor is limited until 14 Nov', 'bugbot failed'],
  )
  assert.deepEqual(reviewLimitLines(undefined), [])
  assert.deepEqual(reviewLimitLines([]), [])
})

test('reviewLimitLines: a custom date format', () => {
  assert.deepEqual(
    reviewLimitLines(
      [{ reviewer: 'bugbot', kind: 'usageLimit', engine: 'cursor', resetAt: '2026-11-13T12:00:00Z' }],
      (d) => d.toISOString().slice(0, 10),
    ),
    ['Cursor is limited until 2026-11-13'],
  )
})

test('commandAnswer: pending, answered, redacted and failed', () => {
  const cmd = { id: 9, at: 't', role: 'user', kind: 'command', body: '{"cmd":"resume"}', state: 'delivered' }
  assert.deepEqual(commandAnswer(undefined, 9), { state: 'pending' })
  assert.deepEqual(commandAnswer([cmd], 9), { state: 'pending' })
  const reply = { id: 10, at: 't', role: 'system', kind: 'message', body: ' Resumed. ', replyTo: 9 }
  assert.deepEqual(commandAnswer([reply, cmd], 9), { state: 'answered', text: 'Resumed.', redacted: false })
  assert.deepEqual(commandAnswer([{ ...reply, body: undefined, redacted: true }, cmd], 9), {
    state: 'answered',
    text: '',
    redacted: true,
  })
  assert.deepEqual(commandAnswer([{ ...reply, replyTo: 8 }, cmd], 9), { state: 'pending' })
  assert.deepEqual(commandAnswer([{ ...cmd, state: 'failed', reason: 'no pod' }], 9), {
    state: 'failed',
    reason: 'no pod',
  })
  assert.deepEqual(commandAnswer([{ ...cmd, state: 'failed' }], 9), {
    state: 'failed',
    reason: 'The command failed.',
  })
})
