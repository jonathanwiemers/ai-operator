// Tests of src/releaseEvents.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  isReleaseEvent,
  releaseEventBadge,
  releaseEventIssues,
  releaseEventText,
  releaseEventTitle,
  releaseWho,
} from '../src/releaseEvents.ts'

const ev = (kind, data = {}, actor) => ({ kind, data, actor })

test('release.decided: who, after which issue, and the why as text', () => {
  const e = ev('release.decided', { by: 'planner', afterIssue: 4, why: ' users see the new tab ' })
  assert.equal(releaseEventTitle(e), 'Release decided by planner after #4')
  assert.equal(releaseEventText(e), 'users see the new tab')
  assert.equal(releaseEventBadge(e), undefined)
  assert.equal(releaseEventTitle(ev('release.decided', { by: 'owner' }, '@jo')), 'Release decided by owner @jo')
  assert.equal(
    releaseEventTitle(ev('release.decided', { by: 'final', final: true, afterIssue: 9 })),
    'Release decided by the operator of the milestone',
  )
  assert.equal(releaseWho(ev('release.decided', { by: 'sweep' })), 'the operator')
  assert.equal(releaseWho(ev('release.decided', {})), 'someone')
})

test('release.started: the issues it contains', () => {
  const e = ev('release.started', { by: 'pilot', afterIssue: 2, issues: [1, 2], pipelineURL: 'https://x/p/7' })
  assert.equal(releaseEventTitle(e), 'Release after #2 started')
  assert.equal(releaseEventIssues(e), '#1, #2')
  assert.equal(releaseEventText(e), '')
  assert.equal(releaseEventIssues(ev('release.started', {})), '')
})

test('release.ended: by state, with version or reason', () => {
  const released = ev('release.ended', { state: 'Released', version: '2026.11.3' })
  assert.equal(releaseEventTitle(released), 'Released 2026.11.3')
  assert.deepEqual(releaseEventBadge(released), { label: 'released', tone: 'success' })
  assert.equal(releaseEventTitle(ev('release.ended', { state: 'AlreadyReleased', version: '2026.11.2' })), 'Already released in 2026.11.2')
  const failed = ev('release.ended', { state: 'Failed', final: true, reason: 'pipeline 7 failed' })
  assert.equal(releaseEventTitle(failed), 'Release of the milestone failed')
  assert.equal(releaseEventText(failed), 'pipeline 7 failed')
  assert.deepEqual(releaseEventBadge(failed), { label: 'failed', tone: 'warning' })
  assert.equal(releaseEventTitle(ev('release.ended', { state: 'NotNeeded', final: true })), 'Final release not needed')
  assert.equal(releaseEventTitle(ev('release.ended', { state: 'NotApplicable' })), 'No release: the project does not tag')
  assert.equal(releaseEventTitle(ev('release.ended', { state: 'Dropped', afterIssue: 3 })), 'Release after #3 dropped')
  assert.equal(releaseEventTitle(ev('release.ended', { state: 'Odd' })), 'Release ended')
})

test('isReleaseEvent', () => {
  assert.ok(isReleaseEvent({ kind: 'release.ended' }))
  assert.ok(!isReleaseEvent({ kind: 'merged' }))
})
