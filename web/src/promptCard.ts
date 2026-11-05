import type { ChatCommand } from './chatState'
import type { PromptOptionView, PromptView } from './types'

// maxAnswerText bounds an answer's free text in bytes (api/v1alpha1
// MaxPromptText; the operator refuses longer ones).
export const maxAnswerText = 4000

// PromptOptionLine is one option of the card: the option with the tags
// shown beside its label and whether it is the answer given.
export interface PromptOptionLine extends PromptOptionView {
  tags: string[]
  chosen: boolean
}

// promptOptions lists a prompt's options in card order: the recommended one
// first, the others in their order (the pilot's "from your answer" ones
// follow the producer's).
export function promptOptions(view: PromptView): PromptOptionLine[] {
  const options = view.options || []
  const ordered = [...options.filter((o) => o.recommended), ...options.filter((o) => !o.recommended)]
  const chosen = view.answer?.optionID || ''
  return ordered.map((o) => {
    const tags: string[] = []
    if (o.recommended) tags.push('recommended')
    if (o.fromAnswer) tags.push('from your answer')
    return { ...o, actions: o.actions || [], tags, chosen: !!chosen && o.id === chosen }
  })
}

// recommendedOption is the option the compact card offers as its button.
export function recommendedOption(view: PromptView): PromptOptionView | undefined {
  return (view.options || []).find((o) => o.recommended)
}

// PromptStatus says where a prompt is: tone for the badge, text for the
// line under the question, at for the time beside it.
export interface PromptStatus {
  tone: 'warning' | 'success' | 'neutral'
  text: string
  at?: string
}

function viaText(via: string | undefined): string {
  switch (via) {
    case 'desk':
      return ' in the Desk'
    case 'gitlab':
      return ' on GitLab'
    default:
      return ''
  }
}

function clip(text: string, max: number): string {
  const t = text.trim().replace(/\s+/g, ' ')
  return t.length > max ? t.slice(0, max - 1) + '…' : t
}

// promptStatus says whether a prompt waits for an answer, who answered it
// with which option (or words), when and where, or why it closed.
export function promptStatus(view: PromptView): PromptStatus {
  switch (view.state) {
    case 'answered': {
      const a = view.answer
      if (!a) return { tone: 'success', text: 'Answered', at: view.closedAt }
      const by = a.by ? ` by ${a.by}` : ''
      const option = a.optionID ? (view.options || []).find((o) => o.id === a.optionID) : undefined
      let what = ''
      if (option) what = `: ${option.label}`
      else if (a.optionID) what = `: option ${a.optionID}`
      else if (a.text) what = `: "${clip(a.text, 120)}"`
      return { tone: 'success', text: `Answered${by}${viaText(a.via)}${what}`, at: a.at || view.closedAt }
    }
    case 'superseded':
      return { tone: 'neutral', text: 'Superseded: its cause resolved or a newer question replaced it', at: view.closedAt }
    case 'expired':
      return { tone: 'neutral', text: 'Expired: nobody answered in time', at: view.closedAt }
    default:
      return { tone: 'warning', text: 'Waiting for an answer', at: view.postedAt }
  }
}

export function promptOpen(view: PromptView): boolean {
  return !view.state || view.state === 'open'
}

// readOnlyText explains why an open prompt has no buttons for this user;
// '' when the user can answer or the prompt is closed.
export function readOnlyText(view: PromptView): string {
  return promptOpen(view) && !view.canAnswer ? "Only the milestone's owners can answer." : ''
}

// canAnswerText says whether the card has a text box: an open prompt that
// takes free text, for a user who may answer it.
export function canAnswerText(view: PromptView): boolean {
  return promptOpen(view) && !!view.canAnswer && !!view.allowFreeText
}

// textBox is the text box's placeholder and the hint under it: an agent's
// question takes the words as its answer, an action prompt turns them into
// an option to confirm.
export function textBox(view: PromptView): { placeholder: string; hint: string } {
  if (view.kind === 'needsInput') {
    return { placeholder: 'Your answer…', hint: 'Your words go to the agent as the answer.' }
  }
  return {
    placeholder: 'Do it differently: …',
    hint: 'The pilot turns your words into an option for you to confirm; nothing runs before that.',
  }
}

export type AnswerChoice = { option: string } | { text: string }

export type AnswerResult = { ok: true; cmd: ChatCommand; label: string } | { ok: false; error: string }

function byteLength(s: string): number {
  return new TextEncoder().encode(s).length
}

// answerCommand is the chat command for an answer to the prompt (sent to its
// target task), with a short label for the card, or why it cannot be sent.
export function answerCommand(view: PromptView, choice: AnswerChoice): AnswerResult {
  if (!promptOpen(view)) return { ok: false, error: `${view.id} is closed (${view.state}).` }
  if (!view.canAnswer) return { ok: false, error: "Only the milestone's owners can answer." }
  if ('option' in choice) {
    const option = (view.options || []).find((o) => o.id === choice.option)
    if (!option) return { ok: false, error: `${view.id} has no option ${choice.option}.` }
    return { ok: true, cmd: { cmd: 'answer', prompt: view.id, option: option.id }, label: option.label }
  }
  if (!view.allowFreeText) return { ok: false, error: `${view.id} takes no free text; pick an option.` }
  const text = choice.text.trim()
  if (!text) return { ok: false, error: 'Write an answer first.' }
  if (byteLength(text) > maxAnswerText) {
    return { ok: false, error: `The answer is longer than ${maxAnswerText} bytes.` }
  }
  return { ok: true, cmd: { cmd: 'answer', prompt: view.id, text }, label: 'Your answer' }
}

// promptAnchor is the element id the Desk links in GitLab comments point to
// (internal/desklink.Prompt).
export function promptAnchor(id: string): string {
  return `prompt-${id}`
}

// splitPrompts orders a view's prompts for a list: the open ones first,
// then the closed ones, each newest first as the view sends them.
export function splitPrompts(prompts: PromptView[] | undefined): { open: PromptView[]; closed: PromptView[] } {
  const list = prompts || []
  return { open: list.filter(promptOpen), closed: list.filter((p) => !promptOpen(p)) }
}

// taskPrompts: the prompts shown above a task's chat: the open ones, then
// the newest closed one, so a card that was just answered stays and shows
// its answer instead of vanishing. Only needsInput prompts (the agent's
// questions); the view sends them newest first.
export function taskPrompts(prompts: PromptView[] | undefined): PromptView[] {
  const { open, closed } = splitPrompts((prompts || []).filter((p) => p.kind === 'needsInput'))
  return closed.length ? [...open, closed[0]!] : open
}
