<script setup lang="ts">
import MilestoneRowItem from './MilestoneRow.vue'
import { milestoneRowKey } from '../milestoneRows'
import type { MilestoneRow } from '../types'

// A list of milestone rows (decision 5), the dashboard's and the project
// page's. showProject: the rows span projects, so each names its own.
//
// The list owns the one column grid (docs/ui.md, "Milestone rows"): from
// tablet width the column heads and every MilestoneRow are subgrids of it.
// The edge tracks are 14 px wider than their content, because a subgrid's
// padding (px-3.5) is taken from its edge tracks. On phones the heads are
// hidden and the rows stack, labelling their figures.
const props = defineProps<{ rows: MilestoneRow[]; showProject?: boolean; label: string }>()

const columns = () =>
  props.showProject
    ? 'tablet:grid-cols-[134px_minmax(0,1fr)_140px_160px_56px_72px_78px]'
    : 'tablet:grid-cols-[minmax(0,1fr)_140px_160px_56px_72px_78px]'
</script>

<template>
  <div class="card overflow-hidden p-0 tablet:grid tablet:gap-x-3" :class="columns()">
    <div
      class="hidden border-b border-(--border) bg-(--surface-2) px-3.5 py-[7px] text-[11px] font-semibold tracking-[0.04em] text-(--text-3) uppercase tablet:col-span-full tablet:grid tablet:grid-cols-subgrid"
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
    </div>
    <ul class="m-0 list-none p-0 tablet:col-span-full tablet:grid tablet:grid-cols-subgrid" :aria-label="label">
      <MilestoneRowItem v-for="r in rows" :key="milestoneRowKey(r)" :row="r" :show-project="showProject" />
    </ul>
  </div>
</template>
