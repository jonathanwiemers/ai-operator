<script setup lang="ts">
import { computed } from 'vue'
import Badge from './Badge.vue'
import { costTooltip } from '../format'
import { leftoversLabel, progressPct, rowCost, rowTokens, stateTone } from '../milestoneRows'
import type { MilestoneRow } from '../types'

// One milestone row (decision 5): project, title, state, progress, merged
// of total, open issues, cost and tokens, and its open leftovers. The
// project column is left out on a project's own list.
const props = defineProps<{ row: MilestoneRow; showProject?: boolean }>()

const pct = computed(() => progressPct(props.row))
const leftovers = computed(() => leftoversLabel(props.row))
const to = computed(() => ({
  name: 'stream-milestone' as const,
  params: { namespace: props.row.namespace, name: props.row.project, iid: String(props.row.iid) },
}))
const project = computed(() => ({
  name: 'stream-project' as const,
  params: { namespace: props.row.namespace, name: props.row.project },
}))
</script>

<template>
  <li class="mrow" :class="{ 'with-project': showProject }">
    <RouterLink v-if="showProject" class="proj" :to="project">{{ row.project }}</RouterLink>
    <span class="title">
      <RouterLink :to="to">{{ row.title || `%${row.iid}` }}</RouterLink>
      <span v-if="leftovers" class="leftovers">{{ leftovers }}</span>
    </span>
    <span class="state">
      <Badge :tone="stateTone(row.state)" :title="row.needsYou ? 'Needs you: see the Inbox' : undefined">
        {{ row.state }}
      </Badge>
      <span v-if="row.needsYou" class="needs">needs you</span>
    </span>
    <span class="progress" :title="`${pct}% merged`">
      <span
        class="bar"
        role="progressbar"
        :aria-valuenow="pct"
        aria-valuemin="0"
        aria-valuemax="100"
        :aria-label="`${row.title || row.iid}: ${pct}% merged`"
      >
        <span :style="{ width: `${pct}%` }" />
      </span>
      <span class="num">{{ row.progress.merged }}/{{ row.progress.total }}</span>
    </span>
    <span class="num open" :title="`${row.progress.open} open issues`">
      <span class="lbl">open </span>{{ row.progress.open }}
    </span>
    <span class="num cost" :title="costTooltip(row)">{{ rowCost(row) }}</span>
    <span class="num tokens" :title="`${row.tokens} tokens`"><span class="lbl">tok </span>{{ rowTokens(row) }}</span>
  </li>
</template>

<style scoped>
.mrow {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 140px 160px 56px 72px 64px;
  align-items: center;
  gap: 12px;
  padding: 9px 14px;
  border-top: 1px solid var(--border);
  font-size: 13px;
}
.mrow:first-child {
  border-top: 0;
}
.mrow.with-project {
  grid-template-columns: 120px minmax(0, 1fr) 140px 160px 56px 72px 64px;
}
.proj {
  font-size: 12px;
  font-weight: 600;
  color: var(--text-3);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.title {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 8px;
  min-width: 0;
  font-weight: 600;
  overflow-wrap: anywhere;
}
.leftovers {
  font-size: 12px;
  font-weight: 500;
  color: var(--tone-warning-fg);
  white-space: nowrap;
}
.state {
  display: flex;
  align-items: center;
  gap: 6px;
}
.needs {
  font-size: 12px;
  color: var(--tone-warning-fg);
  white-space: nowrap;
}
.progress {
  display: flex;
  align-items: center;
  gap: 8px;
}
.bar {
  flex: 1;
  height: 6px;
  border-radius: 999px;
  background: var(--surface-3);
  overflow: hidden;
}
.bar > span {
  display: block;
  height: 100%;
  background: var(--tone-success-fg);
}
.num {
  font-variant-numeric: tabular-nums;
  color: var(--text-2);
  text-align: right;
  white-space: nowrap;
}
.progress .num {
  min-width: 36px;
}
.lbl {
  display: none;
  color: var(--text-3);
}
/* Narrow screens: the row stacks, project and title on top, the figures
   below, labelled. */
@media (max-width: 759.98px) {
  .mrow,
  .mrow.with-project {
    grid-template-columns: minmax(0, 1fr) auto auto auto;
    grid-template-areas:
      'proj proj proj proj'
      'title title title state'
      'progress progress progress progress'
      'open cost tokens .';
    gap: 6px 12px;
  }
  .proj {
    grid-area: proj;
  }
  .title {
    grid-area: title;
  }
  .state {
    grid-area: state;
    justify-self: end;
  }
  .progress {
    grid-area: progress;
  }
  .open {
    grid-area: open;
    text-align: left;
  }
  .cost {
    grid-area: cost;
  }
  .tokens {
    grid-area: tokens;
  }
  .lbl {
    display: inline;
  }
}
</style>
