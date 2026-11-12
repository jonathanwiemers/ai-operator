import type { CostPointView } from './generated/views.ts'
import { tokenCount } from './format.ts'

// The Costs page's chart data (docs/ui.md, "Costs page"; decisions 7 and 8 of
// docs/specs/costs-page.spec.md): buckets and points of GET /api/v1/costs
// shaped into series, top N plus Other, bucket labels and colour slots.
// Pure: no Vue, no Chart.js; the chart wrappers only pass it on.

// OTHER is the series key of Other: the keys beyond the top N and the
// server's folded points (other: true, key ""). The server's key "" without
// other is the legacy runs ("before calls"), so Other needs its own key.
export const OTHER = '\u0000other'

// At most six series plus Other (decision 8).
export const maxSeries = 6

export type ChartMetric = 'cost' | 'tokens'

export type BucketKind = 'day' | 'week' | 'month'

// The colour tokens in style.css (docs/ui.md, "UI kit").
export type ChartToken = '--chart-1' | '--chart-2' | '--chart-3' | '--chart-4' | '--chart-5' | '--chart-6' | '--chart-other'

export interface ChartSeries {
  key: string
  values: number[]
}

export interface ChartSeriesSet {
  series: ChartSeries[]
  // Per bucket, the calls without a known cost and those not metered at all
  // (a part of unknown), for marking: unknown is never $0 (decision 6).
  unknown: number[]
  unmetered: number[]
}

// knownCost is a point's known cost in USD, 0 when unknown (Money "").
function knownCost(p: CostPointView): number {
  const v = Number.parseFloat(p.totals.costUSD)
  return Number.isFinite(v) ? v : 0
}

function meteredTokens(p: CostPointView): number {
  const t = p.totals
  return t.inputTokens + t.outputTokens + t.cacheReadTokens + t.cacheWriteTokens
}

// topKeys returns the n keys with the highest known cost over the range,
// ties by key. The server's Other points are never a key of their own.
export function topKeys(points: CostPointView[], n = maxSeries): string[] {
  const sums = new Map<string, number>()
  for (const p of points) {
    if (p.other) continue
    sums.set(p.key, (sums.get(p.key) ?? 0) + knownCost(p))
  }
  return [...sums.entries()]
    .sort(([ka, a], [kb, b]) => b - a || (ka < kb ? -1 : ka > kb ? 1 : 0))
    .slice(0, Math.max(0, n))
    .map(([k]) => k)
}

// seriesFromPoints spreads the points over every bucket (a bucket without a
// point is 0): one series per top key, in topKeys' order, then Other when a
// point went there. Cost is the known cost, tokens the metered tokens.
// Points of a bucket not in buckets are left out.
export function seriesFromPoints(
  buckets: string[],
  points: CostPointView[],
  metric: ChartMetric,
  n = maxSeries,
): ChartSeriesSet {
  const index = new Map(buckets.map((b, i) => [b, i]))
  points = points.filter((p) => index.has(p.bucket))
  const keys = topKeys(points, n)
  const top = new Set(keys)
  const rows = new Map<string, number[]>(keys.map((k) => [k, buckets.map(() => 0)]))
  const unknown = buckets.map(() => 0)
  const unmetered = buckets.map(() => 0)
  const value = metric === 'cost' ? knownCost : meteredTokens
  for (const p of points) {
    const i = index.get(p.bucket)!
    const key = !p.other && top.has(p.key) ? p.key : OTHER
    let row = rows.get(key)
    if (!row) {
      row = buckets.map(() => 0)
      rows.set(key, row)
    }
    row[i] = (row[i] ?? 0) + value(p)
    unknown[i] = (unknown[i] ?? 0) + p.totals.unknownCalls
    unmetered[i] = (unmetered[i] ?? 0) + p.totals.unmeteredCalls
  }
  const order = rows.has(OTHER) ? [...keys, OTHER] : keys
  // Sums of floats: cost is rounded to 4 decimals, as the store sums it.
  const round = metric === 'cost' ? (v: number) => Math.round(v * 1e4) / 1e4 : (v: number) => v
  return { series: order.map((key) => ({ key, values: rows.get(key)!.map(round) })), unknown, unmetered }
}

// parseBucket reads a bucket start (YYYY-MM-DD) as midnight UTC, so adding
// days never meets a DST change.
function parseBucket(bucket: string): Date {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(bucket)
  if (!m) return new Date(Number.NaN)
  return new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3])))
}

// bucketLabel names a bucket: a day "3 Nov", a week "3–9 Nov", a month
// "Nov 2026" (in en-GB; the locale orders them, and its ICU data may put
// spaces around the dash). The date is a local date
// already; it is formatted in UTC so neither the browser's zone nor a DST
// change in the week shifts it.
export function bucketLabel(bucket: string, kind: BucketKind, locale?: string): string {
  const start = parseBucket(bucket)
  if (Number.isNaN(start.getTime())) return bucket
  if (kind === 'month') {
    return new Intl.DateTimeFormat(locale, { month: 'short', year: 'numeric', timeZone: 'UTC' }).format(start)
  }
  const fmt = new Intl.DateTimeFormat(locale, { day: 'numeric', month: 'short', timeZone: 'UTC' })
  if (kind === 'day') return fmt.format(start)
  const end = new Date(start.getTime() + 6 * 86400 * 1000)
  return fmt.formatRange(start, end)
}

// fnv1a is the 32-bit FNV-1a hash of the key's UTF-8 bytes.
function fnv1a(key: string): number {
  let h = 0x811c9dc5
  for (const b of new TextEncoder().encode(key)) {
    h ^= b
    h = Math.imul(h, 0x01000193) >>> 0
  }
  return h
}

// colorSlots gives each key a colour token that stays the same across
// reloads and key orders (D363-P2): its FNV-1a hash mod 6, probing to the
// next free slot in sorted key order. Other is always --chart-other. With
// more than six keys the rest share their hash slot.
export function colorSlots(keys: string[]): Map<string, ChartToken> {
  const out = new Map<string, ChartToken>()
  const taken = new Set<number>()
  const sorted = [...new Set(keys)].filter((k) => k !== OTHER).sort()
  for (const key of sorted) {
    let slot = fnv1a(key) % maxSeries
    if (taken.size < maxSeries) {
      while (taken.has(slot)) slot = (slot + 1) % maxSeries
      taken.add(slot)
    }
    out.set(key, `--chart-${slot + 1}` as ChartToken)
  }
  if (keys.includes(OTHER)) out.set(OTHER, '--chart-other')
  return out
}

// formatUSD is a chart's cost: two decimals, thousands grouped ("$1,234.50").
export function formatUSD(usd: number): string {
  const v = Number.isFinite(usd) ? usd : 0
  return v.toLocaleString('en-US', { style: 'currency', currency: 'USD', minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

// formatTokens is a chart's token count in k/M (format.ts's tokenCount).
export const formatTokens = tokenCount
