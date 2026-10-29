<script setup lang="ts">
import { canStartResearch } from '../api'
import { useNow, useResource } from '../live'
import { clockTime, runLine, type Tone } from '../format'
import Badge from '../components/Badge.vue'
import RelTime from '../components/RelTime.vue'
import type { ResearchPhase, ResearchSummary } from '../types'

const { data: researches, error, loading } = useResource<ResearchSummary[]>(() => '/researches', ['research'])
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
    <div class="row head">
      <h1>Research</h1>
      <span class="spacer" />
      <RouterLink v-if="canStartResearch()" to="/research/new" class="btn primary">New research</RouterLink>
    </div>

    <div v-if="error" class="banner danger">{{ error }}</div>

    <div v-if="loading && !researches" class="card empty">Loading…</div>
    <div v-else-if="researches && researches.length === 0" class="card empty">
      <h2>No researches</h2>
      <p>There are no AgentResearches, or none your roles may read.</p>
    </div>

    <div v-else class="list">
      <div v-for="r in researches" :key="r.namespace + '/' + r.name" class="research card">
        <div class="row">
          <div class="names">
            <RouterLink :to="`/research/${encodeURIComponent(r.namespace)}/${encodeURIComponent(r.name)}`" class="name">
              {{ r.name }}
            </RouterLink>
            <span class="ns faint">{{ r.namespace }}</span>
          </div>
          <span class="spacer" />
          <Badge>{{ r.view === 'newsflash' ? 'News flash' : 'Report' }}</Badge>
          <Badge tone="muted">{{ r.depth }}</Badge>
          <Badge v-if="r.phase" :tone="phaseTone[r.phase]" :dot="r.phase === 'Running'" :pulse="r.phase === 'Running'">
            {{ r.phase }}
          </Badge>
        </div>
        <div class="facts-line muted">
          <span v-if="r.schedule">
            <code>{{ r.schedule }}</code> {{ r.timeZone || 'UTC' }}
          </span>
          <span v-else>one-off<template v-if="r.createdBy"> by {{ r.createdBy }}</template></span>
          <span>last run <RelTime :time="r.lastRun" /></span>
          <span v-if="r.nextRun">next run <RelTime :time="r.nextRun" /></span>
        </div>
        <div v-if="lastRunLine(r)" class="run faint" :title="r.run?.reason">
          {{ lastRunLine(r) }}
          <template v-if="r.run?.outcome && r.run.outcome !== 'succeeded'"> · {{ r.run.outcome }}</template>
        </div>
        <div v-if="r.waiting || r.skippedReason" class="waiting">
          <Badge v-if="r.waiting" tone="warning">
            waiting for <code>{{ r.waiting.engine }}</code> until {{ clockTime(r.waiting.until, now) }}
          </Badge>
          <span v-if="r.skippedReason" class="muted">{{ r.skippedReason }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.head {
  margin-bottom: 18px;
}

.list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.research {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 14px 18px;
  transition:
    box-shadow 0.15s,
    border-color 0.15s;
}

.research:hover {
  border-color: var(--border-strong);
  box-shadow: var(--shadow-hover);
}

.names {
  display: flex;
  align-items: baseline;
  gap: 10px;
  min-width: 0;
}

.name {
  font-size: 16px;
  font-weight: 650;
  color: var(--text);
}

.name:hover {
  text-decoration: none;
}

/* The whole card opens the research. */
.name::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: var(--radius);
}

.research :deep([title]) {
  position: relative;
  z-index: 1;
}

.ns {
  font-size: 12px;
}

.facts-line {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 18px;
  font-size: 13px;
}

.run {
  font-size: 13px;
}

.waiting {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  font-size: 13px;
}
</style>
