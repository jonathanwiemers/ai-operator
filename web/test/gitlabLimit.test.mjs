// Tests of src/gitlabLimit.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { clockTime } from '../src/format.ts'
import { deferralTail, gitlabBadge, gitlabDeferred, gitlabHeader, gitlabLimitedLine, gitlabUsage } from '../src/gitlabLimit.ts'

const now = Date.parse('2026-11-27T07:30:00Z')
const at = (min) => new Date(now + min * 60_000).toISOString()

test('gitlabDeferred and gitlabBadge: only a gitlab deferral still ahead', () => {
  const d = { blockReason: 'gitlab', deferredUntil: at(12), deferrals: 1 }
  assert.equal(gitlabDeferred(d, now), true)
  assert.equal(gitlabBadge(d, now), `GitLab limit · ${clockTime(at(12), now)}`)
  assert.equal(gitlabDeferred({ ...d, deferredUntil: at(-1) }, now), false)
  assert.equal(gitlabDeferred({ ...d, blockReason: 'quota' }, now), false)
  assert.equal(gitlabDeferred({ blockReason: 'gitlab' }, now), false)
  assert.equal(gitlabBadge(undefined, now), null)
})

test('deferralTail: the time and the count, 1× without one', () => {
  assert.equal(deferralTail({ deferredUntil: at(12), deferrals: 2 }, now), `(at ${clockTime(at(12), now)}, deferred 2×).`)
  assert.equal(deferralTail({ deferredUntil: at(12) }, now), `(at ${clockTime(at(12), now)}, deferred 1×).`)
})

test('gitlabUsage: label and the reserve', () => {
  const r = { observed: 63, remaining: 37, limit: 100, resetAt: at(0.5) }
  assert.deepEqual(gitlabUsage(r, 20, now), { observed: 63, limit: 100, low: false, label: 'GitLab API 63/100 this minute' })
  assert.equal(gitlabUsage({ ...r, observed: 80, remaining: 20 }, 20, now).low, true)
  // Zero counts are omitted from the status.
  assert.deepEqual(gitlabUsage({ observed: 100, limit: 100, resetAt: at(0.5) }, 20, now), {
    observed: 100,
    limit: 100,
    low: true,
    label: 'GitLab API 100/100 this minute',
  })
})

test('gitlabUsage: hidden without a reading or once stale', () => {
  assert.equal(gitlabUsage(undefined, 20, now), null)
  assert.equal(gitlabUsage({ observed: 3 }, 20, now), null)
  assert.equal(gitlabUsage({ observed: 3, limit: 100 }, 20, now), null)
  assert.notEqual(gitlabUsage({ observed: 3, limit: 100, resetAt: at(-1) }, 20, now), null)
  assert.equal(gitlabUsage({ observed: 3, limit: 100, resetAt: at(-1.5) }, 20, now), null)
})

test('gitlabLimitedLine and gitlabHeader', () => {
  assert.equal(gitlabLimitedLine({ limitedUntil: at(6) }, now), `GitLab rate limit until ${clockTime(at(6), now)}`)
  assert.equal(gitlabLimitedLine({ limitedUntil: at(-1) }, now), null)
  assert.equal(gitlabHeader({ gitLabReserve: 20 }, now), null)
  assert.equal(gitlabHeader(null, now), null)
  const h = gitlabHeader({ gitLabReserve: 20, gitLab: { rateLimit: { limitedUntil: at(6) } } }, now)
  assert.equal(h.usage, null)
  assert.match(h.limited, /^GitLab rate limit until /)
})
