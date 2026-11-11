import type { NoteView, StreamEvent } from './types'

// ThreadNote is one GitLab comment as NoteThread.vue shows it, from a stream
// note event (the stream's threads) or a note read with the user's GitLab
// account (the task page's conversation). Body and summary are untrusted.
export interface ThreadNote {
  id: number
  author?: string
  at?: string
  // body is block markdown; summary the one-line fallback without a body.
  body?: string
  summary?: string
  mine: boolean
  bot: boolean
  system?: boolean
  // redacted: the viewer may not read it; ctx and url say where it is.
  redacted?: boolean
  ctx?: string
  url?: string
}

// fromStreamEvents maps a stream thread's note events; me is the user's
// GitLab username. The bot's notes are the pilot's, never the user's.
export function fromStreamEvents(events: StreamEvent[], me?: string): ThreadNote[] {
  return events.map((e) => {
    const bot = !!e.data.bot
    let ctx = ''
    if (e.data.noteable === 'mr' && e.mergeRequestIID) ctx = `!${e.mergeRequestIID}`
    else if (e.issueIID) ctx = `#${e.issueIID}`
    return {
      id: e.id,
      author: e.actor,
      at: e.at,
      body: e.data.body,
      summary: e.summary,
      mine: !!me && e.actor === me && !bot,
      bot,
      redacted: e.redacted,
      ctx: ctx || undefined,
      url: e.data.url,
    }
  })
}

// fromGitLabNotes maps the comments of one issue or merge request.
export function fromGitLabNotes(notes: NoteView[], me?: string): ThreadNote[] {
  return notes.map((n) => ({
    id: n.id,
    author: n.author,
    at: n.createdAt,
    body: n.body,
    mine: !!me && n.author === me,
    bot: false,
    system: n.system,
  }))
}

// noteWho is the name shown on a note.
export function noteWho(n: ThreadNote): string {
  if (n.bot) return 'Pilot'
  if (n.mine) return 'you'
  return n.author ? `@${n.author}` : 'Comment'
}

// noteInitials is the avatar's text.
export function noteInitials(n: ThreadNote): string {
  if (n.bot) return 'P'
  if (n.mine) return 'me'
  return (n.author ?? '?').slice(0, 2).toUpperCase()
}

// foldNotes keeps the last `fold` notes unless expanded (or fold is unset)
// and says how many it hid.
export function foldNotes<T>(notes: T[], fold: number | undefined, expanded: boolean): { visible: T[]; hidden: number } {
  if (!fold || expanded || notes.length <= fold) return { visible: notes, hidden: 0 }
  return { visible: notes.slice(-fold), hidden: notes.length - fold }
}
