// Tests of src/badgeTone.ts: npm test.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { badgeLook } from '../src/badgeTone.ts'

test('every tone has a look using its own tone tokens', () => {
  for (const tone of ['neutral', 'info', 'progress', 'success', 'warning', 'danger', 'muted']) {
    const look = badgeLook[tone]
    assert.ok(look, tone)
    assert.ok(look.class.includes(`--tone-${tone}-bg`), tone)
    assert.ok(look.class.includes(`--tone-${tone}-fg`), tone)
  }
})

test('danger is the kit\'s error colour', () => {
  assert.equal(badgeLook.danger.color, 'error')
})
