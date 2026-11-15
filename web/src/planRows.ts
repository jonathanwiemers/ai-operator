// Pure helpers for the plans list (title fallback, phase badge tone).
import type { AgentPlanPhase } from './generated/api'
import { cost, type Tone } from './format.ts'

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
