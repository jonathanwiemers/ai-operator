<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import { alertColor } from '../badgeTone'
import { computed, ref, watch } from 'vue'
import { hasFeature } from '../api'
import { getStream, LoginRequiredError } from '../client'
import { live } from '../live'
import { streamMessages } from '../chatView'
import { chatStreamKinds, groupThreads, kindsForFilter, streamEventKey } from '../stream'
import DeskChat from '../ui/DeskChat.vue'
import EventCard from './stream/EventCard.vue'
import type { PromptView, StreamEvent, StreamKind } from '../types'

// An issue's stream (R10): GET /api/v1/stream with project, milestone and
// issue, as a chat (DeskChat): oldest first, *Load older* pages back, live
// events come in at the bottom. It reads history, so it outlives the issue's tasks. Without an issue
// it is the milestone's stream, and kinds fixes its kinds (the pilot drawer,
// R11); chat kinds are asked for only with the chat feature.
const props = defineProps<{
  namespace: string
  name: string
  milestone: number
  issue?: number
  kinds?: readonly StreamKind[]
  canSteer?: boolean
  // prompts: the view's prompts, for the state badge of prompt.posted rows.
  prompts?: PromptView[]
}>()

const events = ref<StreamEvent[]>([])
const older = ref<string | undefined>()
const newest = ref<string | undefined>()
const historyOn = ref(true)
const loading = ref(true)
const loadingOlder = ref(false)
const error = ref('')
let seq = 0

const messages = computed(() => streamMessages(groupThreads(events.value)))

function streamQuery(extra: Record<string, string> = {}): URLSearchParams {
  const params = new URLSearchParams({
    project: `${props.namespace}/${props.name}`,
    milestone: String(props.milestone),
    ...(props.issue ? { issue: String(props.issue) } : {}),
    ...extra,
    limit: '50',
  })
  // Chat rows come only when the kinds name them (as on the Stream tab).
  const kinds = props.kinds
    ? props.kinds.filter((k) => hasFeature('chat') || !chatStreamKinds.includes(k))
    : kindsForFilter('all', hasFeature('chat'))
  if (kinds) params.set('kinds', kinds.join(','))
  return params
}

function failed(e: unknown) {
  if (!(e instanceof LoginRequiredError)) error.value = e instanceof Error ? e.message : String(e)
}

async function loadInitial() {
  const mine = ++seq
  loading.value = true
  error.value = ''
  try {
    const page = await getStream(streamQuery())
    if (mine !== seq) return
    events.value = page.events
    older.value = page.older
    newest.value = page.newest
    historyOn.value = page.history
  } catch (e) {
    if (mine === seq) failed(e)
  } finally {
    if (mine === seq) loading.value = false
  }
}

async function loadOlder() {
  if (!older.value || loadingOlder.value) return
  const mine = seq
  loadingOlder.value = true
  try {
    const page = await getStream(streamQuery({ before: older.value }))
    if (mine !== seq) return
    events.value = [...events.value, ...page.events]
    older.value = page.older
  } catch (e) {
    if (mine === seq) failed(e)
  } finally {
    loadingOlder.value = false
  }
}

// loadLive adds what is newer than the newest event; an empty stream loads
// again.
async function loadLive() {
  if (!newest.value) {
    if (!loading.value) void loadInitial()
    return
  }
  const mine = seq
  try {
    const page = await getStream(streamQuery({ after: newest.value }))
    if (mine !== seq || !page.events.length) return
    const seen = new Set(events.value.map(streamEventKey))
    const fresh = page.events.filter((e) => !seen.has(streamEventKey(e)))
    if (fresh.length) events.value = [...fresh, ...events.value]
    if (page.newest) newest.value = page.newest
  } catch {
    /* live refresh is best-effort */
  }
}

watch(
  () => [props.namespace, props.name, props.milestone, props.issue, props.kinds?.join(',')] as const,
  () => {
    events.value = []
    older.value = newest.value = undefined
    void loadInitial()
  },
  { immediate: true },
)

watch(
  () => live.version,
  () => {
    if (!live.kinds || live.kinds.some((k) => k === 'project' || k === 'task')) void loadLive()
  },
)

</script>

<template>
  <div class="flex h-[min(72dvh,720px)] min-h-[320px] min-w-0 flex-col gap-2" data-issue-stream>
    <UAlert v-if="!historyOn" :color="alertColor('info')" variant="subtle" description="History needs the research database." />
    <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" :description="error" />
    <div v-if="loading && !events.length" class="empty">Loading…</div>
    <DeskChat
      v-else
      label="Events"
      :messages="messages"
      :older="!!older"
      :loading-older="loadingOlder"
      empty="No events yet."
      no-prompt
      @older="loadOlder"
    >
      <template #content="{ message }">
        <div v-if="message.metadata.day" class="day">{{ message.metadata.day }}</div>
        <EventCard v-if="message.metadata.notes" :notes="message.metadata.notes" :can-steer="canSteer" @posted="loadInitial" />
        <EventCard v-else-if="message.metadata.event" :event="message.metadata.event" :can-steer="canSteer" :prompts="prompts" @posted="loadInitial" />
      </template>
    </DeskChat>
  </div>
</template>

<style scoped>
.day {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 11.5px;
  font-weight: 600;
  color: var(--text-3);
  text-transform: uppercase;
  letter-spacing: 0.06em;
  margin: 14px 0 10px;
}
.day::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--border);
}
</style>
