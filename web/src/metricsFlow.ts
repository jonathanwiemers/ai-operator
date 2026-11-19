import type { FlowTotalsView, MetricsFlowView } from './generated/views.ts'
import { bucketLabel, type StackedChart } from './chartData.ts'
import { durationShort } from './format.ts'

// The Flow section of the Metrics page (R14 of the milestone "Desk: one
// Metrics page"): GET /api/v1/metrics/flow shaped into the chart and tiles.
// Pure: no Vue.

// flowChart is the merged issues per bucket, one series.
export function flowChart(v: MetricsFlowView, locale?: string): StackedChart {
  const buckets = v.buckets ?? []
  const values = buckets.map((b) => b.merged)
  return {
    labels: buckets.map((b) => bucketLabel(b.bucket, v.bucket, locale)),
    series: [{ key: 'merged', label: 'Merged issues', token: '--chart-1', values, total: values.reduce((a, n) => a + n, 0) }],
    unknown: buckets.map(() => 0),
  }
}

export interface FlowTile {
  id: 'wall' | 'agent' | 'review' | 'fix'
  label: string
  value: string
}

// perIssue is a mean per merged issue to one decimal, "—" without merges.
const perIssue = (n: number, issues: number) => (issues > 0 ? (Number.isFinite(n) ? n : 0).toFixed(1) : '—')

// flowTiles are the median wall and agent times ("—" without an issue to
// take them from) and the review and fix calls per merged issue.
export function flowTiles(t: FlowTotalsView): FlowTile[] {
  return [
    { id: 'wall', label: 'Median wall time', value: t.wallIssues > 0 ? durationShort(t.medianWallSec) : '—' },
    { id: 'agent', label: 'Median agent time', value: t.agentIssues > 0 ? durationShort(t.medianAgentSec) : '—' },
    { id: 'review', label: 'Review calls per issue', value: perIssue(t.reviewCallsPerIssue, t.mergedIssues) },
    { id: 'fix', label: 'Fix calls per issue', value: perIssue(t.fixCallsPerIssue, t.mergedIssues) },
  ]
}
