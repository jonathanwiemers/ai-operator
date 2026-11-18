import type { ChatCommand } from './chatState'
import type { MilestonePermissions } from './generated/views'
import type { AutopilotItem, DecisionRef, QueueItem } from './types'

// QueueRow is one issue of a milestone's autopilot queue: the view's item
// plus the decisions and revisit the spec task's status keeps for it.
export interface QueueRow extends QueueItem {
  decisions: DecisionRef[]
  revisitOf?: string
}

// queueRows joins the queue with the spec task's autopilot items by issue,
// in queue order.
export function queueRows(items: QueueItem[], status: AutopilotItem[] = []): QueueRow[] {
  const byIID = new Map(status.map((s) => [s.iid, s]))
  return items.map((it) => {
    const s = byIID.get(it.iid)
    return { ...it, decisions: s?.decisions ?? [], revisitOf: s?.revisitOf }
  })
}

// queueDone counts the issues the autopilot is through with.
export function queueDone(items: { state: QueueItem['state'] }[]): number {
  return items.filter((i) => i.state === 'Merged' || i.state === 'Skipped').length
}

// queueHeld counts the held issues: Active items with a hold (as the
// milestone view's holds).
export function queueHeld(items: { state: QueueItem['state']; hold?: unknown }[]): number {
  return items.filter((i) => i.state === 'Active' && !!i.hold).length
}

export function queueTone(s: QueueItem['state']): 'success' | 'progress' | 'warning' | 'muted' {
  return s === 'Merged' ? 'success' : s === 'Active' ? 'progress' : s === 'Skipped' ? 'warning' : 'muted'
}

// revisitCommand is the chat command that opens a revisit prompt for a
// decision, the same as `/agent revisit <id>`.
export function revisitCommand(decisionID: string): ChatCommand {
  return { cmd: 'revisit', decision: decisionID.trim().toUpperCase() }
}

// canRevisit: owners (the merge permission is chat plus owner subject) may
// revisit a decision that is not superseded.
export function canRevisit(d: DecisionRef, permissions?: Pick<MilestonePermissions, 'chat' | 'merge'>): boolean {
  return !!permissions?.chat && !!permissions.merge && !d.superseded && !!d.id.trim()
}

// decisionHref is where a decision ID in the queue links (R14): with the
// history feature, the Decisions tab of its milestone's Desk page
// (milestonePath) at the card, "<path>?tab=decisions#<id>"; without it, the
// GitLab comment (d.url, "" when there is none).
export function decisionHref(d: Pick<DecisionRef, 'id' | 'url'>, milestonePath: string, history: boolean): string {
  const id = d.id.trim()
  if (history && id && milestonePath) return `${milestonePath}?tab=decisions#${encodeURIComponent(id)}`
  return d.url ?? ''
}

// revisitPromptID finds the prompt id ("Q7") the operator's answer to a
// revisit names, or '' when it names none.
export function revisitPromptID(answer: string): string {
  return /\bQ\d+\b/.exec(answer)?.[0] ?? ''
}

// waitingForTurn is the Desk's line for a milestone that waits for its turn
// to be planned (queuedBehind: the older milestones, ascending), or '' when
// it does not wait.
export function waitingForTurn(queuedBehind?: number[] | null): string {
  if (!queuedBehind?.length) return ''
  return `Waiting for its turn: ${queuedBehind.map((iid) => `%${iid}`).join(', ')} go first`
}

// Lane is where an autopilot item is (decision 8 of the parallel milestone).
export type Lane = QueueItem['lane']

// LaneGroup is one lane of the Issues tab's queue: its heading and items.
export interface LaneGroup<T> {
  lane: Lane
  label: string
  items: T[]
}

const laneLabels: [Lane, string][] = [
  ['running', 'Running'],
  ['landing', 'Landing'],
  ['ready', 'Ready to land'],
  ['waiting', 'Waiting'],
  ['held', 'Held'],
  ['done', 'Done'],
]

// laneOf is an item's lane; an unknown one (an older operator) counts as
// done when merged or skipped, else waiting.
function laneOf(i: Pick<QueueItem, 'lane' | 'state'>): Lane {
  if (laneLabels.some(([l]) => l === i.lane)) return i.lane
  return i.state === 'Merged' || i.state === 'Skipped' ? 'done' : 'waiting'
}

const readyTime = (t?: string) => (t && Date.parse(t)) || Number.MAX_SAFE_INTEGER

// laneGroups groups the queue by lane in the order of decision 8. Groups
// keep queue order, Ready to land is sorted by readyAt (unknown last).
// Empty groups are left out, except Running, which says "n of N".
export function laneGroups<T extends Pick<QueueItem, 'lane' | 'state' | 'readyAt'>>(
  items: readonly T[],
  running: number,
  maxParallel?: number,
): LaneGroup<T>[] {
  return laneLabels.flatMap(([lane, name]): LaneGroup<T>[] => {
    const in_ = items.filter((i) => laneOf(i) === lane)
    if (lane === 'ready') in_.sort((a, b) => readyTime(a.readyAt) - readyTime(b.readyAt))
    if (lane !== 'running') return in_.length ? [{ lane, label: name, items: in_ }] : []
    const label = maxParallel && maxParallel > 0 ? `${name} (${running} of ${maxParallel})` : `${name} (${running})`
    return [{ lane, label, items: in_ }]
  })
}

// waitingLabel says what a waiting item waits for.
export function waitingLabel(waitingFor?: number[] | null): string {
  return waitingFor?.length ? `after ${waitingFor.map((iid) => `#${iid}`).join(', ')}` : 'for a free slot'
}

// resumeCommand lifts the hold on one issue, the same as `/agent resume #N`.
export function resumeCommand(issue: number): ChatCommand {
  return { cmd: 'resume', issue }
}

// The bounds of a milestone's parallel limit (`/agent parallel N`).
export const parallelMin = 1
export const parallelMax = 10

// parallelCommand is the stepper's command, the limit moved by step, or
// null when that leaves 1 to 10.
export function parallelCommand(limit: number, step: 1 | -1): ChatCommand | null {
  const n = limit + step
  return n >= parallelMin && n <= parallelMax ? { cmd: 'parallel', parallel: n } : null
}

// waveChip is an item's wave chip ("W1"), '' outside the plan (wave 0).
export function waveChip(wave?: number): string {
  return wave && wave > 0 ? `W${wave}` : ''
}

// wavesLabel says how many waves the plan has ("3 waves"), '' for one or
// none: the waves are the layers of the plan graph, numbered from 1.
export function wavesLabel(items: readonly Pick<QueueItem, 'wave'>[]): string {
  const n = Math.max(0, ...items.map((i) => i.wave ?? 0))
  return n > 1 ? `${n} waves` : ''
}

// sentLine is the status line of the last command a queue or card sent,
// pending or sent (an answer the viewer may not read): key is the
// decision, the issue ("#42") or the limit.
export function sentLine(kind: ChatCommand['cmd'], key: string, pending: boolean): string {
  if (kind === 'revisit') return pending ? `Revisiting ${key}…` : `Revisit of ${key} opened.`
  if (kind === 'parallel') return pending ? `Setting the limit to ${key}…` : `Limit ${key} sent.`
  return pending ? `Resuming ${key}…` : `Resume ${key} sent.`
}
