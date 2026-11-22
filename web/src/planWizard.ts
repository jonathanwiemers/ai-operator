// The plan's decision review wizard (R7 of the milestone "Desk: answer a
// plan's decisions step by step"): the button, the preselection, the answers
// a set of picks produces, the summary and the picks in sessionStorage.
// Pure, tested in node.
import { plural } from './format.ts'
import { markedOption } from './planDecisions.ts'
import type { AgentPlanPhase, PlanDecision } from './generated/api'
import type { DecisionAnswer } from './generated/store'

// The longest Other answer the server takes.
export const maxOther = 2000

// WizardPick is an option's index, or the text written under Other.
export type WizardPick = number | string
// Picks holds a pick per decision ID with the version it was made on
// (D618-P4).
export type Picks = Record<string, { version: number; pick: WizardPick }>
export interface WizardState { clientKey: string; picks: Picks; text: string }

// wizardButton is the button's label, and whether it shows at all.
export function wizardButton(decisions: Pick<PlanDecision, 'state'>[]): { label: string; show: boolean } {
  const open = decisions.filter((d) => d.state === 'open').length
  return { label: open ? `Answer ${open} ${plural(open, 'question')}` : 'Review decisions', show: decisions.length > 0 }
}

// wizardBlocked is the hint of a disabled button and Send: a turn runs
// (D618-P3), or the milestone was created; '' when sending is allowed.
export function wizardBlocked(phase?: AgentPlanPhase): string {
  if (phase === 'Working') return 'The agent is answering…'
  return phase === 'Created' ? 'The milestone was created' : ''
}

// preselect is the option a step starts on: the signed-off choice, which is
// the version's recommended option (D618-P2), or the recommendation.
export function preselect(d: Pick<PlanDecision, 'options' | 'recommended' | 'signedOffBy'>): { index: number; mark: 'Signed off' | 'Recommended' } {
  const m = markedOption(d)
  return { index: m?.index ?? 0, mark: m?.label ?? 'Recommended' }
}

// firstStep is the first decision that is open or not signed off, else 0.
export function firstStep(decisions: Pick<PlanDecision, 'state' | 'signedOffBy'>[]): number {
  return Math.max(0, decisions.findIndex((d) => d.state === 'open' || !d.signedOffBy))
}

// pickOf is a step's pick: the owner's on this version, else the preselection.
export function pickOf(d: PlanDecision, picks: Picks): WizardPick {
  const p = picks[d.id]
  return p?.version === d.version ? p.pick : preselect(d).index
}

// answersFrom answers every decision (D618-P1): the preselection is a keep,
// another option its index, Other its text.
export function answersFrom(decisions: PlanDecision[], picks: Picks): DecisionAnswer[] {
  return decisions.map((d) => {
    const p = pickOf(d, picks)
    const a = { id: d.id, version: d.version }
    if (typeof p === 'string') return { ...a, other: p.trim() }
    return p === preselect(d).index ? { ...a, keep: true } : { ...a, option: p }
  })
}

// emptyOther is the step whose Other has no text yet, or -1.
export function emptyOther(decisions: PlanDecision[], picks: Picks): number {
  return decisions.findIndex((d) => {
    const p = pickOf(d, picks)
    return typeof p === 'string' && !p.trim()
  })
}

export interface SummaryRow { id: string; title: string; pick: string }

// summary sorts the decisions into kept and changed; a changed one names
// its new pick.
export function summary(decisions: PlanDecision[], picks: Picks): { kept: SummaryRow[]; changed: SummaryRow[] } {
  const out = { kept: [] as SummaryRow[], changed: [] as SummaryRow[] }
  answersFrom(decisions, picks).forEach((a, i) => {
    const d = decisions[i]!
    const pick = a.other !== undefined ? `Other: ${a.other}` : (d.options[a.option ?? d.recommended] ?? '')
    out[a.keep ? 'kept' : 'changed'].push({ id: d.id, title: d.title, pick })
  })
  return out
}

// mergePicks keeps the picks whose decision still has the version they were
// made on (and, for an option, still has that option): on restore and on a 409.
export function mergePicks(old: Picks, decisions: PlanDecision[]): Picks {
  const out: Picks = {}
  for (const d of decisions) {
    const p = old[d.id]
    if (p?.version !== d.version) continue
    if (typeof p.pick === 'string' || (Number.isInteger(p.pick) && p.pick >= 0 && p.pick < d.options.length)) out[d.id] = p
  }
  return out
}

// changedIds lists the decisions that are new or have another version in
// after than in before: the steps a 409 marks.
export function changedIds(before: PlanDecision[], after: PlanDecision[]): string[] {
  const seen = new Map(before.map((d) => [d.id, d.version]))
  return after.filter((d) => seen.get(d.id) !== d.version).map((d) => d.id)
}

export const wizardKey = (ns: string, name: string, plan: string) => `plan-decisions:${ns}/${name}/${plan}`

// readPicks reads a stored wizard state; anything else is none. storage
// defaults to sessionStorage.
export function readPicks(key: string, storage?: Storage): WizardState | null {
  try {
    const s = JSON.parse((storage ?? sessionStorage).getItem(key) ?? 'null') as Partial<WizardState> | null
    if (typeof s?.clientKey !== 'string' || !s.clientKey || typeof s.picks !== 'object' || !s.picks) return null
    const picks: Picks = {}
    for (const [id, p] of Object.entries(s.picks)) {
      if (typeof p?.version === 'number' && (typeof p.pick === 'number' || typeof p.pick === 'string')) picks[id] = p
    }
    return { clientKey: s.clientKey, picks, text: typeof s.text === 'string' ? s.text : '' }
  } catch {
    return null // no storage, private mode or not JSON
  }
}

// writePicks stores the state; null removes it (after a successful send).
export function writePicks(key: string, s: WizardState | null, storage?: Storage): void {
  try {
    if (!s) (storage ?? sessionStorage).removeItem(key)
    else (storage ?? sessionStorage).setItem(key, JSON.stringify(s))
  } catch {
    /* private mode / quota */
  }
}
