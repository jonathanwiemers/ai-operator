// Pure helpers for the plans list (title fallback, phase badge tone,
// milestone link, sections, request path).
import type { AgentPlanPhase } from './generated/api'
import type { PlanListItem } from './generated/views'
import { cost, type Tone } from './format.ts'
import { milestoneName } from './milestoneName.ts'

// planTitle is the plan's title, or "Untitled plan · <date>" when empty
// (D448-P1).
export function planTitle(title: string, createdAt: string): string {
  const t = title.trim()
  if (t) return t
  const d = new Date(createdAt)
  const when = Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString()
  return when ? `Untitled plan · ${when}` : 'Untitled plan'
}

// planPhaseTone maps an AgentPlan phase to a Desk badge tone.
export function planPhaseTone(phase: AgentPlanPhase | undefined): Tone {
  switch (phase) {
    case 'Working':
      return 'progress'
    case 'Starting':
    case 'Ready':
      return 'info'
    case 'Created':
      return 'success'
    case 'Failed':
      return 'danger'
    case 'Idle':
      return 'muted'
    default:
      return 'muted'
  }
}

// planSpendLabel is the header's "spend of cap", e.g. "$1.20 of $5.00".
export function planSpendLabel(spendUSD: string, budgetUSD: string): string {
  return `${cost(spendUSD)} of ${cost(budgetUSD)}`
}

// raiseBudgetError checks a new cap the owner typed: a dollar amount above
// the current cap and at most the limit; '' when valid (the server checks too).
export function raiseBudgetError(input: string, budgetUSD: string, limitUSD: string): string {
  const v = Number(input.trim().replace(/^\$/, ''))
  if (!input.trim() || !Number.isFinite(v)) return 'Enter an amount in USD.'
  if (v <= Number(budgetUSD) || v > Number(limitUSD)) return `Enter more than ${cost(budgetUSD)} and at most ${cost(limitUSD)}.`
  return ''
}

// planMilestoneTitle prefills the create dialog: the plan's title, else the
// draft's first heading, cut to GitLab's 255 characters.
export function planMilestoneTitle(title: string, draft: string): string {
  const t = title.trim() || (/^#{1,6}[ \t]+(.+)$/m.exec(draft)?.[1] ?? '').replace(/[ \t#]+$/, '').trim()
  return [...t].slice(0, 255).join('')
}

// PlanMilestoneLink is where a plan row's milestone goes: the Desk milestone
// page while the Desk has a row for it, else the milestone on GitLab, "Not
// planned" (decision 7). label is the milestone's title where the page knows
// it, with ref (%N) as its secondary label; %N alone (ref '') without.
export type PlanMilestoneLink =
  | { label: string; ref: string; to: { name: 'stream-milestone'; params: { namespace: string; name: string; iid: string } } }
  | { label: string; ref: string; href: string; note: string }

export function planMilestoneLink(
  item: Pick<PlanListItem, 'namespace' | 'project' | 'createdMilestoneIID' | 'createdMilestoneURL' | 'milestone'>,
  title?: string,
): PlanMilestoneLink | null {
  const iid = item.createdMilestoneIID
  if (!iid) return null
  const { title: label, ref } = milestoneName(iid, title)
  if (item.milestone) {
    return { label, ref, to: { name: 'stream-milestone', params: { namespace: item.namespace, name: item.project, iid: String(iid) } } }
  }
  return { label, ref, href: item.createdMilestoneURL ?? '', note: 'Not planned' }
}

// planSections splits a plan list (decision 9): created holds phase Created,
// drafting every other phase, Failed included. Each is last active first.
export function planSections<T extends Pick<PlanListItem, 'phase' | 'lastActivityAt'>>(items: T[]): { drafting: T[]; created: T[] } {
  const at = (p: T) => Date.parse(p.lastActivityAt) || 0
  const sorted = [...items].sort((a, b) => at(b) - at(a))
  return { drafting: sorted.filter((p) => p.phase !== 'Created'), created: sorted.filter((p) => p.phase === 'Created') }
}

// plansPath lives in its own module: client.ts is in the initial chunk and
// would pull in this whole file.
export { plansPath } from './plansPath.ts'

// planHeaderLines previews the lines the server appends (default markers).
export function planHeaderLines(spec: boolean, autopilot: boolean): string[] {
  return [...(spec ? ['/agent spec'] : []), ...(autopilot ? ['/agent autopilot'] : []), "/agent owners @… (the project's owners)", '/agent owner-subjects … (theirs and yours)']
}

// planDraftsCreated is a plan row's "N of M created" for a plan with more
// than one draft, with the created milestones the row's milestone link does
// not show yet (the first one); null for one draft or none.
export function planDraftsCreated(
  item: Pick<PlanListItem, 'draftsTotal' | 'draftsCreated' | 'createdMilestones' | 'createdMilestoneIID'>,
): { label: string; others: PlanListItem['createdMilestones'] } | null {
  if (item.draftsTotal <= 1) return null
  const others = item.createdMilestones.filter((m) => m.iid !== item.createdMilestoneIID)
  return { label: `${item.draftsCreated} of ${item.draftsTotal} created`, others }
}
