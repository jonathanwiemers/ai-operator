// The Costs page's metrics (docs/contributing/web-ui.md, "Metrics page"): what
// 100 written lines (implement calls of merged issues) cost by experiment
// arm, model, effort and step session, and what fills the context. Pure
// helpers for MetricsBreakdown.vue and MetricsContext.vue; the figures
// come from GET /costs/metrics.
import { formatUSD } from './chartData.ts'
import type { MetricsGroup, MetricsView, ProjectContext } from './generated/views.ts'

// lowHint explains a greyed-out row (MetricsGroup.low).
export const lowHint = 'fewer than 5 merged issues: too few to compare'

// keyLabel names a group's key: none (no effort, no step session, no
// implement call) is "—".
export function keyLabel(key: string): string {
  return key === '' ? '—' : key
}

// issuesLabel counts merged issues: "1 issue", "5 issues".
export function issuesLabel(n: number): string {
  return `${n} ${n === 1 ? 'issue' : 'issues'}`
}

// per100Label is the cost per 100 written lines, "—" without lines.
export function per100Label(g: MetricsGroup): string {
  return g.mergedLines > 0 ? formatUSD(g.costPer100Lines) : '—'
}

// MetricsTable is one table of the metrics: its id, title, the name of its
// key column and its rows.
export interface MetricsTable {
  id: string
  title: string
  keyLabel: string
  rows: MetricsGroup[]
}

// metricsTables lists the arm tables first, one per project in the rows'
// order (by project), titled with the project's current experiment when it
// has the arms; none without arms. Then the by-model, by-effort and
// by-step-session tables, always.
export function metricsTables(v: MetricsView): MetricsTable[] {
  const arms: (MetricsTable & { project: string })[] = []
  for (const a of v.arms) {
    const project = a.project ?? ''
    let t = arms.find((x) => x.project === project)
    if (!t) {
      t = { id: `arms:${project}`, project, title: `Arms: ${project}`, keyLabel: 'Arm', rows: [] }
      arms.push(t)
    }
    if (a.experiment) t.title = `Arms of ${a.experiment}: ${project}`
    t.rows.push(a)
  }
  return [
    ...arms,
    { id: 'model', title: 'By model', keyLabel: 'Model', rows: v.byModel },
    { id: 'effort', title: 'By effort', keyLabel: 'Effort', rows: v.byEffort },
    { id: 'stepSession', title: 'By step session', keyLabel: 'Step session', rows: v.byStepSession },
  ]
}

// hasContext: any project has a base context or tool results in the range.
export function hasContext(c: ProjectContext[]): boolean {
  return c.some((p) => p.baseContext.length > 0 || p.topTools.length > 0)
}

// byteLabel abbreviates a size: 950 B, 81.2 kB, 1.2 MB (decimal units).
export function byteLabel(n: number): string {
  if (!Number.isFinite(n) || n < 1000) return `${Math.max(0, Math.round(Number.isFinite(n) ? n : 0))} B`
  const [div, unit] = n < 1e6 ? [1e3, 'kB'] : n < 1e9 ? [1e6, 'MB'] : [1e9, 'GB']
  const v = n / div
  return `${v < 100 ? v.toFixed(1).replace(/\.0$/, '') : Math.round(v)} ${unit}`
}

// The value filters of the Metrics page (project, kind, engine; there is no
// model filter) that sit behind the phone's Filters button.
export interface ValueFilters {
  project?: string
  kind?: string
  engine?: string
}

// activeFilters counts the value filters not set to All (absent or empty).
export function activeFilters(f: ValueFilters): number {
  return [f.project, f.kind, f.engine].filter((v) => !!v).length
}

// filtersLabel is the phone's Filters button: "Filters" at 0, "Filters (n)".
export function filtersLabel(n: number): string {
  return n > 0 ? `Filters (${n})` : 'Filters'
}
