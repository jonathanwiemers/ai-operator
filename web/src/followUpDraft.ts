// Helpers of the follow-up milestone on the Retrospective tab (R14, R15 UI).
// Titles and bodies are untrusted markdown: the tab shows the draft in a
// plain textarea and previews it with markdown.ts, never as HTML.
import type { FollowUpDraftView, FollowUpView } from './types'

// GitLab's limit of a milestone title (the endpoint checks it too).
export const maxTitle = 255

export interface Draft {
  title: string
  description: string
}

// templateDraft is the draft without a pilot: the title "Follow-ups of
// <title>" (cut to maxTitle characters) and a description with each
// follow-up as a section, its title as the heading and its body below.
export function templateDraft(
  milestoneTitle: string,
  followUps: Pick<FollowUpView, 'title' | 'body'>[],
): Draft {
  const title = oneLine(`Follow-ups of ${oneLine(milestoneTitle) || 'the milestone'}`)
  const description = followUps
    .map((f) => {
      const head = `## ${oneLine(f.title)}`
      const body = (f.body ?? '').trim()
      return body ? `${head}\n\n${body}` : head
    })
    .join('\n\n')
  return { title: [...title].slice(0, maxTitle).join(''), description }
}

// selectable are the follow-ups an owner may tick: the open ones.
export function selectable<T extends Pick<FollowUpView, 'state'>>(followUps: T[]): T[] {
  return followUps.filter((f) => f.state === 'open')
}

// draftWritten reports whether d is a draft the pilot wrote after the one
// seen when it was asked (before: its "at"), or failed to write.
export function draftWritten(d: FollowUpDraftView | undefined, before: string | undefined): boolean {
  if (!d || d.pending) return false
  if (d.at === before) return false
  return !!d.error || !!d.title
}

// draftRefused reports whether the operator's answer to a draft command
// refuses it (internal/controller/pilot_draft.go draftRequest): anything but
// "The pilot writes a draft …" or "The pilot is writing a draft already …",
// after which a draft does land.
export function draftRefused(answer: string): boolean {
  return !/^The pilot (writes|is writing) a draft/.test(answer.trim())
}

// takenMessage turns a 409's taken numbers into plain words, with the
// follow-ups' titles.
export function takenMessage(taken: number[], followUps: Pick<FollowUpView, 'number' | 'title'>[]): string {
  const names = taken.map((n) => {
    const f = followUps.find((x) => x.number === n)
    return f ? `${n}. ${oneLine(f.title)}` : String(n)
  })
  return `Already opened, taken or dismissed: ${names.join('; ')}. Nothing was created; a leftover goes to one place only.`
}

function oneLine(s: string): string {
  return s.replace(/\s+/g, ' ').trim()
}
