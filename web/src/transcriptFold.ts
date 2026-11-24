import type { TranscriptEntry } from './types'

// A live transcript folds a subagent's work into one row: the main-thread
// tool line that started it (Task/Agent, with toolUseID) and the lines
// whose parent is that id. LiveTranscript.vue renders what foldTranscript
// returns; it runs again on every new entry, so children that arrive later
// join their fold.

export interface TranscriptFold {
  kind: 'fold'
  tool: TranscriptEntry
  children: TranscriptEntry[]
  // model: the subagent's models seen so far, in order ("" while none).
  model: string
  // tokens: input + output + cache over its messages seen so far.
  tokens: number
  count: number
}

interface TranscriptLine {
  kind: 'entry'
  entry: TranscriptEntry
}

export type TranscriptRow = TranscriptLine | TranscriptFold

// foldName is what a fold is called: the subagent type, else the tool's
// argument (its description), else the tool.
export function foldName(f: TranscriptFold): string {
  return f.tool.subagent || f.tool.input || f.tool.tool || 'subagent'
}

// OwnerMessage is one owner's words that the pilot passed on to a running
// implementer.
export interface OwnerMessage {
  owner: string
  text: string
}

const ownerHeading = /^## Message from @(\S+) via the pilot$/gm

// ownerMessages reads the user turn the runner writes into a running call
// (or resumes its session with): one "## Message from @owner via the pilot"
// part per message, then a closing line for the agent, which is dropped.
// Any other text gives an empty list.
export function ownerMessages(text: string | undefined): OwnerMessage[] {
  if (!text) return []
  const heads = [...text.matchAll(ownerHeading)]
  if (!heads.length || heads[0]!.index !== 0) return []
  return heads.map((h, i) => {
    const end = i + 1 < heads.length ? heads[i + 1]!.index : text.length
    let body = text.slice(h.index + h[0].length, end).trim()
    if (i === heads.length - 1) {
      // The closing line is the last paragraph after the owner's words.
      const cut = body.lastIndexOf('\n\n')
      if (cut >= 0 && /^(Take this into account and )?[Gg]o on with the task/.test(body.slice(cut + 2))) body = body.slice(0, cut).trim()
    }
    return { owner: h[1]!, text: body }
  })
}

// foldTranscript groups entries into rows. Only a main-thread tool line
// (no parent of its own) opens a fold, and only lines after it join it.
// Lines of a nested subagent, or of a parent not seen (cut off by the
// entry cap), show unfolded where they are.
export function foldTranscript(entries: TranscriptEntry[]): TranscriptRow[] {
  const rows: TranscriptRow[] = []
  const folds = new Map<string, TranscriptFold>()
  const models = new Map<TranscriptFold, string[]>()
  for (const e of entries) {
    const f = e.parent ? folds.get(e.parent) : undefined
    if (f) {
      f.children.push(e)
      f.count++
      f.tokens += e.tokens ?? 0
      const ms = models.get(f)!
      if (e.model && !ms.includes(e.model)) {
        ms.push(e.model)
        f.model = ms.join(', ')
      }
      continue
    }
    if (e.kind === 'tool' && e.toolUseID && !e.parent && !folds.has(e.toolUseID)) {
      const fold: TranscriptFold = { kind: 'fold', tool: e, children: [], model: '', tokens: 0, count: 0 }
      folds.set(e.toolUseID, fold)
      models.set(fold, [])
      rows.push(fold)
      continue
    }
    rows.push({ kind: 'entry', entry: e })
  }
  return rows
}
