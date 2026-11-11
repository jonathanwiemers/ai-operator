<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { get, LoginRequiredError, NotFoundError } from '../api'
import { absTime, cost, runLine, type Tone } from '../format'
import { live } from '../live'
import Badge from '../components/Badge.vue'
import RelTime from '../components/RelTime.vue'
import NotFound from './NotFound.vue'
import type { ResearchRun, ResearchRunsPage } from '../types'

const props = defineProps<{ namespace: string; name: string }>()
const pageSize = 20
const base = computed(() => `/research/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}`)
const apiBase = computed(
  () => `/researches/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}/runs`,
)

const runs = ref<ResearchRun[]>([])
const error = ref<string | null>(null)
const notFound = ref(false)
const loading = ref(true)
const loadingMore = ref(false)
const hasMore = ref(false)
let seq = 0

const statusTone: Record<ResearchRun['status'], Tone> = {
  running: 'progress',
  succeeded: 'success',
  degraded: 'warning',
  failed: 'danger',
  limited: 'warning',
}

function agent(r: ResearchRun): string {
  if (!r.engine) return '—'
  return r.model ? `${r.engine}/${r.model}` : r.engine
}

function duration(r: ResearchRun): string {
  if (!r.finishedAt) return '—'
  return runLine(undefined, undefined, Date.parse(r.finishedAt) - Date.parse(r.startedAt), undefined) || '—'
}

function reason(r: ResearchRun): string {
  const t = r.choiceReason?.trim()
  if (!t) return '—'
  return t.length > 48 ? `${t.slice(0, 47)}…` : t
}

async function fetchPage(before: string | undefined, append: boolean): Promise<boolean> {
  const mine = ++seq
  const q = new URLSearchParams({ limit: String(pageSize) })
  if (before) q.set('before', before)
  try {
    const page = await get<ResearchRunsPage>(`${apiBase.value}?${q}`)
    if (mine !== seq) return false
    runs.value = append ? [...runs.value, ...page.runs] : page.runs
    hasMore.value = page.runs.length >= pageSize
    error.value = null
    notFound.value = false
  } catch (e) {
    if (mine !== seq || e instanceof LoginRequiredError) return false
    if (e instanceof NotFoundError) {
      notFound.value = true
      runs.value = []
    } else {
      error.value = e instanceof Error ? e.message : String(e)
    }
  }
  return mine === seq
}

async function load() {
  loading.value = true
  if (await fetchPage(undefined, false)) loading.value = false
}

async function loadOlder() {
  const last = runs.value[runs.value.length - 1]
  if (!last || loadingMore.value) return
  loadingMore.value = true
  if (await fetchPage(last.startedAt, true)) loadingMore.value = false
}

onMounted(load)
watch(() => [props.namespace, props.name], load)
watch(
  () => live.version,
  () => {
    if (!live.kinds || live.kinds.includes('research')) void load()
  },
)
</script>

<template>
  <NotFound v-if="notFound" />
  <div v-else class="page">

    <div class="row head">
      <h1>Run history</h1>
    </div>

    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="loading && !runs.length" class="card empty">Loading…</div>
    <div v-else-if="!loading && !runs.length && !error" class="card empty">
      <h2>No runs yet</h2>
      <p>This research has not stored a run.</p>
    </div>

    <div v-else-if="runs.length" class="card">
      <table class="list">
        <thead>
          <tr>
            <th>Started</th>
            <th>Duration</th>
            <th>Status</th>
            <th>Agent</th>
            <th>Choice</th>
            <th>Cost</th>
            <th>Error</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in runs" :key="r.id" class="run-row">
            <td>
              <RouterLink
                :to="`${base}/runs/${encodeURIComponent(r.id)}`"
                class="started"
                :title="absTime(r.startedAt)"
              >
                <RelTime :time="r.startedAt" />
              </RouterLink>
            </td>
            <td>{{ duration(r) }}</td>
            <td>
              <Badge :tone="statusTone[r.status] ?? 'muted'" :dot="r.status === 'running'" :pulse="r.status === 'running'">
                {{ r.status }}
              </Badge>
            </td>
            <td class="mono">{{ agent(r) }}</td>
            <td class="reason" :title="r.choiceReason || undefined">{{ reason(r) }}</td>
            <td>{{ cost(r.costUSD) }}</td>
            <td class="err" :title="r.error || undefined">{{ r.error || '—' }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="hasMore" class="more">
      <button type="button" class="btn" :disabled="loadingMore" @click="loadOlder">
        {{ loadingMore ? 'Loading…' : 'Load older' }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.head {
  margin-bottom: 14px;
}

.run-row {
  position: relative;
}

.started {
  color: var(--text);
  font-weight: 550;
}

.started:hover {
  text-decoration: none;
  color: var(--link);
}

/* The whole row opens the run. */
.started::after {
  content: '';
  position: absolute;
  inset: 0;
}

.reason,
.err {
  max-width: 196px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  position: relative;
  z-index: 1;
}

.more {
  margin-top: 14px;
  display: flex;
  justify-content: center;
}
</style>
