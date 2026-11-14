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

// onServerDraft decides what a polled view does to the editor: replace it
// when clean (or equal), a conflict with the diff mine → agent's when dirty
// and the server revision is newer, else keep the owner's edit.
export function onServerDraft(s: EditorState, v: Pick<PlanView, 'draft' | 'draftRevision'>): ServerDraft {
  if (!s.dirty || s.text === v.draft) return { action: 'replace' }
  return v.draftRevision > s.base ? { action: 'conflict', diff: lineDiff(s.text, v.draft) } : { action: 'keep' }
}
export const takeAgent = (v: Pick<PlanView, 'draft' | 'draftRevision'>): EditorState =>
  ({ base: v.draftRevision, text: v.draft, dirty: false })
export const keepMine = (s: EditorState, v: Pick<PlanView, 'draftRevision'>): EditorState =>
  ({ ...s, base: v.draftRevision })

// Unsaved edits per plan in localStorage, so a closed tab keeps them.
const editKey = (ns: string, plan: string) => `plan-draft:${ns}/${plan}`
export function readDraftEdit(ns: string, plan: string): EditorState | null {
  try {
    const e = JSON.parse(localStorage.getItem(editKey(ns, plan)) ?? 'null')
    return typeof e?.base === 'number' && typeof e?.text === 'string' ? { base: e.base, text: e.text, dirty: true } : null
  } catch { return null }
}
// writeDraftEdit stores a dirty state and removes a clean one.
export function writeDraftEdit(ns: string, plan: string, s: EditorState) {
  try {
    if (s.dirty) localStorage.setItem(editKey(ns, plan), JSON.stringify({ base: s.base, text: s.text }))
    else localStorage.removeItem(editKey(ns, plan))
  } catch { /* storage disabled: edits last for this tab */ }
}
