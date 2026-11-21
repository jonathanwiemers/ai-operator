// Create all (R8, Decision 11): the plan's uncreated drafts are created one
// by one, in list order, through the same endpoint as Create milestone. The
// server creates only the first uncreated draft, so the loop stops at the
// first error: the later drafts would get 409 anyway.
import type { FollowUpMilestoneView, PlanDraftView } from './generated/views'

type Draft = Pick<PlanDraftView, 'key' | 'createdMilestoneIID'>

export interface CreateAllResult {
  created: { key: number; iid: number; url: string }[]
  failed?: { key: number; message: string }
  skipped: number[] // keys after the failed draft, still drafts
}

// uncreated is the drafts without a milestone, in list order.
export const uncreated = <T extends Draft>(drafts: T[]): T[] => drafts.filter((d) => !((d.createdMilestoneIID ?? 0) > 0))

// createAll calls create for each uncreated draft in order, each after the
// previous one has finished, and stops at the first error.
export async function createAll<T extends Draft>(drafts: T[], create: (d: T) => Promise<FollowUpMilestoneView>): Promise<CreateAllResult> {
  const todo = uncreated(drafts)
  const r: CreateAllResult = { created: [], skipped: [] }
  for (const [i, d] of todo.entries()) {
    try {
      const m = await create(d)
      r.created.push({ key: d.key, iid: m.iid, url: m.webURL })
    } catch (e) {
      r.failed = { key: d.key, message: e instanceof Error ? e.message : String(e) }
      r.skipped = todo.slice(i + 1).map((d) => d.key)
      break
    }
  }
  return r
}
