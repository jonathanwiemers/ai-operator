// Create all (R8, Decision 11): the plan's uncreated drafts are created one
// by one, in list order, through the same endpoint as Create milestone. The
// server creates only the first uncreated draft, so the loop stops at the
// first error: the later drafts would get 409 anyway.
import { errorMessage } from './format.ts'
import type { PlanDraftView, PlanMilestoneView, PlanRefFillView } from './generated/views'

type Draft = Pick<PlanDraftView, 'key' | 'createdMilestoneIID'>

export interface CreateAllResult {
  created: { key: number; iid: number; url: string }[]
  failed?: { key: number; message: string }
  skipped: number[] // keys after the failed draft, still drafts
  filledIn: FilledIn[] // of every successful create, in order
}

// FilledIn is an entry of a create's filledIn with by, the IID of the
// milestone just created whose %IID it got (or would have got).
export type FilledIn = PlanRefFillView & { by: number }

// filledIn tags a create's filledIn entries with the new milestone's IID.
export const filledIn = (m: PlanMilestoneView): FilledIn[] => (m.filledIn ?? []).map((f) => ({ ...f, by: m.iid }))

// filledInLine is how both create dialogs show a filledIn entry.
export const filledInLine = (f: FilledIn): string =>
  f.state === 'filled' ? `%${f.iid}: filled in %${f.by}` : `%${f.iid}: kept the title (${f.reason || 'skipped'})`

// forwardRefs counts the distinct %draft-N in text (the server's pattern)
// naming a draft after key in list order that has no milestone yet: those
// are written as the later draft's quoted title until it is created.
export function forwardRefs(text: string, drafts: Draft[], key: number): number {
  const at = drafts.findIndex((d) => d.key === key)
  if (at < 0) return 0
  const later = new Set(uncreated(drafts.slice(at + 1)).map((d) => d.key))
  const named = new Set([...text.matchAll(/%draft-([0-9]+)\b/g)].map((m) => Number(m[1])))
  return [...named].filter((n) => later.has(n)).length
}

// uncreated is the drafts without a milestone, in list order.
export const uncreated = <T extends Draft>(drafts: T[]): T[] => drafts.filter((d) => !((d.createdMilestoneIID ?? 0) > 0))

// createAll calls create for each uncreated draft in order, each after the
// previous one has finished, and stops at the first error. filledIn keeps
// the entries of the creates before an error.
export async function createAll<T extends Draft>(drafts: T[], create: (d: T) => Promise<PlanMilestoneView>): Promise<CreateAllResult> {
  const todo = uncreated(drafts)
  const r: CreateAllResult = { created: [], skipped: [], filledIn: [] }
  for (const [i, d] of todo.entries()) {
    try {
      const m = await create(d)
      r.created.push({ key: d.key, iid: m.iid, url: m.webURL })
      r.filledIn.push(...filledIn(m))
    } catch (e) {
      r.failed = { key: d.key, message: errorMessage(e) }
      r.skipped = todo.slice(i + 1).map((d) => d.key)
      break
    }
  }
  return r
}
