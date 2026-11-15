// The estimated cost of a plan's draft or an unstarted milestone (R12).
import { cost, tokenCount } from './format.ts'
import type { EstimateView } from './generated/views'

// estimateLabel is "$low–$high (typical $x), ~N tokens", or "not enough
// history" with fewer than 3 finished milestones.
export function estimateLabel(e: Pick<EstimateView, 'lowUSD' | 'typicalUSD' | 'highUSD' | 'typicalTokens' | 'notEnoughHistory'>): string {
  if (e.notEnoughHistory) return 'not enough history'
  return `${cost(e.lowUSD)}–${cost(e.highUSD)} (typical ${cost(e.typicalUSD)}), ~${tokenCount(e.typicalTokens)} tokens`
}
