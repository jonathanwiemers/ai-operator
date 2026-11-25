// Tests of src/eventNotify.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { eventCategory, eventKey, eventNotifications, eventPoller, eventText } from '../src/eventNotify.ts'

const all = () => true
const only = (...cs) => (c) => cs.includes(c)

let nextID = 1
const ev = (kind, data = {}, over = {}) => ({
  id: nextID++,
  at: '2026-11-25T10:00:00Z',
  namespace: 'team',
  project: 'shop',
  milestoneIID: 3,
  kind,
  summary: `${kind} summary`,
  data: { title: 'Checkout', ...data },
  ...over,
})
const started = (over) => ev('milestone.started', { issues: 4 }, over)
const done = (over) => ev('autopilot', { phase: 'Done' }, over)
const released = (over) => ev('release.ended', { state: 'Released', version: '2026.11.4' }, over)
const failed = (over) => ev('release.ended', { state: 'Failed', reason: 'pipeline 77 failed' }, over)

test('eventNotifications keeps started, done, released and failed only', () => {
  const keep = [started(), done(), released(), failed()]
  const drop = [
    ev('autopilot', { phase: 'Running' }),
    ev('autopilot', { phase: 'Paused' }),
    ev('autopilot', { phase: 'Off' }),
    ev('release.ended', { state: 'AlreadyReleased' }),
    ev('release.ended', { state: 'Dropped' }),
    ev('release.started'),
    ev('ci.started', { sha: 'abc', pipelineID: 7 }),
    ev('comment'),
  ]
  assert.deepEqual(eventNotifications([drop[0], keep[0], ...drop.slice(1), ...keep.slice(1)]), keep)
  assert.deepEqual(keep.map(eventCategory), ['milestoneStarted', 'milestoneDone', 'release', 'release'])
  for (const e of drop) assert.equal(eventCategory(e), '', JSON.stringify(e.data))
})

test('eventText: the four texts', () => {
  assert.deepEqual(eventText(started()), { title: 'Milestone started · shop', body: 'Checkout' })
  assert.deepEqual(eventText(done()), { title: 'Milestone done · shop', body: 'Checkout' })
  assert.deepEqual(eventText(released()), { title: 'Released 2026.11.4 · shop', body: 'Checkout' })
  assert.deepEqual(eventText(failed()), { title: 'Release failed · shop', body: 'pipeline 77 failed\nCheckout' })
})

test('eventText: without a title the summary, without a version no version', () => {
  assert.deepEqual(eventText(ev('milestone.started', { title: '' }, { summary: 'Milestone planned: 4 issues' })), {
    title: 'Milestone started · shop',
    body: 'Milestone planned: 4 issues',
  })
  assert.equal(eventText(ev('release.ended', { state: 'Released' })).title, 'Released · shop')
  assert.equal(eventText(ev('release.ended', { state: 'Failed', title: '' })).body, '')
})

test('eventKey is event/<id>', () => {
  assert.equal(eventKey({ ...done(), id: 42 }), 'event/42')
})

test('poller: the first page seeds and notifies nothing, even with events', () => {
  const p = eventPoller()
  assert.equal(p.query().toString(), 'kinds=autopilot%2Cmilestone.started%2Crelease.ended')
  assert.deepEqual(p.step({ events: [started(), done()], newest: 'c1' }, all), [])
  assert.equal(p.query().get('after'), 'c1')
})

test('poller: an empty stream that gets its first event seeds, not notifies', () => {
  const p = eventPoller()
  assert.deepEqual(p.step({ events: [] }, all), [])
  assert.equal(p.query().get('after'), null)
  assert.deepEqual(p.step({ events: [released()], newest: 'c1' }, all), [])
  assert.equal(p.query().get('after'), 'c1')
  const next = done()
  assert.deepEqual(p.step({ events: [next], newest: 'c2' }, all), [next])
})

test('poller: later pages notify once per id and move the cursor', () => {
  const p = eventPoller()
  p.step({ events: [], newest: 'c1' }, all)
  const a = started()
  const b = released()
  const skip = ev('autopilot', { phase: 'Running' })
  assert.deepEqual(p.step({ events: [b, skip, a], newest: 'c2' }, all), [b, a])
  assert.equal(p.query().get('after'), 'c2')
  // The same event again (an overlapping page) does not notify twice; an
  // empty page keeps the cursor.
  assert.deepEqual(p.step({ events: [b], newest: 'c2' }, all), [])
  assert.deepEqual(p.step({ events: [] }, all), [])
  assert.equal(p.query().get('after'), 'c2')
})

test('poller: categories turned off do not notify', () => {
  const p = eventPoller()
  p.step({ events: [], newest: 'c1' }, all)
  const s = started()
  const d = done()
  const r = failed()
  assert.deepEqual(p.step({ events: [s, d, r], newest: 'c2' }, only('release')), [r])
  assert.deepEqual(p.step({ events: [ev('autopilot', { phase: 'Done' })], newest: 'c3' }, () => false), [])
})

test('poller: reset seeds again', () => {
  const p = eventPoller()
  p.step({ events: [], newest: 'c1' }, all)
  p.reset()
  assert.equal(p.query().get('after'), null)
  assert.deepEqual(p.step({ events: [done()], newest: 'c5' }, all), [])
  assert.equal(p.query().get('after'), 'c5')
})

// pollEvents (deskWorker.ts): the page side, with a stubbed stream and
// Notification (no service worker).
test('pollEvents: seeds, then shows wanted events by key, and passes on history', async (t) => {
  const { pollEvents, resetEvents } = await import('../src/deskWorker.ts')
  const shown = []
  globalThis.Notification = class {
    constructor(title, options) {
      shown.push({ title, ...options })
    }
  }
  t.after(() => delete globalThis.Notification)
  const pages = []
  const asked = []
  const history = []
  const source = (wants = all) => ({
    fetchPage: async (q) => (asked.push(q.get('after')), pages.shift()),
    wants,
    history: (on) => history.push(on),
  })
  const old = done()
  const fresh = released({ id: 501 })
  pages.push({ events: [old], newest: 'c1', history: true }, { events: [fresh, ev('autopilot', { phase: 'Paused' })], newest: 'c2', history: true })
  await pollEvents(source())
  assert.deepEqual(shown, [], 'events from before the tab opened')
  await pollEvents(source())
  assert.deepEqual(asked, [null, 'c1'])
  assert.deepEqual(history, [true, true])
  assert.equal(shown.length, 1)
  assert.equal(shown[0].title, 'Released 2026.11.4 · shop')
  assert.equal(shown[0].tag, 'event/501')

  // Turned off and on: seeds again.
  resetEvents()
  pages.push({ events: [done()], newest: 'c3', history: true })
  await pollEvents(source())
  assert.equal(shown.length, 1)

  // A failed fetch notifies nothing and keeps the cursor.
  await pollEvents({ ...source(), fetchPage: async () => Promise.reject(new Error('offline')) })
  pages.push({ events: [], history: false })
  await pollEvents(source())
  assert.deepEqual(asked.slice(-1), ['c3'])
  assert.equal(history.at(-1), false)
})
