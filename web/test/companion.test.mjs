// Tests of src/companion.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { companionLabel, companionOf, companionRef, companionTitle, companionTone } from '../src/companion.ts'

const view = (over = {}) => ({ homeIssueIID: 12, project: 'group/infra', mrIID: 7, chip: 'draft', ...over })

test('companionTone: each chip has its tone, blocked and closed override it', () => {
  const tones = Object.fromEntries(
    ['draft', 'waiting for release', 'ready', 'merging', 'merged'].map((chip) => [chip, companionTone(view({ chip }))]),
  )
  assert.deepEqual(tones, { draft: 'muted', 'waiting for release': 'info', ready: 'warning', merging: 'progress', merged: 'success' })
  assert.equal(companionTone(view({ chip: 'ready', state: 'blocked' })), 'danger')
  assert.equal(companionTone(view({ chip: 'waiting for release', state: 'closed' })), 'muted')
  assert.equal(companionTone(view({ chip: 'somethingNew' })), 'neutral')
})

test('companionLabel: the chip, and blocked or closed after it', () => {
  assert.equal(companionLabel(view({ chip: 'waiting for release', state: 'waitingCI' })), 'waiting for release')
  assert.equal(companionLabel(view({ chip: 'merging', state: 'blocked' })), 'merging · blocked')
  assert.equal(companionLabel(view({ chip: 'ready', state: 'closed' })), 'ready · closed')
  assert.equal(companionLabel(view({ chip: '' })), 'companion')
})

test('companionRef and companionTitle: the merge request, the reason, the release once', () => {
  assert.equal(companionRef(view()), 'group/infra!7')
  assert.equal(companionRef(view({ mrIID: undefined })), 'group/infra')
  assert.equal(companionTitle(view()), 'Companion group/infra!7')
  assert.equal(companionTitle(view({ reason: ' waits for the release ' })), 'Companion group/infra!7 · waits for the release')
  assert.equal(companionTitle(view({ release: '2026.11.4' })), 'Companion group/infra!7 · release 2026.11.4')
  assert.equal(companionTitle(view({ reason: 'release 2026.11.4 runs', release: '2026.11.4' })), 'Companion group/infra!7 · release 2026.11.4 runs')
})

test('companionOf: the issue companion, none without an IID or a match', () => {
  const list = [view(), view({ homeIssueIID: 14, project: 'group/other', mrIID: 2 })]
  assert.equal(companionOf(list, 14)?.project, 'group/other')
  assert.equal(companionOf(list, 13), undefined)
  assert.equal(companionOf(list, 0), undefined)
  assert.equal(companionOf(undefined, 12), undefined)
})
