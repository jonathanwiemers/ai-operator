import type { ChatCommand } from './chatState'
import type { MilestonePermissions } from './generated/views'
import type { AutopilotItem, DecisionRef, QueueItem } from './types'
import { milestoneName } from './milestoneName.ts'

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
// history feature, the Decisions view of its milestone Cockpit's panel
// (milestonePath) at the decision, "<path>?panel=decisions#<id>"; without it, the
// GitLab comment (d.url, "" when there is none).
export function decisionHref(d: Pick<DecisionRef, 'id' | 'url'>, milestonePath: string, history: boolean): string {
  const id = d.id.trim()
  if (history && id && milestonePath) return `${milestonePath}?panel=decisions#${encodeURIComponent(id)}`
  return d.url ?? ''
}

// DecisionGroup is one line of the context panel's decisions (R10): a kind
// and how many of the queue's decisions are of it.
export interface DecisionGroup {
  kind: 'revisit' | 'open' | 'superseded'
  label: string
  count: number
}

// decisionGroups sums a queue's decisions per kind, instead of a row of
// IDs and Revisit chips per issue: Revisit counts the decisions an issue
// revisits, Open the standing ones, Superseded those a revisit replaced.
// An ID counts once per kind; kinds without one are left out.
export function decisionGroups(rows: readonly Pick<QueueRow, 'decisions' | 'revisitOf'>[]): DecisionGroup[] {
  const ids = { revisit: new Set<string>(), open: new Set<string>(), superseded: new Set<string>() }
  for (const r of rows) {
    const revisit = r.revisitOf?.trim().toUpperCase()
    if (revisit) ids.revisit.add(revisit)
    for (const d of r.decisions) {
      const id = d.id.trim().toUpperCase()
      if (id) ids[d.superseded ? 'superseded' : 'open'].add(id)
    }
  }
  const labels: [DecisionGroup['kind'], string][] = [
    ['revisit', 'Revisit'],
    ['open', 'Open'],
    ['superseded', 'Superseded'],
  ]
  return labels.filter(([k]) => ids[k].size).map(([kind, label]) => ({ kind, label, count: ids[kind].size }))
}

// decisionsTabHref is where the context panel's decision lines link: the
// Decisions view of the milestone's panel with the history feature, which it reads; ''
// without (the lines are text).
export function decisionsTabHref(milestonePath: string, history: boolean): string {
  return history && milestonePath ? `${milestonePath}?panel=decisions` : ''
}

// revisitPromptID finds the prompt id ("Q7") the operator's answer to a
// revisit names, or '' when it names none.
export function revisitPromptID(answer: string): string {
  return /\bQ\d+\b/.exec(answer)?.[0] ?? ''
}

// waitingForTurn is the Desk's line for a milestone that waits for its turn
// to be planned (queuedBehind: the older milestones, ascending), or '' when
// it does not wait. A milestone shows by title with its %n where milestones
// (the project's) has it, else as %n.
export function waitingForTurn(queuedBehind?: number[] | null, milestones: readonly { iid: number; title: string }[] = []): string {
  if (!queuedBehind?.length) return ''
  const name = (iid: number) => {
    const n = milestoneName(iid, milestones.find((m) => m.iid === iid)?.title)
    return n.ref ? `${n.title} (${n.ref})` : n.title
  }
  return `Waiting for its turn: ${queuedBehind.map(name).join(', ')} go first`
}

// Lane is where an autopilot item is (decision 8 of the parallel milestone).
type Lane = QueueItem['lane']

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
