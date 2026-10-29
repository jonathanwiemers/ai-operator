<script setup lang="ts">
import { computed } from 'vue'
import { useResource } from '../live'
import { absTime, cost, runLine, type Tone } from '../format'
import Badge from '../components/Badge.vue'
import RelTime from '../components/RelTime.vue'
import ResearchResult from '../components/ResearchResult.vue'
import NotFound from './NotFound.vue'
import type { ResearchResponse, ResearchRun } from '../types'

const props = defineProps<{ namespace: string; name: string; id: string }>()
const base = computed(() => `/research/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}`)
const { data, error, notFound, loading } = useResource<ResearchResponse>(
  () => `/researches/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}/runs/${encodeURIComponent(props.id)}`,
  ['research'],
)
const run = computed(() => data.value?.run ?? null)

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
    <nav class="crumbs muted">
      <RouterLink to="/research">Research</RouterLink>
      <span>/</span>
      <RouterLink :to="base">{{ name }}</RouterLink>
      <span>/</span>
      <span class="mono">{{ id.slice(0, 8) }}</span>
    </nav>

    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="loading && !run" class="card empty">Loading…</div>

    <template v-if="run && data">
      <div class="row">
        <h1>Run of {{ absTime(run.startedAt) }}</h1>
        <Badge :tone="statusTone[run.status] ?? 'muted'" :dot="run.status === 'running'" :pulse="run.status === 'running'">
          {{ run.status }}
        </Badge>
      </div>
      <div v-if="run.status === 'degraded'" class="banner warning">
        <strong>Degraded</strong>
        <span>The agent's answer failed validation; it is shown as plain text.</span>
      </div>
      <div v-if="run.error" class="banner danger">{{ run.error }}</div>
      <div v-if="run.warnings?.length" class="banner warning">
        <strong>Warnings</strong>
        <ul class="warnings">
          <li v-for="(w, i) in run.warnings" :key="i">{{ w }}</li>
        </ul>
      </div>

      <div class="facts section">
        <div class="fact">
          <div class="label">Agent</div>
          <div class="value mono" :title="run.engine ? `${run.engine}/${run.model}` : undefined">
            {{ run.engine ? `${run.engine}/${run.model}` : '—' }}<template v-if="run.effort"> ({{ run.effort }})</template>
          </div>
        </div>
        <div class="fact">
          <div class="label">Started</div>
          <div class="value"><RelTime :time="run.startedAt" /></div>
        </div>
        <div class="fact">
          <div class="label">Duration</div>
          <div class="value">{{ duration }}</div>
        </div>
        <div class="fact">
          <div class="label">Model spend</div>
          <div class="value">{{ cost(run.costUSD) }}</div>
        </div>
        <div class="fact">
          <div class="label">Kept until</div>
          <div class="value" :title="absTime(run.expiresAt)"><RelTime :time="run.expiresAt" /></div>
        </div>
      </div>
      <p v-if="run.choiceReason" class="reason muted">{{ run.choiceReason }}</p>

      <ResearchResult v-if="data.result" :research="data.research" :run="null" :result="data.result" class="section" />
      <div v-else class="card empty section">
        {{ run.status === 'running' ? 'The run is in progress.' : 'This run stored no result.' }}
      </div>
    </template>
  </div>
</template>

<style scoped>
.crumbs {
  display: flex;
  gap: 8px;
  font-size: 13px;
  margin-bottom: 14px;
}

.warnings {
  margin: 0;
  padding-left: 18px;
  flex-basis: 100%;
}

.reason {
  margin: 10px 0 0;
  font-size: 13px;
}
</style>
