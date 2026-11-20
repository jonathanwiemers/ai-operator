<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import { alertColor } from '../badgeTone'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import { computed, watchEffect } from 'vue'
import { setCrumbNames } from '../crumbNames'
import { getResearchRun } from '../client'
import { useResource } from '../live'
import { absTime, cost, runLine, type Tone } from '../format'
import UPageHeader from '@nuxt/ui/components/PageHeader.vue'
import RelTime from '../components/RelTime.vue'
import { toneText } from '../badgeTone'
import { pageHeaderUi } from '../pageHeader'
import ResearchResult from '../components/ResearchResult.vue'
import StatTile from '../components/StatTile.vue'
import NotFound from './NotFound.vue'
import type { ResearchRun } from '../types'

// One stored run of a research: status, warnings, facts and its result.
// Utilities only (docs/contributing/web-ui.md, "Research page"): the facts wrap rather than
// clip, two to a row on phones.
const props = defineProps<{ namespace: string; name: string; id: string }>()
// The props are read outside the closure, so another run loads again.
const { data, error, notFound, loading } = useResource(() => {
  const { namespace, name, id } = props
  return () => getResearchRun(namespace, name, id)
}, ['research'])
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

    <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" class="mt-3 [overflow-wrap:anywhere]" :description="error" />
    <div v-if="loading && !run" class="card empty">Loading…</div>

    <template v-if="run && data">
      <UPageHeader :title="`Run of ${absTime(run.startedAt)}`" :ui="pageHeaderUi">
        <template #headline>
          <span class="capitalize" :class="toneText[statusTone[run.status] ?? 'muted']" data-run-status>{{ run.status }}</span>
        </template>
      </UPageHeader>
      <UAlert v-if="run.status === 'degraded'" :color="alertColor('warning')" variant="subtle" class="mt-3" title="Degraded" description="The agent's answer failed validation; it is shown as plain text." />
      <UAlert v-if="run.error" :color="alertColor('danger')" variant="subtle" class="mt-3 [overflow-wrap:anywhere]" :description="run.error" />
      <UAlert v-if="run.warnings?.length" :color="alertColor('warning')" variant="subtle" class="mt-3" title="Warnings">
        <template #description>
          <ul class="m-0 ps-[18px] [overflow-wrap:anywhere]">
            <li v-for="(w, i) in run.warnings" :key="i">{{ w }}</li>
          </ul>
        </template>
      </UAlert>

      <div class="stat-tiles section" data-run-facts>
        <StatTile label="Agent">
          <span class="mono text-base">{{ run.engine ? `${run.engine}/${run.model}` : '—' }}<template v-if="run.effort"> ({{ run.effort }})</template></span>
        </StatTile>
        <StatTile label="Started"><RelTime :time="run.startedAt" /></StatTile>
        <StatTile label="Duration" :value="duration" />
        <StatTile label="Model spend" :value="cost(run.costUSD)" />
        <StatTile label="Kept until"><RelTime :time="run.expiresAt" /></StatTile>
      </div>
      <p v-if="run.choiceReason" class="mt-2.5 mb-0 text-[13px] text-(--text-2) [overflow-wrap:anywhere]">{{ run.choiceReason }}</p>

      <ResearchResult v-if="data.result" :research="data.research" :run="null" :result="data.result" class="section" />
      <UEmpty
        v-else
        icon="i-lucide-file-text"
        :title="run.status === 'running' ? 'The run is in progress.' : 'This run stored no result.'"
        class="section"
      />
    </template>
  </div>
</template>
