import { pausePrompt } from './pauseCard.ts'
import { splitPrompts } from './promptCard.ts'
import type { MilestoneView, PromptView, ProposalView } from './types'

type Hold = NonNullable<NonNullable<MilestoneView['autopilot']>['holds']>[number]

// NeedsOpen is what the Open section of a milestone's Needs you tab lists
// (R12); Handled groups all prompts into problems (needsCases).
export interface NeedsOpen {
  // paused: the full pause card shows; it carries its own prompt.
  paused: boolean
  holds: Hold[]
  // prompts: the open prompts without the pause card's own (Decision 3).
  prompts: PromptView[]
  proposals: ProposalView[]
  // count: the items under Open, the n of "Needs you · n" (D530-P1).
  count: number
}

// needsOpen splits a milestone view for the Needs you tab. A stored
// milestone has nothing open.
export function needsOpen(m: MilestoneView): NeedsOpen {
  if (m.stored) return { paused: false, holds: [], prompts: [], proposals: [], count: 0 }
  const open = splitPrompts(m.prompts).open
  const paused = m.autopilot?.phase === 'Paused'
  const holds = m.autopilot?.holds ?? []
  const own = paused ? pausePrompt(m.autopilot?.pauseKind, m.prompts) : undefined
  const prompts = own ? open.filter((p) => p.id !== own.id) : open
  const proposals = m.pilot?.proposals ?? []
  const count = (paused ? 1 : 0) + holds.length + prompts.length + proposals.length
  return { paused, holds, prompts, proposals, count }
}

// needsLabel is the Needs you tab's label: "Needs you · n" while n > 0.
export function needsLabel(count: number): string {
  return count > 0 ? `Needs you · ${count}` : 'Needs you'
}
