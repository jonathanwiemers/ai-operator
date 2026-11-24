import { plural } from './format.ts'
import type { GitLabStatus, ProjectDetail, PromptView, Stage, StreamEvent, StreamKind } from './types'

// Filter chips map to kinds sent as kinds= on GET /stream.
export type StreamFilter = 'all' | 'chat' | 'needs' | 'runs' | 'ci' | 'merged'

// Every kind belongs to one tab of a milestone: Pilot (questions, pauses,
// what the pilot did, the owners' messages, decisions, the retrospective)
// or Stream (runs, pushes, CI, reviews, merges). Mirrors internal/ui/stream.go.
export const pilotStreamKinds = [
  'prompt.posted',
  'prompt.answered',
  'prompt.closed',
  'needs',
  'autopilot',
  'autopilot.paused',
  'autopilot.resumed',
  'proposal',
  'pilot.writeFailed',
  'note',
  'chat.user',
  'chat.agent',
  'retrospective',
  'release.decided',
  'release.started',
  'release.ended',
  'problem.opened',
  'problem.resolved',
  'hold.set',
  'hold.cleared',
  'pilot.acted',
] as const satisfies readonly StreamKind[]

export const runtimeStreamKinds = [
  'run.started',
  'run.finished',
  'push',
  'ci',
  'review',
  'merged',
  'closed',
  'item.skipped',
  'checkpoint',
] as const satisfies readonly StreamKind[]

// A StreamKind in neither list fails the type check here.
const coversStreamKind: Exclude<
  StreamKind,
  (typeof pilotStreamKinds)[number] | (typeof runtimeStreamKinds)[number]
> extends never
  ? true
  : false = true
void coversStreamKind

export const chatStreamKinds: StreamKind[] = ['chat.user', 'chat.agent']

// History kinds only: chat rows collide on id, so they need an explicit kinds=.
export const historyStreamKinds: StreamKind[] = [...pilotStreamKinds, ...runtimeStreamKinds].filter(
  (k) => !chatStreamKinds.includes(k),
)

export const promptStreamKinds: StreamKind[] = ['prompt.posted', 'prompt.answered', 'prompt.closed']

export const streamFilters: { id: StreamFilter; label: string; kinds?: StreamKind[] }[] = [
  { id: 'all', label: 'All' },
  { id: 'chat', label: 'Chat', kinds: ['note', 'proposal', ...promptStreamKinds] },
  { id: 'needs', label: 'Needs you', kinds: ['needs'] },
  { id: 'runs', label: 'Runs', kinds: ['run.started', 'run.finished', 'push', 'checkpoint'] },
  { id: 'ci', label: 'CI & review', kinds: ['ci', 'review'] },
  { id: 'merged', label: 'Merged', kinds: ['merged', 'closed', 'item.skipped'] },
]

// A milestone's Activity tab shows only runtime kinds, the pilot drawer the rest:
// its chips are the ones of runtime kinds (Runs, CI & review, Merged) and
// All names the runtime kinds (D230-P2).
const isRuntimeKind = (k: StreamKind) => (runtimeStreamKinds as readonly StreamKind[]).includes(k)
export const runtimeStreamFilters = streamFilters.filter((f) => !f.kinds || f.kinds.every(isRuntimeKind))

export function runtimeKindsForFilter(id: StreamFilter): StreamKind[] {
  const kinds = streamFilters.find((f) => f.id === id)?.kinds?.filter(isRuntimeKind)
  return kinds?.length ? kinds : [...runtimeStreamKinds]
}

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
    return `running · ${stages!.working} ${plural(stages!.working ?? 0, 'task')}`
  }
  const needs = projectNeedsYou(stages)
  if (needs > 0) {
    const paused = autopilot?.find((a) => a.phase === 'Paused')
    if (paused) return `paused · needs you`
    return `needs you · ${needs}`
  }
  return 'idle'
}

// "6 active · 8 settling"; a zero part is left out, '' when both are 0.
export function milestoneCounts(active?: number, settling?: number): string {
  return [active ? `${active} active` : '', settling ? `${settling} settling` : '']
    .filter(Boolean)
    .join(' · ')
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

type ThreadGroup = {
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

// heatCellLabel is a heatmap cell's aria-label and tooltip: the date as the
// heatmap shows it (locale, short month) and the count, "12 Nov: 3 events" (the count is the stream events the cell colours by).
export function heatCellLabel(day: string, events: number, locale?: string): string {
  const date = new Date(day + 'T12:00:00').toLocaleDateString(locale, { day: 'numeric', month: 'short' })
  return `${date}: ${events} ${plural(events, 'event')}`
}

// heatSummary is the heatmap's text: the total and the busiest day.
export function heatSummary(days: { day: string; events: number }[], locale?: string): string {
  const total = days.reduce((a, d) => a + d.events, 0)
  if (!days.length || total === 0) return 'No activity in this range'
  const top = days.reduce((a, d) => (d.events > a.events ? d : a))
  return `${total} ${plural(total, 'event')} in ${days.length} days; busiest day ${heatCellLabel(top.day, top.events, locale)}`
}

// Stream notes (the Composer's note mode, docs/contributing/web-ui.md "Composer"): a comment
// on a milestone's tracking issue as /pilot …, or on a task's issue.
export type NoteTarget =
  | { type: 'pilot'; milestoneIID: number; title: string; trackingIssueIID?: number }
  | { type: 'task'; task: string; issueIID: number; title: string }

// noteTargets are the pilots of the milestones with a running or paused
// autopilot, the preferred milestone's first (also without one), then the
// project's tasks with an issue.
export function noteTargets(project: ProjectDetail | null, preferMilestone?: number): NoteTarget[] {
  if (!project) return []
  const out: NoteTarget[] = []
  const ms = project.milestones
  const ordered = preferMilestone
    ? [...ms.filter((m) => m.iid === preferMilestone), ...ms.filter((m) => m.iid !== preferMilestone)]
    : ms
  for (const m of ordered) {
    if (!m.trackingIssueIID && !m.spec) continue
    const phase = m.spec?.status.autopilot?.phase
    if (preferMilestone !== m.iid && phase !== 'Running' && phase !== 'Paused') continue
    out.push({ type: 'pilot', milestoneIID: m.iid, title: m.title, trackingIssueIID: m.trackingIssueIID })
  }
  for (const t of [...ms.flatMap((m) => m.tasks), ...project.unplanned]) {
    if (t.issue) out.push({ type: 'task', task: t.name, issueIID: t.issue.iid, title: t.issue.title })
  }
  return out
}

function sameNoteTarget(a: NoteTarget, b: NoteTarget): boolean {
  if (a.type === 'pilot' && b.type === 'pilot') return a.milestoneIID === b.milestoneIID
  if (a.type === 'task' && b.type === 'task') return a.task === b.task
  return false
}

// keepNoteTarget keeps the picked target while it is still offered, else
// takes the first.
export function keepNoteTarget(list: NoteTarget[], current: NoteTarget | null): NoteTarget | null {
  if (!list.length) return null
  if (current && list.some((t) => sameNoteTarget(t, current))) return current
  return list[0]!
}

// noteDisabledReason is why the composer cannot post: GitLab is not
// connected (or loading), the roles may not comment, or there is no
// target. Empty when it can post.
export function noteDisabledReason(status: GitLabStatus | null, steer: boolean | undefined, targets: number): string {
  if (!status) return 'Loading…'
  if (!status.connected && status.login) return 'Connect GitLab to post.'
  if (!status.connected) return 'GitLab is not set up for your user here.'
  if (steer === false) return 'Your roles may not post comments on this project.'
  if (!targets) return 'No pilot or issue to post to yet.'
  return ''
}

export function noteHint(t: NoteTarget | null): string {
  if (!t) return 'Pick where to post'
  if (t.type === 'pilot') {
    const track = t.trackingIssueIID ? `tracking issue #${t.trackingIssueIID}` : `milestone %${t.milestoneIID}`
    return `Posts on ${track} as /pilot … in your GitLab name`
  }
  return `Comment on #${t.issueIID}; start with /agent to start a run`
}

export function noteTargetLabel(t: NoteTarget | null): string {
  if (!t) return '→ Pick target'
  if (t.type === 'pilot') return `→ Pilot · ${t.title}`
  return `→ #${t.issueIID} ${t.title}`
}

export function notePlaceholder(t: NoteTarget | null): string {
  return t?.type === 'pilot' ? 'Message the pilot…  /  for commands' : 'Comment…  /  for commands'
}

export interface SlashSuggestion {
  cmd: string
  hint: string
}

export const slashSuggestions: SlashSuggestion[] = [
  { cmd: '/pilot pause', hint: 'stop starting and merging' },
  { cmd: '/pilot resume', hint: 'resume starting and merging' },
  { cmd: '/pilot skip #', hint: 'skip a planned issue' },
  { cmd: '/agent use claude opus high', hint: 'model for the target issue' },
  { cmd: '/agent effort high', hint: 'effort for the next run' },
  { cmd: '/agent revisit ', hint: 'change a recorded decision' },
  { cmd: 'yes P', hint: "accept the pilot's proposal" },
]

// slashOpens: the commands menu shows while the body is a lone /word or
// the start of "yes P".
export function slashOpens(body: string): boolean {
  return /^\/\S*$/.test(body) || /^yes P?$/.test(body)
}

// slashPick is the body after picking a command: a space to go on, unless
// it ends in one or in "#".
export function slashPick(cmd: string): string {
  return cmd.endsWith(' ') || cmd.endsWith('#') ? cmd : `${cmd} `
}

// The % reference menu's opening and picking, beside the / menu's.
export { refItems, refPick, refQuery, type RefItem, type RefItemSource } from './refMenu.ts'

// Cockpit (R9): every kind is pinned (open problems, proposals and
// questions; the open card is pinned from the views, the event stays in the
// time stream as its history), shown, or folded into one-line groups. A
// Record over StreamKind fails the type check on a new kind.
type CockpitClass = 'pinned' | 'shown' | 'folded'

export const cockpitClass: Record<StreamKind, CockpitClass> = {
  'prompt.posted': 'pinned',
  proposal: 'pinned',
  'problem.opened': 'pinned',
  needs: 'pinned',
  'pilot.acted': 'shown',
  'pilot.writeFailed': 'shown',
  'chat.user': 'shown',
  'chat.agent': 'shown',
  note: 'shown',
  merged: 'shown',
  closed: 'shown',
  autopilot: 'shown',
  'autopilot.paused': 'shown',
  'autopilot.resumed': 'shown',
  'release.decided': 'shown',
  'release.started': 'shown',
  'release.ended': 'shown',
  retrospective: 'shown',
  'problem.resolved': 'shown',
  'prompt.answered': 'shown',
  'prompt.closed': 'shown',
  'hold.set': 'shown',
  'hold.cleared': 'shown',
  'item.skipped': 'shown',
  'run.started': 'folded',
  'run.finished': 'folded',
  push: 'folded',
  ci: 'folded',
  review: 'folded',
  checkpoint: 'folded',
}

// Across all projects only merges and milestone state are shown.
const allScopeShown: StreamKind[] = [
  'merged',
  'closed',
  'autopilot',
  'autopilot.paused',
  'autopilot.resumed',
  'release.decided',
  'release.started',
  'release.ended',
  'retrospective',
]

type CockpitScope = 'milestone' | 'project' | 'all'

export type CockpitGroup = {
  type: 'group'
  key: string
  namespace: string
  project: string
  milestoneIID?: number
  issueIID?: number
  from: string
  to: string
  events: StreamEvent[]
  summary: string
}

export type CockpitItem =
  | { type: 'event'; event: StreamEvent }
  | { type: 'thread'; key: string; at: string; notes: StreamEvent[] }
  | CockpitGroup

// cockpitItems sorts a stream (newest first, as GET /stream returns it)
// into the Cockpit's items, oldest first: shown events, note threads, and
// groups of the folded events of one key between two shown events. The key
// is (milestone, issue), or the project with scope 'all'.
export function cockpitItems(events: StreamEvent[], opts: { scope?: CockpitScope } = {}): CockpitItem[] {
  const scope = opts.scope ?? 'milestone'
  const shown = (k: StreamKind) => (scope === 'all' ? allScopeShown.includes(k) : cockpitClass[k] !== 'folded')
  const out: CockpitItem[] = []
  let open = new Map<string, CockpitGroup>()
  const flush = () => {
    const groups = [...open.values()]
    for (const g of groups) g.summary = groupSummary(g.events, scope === 'all' ? g.project : undefined)
    out.push(...groups)
    open = new Map()
  }
  const fold = (e: StreamEvent) => {
    const key =
      scope === 'all'
        ? `${e.namespace}/${e.project}`
        : `${e.namespace}/${e.project}/${e.milestoneIID ?? ''}/${e.issueIID ?? ''}`
    const g = open.get(key)
    if (g) {
      g.events.push(e)
      g.to = e.at
      return
    }
    open.set(key, {
      type: 'group',
      key,
      namespace: e.namespace,
      project: e.project,
      ...(scope !== 'all' && { milestoneIID: e.milestoneIID, issueIID: e.issueIID }),
      from: e.at,
      to: e.at,
      events: [e],
      summary: '',
    })
  }

  for (const item of groupThreads(events).reverse()) {
    if (item.kind === 'thread') {
      if (scope === 'all') item.notes.forEach(fold)
      else {
        flush()
        out.push({ type: 'thread', key: item.key, at: item.at, notes: item.notes })
      }
    } else if (shown(item.event.kind)) {
      flush()
      out.push({ type: 'event', event: item.event })
    } else fold(item.event)
  }
  flush()
  return out
}

// groupSummary is a folded group's line, "#712 · 3 runs, 2 CI rounds,
// review clean": runs count run.started, CI rounds ci; the review is the
// last one's verdict. Parts with zero are left out. The prefix is the
// project (scope 'all'), else the issue, else the milestone.
export function groupSummary(events: StreamEvent[], projectName?: string): string {
  const count = (k: StreamKind) => events.filter((e) => e.kind === k).length
  const parts: string[] = []
  const runs = count('run.started')
  if (runs) parts.push(`${runs} ${plural(runs, 'run')}`)
  const pushes = count('push')
  if (pushes) parts.push(`${pushes} ${plural(pushes, 'push', 'pushes')}`)
  const ci = count('ci')
  if (ci) parts.push(`${ci} CI ${plural(ci, 'round')}`)
  const review = [...events].reverse().find((e) => e.kind === 'review')
  if (review) {
    const blocking = review.data?.blocking ?? 0
    if (blocking > 0) parts.push(`review: ${blocking} ${plural(blocking, 'finding')}`)
    else if (review.data?.reviewErrors) parts.push('review with errors')
    else parts.push('review clean')
  }
  const checkpoints = count('checkpoint')
  if (checkpoints) parts.push(`${checkpoints} ${plural(checkpoints, 'checkpoint')}`)
  if (!parts.length) parts.push(`${events.length} ${plural(events.length, 'event')}`)

  const first = events[0]
  const prefix =
    projectName ??
    (first?.issueIID != null ? `#${first.issueIID}` : first?.milestoneIID != null ? `%${first.milestoneIID}` : first?.project)
  return prefix ? `${prefix} · ${parts.join(', ')}` : parts.join(', ')
}

// cockpitStreamQuery is GET /stream's query for a Cockpit's scope (one
// milestone, a project's milestones, or everything without a namespace):
// all kinds, the chat kinds only with the chat feature (their ids collide
// with history ids, so they are named explicitly), the search, a cursor.
export function cockpitStreamQuery(
  scope: { namespace?: string; name?: string; milestoneIID?: number },
  opts: { chat?: boolean; q?: string; before?: string; after?: string } = {},
): URLSearchParams {
  const params = new URLSearchParams()
  if (scope.namespace && scope.name) {
    params.set('project', `${scope.namespace}/${scope.name}`)
    if (scope.milestoneIID) params.set('milestone', String(scope.milestoneIID))
  }
  params.set('kinds', [...historyStreamKinds, ...(opts.chat ? chatStreamKinds : [])].join(','))
  const q = opts.q?.trim()
  if (q) params.set('q', q)
  if (opts.before) params.set('before', opts.before)
  if (opts.after) params.set('after', opts.after)
  params.set('limit', '50')
  return params
}
