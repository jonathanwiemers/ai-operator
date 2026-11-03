<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { chatPostError, postTaskChat } from '../api'
import { chatEfforts, type ChatCommand } from '../chatState'
import type { AutopilotItem, AutopilotPhase, ChatKind, ProposalView } from '../types'

const props = defineProps<{
  namespace: string
  name: string
  task: string
  // Show Interrupt next to Send while a delivered turn has no reply.
  canInterrupt?: boolean
  // Command buttons (same chat grant as Send).
  taskType?: 'Spec' | 'Implement' | 'Coordinate'
  autopilotPhase?: AutopilotPhase
  autopilotItems?: AutopilotItem[]
  proposals?: ProposalView[]
}>()
const emit = defineEmits<{ posted: [id: number] }>()

const body = ref('')
const sending = ref(false)
const sendError = ref('')
// Retry key kept with the draft until a 2xx; same key on retry/reload.
const clientKey = ref('')
const pendingKind = ref<ChatKind | null>(null)

// Model… form (Implement).
const modelOpen = ref(false)
const useEngine = ref<'claude' | 'cursor'>('claude')
const useModel = ref('')
const useEffort = ref('')
// Skip… (Spec autopilot).
const skipIssue = ref<number | ''>('')

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
      (d.kind === 'message' || d.kind === 'interrupt' || d.kind === 'command')
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
  clientKey.value = d.clientKey
  pendingKind.value = d.kind
  // Commands keep the key for retry without putting JSON in the textarea.
  if (d.kind === 'command') return
  body.value = d.body
}

watch(
  () => [props.namespace, props.name, props.task] as const,
  () => {
    body.value = ''
    clientKey.value = ''
    pendingKind.value = null
    sendError.value = ''
    modelOpen.value = false
    skipIssue.value = ''
    restore()
  },
)

onMounted(restore)

const skipOptions = computed(() =>
  (props.autopilotItems ?? []).filter((i) => i.state !== 'Merged'),
)

const openProposals = computed(() => props.proposals ?? [])

async function post(kind: ChatKind, text: string) {
  if (sending.value) return
  const trimmed = text.trim() || (kind === 'interrupt' ? 'Stop.' : '')
  if (!trimmed) return
  // New key when the draft is first sent, or when the kind/body changes.
  if (!clientKey.value || pendingKind.value !== kind) {
    clientKey.value = crypto.randomUUID()
    pendingKind.value = kind
  }
  // Keep the typed body in storage (not the Stop. default) for retries.
  // Commands store their JSON body so a reload can retry the same key.
  const storedBody = kind === 'command' ? trimmed : body.value
  const key = storageKey()
  writeDraft(key, { body: storedBody, clientKey: clientKey.value, kind })
  sending.value = true
  sendError.value = ''
  try {
    const r = await postTaskChat(props.namespace, props.name, props.task, {
      body: trimmed,
      clientKey: clientKey.value,
      kind,
    })
    writeDraft(key, null)
    if (key !== storageKey()) return
    if (kind !== 'command') body.value = ''
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
  void post('message', body.value)
}

function interrupt() {
  void post('interrupt', body.value)
}

// postCommand: each click starts a new key unless the same body is still
// pending from a failed attempt (same retry-safe clientKey as messages).
function postCommand(cmd: ChatCommand) {
  const text = JSON.stringify(cmd)
  if (pendingKind.value !== 'command' || readDraft()?.body !== text) {
    clientKey.value = ''
    pendingKind.value = null
  }
  void post('command', text)
}

function retry() {
  postCommand({ cmd: 'retry' })
}
function reviewAgain() {
  postCommand({ cmd: 'review' })
}
function useModelCmd() {
  const model = useModel.value.trim()
  if (!model) {
    sendError.value = 'Model needs a name.'
    return
  }
  const effort = useEffort.value
  if (effort && effort !== 'default') {
    postCommand({ cmd: 'use', engine: useEngine.value, model, effort })
  } else {
    postCommand({ cmd: 'use', engine: useEngine.value, model })
  }
}
function useDefault() {
  postCommand({ cmd: 'use', default: true })
}
function setEffort() {
  const effort = useEffort.value || 'default'
  postCommand({ cmd: 'effort', effort })
}
function pause() {
  postCommand({ cmd: 'pause' })
}
function resume() {
  postCommand({ cmd: 'resume' })
}
function skip() {
  const n = typeof skipIssue.value === 'number' ? skipIssue.value : Number(skipIssue.value)
  if (!Number.isFinite(n) || n <= 0) {
    sendError.value = 'Pick an issue to skip.'
    return
  }
  postCommand({ cmd: 'skip', issue: n })
}
function accept(n: number) {
  postCommand({ cmd: 'accept', proposal: n })
}
function reject(n: number) {
  postCommand({ cmd: 'reject', proposal: n })
}
</script>

<template>
  <div class="composer">
    <div v-if="taskType === 'Implement'" class="cmds">
      <button type="button" class="chip" :disabled="sending" @click="retry">Retry</button>
      <button type="button" class="chip" :disabled="sending" @click="reviewAgain">Review again</button>
      <button type="button" class="chip" :disabled="sending" :aria-expanded="modelOpen" @click="modelOpen = !modelOpen">
        Model…
      </button>
      <div v-if="modelOpen" class="model">
        <select v-model="useEngine" aria-label="Engine" :disabled="sending">
          <option value="claude">claude</option>
          <option value="cursor">cursor</option>
        </select>
        <input
          v-model="useModel"
          type="text"
          placeholder="model"
          aria-label="Model"
          :disabled="sending"
          @keydown.enter.prevent="useModelCmd"
        />
        <select v-model="useEffort" aria-label="Effort" :disabled="sending">
          <option value="">effort…</option>
          <option value="default">default</option>
          <option v-for="e in chatEfforts" :key="e" :value="e">{{ e }}</option>
        </select>
        <button type="button" class="chip" :disabled="sending || !useModel.trim()" @click="useModelCmd">Use</button>
        <button type="button" class="chip" :disabled="sending" @click="useDefault">Default</button>
        <button type="button" class="chip" :disabled="sending || !useEffort" @click="setEffort">Effort</button>
      </div>
    </div>

    <div v-else-if="taskType === 'Spec' && autopilotPhase" class="cmds">
      <button
        v-if="autopilotPhase === 'Paused'"
        type="button"
        class="chip"
        :disabled="sending"
        @click="resume"
      >
        Resume
      </button>
      <button
        v-else-if="autopilotPhase === 'Running'"
        type="button"
        class="chip"
        :disabled="sending"
        @click="pause"
      >
        Pause
      </button>
      <template v-if="skipOptions.length">
        <span class="skip-label">Skip…</span>
        <select v-model="skipIssue" aria-label="Issue to skip" :disabled="sending">
          <option value="">issue…</option>
          <option v-for="it in skipOptions" :key="it.iid" :value="it.iid">#{{ it.iid }}</option>
        </select>
        <button type="button" class="chip" :disabled="sending || !skipIssue" @click="skip">Skip</button>
      </template>
    </div>

    <div v-else-if="taskType === 'Coordinate' && openProposals.length" class="cmds">
      <template v-for="p in openProposals" :key="p.id">
        <button type="button" class="chip" :disabled="sending" :title="p.summary" @click="accept(p.id)">
          Accept P{{ p.id }}
        </button>
        <button type="button" class="chip" :disabled="sending" :title="p.summary" @click="reject(p.id)">
          Reject P{{ p.id }}
        </button>
      </template>
    </div>

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
.cmds {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
  margin-bottom: 8px;
}
.chip {
  border: 1px solid var(--border-strong);
  background: var(--surface);
  padding: 4px 10px;
  border-radius: 7px;
  font: inherit;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  color: var(--text);
}
.chip:disabled {
  opacity: 0.5;
  cursor: default;
}
.model {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
  width: 100%;
  padding: 6px 0 2px;
}
.model select,
.cmds select,
.model input {
  font: inherit;
  font-size: 13px;
  padding: 4px 8px;
  border: 1px solid var(--border-strong);
  border-radius: 7px;
  background: var(--surface);
  color: var(--text);
}
.model input {
  min-width: 10ch;
  flex: 1;
  max-width: 22ch;
}
.skip-label {
  font-size: 13px;
  color: var(--text-3);
  font-weight: 600;
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
