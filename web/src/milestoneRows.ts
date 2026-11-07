import { cost, tokenCount, type Tone } from './format.ts'
import type { MilestoneRow, MilestoneState } from './types'

// The dashboard's and the project page's milestone rows (decision 5).

// progressPct is the share of merged (or skipped) issues, rounded to whole
// percent, 0 without issues.
export function progressPct(row: Pick<MilestoneRow, 'progress'>): number {
  const { merged, total } = row.progress
  if (!(total > 0)) return 0
  return Math.min(100, Math.max(0, Math.round((merged / total) * 100)))
}

// rowCost is the row's cost: a dash when it is unknown (no calls recorded,
// the oldest milestones), never $0.
export function rowCost(row: Pick<MilestoneRow, 'costUSD' | 'costKnown'>): string {
  return row.costKnown ? cost(row.costUSD) : '—'
}

// rowTokens is the row's tokens, a dash without any (costs summed over
// tasks carry none).
export function rowTokens(row: Pick<MilestoneRow, 'tokens'>): string {
  return row.tokens > 0 ? tokenCount(row.tokens) : '—'
}

// leftoversLabel says how many of the retrospective's leftovers are still
// open: "1 leftover", "3 leftovers", empty without any.
export function leftoversLabel(row: Pick<MilestoneRow, 'followUpsOpen'>): string {
  const n = row.followUpsOpen
  if (!(n > 0)) return ''
  return n === 1 ? '1 leftover' : `${n} leftovers`
}

// stateTone is the state badge's tone.
export function stateTone(s: MilestoneState): Tone {
  switch (s) {
    case 'running':
      return 'progress'
    case 'paused':
      return 'warning'
    case 'done':
      return 'success'
    default:
      return 'neutral'
  }
}

// inProgressRank orders In progress (decision 4): needs you first, then
// running, then waiting, then paused and planned.
function inProgressRank(r: Pick<MilestoneRow, 'needsYou' | 'state'>): number {
  if (r.needsYou) return 0
  switch (r.state) {
    case 'running':
      return 1
    case 'waiting':
      return 2
    case 'paused':
    case 'planned':
      return 3
    default:
      return 4
  }
}

// sortInProgress sorts the open rows as decision 4 asks. The server sorts
// them already; this is a defensive sort that keeps its order within a
// rank. It returns a new array.
export function sortInProgress<T extends Pick<MilestoneRow, 'needsYou' | 'state'>>(rows: readonly T[]): T[] {
  return rows
    .map((r, i) => ({ r, i, k: inProgressRank(r) }))
    .sort((a, b) => a.k - b.k || a.i - b.i)
    .map((x) => x.r)
}

// milestoneRowKey identifies a row across projects.
export function milestoneRowKey(r: Pick<MilestoneRow, 'namespace' | 'project' | 'iid'>): string {
  return `${r.namespace}/${r.project}/${r.iid}`
}
