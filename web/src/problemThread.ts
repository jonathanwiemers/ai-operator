import { localMessage, type DeskChatMessage } from './chatView.ts'
import { promptAnchor } from './promptCard.ts'
import type { ProblemStatus, ProblemView, PromptView, StreamEvent } from './types'

// The pure part of the problem thread (Needs you tab, spec
// desk-problem-thread): route ids, old anchors, the status line, the closed
// prompts of a thread without history, and the owner's own answers until the
// operator's events take over (Decision 8). No Vue, no DOM.

// problemParam is the route id of a problem: its row id, or issue-<iid>
// when the history database does not know it (the server reads both).
export function problemParam(p: Pick<ProblemView, 'id' | 'issueIID'>): string {
  return p.id ? String(p.id) : `issue-${p.issueIID}`
}

// sameProblem: param names p, by row id or by issue-<iid>.
export function sameProblem(param: string, p: Pick<ProblemView, 'id' | 'issueIID'>): boolean {
  return param === problemParam(p) || param === `issue-${p.issueIID}` || (!!p.id && param === String(p.id))
}

// problemFromHash is the problem an old link names: #problem-<iid>, or the
// issue of the prompt of #prompt-<id> (the milestone's problem for a prompt
// without an issue). Undefined for other hashes or an unknown prompt.
export function problemFromHash(hash: string, prompts: Pick<PromptView, 'id' | 'issueIID'>[] = []): string | undefined {
  const name = hash.startsWith('#') ? hash.slice(1) : hash
  const m = /^problem-(\d+)$/.exec(name)
  if (m) return `issue-${Number(m[1])}`
  const p = prompts.find((q) => name === promptAnchor(q.id))
  return p ? `issue-${p.issueIID ?? 0}` : undefined
}

// problemSubject is "#N" for an issue's problem, "Milestone" for the
// milestone's own.
export function problemSubject(p: Pick<ProblemView, 'issueIID'>): string {
  return p.issueIID ? `#${p.issueIID}` : 'Milestone'
}

// statusLine is the live status as a person reads it, with its tone.
export function statusLine(s: ProblemStatus): { text: string; tone: 'warning' | 'progress' | 'success' | 'neutral' } {
  switch (s) {
    case 'waiting':
      return { text: 'Waiting for you', tone: 'warning' }
    case 'acting':
      return { text: 'Operator acting', tone: 'progress' }
    case 'pilot':
      return { text: 'Pilot looking', tone: 'progress' }
    case 'resolved':
      return { text: 'Resolved', tone: 'success' }
  }
  return { text: s || 'Open', tone: 'neutral' }
}

// promptMessage is a closed question as one message: the question, the
// chosen answer and who chose it (Decision 7), for a thread without history.
export function promptMessage(p: PromptView): DeskChatMessage {
  const chosen = p.answer?.optionID ? p.options.find((o) => o.id === p.answer?.optionID)?.label : undefined
  const answer = chosen ?? p.answer?.text
  const lines = [`**${p.id}** ${p.question}`]
  if (answer) lines.push(`→ ${answer}${p.answer?.by ? ` (@${p.answer.by})` : ''}`)
  else if (p.closedReason) lines.push(`→ ${p.closedReason}${p.closedBy ? ` (${p.closedBy})` : ''}`)
  if (p.answer?.outcome) lines.push(p.answer.outcome)
  return {
    id: `prompt:${p.id}`,
    role: 'assistant',
    parts: [{ type: 'text', text: lines.join('\n\n') }],
    metadata: { at: p.closedAt ?? p.postedAt, author: 'operator', kind: 'prompt' },
  }
}

// LocalAnswer is an owner's answer or message the thread shows before the
// operator records anything: sending until the API answers, then sent or
// failed.
export interface LocalAnswer {
  id: string
  text: string
  // at: when it was sent (ISO); sentAt the same in ms.
  at: string
  sentAt: number
  send: 'sending' | 'sent' | 'failed'
  error?: string
  // task: the task the answer went to (the no-reaction note links its logs).
  task?: string
}

// AnswerEvent is what a bar's card (PromptCard, PauseCard) emits as its
// answer goes out, so the thread can show it at once (Decision 8). The
// same id comes again with sent or failed.
export interface AnswerEvent {
  id: string
  label: string
  send: 'sending' | 'sent' | 'failed'
  error?: string
  task?: string
}

// withAnswer applies an AnswerEvent to the local answers: a new one is
// added (sent at now), a known one changes its state.
export function withAnswer(locals: LocalAnswer[], e: AnswerEvent, now: number): LocalAnswer[] {
  const i = locals.findIndex((a) => a.id === e.id)
  if (i < 0) return [...locals, { id: e.id, text: e.label, at: new Date(now).toISOString(), sentAt: now, send: e.send, error: e.error, task: e.task }]
  const next = [...locals]
  const a = next[i]!
  // sentAt: when the operator could first react, the API's answer.
  next[i] = { ...a, send: e.send, error: e.error, sentAt: e.send === 'sent' ? now : a.sentAt }
  return next
}

// noReactionAfter is how long a sent answer waits for a new event before
// the thread says the operator has not reacted (Decision 8).
export const noReactionAfter = 60_000

const ms = (s: string | undefined): number => {
  const t = s ? Date.parse(s) : NaN
  return Number.isNaN(t) ? 0 : t
}

// newestAt is the time of the newest event (events newest first).
export function newestAt(events: Pick<StreamEvent, 'at'>[]): number {
  return events.reduce((n, e) => Math.max(n, ms(e.at)), 0)
}

// answered: an event at or after the local answer was sent arrived; the
// thread then shows the operator's record instead of the local copy.
export function answered(a: LocalAnswer, newest: number): boolean {
  return a.send === 'sent' && newest >= a.sentAt - 2_000
}

// noReaction: the answer went out 60 s ago or more and no event came since.
export function noReaction(a: LocalAnswer, newest: number, now: number): boolean {
  return a.send === 'sent' && !answered(a, newest) && now - a.sentAt >= noReactionAfter
}

// localMessages are the local answers still to show, as messages, each sent
// one followed by the no-reaction note once it is due; the note links the
// logs of the task the answer went to (project: the task route's params).
export function localMessages(
  locals: LocalAnswer[],
  newest: number,
  now: number,
  project?: { namespace: string; name: string },
): DeskChatMessage[] {
  const out: DeskChatMessage[] = []
  for (const a of locals) {
    if (answered(a, newest)) continue
    const m = localMessage(a.id, a.text, a.at, a.send)
    if (a.error) m.metadata.error = a.error
    out.push(m)
    if (noReaction(a, newest, now)) {
      out.push({
        id: `noreaction:${a.id}`,
        role: 'system',
        parts: [{ type: 'text', text: 'No reaction from the operator yet.' }],
        metadata: {
          kind: 'noReaction',
          link:
            a.task && project
              ? { label: 'Task logs', to: { name: 'task', params: { ...project, task: a.task }, query: { tab: 'logs' } } }
              : undefined,
        },
      })
    }
  }
  return out
}
