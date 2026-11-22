import type { MilestoneRow, PlanDraftView } from './generated/views'

// The % reference menu of the plan chat (Composer's references): the
// project's milestones and the plan's drafts not created yet. Typing "%"
// opens it, typing on filters it, picking one puts its reference in.

// RefItemSource is what the menu lists: the project's milestones
// (GET /milestones?state=all) and the plan's drafts.
export interface RefItemSource {
  milestones: Pick<MilestoneRow, 'iid' | 'title' | 'state' | 'queuePosition'>[]
  drafts: Pick<PlanDraftView, 'key' | 'title' | 'createdMilestoneIID'>[]
}

// RefItem is one option: the reference to insert, the title and the state
// (a milestone's, or "draft").
export interface RefItem {
  ref: string
  label: string
  state: string
}

// At most this many options, and this many done milestones among them.
const maxRefItems = 20
const maxDoneRefs = 10

// refQuery: the menu is open while the text before the caret ends in "%"
// and an optional word (the query), at the start or after a space or "(";
// start is the index of the "%". "50%" in a word opens nothing.
export function refQuery(body: string, caret: number): { start: number; query: string } | null {
  const m = /(?:^|[\s(])%([\w-]*)$/.exec(body.slice(0, caret))
  const query = m?.[1]
  if (query === undefined) return null
  return { start: caret - query.length - 1, query }
}

// refMatches: a query of digits matches the milestones whose number starts
// with it; any other query matches a reference that starts with it or a
// title that holds it, in any case.
function refMatches(ref: string, title: string, query: string): boolean {
  if (!query) return true
  const q = query.toLowerCase()
  if (/^\d+$/.test(q)) return /^%\d/.test(ref) && ref.slice(1).startsWith(q)
  return ref.slice(1).toLowerCase().startsWith(q) || title.toLowerCase().includes(q)
}

// refItems lists the options for query: open milestones in queue order
// (those without a position after, by number), then the newest done ones,
// then the drafts not created yet as %draft-N (N the draft's key).
export function refItems(src: RefItemSource, query: string): RefItem[] {
  const byQueue = (a: RefItemSource['milestones'][number], b: RefItemSource['milestones'][number]) =>
    (a.queuePosition || Infinity) - (b.queuePosition || Infinity) || a.iid - b.iid
  const pick = (ms: RefItemSource['milestones']) =>
    ms.map((m) => ({ ref: `%${m.iid}`, label: m.title, state: m.state })).filter((i) => refMatches(i.ref, i.label, query))
  // A milestone may have a row per spec task: one option each.
  const ms = [...new Map(src.milestones.map((m) => [m.iid, m])).values()]
  const open = pick(ms.filter((m) => m.state !== 'done').sort(byQueue))
  const done = pick(ms.filter((m) => m.state === 'done').sort((a, b) => b.iid - a.iid)).slice(0, maxDoneRefs)
  const drafts = src.drafts
    .filter((d) => !d.createdMilestoneIID)
    .map((d) => ({ ref: `%draft-${d.key}`, label: d.title || 'Untitled draft', state: 'draft' }))
    .filter((i) => refMatches(i.ref, i.label, query))
  return [...open, ...done, ...drafts].slice(0, maxRefItems)
}

// refPick is the body and caret after picking ref: it and one space take
// the place of the typed "%query" (start to caret).
export function refPick(body: string, start: number, caret: number, ref: string): { body: string; caret: number } {
  const text = `${ref} `
  return { body: body.slice(0, start) + text + body.slice(caret), caret: start + text.length }
}
