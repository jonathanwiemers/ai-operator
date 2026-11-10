import type { ChatCommand } from './chatState'
import type { ChatMessage, ExitStatus, ExitView, PauseKind, PromptView, ReviewError } from './types'

// PauseActionID names one button of the pause card.
export type PauseActionID =
  | 'review'
  | 'retry'
  | 'merge'
  | 'skip'
  | 'resume'
  | 'openChat'
  | 'openPipeline'
  | 'openMR'
  | 'openCompanion'
  | 'openPlan'

// PauseAction is one button of the pause card. target says where it goes:
// a command to the paused issue's implement task, one to the milestone's
// Spec task, or a link.
export interface PauseAction {
  id: PauseActionID
  label: string
  target: 'implement' | 'spec' | 'link'
  // confirm: ask before sending; ownerOnly: only for users who may send it.
  confirm?: boolean
  ownerOnly?: boolean
}

const review: PauseAction = { id: 'review', label: 'Review again', target: 'implement' }
const retry: PauseAction = { id: 'retry', label: 'Retry', target: 'implement' }
const merge: PauseAction = {
  id: 'merge',
  label: 'Merge anyway',
  target: 'spec',
  confirm: true,
  ownerOnly: true,
}
const skip: PauseAction = { id: 'skip', label: 'Skip', target: 'spec' }
const resume: PauseAction = { id: 'resume', label: 'Resume', target: 'spec' }
const openChat: PauseAction = { id: 'openChat', label: 'Open chat', target: 'link' }
const openPipeline: PauseAction = { id: 'openPipeline', label: 'Open pipeline', target: 'link' }
const openMR: PauseAction = { id: 'openMR', label: 'Open MR', target: 'link' }
const openCompanion: PauseAction = { id: 'openCompanion', label: 'Open companion MR', target: 'link' }
const openPlan: PauseAction = { id: 'openPlan', label: 'Open plan', target: 'link' }

// The actions per kind (spec desk-pause-card, section 3), in button order.
// PauseKind is a string in Go (its consts are not named after the type, so
// tygo emits no union): the kinds the card knows are the keys here.
const actionsByKind = {
  reviewErrors: [review, merge, skip],
  notReviewed: [review, merge, skip],
  findings: [retry, openChat, skip],
  runFailed: [retry, openChat, skip],
  needsInput: [retry, openChat, skip],
  ciRed: [retry, openPipeline, skip],
  ciCanceled: [retry, openPipeline, skip],
  mergeRequest: [openMR, resume, skip],
  cycle: [openPlan, skip],
  stuck: [openPlan, skip],
  owner: [resume],
  mainRed: [resume],
  config: [resume],
  // Resume would pause again at once: the owner raises the budget (the
  // milestone's line, or the issue's with a comment).
  budget: [],
  // Its way out is a milestone description change, a GitLab action: the
  // Way out links the milestone.
  emptyPlan: [],
  // Retry is Resume (the final release once more); ending without a
  // release is /agent endMilestone on the tracking issue, a remedy.
  release: [openPipeline, resume],
  // The companion merge request cannot go on: fixed in GitLab (rebase,
  // discussions, approval), then Resume; or the issue skipped.
  companion: [openCompanion, resume, skip],
} satisfies Record<PauseKind, PauseAction[]>

type KnownPauseKind = keyof typeof actionsByKind

function known(kind: string | undefined): kind is KnownPauseKind {
  return !!kind && Object.prototype.hasOwnProperty.call(actionsByKind, kind)
}

// pauseActions lists the card's action row for a pause; a pause without a
// kind (from before the field existed) or with an unknown one offers Resume
// only. issue labels the chat link ("Open #N chat"). With exits (an operator
// that sends them) the row keeps the kind's links: its command buttons are
// the Way out's remedies (wayOut), so none shows twice.
export function pauseActions(kind: PauseKind | undefined, issue?: number, exits?: ExitView[]): PauseAction[] {
  let actions = known(kind) ? actionsByKind[kind] : [resume]
  if (exits?.length) actions = actions.filter((a) => a.target === 'link')
  return actions.map((a) =>
    a.id === 'openChat' && issue ? { ...a, label: `Open #${issue} chat` } : { ...a },
  )
}

// The Desk command each remedy command word sends (chatState's ChatCommand);
// other words (rebase, mergeBase, unskip, startAnyway) are text.
const remedyActions: Record<string, PauseAction> = { resume, skip, merge, retry, review }

// Button labels of remedies whose command word says less than the exit:
// retryRelease sends resume.
const remedyLabels: Partial<Record<string, string>> = { retryRelease: 'Retry release' }

// deskCommand: the card can send the remedy command word (with the grants).
export function deskCommand(cmd: string | undefined): boolean {
  return !!cmd && Object.prototype.hasOwnProperty.call(remedyActions, cmd)
}

export type ExitTone = 'success' | 'progress' | 'muted'

// WayOutAuto is an automatic exit with its live status chip.
export interface WayOutAuto {
  id: string
  when: string
  does: string
  status: ExitStatus
  chip: string
  tone: ExitTone
  detail: string
}

// WayOutRemedy is one of the owner's remedies. kind says how it is taken:
// button (action set: the viewer may send it from the card), command (a
// command word to send elsewhere), label, gitlab (an action in GitLab) or
// prompt (an answer to the pilot's prompt). text is the plain-text
// instruction (for a button: what it needs first, often ''); elsewhere says
// where the command word goes when not as a button ("on the tracking
// issue"); link names the GitLab page it happens on.
export interface WayOutRemedy {
  id: string
  kind: 'button' | 'command' | 'label' | 'gitlab' | 'prompt'
  text: string
  does: string
  command?: string
  elsewhere?: string
  action?: PauseAction
  link?: 'milestone' | 'mergeRequest' | 'issue' | 'companion'
}

export interface WayOut {
  auto: WayOutAuto[]
  remedies: WayOutRemedy[]
}

const chips: Record<ExitStatus, { chip: string; tone: ExitTone }> = {
  met: { chip: 'lifts on the next poll', tone: 'success' },
  inProgress: { chip: 'in progress', tone: 'progress' },
  waiting: { chip: 'waiting', tone: 'muted' },
  unknown: { chip: 'not known', tone: 'muted' },
}

// GitLab pages of the GitLab actions and labels.
const exitLinks: Partial<Record<string, WayOutRemedy['link']>> = {
  changePlan: 'milestone',
  changeSpec: 'milestone',
  fixInGitLab: 'mergeRequest',
  fixCompanion: 'companion',
  removeLink: 'issue',
  answered: 'issue',
}

// statusOf is a status the card knows, unknown for others (a newer operator).
function statusOf(status: string | undefined): ExitStatus {
  return status && Object.prototype.hasOwnProperty.call(chips, status) ? (status as ExitStatus) : 'unknown'
}

// remedy turns a remedy exit into its instruction; used names the Desk
// actions already given, so a second exit with the same command is text.
function remedy(e: ExitView, used: Set<PauseActionID>): WayOutRemedy {
  const when = (e.when || '').trim()
  const base = { id: e.id, does: (e.does || '').trim() }
  const then = e.command ? `, then ${e.command}` : ''
  if (e.label) {
    return { ...base, kind: 'label', text: `Set the ${e.label} label${when ? ` ${when}` : ''}${then}`, link: exitLinks[e.id] }
  }
  if (e.gitlab) {
    return { ...base, kind: 'gitlab', text: `In GitLab: ${when}${then}`, link: exitLinks[e.id] }
  }
  if (e.prompt) {
    return { ...base, kind: 'prompt', text: `Answer the pilot's prompt${when ? ` ${when}` : ''}` }
  }
  const command = (e.command || '').trim()
  const where = e.target === 'implement' ? 'on the issue' : 'on the tracking issue'
  const elsewhere = `${where}${when ? `, ${when}` : ''}`
  const a = deskCommand(command) ? remedyActions[command] : undefined
  if (e.allowed && a && !used.has(a.id)) {
    used.add(a.id)
    const label = remedyLabels[e.id] ?? a.label
    return { ...base, kind: 'button', text: capitalise(when), command, elsewhere, action: { ...a, label } }
  }
  return { ...base, kind: 'command', text: elsewhere, command, elsewhere }
}

// wayOut groups a pause's exits: the automatic ones with their status chip
// (met: lifts on the next poll), and the remedies, as buttons where the
// viewer's grants allow them (allowed) and the Desk can send them, else as
// text.
export function wayOut(exits: ExitView[] | undefined): WayOut {
  const out: WayOut = { auto: [], remedies: [] }
  const used = new Set<PauseActionID>()
  for (const e of exits || []) {
    if (e.auto) {
      const status = statusOf(e.status)
      out.auto.push({
        id: e.id,
        when: capitalise((e.when || '').trim()),
        does: (e.does || '').trim(),
        status,
        ...chips[status],
        detail: (e.detail || '').trim(),
      })
    } else {
      out.remedies.push(remedy(e, used))
    }
  }
  return out
}

// wayOutLine is the compact card's line: the first automatic exit with its
// status, and the number of remedies; '' without exits.
export function wayOutLine(exits: ExitView[] | undefined): string {
  const w = wayOut(exits)
  const parts: string[] = []
  const first = w.auto[0]
  if (first) parts.push(`${first.when} — ${first.chip}`)
  const n = w.remedies.length
  if (n) parts.push(`${n} ${n === 1 ? 'remedy' : 'remedies'}`)
  return parts.length ? `Way out: ${parts.join(' · ')}` : ''
}

// pausePromptKinds: the pause kinds that need a choice, and the kind of the
// prompt that asks it.
const pausePromptKinds: Partial<Record<string, string>> = {
  mainRed: 'mainHealth',
  budget: 'budget',
}

// pausePrompt is the open prompt a pause shows instead of its bare actions:
// the newest open mainHealth prompt for mainRed, budget for budget. Other
// kinds, and a pause without one (not posted yet, or answered), get
// undefined and keep the card's actions.
export function pausePrompt(kind: PauseKind | undefined, prompts: PromptView[] | undefined): PromptView | undefined {
  const want = kind ? pausePromptKinds[kind] : undefined
  if (!want) return undefined
  return (prompts || []).find((p) => p.kind === want && (!p.state || p.state === 'open'))
}

export interface PauseContext {
  // issue: the paused issue; head: its pushed SHA.
  issue?: number
  head?: string
}

// pauseCommand is the chat command an action sends, or null for links and
// for skip/merge without the issue (and head) they need.
export function pauseCommand(action: PauseAction, ctx: PauseContext): ChatCommand | null {
  const issue = ctx.issue && ctx.issue > 0 ? ctx.issue : 0
  const head = (ctx.head || '').trim()
  switch (action.id) {
    case 'retry':
      return { cmd: 'retry' }
    case 'review':
      return { cmd: 'review' }
    case 'resume':
      return { cmd: 'resume' }
    case 'skip':
      return issue ? { cmd: 'skip', issue } : null
    case 'merge':
      return issue && head ? { cmd: 'merge', issue, sha: head } : null
    default:
      return null
  }
}

// pauseEndsBy says what ends the pause without a human; '' when it needs one.
export function pauseEndsBy(kind: PauseKind | undefined, issue?: number): string {
  const run = issue ? `a later run of #${issue}` : 'a later run'
  const gate = `Continues by itself once ${run} passes the gate.`
  switch (kind) {
    case 'needsInput':
    case 'runFailed':
    case 'findings':
    case 'notReviewed':
    case 'ciRed':
    case 'ciCanceled':
      return gate
    case 'reviewErrors':
      return `${gate} After a transient reviewer failure one re-review runs by itself.`
    case 'mergeRequest':
      return 'Continues by itself when the MR is merged by hand.'
    case 'mainRed':
      return 'Continues by itself when main is green.'
    case 'budget':
      return issue
        ? `Continues by itself once the budget of #${issue} is raised above its spend (\`/agent budget <usd>\` on the issue).`
        : "Continues by itself once the milestone's budget line is raised above the spend."
    case 'emptyPlan':
      return 'Continues once the milestone description is changed and planned again.'
    case 'release':
      return "Continues by itself once a tag contains the milestone's last merge."
    case 'companion':
      return 'Continues by itself once the companion MR can be merged again, or is merged by hand.'
    default:
      return ''
  }
}

const titles: Record<KnownPauseKind, string> = {
  owner: 'Paused by an owner',
  cycle: 'The plan has a dependency cycle',
  stuck: 'No issue can start',
  needsInput: 'The agent needs input',
  runFailed: 'The run failed',
  findings: 'Review findings are open',
  reviewErrors: 'Reviewers could not run',
  notReviewed: 'Not reviewed',
  ciRed: 'CI is red',
  ciCanceled: 'CI was canceled',
  mergeRequest: 'The MR cannot be merged',
  mainRed: 'main is red',
  config: 'Configuration problem',
  budget: 'The budget is used',
  emptyPlan: 'Nothing was planned',
  companion: 'The companion MR cannot go on',
  release: 'The final release failed twice',
}

// pauseTitle is the card's heading for a pause.
export function pauseTitle(kind: PauseKind | undefined): string {
  return known(kind) ? titles[kind] : 'Autopilot paused'
}

// pauseExplain is the card's short explanation of a kind, '' for kinds the
// title and reason explain.
export function pauseExplain(kind: PauseKind | undefined): string {
  return kind === 'release'
    ? "The final release pipeline failed, and its retry too. Retry it, or end the milestone without a release; a tag that contains the milestone's last merge ends the pause by itself."
    : ''
}

// agentCommand is how a remedy's command word is typed on GitLab:
// "/agent endMilestone".
export function agentCommand(word: string | undefined): string {
  const w = (word || '').trim()
  return w ? `/agent ${w}` : ''
}

// pauseRelease is the release record a release pause is about: the newest
// one the operator decided as final (by id), undefined for other kinds or
// without one.
export function pauseRelease<R extends { id: number; by: string }>(
  kind: PauseKind | undefined,
  releases: readonly R[] | null | undefined,
): R | undefined {
  if (kind !== 'release') return undefined
  let out: R | undefined
  for (const r of releases ?? []) if (r.by === 'final' && (!out || r.id > out.id)) out = r
  return out
}

const dayMonth = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short' })

function capitalise(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1)
}

// reviewLimitLines says why the reviewers failed, one line per limited
// engine or failed reviewer: "Cursor is limited until 13 Nov",
// "bugbot failed transiently". A limited engine with several resets shows
// the latest.
export function reviewLimitLines(
  errors: ReviewError[] | undefined,
  fmt: (d: Date) => string = (d) => dayMonth.format(d),
): string[] {
  const order: string[] = []
  const limits = new Map<string, number | undefined>()
  const failures = new Map<string, string>()
  for (const e of errors || []) {
    if (e.kind === 'usageLimit') {
      const name = capitalise(e.engine || e.reviewer)
      const key = `limit:${name}`
      const t = e.resetAt ? Date.parse(e.resetAt) : NaN
      const reset = Number.isNaN(t) ? undefined : t
      if (!limits.has(key)) {
        order.push(key)
        limits.set(key, reset)
      } else if (reset !== undefined && (limits.get(key) ?? -Infinity) < reset) {
        limits.set(key, reset)
      }
      continue
    }
    const key = `fail:${e.reviewer}`
    const line = e.kind === 'transient' ? `${e.reviewer} failed transiently` : `${e.reviewer} failed`
    if (!failures.has(key)) {
      order.push(key)
      failures.set(key, line)
    } else if (e.kind !== 'transient') {
      // A lasting failure says more than a transient one of the same reviewer.
      failures.set(key, line)
    }
  }
  return order.map((key) => {
    if (failures.has(key)) return failures.get(key) as string
    const name = key.slice('limit:'.length)
    const reset = limits.get(key)
    return reset === undefined ? `${name} is limited` : `${name} is limited until ${fmt(new Date(reset))}`
  })
}

// CommandAnswer is what became of a command the card sent: no answer yet,
// the operator's reply (text, or redacted without the conversation grant),
// or a failed command row with its reason.
export type CommandAnswer =
  | { state: 'pending' }
  | { state: 'answered'; text: string; redacted: boolean }
  | { state: 'failed'; reason: string }

// commandAnswer finds the answer to command id in a page of its task's chat.
export function commandAnswer(messages: ChatMessage[] | undefined, id: number): CommandAnswer {
  const list = messages || []
  const reply = list.find((m) => m.replyTo === id)
  if (reply) {
    const text = (reply.body || '').trim()
    return { state: 'answered', text: reply.redacted ? '' : text, redacted: !!reply.redacted || !text }
  }
  const row = list.find((m) => m.id === id)
  if (row?.state === 'failed') return { state: 'failed', reason: (row.reason || '').trim() || 'The command failed.' }
  return { state: 'pending' }
}
