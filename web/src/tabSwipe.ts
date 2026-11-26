// Pure gesture rules for swiping between Desk tabs (milestone "Desk: tab
// rows without stray dots, and swiping between tabs on touch screens", R2).
// SwipeTabs (docs/contributing/web-ui.md, "SwipeTabs") drives the DOM; this
// module has no window, document or Vue imports.

export const lockDistance = 10 // px before the direction is decided
export const switchDistance = 60 // px a switch needs at least
export const flickSpeed = 0.4 // px/ms
export const edgeResistance = 1 / 3

export type Axis = 'pending' | 'x' | 'y'

// Decide the axis once the finger has moved lockDistance (Euclidean).
// Sideways when |dx| >= 2·|dy|; otherwise vertical for the rest of the touch.
export function lockAxis(dx: number, dy: number): Axis {
  if (Math.hypot(dx, dy) < lockDistance) return 'pending'
  return Math.abs(dx) >= 2 * Math.abs(dy) ? 'x' : 'y'
}

// A left swipe (dx < 0) opens the next tab; a right swipe the previous.
// No wrap-around past the first or last tab.
export function neighbour(index: number, count: number, dx: number): number | null {
  if (dx < 0) {
    const next = index + 1
    return next < count ? next : null
  }
  if (dx > 0) {
    const prev = index - 1
    return prev >= 0 ? prev : null
  }
  return null
}

// Content follows the finger 1:1 when a neighbour exists in the drag's
// direction; otherwise it moves at edgeResistance (a third).
export function dragOffset(dx: number, index: number, count: number): number {
  return neighbour(index, count, dx) == null ? dx * edgeResistance : dx
}

export interface Sample {
  x: number
  t: number // ms
}

const speedWindowMs = 100

// Signed px/ms over the pointer samples of the last 100 ms. A single
// sample (or a zero-duration window) gives 0.
export function releaseSpeed(samples: readonly Sample[]): number {
  const last = samples.at(-1)
  if (last == null || samples.length < 2) return 0
  const tMin = last.t - speedWindowMs
  const window = samples.filter((s) => s.t >= tMin)
  const first = window[0]
  const end = window.at(-1)
  if (first == null || end == null || window.length < 2) return 0
  const dt = end.t - first.t
  if (dt <= 0) return 0
  return (end.x - first.x) / dt
}

// Target index to switch to, or null to spring back. Needs a neighbour,
// |dx| >= switchDistance, and either |dx| >= width/3 or a flick of at
// least flickSpeed in the drag's direction. A one-tab row never switches.
export function settle(o: {
  dx: number
  width: number
  speed: number
  index: number
  count: number
}): number | null {
  const target = neighbour(o.index, o.count, o.dx)
  if (target == null) return null
  const travel = Math.abs(o.dx)
  if (travel < switchDistance) return null
  const flick =
    o.dx !== 0 && Math.sign(o.dx) * o.speed >= flickSpeed
  if (travel >= o.width / 3 || flick) return target
  return null
}
