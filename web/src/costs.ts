import type { CostsParams } from './client.ts'
import type { SpendTotals } from './generated/store.ts'
import type { CostPointView, CostsMonthView, CostsView } from './generated/views.ts'

// The Costs page's pure parts (docs/ui.md, "Costs page"): its filters in
// the URL (?range=&from=&to=&group=&project=&kind=&engine=, decision 9) and
// the KPI row's figures. Costs.vue renders them.

type QueryValue = string | null | (string | null)[] | undefined

export type CostRange = CostsView['range']
export type CostGroup = CostsView['group']

// The choices of each filter, as the server's parseCostParams accepts them;
// the first range and group are the defaults.
export const costRanges: Exclude<CostRange, 'custom'>[] = ['30d', '7d', '90d', 'month', 'lastMonth', '12m']
export const costGroups: CostGroup[] = ['project', 'kind', 'model', 'engine']
export const costKinds = [
  'spec',
  'implement',
  'review',
  'fix',
  'ci',
  'conflict',
  'describe',
  'pilot',
  'retro',
  'followUpDraft',
  'chat',
  'research',
] as const
export const costEngines = ['claude', 'cursor'] as const

export const rangeLabels: Record<CostRange, string> = {
  '7d': 'Last 7 days',
  '30d': 'Last 30 days',
  '90d': 'Last 90 days',
  month: 'This month',
  lastMonth: 'Last month',
  '12m': 'Last 12 months',
  custom: 'Custom',
}

// CostFilters are the page's filters. from and to (YYYY-MM-DD, inclusive)
// are set for the range custom only; project is "namespace/name".
export interface CostFilters {
  range: CostRange
  from?: string
  to?: string
  group: CostGroup
  project?: string
  kind?: string
  engine?: string
}

function first(v: QueryValue): string {
  const s = Array.isArray(v) ? v[0] : v
  return typeof s === 'string' ? s : ''
}

// isDate: a real calendar date, YYYY-MM-DD.
function isDate(s: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(s)) return false
  const d = new Date(`${s}T00:00:00Z`)
  return !Number.isNaN(d.getTime()) && d.toISOString().slice(0, 10) === s
}

function oneOf<T extends string>(list: readonly T[], v: string): T | undefined {
  return (list as readonly string[]).includes(v) ? (v as T) : undefined
}

// parseCostFilters reads the filters from a route's query. A value the page
// does not know falls back to the default (30d, project) or no filter; from
// and to count only together and in order, and then win over range.
export function parseCostFilters(query: Record<string, QueryValue>): CostFilters {
  const from = first(query.from)
  const to = first(query.to)
  const f: CostFilters =
    isDate(from) && isDate(to) && from <= to
      ? { range: 'custom', from, to, group: 'project' }
      : { range: oneOf(costRanges, first(query.range)) ?? costRanges[0]!, group: 'project' }
  f.group = oneOf(costGroups, first(query.group)) ?? costGroups[0]!
  const project = first(query.project)
  if (/^[^/\s]+\/[^/\s]+$/.test(project)) f.project = project
  const kind = oneOf(costKinds, first(query.kind))
  if (kind) f.kind = kind
  const engine = oneOf(costEngines, first(query.engine))
  if (engine) f.engine = engine
  return f
}

// costFiltersQuery is the URL query of filters, without the defaults, so
// the plain page is /costs.
export function costFiltersQuery(f: CostFilters): Record<string, string> {
  const q: Record<string, string> = {}
  if (f.range === 'custom' && f.from && f.to) {
    q.from = f.from
    q.to = f.to
  } else if (f.range !== 'custom' && f.range !== costRanges[0]) {
    q.range = f.range
  }
  if (f.group !== costGroups[0]) q.group = f.group
  if (f.project) q.project = f.project
  if (f.kind) q.kind = f.kind
  if (f.engine) q.engine = f.engine
  return q
}

// costParams is the request of GET /costs for filters, in the viewer's
// time zone tz (decision D362-P3: never in the URL).
export function costParams(f: CostFilters, tz: string): CostsParams {
  const p: CostsParams = { group: f.group, project: f.project, kind: f.kind, engine: f.engine, tz }
  if (f.range === 'custom') {
    p.from = f.from
    p.to = f.to
  } else {
    p.range = f.range
  }
  return p
}

function usd(t: Pick<SpendTotals, 'costUSD' | 'costKnown'>): number {
  const v = Number.parseFloat(t.costUSD)
  return t.costKnown && Number.isFinite(v) ? v : 0
}

// costChange is the change of known cost against the previous period, in
// percent; null when there is nothing to compare with: the previous known
// cost is 0 or unknown, or this period's calls all have an unknown cost.
export function costChange(
  current: Pick<SpendTotals, 'costUSD' | 'costKnown'>,
  previous: Pick<SpendTotals, 'costUSD' | 'costKnown'>,
): number | null {
  const before = usd(previous)
  if (!current.costKnown || before <= 0) return null
  return ((usd(current) - before) / before) * 100
}

// changeLabel shows a change: "+12%", "−5%", "±0%"; empty for none.
export function changeLabel(p: number | null): string {
  if (p === null || !Number.isFinite(p)) return ''
  const r = Math.round(p)
  if (r === 0) return '±0%'
  return r > 0 ? `+${r}%` : `−${-r}%`
}

// MonthToDate is this month's known cost up to today against the same days
// of last month (the 1st to today's day number, fewer when last month is
// shorter), with the calls of unknown cost of each.
export interface MonthToDate {
  current: MonthSum
  previous: MonthSum
  change: number | null
}

export interface MonthSum {
  costUSD: string
  costKnown: boolean
  calls: number
  unknownCalls: number
}

function sumPoints(points: CostPointView[]): MonthSum {
  let cost = 0
  let known = false
  let calls = 0
  let unknownCalls = 0
  for (const p of points) {
    cost += usd(p.totals)
    known ||= p.totals.costKnown
    calls += p.totals.calls
    unknownCalls += p.totals.unknownCalls
  }
  return { costUSD: known ? cost.toFixed(2) : '', costKnown: known, calls, unknownCalls }
}

// monthToDate compares this month with the same days of last month, from
// the response's month.
export function monthToDate(m: CostsMonthView): MonthToDate {
  const sameDays = m.previous.filter((p) => Number(p.bucket.slice(8, 10)) <= m.today)
  const current = sumPoints(m.current)
  const previous = sumPoints(sameDays)
  return { current, previous, change: costChange(current, previous) }
}

// costKeyLabel names a series or breakdown key: "" is the runs from before
// agent calls were recorded (the legacy runs, decision 3).
export function costKeyLabel(key: string): string {
  return key === '' ? 'before calls' : key
}
