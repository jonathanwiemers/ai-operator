<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { chatPostError, postTaskChat } from '../api'
import type { ChatKind } from '../types'

const props = defineProps<{
  namespace: string
  name: string
  task: string
  // Show Interrupt next to Send while a delivered turn has no reply.
  canInterrupt?: boolean
}>()
const emit = defineEmits<{ posted: [id: number] }>()

const body = ref('')
const sending = ref(false)
const sendError = ref('')
// Retry key kept with the draft until a 2xx; same key on retry/reload.
const clientKey = ref('')
const pendingKind = ref<ChatKind | null>(null)

function storageKey(): string {
  return `desk-chat:${props.namespace}/${props.name}/${props.task}`
}

type Draft = { body: string; clientKey: string; kind: ChatKind }

function readDraft(): Draft | null {
  try {
    const raw = sessionStorage.getItem(storageKey())
    if (!raw) return null
    const d = JSON.parse(raw) as Draft
    if (
      typeof d?.body === 'string' &&
      typeof d?.clientKey === 'string' &&
      d.clientKey &&
      (d.kind === 'message' || d.kind === 'interrupt')
    ) {
      return d
    }
  } catch {
    /* private mode / quota */
  }
  return null
}

// writeDraft takes the key: a post that outlives a task switch must clear
// its own task's draft, not the one now shown.
function writeDraft(key: string, d: Draft | null) {
  try {
    if (!d) sessionStorage.removeItem(key)
    else sessionStorage.setItem(key, JSON.stringify(d))
  } catch {
    /* ignore */
  }
}

function restore() {
  const d = readDraft()
  if (!d) return
  body.value = d.body
  clientKey.value = d.clientKey
  pendingKind.value = d.kind
}

watch(
  () => [props.namespace, props.name, props.task] as const,
  () => {
    body.value = ''
    clientKey.value = ''
    pendingKind.value = null
    sendError.value = ''
    restore()
  },
)

onMounted(restore)

async function post(kind: ChatKind) {
  if (sending.value) return
  const text = body.value.trim() || (kind === 'interrupt' ? 'Stop.' : '')
  if (!text) return
  // New key when the draft is first sent, or when the kind changes.
  if (!clientKey.value || pendingKind.value !== kind) {
    clientKey.value = crypto.randomUUID()
    pendingKind.value = kind
  }
  // Keep the typed body in storage (not the Stop. default) for retries.
  const key = storageKey()
  writeDraft(key, { body: body.value, clientKey: clientKey.value, kind })
  sending.value = true
  sendError.value = ''
  try {
    const r = await postTaskChat(props.namespace, props.name, props.task, {
      body: text,
      clientKey: clientKey.value,
      kind,
    })
    writeDraft(key, null)
    if (key !== storageKey()) return
    body.value = ''
    clientKey.value = ''
    pendingKind.value = null
    emit('posted', r.id)
  } catch (e) {
    if (key === storageKey()) sendError.value = chatPostError(e)
  } finally {
    sending.value = false
  }
}

function send() {
  void post('message')
}

function interrupt() {
  void post('interrupt')
}
</script>

<template>
  <div class="composer">
    <div class="box">
      <textarea
        v-model="body"
        rows="3"
        placeholder="Message the agent…"
        :disabled="sending"
        @keydown.meta.enter="send"
        @keydown.ctrl.enter="send"
      />
      <div class="box-row">
        <span class="hint">Enter+Ctrl/Cmd to send</span>
        <button
          v-if="canInterrupt"
          type="button"
          class="interrupt"
          :disabled="sending"
          @click="interrupt"
        >
          Interrupt
        </button>
        <button type="button" class="send" :disabled="sending || !body.trim()" @click="send">
          {{ sending ? '…' : 'Send' }}
        </button>
      </div>
    </div>
    <p v-if="sendError" class="err">{{ sendError }}</p>
  </div>
</template>

<style scoped>
.composer {
  margin-top: 10px;
  padding: 0 14px 14px;
}
.box {
  background: var(--surface);
  border: 1px solid var(--border-strong);
  border-radius: 12px;
  box-shadow: var(--shadow);
}
.box:focus-within {
  border-color: var(--focus);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--focus) 20%, transparent);
}
textarea {
  width: 100%;
  border: 0;
  background: none;
  resize: vertical;
  padding: 12px 14px 4px;
  font: inherit;
  color: var(--text);
  outline: none;
  min-height: 64px;
  box-sizing: border-box;
}
textarea:disabled {
  opacity: 0.6;
}
.box-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px 8px 14px;
}
.hint {
  font-size: 12px;
  color: var(--text-3);
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.send,
.interrupt {
  border: 0;
  font-weight: 600;
  padding: 6px 14px;
  border-radius: 8px;
  cursor: pointer;
  font: inherit;
}
.send {
  background: var(--accent);
  color: #fff;
}
.interrupt {
  background: var(--tone-warning-bg);
  color: var(--tone-warning-fg);
  border: 1px solid color-mix(in srgb, var(--tone-warning-fg) 30%, var(--border));
}
.send:disabled,
.interrupt:disabled {
  opacity: 0.5;
  cursor: default;
}
.err {
  color: var(--tone-danger-fg);
  font-size: 13px;
  margin: 6px 0 0;
}
</style>
