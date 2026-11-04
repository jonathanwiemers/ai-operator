import type { ChatKind, ChatState, TaskPhase } from './types'

// Efforts the Desk chat API accepts on use/effort commands (api/v1alpha1.Efforts).
export const chatEfforts = ['low', 'medium', 'high', 'xhigh', 'max'] as const
export type ChatEffort = (typeof chatEfforts)[number]

export type ChatWaitingKind = 'capacity' | 'starting' | 'answering'

export interface ChatWaitingInput {
  // Unanswered user message that waits for the agent (not a command).
  pending: boolean
  // Delivered to the task (Interrupt only applies then).
  delivered: boolean
  phase?: TaskPhase
  message?: string
  podName?: string
}

export interface ChatWaiting {
  kind: ChatWaitingKind
  label: string
  canInterrupt: boolean
}

// chatWaiting describes the honest status chip while a user turn is open.
// Queued/Pending/Blocked use the task's status message (capacity, blocked, …);
// a cold start without a pod says "starting the agent…"; only Running/Idle
// with a pod is "the agent is answering…" (and Interrupt).
export function chatWaiting(input: ChatWaitingInput): ChatWaiting | null {
  if (!input.pending) return null
  const phase = input.phase
  const msg = (input.message || '').trim()
  const pod = !!(input.podName && input.podName.trim())

  if (phase === 'Queued' || phase === 'Pending' || phase === 'Blocked') {
    return {
      kind: 'capacity',
      label: msg || (phase === 'Blocked' ? 'blocked' : 'waiting for capacity'),
      canInterrupt: false,
    }
  }

  if (!pod) {
    return {
      kind: 'starting',
      label: 'starting the agent…',
      canInterrupt: false,
    }
  }

  if (phase === 'Running' || phase === 'Idle') {
    return {
      kind: 'answering',
      label: 'the agent is answering…',
      canInterrupt: input.delivered,
    }
  }

  // Waiting / Hibernated / Archived / unknown with a pod: still not a live turn.
  return {
    kind: 'starting',
    label: 'starting the agent…',
    canInterrupt: false,
  }
}

// ChatCommand is the JSON body of kind "command" (docs/ui.md, chatcmd).
export type ChatCommand =
  | { cmd: 'retry' }
  | { cmd: 'review' }
  | { cmd: 'use'; engine: 'claude' | 'cursor'; model: string; effort?: string }
  | { cmd: 'use'; default: true }
  | { cmd: 'effort'; effort: string }
  | { cmd: 'pause' }
  | { cmd: 'resume' }
  | { cmd: 'skip'; issue: number }
  | { cmd: 'merge'; issue: number; sha: string }
  | { cmd: 'accept'; proposal: number }
  | { cmd: 'reject'; proposal: number }
  | { cmd: 'answer'; prompt: string; option: string }
  | { cmd: 'answer'; prompt: string; text: string }

// commandLabel turns a command row's body into a short chip; unparsable
// bodies fall back to the raw text.
export function commandLabel(body: string | undefined): string {
  if (body === undefined || body === '') return ''
  let raw: unknown
  try {
    raw = JSON.parse(body)
  } catch {
    return body
  }
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return body
  const o = raw as Record<string, unknown>
  const cmd = typeof o.cmd === 'string' ? o.cmd.toLowerCase().trim() : ''
  switch (cmd) {
    case 'retry':
      return 'retry'
    case 'review':
      return 'review again'
    case 'pause':
      return 'pause'
    case 'resume':
      return 'resume'
    case 'use':
      if (o.default === true) return 'use default'
      {
        const parts = ['use']
        if (typeof o.engine === 'string' && o.engine) parts.push(o.engine)
        if (typeof o.model === 'string' && o.model) parts.push(o.model)
        if (typeof o.effort === 'string' && o.effort) parts.push(o.effort)
        return parts.length > 1 ? parts.join(' ') : body
      }
    case 'effort':
      return typeof o.effort === 'string' && o.effort ? `effort ${o.effort}` : body
    case 'skip':
      return typeof o.issue === 'number' && o.issue > 0 ? `skip #${o.issue}` : body
    case 'merge':
      return typeof o.issue === 'number' && o.issue > 0 && typeof o.sha === 'string' && o.sha.trim()
        ? `merge #${o.issue} ${o.sha.trim().slice(0, 7)}`
        : body
    case 'accept':
      return typeof o.proposal === 'number' && o.proposal > 0 ? `accept P${o.proposal}` : body
    case 'reject':
      return typeof o.proposal === 'number' && o.proposal > 0 ? `reject P${o.proposal}` : body
    case 'answer': {
      const prompt = typeof o.prompt === 'string' ? o.prompt.trim() : ''
      if (!prompt) return body
      if (typeof o.option === 'string' && o.option.trim()) return `answer ${prompt}: ${o.option.trim()}`
      const text = typeof o.text === 'string' ? o.text.trim().replace(/\s+/g, ' ') : ''
      if (!text) return body
      return `answer ${prompt}: "${text.length > 40 ? text.slice(0, 39) + '…' : text}"`
    }
    default:
      return body
  }
}

// agentTurnPending: a message/interrupt that still waits for an agent reply.
export function agentTurnPending(
  kind: ChatKind | undefined,
  state: ChatState | undefined,
  hasReply: boolean,
): boolean {
  if (kind === 'command') return false
  if (state !== 'queued' && state !== 'delivered') return false
  return !hasReply
}
