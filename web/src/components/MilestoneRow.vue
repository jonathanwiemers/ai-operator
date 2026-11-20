<script setup lang="ts">
import { computed } from 'vue'
import Badge from './Badge.vue'
import ProgressBar from './ProgressBar.vue'
import { costTooltip } from '../format'
import { measuredLabel, totalsLabel, totalsTooltip } from '../lines'
import { leftoversLabel, rowCost, rowTokens, stateTone } from '../milestoneRows'
import type { MilestoneRow } from '../types'

// One milestone row (decision 5): project, title, state, progress, merged
// of total, open issues, cost, tokens and merged production lines ("M of N
// measured" below them while some merged issues have none, R16), and its
// open leftovers. The
// project column is left out on a project's own list. The columns are
// MilestoneList's.
const props = defineProps<{ row: MilestoneRow; showProject?: boolean }>()

const leftovers = computed(() => leftoversLabel(props.row))
const to = computed(() => ({
  name: 'stream-milestone' as const,
  params: { namespace: props.row.namespace, name: props.row.project, iid: String(props.row.iid) },
}))
const project = computed(() => ({
  name: 'stream-project' as const,
  params: { namespace: props.row.namespace, name: props.row.project },
}))
const measured = computed(() => measuredLabel(props.row.mergedLines))
const num = 'text-right whitespace-nowrap text-(--text-2) tabular-nums'
</script>

<template>
  <!-- From desktop width a subgrid of MilestoneList's columns, with the
       list's column gap (a subgrid's own gap moves its lines off the heads');
       below it title and state on one line, the figures wrapping below. -->
  <li
    class="flex flex-wrap items-center gap-x-3 gap-y-1.5 border-t border-(--border) px-3.5 py-[9px] text-[13px] first:border-t-0 desktop:col-span-full desktop:grid desktop:grid-cols-subgrid desktop:gap-x-2"
  >
    <RouterLink v-if="showProject" class="min-w-0 basis-full truncate text-xs font-semibold text-(--text-3)" :to="project">
      {{ row.project }}
    </RouterLink>
    <span class="flex min-w-0 flex-1 flex-wrap items-baseline gap-2 font-semibold [overflow-wrap:anywhere]">
      <RouterLink :to="to">{{ row.title || `%${row.iid}` }}</RouterLink>
      <span v-if="leftovers" class="text-xs font-medium whitespace-nowrap text-(--tone-warning-fg)">{{ leftovers }}</span>
    </span>
    <span class="flex shrink-0 flex-wrap items-center gap-x-1.5">
      <Badge :tone="stateTone(row.state)" :title="row.needsYou ? 'Needs you: see the Inbox' : undefined">
        {{ row.state }}
      </Badge>
      <span v-if="row.needsYou" class="text-xs whitespace-nowrap text-(--tone-warning-fg)">needs you</span>
    </span>
    <span class="flex basis-full flex-wrap items-center gap-x-3 gap-y-1.5 desktop:contents">
      <ProgressBar :merged="row.progress.merged" :total="row.progress.total" :label="String(row.title || row.iid)" />
      <span :class="num" :title="`${row.progress.open} open issues`">
        <span class="text-(--text-3) desktop:hidden">open </span>{{ row.progress.open }}
      </span>
      <span :class="num" :title="costTooltip(row)">{{ rowCost(row) }}</span>
      <span :class="num" :title="`${row.tokens} tokens`">
        <span class="text-(--text-3) desktop:hidden">tok </span>{{ rowTokens(row) }}
      </span>
      <span :class="num" :title="totalsTooltip(row.mergedLines)" data-merged>
        <span class="text-(--text-3) desktop:hidden">merged </span>{{ totalsLabel(row.mergedLines) }}
        <small v-if="measured" class="text-[11px] text-(--text-3) desktop:block desktop:whitespace-normal">{{ measured }}</small>
      </span>
    </span>
  </li>
</template>
