// Helpers of the milestone's Retrospective tab (R12).
import type { Tone } from './format.ts'
import { pctLabel, shares } from './spend.ts'
import type { CostDriverView, FollowUpView } from './types'

// followUpStateLabel is what came of a leftover: "open", "opened as #12",
// "taken by %3" or "dismissed"; without the target's iid it names the kind.
export function followUpStateLabel(f: Pick<FollowUpView, 'state' | 'targetIID'>): string {
  switch (f.state) {
    case 'issue':
      return f.targetIID ? `opened as #${f.targetIID}` : 'opened as an issue'
    case 'milestone':
      return f.targetIID ? `taken by %${f.targetIID}` : 'taken by a milestone'
    case 'dismissed':
      return 'dismissed'
    default:
      return 'open'
  }
}

export function followUpStateTone(state: FollowUpView['state']): Tone {
  if (state === 'open') return 'warning'
  if (state === 'dismissed') return 'muted'
  return 'success'
}

// driverShares are the cost drivers' labelled shares of their sum, by cost
// when any is known, else by tokens (spend.ts shares). Issues and roles
// overlap, so a share is of the listed drivers, not of the milestone.
export function driverShares(drivers: CostDriverView[]): string[] {
  const { pct } = shares(
    drivers.map((d) => ({
      costUSD: d.costUSD,
      costKnown: d.costKnown,
      unknownCalls: 0,
      unmeteredCalls: 0,
      oldBasisCalls: 0,
      inputTokens: d.tokens,
      outputTokens: 0,
      cacheReadTokens: 0,
      cacheWriteTokens: 0,
      calls: 0,
      subagentCalls: 0,
      durationMS: 0,
      checkpoints: 0,
    })),
  )
  return pct.map(pctLabel)
}
