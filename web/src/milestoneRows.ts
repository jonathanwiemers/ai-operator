import { cost, durationShort, plural, tokenCount, type Tone } from './format.ts'
import type { MilestoneIssueRow, MilestoneProgress, MilestoneRow, MilestoneState, QueueItem } from './types'

// The dashboard's and the project page's milestone rows (decision 5).

// progressPct is the share of merged (or skipped) issues, rounded to whole
// percent, 0 without issues.
export function progressPct(row: Pick<MilestoneRow, 'progress'>): number {
  const { merged, total } = row.progress
  if (!(total > 0)) return 0
  return Math.min(100, Math.max(0, Math.round((merged / total) * 100)))
}

// progressBar holds what a milestone's UProgress gets: the value and max
// (merged capped at total, a max of 1 without issues, so the bar is empty
// rather than indeterminate) and its accessible name, "3 of 5 merged" after
// the name of what progresses.
export function progressBar(
  progress: Pick<MilestoneProgress, 'merged' | 'total'>,
  name?: string,
): { value: number; max: number; label: string } {
  const total = Math.max(0, progress.total || 0)
  const value = Math.min(total, Math.max(0, progress.merged || 0))
  const text = `${progress.merged} of ${progress.total} merged`
  return { value, max: Math.max(1, total), label: name ? `${name}: ${text}` : text }
}

// progressUI is the look of a milestone's UProgress on the plan and
// milestone rows: green, filling the room next to its "3/5".
export const progressUI = { color: 'success', class: 'min-w-0 flex-1' } as const

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
  return `${n} ${plural(n, 'leftover')}`
}

// milestoneStatus is a milestone's one status, the eyebrow of its page
// (decision 4: no badge row repeats it): its state ("Paused"), named for the
// autopilot while one drives it ("Autopilot paused").
export function milestoneStatus(m: { state: MilestoneState; autopilot?: { phase: string } }): { label: string; tone: Tone } {
  const auto = !!m.autopilot && m.autopilot.phase !== 'Off'
  const label = auto ? `Autopilot ${m.state}` : m.state.charAt(0).toUpperCase() + m.state.slice(1)
  return { label, tone: stateTone(m.state) }
}

// stateTone is the state's tone.
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

// issueProgress counts a milestone page's issues as the rows do: merged
// (or skipped, or closed) of total, and the open rest.
export function issueProgress(issues: readonly Pick<MilestoneIssueRow, 'state'>[]): MilestoneProgress {
  const merged = issues.filter((i) => i.state === 'merged' || i.state === 'skipped' || i.state === 'closed').length
  return { merged, total: issues.length, open: issues.length - merged }
}

// PanelIssueState is an issue's state in the milestone panel's Issues view
// (R13): running, waiting (on dependencies or a hold), next (planned or
// ready, its turn to come), merged and skipped (or closed unmerged).
export type PanelIssueState = 'running' | 'waiting' | 'next' | 'merged' | 'skipped'

export interface PanelIssueGroup {
  state: PanelIssueState
  label: string
  issues: Pick<MilestoneIssueRow, 'iid' | 'title'>[]
}

const panelIssueLabels: Record<PanelIssueState, string> = {
  running: 'Running',
  waiting: 'Waiting',
  next: 'Next',
  merged: 'Merged',
  skipped: 'Skipped',
}

// panelIssueState is an issue's panel state: its row state, and for an
// open one the lane of its autopilot queue item (QueueItem.lane), if any:
// a waiting or held lane before an active state.
export function panelIssueState(state: MilestoneIssueRow['state'], lane?: QueueItem['lane']): PanelIssueState {
  if (state === 'merged') return 'merged'
  if (state === 'skipped' || state === 'closed') return 'skipped'
  // A held issue waits, even while its task is active (a red CI hold).
  if (lane === 'waiting' || lane === 'held') return 'waiting'
  if (state === 'active' || lane === 'running' || lane === 'landing') return 'running'
  return 'next'
}

const panelIssueTones: Record<PanelIssueState, Tone> = {
  running: 'progress',
  waiting: 'warning',
  next: 'neutral',
  merged: 'success',
  skipped: 'muted',
}

// panelIssueBadge is a panel state's badge, label and tone (a Cockpit
// thread's header).
export function panelIssueBadge(s: PanelIssueState): { label: string; tone: Tone } {
  return { label: panelIssueLabels[s], tone: panelIssueTones[s] }
}

// panelIssueGroups are a milestone's issues by panel state, in the order
// running, waiting, next, merged, skipped, each in the milestone's order;
// empty states are left out.
export function panelIssueGroups(
  issues: readonly Pick<MilestoneIssueRow, 'iid' | 'title' | 'state'>[],
  queue: readonly Pick<QueueItem, 'iid' | 'lane'>[] = [],
): PanelIssueGroup[] {
  const lanes = new Map(queue.map((q) => [q.iid, q.lane]))
  const groups = (Object.keys(panelIssueLabels) as PanelIssueState[]).map((state) => ({
    state,
    label: panelIssueLabels[state],
    issues: [] as Pick<MilestoneIssueRow, 'iid' | 'title'>[],
  }))
  for (const i of issues) {
    const s = panelIssueState(i.state, lanes.get(i.iid))
    groups.find((g) => g.state === s)!.issues.push({ iid: i.iid, title: i.title })
  }
  return groups.filter((g) => g.issues.length)
}

// issueStateTone is an issue row's state badge tone.
export function issueStateTone(s: MilestoneIssueRow['state']): Tone {
  switch (s) {
    case 'active':
      return 'progress'
    case 'merged':
      return 'success'
    case 'skipped':
    case 'closed':
      return 'muted'
    default:
      return 'neutral'
  }
}

// issueTime is an issue row's wall, agent or paused time: a dash without
// figures, and a wall time of an issue that never ran is one too.
export function issueTime(
  row: Pick<MilestoneIssueRow, 'figures'>,
  k: 'wallSec' | 'agentSec' | 'pausedSec',
): string {
  const f = row.figures
  if (!f || (k === 'wallSec' && f.wallSec <= 0)) return '—'
  return durationShort(f[k])
}
