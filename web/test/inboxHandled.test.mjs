// Tests of src/inboxHandled.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { handledLabel, handledRoute, handledSubject, handledView } from '../src/inboxHandled.ts'

const row = (over = {}) => ({ namespace: 'team', project: 'shop', kind: 'prompt', title: 'Retry?', at: '2026-11-17T10:00:00Z', ...over })

test('handledView: only ?view=handled is Handled', () => {
  assert.equal(handledView('handled'), 'handled')
  assert.equal(handledView(undefined), 'open')
  assert.equal(handledView('other'), 'open')
  assert.equal(handledView(['handled']), 'open')
})

test('handledLabel: prompts use the prompt card status', () => {
  assert.deepEqual(handledLabel(row({ closedReason: 'answered', closedBy: 'owner', by: 'jo' })), { tone: 'success', text: 'Answered by jo' })
  assert.deepEqual(handledLabel(row({ closedReason: 'mainHealthy', closedBy: 'operator' })), {
    tone: 'neutral',
    text: 'Resolved on its own: main is green again',
  })
  assert.deepEqual(handledLabel(row({ closedReason: 'pauseOver', closedBy: 'pilot' })), { tone: 'progress', text: 'Handled by the pilot: the pause ended' })
  assert.deepEqual(handledLabel(row({ closedReason: 'replaced', replacedBy: 'Q14' })), { tone: 'neutral', text: 'Replaced by Q14' })
  assert.deepEqual(handledLabel(row({ closedReason: 'expired' })), { tone: 'neutral', text: 'Expired: nobody answered in time' })
})

test('handledLabel: a prompt closed without a recorded reason keeps the old texts', () => {
  assert.deepEqual(handledLabel(row({ by: 'jo' })), { tone: 'success', text: 'Answered by jo' })
  assert.equal(handledLabel(row()).text, 'Superseded: its cause resolved or a newer question replaced it')
})

test('handledLabel: actions name the pilot or the owner', () => {
  assert.deepEqual(handledLabel(row({ kind: 'acted' })), { tone: 'progress', text: 'Pilot' })
  assert.deepEqual(handledLabel(row({ kind: 'acted', by: 'ann' })), { tone: 'success', text: '@ann' })
})

test('handledSubject: the issue or the milestone', () => {
  assert.equal(handledSubject(row({ issueIID: 12 })), '#12')
  assert.equal(handledSubject(row()), 'Milestone')
})

test('handledRoute: the problem thread, else the prompt in the project Cockpit', () => {
  assert.deepEqual(handledRoute(row({ milestoneIID: 3, issueIID: 12, promptID: 'Q4' })), {
    name: 'problem',
    params: { namespace: 'team', name: 'shop', iid: '3', problem: 'issue-12' },
  })
  assert.equal(handledRoute(row({ kind: 'acted', milestoneIID: 3 })).params.problem, 'issue-0')
  assert.deepEqual(handledRoute(row({ promptID: 'Q4' })), { name: 'stream-project', params: { namespace: 'team', name: 'shop' }, query: { tab: 'cockpit' }, hash: '#prompt-Q4' })
})
