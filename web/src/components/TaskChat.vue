<script setup lang="ts">
import { errorMessage } from '../format'
import UAlert from '@nuxt/ui/components/Alert.vue'
import UTooltip from '@nuxt/ui/components/Tooltip.vue'
import { alertColor } from '../badgeTone'
import UButton from '@nuxt/ui/components/Button.vue'
import UInput from '@nuxt/ui/components/Input.vue'
import USelect from '@nuxt/ui/components/Select.vue'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useChatSend } from '../chatSend'
import { getTaskChat, LoginRequiredError, NotFoundError } from '../client'
import { agentTurnPending, chatEfforts, chatTurns, chatWaiting, modelCommand, skipCommand, skipOptions } from '../chatState'
import { chatMessages } from '../chatView'
import { live } from '../live'
import type { RouteLocationRaw } from 'vue-router'
import type { AutopilotItem, AutopilotPhase, ChatMessage, ProposalView, TaskPhase } from '../types'
import Badge from './Badge.vue'
import DeskChat from '../ui/DeskChat.vue'
import ChatTurn from './ChatTurn.vue'
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
  // messagePrefix goes before every message sent.
  messagePrefix?: string
}>()

const messages = ref<ChatMessage[]>([])
const loading = ref(true)
const loadingOlder = ref(false)
const hasOlder = ref(false)
const error = ref('')
let seq = 0
let pollTimer: number | undefined

// The chat oldest first, each reply after the message it answers (DeskChat).
const grouped = computed(() => chatTurns(messages.value))
const shown = computed(() => chatMessages(grouped.value.chronological))
const repliesByParent = computed(() => grouped.value.repliesByParent)
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

// quiet: a refresh of an empty thread (after the first message), which keeps
// the thread and its composer instead of showing Loading…, so the composer
// keeps its focus.
async function loadInitial(quiet = false) {
  const mine = ++seq
  if (!quiet) loading.value = true
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
      error.value = errorMessage(e)
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
    error.value = errorMessage(e)
  } finally {
    if (mine === seq) loadingOlder.value = false
  }
}

// loadNewer refreshes the newest page and merges it: new rows (replies)
// and state changes of shown ones (queued → delivered → answered/failed),
// which an after= cursor would miss.
async function loadNewer() {
  if (!messages.value.length) {
    await loadInitial(true)
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

// The composer (Composer.vue, mode chat) and its sending (chatSend.ts).
const { body, sending, sendError, post, postCommand } = useChatSend(
  () => ({ namespace: props.namespace, name: props.name, task: props.task, prefix: props.messagePrefix }),
  () => void loadNewer(),
)
// Model… form (Implement) and Skip… (Spec autopilot).
const modelOpen = ref(false)
const useEngine = ref<'claude' | 'cursor'>('claude')
const useModel = ref('')
const useEffort = ref<string>()
const skipIssue = ref<number>()
const skippable = computed(() => skipOptions(props.autopilotItems))
// The command row's classes and its buttons' look.
const cmds = 'mb-2 flex flex-wrap items-center gap-1.5'
const chip = { color: 'neutral', variant: 'outline', size: 'sm' } as const
const field = {
  size: 'sm',
  trailingIcon: 'i-lucide-chevron-down',
  selectedIcon: 'i-lucide-check',
  ui: { item: 'min-h-11 tablet:min-h-0' },
} as const
const engineItems = ['claude', 'cursor']
const effortItems = ['default', ...chatEfforts]

watch(
  () => [props.namespace, props.name, props.task] as const,
  () => {
    modelOpen.value = false
    skipIssue.value = undefined
  },
)

function onSend(s: ComposerSend) {
  void post(s.kind, s.body)
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
  <div class="card flex h-[min(72dvh,680px)] min-h-[360px] flex-col gap-2 overflow-clip p-3" data-task-chat>
    <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" :description="error" />
    <div v-if="loading && !messages.length" class="empty">Loading…</div>
    <DeskChat
      v-else
      label="Chat with the agent"
      :replier="taskType === 'Coordinate' ? 'Pilot' : 'Agent'"
      :messages="shown"
      :older="hasOlder"
      :loading-older="loadingOlder"
      :waiting="!!waiting"
      empty="No messages yet."
      @older="loadOlder"
    >
      <template #content="{ message }">
        <ChatTurn v-if="message.metadata.chat" :message="message.metadata.chat" />
      </template>
      <template #bar>
        <div v-if="statusLine" class="flex items-center gap-2.5" data-chat-status>
          <Badge tone="progress" dot pulse>{{ statusLine }}</Badge>
          <RouterLink v-if="transcript" class="text-[13px]" :to="transcript">Live transcript</RouterLink>
        </div>
      </template>
      <template #prompt>
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
                <USelect v-model="useEngine" :items="engineItems" v-bind="field" aria-label="Engine" :disabled="sending" />
                <UInput
                  v-model="useModel"
                  size="sm"
                  class="max-w-[22ch] min-w-[10ch] flex-1"
                  type="text"
                  placeholder="model"
                  aria-label="Model"
                  :disabled="sending"
                  @keydown.enter.prevent="postCommand(modelCommand(useEngine, useModel, useEffort ?? ''))"
                />
                <USelect
                  v-model="useEffort"
                  :items="effortItems"
                  v-bind="field"
                  placeholder="effort…"
                  aria-label="Effort"
                  :disabled="sending"
                />
                <UButton
                  v-bind="chip"
                  label="Use"
                  :disabled="sending || !useModel.trim()"
                  @click="postCommand(modelCommand(useEngine, useModel, useEffort ?? ''))"
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
                <USelect
                  v-model="skipIssue"
                  :items="skippable.map((it) => ({ label: `#${it.iid}`, value: it.iid }))"
                  v-bind="field"
                  placeholder="issue…"
                  aria-label="Issue to skip"
                  :disabled="sending"
                />
                <UButton v-bind="chip" label="Skip" :disabled="sending || !skipIssue" @click="postCommand(skipCommand(skipIssue ?? 0))" />
              </template>
            </div>
            <div v-else-if="taskType === 'Coordinate' && proposals?.length" :class="cmds">
              <template v-for="p in proposals" :key="p.id">
                <UTooltip :text="p.summary" :disabled="!p.summary">
                  <UButton
                    v-bind="chip"
                    :label="`Accept P${p.id}`"
                    :disabled="sending"
                    @click="postCommand({ cmd: 'accept', proposal: p.id })"
                  />
                </UTooltip>
                <UTooltip :text="p.summary" :disabled="!p.summary">
                  <UButton
                    v-bind="chip"
                    :label="`Reject P${p.id}`"
                    :disabled="sending"
                    @click="postCommand({ cmd: 'reject', proposal: p.id })"
                  />
                </UTooltip>
              </template>
            </div>
</template>
        </Composer>
      </template>
    </DeskChat>
  </div>
</template>
