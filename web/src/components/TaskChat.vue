<script setup lang="ts">
import UEmpty from '@nuxt/ui/components/Empty.vue'
import UButton from '@nuxt/ui/components/Button.vue'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { chatPostError } from '../api'
import { getTaskChat, LoginRequiredError, NotFoundError, postTaskChat } from '../client'
import {
  agentTurnPending,
  chatDraftKey,
  chatEfforts,
  chatWithPrefix,
  focusTurns,
  chatPostBody,
  chatTurns,
  chatWaiting,
  modelCommand,
  readChatDraft,
  skipCommand,
  skipOptions,
  writeChatDraft,
  type ChatCommand,
} from '../chatState'
import { live } from '../live'
import { keepThroughSignIn } from '../signInDraft'
import type { RouteLocationRaw } from 'vue-router'
import type { AutopilotItem, AutopilotPhase, ChatKind, ChatMessage, ProposalView, TaskPhase } from '../types'
import Badge from './Badge.vue'
import ChatThread from './ChatThread.vue'
import Composer, { type ComposerSend } from './Composer.vue'

const props = defineProps<{
  namespace: string
  name: string
  task: string
  canPost: boolean
  // transcript is where the "Live transcript" link goes, none without access.
  transcript?: RouteLocationRaw
  phase?: TaskPhase
  message?: string
  podName?: string
  taskType?: 'Spec' | 'Implement' | 'Coordinate'
  autopilotPhase?: AutopilotPhase
  autopilotItems?: AutopilotItem[]
  proposals?: ProposalView[]
  // focus lists the threads whose message starts with it first;
  // messagePrefix goes before every message sent (a problem card, R9).
  focus?: string
  messagePrefix?: string
}>()

const messages = ref<ChatMessage[]>([])
const loading = ref(true)
const loadingOlder = ref(false)
const hasOlder = ref(false)
const error = ref('')
let seq = 0
let pollTimer: number | undefined

// User messages and orphan agent/system rows; replies nest under their parent.
const grouped = computed(() => chatTurns(messages.value))
const chronological = computed(() => grouped.value.chronological)
const repliesByParent = computed(() => grouped.value.repliesByParent)
const turns = computed(() => focusTurns(grouped.value.turns, props.focus))
const newestUser = computed(() => grouped.value.newestUser)

const waiting = computed(() => {
  const u = newestUser.value
  if (!u) return null
  const pending = agentTurnPending(u.kind, u.state, repliesByParent.value.has(u.id))
  return chatWaiting({
    pending,
    delivered: u.state === 'delivered',
    phase: props.phase,
    message: props.message,
    podName: props.podName,
  })
})

const statusLine = computed(() => waiting.value?.label ?? '')
const canInterrupt = computed(() => !!waiting.value?.canInterrupt)

function mergeMessages(incoming: ChatMessage[]) {
  const byId = new Map(messages.value.map((m) => [m.id, m]))
  for (const m of incoming) byId.set(m.id, m)
  messages.value = [...byId.values()].sort((a, b) => b.id - a.id)
}

async function loadInitial() {
  const mine = ++seq
  loading.value = true
  error.value = ''
  try {
    const page = await getTaskChat(props.namespace, props.name, props.task, { limit: 50 })
    if (mine !== seq) return
    messages.value = page.messages
    hasOlder.value = page.messages.length >= 50
  } catch (e) {
    if (mine !== seq || e instanceof LoginRequiredError) return
    if (e instanceof NotFoundError) {
      error.value = 'Desk chat is not available.'
      messages.value = []
    } else {
      error.value = e instanceof Error ? e.message : String(e)
    }
  } finally {
    if (mine === seq) loading.value = false
  }
}

async function loadOlder() {
  const oldest = messages.value.reduce((min, m) => (m.id < min ? m.id : min), Number.POSITIVE_INFINITY)
  if (!Number.isFinite(oldest) || loadingOlder.value) return
  const mine = seq
  loadingOlder.value = true
  try {
    const page = await getTaskChat(props.namespace, props.name, props.task, {
      before: oldest,
      limit: 50,
    })
    if (mine !== seq) return
    mergeMessages(page.messages)
    hasOlder.value = page.messages.length >= 50
  } catch (e) {
    if (mine !== seq || e instanceof LoginRequiredError) return
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    if (mine === seq) loadingOlder.value = false
  }
}

// loadNewer refreshes the newest page and merges it: new rows (replies)
// and state changes of shown ones (queued → delivered → answered/failed),
// which an after= cursor would miss.
async function loadNewer() {
  if (!messages.value.length) {
    await loadInitial()
    return
  }
  const mine = seq
  try {
    const page = await getTaskChat(props.namespace, props.name, props.task, { limit: 50 })
    // A response for the task shown before a switch is dropped.
    if (mine === seq) mergeMessages(page.messages)
  } catch {
    /* live refresh is best-effort */
  }
}

function clearPoll() {
  if (pollTimer !== undefined) {
    window.clearInterval(pollTimer)
    pollTimer = undefined
  }
}

function syncPoll() {
  clearPoll()
  if (!waiting.value) return
  pollTimer = window.setInterval(() => {
    if (document.visibilityState === 'hidden') return
    void loadNewer()
  }, 5000)
}

watch(
  () => [props.namespace, props.name, props.task] as const,
  () => {
    // Drop the old task's thread at once; loadInitial's seq bump discards
    // responses still in flight for it.
    messages.value = []
    hasOlder.value = false
    loadingOlder.value = false
    void loadInitial()
  },
)

watch(
  () => live.version,
  () => {
    if (document.visibilityState === 'hidden') return
    void loadNewer()
  },
)

watch(waiting, syncPoll, { immediate: true })

function onVisibility() {
  if (document.visibilityState === 'hidden') {
    clearPoll()
    return
  }
  syncPoll()
  if (waiting.value) void loadNewer()
}

// The composer (Composer.vue, mode chat): the draft and its clientKey stay
// in sessionStorage until a 2xx, so a retry or a reload reuses the key.
const body = ref('')
const sending = ref(false)
const sendError = ref('')
const clientKey = ref('')
const pendingKind = ref<ChatKind | null>(null)
keepThroughSignIn(() => `chat:${props.namespace}/${props.name}/${props.task}${props.messagePrefix ? `#${props.messagePrefix}` : ''}`, body)
// Model… form (Implement) and Skip… (Spec autopilot).
const modelOpen = ref(false)
const useEngine = ref<'claude' | 'cursor'>('claude')
const useModel = ref('')
const useEffort = ref('')
const skipIssue = ref<number | ''>('')
const skippable = computed(() => skipOptions(props.autopilotItems))
// The command row's classes and its buttons' look.
const cmds = 'mb-2 flex flex-wrap items-center gap-1.5'
const chip = { color: 'neutral', variant: 'outline', size: 'sm' } as const
const field = 'rounded-[7px] border border-(--border-strong) bg-(--surface) px-2 py-1 font-[inherit] text-[13px] text-(--text)'

const draftKey = () => chatDraftKey(props.namespace, props.name, props.task, props.messagePrefix)

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
  () => [props.namespace, props.name, props.task] as const,
  () => {
    body.value = ''
    clientKey.value = ''
    pendingKind.value = null
    sendError.value = ''
    modelOpen.value = false
    skipIssue.value = ''
    restoreDraft()
  },
)

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
  writeChatDraft(key, { body: kind === 'command' ? trimmed : body.value, clientKey: clientKey.value, kind })
  sending.value = true
  sendError.value = ''
  try {
    const sent = kind === 'command' ? trimmed : chatWithPrefix(trimmed, props.messagePrefix)
    await postTaskChat(props.namespace, props.name, props.task, { body: sent, clientKey: clientKey.value, kind })
    writeChatDraft(key, null)
    if (key !== draftKey()) return
    if (kind !== 'command') body.value = ''
    clientKey.value = ''
    pendingKind.value = null
    void loadNewer()
  } catch (e) {
    if (key === draftKey()) sendError.value = chatPostError(e)
  } finally {
    sending.value = false
  }
}

function onSend(s: ComposerSend) {
  void post(s.kind, s.body)
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

onMounted(() => {
  restoreDraft()
  void loadInitial()
  document.addEventListener('visibilitychange', onVisibility)
})
onUnmounted(() => {
  clearPoll()
  document.removeEventListener('visibilitychange', onVisibility)
})
</script>

<template>
  <div class="card chat">
    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="loading && !messages.length" class="empty">Loading…</div>
    <template v-else>
      <div v-if="hasOlder" class="older">
        <UButton
          color="neutral"
          variant="outline"
          size="sm"
          :disabled="loadingOlder"
          :label="loadingOlder ? 'Loading…' : 'Older messages'"
          @click="loadOlder"
        />
      </div>
      <UEmpty v-if="!chronological.length" icon="i-lucide-message-square" title="No messages yet." variant="naked" size="sm" />
      <ChatThread v-else :turns="turns" :replies="repliesByParent" />

      <div v-if="statusLine" class="status">
        <Badge tone="progress" dot pulse>{{ statusLine }}</Badge>
        <RouterLink v-if="transcript" class="t" :to="transcript">Live transcript</RouterLink>
      </div>

      <Composer
        v-if="canPost"
        v-model="body"
        mode="chat"
        :placeholder="messagePrefix ? 'Write to the pilot about this problem…' : 'Message the agent…'"
        hint="Enter+Ctrl/Cmd to send"
        :busy="sending"
        :disabled="sending"
        :error="sendError"
        :can-interrupt="canInterrupt"
        @send="onSend"
      >
        <template #controls>
          <div v-if="taskType === 'Implement'" :class="cmds">
            <UButton v-bind="chip" label="Retry" :disabled="sending" @click="postCommand({ cmd: 'retry' })" />
            <UButton v-bind="chip" label="Review again" :disabled="sending" @click="postCommand({ cmd: 'review' })" />
            <UButton v-bind="chip" label="Model…" :disabled="sending" :aria-expanded="modelOpen" @click="modelOpen = !modelOpen" />
            <div v-if="modelOpen" :class="[cmds, 'w-full pt-1.5 pb-0.5']">
              <select v-model="useEngine" :class="field" aria-label="Engine" :disabled="sending">
                <option value="claude">claude</option>
                <option value="cursor">cursor</option>
              </select>
              <input
                v-model="useModel"
                :class="[field, 'max-w-[22ch] min-w-[10ch] flex-1']"
                type="text"
                placeholder="model"
                aria-label="Model"
                :disabled="sending"
                @keydown.enter.prevent="postCommand(modelCommand(useEngine, useModel, useEffort))"
              />
              <select v-model="useEffort" :class="field" aria-label="Effort" :disabled="sending">
                <option value="">effort…</option>
                <option value="default">default</option>
                <option v-for="e in chatEfforts" :key="e" :value="e">{{ e }}</option>
              </select>
              <UButton
                v-bind="chip"
                label="Use"
                :disabled="sending || !useModel.trim()"
                @click="postCommand(modelCommand(useEngine, useModel, useEffort))"
              />
              <UButton v-bind="chip" label="Default" :disabled="sending" @click="postCommand({ cmd: 'use', default: true })" />
              <UButton
                v-bind="chip"
                label="Effort"
                :disabled="sending || !useEffort"
                @click="postCommand({ cmd: 'effort', effort: useEffort || 'default' })"
              />
            </div>
          </div>
          <div v-else-if="taskType === 'Spec' && autopilotPhase" :class="cmds">
            <UButton
              v-if="autopilotPhase === 'Paused'"
              v-bind="chip"
              label="Resume"
              :disabled="sending"
              @click="postCommand({ cmd: 'resume' })"
            />
            <UButton
              v-else-if="autopilotPhase === 'Running'"
              v-bind="chip"
              label="Pause"
              :disabled="sending"
              @click="postCommand({ cmd: 'pause' })"
            />
            <template v-if="skippable.length">
              <span class="text-[13px] font-semibold text-(--text-3)">Skip…</span>
              <select v-model="skipIssue" :class="field" aria-label="Issue to skip" :disabled="sending">
                <option value="">issue…</option>
                <option v-for="it in skippable" :key="it.iid" :value="it.iid">#{{ it.iid }}</option>
              </select>
              <UButton v-bind="chip" label="Skip" :disabled="sending || !skipIssue" @click="postCommand(skipCommand(skipIssue))" />
            </template>
          </div>
          <div v-else-if="taskType === 'Coordinate' && proposals?.length" :class="cmds">
            <template v-for="p in proposals" :key="p.id">
              <UButton
                v-bind="chip"
                :label="`Accept P${p.id}`"
                :disabled="sending"
                :title="p.summary"
                @click="postCommand({ cmd: 'accept', proposal: p.id })"
              />
              <UButton
                v-bind="chip"
                :label="`Reject P${p.id}`"
                :disabled="sending"
                :title="p.summary"
                @click="postCommand({ cmd: 'reject', proposal: p.id })"
              />
            </template>
          </div>
        </template>
      </Composer>
    </template>
  </div>
</template>

<style scoped>
/* clip, not hidden: hidden would make the card the scroll container of the
   composer, which is sticky on phones. */
.chat {
  overflow: clip;
}
.older {
  display: flex;
  justify-content: center;
  padding: 10px;
  border-bottom: 1px solid var(--border);
}
.status {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 16px;
  border-top: 1px solid var(--border);
  background: var(--surface-2);
}
.status .t {
  font-size: 13px;
}
</style>
