import type { ChatCommand } from './chatState'
import type { Tone } from './format'
import type { QueueEntry } from './generated/views'

// Labels, tones and Desk buttons for the project Queue (Decision 4 / D412-P1).

export function queueStateLabel(e: Pick<QueueEntry, 'state' | 'holdingFor' | 'pausedReason'>): string {
  switch (e.state) {
    case 'running':
      return 'Running'
    case 'holding':
      return `Holding for %${e.holdingFor ?? 0}`
    case 'waiting':
      return 'Waiting to plan'
    case 'paused':
      return e.pausedReason ? `Paused: ${e.pausedReason}` : 'Paused'
    case 'budget':
      return 'Budget'
  }
}

export function queueTone(e: Pick<QueueEntry, 'state'>): Tone {
  return e.state === 'running' ? 'progress' : e.state === 'paused' ? 'warning' : e.state === 'budget' ? 'danger' : 'muted'
}

export interface QueueAction {
  id: 'runNext' | 'clearMark' | 'pause' | 'resume'
  label: string
  cmd: ChatCommand
}

// i: rank index; unmarked at 0 is first by age (Run next pins it, D412-P1).
export function queueActions(e: Pick<QueueEntry, 'state' | 'markedBy'>, i: number): QueueAction[] {
  const mark: QueueAction = e.markedBy
    ? { id: 'clearMark', label: 'Clear mark', cmd: { cmd: 'unstart' } }
    : { id: 'runNext', label: 'Run next', cmd: { cmd: 'start' } }
  const ctrl: QueueAction = e.state === 'paused' || e.state === 'budget'
    ? { id: 'resume', label: 'Resume', cmd: { cmd: 'resume' } }
    : { id: 'pause', label: 'Pause', cmd: { cmd: 'pause' } }
  return i < 0 ? [] : [mark, ctrl]
}
