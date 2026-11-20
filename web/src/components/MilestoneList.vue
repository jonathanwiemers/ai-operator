<script setup lang="ts">
import MilestoneRowItem from './MilestoneRow.vue'
import { milestoneRowKey } from '../milestoneRows'
import type { MilestoneRow } from '../types'

// A list of milestone rows (decision 5), the dashboard's and the project
// page's. showProject: the rows span projects, so each names its own.
//
// The list owns the one column grid (docs/contributing/web-ui.md, "Milestone rows"): from
// desktop width the column heads and every MilestoneRow are subgrids of it.
// Only the title's track grows; the figures' tracks take their content's
// width (auto) and the progress bar 72 to 160 px, so the title keeps at
// least 8rem beside the context panel and the heads never run into each
// other. A subgrid's padding (px-3.5) adds to its edge tracks. Below
// desktop width the heads are hidden and the rows stack, labelling their
// figures: a tablet's content pane is too narrow for eight columns.
const props = defineProps<{ rows: MilestoneRow[]; showProject?: boolean; label: string }>()

const columns = () =>
  props.showProject
    ? 'desktop:grid-cols-[minmax(5rem,134px)_minmax(8rem,1fr)_auto_minmax(4.5rem,160px)_auto_auto_auto_auto]'
    : 'desktop:grid-cols-[minmax(8rem,1fr)_auto_minmax(4.5rem,160px)_auto_auto_auto_auto]'
</script>

<template>
  <div class="card overflow-hidden p-0 desktop:grid desktop:gap-x-2" :class="columns()">
    <div
      class="hidden border-b border-(--border) bg-(--surface-2) px-3.5 py-[7px] text-[11px] font-semibold tracking-[0.04em] text-(--text-3) uppercase desktop:col-span-full desktop:grid desktop:grid-cols-subgrid"
      aria-hidden="true"
      data-heads
    >
      <span v-if="showProject">Project</span>
      <span>Milestone</span>
      <span>State</span>
      <span>Merged</span>
      <span class="text-right">Open</span>
      <span class="text-right">Cost</span>
      <span class="text-right">Tokens</span>
      <span class="text-right" title="Merged production lines">Lines</span>
    </div>
    <ul class="m-0 list-none p-0 desktop:col-span-full desktop:grid desktop:grid-cols-subgrid" :aria-label="label">
      <MilestoneRowItem v-for="r in rows" :key="milestoneRowKey(r)" :row="r" :show-project="showProject" />
    </ul>
  </div>
</template>
