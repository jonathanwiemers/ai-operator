import { computed, ref, watch } from 'vue'
import { chatPostError } from './api'
import type { ChatCommand } from './chatState'
import { getTaskChat, postTaskChat } from './client'
import { live } from './live'
import { revisitPromptID } from './milestoneQueue'
import { commandAnswer, type CommandAnswer } from './pauseCard'

// Sent is the last command sent: its chat message, the task, the key the
// button shows "…" for (a decision ID, "#42", a limit) and the command.
export type Sent = { id: number; task: string; key: string; kind: ChatCommand['cmd'] }

// useCommandSend sends chat commands ({cmd:…} rows, kind command) to a
// task and follows the operator's answer, as the queue's Revisit, Resume
// and parallel stepper and the decision card's Revisit do. One command at a
// time; the state follows the last one sent. The answer is read from the
// task's chat after the post and on every live update until it is there.
// promptID is the prompt ("Q7") a revisit's answer names.
export function useCommandSend(project: () => { namespace: string; name: string }) {
  const sending = ref('')
  const error = ref('')
  const sent = ref<Sent | null>(null)
  const answer = ref<CommandAnswer>({ state: 'pending' })
  const promptID = computed(() =>
    sent.value?.kind === 'revisit' && answer.value.state === 'answered' ? revisitPromptID(answer.value.text) : '',
  )

  async function send(task: string | undefined, cmd: ChatCommand, key: string): Promise<boolean> {
    if (!task || sending.value) return false
    sending.value = key
    error.value = ''
    try {
      const { namespace, name } = project()
      const body = JSON.stringify(cmd)
      const r = await postTaskChat(namespace, name, task, { body, clientKey: crypto.randomUUID(), kind: 'command' })
      sent.value = { id: r.id, task, key, kind: cmd.cmd }
      answer.value = { state: 'pending' }
      void loadAnswer()
      return true
    } catch (e) {
      error.value = chatPostError(e)
      return false
    } finally {
      sending.value = ''
    }
  }

  async function loadAnswer() {
    const s = sent.value
    if (!s || answer.value.state !== 'pending') return
    try {
      const { namespace, name } = project()
      const page = await getTaskChat(namespace, name, s.task, { limit: 50 })
      if (sent.value !== s) return
      answer.value = commandAnswer(page.messages, s.id)
    } catch {
      /* best-effort; the next live update tries again */
    }
  }
  watch(
    () => live.version,
    () => void loadAnswer(),
  )

  return { sending, error, sent, answer, promptID, send }
}
