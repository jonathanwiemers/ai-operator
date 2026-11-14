// Pure helpers for plan draft saves (PUT …/plans/{plan}/draft).
import type { PlanView } from './generated/views'

// DraftConflict: 409 body is the server's current PlanView (revision moved).
export class DraftConflict extends Error {
  view: PlanView
  constructor(view: PlanView) {
    super('the draft changed on the server')
    this.view = view
  }
}

// draftConflictFrom maps a 409 PlanView body; error envelopes stay ApiErrors.
export function draftConflictFrom(status: number, data: Record<string, unknown>): DraftConflict | null {
  if (status !== 409 || typeof data.name !== 'string' || typeof data.draftRevision !== 'number') return null
  return new DraftConflict(data as unknown as PlanView)
}
