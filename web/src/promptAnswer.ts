import { computed, ref, watch } from 'vue'
import { chatPostError } from './api'
import { getTaskChat, postTaskChat } from './client'
import { live } from './live'
import { commandAnswer, type CommandAnswer } from './pauseCard'
import { answerCommand, type AnswerChoice } from './promptCard'
import type { PromptView } from './types'

// usePromptAnswer sends a prompt card's answer command to the prompt's
// target task and follows the operator's reply in that task's chat, like the
// pause card. One answer at a time: sending names the option ('text' for the
// text box) while the post is in flight, and the card stays pending until
// the reply (or a failed row) arrives.
export function usePromptAnswer(project: () => { namespace: string; name: string }, prompt: () => PromptView) {
  const sending = ref<string | null>(null)
  const error = ref('')
  const sent = ref<{ id: number; task: string; label: string } | null>(null)
  const answer = ref<CommandAnswer>({ state: 'pending' })
  // A failed post keeps its key: retrying the same body reuses it.
  let unsent: { task: string; body: string; clientKey: string } | null = null
  let seq = 0

  // busy: a post in flight, or a sent answer without its reply yet.
  const busy = computed(() => !!sending.value || (!!sent.value && answer.value.state === 'pending'))

  async function send(choice: AnswerChoice): Promise<boolean> {
    if (busy.value) return false
    const p = prompt()
    const r = answerCommand(p, choice)
    if (!r.ok) {
      error.value = r.error
      return false
    }
    const task = p.target.task
    const body = JSON.stringify(r.cmd)
    const clientKey = unsent && unsent.task === task && unsent.body === body ? unsent.clientKey : crypto.randomUUID()
    unsent = { task, body, clientKey }
    sending.value = 'option' in choice ? choice.option : 'text'
    error.value = ''
    try {
      const { namespace, name } = project()
      const res = await postTaskChat(namespace, name, task, { body, clientKey, kind: 'command' })
      unsent = null
      sent.value = { id: res.id, task, label: r.label }
      answer.value = { state: 'pending' }
      void loadAnswer()
      return true
    } catch (e) {
      error.value = chatPostError(e)
      return false
    } finally {
      sending.value = null
    }
  }

  // loadAnswer looks for the operator's reply in the target task's chat;
  // live updates bring it.
  async function loadAnswer() {
    const s = sent.value
    if (!s || answer.value.state !== 'pending') return
    const mine = ++seq
    try {
      const { namespace, name } = project()
      const page = await getTaskChat(namespace, name, s.task, { limit: 50 })
      if (mine !== seq || sent.value !== s) return
      answer.value = commandAnswer(page.messages, s.id)
    } catch {
      /* best-effort; the next live update tries again */
    }
  }

  watch(
    () => live.version,
    () => void loadAnswer(),
  )

  return { sending, error, sent, answer, busy, send }
}
