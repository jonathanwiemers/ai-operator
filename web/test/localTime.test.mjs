// Tests of src/localTime.ts: npm test (node --test, no extra dependencies).
// Labels and tooltips are in the locale of the run, so the expected values are
// built from the same Intl parts; the zone is fixed to Europe/Berlin.
process.env.TZ = 'Europe/Berlin'

import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { fullTime } from '../src/format.ts'
import { localMinute, localTimes, localTimesText } from '../src/localTime.ts'
import { localTimeNodes } from '../src/localTimeNodes.ts'

// Saturday, 21 Nov 2026, 14:00 CET.
const now = Date.parse('2026-11-21T13:00:00Z')

const fmt = (iso, o) => new Intl.DateTimeFormat(undefined, o).format(new Date(iso))
const hm = (iso) => fmt(iso, { hour: '2-digit', minute: '2-digit' })
const wd = (iso) => fmt(iso, { weekday: 'short' })

test('the zone of the run is Europe/Berlin', () => {
  assert.equal(new Date('2026-11-21T14:05:00Z').getHours(), 15)
})

test('localMinute: the time alone on the local day of now', () => {
  assert.equal(localMinute('2026-11-21T14:05:00.000Z', now), hm('2026-11-21T14:05:00Z'))
  // 23:30 UTC is 00:30 on Sunday in Berlin: not today, though the UTC date is.
  assert.equal(localMinute('2026-11-21T23:30:00.000Z', now), `${wd('2026-11-22T00:00:00+01:00')} ${hm('2026-11-21T23:30:00Z')}`)
})

test('localMinute: the weekday within 6 local days before or after now', () => {
  const ahead = '2026-11-27T08:00:00.000Z' // Friday, 6 days on
  const behind = '2026-11-15T09:00:00.000Z' // Sunday, 6 days back
  assert.equal(localMinute(ahead, now), `${wd(ahead)} ${hm(ahead)}`)
  assert.equal(localMinute(behind, now), `${wd(behind)} ${hm(behind)}`)
})

test('localMinute: the date in the same year, with the year in another', () => {
  const sameYear = '2026-11-13T14:05:00.000Z' // 8 days back
  const nextYear = '2027-11-13T14:05:00.000Z'
  assert.equal(localMinute(sameYear, now), `${fmt(sameYear, { day: 'numeric', month: 'short' })}, ${hm(sameYear)}`)
  assert.equal(localMinute(nextYear, now), `${fmt(nextYear, { day: 'numeric', month: 'short', year: 'numeric' })}, ${hm(nextYear)}`)
  assert.match(localMinute(nextYear, now), /2027/)
  assert.doesNotMatch(localMinute(sameYear, now), /2026/)
})

test('localMinute: calendar days across a DST change, not 24-hour steps', () => {
  // Saturday 28 Mar 2026, 23:30 CET; clocks go to CEST on Sunday 29 Mar.
  const before = Date.parse('2026-03-28T22:30:00Z')
  // Exactly 6 × 24 hours later is 00:30 CEST on Saturday 4 Apr: 7 calendar days.
  const week = '2026-04-03T22:30:00.000Z'
  assert.equal(localMinute(week, before), `${fmt(week, { day: 'numeric', month: 'short' })}, ${hm(week)}`)
  // After the change, 23:30 CEST on Sunday is still the local day of 01:30 CET.
  const early = Date.parse('2026-03-29T00:30:00Z')
  assert.equal(localMinute('2026-03-29T21:30:00.000Z', early), hm('2026-03-29T21:30:00Z'))
})

test('localMinute is empty for an invalid time', () => {
  assert.equal(localMinute('not a time', now), '')
  assert.equal(localMinute('', now), '')
})

test('localTimes: a UTC time with the local label and the tooltip', () => {
  const parts = localTimes('resets 2026-11-21 14:05 UTC.', now)
  assert.deepEqual(parts, [
    { text: 'resets ' },
    {
      iso: '2026-11-21T14:05:00.000Z',
      label: hm('2026-11-21T14:05:00Z'),
      title: `${fullTime('2026-11-21T14:05:00Z')} · 2026-11-21 14:05 UTC as written`,
      written: '2026-11-21 14:05 UTC',
    },
    { text: '.' },
  ])
  assert.match(parts[1].title, /21, 2026|21 Nov 2026/)
  assert.match(parts[1].title, /GMT\+1|CET/)
})

test('localTimes: the seconds form, labelled at minute precision', () => {
  const [p] = localTimes('2026-11-21 14:05:42 UTC', now)
  assert.equal(p.iso, '2026-11-21T14:05:42.000Z')
  assert.equal(p.label, hm('2026-11-21T14:05:00Z'))
  assert.equal(p.written, '2026-11-21 14:05:42 UTC')
  assert.match(p.title, / · 2026-11-21 14:05:42 UTC as written$/)
})

test('localTimes leaves other forms alone', () => {
  for (const s of [
    'resets 14:05 UTC',
    'at 2026-11-21 14:05 CET',
    'at 2026-11-21 14:05 GMT',
    'at 2026-11-21T14:05:00Z',
    'at 2026-11-21 14:05',
    'id x2026-11-21 14:05 UTC',
    'id 12026-11-21 14:05 UTC',
    'id _2026-11-21 14:05 UTC',
    '2026-11-21 14:05 UTCx',
    '2026-11-21 14:05 UTC_',
  ]) {
    assert.deepEqual(localTimes(s, now), [{ text: s }], s)
  }
})

test('localTimes leaves invalid dates and times alone', () => {
  for (const s of ['2026-13-01 14:05 UTC', '2026-02-30 10:00 UTC', '2026-11-21 24:00 UTC', '2026-11-21 14:60 UTC', '2026-11-21 14:05:60 UTC']) {
    assert.deepEqual(localTimes(s, now), [{ text: s }], s)
  }
})

test('localTimes: several times, with the text between them kept', () => {
  const parts = localTimes('(2026-11-21 14:05 UTC–2026-11-22 09:00:30 UTC) and 2026-13-01 10:00 UTC', now)
  assert.deepEqual(
    parts.map((p) => p.text ?? p.iso),
    ['(', '2026-11-21T14:05:00.000Z', '–', '2026-11-22T09:00:30.000Z', ') and 2026-13-01 10:00 UTC'],
  )
  assert.deepEqual(localTimes('', now), [])
})

test('localTimesText replaces each time by its label', () => {
  assert.equal(
    localTimesText('claude usage limit, resets 2026-11-21 14:05 UTC; again 2026-11-24 08:00 UTC', now),
    `claude usage limit, resets ${hm('2026-11-21T14:05:00Z')}; again ${wd('2026-11-24T08:00:00Z')} ${hm('2026-11-24T08:00:00Z')}`,
  )
  assert.equal(localTimesText('no time here', now), 'no time here')
})

test('localTimeNodes renders a <time> with datetime and title', async () => {
  const html = await renderToString(createSSRApp({ render: () => h('p', localTimeNodes('until 2026-11-21 14:05 UTC', now)) }))
  const title = `${fullTime('2026-11-21T14:05:00Z')} · 2026-11-21 14:05 UTC as written`
  assert.equal(
    html.replaceAll('<!--[-->', '').replaceAll('<!--]-->', ''),
    `<p>until <time datetime="2026-11-21T14:05:00.000Z" title="${title}">${hm('2026-11-21T14:05:00Z')}</time></p>`,
  )
})
