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
