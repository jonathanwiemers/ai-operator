<script setup lang="ts">
import { computed } from 'vue'
import EngineUsage from './EngineUsage.vue'
import MilestoneSections from './MilestoneSections.vue'
import { clockTime, duration } from '../format'
import { useNow, useResource } from '../live'
import { activeLimits, overviewStrip } from '../overview'
import type { OverviewView, WorkingNow } from '../types'

// The dashboard's Overview tab (decision 4): the summary strip, Working
// now, and the milestone lists (In progress, Recently done). It reloads on
// project and task changes.
const now = useNow()
const { data: overview, error, loading } = useResource<OverviewView>(() => '/overview', ['project', 'task'])
const strip = computed(() => (overview.value ? overviewStrip(overview.value) : []))

function project(w: WorkingNow) {
  return { name: 'stream-project' as const, params: { namespace: w.namespace, name: w.project } }
}

// runningFor is how long the run has held its slot.
function runningFor(w: WorkingNow): string {
  const t = w.since ? Date.parse(w.since) : NaN
  return Number.isNaN(t) ? '' : duration(Math.max(0, now.value - t))
}
</script>

<template>
  <div class="overview">
    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="loading && !overview" class="empty">Loading…</div>
    <template v-else-if="overview">
      <ul class="strip" aria-label="Summary">
        <li v-for="c in strip" :key="c.id" :class="{ warn: c.warn }">
          <RouterLink v-if="c.inbox" to="/inbox">
            <b>{{ c.value }}</b> {{ c.label }}
          </RouterLink>
          <template v-else><b>{{ c.value }}</b> {{ c.label }}</template>
        </li>
      </ul>
      <div v-if="!overview.history" class="banner info">
        Done milestones and spend need the research database.
      </div>

      <section class="section">
        <h2>Working now</h2>
        <p v-if="!overview.working.length" class="muted">Nothing runs right now.</p>
        <div v-else class="cards">
          <article
            v-for="w in overview.working"
            :key="`${w.namespace}/${w.project}/${w.task ?? ''}`"
            class="card work"
          >
            <RouterLink class="proj" :to="project(w)">{{ w.project }}</RouterLink>
            <template v-if="w.issueIID">
              <RouterLink
                v-if="w.task"
                class="issue"
                :to="{ name: 'task', params: { namespace: w.namespace, name: w.project, task: w.task } }"
              >
                {{ w.title || `#${w.issueIID}` }}
              </RouterLink>
              <span v-else class="issue">{{ w.title || `#${w.issueIID}` }}</span>
              <span v-if="runningFor(w)" class="muted">running for {{ runningFor(w) }}</span>
            </template>
            <span v-else class="muted">No run holds the slot.</span>
            <span v-if="w.next" class="next">
              Next:
              <RouterLink
                v-if="w.next.milestoneIID"
                :to="{
                  name: 'stream-milestone',
                  params: { namespace: w.namespace, name: w.project, iid: String(w.next.milestoneIID) },
                }"
              >
                #{{ w.next.issueIID }} {{ w.next.title }}
              </RouterLink>
              <template v-else>#{{ w.next.issueIID }} {{ w.next.title }}</template>
            </span>
            <span v-for="l in activeLimits(w.usageLimits, now)" :key="l.engine" class="limit">
              {{ l.engine }} limited until {{ clockTime(l.until, now) }}
            </span>
            <EngineUsage v-for="u in w.engineUsage ?? []" :key="u.engine" :usage="u" />
          </article>
        </div>
      </section>

      <MilestoneSections />
    </template>
  </div>
</template>

<style scoped>
.overview {
  overflow-y: auto;
  padding: 16px 28px 40px;
}
.strip {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  list-style: none;
  margin: 0;
  padding: 0;
}
.strip li {
  padding: 6px 12px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--surface);
  color: var(--text-2);
  font-size: 13px;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.strip li b {
  color: var(--text);
}
.strip li.warn,
.strip li.warn b {
  color: var(--tone-warning-fg);
  border-color: var(--tone-warning-fg);
}
.strip a:hover {
  text-decoration: none;
}
.section h2 {
  font-size: 15px;
}
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 12px;
}
.work {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 12px 14px;
  min-width: 0;
}
.proj {
  font-size: 12px;
  font-weight: 600;
  color: var(--text-3);
}
.issue {
  font-weight: 600;
  overflow-wrap: anywhere;
}
.next {
  font-size: 13px;
  color: var(--text-2);
  overflow-wrap: anywhere;
}
.limit {
  font-size: 13px;
  color: var(--tone-warning-fg);
}
.empty {
  padding: 32px;
  text-align: center;
  color: var(--text-3);
}
@media (max-width: 759.98px) {
  .overview {
    padding-left: 16px;
    padding-right: 16px;
  }
  .cards {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
