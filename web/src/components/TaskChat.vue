<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { getTaskChat, LoginRequiredError, NotFoundError } from '../api'
import { agentTurnPending, chatWaiting, commandLabel } from '../chatState'
import { live } from '../live'
import type { AutopilotItem, AutopilotPhase, ChatMessage, ChatState, ProposalView, TaskPhase } from '../types'
import Badge from './Badge.vue'
import ChatComposer from './ChatComposer.vue'
import RelTime from './RelTime.vue'
import Markdown from './Markdown.vue'

const props = defineProps<{
  namespace: string
  name: string
  task: string
  canPost: boolean
  canTranscript: boolean
  phase?: TaskPhase
  message?: string
  podName?: string
  taskType?: 'Spec' | 'Implement' | 'Coordinate'
  autopilotPhase?: AutopilotPhase
  autopilotItems?: AutopilotItem[]
  proposals?: ProposalView[]
}>()

const messages = ref<ChatMessage[]>([])
const loading = ref(true)
const loadingOlder = ref(false)
const hasOlder = ref(false)
const error = ref('')
let seq = 0
let pollTimer: number | undefined

const chronological = computed(() => [...messages.value].sort((a, b) => a.id - b.id))

const repliesByParent = computed(() => {
  const m = new Map<number, ChatMessage>()
  for (const msg of chronological.value) {
    if (msg.replyTo) m.set(msg.replyTo, msg)
  }
  return m
})

// User messages and orphan agent/system rows; replies nest under their parent.
const turns = computed(() => {
  const ids = new Set(chronological.value.map((m) => m.id))
  return chronological.value.filter((msg) => {
    if (msg.role === 'user') return true
    if (msg.replyTo && ids.has(msg.replyTo)) return false
    return true
  })
})

const newestUser = computed(() => {
  for (let i = chronological.value.length - 1; i >= 0; i--) {
    const m = chronological.value[i]!
    if (m.role === 'user') return m
  }
  return null
})

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

function stateLabel(state: ChatState | undefined): {
  text: string
  tone: 'muted' | 'progress' | 'success' | 'danger' | 'warning'
} {
  switch (state) {
    case 'queued':
      return { text: 'waiting', tone: 'muted' }
    case 'delivered':
      return { text: 'sent to the agent', tone: 'progress' }
    case 'answered':
      return { text: 'answered', tone: 'success' }
    case 'failed':
      return { text: 'failed', tone: 'danger' }
    case 'deferred':
      return { text: 'deferred', tone: 'warning' }
    default:
      return { text: state || '', tone: 'muted' }
  }
}

function authorLabel(m: ChatMessage): string {
  if (m.role === 'agent') return 'agent'
  if (m.role === 'system') return 'system'
  return m.authorName || m.author || 'you'
}

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

onMounted(() => {
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
        <button type="button" class="btn" :disabled="loadingOlder" @click="loadOlder">
          {{ loadingOlder ? 'Loading…' : 'Older messages' }}
        </button>
      </div>
      <div v-if="!chronological.length" class="empty">No messages yet.</div>
      <div v-else class="thread">
        <div v-for="msg in turns" :key="msg.id" class="turn">
          <div class="bubble" :class="msg.role">
            <div class="meta">
              <strong>{{ authorLabel(msg) }}</strong>
              <span v-if="msg.kind === 'interrupt'" class="faint">interrupt</span>
              <span v-else-if="msg.kind === 'command'" class="faint">command</span>
              <span class="faint"><RelTime :time="msg.at" /></span>
              <Badge
                v-if="msg.role === 'user' && msg.state"
                :tone="stateLabel(msg.state).tone"
                :title="msg.reason"
              >
                {{ stateLabel(msg.state).text }}
              </Badge>
            </div>
            <p v-if="msg.redacted && !msg.body" class="withheld">message hidden</p>
            <p v-else-if="msg.kind === 'command'" class="cmd">{{ commandLabel(msg.body) }}</p>
            <Markdown v-else-if="msg.body" :text="msg.body" />
            <p v-if="msg.role === 'user' && msg.state === 'failed' && msg.reason" class="reason">
              {{ msg.reason }}
            </p>
          </div>
          <template v-if="repliesByParent.get(msg.id)">
            <div class="bubble reply" :class="repliesByParent.get(msg.id)!.role">
              <div class="meta">
                <strong>{{ authorLabel(repliesByParent.get(msg.id)!) }}</strong>
                <span class="faint"><RelTime :time="repliesByParent.get(msg.id)!.at" /></span>
              </div>
              <p
                v-if="repliesByParent.get(msg.id)!.redacted && !repliesByParent.get(msg.id)!.body"
                class="withheld"
              >
                message hidden
              </p>
              <Markdown
                v-else-if="repliesByParent.get(msg.id)!.body"
                :text="repliesByParent.get(msg.id)!.body!"
              />
            </div>
          </template>
        </div>
      </div>

      <div v-if="statusLine" class="status">
        <Badge tone="progress" dot pulse>{{ statusLine }}</Badge>
        <a v-if="canTranscript" class="t" href="#live-transcript">Live transcript</a>
      </div>

      <ChatComposer
        v-if="canPost"
        :namespace="namespace"
        :name="name"
        :task="task"
        :can-interrupt="canInterrupt"
        :task-type="taskType"
        :autopilot-phase="autopilotPhase"
        :autopilot-items="autopilotItems"
        :proposals="proposals"
        @posted="loadNewer"
      />
    </template>
  </div>
</template>

<style scoped>
.chat {
  overflow: hidden;
}
.older {
  display: flex;
  justify-content: center;
  padding: 10px;
  border-bottom: 1px solid var(--border);
}
.btn {
  border: 1px solid var(--border-strong);
  background: var(--surface);
  padding: 4px 12px;
  border-radius: 7px;
  font: inherit;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  color: var(--text);
}
.btn:disabled {
  opacity: 0.5;
  cursor: default;
}
.thread {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 14px 16px;
  max-height: 520px;
  overflow-y: auto;
}
.turn {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.bubble {
  padding: 10px 12px;
  border-radius: var(--radius);
  background: var(--surface-2);
  border: 1px solid var(--border);
}
.bubble.user {
  background: var(--surface);
}
.bubble.system,
.bubble.reply.system {
  background: var(--tone-danger-bg);
  border-color: color-mix(in srgb, var(--tone-danger-fg) 25%, var(--border));
  color: var(--tone-danger-fg);
}
.bubble.reply {
  margin-left: 18px;
}
.meta {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
  font-size: 13px;
  margin-bottom: 4px;
}
.cmd {
  margin: 0;
  font-family: var(--mono);
  font-size: 13px;
}
.withheld,
.reason {
  margin: 0;
  font-size: 13px;
  color: var(--text-3);
}
.reason {
  color: var(--tone-danger-fg);
  margin-top: 6px;
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
.empty {
  padding: 24px 16px;
  text-align: center;
  color: var(--text-3);
}
</style>
