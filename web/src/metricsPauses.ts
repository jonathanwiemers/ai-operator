import type { LongPauseView, MetricsPausesView } from './generated/views.ts'
import { bucketLabel, colorSlots, formatCount, formatHours, maxSeries, OTHER, type StackedChart } from './chartData.ts'
import { pauseKindLabel } from './pauseCard.ts'
import { pauseDuration, pausesSummary, type PauseResolverRow, type PauseViaRow } from './pauses.ts'

// The Pauses section of the Metrics page (decision 10 of the milestone
// "Desk: one Metrics page"): GET /api/v1/metrics/pauses shaped into
// the charts, tiles and tables. Kinds, resolvers and vias carry the labels
// of the milestone's pause card (pauseCard.ts, pauses.ts). Pure: no Vue.

// metricsFilterNote is what the Code, Flow and Pauses sections say while the
// Kind or Engine filter is set (decision 8).
export const metricsFilterNote = 'Kind and engine filter cost and tokens only'

// pausesTruncatedNote is what the section says when the server read only the
// newest pause events.
export const pausesTruncatedNote = 'Counts only the newest 20000 pause events.'

// pauseKindsChart is the pauses per bucket, stacked by kind: the kinds with
// the most pauses over the range first, at most six, the rest as Other.
export function pauseKindsChart(v: MetricsPausesView, locale?: string): StackedChart {
  const buckets = v.buckets ?? []
  const counts = new Map<string, number>()
  for (const b of buckets) {
    for (const [kind, t] of Object.entries(b.byKind ?? {})) counts.set(kind, (counts.get(kind) ?? 0) + t.count)
  }
  const ranked = [...counts.entries()]
    .filter(([, n]) => n > 0)
    .sort(([ka, a], [kb, b]) => b - a || (ka < kb ? -1 : ka > kb ? 1 : 0))
    .map(([k]) => k)
  const top = ranked.slice(0, maxSeries)
  const keys = ranked.length > maxSeries ? [...top, OTHER] : top
  const inTop = new Set(top)
  const slots = colorSlots(keys)
  const series = keys.map((key) => {
    const values = buckets.map((b) =>
      Object.entries(b.byKind ?? {})
        .filter(([kind]) => (key === OTHER ? !inTop.has(kind) : kind === key))
        .reduce((n, [, t]) => n + t.count, 0),
    )
    return {
      key,
      label: key === OTHER ? 'Other' : pauseKindLabel(key),
      token: slots.get(key)!,
      values,
      total: values.reduce((a, n) => a + n, 0),
    }
  })
  return {
    labels: buckets.map((b) => bucketLabel(b.bucket, v.bucket, locale)),
    series,
    unknown: buckets.map(() => 0),
  }
}

// hours is seconds as hours, to two decimals.
function hours(sec: number): number {
  return Math.round((sec / 3600) * 100) / 100
}

// pausedHoursChart is the paused hours per bucket, one series.
export function pausedHoursChart(v: MetricsPausesView, locale?: string): StackedChart {
  const buckets = v.buckets ?? []
  const values = buckets.map((b) => hours(b.pausedSec))
  return {
    labels: buckets.map((b) => bucketLabel(b.bucket, v.bucket, locale)),
    series: [{ key: 'paused', label: 'Paused hours', token: '--chart-1', values, total: hours(buckets.reduce((a, b) => a + b.pausedSec, 0)) }],
    unknown: buckets.map(() => 0),
  }
}

// pausesChartSummary is a pauses chart's text for screen readers.
export function pausesChartSummary(title: string, chart: StackedChart, unit: 'count' | 'hours'): string {
  const total = chart.series.reduce((a, s) => a + s.total, 0)
  const range = chart.labels.length ? `, ${chart.labels[0]} to ${chart.labels[chart.labels.length - 1]}` : ''
  const fmt = unit === 'count' ? formatCount : formatHours
  let out = `${title}${range}: ${fmt(total)} in total`
  const top = chart.series.length > 1 ? [...chart.series].filter((s) => s.key !== OTHER).sort((a, b) => b.total - a.total)[0] : undefined
  if (top && top.total > 0) out += `. Most: ${top.label}, ${fmt(top.total)}`
  return `${out}. The table view lists every value.`
}

export interface PauseTile {
  id: 'count' | 'paused' | 'mean' | 'open'
  label: string
  value: string
}

// pauseTiles are the section's figures: pauses, paused time, mean time to
// resume ("—" while none was resumed) and the pauses open now.
export function pauseTiles(v: MetricsPausesView): PauseTile[] {
  const t = v.totals
  const resumed = Object.values(t?.byResolver ?? {}).reduce((a, n) => a + n, 0)
  return [
    { id: 'count', label: 'Pauses', value: formatCount(t?.count ?? 0) },
    { id: 'paused', label: 'Paused time', value: t?.count ? pauseDuration(t.pausedSec) : '—' },
    { id: 'mean', label: 'Mean time to resume', value: resumed ? pauseDuration(t.meanResumeSec) : '—' },
    { id: 'open', label: 'Open now', value: formatCount(v.openNow ?? 0) },
  ]
}

export interface PauseKindTableRow {
  kind: string
  label: string
  count: number
  duration: string
}

export interface PauseTables {
  kinds: PauseKindTableRow[]
  resolvers: PauseResolverRow[]
  vias: PauseViaRow[]
}

// pauseTables are the tables by kind (count and time, longest paused first),
// by resolver and by via (most first), as the pause card counts them.
export function pauseTables(v: MetricsPausesView): PauseTables {
  const s = pausesSummary({ pauses: [], totals: v.totals }, 0)
  if (!s) return { kinds: [], resolvers: [], vias: [] }
  return {
    kinds: s.kinds.map((k) => ({ kind: k.kind, label: pauseKindLabel(k.kind), count: k.count, duration: pauseDuration(k.sec) })),
    resolvers: s.resolvers,
    vias: s.vias,
  }
}

export interface PauseProjectRow {
  project: string
  count: number
  duration: string
}

// pauseProjectRows are the pauses per project (namespace/name), longest
// paused first as the server sends them.
export function pauseProjectRows(v: MetricsPausesView): PauseProjectRow[] {
  return (v.byProject ?? []).map((p) => ({ project: `${p.namespace}/${p.project}`, count: p.count, duration: pauseDuration(p.pausedSec) }))
}

export interface LongPauseRow {
  id: string
  project: string
  milestone: string
  kind: string
  // reason is one line of untrusted text, shown as text.
  reason: string
  duration: string
  open: boolean
  // to is the milestone's Pilot tab on the Desk.
  to: string
}

// longPauseRows are the ten longest pauses as rows linking to their
// milestone's Pilot tab.
export function longPauseRows(v: MetricsPausesView): LongPauseRow[] {
  return (v.longest ?? []).map((p: LongPauseView, i) => ({
    id: `${i}:${p.namespace}/${p.project}/${p.milestoneIID}/${p.since}`,
    project: `${p.namespace}/${p.project}`,
    // %n alone: LongPauseView carries no milestone title, and the Metrics
    // page loads none (steering log of "Desk: one look on every page", R10).
    milestone: `%${p.milestoneIID}`,
    kind: pauseKindLabel(p.kind),
    reason: (p.reason ?? '').replace(/\s+/g, ' ').trim(),
    duration: pauseDuration(p.seconds),
    open: p.open,
    to: p.deskURL,
  }))
}
