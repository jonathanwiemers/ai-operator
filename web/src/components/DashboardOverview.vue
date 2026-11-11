<script setup lang="ts">
import { computed } from 'vue'
import EngineUsage from './EngineUsage.vue'
import MilestoneSections from './MilestoneSections.vue'
import { clockTime, duration } from '../format'
import { useNow, useResource } from '../live'
import { activeLimits, overviewStrip, type StripChip } from '../overview'
import type { OverviewView, WorkingNow } from '../types'

// The dashboard's Overview tab (decision 4): the summary strip, Working
// now, and the milestone lists (In progress, Recently done). It reloads on
// project and task changes.
const now = useNow()
const { data: overview, error, loading } = useResource<OverviewView>(() => '/overview', ['project', 'task'])
const strip = computed(() => (overview.value ? overviewStrip(overview.value) : []))

// chip styles one chip of the summary strip. The Inbox chip is a link: 44 px
// high on phones.
function chip(c: StripChip): string[] {
  return [
    'inline-flex items-center gap-1 rounded-full border bg-(--surface) px-3 py-1.5 text-[13px] whitespace-nowrap tabular-nums',
    c.warn ? 'border-(--tone-warning-fg) text-(--tone-warning-fg)' : 'border-(--border) text-(--text-2)',
    c.inbox ? 'min-h-11 hover:no-underline tablet:min-h-0' : '',
  ]
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
    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="loading && !overview" class="empty">Loading…</div>
    <template v-else-if="overview">
      <ul class="m-0 flex list-none flex-wrap gap-2 p-0" aria-label="Summary">
        <li v-for="c in strip" :key="c.id" class="flex">
          <RouterLink v-if="c.inbox" to="/inbox" :class="chip(c)">
            <b :class="{ 'text-(--text)': !c.warn }">{{ c.value }}</b> {{ c.label }}
          </RouterLink>
          <span v-else :class="chip(c)">
            <b :class="{ 'text-(--text)': !c.warn }">{{ c.value }}</b> {{ c.label }}
          </span>
        </li>
      </ul>
      <div v-if="!overview.history" class="banner info">
        Done milestones and spend need the research database.
      </div>

      <section class="section">
        <h2 class="text-[15px]">Working now</h2>
        <p v-if="!overview.working.length" class="muted">Nothing runs right now.</p>
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
            <RouterLink class="text-xs font-semibold text-(--text-3)" :to="project(w)">{{ w.project }}</RouterLink>
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

      <MilestoneSections />
    </template>
  </div>
</template>
