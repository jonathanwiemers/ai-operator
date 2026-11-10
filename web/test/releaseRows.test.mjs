// Tests of src/releaseRows.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  interleaveReleases,
  releaseAnchor,
  releaseBy,
  releaseHeadline,
  releaseMuted,
  releaseNote,
  releaseStateLabel,
  releaseTime,
  releaseTone,
} from '../src/releaseRows.ts'

const issue = (iid) => ({ iid, title: `t${iid}`, state: 'merged' })
const rel = (id, extra = {}) => ({ id, by: 'planner', state: 'Planned', issues: [], mergeRequests: [], ...extra })
const keys = (rows) => rows.map((r) => r.key)

test('interleaveReleases: after their afterIssue, else after the last issue they contain, else at the end', () => {
  const issues = [issue(1), issue(2), issue(3)]
  const releases = [
    rel(5, { by: 'final', issues: [] }), // planned final without contents: end
    rel(4, { by: 'owner', issues: [1, 2] }), // ends with #2
    rel(2, { afterIssue: 1 }),
    rel(3, { afterIssue: 1, state: 'Released', version: '2026.11.3' }),
    rel(6, { afterIssue: 99 }), // not an issue of the list: end
  ]
  assert.deepEqual(keys(interleaveReleases(issues, releases)), ['i1', 'r2', 'r3', 'i2', 'r4', 'i3', 'r5', 'r6'])
})

test('interleaveReleases: no releases, null releases, and no issues', () => {
  assert.deepEqual(keys(interleaveReleases([issue(1)], null)), ['i1'])
  assert.deepEqual(keys(interleaveReleases([], [rel(1, { afterIssue: 3 })])), ['r1'])
})

test('releaseAnchor: afterIssue wins over the contents', () => {
  assert.equal(releaseAnchor({ afterIssue: 2, issues: [1, 5] }), 2)
  assert.equal(releaseAnchor({ issues: [1, 5] }), 5)
  assert.equal(releaseAnchor({ issues: [] }), undefined)
})

test('releaseHeadline: planned after #N, running, the version once done', () => {
  assert.equal(releaseHeadline({ state: 'Planned', afterIssue: 4, issues: [] }), 'Release after #4')
  assert.equal(releaseHeadline({ state: 'Waiting', issues: [2, 3] }), 'Release after #3')
  assert.equal(releaseHeadline({ state: 'Planned', issues: [] }), 'Release at the end')
  assert.equal(releaseHeadline({ state: 'Running', issues: [] }), 'Releasing…')
  assert.equal(releaseHeadline({ state: 'Released', version: '2026.11.3', issues: [] }), '2026.11.3')
  assert.equal(releaseHeadline({ state: 'AlreadyReleased', version: '2026.11.4', issues: [] }), '2026.11.4')
  assert.equal(releaseHeadline({ state: 'Dropped', afterIssue: 4, issues: [] }), 'Release after #4 dropped')
  assert.equal(releaseHeadline({ state: 'Failed', issues: [] }), 'Release failed')
})

test('release tones, labels and muted rows', () => {
  assert.equal(releaseTone('Released'), 'success')
  assert.equal(releaseTone('Running'), 'progress')
  assert.equal(releaseTone('Failed'), 'warning')
  assert.equal(releaseTone('Dropped'), 'muted')
  assert.equal(releaseStateLabel('AlreadyReleased'), 'already released')
  assert.equal(releaseStateLabel('Planned'), 'planned')
  assert.ok(releaseMuted('Failed') && releaseMuted('NotNeeded') && !releaseMuted('Released'))
})

test('releaseBy and releaseTime', () => {
  assert.equal(releaseBy({ by: 'owner', actor: 'jo' }), 'owner jo')
  assert.equal(releaseBy({ by: 'final' }), 'final')
  assert.equal(releaseTime({ decidedAt: 'a', startedAt: 'b', endedAt: 'c' }), 'c')
  assert.equal(releaseTime({ decidedAt: 'a' }), 'a')
  assert.equal(releaseTime({}), undefined)
})

test('releaseNote: why and reason on one line, empty without either', () => {
  assert.equal(
    releaseNote({ why: 'users notice the new tab', reason: 'tag 2026.11.2 contains it' }),
    'users notice the new tab · tag 2026.11.2 contains it',
  )
  assert.equal(releaseNote({ reason: 'docs only' }), 'docs only')
  assert.equal(releaseNote({ why: ' ' }), '')
})
