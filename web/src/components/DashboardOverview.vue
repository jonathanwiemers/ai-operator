<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import { alertColor } from '../badgeTone'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import { computed, defineAsyncComponent } from 'vue'
import EngineUsage from './EngineUsage.vue'
import MilestoneSections from './MilestoneSections.vue'
import StatTile from './StatTile.vue'
import { clockTime, duration } from '../format'
import { getOverview } from '../client'
import { useNow, useResource } from '../live'
import { activeLimits, finishingLabel, overviewStrip, type StripChip } from '../overview'
import type { WorkingNow } from '../types'

// The dashboard's Overview tab (decision 4): the summary strip, Working
// now, the Timeline across projects (R11), and the milestone lists (In progress, Recently done). It reloads on
// project and task changes.
const now = useNow()
// The Timeline loads in its own chunk, out of the initial bundle (D667-P1).
const OverviewTimeline = defineAsyncComponent(() => import('./OverviewTimeline.vue'))
const { data: overview, error, loading } = useResource(() => getOverview, ['project', 'task'])
const strip = computed(() => (overview.value ? overviewStrip(overview.value) : []))

// chipLink is where a strip tile leads: the Inbox, or the Metrics page's
// last 7 days for the spend tiles.
function chipLink(c: StripChip) {
  if (c.inbox) return '/inbox'
  if (c.costs) return { path: '/metrics', query: { range: '7d' } }
  return undefined
}

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
  <div class="overflow-y-auto px-4 pt-4 pb-10 tablet:px-7">
    <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" class="mt-3" :description="error" />
    <div v-if="loading && !overview" class="empty">Loading…</div>
    <template v-else-if="overview">
      <ul class="stat-tiles" aria-label="Summary">
        <li v-for="c in strip" :key="c.id" class="flex min-w-0 *:flex-1">
          <StatTile :label="c.label" :value="c.value" :warn="c.warn" :to="chipLink(c)" :data-chip-costs="c.costs || undefined" />
        </li>
      </ul>
      <UAlert v-if="!overview.history" :color="alertColor('info')" variant="subtle" class="mt-3" description="Done milestones and spend need the research database." />

      <section class="section">
        <h2 class="text-[15px]">Working now</h2>
        <UEmpty v-if="!overview.working.length" icon="i-lucide-circle-dot" title="Nothing runs right now." size="sm" />
        <!-- One column on phones, two on tablets, as many columns of at
             least 300 px as fit on desktop. -->
        <div
          v-else
          class="grid grid-cols-[minmax(0,1fr)] gap-3 tablet:grid-cols-2 desktop:grid-cols-[repeat(auto-fill,minmax(300px,1fr))]"
        >
          <article
            v-for="w in overview.working"
            :key="`${w.namespace}/${w.project}/${w.task ?? ''}`"
            class="card flex min-w-0 flex-col gap-1.5 px-3.5 py-3"
          >
            <div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
              <RouterLink class="text-xs font-semibold text-(--text-3)" :to="project(w)">{{ w.project }}</RouterLink>
              <RouterLink
                v-if="finishingLabel(w.finishing)"
                :to="project(w)"
                class="inline-flex h-[22px] shrink-0 items-center rounded-full bg-(--tone-muted-bg) px-2 text-xs font-semibold whitespace-nowrap text-(--tone-muted-fg) hover:no-underline"
                data-overview-finishing
              >
                {{ finishingLabel(w.finishing) }}
              </RouterLink>
            </div>
            <template v-if="w.issueIID">
              <RouterLink
                v-if="w.task"
                class="font-semibold wrap-anywhere"
                :to="{ name: 'task', params: { namespace: w.namespace, name: w.project, task: w.task } }"
              >
                {{ w.title || `#${w.issueIID}` }}
              </RouterLink>
              <span v-else class="font-semibold wrap-anywhere">{{ w.title || `#${w.issueIID}` }}</span>
              <span v-if="runningFor(w)" class="muted">running for {{ runningFor(w) }}</span>
            </template>
            <span v-else class="muted">No run holds the slot.</span>
            <span v-if="w.next" class="text-[13px] wrap-anywhere text-(--text-2)">
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
            <span
              v-for="l in activeLimits(w.usageLimits, now)"
              :key="l.engine"
              class="text-[13px] text-(--tone-warning-fg)"
            >
              {{ l.engine }} limited until {{ clockTime(l.until, now) }}
            </span>
            <EngineUsage v-for="u in w.engineUsage ?? []" :key="u.engine" :usage="u" />
          </article>
        </div>
      </section>

      <section class="section" aria-labelledby="overview-timeline-head">
        <h2 id="overview-timeline-head" class="text-[15px]">Timeline</h2>
        <OverviewTimeline v-if="overview.history" />
        <UAlert v-else :color="alertColor('info')" variant="subtle" description="Done milestones and spend need the research database." data-timeline-no-history />
      </section>

      <MilestoneSections />
    </template>
  </div>
</template>
