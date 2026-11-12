<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import { computed, onMounted, ref, watch } from 'vue'
import { getResearchRuns, LoginRequiredError, NotFoundError } from '../client'
import { absTime, cost, runLine, type Tone } from '../format'
import { live } from '../live'
import Badge from '../components/Badge.vue'
import DataList, { type DataListColumn } from '../ui/DataList.vue'
import RelTime from '../components/RelTime.vue'
import Truncated from '../ui/Truncated.vue'
import NotFound from './NotFound.vue'
import type { ResearchRun } from '../types'

const props = defineProps<{ namespace: string; name: string }>()
const pageSize = 20
const base = computed(() => `/research/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}`)

const runs = ref<ResearchRun[]>([])
const error = ref<string | null>(null)
const notFound = ref(false)
const loading = ref(true)
const loadingMore = ref(false)
const hasMore = ref(false)
let seq = 0

// Long text wraps; it sits above the row's link overlay, so its title shows.
const long = 'relative z-1 tablet:min-w-0 tablet:max-w-48 tablet:break-words'
const columns: DataListColumn[] = [
  { key: 'startedAt', label: 'Started' },
  { key: 'duration', label: 'Duration' },
  { key: 'status', label: 'Status' },
  { key: 'agent', label: 'Agent' },
  { key: 'choiceReason', label: 'Choice', class: long },
  { key: 'costUSD', label: 'Cost' },
  { key: 'error', label: 'Error', class: long },
]

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


async function fetchPage(before: string | undefined, append: boolean): Promise<boolean> {
  const mine = ++seq
  try {
    const page = await getResearchRuns(props.namespace, props.name, { before, limit: pageSize })
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

    <h1 class="mb-3.5">Run history</h1>

    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="loading && !runs.length" class="card empty">Loading…</div>
    <div v-else-if="!loading && !runs.length && !error" class="card empty">
      <h2>No runs yet</h2>
      <p>This research has not stored a run.</p>
    </div>

    <div v-else-if="runs.length" class="card">
      <DataList :columns="columns" :rows="runs" :row-key="(r) => r.id">
        <template #cell-startedAt="{ row: r }">
          <RouterLink
            :to="`${base}/runs/${encodeURIComponent(r.id)}`"
            class="font-semibold text-(--text) after:absolute after:inset-0 hover:text-(--link) hover:no-underline"
            :title="absTime(r.startedAt)"
          >
            <RelTime :time="r.startedAt" />
          </RouterLink>
        </template>
        <template #cell-duration="{ row: r }">{{ duration(r) }}</template>
        <template #cell-status="{ row: r }">
          <Badge :tone="statusTone[r.status] ?? 'muted'" :dot="r.status === 'running'" :pulse="r.status === 'running'">
            {{ r.status }}
          </Badge>
        </template>
        <template #cell-agent="{ row: r }"><span class="font-mono">{{ agent(r) }}</span></template>
        <!-- The full reason opens on tap; relative z-1 lifts it above the row's link cover. -->
        <template #cell-choiceReason="{ row: r }">
          <Truncated :lines="2" class="relative z-1 max-w-[48ch]" :text="r.choiceReason?.trim() || '—'" />
        </template>
        <template #cell-costUSD="{ row: r }">{{ cost(r.costUSD) }}</template>
        <template #cell-error="{ row: r }"><span :title="r.error || undefined">{{ r.error || '—' }}</span></template>
      </DataList>
    </div>

    <div v-if="hasMore" class="mt-3.5 flex justify-center">
      <UButton
        color="neutral"
        variant="outline"
        :disabled="loadingMore"
        :label="loadingMore ? 'Loading…' : 'Load older'"
        @click="loadOlder"
      />
    </div>
  </div>
</template>
