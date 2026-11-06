import type { InboxItem } from './types'

export interface PausedMilestone {
  namespace: string
  project: string
  milestoneIID: number
  title: string
  // sinceMs is how long the autopilot has been paused; undefined when the
  // pause start is not known.
  sinceMs?: number
}

export interface PausedStrip {
  count: number
  // longestMs is the longest known pause; 0 when none has a start time.
  longestMs: number
  milestones: PausedMilestone[]
}

// pausedStrip summarizes the paused autopilots in the Inbox, longest first.
// Pauses without a start time count but never set the longest.
export function pausedStrip(items: InboxItem[], now: number): PausedStrip {
  const milestones: PausedMilestone[] = []
  for (const it of items) {
    if (it.kind !== 'pause') continue
    const t = it.pausedSince ? Date.parse(it.pausedSince) : NaN
    milestones.push({
      namespace: it.namespace,
      project: it.project,
      milestoneIID: it.milestoneIID ?? 0,
      title: it.title,
      sinceMs: Number.isNaN(t) ? undefined : Math.max(0, now - t),
    })
  }
  milestones.sort((a, b) => (b.sinceMs ?? -1) - (a.sinceMs ?? -1))
  return { count: milestones.length, longestMs: milestones[0]?.sinceMs ?? 0, milestones }
}
