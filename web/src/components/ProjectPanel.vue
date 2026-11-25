<script setup lang="ts">
import { computed } from 'vue'
import { useNow } from '../live'
import { projectNowAndNext, type NowGroup, type NowRow } from '../nowAndNext'
import type { HistoryResponse, MilestoneView, ProjectDetail } from '../types'
import NowNext from './NowNext.vue'
import ProjectHistory from './ProjectHistory.vue'

// ProjectPanel is a project page's sidebar (R9 of "Desk: the Cockpit newest
// first by issue"), laid out by ContextLayout like MilestonePanel: a column
// from 1180 px (collapsible to a rail), a slide-over below that the Now
// strip opens. NowNext shows Running and Waiting of every milestone under
// its title, Up next of the milestone that runs first (first, its loaded
// view, null while none runs) and the project queue; ProjectHistory sits
// below, closed. A Running or Waiting row opens the issue's thread in its milestone's Cockpit
// (#issue-<iid>); with the transcript grant, which is the project's, a
// running row with a live run opens its Live transcript too (?live=1).
const props = defineProps<{
  namespace: string
  name: string
  project: ProjectDetail | null
  // milestone: the loaded milestone view, for the companions and the
  // transcript grant.
  milestone: MilestoneView | null
  first: MilestoneView | null
  history: HistoryResponse | null
  jumped?: string
  sheet?: boolean
}>()
const emit = defineEmits<{ follow: []; day: [day: string]; clear: [] }>()

const now = useNow()
const nn = computed(() =>
  projectNowAndNext({ milestones: props.project?.milestones, first: props.first, queue: props.project?.queue, now: now.value }),
)

function milestoneTo(iid: number) {
  const { namespace, name } = props
  return { name: 'stream-milestone' as const, params: { namespace, name, iid: String(iid) } }
}
function rowTo(row: NowRow, running: boolean, group: NowGroup) {
  const live = running && !!row.run && !!props.milestone?.permissions?.transcript
  return { ...milestoneTo(Number(group.key)), query: live ? { live: '1' } : {}, hash: `#issue-${row.iid}` }
}
// "+N more" of Up next: the Issues tab of the milestone it is of.
const moreTo = computed(() => (nn.value.nextOf ? { ...milestoneTo(nn.value.nextOf), query: { tab: 'issues' } } : undefined))
</script>

<template>
  <aside class="ppanel" :class="{ column: !sheet }" aria-label="Now & next" data-project-panel>
    <h2 v-if="!sheet">Now &amp; next</h2>
    <div v-if="!project" class="faint">Loading…</div>
    <NowNext
      v-else
      :running="nn.running"
      :waiting="nn.waiting"
      :next="nn.next"
      :more-next="nn.moreNext"
      :more-to="moreTo"
      :milestones="nn.milestones"
      :row-to="rowTo"
      :milestone-to="milestoneTo"
      @follow="emit('follow')"
    />
    <ProjectHistory
      :namespace="namespace"
      :name="name"
      :milestone="milestone"
      :history="history"
      :jumped="jumped"
      @day="emit('day', $event)"
      @clear="emit('clear')"
    />
  </aside>
</template>

<!-- Scoped, not Tailwind utilities: they load with the page, not with the
     initial stylesheet (docs/contributing/web-ui.md, "Bundle budget"). -->
<style scoped>
.ppanel {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  align-content: start;
  gap: 14px;
  min-width: 0;
  min-height: 0;
  padding: 16px;
  background: var(--surface);
}
.ppanel.column {
  overflow-y: auto;
  border-left: 1px solid var(--border);
}
h2 {
  margin: 0;
  font-size: 15px;
  font-weight: 700;
}
.faint {
  font-size: 12px;
  color: var(--text-3);
}
</style>
