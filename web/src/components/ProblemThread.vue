<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import UBadge from '@nuxt/ui/components/Badge.vue'
import { computed, onMounted, ref, watch } from 'vue'
import { chatPostError } from '../api'
import { alertColor } from '../badgeTone'
import { chatWithPrefix, problemChatRef } from '../chatState'
import { eventMessages, type DeskChatMessage } from '../chatView'
import { getProblem, postTaskChat, warmTask } from '../client'
import { live, useNow } from '../live'
import { needsOpen } from '../needsOpen'
import { hidesReason, problemCause } from '../problemFacts'
import {
  localMessages,
  newestAt,
  promptMessage,
  problemSubject,
  statusLine,
  withAnswer,
  type AnswerEvent,
  type LocalAnswer,
} from '../problemThread'
import type { MilestoneView, ProblemThread, StreamEvent } from '../types'
import DeskChat from '../ui/DeskChat.vue'
import PauseCard from './PauseCard.vue'
import ProblemFacts from './ProblemFacts.vue'
import PromptCard from './PromptCard.vue'

// ProblemThread is one problem as a chat (spec desk-problem-thread,
// Decisions 7, 8, 13): its facts on top, its history oldest first, and one
// bar to act above the text box. The bar is the newest open question's
// card, else the pause's or hold's ways out; never both. An answer shows as
// the owner's message at once (sending, sent), until the operator's events
// arrive; 60 s without one adds a note with the task's logs. The text box
// writes to the pilot, prefixed "problem: #N". Without the history
// database the thread is the closed questions, and says so.
const props = defineProps<{ namespace: string; name: string; milestone: MilestoneView; problem: string }>()

const thread = ref<ProblemThread | null>(null)
const failed = ref('')
// older: the pages loaded with Load older (newest first, like the API).
const older = ref<StreamEvent[]>([])
const olderCursor = ref<string | undefined>()
const loadingOlder = ref(false)
let seq = 0

async function load() {
  const mine = ++seq
  try {
    const t = await getProblem(props.namespace, props.name, props.milestone.iid, props.problem)
    if (mine !== seq) return
    thread.value = t
    failed.value = ''
    if (!older.value.length) olderCursor.value = t.older
  } catch (e) {
    if (mine === seq) failed.value = e instanceof Error ? e.message : String(e)
  }
}

async function loadOlder() {
  const before = olderCursor.value
  if (!before || loadingOlder.value) return
  loadingOlder.value = true
  try {
    const t = await getProblem(props.namespace, props.name, props.milestone.iid, props.problem, before)
    older.value = [...older.value, ...t.events]
    olderCursor.value = t.older
  } catch (e) {
    failed.value = e instanceof Error ? e.message : String(e)
  } finally {
    loadingOlder.value = false
  }
}

watch(
  () => props.problem,
  () => {
    thread.value = null
    older.value = []
    olderCursor.value = undefined
    locals.value = []
    void load()
  },
)
watch(
  () => live.version,
  () => void load(),
)
onMounted(load)

const iid = computed(() => thread.value?.problem.issueIID ?? 0)
const prefix = computed(() => problemChatRef(iid.value))

// events: the first page and the older ones, newest first, each once.
const events = computed(() => {
  const seen = new Set<string>()
  const out: StreamEvent[] = []
  for (const e of [...(thread.value?.events ?? []), ...older.value]) {
    const k = `${e.kind}:${e.id}`
    if (seen.has(k)) continue
    seen.add(k)
    out.push(e)
  }
  return out
})

const now = useNow()
const locals = ref<LocalAnswer[]>([])
function onAnswer(e: AnswerEvent) {
  locals.value = withAnswer(locals.value, e, Date.now())
}

const messages = computed((): DeskChatMessage[] => {
  const t = thread.value
  if (!t) return []
  const history = t.history
    ? eventMessages(events.value, prefix.value)
    : t.prompts
        .filter((p) => p.id !== t.prompt?.id)
        .map(promptMessage)
        .sort((a, b) => Date.parse(a.metadata.at ?? '') - Date.parse(b.metadata.at ?? ''))
  const project = { namespace: props.namespace, name: props.name }
  return [...history, ...localMessages(locals.value, newestAt(events.value), now.value, project)]
})

const status = computed(() => (thread.value ? statusLine(thread.value.problem.status) : undefined))
const facts = computed(() => thread.value?.facts ?? props.milestone.problems?.find((f) => f.iid === iid.value))
const cause = computed(() => thread.value?.problem.cause || problemCause(facts.value))
const heading = computed(() => {
  const p = thread.value?.problem
  return p ? [problemSubject(p), p.title].filter(Boolean).join(' ') : ''
})
const issueTo = computed(() => ({
  name: 'issue' as const,
  params: { namespace: props.namespace, name: props.name, iid: String(props.milestone.iid), issue: String(iid.value) },
}))

// The bar: the open question, else the ways out of the pause or hold that
// belong to this problem (needsOpen knows which).
const open = computed(() => needsOpen(props.milestone).problems.find((p) => p.iid === iid.value))
const resolved = computed(() => thread.value?.problem.state !== 'open')
const prompt = computed(() => (resolved.value ? undefined : thread.value?.prompt))
const pauseBar = computed(() => !resolved.value && !prompt.value && !!open.value?.paused)
const holdBar = computed(() => (!resolved.value && !prompt.value && !pauseBar.value ? open.value?.hold : undefined))

// The text box: the pilot's chat about this problem.
const pilot = computed(() => thread.value?.pilot)
const canChat = computed(() => !!pilot.value && !!thread.value?.permissions.chat)
onMounted(() => {
  if (props.milestone.pilot && props.milestone.permissions?.chat) void warmTask(props.namespace, props.name, props.milestone.pilot.task)
})
const draft = ref('')
const busy = ref(false)
const chatError = ref('')
async function submit(text: string) {
  const task = pilot.value
  if (!task || busy.value) return
  busy.value = true
  chatError.value = ''
  const shown = { id: crypto.randomUUID(), label: text, task }
  onAnswer({ ...shown, send: 'sending' })
  try {
    await postTaskChat(props.namespace, props.name, task, {
      body: chatWithPrefix(text, prefix.value),
      clientKey: shown.id,
      kind: 'message',
    })
    draft.value = ''
    onAnswer({ ...shown, send: 'sent' })
  } catch (e) {
    chatError.value = chatPostError(e)
    onAnswer({ ...shown, send: 'failed', error: chatError.value })
  } finally {
    busy.value = false
  }
}

const waiting = computed(() => locals.value.some((a) => a.send === 'sending'))
const tones = { warning: 'warning', progress: 'info', success: 'success', neutral: 'neutral' } as const
</script>

<template>
  <section class="flex min-h-0 min-w-0 flex-1 flex-col gap-2" :data-problem-thread="problem">
    <UAlert v-if="failed && !thread" :color="alertColor('danger')" variant="subtle" description="The problem could not be loaded." />
    <div v-else-if="!thread" class="empty">Loading…</div>
    <template v-else>
      <header class="grid min-w-0 gap-1.5" data-problem-head>
        <div class="flex flex-wrap items-baseline gap-x-2 gap-y-1">
          <RouterLink v-if="iid" :to="issueTo" class="min-w-0 font-semibold wrap-anywhere">{{ heading }}</RouterLink>
          <b v-else>{{ heading }}</b>
          <UBadge v-if="status" class="ms-auto" size="sm" variant="subtle" :color="tones[status.tone]" :label="status.text" data-problem-status />
        </div>
        <p v-if="cause" class="m-0 min-w-0 font-medium wrap-anywhere" data-problem-cause>{{ cause }}</p>
        <ProblemFacts v-if="facts" :namespace="namespace" :name="name" :facts="facts" />
        <p v-if="!thread.history" class="m-0 text-[12.5px] text-(--text-3)" data-history-off>
          History is off: the history database is not set up, so this shows the questions only.
        </p>
      </header>
      <DeskChat
        v-model="draft"
        class="min-h-[320px]"
        label="Problem thread"
        replier="Pilot"
        :messages="messages"
        :older="!!olderCursor"
        :loading-older="loadingOlder"
        :waiting="waiting"
        :no-prompt="!canChat"
        :busy="busy"
        :error="chatError"
        placeholder="Write to the pilot about this problem…"
        empty="Nothing recorded yet"
        @older="loadOlder"
        @submit="submit"
      >
        <template #bar>
          <PromptCard v-if="prompt" :key="prompt.id" :namespace="namespace" :name="name" :prompt="prompt" :facts="facts" @answer="onAnswer" />
          <PauseCard
            v-else-if="pauseBar"
            :namespace="namespace"
            :name="name"
            :milestone="milestone"
            :facts="facts"
            :no-reason="hidesReason(cause, milestone.autopilot?.pauseKind)"
            no-status
            @answer="onAnswer"
          />
          <PauseCard
            v-else-if="holdBar"
            :namespace="namespace"
            :name="name"
            :milestone="milestone"
            :hold="holdBar"
            :facts="facts"
            :no-reason="hidesReason(cause, holdBar.kind)"
            @answer="onAnswer"
          />
        </template>
      </DeskChat>
    </template>
  </section>
</template>
