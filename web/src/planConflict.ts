// DraftConflict of plan draft changes, apart from planDraft.ts: client.ts is
// in the initial bundle, the draft editor's helpers are not.
import type { PlanView } from './generated/views'

// DraftConflict: 409 body is the server's current PlanView (a draft's
// revision or the draft list moved).
export class DraftConflict extends Error {
  view: PlanView
  constructor(view: PlanView) {
    super('the draft changed on the server')
    this.view = view
  }
}

// draftConflictFrom maps a 409 PlanView body; error envelopes stay ApiErrors.
export function draftConflictFrom(status: number, data: Record<string, unknown>): DraftConflict | null {
  if (status !== 409 || typeof data.name !== 'string' || !Array.isArray(data.drafts)) return null
  return new DraftConflict(data as unknown as PlanView)
}
