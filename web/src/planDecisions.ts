// A plan's Decisions tab (R6 of the milestone "Desk: answer a plan's
// decisions step by step"): a decision's state and badge, its who-line and
// the option the card marks. Pure, tested in node.
import type { PlanDecision } from './generated/api'
import type { DecisionAnswer } from './generated/store'
import { plural, type Tone } from './format.ts'

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


// answersSummary counts a message's answers: kept, changed (option or other)
// and the label "Answered 7 decisions: 5 kept, 2 changed".
export function answersSummary(answers: readonly DecisionAnswer[]): { total: number; kept: number; changed: number; label: string } {
  const total = answers.length
  const kept = answers.filter((a) => a.keep).length
  const changed = total - kept
  return { total, kept, changed, label: `Answered ${total} ${plural(total, 'decision')}: ${kept} kept, ${changed} changed` }
}

// changedLines lists the changed answers: "D2 → option text" (or "option N"
// when the current list no longer has that version) and `D6 → "other"`.
// Other text is cut to one line; full is the whole text for a title.
export function changedLines(
  answers: readonly DecisionAnswer[],
  decisions: readonly Pick<PlanDecision, 'id' | 'version' | 'options'>[],
): { id: string; text: string; full: string }[] {
  const out: { id: string; text: string; full: string }[] = []
  for (const a of answers) {
    if (a.keep) continue
    if (a.other) {
      const oneLine = a.other.replace(/\s+/g, ' ').trim()
      const short = oneLine.length > 80 ? `${oneLine.slice(0, 79)}…` : oneLine
      out.push({ id: a.id, text: `${a.id} → "${short}"`, full: a.other })
      continue
    }
    const n = a.option ?? 0
    const cur = decisions.find((d) => d.id === a.id && d.version === a.version)
    const label = cur?.options?.[n] ?? `option ${n + 1}`
    out.push({ id: a.id, text: `${a.id} → ${label}`, full: label })
  }
  return out
}

// createWarning is the Create milestone dialog's warning, '' when nothing is pending.
export function createWarning(pending: number): string {
  if (!(pending > 0)) return ''
  return `${pending} ${plural(pending, 'decision is', 'decisions are')} open or not signed off`
}
