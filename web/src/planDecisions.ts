// A plan's Decisions tab (R6 of the milestone "Desk: answer a plan's
// decisions step by step"): a decision's state and badge, its who-line and
// the option the card marks. Pure, tested in node.
import type { PlanDecision } from './generated/api'
import type { Tone } from './format.ts'

export type PlanDecisionCardState = 'open' | 'unsigned' | 'signed'

// decisionState: open while the agent asks it, unsigned when locked without
// a sign-off, else signed.
export function decisionState(d: Pick<PlanDecision, 'state' | 'signedOffBy'>): PlanDecisionCardState {
  if (d.state === 'open') return 'open'
  return d.signedOffBy ? 'signed' : 'unsigned'
}

// decisionBadge is the state's badge label and tone.
export function decisionBadge(state: PlanDecisionCardState): { label: string; tone: Tone } {
  switch (state) {
    case 'open':
      return { label: 'Open', tone: 'info' }
    case 'unsigned':
      return { label: 'Not signed off', tone: 'warning' }
    default:
      return { label: 'Signed off', tone: 'success' }
  }
}

// personLabel names a decider or signer: agent reads "the agent", an owner
// shows by name, falling back to the subject (D617-P3).
export function personLabel(by: string, name?: string): string {
  if (by === 'agent') return 'the agent'
  return name || by
}

// whoLine is "Decided by X · signed off by Y", or "· not signed off"; the
// card appends the time.
export function whoLine(d: Pick<PlanDecision, 'decidedBy' | 'decidedByName' | 'signedOffBy' | 'signedOffByName'>): string {
  const decided = `Decided by ${personLabel(d.decidedBy, d.decidedByName)}`
  const signed = d.signedOffBy ? `signed off by ${personLabel(d.signedOffBy, d.signedOffByName)}` : 'not signed off'
  return `${decided} · ${signed}`
}

// markedOption is the option the card marks: the recommended one, labelled
// Signed off when the version is signed off, Recommended otherwise; null
// when the index is out of range.
export function markedOption(
  d: Pick<PlanDecision, 'options' | 'recommended' | 'signedOffBy'>,
): { index: number; label: 'Signed off' | 'Recommended' } | null {
  if (!Number.isInteger(d.recommended) || d.recommended < 0 || d.recommended >= (d.options?.length ?? 0)) return null
  return { index: d.recommended, label: d.signedOffBy ? 'Signed off' : 'Recommended' }
}
