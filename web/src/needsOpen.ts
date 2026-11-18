import { needsCases, type Problem } from './needsCases.ts'
import { pausePrompt } from './pauseCard.ts'
import { splitPrompts } from './promptCard.ts'
import type { MilestoneView, PromptView, ProposalView } from './types'

type Hold = NonNullable<NonNullable<MilestoneView['autopilot']>['holds']>[number]

// OpenProblem is one card under Open: a problem of needsCases (its history)
// with what is open on it. paused: the milestone's pause belongs to it;
// prompts: its open prompts without the pause or hold card's own.
export interface OpenProblem extends Problem {
  paused: boolean
  hold?: Hold
  prompts: PromptView[]
}

// NeedsOpen is what the Open section of a milestone's Needs you tab lists:
// one card per problem (Decision 1), then the proposals.
export interface NeedsOpen {
  problems: OpenProblem[]
  proposals: ProposalView[]
  // count: the problems and proposals, the n of "Needs you · n".
  count: number
}

// needsOpen groups a milestone view for the Needs you tab. A stored
// milestone has nothing open.
export function needsOpen(m: MilestoneView): NeedsOpen {
  if (m.stored) return { problems: [], proposals: [], count: 0 }
  const ap = m.autopilot
  const pauseIssue = ap?.phase === 'Paused' ? ap.pauseIssue ?? ap.pausedFor ?? 0 : undefined
  const holds = ap?.holds ?? []
  const own = new Set(
    [pauseIssue !== undefined ? ap?.pauseKind : undefined, ...holds.map((h) => h.kind)].map((k) => pausePrompt(k, m.prompts)?.id),
  )
  const open = splitPrompts(m.prompts).open.filter((p) => !own.has(p.id))
  const problems = needsCases(m)
    .map((p) => ({
      ...p,
      paused: p.iid === pauseIssue,
      hold: holds.find((h) => h.iid === p.iid),
      prompts: open.filter((q) => (q.issueIID ?? 0) === p.iid),
    }))
    .filter((p) => p.paused || p.hold || p.prompts.length)
  const proposals = m.pilot?.proposals ?? []
  return { problems, proposals, count: problems.length + proposals.length }
}

// needsLabel is the Needs you tab's label: "Needs you · n" while n > 0.
export function needsLabel(count: number): string {
  return count > 0 ? `Needs you · ${count}` : 'Needs you'
}
