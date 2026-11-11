<script setup lang="ts">
import { computed, watchEffect } from 'vue'
import { setCrumbNames } from '../crumbNames'
import { useResource } from '../live'
import { absTime, cost, runLine, type Tone } from '../format'
import Badge from '../components/Badge.vue'
import RelTime from '../components/RelTime.vue'
import ResearchResult from '../components/ResearchResult.vue'
import NotFound from './NotFound.vue'
import type { ResearchResponse, ResearchRun } from '../types'

// One stored run of a research: status, warnings, facts and its result.
// Utilities only (docs/ui.md, "Research page"): the facts wrap rather than
// clip, two to a row on phones.
const props = defineProps<{ namespace: string; name: string; id: string }>()
const { data, error, notFound, loading } = useResource<ResearchResponse>(
  () => `/researches/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}/runs/${encodeURIComponent(props.id)}`,
  ['research'],
)
const run = computed(() => data.value?.run ?? null)
watchEffect(() => {
  const r = run.value
  if (r?.id === props.id) {
    setCrumbNames({ run: { namespace: props.namespace, name: props.name, id: r.id, title: `Run of ${absTime(r.startedAt)}` } })
  }
})

const statusTone: Record<ResearchRun['status'], Tone> = {
  running: 'progress',
  succeeded: 'success',
  degraded: 'warning',
  failed: 'danger',
  limited: 'warning',
}

const duration = computed(() => {
  const r = run.value
  if (!r?.finishedAt) return '—'
  return runLine(undefined, undefined, Date.parse(r.finishedAt) - Date.parse(r.startedAt), undefined) || '—'
})
</script>

<template>
  <NotFound v-if="notFound" />
  <div v-else class="page">

    <div v-if="error" class="banner danger [overflow-wrap:anywhere]">{{ error }}</div>
    <div v-if="loading && !run" class="card empty">Loading…</div>

    <template v-if="run && data">
      <div class="flex flex-wrap items-center gap-2">
        <h1 class="[overflow-wrap:anywhere]">Run of {{ absTime(run.startedAt) }}</h1>
        <Badge :tone="statusTone[run.status] ?? 'muted'" :dot="run.status === 'running'" :pulse="run.status === 'running'">
          {{ run.status }}
        </Badge>
      </div>
      <div v-if="run.status === 'degraded'" class="banner warning">
        <strong>Degraded</strong>
        <span>The agent's answer failed validation; it is shown as plain text.</span>
      </div>
      <div v-if="run.error" class="banner danger [overflow-wrap:anywhere]">{{ run.error }}</div>
      <div v-if="run.warnings?.length" class="banner warning">
        <strong>Warnings</strong>
        <ul class="m-0 basis-full ps-[18px] [overflow-wrap:anywhere]">
          <li v-for="(w, i) in run.warnings" :key="i">{{ w }}</li>
        </ul>
      </div>

      <div class="facts section" data-run-facts>
        <div class="fact">
          <div class="label">Agent</div>
          <div class="mono font-[550] [overflow-wrap:anywhere]">
            {{ run.engine ? `${run.engine}/${run.model}` : '—' }}<template v-if="run.effort"> ({{ run.effort }})</template>
          </div>
        </div>
        <div class="fact">
          <div class="label">Started</div>
          <div class="font-[550] [overflow-wrap:anywhere]"><RelTime :time="run.startedAt" /></div>
        </div>
        <div class="fact">
          <div class="label">Duration</div>
          <div class="font-[550] [overflow-wrap:anywhere]">{{ duration }}</div>
        </div>
        <div class="fact">
          <div class="label">Model spend</div>
          <div class="font-[550] [overflow-wrap:anywhere]">{{ cost(run.costUSD) }}</div>
        </div>
        <div class="fact">
          <div class="label">Kept until</div>
          <div class="font-[550] [overflow-wrap:anywhere]"><RelTime :time="run.expiresAt" /></div>
        </div>
      </div>
      <p v-if="run.choiceReason" class="mt-2.5 mb-0 text-[13px] text-(--text-2) [overflow-wrap:anywhere]">{{ run.choiceReason }}</p>

      <ResearchResult v-if="data.result" :research="data.research" :run="null" :result="data.result" class="section" />
      <div v-else class="card empty section">
        {{ run.status === 'running' ? 'The run is in progress.' : 'This run stored no result.' }}
      </div>
    </template>
  </div>
</template>
