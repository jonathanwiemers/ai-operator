<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import { computed } from 'vue'
import { alertColor } from '../badgeTone'
import { clockTime, duration } from '../format'
import { useNow } from '../live'
import { activeLimits, finishingLabel, overviewStrip, type StripChip } from '../overview'
import type { OverviewView, WorkingNow } from '../types'
import EngineUsage from './EngineUsage.vue'
import StatTile from './StatTile.vue'

// OverviewPanel is the Overview Cockpit's context panel (R16 of "Desk: the
// Cockpit"): the summary tiles (the milestone counts, the needs-you count
// and the spend of today and the last 7 days), then Working now, one card
// per running issue or busy project with its engines' usage meters and
// limits. ContextLayout shows it as a column from 1180 px, else as a sheet.
const props = defineProps<{ overview: OverviewView | null; error?: string; loading?: boolean; sheet?: boolean }>()

const now = useNow()
const strip = computed(() => (props.overview ? overviewStrip(props.overview) : []))

// chipLink is where a tile leads: the Inbox, or the Metrics page's last 7
// days for the spend tiles.
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
  <aside
    class="grid min-h-0 min-w-0 grid-cols-[minmax(0,1fr)] content-start gap-4 bg-(--surface) p-4"
    :class="!sheet && 'overflow-y-auto border-l border-(--border)'"
    aria-label="Working now"
    data-overview-panel
  >
    <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" :description="error" />
    <div v-if="loading && !overview" class="empty">Loading…</div>
    <template v-else-if="overview">
      <ul class="m-0 grid list-none grid-cols-2 gap-2 p-0" aria-label="Summary" data-overview-tiles>
        <li v-for="c in strip" :key="c.id" class="flex min-w-0 *:flex-1">
          <StatTile :label="c.label" :value="c.value" :warn="c.warn" :to="chipLink(c)" :data-chip-costs="c.costs || undefined" />
        </li>
      </ul>
      <section class="grid min-w-0 gap-3" aria-labelledby="overview-working-head" data-overview-working>
        <h2 id="overview-working-head" class="m-0 text-[15px]">Working now</h2>
        <UEmpty v-if="!overview.working.length" icon="i-lucide-circle-dot" title="Nothing runs right now." size="sm" />
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
          <span v-for="l in activeLimits(w.usageLimits, now)" :key="l.engine" class="text-[13px] text-(--tone-warning-fg)">
            {{ l.engine }} limited until {{ clockTime(l.until, now) }}
          </span>
          <EngineUsage v-for="u in w.engineUsage ?? []" :key="u.engine" :usage="u" />
        </article>
      </section>
    </template>
  </aside>
</template>
