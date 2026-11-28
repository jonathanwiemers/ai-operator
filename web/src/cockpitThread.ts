import { liveLines, type LiveLine } from './cockpitLive.ts'
import type { Tone } from './format.ts'
import type { MilestoneIssueRow, QueueItem, Task } from './generated/views.ts'
import { panelIssueBadge, panelIssueState } from './milestoneRows.ts'
import type { CockpitGroup, CockpitItem, CockpitScope, CockpitThread } from './stream.ts'

// The header of a Cockpit thread (R4 of "Desk: the Cockpit newest first by
// issue"): "#712 · Add the provider · Running · Implementing · 4 min".

export interface ThreadHeaderContext {
  scope?: CockpitScope
  // issues and items: the milestone's issue rows and queue items (the
  // milestone page); the project page has neither.
  issues?: readonly Pick<MilestoneIssueRow, 'iid' | 'title' | 'state'>[]
  items?: readonly Pick<QueueItem, 'iid' | 'lane'>[]
  // tasks: the implement tasks of the milestone, or of all the project's
  // milestones; their runs give the step, their issues a title.
  tasks?: readonly Task[]
  // milestones: the project's milestone titles by IID (project scope).
  milestones?: Readonly<Record<number, string>>
  // status: the milestone's status (the page's eyebrow), the Milestone
  // thread's state.
  status?: { label: string; tone: Tone }
  now: number
}

export interface ThreadHeader {
  // ref: "#712", "Milestone", or the project with scope 'all' (and on the
  // project page for events of neither).
  ref: string
  title: string
  state?: { label: string; tone: Tone }
  // step: "Implementing · 4 min" while the issue's implementer runs.
  step?: string
  milestoneTitle?: string
  // live: the issue's live run, for its LiveTranscript.
  live?: LiveLine
  // label: the thread's accessible name, its ref and title.
  label: string
  // anchor: "issue-712", the thread's id for #issue-<iid> links.
  anchor?: string
}

const capital = (s: string) => s.charAt(0).toUpperCase() + s.slice(1)

// threadHeader is a thread's header. An issue's title comes from the
// milestone's issue rows, else its task, else stays empty (D818-P2); its
// state is panelIssueState's from its row and queue lane, or "Running"
// without a row while it has a live run.
export function threadHeader(t: CockpitThread, ctx: ThreadHeaderContext): ThreadHeader {
  const scope = ctx.scope ?? 'milestone'
  if (scope === 'all') return { ref: t.project, title: '', label: t.project }
  const milestoneTitle =
    scope === 'project' && t.milestoneIID != null ? ctx.milestones?.[t.milestoneIID] || `%${t.milestoneIID}` : undefined
  const iid = t.issueIID
  if (iid == null) {
    if (scope === 'project' && !milestoneTitle) return { ref: t.project, title: '', label: t.project }
    const state = scope === 'milestone' ? ctx.status : undefined
    return { ref: 'Milestone', title: '', state, milestoneTitle, label: milestoneTitle ? `Milestone · ${milestoneTitle}` : 'Milestone' }
  }
  const ref = `#${iid}`
  const row = ctx.issues?.find((i) => i.iid === iid)
  const title = row?.title || ctx.tasks?.find((x) => x.issue?.iid === iid)?.issue?.title || ''
  const live = liveLines(ctx.tasks, ctx.now).find((l) => l.iid === iid)
  const lane = ctx.items?.find((q) => q.iid === iid)?.lane
  const s = row ? panelIssueState(row.state, lane) : live ? 'running' : undefined
  return {
    ref,
    title,
    state: s && panelIssueBadge(s),
    step: live && capital(live.text.replace(/^#\d+\s+/, '')),
    milestoneTitle,
    live,
    label: [ref, title, milestoneTitle].filter(Boolean).join(' · '),
    anchor: `issue-${iid}`,
  }
}

// EventPlace is where an event sits in a thread's items: the item's index
// (at or past the thread's newest, behind Show N more) and, inside a folded
// group, that group.
export interface EventPlace {
  index: number
  group?: CockpitGroup
}

// eventPlace finds the event with id among a thread's items, the target of
// an #event-<id> link (D906-P4): a shown event or one in a group, never a
// note thread's. Null when the thread does not hold it.
export function eventPlace(items: readonly CockpitItem[], id: number): EventPlace | null {
  for (const [index, item] of items.entries()) {
    if (item.type === 'event' && item.event.id === id) return { index }
    if (item.type === 'group' && item.events.some((e) => e.id === id)) return { index, group: item }
  }
  return null
}
