import type { PromptView, Stage, StreamEvent, StreamKind } from './types'

// Filter chips map to kinds sent as kinds= on GET /stream.
export type StreamFilter = 'all' | 'chat' | 'needs' | 'runs' | 'ci' | 'merged'

// History kinds only: chat rows collide on id, so they need an explicit kinds=.
export const historyStreamKinds: StreamKind[] = [
  'note',
  'run.started',
  'run.finished',
  'push',
  'ci',
  'review',
  'needs',
  'merged',
  'closed',
  'autopilot',
  'proposal',
  'prompt.posted',
  'prompt.answered',
  'prompt.closed',
]

export const promptStreamKinds: StreamKind[] = ['prompt.posted', 'prompt.answered', 'prompt.closed']

export const chatStreamKinds: StreamKind[] = ['chat.user', 'chat.agent']

export const streamFilters: { id: StreamFilter; label: string; kinds?: StreamKind[] }[] = [
  { id: 'all', label: 'All' },
  { id: 'chat', label: 'Chat', kinds: ['note', 'proposal', ...promptStreamKinds] },
  { id: 'needs', label: 'Needs you', kinds: ['needs'] },
  { id: 'runs', label: 'Runs', kinds: ['run.started', 'run.finished', 'push'] },
  { id: 'ci', label: 'CI & review', kinds: ['ci', 'review'] },
  { id: 'merged', label: 'Merged', kinds: ['merged', 'closed'] },
]

// kindsForFilter: with includeChat, "all" must name every kind (including
// chat.user/chat.agent); other chips stay as they are.
export function kindsForFilter(id: StreamFilter, includeChat = false): StreamKind[] | undefined {
  if (id === 'all') {
    return includeChat ? [...historyStreamKinds, ...chatStreamKinds] : undefined
  }
  return streamFilters.find((f) => f.id === id)?.kinds
}

// streamEventKey dedupes history and chat rows whose numeric ids collide.
export function streamEventKey(e: Pick<StreamEvent, 'id' | 'kind'>): string {
  return `${e.kind.startsWith('chat.') ? 'chat' : 'event'}:${e.id}`
}

export function filterForKind(kind: StreamKind): StreamFilter | null {
  for (const f of streamFilters) {
    if (f.kinds?.includes(kind)) return f.id
  }
  return null
}

// Project rail status from board stage counts.
export type ProjectDot = 'running' | 'needs-you' | 'idle'

export function projectDot(stages: Partial<Record<Stage, number>> | undefined): ProjectDot {
  if ((stages?.working ?? 0) > 0) return 'running'
  const needs =
    (stages?.['needs-input'] ?? 0) + (stages?.failed ?? 0) + (stages?.blocked ?? 0)
  if (needs > 0) return 'needs-you'
  return 'idle'
}

export function projectNeedsYou(stages: Partial<Record<Stage, number>> | undefined): number {
  return (stages?.['needs-input'] ?? 0) + (stages?.failed ?? 0) + (stages?.blocked ?? 0)
}

export function projectStateLine(
  stages: Partial<Record<Stage, number>> | undefined,
  autopilot?: { phase: string; title?: string; waitingFor?: number }[],
): string {
  if ((stages?.working ?? 0) > 0) {
    const waiting = autopilot?.find((a) => a.waitingFor)
    if (waiting?.waitingFor) return `running #${waiting.waitingFor}`
    return `running · ${stages!.working} task${stages!.working === 1 ? '' : 's'}`
  }
  const needs = projectNeedsYou(stages)
  if (needs > 0) {
    const paused = autopilot?.find((a) => a.phase === 'Paused')
    if (paused) return `paused · needs you`
    return `needs you · ${needs}`
  }
  return 'idle'
}

// Day divider label in the user's local timezone.
export function dayLabel(iso: string, now = Date.now()): string {
  const t = new Date(iso)
  if (Number.isNaN(t.getTime())) return ''
  const today = new Date(now)
  const startToday = new Date(today.getFullYear(), today.getMonth(), today.getDate())
  const startThat = new Date(t.getFullYear(), t.getMonth(), t.getDate())
  const diffDays = Math.round((startToday.getTime() - startThat.getTime()) / 86400000)
  const weekday = t.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' })
  if (diffDays === 0) return `Today · ${weekday}`
  if (diffDays === 1) return `Yesterday · ${weekday}`
  return weekday
}

export function sameLocalDay(a: string, b: string): boolean {
  const da = new Date(a)
  const db = new Date(b)
  if (Number.isNaN(da.getTime()) || Number.isNaN(db.getTime())) return false
  return (
    da.getFullYear() === db.getFullYear() &&
    da.getMonth() === db.getMonth() &&
    da.getDate() === db.getDate()
  )
}

// Cursor for "jump to end of day" on the activity strip.
export function dayEndCursor(dayYYYYMMDD: string): string {
  // Local end of day as RFC 3339; backend accepts t:<RFC3339>.
  const [y, m, d] = dayYYYYMMDD.split('-').map(Number)
  const end = new Date(y!, m! - 1, d!, 23, 59, 59, 999)
  return `t:${end.toISOString()}`
}

// mirroredPrompt matches the markers of the bot's comments that mirror a
// prompt (internal/marker Prompt) and its answer reply.
const mirroredPrompt = /<!-- ai-operator:prompt(?:-answer)?=Q\d+ /

// isMirroredPrompt: the note is the bot's mirror of a prompt (or of its
// answer); the stream shows the prompt card instead.
export function isMirroredPrompt(note: Pick<StreamEvent, 'kind' | 'data'>): boolean {
  return note.kind === 'note' && mirroredPrompt.test(note.data?.body ?? '')
}

// promptForEvent looks up the prompt a prompt.posted event is about in the
// milestone view's prompts; undefined when status dropped it (the event's
// summary line stands alone then).
export function promptForEvent(
  e: Pick<StreamEvent, 'kind' | 'data'>,
  prompts: PromptView[] | undefined,
): PromptView | undefined {
  if (e.kind !== 'prompt.posted') return undefined
  const id = e.data?.promptID
  return id ? prompts?.find((p) => p.id === id) : undefined
}

export type ThreadGroup = {
  kind: 'thread'
  key: string
  at: string
  notes: StreamEvent[]
}

export type StreamItem =
  | { kind: 'event'; event: StreamEvent }
  | ThreadGroup

// Group notes on the same issue/MR: one card at the newest, notes chrono inside.
export function groupThreads(events: StreamEvent[]): StreamItem[] {
  const noteKey = (e: StreamEvent): string | null => {
    if (e.kind !== 'note') return null
    const noteable = e.data.noteable ?? (e.mergeRequestIID ? 'mr' : 'issue')
    const iid = noteable === 'mr' ? e.mergeRequestIID : e.issueIID
    if (iid == null) return null
    return `${e.namespace}/${e.project}/${noteable}/${iid}`
  }

  events = events.filter((e) => !isMirroredPrompt(e))
  const groups = new Map<string, StreamEvent[]>()
  for (const e of events) {
    const k = noteKey(e)
    if (!k) continue
    const list = groups.get(k)
    if (list) list.push(e)
    else groups.set(k, [e])
  }

  const emitted = new Set<string>()
  const out: StreamItem[] = []
  for (const e of events) {
    const k = noteKey(e)
    if (!k) {
      out.push({ kind: 'event', event: e })
      continue
    }
    if (emitted.has(k)) continue
    emitted.add(k)
    const notes = groups.get(k)!
    // Newest-first stream: place the group where the newest note sits.
    const chrono = [...notes].sort((a, b) => Date.parse(a.at) - Date.parse(b.at) || a.id - b.id)
    out.push({ kind: 'thread', key: k, at: chrono[chrono.length - 1]!.at, notes: chrono })
  }
  return out
}

export function heatLevel(events: number, max: number): 0 | 1 | 2 | 3 {
  if (events <= 0 || max <= 0) return 0
  const r = events / max
  if (r < 0.25) return 1
  if (r < 0.5) return 2
  return 3
}
