<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useNow } from '../live'
import { nowAndNext, type NowGroup, type NowRow } from '../nowAndNext'
import { milestoneTabs, tabQuery } from '../streamTabs'
import type { QueueEntry } from '../generated/views'
import type { MilestoneView } from '../types'
import NowNext from './NowNext.vue'

// MilestonePanel is a milestone page's sidebar, Now & next (R7 of "Desk:
// the Cockpit newest first by issue"), laid out by ContextLayout: a column
// from 1180 px (collapsible to a rail), a slide-over below that the Now strip
// opens. It feeds NowNext from the milestone's queue items and tasks and
// the project's queue. A Running or Waiting row opens the issue's thread
// in the Cockpit (#issue-<iid>), from any tab; with the transcript grant a
// running row with a live run opens its Live transcript too (?live=1).
// Everything the old Issues view listed is on the Issues tab ("+N more").
const props = defineProps<{
  namespace: string
  name: string
  iid: number
  milestone: MilestoneView | null
  queue: QueueEntry[]
  // sheet: the panel is in the slide-over, which has its own edge, title
  // and scroll.
  sheet?: boolean
}>()
const emit = defineEmits<{ follow: [] }>()

const route = useRoute()
const now = useNow()
const nn = computed(() =>
  nowAndNext({
    milestone: props.milestone ?? { iid: props.iid, title: `%${props.iid}` },
    items: props.milestone?.autopilot?.items,
    tasks: props.milestone?.tasks,
    queue: props.queue,
    now: now.value,
  }),
)
const group = (rows: NowRow[]): NowGroup[] => [{ key: String(props.iid), rows }]

function milestoneTo(iid: number) {
  const { namespace, name } = props
  return { name: 'stream-milestone' as const, params: { namespace, name, iid: String(iid) } }
}
function rowTo(row: NowRow, running: boolean) {
  const { live: _, ...query } = tabQuery(route.query, 'cockpit', milestoneTabs)
  const live = running && !!row.run && !!props.milestone?.permissions?.transcript
  return { ...milestoneTo(props.iid), query: live ? { ...query, live: '1' } : query, hash: `#issue-${row.iid}` }
}
const moreTo = computed(() => ({ ...milestoneTo(props.iid), query: tabQuery(route.query, 'issues', milestoneTabs) }))
</script>

<template>
  <aside class="mpanel" :class="{ column: !sheet }" aria-label="Now & next" data-milestone-panel>
    <h2 v-if="!sheet">Now &amp; next</h2>
    <div v-if="!milestone" class="faint">Loading…</div>
    <NowNext
      v-else
      :running="group(nn.running)"
      :waiting="group(nn.waiting)"
      :next="nn.next"
      :more-next="nn.moreNext"
      :more-to="moreTo"
      :milestones="nn.milestones"
      :row-to="rowTo"
      :milestone-to="milestoneTo"
      @follow="emit('follow')"
    />
  </aside>
</template>

<!-- Scoped, not Tailwind utilities: they load with the page, not with the
     initial stylesheet (docs/contributing/web-ui.md, "Bundle budget"). -->
<style scoped>
.mpanel {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  align-content: start;
  gap: 14px;
  min-width: 0;
  min-height: 0;
  padding: 16px;
  background: var(--surface);
}
.mpanel.column {
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
