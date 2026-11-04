import { tokenCount } from './format.ts'
import type { SpendChild } from './types'

// The context-growth chart of a run on the Task page: one line per
// implementer step (context tokens per main-thread turn), with markers at
// the turns after which a subagent ran. ContextChart.vue draws what
// contextChart returns.

// stepKinds are the call kinds that are implementer steps; reviewers,
// describe and pilot calls get no series.
export const stepKinds = ['implement', 'ci', 'conflict', 'fix']

// Steps are ordered, so they share one hue and grow more opaque from the
// first to the latest; subagent markers take a contrasting hue.
export const lineColor = 'var(--tone-progress-fg)'
export const markerColor = 'var(--accent-2)'

// stepOpacity is series i's opacity of n: 0.3 for the first, 1 for the latest.
export function stepOpacity(i: number, n: number): number {
  return n <= 1 ? 1 : Math.round((0.3 + (0.7 * i) / (n - 1)) * 100) / 100
}

// The chart's viewBox and the plot area inside it (room for axis labels).
export const chartBox = { width: 600, height: 170, left: 44, right: 10, top: 8, bottom: 22 }

export interface ChartPoint {
  x: number
  y: number
  turn: number
  tokens: number
  title: string
}

export interface ChartSeries {
  key: string
  label: string
  opacity: number
  // points is the polyline's points attribute.
  points: string
  markers: ChartPoint[]
  end: ChartPoint
}

export interface ChartTick {
  pos: number
  label: string
}

export interface ContextChartData {
  series: ChartSeries[]
  xTicks: ChartTick[]
  yTicks: ChartTick[]
  label: string
}

// stepLabel names a series in the legend and tooltips: "#3 implement".
export function stepLabel(c: Pick<SpendChild, 'key' | 'kind'>): string {
  return c.kind ? `#${c.key} ${c.kind}` : `#${c.key}`
}

// chartSteps are the calls of a run that get a series: implementer steps
// with a non-empty context series, in seq order.
export function chartSteps(calls: SpendChild[]): SpendChild[] {
  return calls
    .filter((c) => stepKinds.includes(c.kind ?? '') && (c.contextSeries?.length ?? 0) > 0)
    .sort((a, b) => Number(a.key) - Number(b.key))
}

// seriesTurns gives each of n points its turn number (1-based). A thinned
// series is spread over the call's real turn count when that is known and
// at least n; otherwise the turn is the point's index + 1.
export function seriesTurns(n: number, turns?: number): number[] {
  const t = turns && Number.isFinite(turns) && turns >= n ? Math.floor(turns) : n
  if (n <= 1 || t === n) return Array.from({ length: n }, (_, i) => i + 1)
  return Array.from({ length: n }, (_, i) => 1 + Math.round((i * (t - 1)) / (n - 1)))
}

// niceMax rounds v up to 1, 2 or 5 times a power of ten (at least 1).
export function niceMax(v: number): number {
  if (!Number.isFinite(v) || v <= 1) return 1
  const p = 10 ** Math.floor(Math.log10(v))
  for (const m of [1, 2, 5, 10]) if (m * p >= v) return m * p
  return 10 * p
}

// ticks are count+1 evenly spaced values from 0 to max, rounded to whole
// numbers and without duplicates.
function ticks(max: number, count: number): number[] {
  const out: number[] = []
  for (let i = 0; i <= count; i++) {
    const v = Math.round((max * i) / count)
    if (!out.includes(v)) out.push(v)
  }
  return out
}

function pointTitle(turn: number, tokens: number, step: string, extra = ''): string {
  return `Turn ${turn} · ${tokenCount(tokens)} tokens · ${step}${extra}`
}

// contextChart maps a run's calls to the chart: all series share one x
// scale (turns) and one y scale (context tokens). It returns null when no
// implementer step has a series, so older runs show no chart.
export function contextChart(calls: SpendChild[]): ContextChartData | null {
  const steps = chartSteps(calls)
  if (!steps.length) return null
  const { width, height, left, right, top, bottom } = chartBox
  const plotW = width - left - right
  const plotH = height - top - bottom

  const turnsOf = steps.map((c) => seriesTurns(c.contextSeries!.length, c.turns))
  const maxTurn = Math.max(2, ...turnsOf.map((t) => t[t.length - 1] ?? 1))
  const maxTokens = niceMax(Math.max(...steps.flatMap((c) => c.contextSeries!)))
  const xOf = (turn: number) => round1(left + ((turn - 1) / (maxTurn - 1)) * plotW)
  const yOf = (tokens: number) => round1(top + plotH - (Math.max(0, tokens) / maxTokens) * plotH)

  const series = steps.map((c, si): ChartSeries => {
    const label = stepLabel(c)
    const turns = turnsOf[si]!
    const pts = c.contextSeries!.map((v, i) => {
      const turn = turns[i] ?? i + 1
      return { x: xOf(turn), y: yOf(v), turn, tokens: v }
    })
    const markers = [...new Set(c.subagentTurns ?? [])]
      .filter((i) => Number.isInteger(i) && i >= 0 && i < pts.length)
      .sort((a, b) => a - b)
      .map((i) => pts[i]!)
      .map((p) => ({ ...p, title: pointTitle(p.turn, p.tokens, label, ' · subagent ran') }))
    const last = pts[pts.length - 1]!
    return {
      key: c.key,
      label,
      opacity: stepOpacity(si, steps.length),
      points: pts.map((p) => `${p.x},${p.y}`).join(' '),
      markers,
      end: { ...last, title: pointTitle(last.turn, last.tokens, label) },
    }
  })

  const xTicks = ticks(maxTurn - 1, Math.min(5, maxTurn - 1)).map((t) => ({ pos: xOf(t + 1), label: String(t + 1) }))
  const yTicks = ticks(maxTokens, 4).map((t) => ({ pos: yOf(t), label: tokenCount(t) }))
  const subagents = series.reduce((s, x) => s + x.markers.length, 0)
  const label =
    `Context tokens per turn, ${series.length} step${series.length === 1 ? '' : 's'}` +
    (subagents ? `, ${subagents} turn${subagents === 1 ? '' : 's'} with a subagent` : '')
  return { series, xTicks, yTicks, label }
}

function round1(v: number): number {
  return Math.round(v * 10) / 10
}
