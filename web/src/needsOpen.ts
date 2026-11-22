import { pausePrompt } from './pauseCard.ts'
import { splitPrompts } from './promptCard.ts'
import type { MilestoneView, PromptView, ProposalView } from './types'

type Hold = NonNullable<NonNullable<MilestoneView['autopilot']>['holds']>[number]

// OpenProblem is what is open on one problem, an issue (iid 0: the
// milestone). paused: the milestone's pause belongs to it; prompts: its
// open prompts without the pause or hold card's own.
interface OpenProblem {
  iid: number
  title?: string
  paused: boolean
  hold?: Hold
  prompts: PromptView[]
}

// NeedsOpen is what the Open section of a milestone's Needs you tab lists:
// one row per problem (Decision 1), then the proposals.
export interface NeedsOpen {
  problems: OpenProblem[]
  proposals: ProposalView[]
  // count: the problems and proposals, the Needs you tab's badge.
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
  // A pause belongs to its issue's problem (pauseIssue), else the
  // milestone's; the milestone's own problem comes first, issues by IID.
  const iids = new Set<number>([...(pauseIssue !== undefined ? [pauseIssue] : []), ...holds.map((h) => h.iid), ...open.map((q) => q.issueIID ?? 0)])
  const problems = [...iids]
    .sort((a, b) => a - b)
    .map((iid) => ({
      iid,
      title: iid ? m.issues?.find((i) => i.iid === iid)?.title : undefined,
      paused: iid === pauseIssue,
      hold: holds.find((h) => h.iid === iid),
      prompts: open.filter((q) => (q.issueIID ?? 0) === iid),
    }))
  const proposals = m.pilot?.proposals ?? []
  return { problems, proposals, count: problems.length + proposals.length }
}
