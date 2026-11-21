// Timeline layout (R8 of "Desk: a timeline of which issues run when"): maps
// the rows of GET /api/v1/timeline and a time window to bars, segments,
// lines, ticks and accessible names in percent of the window. Pure, no DOM:
// ui/Timeline.vue draws what it returns.
import type { RouteLocationRaw } from 'vue-router'
import type { ChartToken } from './chartData'
import type { TimelineRowView, TimelineSegmentView, TimelineView } from './generated/views'
import { clockTime, duration } from './format.ts'
import { waveChip } from './milestoneQueue.ts'

// TimelineRow is what the layout reads of a row.
export type TimelineRow = Pick<TimelineRowView, 'issueIID' | 'issueTitle' | 'milestoneIID' | 'segments' | 'mergedAt' | 'skippedAt'>

export interface SegmentLayout {
  kind: TimelineSegmentView['kind']
  // left and width in percent of the bar.
  left: number
  width: number
  open: boolean
}

export interface BarLayout {
  // left and width in percent of the window; cut where the bar goes on
  // before or after the window.
  left: number
  width: number
  cutStart: boolean
  cutEnd: boolean
  segments: SegmentLayout[]
  // marker: the merge or skip, in percent of the bar.
  marker: { kind: 'merged' | 'skipped'; left: number } | null
}

export interface Tick {
  pct: number
  label: string
}

// PlacedBar is one issue's bar with what the component shows of it.
export interface PlacedBar<R> {
  row: R
  bar: BarLayout | null
  label: string
  name: string
}

export interface Window {
  from: number
  to: number
  nowPct: number | null
  ticks: Tick[]
}

// gapPct is the least space between two bars on one Overview line, so
// their #iid labels do not touch.
export const gapPct = 1

const hour = 3600 * 1000
const day = 24 * hour

function ms(iso: string | undefined | null): number | null {
  if (!iso) return null
  const t = Date.parse(iso)
  return Number.isNaN(t) ? null : t
}

function pctOf(t: number, from: number, to: number): number {
  return to > from ? Math.min(100, Math.max(0, ((t - from) / (to - from)) * 100)) : 0
}

// barLayout places a row's bar in the window [from, to). An open segment
// ends at now. null when the row has no time or lies outside the window.
export function barLayout(row: Pick<TimelineRow, 'segments' | 'mergedAt' | 'skippedAt'>, from: number, to: number, now: number): BarLayout | null {
  const segs: { kind: TimelineSegmentView['kind']; start: number; end: number; open: boolean }[] = []
  for (const s of row.segments ?? []) {
    const start = ms(s.start)
    if (start === null) continue
    const end = ms(s.end)
    segs.push({ kind: s.kind, start, end: Math.max(start, end ?? now), open: end === null })
  }
  const mergedAt = ms(row.mergedAt)
  const skippedAt = mergedAt === null ? ms(row.skippedAt) : null
  const mark = mergedAt ?? skippedAt
  const times = [...segs.flatMap((s) => [s.start, s.end]), ...(mark === null ? [] : [mark])]
  if (times.length === 0) return null
  const start = Math.min(...times)
  const end = Math.max(...times)
  if (end < from || start > to || to <= from) return null
  const left = pctOf(start, from, to)
  const width = pctOf(end, from, to) - left
  const a = Math.max(start, from)
  const b = Math.min(end, to)
  const inBar = (t: number) => (b > a ? Math.min(100, Math.max(0, ((t - a) / (b - a)) * 100)) : 0)
  const segments = segs
    .filter((s) => s.end >= from && s.start <= to)
    .map((s) => ({ kind: s.kind, left: inBar(s.start), width: inBar(s.end) - inBar(s.start), open: s.open }))
  const marker =
    mark === null || mark < from || mark > to ? null : { kind: mergedAt !== null ? ('merged' as const) : ('skipped' as const), left: inBar(mark) }
  return { left, width, cutStart: start < from, cutEnd: end > to, segments, marker }
}

// nowPct is the now line's place in percent, null outside the window.
export function nowPct(from: number, to: number, now: number): number | null {
  return to > from && now >= from && now <= to ? pctOf(now, from, to) : null
}

const two = (n: number) => String(n).padStart(2, '0')

// ticks are the axis marks of the window: up to 36 h full local hours
// every 1, 2, 3 or 6 h (at most about 12, D665-P2), longer local midnights
// (every 1, 2, 3, 7 or more days, again at most about 12), in [from, to).
export function ticks(from: number, to: number): Tick[] {
  if (!(to > from)) return []
  const out: Tick[] = []
  const span = to - from
  if (span <= 36 * hour) {
    const step = [1, 2, 3, 6].find((s) => span / (s * hour) <= 12) ?? 6
    const t = new Date(from)
    t.setMinutes(0, 0, 0)
    if (t.getTime() < from) t.setHours(t.getHours() + 1)
    while (t.getHours() % step !== 0) t.setHours(t.getHours() + 1)
    for (; t.getTime() < to; t.setHours(t.getHours() + step)) {
      out.push({ pct: pctOf(t.getTime(), from, to), label: `${two(t.getHours())}:00` })
    }
    return out
  }
  const days = span / day
  const step = [1, 2, 3, 7, 14].find((s) => days / s <= 12) ?? Math.ceil(days / 12)
  const t = new Date(from)
  t.setHours(0, 0, 0, 0)
  if (t.getTime() < from) t.setDate(t.getDate() + 1)
  for (; t.getTime() < to; t.setDate(t.getDate() + step)) {
    out.push({ pct: pctOf(t.getTime(), from, to), label: t.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric' }) })
  }
  return out
}

// runningMs is the union of a row's running segments, open ones up to now.
function runningMs(row: Pick<TimelineRow, 'segments'>, now: number): number {
  const spans = (row.segments ?? [])
    .filter((s) => s.kind === 'running')
    .map((s) => [ms(s.start), ms(s.end) ?? now] as const)
    .filter((s): s is readonly [number, number] => s[0] !== null)
    .sort((x, y) => x[0] - y[0])
  let total = 0
  let reach = -Infinity
  for (const [a, b] of spans) {
    if (b > reach) total += b - Math.max(a, reach)
    reach = Math.max(reach, b)
  }
  return total
}

// barName is a bar's accessible name (Decision 5, D665-P3): "#42 Add
// parser, running 12 min, merged 14:05", with ", in %5" on the Overview.
export function barName(row: TimelineRow, now: number, opts: { milestone?: boolean } = {}): string {
  const parts = [row.issueTitle ? `#${row.issueIID} ${row.issueTitle}` : `#${row.issueIID}`]
  const run = runningMs(row, now)
  if (run > 0) parts.push(`running ${duration(run)}`)
  const open = (row.segments ?? []).filter((s) => !s.end).at(-1)
  if (row.mergedAt) parts.push(`merged ${clockTime(row.mergedAt, now)}`)
  else if (row.skippedAt) parts.push(`skipped ${clockTime(row.skippedAt, now)}`)
  else if (open?.kind === 'running') parts.push('running now')
  else if (open) parts.push(`${open.kind} since ${clockTime(open.start, now)}`)
  if (opts.milestone && row.milestoneIID > 0) parts.push(`in %${row.milestoneIID}`)
  return parts.join(', ')
}

function windowOf(from: number, to: number, now: number): Window {
  return { from, to, nowPct: nowPct(from, to, now), ticks: ticks(from, to) }
}

export interface WaveGroup<R> {
  wave: number
  heading: string
  bars: PlacedBar<R>[]
}

// milestoneLayout is the Issues tab's timeline: one bar per issue, grouped
// by wave in wave order, wave 0 last without a heading, API order inside a
// group. The window runs from the view's from to now, so running bars grow.
export function milestoneLayout<R extends TimelineRow & Pick<TimelineRowView, 'wave'>>(
  view: Pick<TimelineView, 'from' | 'to'> & { rows: R[] },
  now: number,
): Window & { groups: WaveGroup<R>[] } {
  const from = ms(view.from) ?? now
  const to = Math.max(ms(view.to) ?? now, now)
  const groups = new Map<number, WaveGroup<R>>()
  for (const row of view.rows) {
    const wave = row.wave > 0 ? row.wave : 0
    let g = groups.get(wave)
    if (!g) groups.set(wave, (g = { wave, heading: waveChip(wave), bars: [] }))
    g.bars.push({ row, bar: barLayout(row, from, to, now), label: `#${row.issueIID}`, name: barName(row, now) })
  }
  const order = (w: number) => (w === 0 ? Infinity : w)
  return { ...windowOf(from, to, now), groups: [...groups.values()].sort((a, b) => order(a.wave) - order(b.wave)) }
}

export interface OverviewBar<R> extends PlacedBar<R> {
  bar: BarLayout
  token: ChartToken
}

export interface ProjectLine<R> {
  key: string
  namespace: string
  project: string
  // lines: the project's bars packed so that none hides another.
  lines: OverviewBar<R>[][]
}

export interface LegendEntry {
  key: string
  namespace: string
  project: string
  milestoneIID: number
  title: string
  token: ChartToken
}

// pack puts bars on lines greedily: by start, each on the first line whose
// last bar ends at least gapPct before it starts.
export function pack<B extends { bar: Pick<BarLayout, 'left' | 'width'> }>(bars: B[]): B[][] {
  const lines: B[][] = []
  const ends: number[] = []
  for (const b of [...bars].sort((x, y) => x.bar.left - y.bar.left)) {
    const i = ends.findIndex((end) => end + gapPct <= b.bar.left)
    if (i < 0) {
      lines.push([b])
      ends.push(b.bar.left + b.bar.width)
    } else {
      lines[i]!.push(b)
      ends[i] = b.bar.left + b.bar.width
    }
  }
  return lines
}

type OverviewRow = TimelineRow & Pick<TimelineRowView, 'namespace' | 'project' | 'milestoneTitle'>

// overviewLayout is the Overview's timeline: one entry per project in API
// order, its bars packed into lines and coloured by milestone in order of
// first appearance (--chart-1 … --chart-6, then --chart-other, D665-P1).
// A window that now has passed slides along, keeping its length.
export function overviewLayout<R extends OverviewRow>(
  view: Pick<TimelineView, 'from' | 'to'> & { rows: R[] },
  now: number,
): Window & { projects: ProjectLine<R>[]; legend: LegendEntry[] } {
  const to0 = ms(view.to) ?? now
  const shift = Math.max(0, now - to0)
  const from = (ms(view.from) ?? now) + shift
  const to = to0 + shift
  const legend = new Map<string, LegendEntry>()
  const tokenOf = (row: R): ChartToken => {
    const key = row.milestoneIID > 0 ? `${row.namespace}/${row.project}%${row.milestoneIID}` : ''
    let e = legend.get(key)
    if (!e) {
      const n = [...legend.values()].filter((x) => x.milestoneIID > 0).length
      const token: ChartToken = key && n < 6 ? (`--chart-${n + 1}` as ChartToken) : '--chart-other'
      const title = key ? row.milestoneTitle || `%${row.milestoneIID}` : 'No milestone'
      e = { key, namespace: row.namespace, project: row.project, milestoneIID: key ? row.milestoneIID : 0, title, token }
      legend.set(key, e)
    }
    return e.token
  }
  const projects = new Map<string, { p: ProjectLine<R>; bars: OverviewBar<R>[] }>()
  for (const row of view.rows) {
    const key = `${row.namespace}/${row.project}`
    let p = projects.get(key)
    if (!p) projects.set(key, (p = { p: { key, namespace: row.namespace, project: row.project, lines: [] }, bars: [] }))
    const bar = barLayout(row, from, to, now)
    if (!bar) continue
    p.bars.push({ row, bar, label: `#${row.issueIID}`, name: barName(row, now, { milestone: true }), token: tokenOf(row) })
  }
  const out = [...projects.values()].map(({ p, bars }) => ({ ...p, lines: pack(bars) }))
  return { ...windowOf(from, to, now), projects: out, legend: [...legend.values()] }
}

// TimelineBarItem, TimelineRowItem and TimelineGroupItem are what
// ui/Timeline.vue draws: groups (waves) of labelled rows, each row one or
// more lines of bars. A bar links to `to`; token colours it by milestone
// (the Overview), else its segments take the tone tokens.
export interface TimelineBarItem {
  key: string
  to: string
  label: string
  name: string
  bar: BarLayout
  token?: ChartToken
}

export interface TimelineRowItem {
  key: string
  label: string
  sub?: string
  // to: the label links there (the Overview's project rows).
  to?: RouteLocationRaw
  lines: TimelineBarItem[][]
}

export interface TimelineGroupItem {
  key: string
  heading: string
  rows: TimelineRowItem[]
}

// milestoneGroups turns milestoneLayout's waves into the component's
// groups: one row per issue with a bar, labelled "#iid" and its title.
export function milestoneGroups<R extends TimelineRow & Pick<TimelineRowView, 'deskURL'>>(groups: WaveGroup<R>[]): TimelineGroupItem[] {
  return groups
    .map((g) => ({
      key: `w${g.wave}`,
      heading: g.heading,
      rows: g.bars.flatMap((b): TimelineRowItem[] => {
        if (!b.bar) return []
        const key = `${b.row.milestoneIID}#${b.row.issueIID}`
        const bar = { key, to: b.row.deskURL, label: b.label, name: b.name, bar: b.bar }
        return [{ key, label: b.label, sub: b.row.issueTitle, lines: [[bar]] }]
      }),
    }))
    .filter((g) => g.rows.length > 0)
}

// overviewGroups turns overviewLayout's projects into the component's one
// group: a row per project with bars in the window, labelled
// "namespace/name" and linked to the project page (D667-P2).
export function overviewGroups<R extends OverviewRow & Pick<TimelineRowView, 'deskURL'>>(projects: ProjectLine<R>[]): TimelineGroupItem[] {
  const rows = projects
    .filter((p) => p.lines.length > 0)
    .map((p) => ({
      key: p.key,
      label: p.key,
      to: { name: 'stream-project', params: { namespace: p.namespace, name: p.project } },
      lines: p.lines.map((line) =>
        line.map((b) => ({ key: `${b.row.milestoneIID}#${b.row.issueIID}`, to: b.row.deskURL, label: b.label, name: b.name, bar: b.bar, token: b.token })),
      ),
    }))
  return rows.length ? [{ key: 'projects', heading: '', rows }] : []
}

// legendTitle is a legend entry's text: "%5 Parser", or "No milestone".
export function legendTitle(e: Pick<LegendEntry, 'milestoneIID' | 'title'>): string {
  return e.milestoneIID > 0 ? `%${e.milestoneIID} ${e.title}` : e.title
}

export type OverviewWindow = '6h' | '24h' | '7d'

export const overviewWindows: { value: OverviewWindow; label: string; ms: number }[] = [
  { value: '6h', label: '6 h', ms: 6 * hour },
  { value: '24h', label: '24 h', ms: day },
  { value: '7d', label: '7 d', ms: 7 * day },
]

// overviewWindow is the Overview timeline's window from ?window= (Decision
// 4): 6h, 24h or 7d, 24h by default.
export function overviewWindow(q: unknown): OverviewWindow {
  return q === '6h' || q === '7d' ? q : '24h'
}

// windowRange is the window of the given length ending at now, as the
// endpoint reads it.
export function windowRange(w: OverviewWindow, now: number): { from: string; to: string } {
  const len = overviewWindows.find((x) => x.value === w)!.ms
  return { from: new Date(now - len).toISOString(), to: new Date(now).toISOString() }
}

export type IssuesView = 'timeline' | 'lanes'

// issuesView is the Issues tab's view from ?view= (Decision 15): Timeline
// by default with the history feature, Lanes without it.
export function issuesView(q: unknown, history: boolean): IssuesView {
  if (!history) return 'lanes'
  return q === 'lanes' ? 'lanes' : 'timeline'
}
