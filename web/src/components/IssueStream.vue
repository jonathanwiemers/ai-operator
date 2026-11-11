<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { hasFeature, LoginRequiredError } from '../api'
import { getStream } from '../client'
import { live } from '../live'
import { chatStreamKinds, dayLabel, groupThreads, kindsForFilter, sameLocalDay, streamEventKey } from '../stream'
import EventCard from './stream/EventCard.vue'
import type { StreamEvent, StreamKind } from '../types'

// An issue's stream (R10): GET /api/v1/stream with project, milestone and
// issue, newest first, *Load older* pages back, live events come in on
// top. It reads history, so it outlives the issue's tasks. Without an issue
// it is the milestone's stream, and kinds fixes its kinds (the Pilot tab,
// R11); chat kinds are asked for only with the chat feature.
const props = defineProps<{
  namespace: string
  name: string
  milestone: number
  issue?: number
  kinds?: readonly StreamKind[]
  canSteer?: boolean
}>()

const events = ref<StreamEvent[]>([])
const older = ref<string | undefined>()
const newest = ref<string | undefined>()
const historyOn = ref(true)
const loading = ref(true)
const loadingOlder = ref(false)
const error = ref('')
let seq = 0

const items = computed(() => groupThreads(events.value))

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

// showDay is the day label above item i when it starts a new day.
function showDay(i: number): string | null {
  const at = (n: number) => {
    const it = items.value[n]!
    return it.kind === 'thread' ? it.at : it.event.at
  }
  if (i === 0) return dayLabel(at(0))
  return sameLocalDay(at(i), at(i - 1)) ? null : dayLabel(at(i))
}
</script>

<template>
  <div class="istream">
    <div v-if="!historyOn" class="banner info">History needs the research database.</div>
    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="loading && !events.length" class="empty">Loading…</div>
    <div v-else-if="!events.length" class="empty">No events yet.</div>

    <template v-for="(item, i) in items" :key="item.kind === 'thread' ? item.key : streamEventKey(item.event)">
      <div v-if="showDay(i)" class="day">{{ showDay(i) }}</div>
      <EventCard v-if="item.kind === 'thread'" :notes="item.notes" :can-steer="canSteer" @posted="loadInitial" />
      <EventCard v-else :event="item.event" :can-steer="canSteer" @posted="loadInitial" />
    </template>

    <div v-if="older" class="older">
      <button type="button" class="btn" :disabled="loadingOlder" @click="loadOlder">
        {{ loadingOlder ? 'Loading…' : 'Load older' }}
      </button>
    </div>
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
  margin: 22px 0 10px;
}
.day:first-child {
  margin-top: 0;
}
.day::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--border);
}
.older {
  display: flex;
  justify-content: center;
  margin-top: 18px;
}
.btn {
  border: 1px solid var(--border-strong);
  background: var(--surface);
  padding: 6px 14px;
  border-radius: 7px;
  font: inherit;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
}
.empty {
  padding: 32px;
  text-align: center;
  color: var(--text-3);
}
</style>
