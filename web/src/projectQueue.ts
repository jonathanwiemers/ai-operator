import type { ChatCommand } from './chatState'
import type { Tone } from './format'
import type { QueueEntry } from './generated/views'

// Labels, tones, Desk buttons and moves for the project Queue (Decision 4 / D412-P1).

// needsEstimate: waiting/holding with no done issues and no spend (D455-P1).
export function needsEstimate(e: Pick<QueueEntry, 'state' | 'done' | 'costUSD' | 'tokens'>): boolean {
  const spent = Number.parseFloat(e.costUSD || '0') > 0 || e.tokens > 0
  return (e.state === 'waiting' || e.state === 'holding') && e.done === 0 && !spent
}

// The badge stays one short word or two: a paused entry's reason is
// markdown of any length and has a line of its own below the row.
export function queueStateLabel(e: Pick<QueueEntry, 'state' | 'holdingFor'>): string {
  switch (e.state) {
    case 'running':
      return 'Running'
    case 'holding':
      return `Holding for %${e.holdingFor ?? 0}`
    case 'waiting':
      return 'Waiting to plan'
    case 'paused':
      return 'Paused'
    case 'budget':
      return 'Budget'
  }
}

export function queueTone(e: Pick<QueueEntry, 'state'>): Tone {
  return e.state === 'running' ? 'progress' : e.state === 'paused' ? 'warning' : e.state === 'budget' ? 'danger' : 'muted'
}

export interface QueueAction {
  id: 'pause' | 'resume'
  label: string
  cmd: ChatCommand
}

// The row buttons: Pause or Resume. Moves are in the row menu (menuMoves) and
// the drag handle; Move to top replaced Run next (Decision 8).
export function queueActions(e: Pick<QueueEntry, 'state'>): QueueAction[] {
  return e.state === 'paused' || e.state === 'budget'
    ? [{ id: 'resume', label: 'Resume', cmd: { cmd: 'resume' } }]
    : [{ id: 'pause', label: 'Pause', cmd: { cmd: 'pause' } }]
}

export type MoveCommand = Extract<ChatCommand, { cmd: 'move' }>

// moved returns list with the entry at from taken out and put in at to.
export function moved<T>(list: readonly T[], from: number, to: number): T[] {
  const out = [...list]
  out.splice(to, 0, ...out.splice(from, 1))
  return out
}

// moveCommand is the command that moves the entry at from to index to
// (D662-P1): the first place is top, the last bottom, a move up goes before
// the entry now below it, a move down after the entry now above it.
export function moveCommand(queue: readonly Pick<QueueEntry, 'iid'>[], from: number, to: number): MoveCommand | null {
  const n = queue.length
  if (from === to || from < 0 || to < 0 || from >= n || to >= n) return null
  if (to === 0) return { cmd: 'move', to: 'top' }
  if (to === n - 1) return { cmd: 'move', to: 'bottom' }
  const iid = queue[to]!.iid
  return to < from ? { cmd: 'move', before: iid } : { cmd: 'move', after: iid }
}

export interface MenuMove {
  id: 'top' | 'up' | 'down'
  label: string
  to: number
}

// menuMoves are the row menu's moves for the entry at i, without those that
// would not move it.
export function menuMoves(queue: readonly unknown[], i: number): MenuMove[] {
  const out: MenuMove[] = []
  if (i < 0 || i >= queue.length) return out
  if (i > 0) out.push({ id: 'top', label: 'Move to top', to: 0 }, { id: 'up', label: 'Move up', to: i - 1 })
  if (i < queue.length - 1) out.push({ id: 'down', label: 'Move down', to: i + 1 })
  return out
}

// passedRunning is the running entry a move from from to to puts the moved
// entry above, if any.
function passedRunning<T extends Pick<QueueEntry, 'state'>>(queue: readonly T[], from: number, to: number): T | undefined {
  return to < from ? queue.slice(Math.max(to, 0), from).find((e) => e.state === 'running') : undefined
}

// needsConfirm: the move puts the entry above a running one (Decision 9),
// so the Desk asks first.
export function needsConfirm(queue: readonly Pick<QueueEntry, 'state'>[], from: number, to: number): boolean {
  return passedRunning(queue, from, to) !== undefined
}

// confirmText is the question for a move above a running entry; '' when the
// move needs none.
export function confirmText(queue: readonly Pick<QueueEntry, 'iid' | 'state'>[], from: number, to: number): string {
  const r = passedRunning(queue, from, to)
  const m = queue[from]
  if (!r || !m) return ''
  return `Move %${m.iid} above %${r.iid}? %${r.iid}'s running issues finish, then %${m.iid} runs. %${r.iid} starts no new issues until %${m.iid} is done.`
}

// inOrder shows queue in a pending order of iids; entries the order does not
// name follow in their own order.
export function inOrder<T extends Pick<QueueEntry, 'iid'>>(queue: readonly T[], order: readonly number[]): T[] {
  const at = (e: T) => {
    const k = order.indexOf(e.iid)
    return k < 0 ? order.length : k
  }
  return queue.map((e, i) => ({ e, i })).sort((a, b) => at(a.e) - at(b.e) || a.i - b.i).map(({ e }) => e)
}

// reports: the queue shows the iids of order in that order (other entries
// may join or leave around them).
export function reports(queue: readonly Pick<QueueEntry, 'iid'>[], order: readonly number[]): boolean {
  const got = queue.map((e) => e.iid).filter((iid) => order.includes(iid))
  const want = order.filter((iid) => got.includes(iid))
  return got.length === want.length && got.every((iid, k) => iid === want[k])
}
