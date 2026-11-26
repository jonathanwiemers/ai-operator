import type { ChatCommand } from './chatState'
import { cost, plural, type Tone } from './format.ts'
import type { Facts } from './generated/problem'
import type { LooseIssue, LooseIssues, LooseItem, LooseRoundView, PromptView, ProposalView } from './generated/views'
import type { CommandAnswer } from './pauseCard'
import { problemCause } from './problemFacts.ts'
import { splitPrompts } from './promptCard.ts'

// The Loose issues tab (R8 of "Loose issues", LooseIssues.vue): the queue's
// state badges and row actions, the selection of issues without a
// milestone, the search and the command texts; and the queue pilot's
// problem cards and round line (R6 of "a pilot for the queue").

// finished: the item left the queue, merged or not; it has no actions.
function finished(it: Pick<LooseItem, 'state'>): boolean {
  return it.state === 'Merged' || it.state === 'Left'
}

// itemState is an item's badge: Held while a hold stands on an unfinished
// item, Pending with its place among the pending items (the next to start
// is 1), else its state.
export function itemState(it: LooseItem, items: readonly LooseItem[]): { label: string; tone: Tone } {
  if (it.holdReason && !finished(it)) return { label: 'Held', tone: 'warning' }
  switch (it.state) {
    case 'Pending': {
      const pending = items.filter((x) => x.state === 'Pending' && !x.holdReason)
      return { label: `Pending · ${pending.indexOf(it) + 1}`, tone: 'muted' }
    }
    case 'Active':
      return { label: 'Active', tone: 'progress' }
    case 'Merged':
      return { label: 'Merged', tone: 'success' }
    case 'Left':
      return { label: 'Left', tone: 'neutral' }
    default:
      return { label: it.state, tone: 'neutral' }
  }
}

// itemReason is why an item is held or why it left, '' for neither.
export function itemReason(it: LooseItem): string {
  if (it.state === 'Left') return it.leftReason ?? ''
  return finished(it) ? '' : (it.holdReason ?? '')
}

export interface ItemAction {
  id: 'resume' | 'drop' | 'merge'
  label: string
  cmd: ChatCommand
}

// itemActions are an unfinished item's ⋯ menu: Resume while held, Drop,
// and Merge head while active with a pushed head (the sha the merge
// names, so a later push is not merged unseen).
export function itemActions(it: LooseItem): ItemAction[] {
  if (finished(it)) return []
  const out: ItemAction[] = []
  if (it.holdReason) out.push({ id: 'resume', label: 'Resume', cmd: { cmd: 'resume', issue: it.iid } })
  out.push({ id: 'drop', label: 'Drop', cmd: { cmd: 'drop', issue: it.iid } })
  if (it.state === 'Active' && it.headSHA) out.push({ id: 'merge', label: 'Merge head', cmd: { cmd: 'merge', issue: it.iid, sha: it.headSHA } })
  return out
}

// takeCommand hands the selected issues to the queue, in the order they
// were ticked; null for none.
export function takeCommand(iids: readonly number[]): ChatCommand | null {
  return iids.length ? { cmd: 'take', issues: [...iids] } : null
}

// selectionOrder keeps the ticked IIDs in the order they were ticked: the
// IIDs of prev still selected, then the newly selected in the order of
// rows (a "select all" ticks them top down). selected is the table's
// selection state, keyed by IID; issues in the queue never count.
export function selectionOrder(prev: readonly number[], selected: Readonly<Record<string, boolean>>, rows: readonly LooseIssue[]): number[] {
  const on = (iid: number) => selected[String(iid)] === true
  const takeable = new Set(rows.filter((r) => !r.inQueue).map((r) => r.iid))
  const kept = prev.filter((iid) => on(iid) && takeable.has(iid))
  const added = rows.filter((r) => !r.inQueue && on(r.iid) && !kept.includes(r.iid)).map((r) => r.iid)
  return [...kept, ...added]
}

// filterIssues are the issues the search matches, case-insensitive: by IID
// ("350" or "#350"), title, label or author. Every word must match.
export function filterIssues(issues: readonly LooseIssue[], q: string): LooseIssue[] {
  const words = q.toLowerCase().split(/\s+/).filter(Boolean)
  if (!words.length) return [...issues]
  return issues.filter((is) => {
    const hay = [String(is.iid), `#${is.iid}`, is.title, is.author, ...is.labels].map((s) => s.toLowerCase())
    return words.every((w) => hay.some((h) => h.includes(w)))
  })
}

// ownersNote names who may act on the queue, for a user who may not.
export function ownersNote(ownerSubjects: readonly string[]): string {
  const who = ownerSubjects.length ? ownerSubjects.join(', ') : 'nobody: the queue has no ownerSubjects'
  return `Only the queue's owners may take, drop, resume or merge issues here. Who may act: ${who}.`
}

// answerToast is the toast of a command's answer; null while it is pending.
// The operator's answer names each refused IID with its reason ("Not
// taken: #564 is in milestone …"), so the text is shown as it is.
// A take that refused some issues ("Not taken: …") is a warning; other
// answers are shown as information, their text says what happened.
export function answerToast(a: CommandAnswer): { title: string; description?: string; color: 'info' | 'warning' | 'error' } | null {
  switch (a.state) {
    case 'pending':
      return null
    case 'failed':
      return { title: 'The command failed', description: a.reason, color: 'error' }
    case 'answered':
      if (a.redacted) return { title: 'The operator answered', color: 'info' }
      return { title: 'The operator answered', description: a.text, color: /\bNot taken:/.test(a.text) ? 'warning' : 'info' }
  }
}

// roundLine is the pilot's round on the tab: "Round 3 · started 09:12 ·
// 2 of 5 issues · $1.40". The start is local time, with the date when the
// round did not start on now's day; a 0 issue limit is "no issue limit".
// No round (before the first) is ''.
export function roundLine(round: LooseRoundView | undefined, now: number): string {
  if (!round) return ''
  const t = new Date(round.startedAt)
  const parts = [`Round ${round.number}`]
  if (!Number.isNaN(t.getTime())) {
    const n = new Date(now)
    const clock = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' }).format(t)
    const today = t.toDateString() === n.toDateString()
    const year = t.getFullYear() === n.getFullYear() ? undefined : 'numeric'
    const date = today ? '' : `${new Intl.DateTimeFormat(undefined, { day: 'numeric', month: 'short', year }).format(t)}, `
    parts.push(`started ${date}${clock}`)
  }
  const issues = round.roundIssues > 0 ? round.roundIssues : round.finished
  const limit = round.roundIssues > 0 ? `${round.finished} of ${issues}` : `${issues}`
  parts.push(`${limit} ${plural(issues, 'issue')}${round.roundIssues > 0 ? '' : ', no issue limit'}`)
  parts.push(cost(round.costUSD))
  return parts.join(' · ')
}

// QueueProblem is one problem card of the queue: an issue of the queue with
// an open problem or open prompts (iid 0: the queue's own prompts).
// cause: the run's cause, else the item's hold reason.
export interface QueueProblem {
  iid: number
  title: string
  url?: string
  cause: string
  facts?: Facts
  prompts: PromptView[]
}

// queueNeeds groups the loose issues view into the tab's pinned cards: one
// per issue with problem facts or an open prompt, the queue's own first,
// then by IID; then the pilot's open proposals. count is both.
export function queueNeeds(v: Pick<LooseIssues, 'problems' | 'prompts' | 'proposals' | 'items'>): {
  problems: QueueProblem[]
  proposals: ProposalView[]
  count: number
} {
  const facts = v.problems ?? []
  const open = splitPrompts(v.prompts).open
  const iids = new Set([...facts.map((f) => f.iid), ...open.map((p) => p.issueIID ?? 0)])
  const problems = [...iids]
    .sort((a, b) => a - b)
    .map((iid): QueueProblem => {
      const f = facts.find((x) => x.iid === iid)
      const item = iid ? v.items.find((x) => x.iid === iid) : undefined
      const hold = item && !finished(item) ? (item.holdReason ?? '') : ''
      return {
        iid,
        title: iid ? (f?.issue.title ?? item?.title ?? '') : 'The queue',
        url: f?.issue.url ?? item?.webURL,
        cause: problemCause(f) ?? hold,
        facts: f,
        prompts: open.filter((p) => (p.issueIID ?? 0) === iid),
      }
    })
  const proposals = v.proposals ?? []
  return { problems, proposals, count: problems.length + proposals.length }
}

// looseAnchor is the element id of a card on the Loose issues tab
// (LooseNeeds.vue): an issue's problem card, the queue's own (iid 0) or a
// proposal of the queue's pilot. inboxRoute links them by the same names.
export function looseAnchor(card: { issue: number } | { proposal: number }): string {
  return 'issue' in card ? `loose-${card.issue}` : `loose-P${card.proposal}`
}
