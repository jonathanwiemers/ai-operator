<script setup lang="ts">
import { canStartResearch } from '../api'
import { getResearches } from '../client'
import { useNow, useResource } from '../live'
import { clockTime, runLine, type Tone } from '../format'
import Badge from '../components/Badge.vue'
import RelTime from '../components/RelTime.vue'
import type { ResearchPhase, ResearchSummary } from '../types'

// The researches as cards, one per row at every width; the name's link covers
// the card. Utilities only (docs/ui.md, "Research list and form"): lines wrap
// on phones, where *New research* and the cards are 44 px high or more.
const { data: researches, error, loading } = useResource(() => getResearches, ['research'])
const now = useNow()

const phaseTone: Record<ResearchPhase, Tone> = {
  Scheduled: 'info',
  Running: 'progress',
  Waiting: 'warning',
  Succeeded: 'success',
  Failed: 'danger',
}

function lastRunLine(r: ResearchSummary): string {
  const run = r.run
  if (!run) return runLine(r.lastEngine, r.lastModel, undefined, undefined)
  const start = run.startedAt ? Date.parse(run.startedAt) : NaN
  const end = run.finishedAt ? Date.parse(run.finishedAt) : NaN
  return runLine(run.engine, run.model, Number.isNaN(start + end) ? undefined : end - start, run.startedAt)
}
</script>

<template>
  <div class="page">
    <div class="mb-[18px] flex flex-wrap items-center gap-3">
      <h1>Research</h1>
      <!-- a.btn is 44 px high on phones (style.css, the phone rule). -->
      <RouterLink v-if="canStartResearch()" to="/research/new" class="btn primary ms-auto">New research</RouterLink>
    </div>

    <div v-if="error" class="banner danger [overflow-wrap:anywhere]">{{ error }}</div>

    <div v-if="loading && !researches" class="card empty">Loading…</div>
    <div v-else-if="researches && researches.length === 0" class="card empty">
      <h2>No researches</h2>
      <p>There are no AgentResearches, or none your roles may read.</p>
    </div>

    <ul v-else class="m-0 flex list-none flex-col gap-3 p-0" data-researches>
      <li
        v-for="r in researches"
        :key="r.namespace + '/' + r.name"
        class="relative flex min-w-0 rounded-(--radius) border border-(--border) bg-(--surface) shadow-(--shadow) flex-col gap-2 px-4 py-3.5 transition-[box-shadow,border-color] duration-150 hover:border-(--border-strong) hover:shadow-(--shadow-hover) tablet:px-[18px]"
        data-research
      >
        <div class="flex flex-wrap items-center gap-x-2.5 gap-y-1.5">
          <div class="flex min-w-0 flex-wrap items-baseline gap-x-2.5">
            <RouterLink
              :to="`/research/${encodeURIComponent(r.namespace)}/${encodeURIComponent(r.name)}`"
              class="text-base font-[650] text-(--text) [overflow-wrap:anywhere] after:absolute after:inset-0 after:rounded-(--radius) hover:no-underline"
            >
              {{ r.name }}
            </RouterLink>
            <span class="text-xs text-(--text-3)">{{ r.namespace }}</span>
          </div>
          <div class="flex flex-wrap items-center gap-2 tablet:ms-auto">
            <Badge>{{ r.view === 'newsflash' ? 'News flash' : 'Report' }}</Badge>
            <Badge tone="muted">{{ r.depth }}</Badge>
            <Badge v-if="r.phase" :tone="phaseTone[r.phase]" :dot="r.phase === 'Running'" :pulse="r.phase === 'Running'">
              {{ r.phase }}
            </Badge>
          </div>
        </div>
        <div class="flex flex-wrap gap-x-[18px] gap-y-1.5 text-[13px] text-(--text-2)">
          <span v-if="r.schedule" class="[overflow-wrap:anywhere]">
            <code>{{ r.schedule }}</code> {{ r.timeZone || 'UTC' }}
          </span>
          <span v-else>one-off<template v-if="r.createdBy"> by {{ r.createdBy }}</template></span>
          <span>last run <RelTime :time="r.lastRun" /></span>
          <span v-if="r.nextRun">next run <RelTime :time="r.nextRun" /></span>
        </div>
        <div v-if="lastRunLine(r)" class="text-[13px] text-(--text-3) [overflow-wrap:anywhere]">
          {{ lastRunLine(r) }}
          <template v-if="r.run?.outcome && r.run.outcome !== 'succeeded'"> · {{ r.run.outcome }}</template>
          <span v-if="r.run?.reason" class="block">{{ r.run.reason }}</span>
        </div>
        <div v-if="r.waiting || r.skippedReason" class="flex flex-wrap items-center gap-2 text-[13px]">
          <Badge v-if="r.waiting" tone="warning">
            waiting for <code>{{ r.waiting.engine }}</code> until {{ clockTime(r.waiting.until, now) }}
          </Badge>
          <span v-if="r.skippedReason" class="text-(--text-2) [overflow-wrap:anywhere]">{{ r.skippedReason }}</span>
        </div>
      </li>
    </ul>
  </div>
</template>
