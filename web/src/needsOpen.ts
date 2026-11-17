import { pausePrompt } from './pauseCard.ts'
import { splitPrompts } from './promptCard.ts'
import type { MilestoneView, PromptView, ProposalView } from './types'

type Hold = NonNullable<NonNullable<MilestoneView['autopilot']>['holds']>[number]

// NeedsOpen is what the Open section of a milestone's Needs you tab lists
// (R12), and the closed prompts its interim Handled section folds.
export interface NeedsOpen {
  // paused: the full pause card shows; it carries its own prompt.
  paused: boolean
  holds: Hold[]
  // prompts: the open prompts without the pause card's own (Decision 3).
  prompts: PromptView[]
  closed: PromptView[]
  proposals: ProposalView[]
  // count: the items under Open, the n of "Needs you · n" (D530-P1).
  count: number
}

// needsOpen splits a milestone view for the Needs you tab. A stored
// milestone has nothing open: only its closed prompts.
export function needsOpen(m: MilestoneView): NeedsOpen {
  const s = splitPrompts(m.prompts)
  if (m.stored) return { paused: false, holds: [], prompts: [], closed: s.closed, proposals: [], count: 0 }
  const paused = m.autopilot?.phase === 'Paused'
  const holds = m.autopilot?.holds ?? []
  const own = paused ? pausePrompt(m.autopilot?.pauseKind, m.prompts) : undefined
  const prompts = own ? s.open.filter((p) => p.id !== own.id) : s.open
  const proposals = m.pilot?.proposals ?? []
  const count = (paused ? 1 : 0) + holds.length + prompts.length + proposals.length
  return { paused, holds, prompts, closed: s.closed, proposals, count }
}

// needsLabel is the Needs you tab's label: "Needs you · n" while n > 0.
export function needsLabel(count: number): string {
  return count > 0 ? `Needs you · ${count}` : 'Needs you'
}
