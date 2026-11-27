<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import { computed } from 'vue'
import { alertColor } from '../badgeTone'
import { clockTime } from '../format'
import { useNow } from '../live'
import { overviewNowAndNext, type NowGroup, type NowRow, type OverviewProjectNow } from '../nowAndNext'
import { activeLimits, finishingLabel, overviewStrip, type StripChip } from '../overview'
import type { OverviewView, WorkingNow } from '../types'
import EngineUsage from './EngineUsage.vue'
import NowNext from './NowNext.vue'
import StatTile from './StatTile.vue'

// OverviewPanel is the Overview Cockpit's sidebar (R10 of "Desk: the Cockpit
// newest first by issue"): Now & next per project (overviewNowAndNext, one
// NowNext under each project's name with its "N finishing"), then the
// summary tiles (the milestone counts, the needs-you count and the spend of
// today and the last 7 days), then each project's engine limits and usage
// meters. A Running or Waiting row opens the issue's thread in its
// milestone's Cockpit (#issue-<iid>), else its task, else the project.
// ContextLayout shows it as a column from 1180 px (collapsible to a rail),
// else as a slide-over.
const props = defineProps<{ overview: OverviewView | null; error?: string; loading?: boolean; sheet?: boolean }>()
const emit = defineEmits<{ follow: [] }>()

const now = useNow()
const strip = computed(() => (props.overview ? overviewStrip(props.overview) : []))
const nn = computed(() => overviewNowAndNext(props.overview?.working, now.value))

// engines: the projects' engine limits and usage, once per project (each
// of its entries repeats them), those with any.
const engines = computed(() => {
  const seen = new Map<string, WorkingNow>()
  for (const w of props.overview?.working ?? []) {
    const key = `${w.namespace}/${w.project}`
    if (!seen.has(key) && (w.engineUsage?.length || activeLimits(w.usageLimits, now.value).length)) seen.set(key, w)
  }
  return [...seen.entries()].map(([key, w]) => ({ key, w, limits: activeLimits(w.usageLimits, now.value) }))
})

// chipLink is where a tile leads: the Inbox, or the Metrics page's last 7
// days for the spend tiles.
function chipLink(c: StripChip) {
  if (c.inbox) return '/inbox'
  if (c.costs) return { path: '/metrics', query: { range: '7d' } }
  return undefined
}

function project(p: Pick<OverviewProjectNow, 'namespace' | 'project'>) {
  return { name: 'stream-project' as const, params: { namespace: p.namespace, name: p.project } }
}
function milestoneTo(p: OverviewProjectNow) {
  return (iid: number) => ({ name: 'stream-milestone' as const, params: { ...project(p).params, iid: String(iid) } })
}
function rowTo(p: OverviewProjectNow) {
  return (row: NowRow, _running: boolean, group: NowGroup) => {
    if (group.key !== '0') return { ...milestoneTo(p)(Number(group.key)), query: { tab: 'cockpit' }, hash: `#issue-${row.iid}` }
    if (row.task) return { name: 'task' as const, params: { ...project(p).params, task: row.task } }
    return project(p)
  }
}
</script>

<template>
  <aside
    class="grid min-h-0 min-w-0 grid-cols-[minmax(0,1fr)] content-start gap-4 bg-(--surface) p-4"
    :class="!sheet && 'overflow-y-auto border-l border-(--border)'"
    aria-label="Now & next"
    data-overview-panel
  >
    <h2 v-if="!sheet" class="m-0 text-[15px]">Now &amp; next</h2>
    <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" :description="error" />
    <div v-if="loading && !overview" class="empty">Loading…</div>
    <template v-else-if="overview">
      <UEmpty v-if="!nn.projects.length" icon="i-lucide-inbox" title="Nothing running" variant="naked" size="xs" data-now-empty />
      <section
        v-for="p in nn.projects"
        :key="p.key"
        class="card grid min-w-0 gap-2 px-3.5 py-3"
        :aria-labelledby="`now-project-${p.key}`"
        :data-overview-project="p.key"
      >
        <h3 :id="`now-project-${p.key}`" class="m-0 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[13.5px]">
          <RouterLink :to="project(p)" class="wrap-anywhere" @click="emit('follow')">{{ p.project }}</RouterLink>
          <RouterLink
            v-if="finishingLabel(p.finishing)"
            :to="project(p)"
            class="inline-flex h-[22px] shrink-0 items-center rounded-full bg-(--tone-muted-bg) px-2 text-xs font-semibold whitespace-nowrap text-(--tone-muted-fg) hover:no-underline"
            data-overview-finishing
            @click="emit('follow')"
          >
            {{ finishingLabel(p.finishing) }}
          </RouterLink>
        </h3>
        <NowNext
          :id-prefix="`${p.key}-`"
          :running="p.running"
          :waiting="p.waiting"
          :next="p.next"
          :more-next="0"
          :milestones="p.milestones"
          :row-to="rowTo(p)"
          :milestone-to="milestoneTo(p)"
          @follow="emit('follow')"
        />
      </section>
      <ul class="m-0 grid list-none grid-cols-2 gap-2 p-0" aria-label="Summary" data-overview-tiles>
        <li v-for="c in strip" :key="c.id" class="flex min-w-0 *:flex-1">
          <StatTile :label="c.label" :value="c.value" :warn="c.warn" :to="chipLink(c)" :data-chip-costs="c.costs || undefined" />
        </li>
      </ul>
      <section v-if="engines.length" class="grid min-w-0 gap-3" aria-labelledby="overview-engines-head" data-overview-engines>
        <h2 id="overview-engines-head" class="m-0 text-[15px]">Engine usage</h2>
        <article v-for="e in engines" :key="e.key" class="card flex min-w-0 flex-col gap-1.5 px-3.5 py-3" :data-overview-engine="e.key">
          <RouterLink class="text-xs font-semibold text-(--text-3)" :to="project(e.w)">{{ e.w.project }}</RouterLink>
          <span v-for="l in e.limits" :key="l.engine" class="text-[13px] text-(--tone-warning-fg)">
            {{ l.engine }} limited until {{ clockTime(l.until, now) }}
          </span>
          <EngineUsage v-for="u in e.w.engineUsage ?? []" :key="u.engine" :usage="u" />
        </article>
      </section>
    </template>
  </aside>
</template>
