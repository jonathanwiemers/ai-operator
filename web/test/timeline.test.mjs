// Tests of src/timeline.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { clockTime } from '../src/format.ts'
import { barLayout, barName, gapPct, issuesView, legendTitle, milestoneGroups, milestoneLayout, nowPct, overviewGroups, overviewLayout, overviewWindow, pack, segmentKey, ticks, windowRange } from '../src/timeline.ts'

process.env.TZ = 'UTC'

const t0 = Date.parse('2026-11-20T00:00:00Z')
const at = (min) => new Date(t0 + min * 60000).toISOString()
const H = 3600 * 1000
const close = (a, b) => assert.ok(Math.abs(a - b) < 1e-9, `${a} != ${b}`)

const row = (over = {}) => ({
  namespace: 'g',
  project: 'shop',
  milestoneIID: 5,
  milestoneTitle: 'Parser',
  issueIID: 42,
  issueTitle: 'Add parser',
  wave: 1,
  segments: [],
  deskURL: '/x',
  ...over,
})

test('barLayout: bar and segments in percent, merge marker', () => {
  const r = row({
    segments: [
      { kind: 'running', start: at(60), end: at(90) },
      { kind: 'waiting', start: at(90), end: at(120) },
    ],
    mergedAt: at(120),
  })
  const b = barLayout(r, t0, t0 + 4 * H, t0 + 3 * H)
  close(b.left, 25)
  close(b.width, 25)
  assert.equal(b.cutStart, false)
  assert.equal(b.cutEnd, false)
  assert.deepEqual(b.segments.map((s) => [s.kind, s.left, s.width, s.open]), [
    ['running', 0, 50, false],
    ['waiting', 50, 50, false],
  ])
  assert.deepEqual(b.marker, { kind: 'merged', left: 100 })
})

test('barLayout: an open segment ends at now', () => {
  const r = row({ segments: [{ kind: 'running', start: at(60) }] })
  const b = barLayout(r, t0, t0 + 4 * H, t0 + 2 * H)
  close(b.left, 25)
  close(b.width, 25)
  assert.equal(b.segments[0].open, true)
  close(b.segments[0].width, 100)
  assert.equal(b.marker, null)
})

test('barLayout: skip marker, rows without time or outside the window', () => {
  const s = barLayout(row({ segments: [{ kind: 'running', start: at(0), end: at(30) }], skippedAt: at(60) }), t0, t0 + H, t0 + H)
  assert.deepEqual(s.marker, { kind: 'skipped', left: 100 })
  close(s.width, 100)
  assert.equal(barLayout(row(), t0, t0 + H, t0 + H), null)
  assert.equal(barLayout(row({ segments: [{ kind: 'running', start: at(120), end: at(150) }] }), t0, t0 + H, t0 + 3 * H), null)
})

test('barLayout: cut bars are clamped to 0-100 % and flagged at both edges', () => {
  const r = row({ segments: [{ kind: 'waiting', start: at(-60), end: at(60) }, { kind: 'running', start: at(60) }] })
  const b = barLayout(r, t0, t0 + 2 * H, t0 + 3 * H)
  assert.equal(b.left, 0)
  assert.equal(b.width, 100)
  assert.equal(b.cutStart, true)
  assert.equal(b.cutEnd, true)
  assert.deepEqual(b.segments.map((s) => [s.kind, s.left, s.width]), [
    ['waiting', 0, 50],
    ['running', 50, 50],
  ])
  const m = barLayout(row({ segments: [{ kind: 'running', start: at(-30), end: at(30) }], mergedAt: at(30) }), t0, t0 + H, t0 + H)
  assert.equal(m.cutStart, true)
  assert.equal(m.cutEnd, false)
  close(m.width, 50)
})

test('nowPct: inside and outside the window', () => {
  assert.equal(nowPct(t0, t0 + 4 * H, t0 + H), 25)
  assert.equal(nowPct(t0, t0 + 4 * H, t0 + 4 * H), 100)
  assert.equal(nowPct(t0, t0 + 4 * H, t0 + 5 * H), null)
  assert.equal(nowPct(t0, t0 + 4 * H, t0 - 1), null)
  assert.equal(nowPct(t0, t0, t0), null)
})

test('ticks: 6 h hourly, 24 h every 2 h, 36 h every 3 h', () => {
  const six = ticks(t0 + 30 * 60000, t0 + 6.5 * H)
  assert.deepEqual(six.map((t) => t.label), ['01:00', '02:00', '03:00', '04:00', '05:00', '06:00'])
  close(six[0].pct, (0.5 / 6) * 100)
  const day = ticks(t0 + 10 * 60000, t0 + 24 * H + 10 * 60000)
  assert.equal(day.length, 12)
  assert.deepEqual(day.slice(0, 3).map((t) => t.label), ['02:00', '04:00', '06:00'])
  assert.ok(day.every((t) => t.pct >= 0 && t.pct <= 100))
  const long = ticks(t0, t0 + 36 * H)
  assert.deepEqual(long.slice(0, 3).map((t) => t.label), ['00:00', '03:00', '06:00'])
  assert.equal(long[0].pct, 0)
})

test('ticks: 7 d and a 3-day span give day ticks at local midnight', () => {
  const week = ticks(t0 - 7 * 24 * H + 5 * H, t0 + 5 * H)
  assert.equal(week.length, 7)
  const label = (ms) => new Date(ms).toLocaleDateString(undefined, { weekday: 'short', day: 'numeric' })
  assert.equal(week.at(-1).label, label(t0))
  close(week.at(-1).pct, (7 * 24 - 5) / (7 * 24) * 100)
  const saved = process.env.TZ
  process.env.TZ = 'Europe/Berlin'
  try {
    const three = ticks(t0 + 6 * H, t0 + 3 * 24 * H + 6 * H)
    assert.equal(three.length, 3)
    for (const t of three) {
      const d = new Date(t0 + 6 * H + (t.pct / 100) * 3 * 24 * H)
      assert.equal(d.getHours() * 60 + d.getMinutes(), 0)
    }
  } finally {
    process.env.TZ = saved
  }
  assert.deepEqual(ticks(t0, t0), [])
})

test('barName: merged, skipped, running, held and waiting issues', () => {
  const now = t0 + 5 * H
  const merged = row({
    segments: [
      { kind: 'running', start: at(0), end: at(10) },
      { kind: 'running', start: at(5), end: at(12) },
      { kind: 'waiting', start: at(12), end: at(30) },
    ],
    mergedAt: at(30),
  })
  assert.equal(barName(merged, now), `#42 Add parser, running 12 min, waiting 18 min, merged ${clockTime(at(30), now)}`)
  assert.equal(barName(merged, now, { milestone: true }), `#42 Add parser, running 12 min, waiting 18 min, merged ${clockTime(at(30), now)}, in %5`)
  const skipped = row({ issueTitle: '', segments: [], skippedAt: at(40), milestoneIID: 0 })
  assert.equal(barName(skipped, now, { milestone: true }), `#42, skipped ${clockTime(at(40), now)}`)
  const running = row({ segments: [{ kind: 'running', start: new Date(now - 20 * 60000).toISOString() }] })
  assert.equal(barName(running, now), '#42 Add parser, running 20 min, running now')
  const held = row({ segments: [{ kind: 'running', start: at(0), end: at(3) }, { kind: 'held', start: at(3) }] })
  assert.equal(barName(held, now), `#42 Add parser, running 3 min, held 4 h 57 min, held since ${clockTime(at(3), now)}`)
  const waiting = row({ segments: [{ kind: 'running', start: at(0), end: at(3) }, { kind: 'waiting', start: at(3) }] })
  assert.equal(barName(waiting, now), `#42 Add parser, running 3 min, waiting 4 h 57 min, waiting since ${clockTime(at(3), now)}`)
})

test('barName: the split by kind in key order, each kind counted once', () => {
  const now = t0 + 5 * H
  const merged = row({
    segments: [
      { kind: 'running', start: at(0), end: at(15) },
      { kind: 'review', start: at(15), end: at(18) },
      { kind: 'review', start: at(16), end: at(20) },
      { kind: 'ci', start: at(20), end: at(35) },
      { kind: 'held', start: at(35), end: at(38) },
      { kind: 'waiting', start: at(38), end: at(48) },
    ],
    mergedAt: at(48),
  })
  assert.equal(
    barName(merged, now),
    `#42 Add parser, running 15 min, review 5 min, CI 15 min, waiting 10 min, held 3 min, merged ${clockTime(at(48), now)}`,
  )
  assert.equal(
    barName(merged, now, { milestone: true }),
    `#42 Add parser, running 15 min, review 5 min, CI 15 min, waiting 10 min, held 3 min, merged ${clockTime(at(48), now)}, in %5`,
  )
  const noReview = row({ segments: [{ kind: 'running', start: at(0), end: at(10) }, { kind: 'ci', start: at(10), end: at(20) }], skippedAt: at(20) })
  assert.equal(barName(noReview, now), `#42 Add parser, running 10 min, CI 10 min, skipped ${clockTime(at(20), now)}`)
})

test('barName: an issue open in review or in CI says so', () => {
  const now = t0 + 30 * 60000
  const inReview = row({ segments: [{ kind: 'running', start: at(0), end: at(20) }, { kind: 'review', start: at(20) }] })
  assert.equal(barName(inReview, now), `#42 Add parser, running 20 min, review 10 min, in review since ${clockTime(at(20), now)}`)
  const inCI = row({ segments: [{ kind: 'running', start: at(0), end: at(25) }, { kind: 'ci', start: at(25) }] })
  assert.equal(barName(inCI, now), `#42 Add parser, running 25 min, CI 5 min, in CI since ${clockTime(at(25), now)}`)
})

test('segmentKey: the five kinds in order with their tokens', () => {
  assert.deepEqual(segmentKey.map((k) => [k.kind, k.label, k.token]), [
    ['running', 'Running', '--segment-running'],
    ['review', 'Review', '--segment-review'],
    ['ci', 'CI', '--segment-ci'],
    ['waiting', 'Waiting', '--segment-waiting'],
    ['held', 'Held', '--segment-held'],
  ])
})

test('milestoneLayout: groups by wave, wave 0 last without a heading, API order inside', () => {
  const seg = [{ kind: 'running', start: at(0), end: at(30) }]
  const view = {
    from: at(0),
    to: at(60),
    rows: [
      row({ issueIID: 13, wave: 2, segments: seg }),
      row({ issueIID: 20, wave: 0, segments: seg }),
      row({ issueIID: 12, wave: 1, segments: seg }),
      row({ issueIID: 11, wave: 1, segments: seg }),
    ],
  }
  const l = milestoneLayout(view, t0 + 2 * H)
  assert.deepEqual(l.groups.map((g) => [g.heading, g.bars.map((b) => b.label)]), [
    ['W1', ['#12', '#11']],
    ['W2', ['#13']],
    ['', ['#20']],
  ])
  assert.equal(l.to, t0 + 2 * H, 'the window grows to now')
  assert.equal(l.nowPct, 100)
  close(l.groups[0].bars[0].bar.width, 25)
  assert.match(l.groups[0].bars[0].name, /^#12 Add parser, running 30 min$/)
})

test('pack: overlapping and touching bars go on separate lines, gaps share one', () => {
  const b = (left, width) => ({ bar: { left, width } })
  const lines = pack([b(50, 10), b(0, 20), b(10, 20), b(20, 5), b(20 + gapPct, 5), b(32, 5)])
  assert.deepEqual(lines.map((l) => l.map((x) => x.bar.left)), [
    [0, 20 + gapPct, 32, 50],
    [10],
    [20],
  ])
  for (const line of lines) {
    for (let i = 1; i < line.length; i++) assert.ok(line[i - 1].bar.left + line[i - 1].bar.width + gapPct <= line[i].bar.left)
  }
  assert.deepEqual(pack([]), [])
})

test('overviewLayout: one entry per project, packed lines, colours beyond six', () => {
  const seg = (a, b) => [{ kind: 'running', start: at(a), end: at(b) }]
  const rows = [
    ...[1, 2, 3, 4, 5, 6, 7].map((m) => row({ milestoneIID: m, milestoneTitle: `M${m}`, issueIID: 10 + m, segments: seg(m * 60, m * 60 + 30) })),
    row({ milestoneIID: 1, milestoneTitle: 'M1', issueIID: 30, segments: seg(65, 80) }),
    row({ project: 'cart', milestoneIID: 1, milestoneTitle: 'Cart M1', issueIID: 5, segments: seg(0, 10) }),
    row({ project: 'cart', milestoneIID: 0, milestoneTitle: '', issueIID: 6, segments: seg(60, 70) }),
  ]
  const l = overviewLayout({ from: at(0), to: at(24 * 60), rows }, t0 + 24 * H)
  assert.deepEqual(l.projects.map((p) => [p.key, p.lines.length]), [
    ['g/shop', 2],
    ['g/cart', 1],
  ])
  assert.deepEqual(l.projects[0].lines[1].map((b) => b.label), ['#30'])
  assert.deepEqual(l.legend.map((e) => [e.project, e.milestoneIID, e.title, e.token]), [
    ['shop', 1, 'M1', '--chart-1'],
    ['shop', 2, 'M2', '--chart-2'],
    ['shop', 3, 'M3', '--chart-3'],
    ['shop', 4, 'M4', '--chart-4'],
    ['shop', 5, 'M5', '--chart-5'],
    ['shop', 6, 'M6', '--chart-6'],
    ['shop', 7, 'M7', '--chart-other'],
    ['cart', 1, 'Cart M1', '--chart-other'],
    ['cart', 0, 'No milestone', '--chart-other'],
  ])
  assert.equal(l.projects[0].lines[1][0].token, '--chart-1')
  assert.match(l.projects[0].lines[0][0].name, /, in %1$/)
  assert.equal(l.ticks.length, 12)
})

test('overviewLayout: the window slides with now, keeping its length', () => {
  const l = overviewLayout({ from: at(0), to: at(60), rows: [row({ segments: [{ kind: 'running', start: at(0) }] })] }, t0 + 2 * H)
  assert.equal(l.from, t0 + H)
  assert.equal(l.to, t0 + 2 * H)
  const b = l.projects[0].lines[0][0].bar
  assert.equal(b.cutStart, true)
  assert.equal(b.left, 0)
  assert.equal(b.width, 100)
})

test('milestoneGroups: one row per issue with a bar, empty waves dropped', () => {
  const now = t0 + 3 * H
  const view = {
    from: at(0),
    to: at(180),
    rows: [
      row({ issueIID: 11, wave: 1, deskURL: '/i/11', segments: [{ kind: 'running', start: at(0), end: at(30) }], mergedAt: at(40) }),
      row({ issueIID: 12, wave: 1, segments: [] }),
      row({ issueIID: 13, wave: 2, segments: [] }),
    ],
  }
  const groups = milestoneGroups(milestoneLayout(view, now).groups)
  assert.equal(groups.length, 1)
  assert.equal(groups[0].heading, 'W1')
  assert.deepEqual(
    groups[0].rows.map((r) => [r.key, r.label, r.sub, r.lines[0][0].to]),
    [['5#11', '#11', 'Add parser', '/i/11']],
  )
  assert.match(groups[0].rows[0].lines[0][0].name, /^#11 Add parser, running 30 min, merged/)
})

test('issuesView: Timeline by default with history, Lanes without', () => {
  assert.equal(issuesView(undefined, true), 'timeline')
  assert.equal(issuesView('timeline', true), 'timeline')
  assert.equal(issuesView('lanes', true), 'lanes')
  assert.equal(issuesView('bogus', true), 'timeline')
  assert.equal(issuesView('timeline', false), 'lanes')
  assert.equal(issuesView(undefined, false), 'lanes')
})

test('issuesView: the Board by ?view=board while the milestone has its tasks', () => {
  assert.equal(issuesView('board', true, true), 'board')
  assert.equal(issuesView('board', false, true), 'board')
  assert.equal(issuesView('board', true), 'timeline')
  assert.equal(issuesView('board', false, false), 'lanes')
})

test('overviewGroups: one row per project with bars, linked to the project', () => {
  const rows = [
    row({ issueIID: 1, deskURL: '/i/1', segments: [{ kind: 'running', start: at(60), end: at(120) }] }),
    row({ issueIID: 2, deskURL: '/i/2', segments: [{ kind: 'running', start: at(90), end: at(150) }] }),
    row({ project: 'cart', issueIID: 3, segments: [{ kind: 'running', start: at(-600), end: at(-500) }] }),
  ]
  const l = overviewLayout({ from: at(0), to: at(24 * 60), rows }, t0 + 24 * H)
  const g = overviewGroups(l.projects)
  assert.equal(g.length, 1)
  assert.deepEqual(g[0].rows.map((r) => [r.label, r.to, r.lines.map((line) => line.map((b) => [b.label, b.to, b.token]))]), [
    ['g/shop', { name: 'stream-project', params: { namespace: 'g', name: 'shop' } }, [[['#1', '/i/1', '--chart-1']], [['#2', '/i/2', '--chart-1']]]],
  ])
  assert.deepEqual(overviewGroups([]), [])
})

test('legendTitle, overviewWindow and windowRange', () => {
  assert.equal(legendTitle({ milestoneIID: 5, title: 'Parser' }), '%5 Parser')
  assert.equal(legendTitle({ milestoneIID: 0, title: 'No milestone' }), 'No milestone')
  assert.equal(overviewWindow(undefined), '24h')
  assert.equal(overviewWindow('bogus'), '24h')
  assert.equal(overviewWindow('6h'), '6h')
  assert.equal(overviewWindow('7d'), '7d')
  assert.deepEqual(windowRange('6h', t0), { from: at(-360), to: at(0) })
  assert.deepEqual(windowRange('7d', t0), { from: at(-7 * 24 * 60), to: at(0) })
})
