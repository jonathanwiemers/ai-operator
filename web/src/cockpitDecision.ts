// A decision.recorded event in a Cockpit thread (R5 of "Desk: the Cockpit
// newest first by issue"): what its item shows, from the stored decision
// when the Cockpit has loaded it, else from the event's data (D819-P2).
import type { InjectionKey, Ref } from 'vue'
import type { DecisionView } from './generated/views'
import type { StreamEventData } from './types'

// cockpitDecisionsKey: the decisions a Cockpit loaded, by ID (provided by
// Cockpit.vue, injected by CockpitDecision.vue). The Overview provides none.
export const cockpitDecisionsKey: InjectionKey<Ref<ReadonlyMap<string, DecisionView>>> = Symbol('cockpitDecisions')

// decisionsByID: the loaded decisions keyed by their ID.
export function decisionsByID(decisions: readonly DecisionView[] | undefined): Map<string, DecisionView> {
  return new Map((decisions ?? []).map((d) => [d.id, d]))
}

// supersedingID is the ID of the decision that superseded id: the stored
// view's supersededBy, else a loaded decision whose supersedes is id.
// Undefined while nothing superseded it.
export function supersedingID(id: string, byID: ReadonlyMap<string, DecisionView>): string | undefined {
  const own = byID.get(id)?.supersededBy
  if (own) return own
  for (const d of byID.values()) if (d.supersedes === id) return d.id
  return undefined
}

// CockpitDecisionItem is what a decision item shows: view is the stored
// decision (with its revisit mode), absent when it is not loaded (expired or
// still loading); then the item shows the event's data without Revisit.
export interface CockpitDecisionItem {
  id: string
  title: string
  choice: string
  superseded: boolean
  supersededBy?: string
  view?: DecisionView
}

// decisionItem builds the item of an event's data and the loaded decisions.
export function decisionItem(data: StreamEventData, byID: ReadonlyMap<string, DecisionView>): CockpitDecisionItem {
  const id = data.id ?? ''
  const view = id ? byID.get(id) : undefined
  const supersededBy = id ? supersedingID(id, byID) : undefined
  return {
    id,
    title: view?.title || data.title || '',
    choice: view?.choice || data.choice || '',
    superseded: !!view?.superseded || !!supersededBy,
    supersededBy,
    view,
  }
}
