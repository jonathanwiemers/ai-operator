<script setup lang="ts">
import MilestoneRowItem from './MilestoneRow.vue'
import { milestoneRowKey } from '../milestoneRows'
import type { MilestoneRow } from '../types'

// A list of milestone rows (decision 5), the dashboard's and the project
// page's. showProject: the rows span projects, so each names its own. The
// column heads match MilestoneRow's grid; narrow screens hide them, the
// rows label their figures there.
defineProps<{ rows: MilestoneRow[]; showProject?: boolean; label: string }>()
</script>

<template>
  <div class="mlist card">
    <div class="heads" :class="{ 'with-project': showProject }" aria-hidden="true">
      <span v-if="showProject">Project</span>
      <span>Milestone</span>
      <span>State</span>
      <span>Merged</span>
      <span class="r">Open</span>
      <span class="r">Cost</span>
      <span class="r">Tokens</span>
    </div>
    <ul :aria-label="label">
      <MilestoneRowItem v-for="r in rows" :key="milestoneRowKey(r)" :row="r" :show-project="showProject" />
    </ul>
  </div>
</template>

<style scoped>
.mlist {
  padding: 0;
  overflow: hidden;
}
ul {
  list-style: none;
  margin: 0;
  padding: 0;
}
.heads {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 140px 160px 56px 72px 64px;
  gap: 12px;
  padding: 7px 14px;
  border-bottom: 1px solid var(--border);
  background: var(--surface-2, var(--surface));
  color: var(--text-3);
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
}
.heads.with-project {
  grid-template-columns: 120px minmax(0, 1fr) 140px 160px 56px 72px 64px;
}
.r {
  text-align: right;
}
@media (max-width: 760px) {
  .heads {
    display: none;
  }
}
</style>
