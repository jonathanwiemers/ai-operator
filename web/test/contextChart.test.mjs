// Tests of src/contextChart.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { chartBox, chartSteps, contextChart, niceMax, seriesTurns, stepLabel, stepOpacity } from '../src/contextChart.ts'

function call(over = {}) {
  return { level: 'call', key: '1', label: '', at: '', totals: {}, models: [], kind: 'implement', ...over }
}

const { left, right, top, bottom, width, height } = chartBox
const plotRight = width - right
const plotBottom = height - bottom

test('seriesTurns numbers points from 1 without thinning', () => {
  assert.deepEqual(seriesTurns(4), [1, 2, 3, 4])
  assert.deepEqual(seriesTurns(4, 4), [1, 2, 3, 4])
  assert.deepEqual(seriesTurns(1, 7), [1])
  assert.deepEqual(seriesTurns(0), [])
})

test('seriesTurns spreads a thinned series over the real turn count', () => {
  assert.deepEqual(seriesTurns(3, 9), [1, 5, 9])
  assert.deepEqual(seriesTurns(4, 10), [1, 4, 7, 10])
  // A turn count below the points (or none) falls back to the index.
  assert.deepEqual(seriesTurns(3, 2), [1, 2, 3])
  assert.deepEqual(seriesTurns(3, 0), [1, 2, 3])
})

test('niceMax rounds up to 1, 2 or 5 times a power of ten', () => {
  assert.equal(niceMax(0), 1)
  assert.equal(niceMax(73_000), 100_000)
  assert.equal(niceMax(150_000), 200_000)
  assert.equal(niceMax(200_000), 200_000)
  assert.equal(niceMax(4_100_000), 5_000_000)
})

test('chartSteps keeps implementer steps with a series, in seq order', () => {
  const calls = [
    call({ key: '5', kind: 'fix', contextSeries: [1] }),
    call({ key: '2', kind: 'implement', contextSeries: [1] }),
    call({ key: '3', kind: 'review', contextSeries: [1] }),
    call({ key: '4', kind: 'ci', contextSeries: [] }),
    call({ key: '6', kind: 'conflict' }),
    call({ key: '7', kind: 'describe', contextSeries: [1] }),
  ]
  assert.deepEqual(chartSteps(calls).map((c) => c.key), ['2', '5'])
  assert.equal(stepLabel(calls[0]), '#5 fix')
  assert.equal(stepLabel({ key: '9' }), '#9')
})

test('contextChart is null without a series', () => {
  assert.equal(contextChart([]), null)
  assert.equal(contextChart([call({ contextSeries: [] }), call({ key: '2', kind: 'review', contextSeries: [5] })]), null)
  assert.equal(contextChart([call()]), null)
})

test('contextChart maps points onto the plot area', () => {
  const d = contextChart([call({ contextSeries: [0, 50_000, 100_000] })])
  assert.equal(d.series.length, 1)
  const s = d.series[0]
  assert.equal(s.label, '#1 implement')
  assert.equal(s.points, `${left},${plotBottom} ${(left + plotRight) / 2},${(top + plotBottom) / 2} ${plotRight},${top}`)
  assert.deepEqual(s.markers, [])
  assert.equal(s.end.turn, 3)
  assert.equal(s.end.title, 'Turn 3 · 100k tokens · #1 implement')
  assert.deepEqual(d.xTicks.map((t) => t.label), ['1', '2', '3'])
  assert.deepEqual(d.yTicks.map((t) => t.label), ['0', '25k', '50k', '75k', '100k'])
  assert.equal(d.yTicks[0].pos, plotBottom)
  assert.equal(d.label, 'Context tokens per turn, 1 step')
})

test('contextChart scales a thinned series by its turns', () => {
  const d = contextChart([
    call({ key: '1', contextSeries: [10, 20, 30], turns: 9 }),
    call({ key: '2', kind: 'fix', contextSeries: [10, 20, 30] }),
  ])
  const [thinned, plain] = d.series
  // The thinned one ends at turn 9 (the right edge), the plain one at 3.
  assert.equal(thinned.end.turn, 9)
  assert.equal(thinned.end.x, plotRight)
  assert.equal(plain.end.turn, 3)
  assert.equal(plain.end.x, Math.round((left + (2 / 8) * (plotRight - left)) * 10) / 10)
  assert.equal(d.xTicks.at(-1).label, '9')
  assert.deepEqual([thinned.opacity, plain.opacity], [0.3, 1])
})

test('stepOpacity grows from the first step to the latest', () => {
  assert.equal(stepOpacity(0, 1), 1)
  assert.deepEqual([0, 1, 2].map((i) => stepOpacity(i, 3)), [0.3, 0.65, 1])
})

test('contextChart shares one y scale across steps', () => {
  const d = contextChart([
    call({ key: '1', contextSeries: [40_000] }),
    call({ key: '2', contextSeries: [80_000, 160_000] }),
  ])
  // The scale tops out at 200k for both: 40k sits at a fifth of the height.
  assert.equal(d.yTicks.at(-1).label, '200k')
  assert.equal(d.series[0].end.y, Math.round((plotBottom - (plotBottom - top) / 5) * 10) / 10)
  assert.equal(d.series[1].end.y, Math.round((plotBottom - ((plotBottom - top) * 4) / 5) * 10) / 10)
})

test('contextChart puts markers on subagent turns', () => {
  const d = contextChart([call({ key: '3', contextSeries: [10, 20, 30, 40, 50], turns: 9, subagentTurns: [3, 1, 1, 7, -1] })])
  const m = d.series[0].markers
  assert.deepEqual(m.map((p) => p.turn), [3, 7])
  assert.equal(m[0].tokens, 20)
  assert.equal(m[0].title, 'Turn 3 · 20 tokens · #3 implement · subagent ran')
  assert.equal(d.label, 'Context tokens per turn, 1 step, 2 turns with a subagent')
})
