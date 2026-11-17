import type { HandledItem } from './generated/views'
import { problemAnchor } from './needsCases.ts'
import { promptStatus, type PromptStatus } from './promptCard.ts'
import type { PromptView } from './types'

// The Inbox's Handled list: the label and the link of a row (a prompt that
// closed or an action the pilot ran). Pure: no Vue, no DOM.

export type HandledView = 'open' | 'handled'

// handledView reads the Inbox's switch from ?view=: handled, else open.
export function handledView(q: unknown): HandledView {
  return q === 'handled' ? 'handled' : 'open'
}

// handledLabel is a row's badge: the prompt's status line, from a
// view-shaped prompt built from the row, or who ran the action.
export function handledLabel(item: HandledItem): Pick<PromptStatus, 'tone' | 'text'> {
  if (item.kind === 'acted') {
    return item.by ? { tone: 'success', text: `@${item.by}` } : { tone: 'progress', text: 'Pilot' }
  }
  const { tone, text } = promptStatus(promptOf(item))
  return { tone, text }
}

// promptOf is the part of a PromptView promptStatus reads. The state comes
// from the close reason; a prompt closed before the operator recorded one
// counts as answered when it has an answer's by.
function promptOf(item: HandledItem): PromptView {
  const r = item.closedReason
  const state = r === 'answered' || (!r && item.by) ? 'answered' : r === 'expired' ? 'expired' : 'superseded'
  return {
    id: item.promptID ?? '',
    kind: 'assessment',
    question: item.title,
    options: [],
    allowFreeText: false,
    state,
    answer: item.by ? { by: item.by, at: item.at } : undefined,
    closedAt: item.at,
    closedReason: item.closedReason,
    closedBy: item.closedBy,
    replacedBy: item.replacedBy,
    canAnswer: false,
    target: { namespace: item.namespace, task: '' },
  }
}

// handledSubject is "#N" for an issue's row, "Milestone" for the
// milestone's own.
export function handledSubject(item: HandledItem): string {
  return item.issueIID ? `#${item.issueIID}` : 'Milestone'
}

export type HandledRoute =
  | { name: 'stream-milestone'; params: { namespace: string; name: string; iid: string }; query: { tab: 'needs' }; hash: string }
  | { name: 'stream-project'; params: { namespace: string; name: string }; hash?: string }

// handledRoute opens the row's problem on the milestone's Needs you tab; a
// row without a milestone opens the project page at the prompt.
export function handledRoute(item: HandledItem): HandledRoute {
  const base = { namespace: item.namespace, name: item.project }
  if (item.milestoneIID) {
    return {
      name: 'stream-milestone',
      params: { ...base, iid: String(item.milestoneIID) },
      query: { tab: 'needs' },
      hash: `#${problemAnchor(item.issueIID ?? 0)}`,
    }
  }
  return { name: 'stream-project', params: base, hash: item.promptID ? `#prompt-${item.promptID}` : undefined }
}
