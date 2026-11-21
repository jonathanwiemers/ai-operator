// Pure helpers for plan drafts: the editor (PUT …/plans/{plan}/draft), its
// local storage and the draft list's order.
import type { PlanDraftView } from './generated/views'

export { DraftConflict, draftConflictFrom } from './planConflict.ts'

export interface DiffLine { kind: 'same' | 'add' | 'del'; text: string }

// maxCells bounds the LCS table (about 2000×2000 lines); above it the diff
// is one block replace.
const maxCells = 4_000_000
const lines = (s: string) => (s === '' ? [] : s.split('\n'))
const of = (kind: DiffLine['kind'], l: string[]) => l.map((text): DiffLine => ({ kind, text }))

// lineDiff is a line LCS diff from a to b; common head and tail are
// trimmed first, so typical edits stay small.
export function lineDiff(a: string, b: string): DiffLine[] {
  const x = lines(a), y = lines(b)
  let h = 0, t = 0
  while (h < x.length && h < y.length && x[h] === y[h]) h++
  while (t < x.length - h && t < y.length - h && x[x.length - 1 - t] === y[y.length - 1 - t]) t++
  const xs = x.slice(h, x.length - t), ys = y.slice(h, y.length - t)
  const n = xs.length, m = ys.length
  let mid: DiffLine[] = []
  if (n * m > maxCells) mid = [...of('del', xs), ...of('add', ys)]
  else {
    const w = m + 1, L = new Uint32Array((n + 1) * w) // L[i*w+j]: LCS of xs[i:], ys[j:]
    const at = (i: number, j: number) => L[i * w + j] ?? 0
    for (let i = n - 1; i >= 0; i--)
      for (let j = m - 1; j >= 0; j--) L[i * w + j] = xs[i] === ys[j] ? at(i + 1, j + 1) + 1 : Math.max(at(i + 1, j), at(i, j + 1))
    let i = 0, j = 0
    while (i < n || j < m) {
      if (i < n && j < m && xs[i] === ys[j]) mid.push({ kind: 'same', text: xs[i++]! }), j++
      else if (i < n && (j === m || at(i + 1, j) >= at(i, j + 1))) mid.push({ kind: 'del', text: xs[i++]! })
      else mid.push({ kind: 'add', text: ys[j++]! })
    }
  }
  return [...of('same', x.slice(0, h)), ...mid, ...of('same', x.slice(x.length - t))]
}

// EditorState is the draft editor: the revision edited, its text, and
// whether the owner changed it since.
export interface EditorState { base: number; text: string; dirty: boolean }
export type ServerDraft = { action: 'replace' } | { action: 'keep' } | { action: 'conflict'; diff: DiffLine[] }

// onServerDraft decides what a polled draft does to its editor: replace it
// when clean (or equal), a conflict with the diff mine → agent's when dirty
// and the server revision is newer, else keep the owner's edit.
type DraftText = Pick<PlanDraftView, 'draft' | 'revision'>
export function onServerDraft(s: EditorState, d: DraftText): ServerDraft {
  if (!s.dirty || s.text === d.draft) return { action: 'replace' }
  return d.revision > s.base ? { action: 'conflict', diff: lineDiff(s.text, d.draft) } : { action: 'keep' }
}
export const takeAgent = (d: DraftText): EditorState => ({ base: d.revision, text: d.draft, dirty: false })
export const keepMine = (s: EditorState, d: Pick<PlanDraftView, 'revision'>): EditorState => ({ ...s, base: d.revision })

// Unsaved edits per draft in localStorage, so a closed tab keeps them.
const editKey = (ns: string, plan: string, key: number) => `plan-draft:${ns}/${plan}/${key}`
export function readDraftEdit(ns: string, plan: string, key: number): EditorState | null {
  try {
    const e = JSON.parse(localStorage.getItem(editKey(ns, plan, key)) ?? 'null')
    return typeof e?.base === 'number' && typeof e?.text === 'string' ? { base: e.base, text: e.text, dirty: true } : null
  } catch { return null }
}
// writeDraftEdit stores a dirty state and removes a clean one.
export function writeDraftEdit(ns: string, plan: string, key: number, s: EditorState) {
  try {
    if (s.dirty) localStorage.setItem(editKey(ns, plan, key), JSON.stringify({ base: s.base, text: s.text }))
    else localStorage.removeItem(editKey(ns, plan, key))
  } catch { /* storage disabled: edits last for this tab */ }
}

// The draft list (Decision 10): drafts are created in list order; a created
// draft, or one being created, is read-only and keeps its place.
type ListDraft = Pick<PlanDraftView, 'key' | 'createdMilestoneIID' | 'creating'>
export const draftLocked = (d: ListDraft): boolean => !!d.createdMilestoneIID || !!d.creating

// nextToCreate is the draft Create milestone is for: the first without a
// milestone, null when every draft has one or the first is being created.
export function nextToCreate<T extends ListDraft>(drafts: T[]): T | null {
  const d = drafts.find((d) => !((d.createdMilestoneIID ?? 0) > 0))
  return d && !draftLocked(d) ? d : null
}

// canMove: the draft key and its neighbour in dir (-1 left, 1 right) both
// exist and neither is locked.
export function canMove(drafts: ListDraft[], key: number, dir: -1 | 1): boolean {
  const i = drafts.findIndex((d) => d.key === key)
  const a = drafts[i], b = drafts[i + dir]
  return !!a && !!b && !draftLocked(a) && !draftLocked(b)
}

// movedKeys is the key order with the draft key swapped with its neighbour
// in dir, the order unchanged when it cannot move.
export function movedKeys(drafts: ListDraft[], key: number, dir: -1 | 1): number[] {
  const keys = drafts.map((d) => d.key)
  if (!canMove(drafts, key, dir)) return keys
  const i = keys.indexOf(key)
  ;[keys[i], keys[i + dir]] = [keys[i + dir]!, keys[i]!]
  return keys
}

// draftLabel is a draft's tab label: its title, else "Draft <key>" (the N of
// %draft-N).
export const draftLabel = (d: Pick<PlanDraftView, 'key' | 'title'>): string => d.title.trim() || `Draft ${d.key}`
