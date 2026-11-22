import type { RouteLocationRaw } from 'vue-router'
import { dayLabel, sameLocalDay, type StreamItem } from './stream.ts'
import type { ChatMessage, StreamEvent } from './types'

// The pure part of DeskChat (web/src/ui/DeskChat.vue): Desk events and chat
// turns as Nuxt UI chat messages, the problem prefix, and the scroll rules
// (follow new messages only at the bottom, keep the anchor on Load older).

// DeskChatRole: user = an owner, assistant = the pilot or an agent,
// system = an operator event (shown compact).
export type DeskChatRole = 'user' | 'assistant' | 'system'

// DeskChatSend is a message of the owner on its way: sending until the API
// answers, sent after, failed with the error.
export type DeskChatSend = 'sending' | 'sent' | 'failed'

// DeskChatMeta is what DeskChat shows around the text.
interface DeskChatMeta {
  at?: string
  author?: string
  kind?: string
  send?: DeskChatSend
  error?: string
  // event: the stream event the message came from (the caller's slots
  // render it).
  event?: StreamEvent
  // chat: the Desk chat message it came from (TaskChat, PlanChat render it
  // with ChatTurn).
  chat?: ChatMessage
  // card: shown as the caller's card (content slot) without a bubble or
  // muted text; notes: a stream thread's notes; day: the day label above it
  // when it starts a new day (streamMessages).
  card?: boolean
  notes?: StreamEvent[]
  day?: string
  // link: a Desk link after the text (a note's "Task logs").
  link?: { label: string; to: RouteLocationRaw }
}

// DeskChatMessage is Nuxt UI's message shape (UIMessage of the `ai` package,
// which the Desk does not depend on).
export interface DeskChatMessage {
  id: string
  role: DeskChatRole
  parts: { type: 'text'; text: string }[]
  metadata: DeskChatMeta
}

// stripPrefix hides a composer prefix ("problem: #42") at the start of a
// body, with the blank line after it; a longer token ("problem: #421") is
// not the prefix and stays.
export function stripPrefix(body: string, prefix?: string): string {
  if (!prefix) return body
  const t = body.trimStart()
  if (!t.startsWith(prefix)) return body
  const rest = t.slice(prefix.length)
  if (/^[\p{L}\p{N}]/u.test(rest)) return body
  return rest.trimStart()
}

// eventRole is the side of a stream event in a chat.
export function eventRole(e: Pick<StreamEvent, 'kind'>): DeskChatRole {
  switch (e.kind) {
    case 'chat.user':
      return 'user'
    case 'chat.agent':
    case 'pilot.acted':
      return 'assistant'
    default:
      return 'system'
  }
}

// eventMessage is one stream event as a message: a chat turn's body (the
// prefix hidden), else the event's one-line summary.
export function eventMessage(e: StreamEvent, prefix?: string): DeskChatMessage {
  const role = eventRole(e)
  const body = typeof e.data?.body === 'string' && e.data.body ? e.data.body : ''
  const text = role === 'system' || !body ? e.summary : stripPrefix(body, prefix)
  return {
    id: `${e.kind}:${e.id}`,
    role,
    parts: [{ type: 'text', text: text || e.kind }],
    metadata: { at: e.at, author: e.actor, kind: e.kind, event: e },
  }
}

// eventMessages turns a page of events, newest first as the API returns
// them, into messages oldest first, as a chat shows them.
export function eventMessages(events: StreamEvent[], prefix?: string): DeskChatMessage[] {
  return events.map((e) => eventMessage(e, prefix)).reverse()
}

// chatMessages turns a task's or plan's chat, oldest first (chatTurns'
// chronological), into messages: an owner's on the right, the agent's on the
// left, the operator's errors as compact lines. A reply follows the message
// it answers.
export function chatMessages(chronological: ChatMessage[]): DeskChatMessage[] {
  return chronological.map((m) => ({
    id: `chat:${m.id}`,
    role: m.role === 'user' ? 'user' : m.role === 'agent' ? 'assistant' : 'system',
    parts: [{ type: 'text', text: m.body ?? '' }],
    metadata: { at: m.at, kind: m.kind, chat: m },
  }))
}

// streamMessages turns stream items, newest first as groupThreads returns
// them, into cards oldest first, each new day's first with its label.
export function streamMessages(items: StreamItem[], now = Date.now()): DeskChatMessage[] {
  const out: DeskChatMessage[] = []
  let prev: string | undefined
  for (const it of [...items].reverse()) {
    const at = it.kind === 'thread' ? it.at : it.event.at
    const day = prev !== undefined && sameLocalDay(at, prev) ? undefined : dayLabel(at, now)
    prev = at
    out.push(
      it.kind === 'thread'
        ? { id: `thread:${it.key}`, role: 'system', parts: [{ type: 'text', text: '' }], metadata: { at, card: true, notes: it.notes, day } }
        : {
            id: `${it.event.kind}:${it.event.id}`,
            role: 'system',
            parts: [{ type: 'text', text: it.event.summary }],
            metadata: { at, card: true, event: it.event, kind: it.event.kind, day },
          },
    )
  }
  return out
}

// localMessage is the owner's message shown before the operator records it.
export function localMessage(id: string, text: string, at: string, send: DeskChatSend = 'sending'): DeskChatMessage {
  return { id: `local:${id}`, role: 'user', parts: [{ type: 'text', text }], metadata: { at, send } }
}

// ChatSnapshot is what DeskChat shows at one time: its messages and whether
// an answer is still expected.
export interface ChatSnapshot {
  messages: DeskChatMessage[]
  waiting: boolean
}

// firstSentence is a text's first sentence, cut at about max characters.
export function firstSentence(text: string, max = 140): string {
  const t = text.replace(/\s+/g, ' ').trim()
  const end = t.search(/[.!?…](\s|$)/)
  const s = end >= 0 ? t.slice(0, end + 1) : t
  if (s.length <= max) return s
  const cut = s.slice(0, max)
  const space = cut.lastIndexOf(' ')
  return `${(space > max / 2 ? cut.slice(0, space) : cut).trimEnd()}…`
}

// replyAnnouncement is what the announcer says when a reply is complete
// (Decision 7 of docs/specs/desk-accessible.spec.md): "<author> replied:
// <first sentence>", once, never while it is written. A reply is complete
// when nothing is expected any more (waiting is false). It is the newest
// message that is not an operator event, by the pilot or an agent, and new
// since prev or prev was still waiting. A first load (prev empty) says
// nothing. The author is the message's, else replier, else "Reply".
export function replyAnnouncement(prev: ChatSnapshot, next: ChatSnapshot, replier?: string): string {
  if (next.waiting || !prev.messages.length) return ''
  const m = [...next.messages].reverse().find((x) => x.role !== 'system')
  if (!m || m.role !== 'assistant') return ''
  if (!prev.waiting && prev.messages.some((x) => x.id === m.id)) return ''
  const s = firstSentence(m.parts.map((p) => p.text).join(' '))
  if (!s) return ''
  const author = m.metadata.author || replier
  return author ? `${author} replied: ${s}` : `Reply: ${s}`
}

// ChatChange is how a message list changed: older messages came in before
// the first (prepend), new ones after the last (append), or something else.
export type ChatChange = 'none' | 'prepend' | 'append' | 'replace'

export function chatChange(before: string[], after: string[]): ChatChange {
  if (before.length === after.length && before.every((id, i) => id === after[i])) return 'none'
  const head = before[0]
  if (head === undefined) return 'replace'
  const first = after.indexOf(head)
  if (first > 0 && after[after.length - 1] === before[before.length - 1]) return 'prepend'
  if (first === 0 && after.length > before.length && after[before.length - 1] === before[before.length - 1]) return 'append'
  return 'replace'
}

// atBottom: the reader sees the last message (within slack pixels), so new
// messages scroll into view; scrolled up, they do not.
export function atBottom(top: number, client: number, height: number, slack = 48): boolean {
  return height - (top + client) <= slack
}

// anchoredTop is the scroll position that keeps the same message in view
// after older messages were added above it.
export function anchoredTop(before: { top: number; height: number }, height: number): number {
  return Math.max(0, height - (before.height - before.top))
}
