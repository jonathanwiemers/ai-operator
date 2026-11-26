// Tests of src/tabSwipe.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  dragOffset,
  edgeResistance,
  flickSpeed,
  lockAxis,
  lockDistance,
  neighbour,
  releaseSpeed,
  settle,
  switchDistance,
} from '../src/tabSwipe.ts'

test('thresholds: lock, switch, flick and edge resistance', () => {
  assert.equal(lockDistance, 10)
  assert.equal(switchDistance, 60)
  assert.equal(flickSpeed, 0.4)
  assert.equal(edgeResistance, 1 / 3)
})

test('lockAxis: pending below 10 px of movement', () => {
  assert.equal(lockAxis(0, 0), 'pending')
  assert.equal(lockAxis(6, 6), 'pending') // hypot ≈ 8.5
  assert.equal(lockAxis(lockDistance - 1, 0), 'pending')
  assert.equal(lockAxis(0, lockDistance - 1), 'pending')
})

test('lockAxis: sideways when |dx| >= 2·|dy| once past 10 px', () => {
  assert.equal(lockAxis(12, 5), 'x') // 12 >= 10
  assert.equal(lockAxis(12, 6), 'x') // exactly |dx| = 2|dy|
  assert.equal(lockAxis(-12, 5), 'x')
  assert.equal(lockAxis(lockDistance, 0), 'x')
})

test('lockAxis: vertical when the move is taller than twice as wide', () => {
  assert.equal(lockAxis(12, 7), 'y') // 12 < 14
  assert.equal(lockAxis(3, 12), 'y')
  assert.equal(lockAxis(0, lockDistance), 'y')
})

test('neighbour: left opens next, right opens previous', () => {
  assert.equal(neighbour(1, 4, -20), 2)
  assert.equal(neighbour(1, 4, 20), 0)
})

test('neighbour: null past both ends and with no drag', () => {
  assert.equal(neighbour(0, 4, 20), null)
  assert.equal(neighbour(3, 4, -20), null)
  assert.equal(neighbour(0, 1, -20), null)
  assert.equal(neighbour(0, 1, 20), null)
  assert.equal(neighbour(2, 5, 0), null)
})

test('dragOffset: 1:1 with a neighbour, a third at the ends', () => {
  assert.equal(dragOffset(-40, 1, 4), -40)
  assert.equal(dragOffset(40, 1, 4), 40)
  assert.equal(dragOffset(30, 0, 4), 30 * edgeResistance) // first tab, drag right
  assert.equal(dragOffset(-30, 3, 4), -30 * edgeResistance) // last tab, drag left
})

test('settle: distance below 60 springs back even when fast', () => {
  const short = switchDistance - 10
  assert.equal(settle({ dx: -short, width: 300, speed: -1, index: 0, count: 3 }), null)
  assert.equal(settle({ dx: short, width: 300, speed: 1, index: 1, count: 3 }), null)
})

test('settle: a third of the width switches', () => {
  assert.equal(settle({ dx: -100, width: 300, speed: 0, index: 0, count: 3 }), 1)
  assert.equal(settle({ dx: 100, width: 300, speed: 0, index: 1, count: 3 }), 0)
})

test('settle: a fast flick past 60 but below a third switches', () => {
  const mid = switchDistance + 10
  assert.equal(settle({ dx: -mid, width: 300, speed: -0.5, index: 0, count: 3 }), 1)
  assert.equal(settle({ dx: mid, width: 300, speed: flickSpeed, index: 1, count: 3 }), 0)
})

test('settle: slow and short of a third springs back', () => {
  const mid = switchDistance + 10
  assert.equal(settle({ dx: -mid, width: 300, speed: -0.2, index: 0, count: 3 }), null)
  assert.equal(settle({ dx: mid, width: 300, speed: flickSpeed - 0.01, index: 1, count: 3 }), null)
})

test('settle: a flick against the drag direction springs back', () => {
  const mid = switchDistance + 10
  assert.equal(settle({ dx: -mid, width: 300, speed: 0.5, index: 0, count: 3 }), null)
  assert.equal(settle({ dx: mid, width: 300, speed: -0.5, index: 1, count: 3 }), null)
})

test('settle: both ends never switch outward', () => {
  assert.equal(settle({ dx: 100, width: 300, speed: 1, index: 0, count: 3 }), null)
  assert.equal(settle({ dx: -100, width: 300, speed: -1, index: 2, count: 3 }), null)
})

test('settle: a one-tab row never switches', () => {
  assert.equal(settle({ dx: -100, width: 300, speed: -1, index: 0, count: 1 }), null)
  assert.equal(settle({ dx: 100, width: 300, speed: 1, index: 0, count: 1 }), null)
})

test('releaseSpeed: samples older than 100 ms are ignored', () => {
  const samples = [
    { x: 0, t: 0 },
    { x: 50, t: 50 },
    { x: 100, t: 200 },
    { x: 120, t: 250 },
  ]
  // Window is t >= 150: only (100, 200) and (120, 250) → 20/50 = flickSpeed
  assert.equal(releaseSpeed(samples), flickSpeed)
})

test('releaseSpeed: a single sample gives 0', () => {
  assert.equal(releaseSpeed([{ x: 10, t: 100 }]), 0)
  assert.equal(releaseSpeed([]), 0)
})
