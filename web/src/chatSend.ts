import { onMounted, ref, watch } from 'vue'
import { chatPostError } from './api'
import { chatDraftKey, chatPostBody, chatWithPrefix, readChatDraft, writeChatDraft, type ChatCommand } from './chatState'
import { postTaskChat } from './client'
import { keepThroughSignIn } from './signInDraft'
import type { ChatKind } from './types'

// ChatSendTarget is the task a chat composer posts to; prefix goes before
// every message sent (a problem's thread).
export interface ChatSendTarget {
  namespace: string
  name: string
  task: string
  prefix?: string
}

// useChatSend is a Desk chat composer's sending (TaskChat, CockpitComposer):
// the draft and its clientKey stay in sessionStorage until a 2xx, so a
// retry or a reload reuses the key; the text survives a sign-in. A change
// of task drops the draft and takes the new task's kept one. onSent runs
// after each 2xx, with the message's id.
export function useChatSend(target: () => ChatSendTarget, onSent: (id: number) => void) {
  const body = ref('')
  const sending = ref(false)
  const sendError = ref('')
  const clientKey = ref('')
  const pendingKind = ref<ChatKind | null>(null)
  const id = () => {
    const t = target()
    return [t.namespace, t.name, t.task, t.prefix] as const
  }
  keepThroughSignIn(() => {
    const t = target()
    return `chat:${t.namespace}/${t.name}/${t.task}${t.prefix ? `#${t.prefix}` : ''}`
  }, body)
  const draftKey = () => chatDraftKey(...id())

  function restoreDraft() {
    const d = readChatDraft(draftKey())
    if (!d) return
    clientKey.value = d.clientKey
    pendingKind.value = d.kind
    // Commands keep the key for retry without putting JSON in the textarea;
    // text kept through a sign-in (newer) wins over the failed send's body.
    if (d.kind !== 'command' && !body.value) body.value = d.body
  }

  watch(
    () => id().join('\n'),
    () => {
      body.value = ''
      clientKey.value = ''
      pendingKind.value = null
      sendError.value = ''
      restoreDraft()
    },
  )
  onMounted(restoreDraft)

  async function post(kind: ChatKind, text: string) {
    if (sending.value) return
    const trimmed = chatPostBody(kind, text)
    if (!trimmed) return
    // A new key when the draft is first sent, or when its kind changes.
    if (!clientKey.value || pendingKind.value !== kind) {
      clientKey.value = crypto.randomUUID()
      pendingKind.value = kind
    }
    // Storage keeps the typed body (not the Stop. default) for retries, and a
    // command's JSON so a reload can retry the same key.
    const key = draftKey()
    const t = target()
    writeChatDraft(key, { body: kind === 'command' ? trimmed : body.value, clientKey: clientKey.value, kind })
    sending.value = true
    sendError.value = ''
    try {
      const sent = kind === 'command' ? trimmed : chatWithPrefix(trimmed, t.prefix)
      const res = await postTaskChat(t.namespace, t.name, t.task, { body: sent, clientKey: clientKey.value, kind })
      writeChatDraft(key, null)
      if (key !== draftKey()) return
      if (kind !== 'command') body.value = ''
      clientKey.value = ''
      pendingKind.value = null
      onSent(res.id)
    } catch (e) {
      if (key === draftKey()) sendError.value = chatPostError(e)
    } finally {
      sending.value = false
    }
  }

  // postCommand: each click starts a new key unless the same body is still
  // pending from a failed attempt. A string is why it cannot be sent.
  function postCommand(cmd: ChatCommand | string) {
    if (typeof cmd === 'string') {
      sendError.value = cmd
      return
    }
    const text = JSON.stringify(cmd)
    if (pendingKind.value !== 'command' || readChatDraft(draftKey())?.body !== text) {
      clientKey.value = ''
      pendingKind.value = null
    }
    void post('command', text)
  }

  return { body, sending, sendError, post, postCommand }
}
