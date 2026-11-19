import type { LineCount, MergedLines, MergedLineTotals } from './generated/store.ts'
import type { CodeTotalsView, MetricsCodeView } from './generated/views.ts'
import { bucketLabel, type StackedChart } from './chartData.ts'
import { cost } from './format.ts'
import { linesLabel, measuredLabel } from './lines.ts'
import { basisNote, unmeteredNote } from './spend.ts'

// The Code section of the Metrics page (R14 of the milestone "Desk: one
// Metrics page"): GET /api/v1/metrics/code shaped into the chart, tiles and
// the per-project table. A partial merged count is "≥", its cost per 100
// lines "≤"; an unknown one "—". Pure: no Vue.

const sum = (c?: LineCount) => (c?.added ?? 0) + (c?.deleted ?? 0)

// codeChart is the written lines and the merged production lines (added
// plus deleted) per bucket.
export function codeChart(v: MetricsCodeView, locale?: string): StackedChart {
  const buckets = v.buckets ?? []
  const series = (key: string, label: string, token: '--chart-1' | '--chart-2', of: (b: (typeof buckets)[number]) => number) => {
    const values = buckets.map(of)
    return { key, label, token, values, total: values.reduce((a, n) => a + n, 0) }
  }
  return {
    labels: buckets.map((b) => bucketLabel(b.bucket, v.bucket, locale)),
    series: [
      series('written', 'Written lines', '--chart-1', (b) => sum(b.written)),
      series('merged', 'Merged production lines', '--chart-2', (b) => sum(b.merged?.production)),
    ],
    unknown: buckets.map(() => 0),
  }
}

// categoryLabel is one category's merged lines, "≥ +A/−D" when partial.
export function categoryLabel(c: LineCount | undefined, partial?: boolean): string {
  const s = linesLabel(c?.added ?? 0, c?.deleted ?? 0)
  return partial ? `≥ ${s}` : s
}

// per100Label is the cost per 100 merged production lines: "—" when the
// cost is unknown or there are no production lines, "≤ $1.20" when the line
// count is partial (the true figure can only be lower).
export function per100Label(t?: MergedLineTotals): string {
  if (!t?.costPer100USD || !sum(t.lines?.production)) return '—'
  const s = cost(t.costPer100USD)
  return t.lines.partial ? `≤ ${s}` : s
}

// per100Note is "2 of 3 measured", the cost's token-basis and unmetered
// notes, joined; "" without any.
export function per100Note(t: CodeTotalsView): string {
  const notes = [measuredLabel(t.issues)]
  if (t.cost) notes.push(basisNote(t.cost), unmeteredNote(t.cost))
  return notes.filter(Boolean).join(' · ')
}

export interface CodeTile {
  id: 'written' | 'production' | 'tests' | 'docs' | 'generated' | 'per100'
  label: string
  value: string
  note?: string
}

const categories: [keyof Omit<MergedLines, 'partial'>, string][] = [
  ['production', 'Merged production'],
  ['tests', 'Merged tests'],
  ['docs', 'Merged docs'],
  ['generated', 'Merged generated'],
]

// codeTiles are the written lines, the merged lines by category and the
// cost per 100 merged production lines, whose note says how many merged
// issues are measured and carries the cost's basis and unmetered notes.
export function codeTiles(t: CodeTotalsView): CodeTile[] {
  return [
    { id: 'written', label: 'Written', value: linesLabel(t.written?.added ?? 0, t.written?.deleted ?? 0) },
    ...categories.map(([id, label]) => ({ id, label, value: categoryLabel(t.merged?.[id], t.merged?.partial) })),
    { id: 'per100', label: 'Cost per 100 merged production lines', value: per100Label(t.issues), note: per100Note(t) || undefined },
  ]
}

export interface CodeProjectRow {
  project: string
  production: string
  per100: string
}

// codeProjectRows are the merged production lines and the cost per 100 of
// each project, as the server orders them.
export function codeProjectRows(v: MetricsCodeView): CodeProjectRow[] {
  return (v.byProject ?? []).map((p) => ({
    project: `${p.namespace}/${p.project}`,
    production: categoryLabel(p.totals.merged?.production, p.totals.merged?.partial),
    per100: per100Label(p.totals.issues),
  }))
}
