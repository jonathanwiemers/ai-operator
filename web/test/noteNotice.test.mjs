// Tests of src/noteNotice.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { noteNotice } from '../src/noteNotice.ts'

test('a posted note says nothing, routed Desk commands are named', () => {
  assert.equal(noteNotice({ id: 1, author: 'ada', body: 'hi', createdAt: '' }), '')
  assert.equal(noteNotice(undefined), '')
  assert.equal(noteNotice({ routed: [] }), '')
  assert.equal(noteNotice({ routed: ['retry'] }), 'Sent retry as a Desk command, not posted to GitLab. The operator answers in the chat.')
  assert.equal(noteNotice({ routed: ['resume', 'retry'] }), 'Sent resume, retry as Desk commands, not posted to GitLab. The operator answers in the chat.')
})
