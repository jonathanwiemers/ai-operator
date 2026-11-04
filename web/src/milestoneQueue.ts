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
