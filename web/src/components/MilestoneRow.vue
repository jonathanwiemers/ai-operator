<script setup lang="ts">
import { computed } from 'vue'
import Badge from './Badge.vue'
import { costTooltip } from '../format'
import { leftoversLabel, progressPct, rowCost, rowTokens, stateTone } from '../milestoneRows'
import type { MilestoneRow } from '../types'

// One milestone row (decision 5): project, title, state, progress, merged
// of total, open issues, cost and tokens, and its open leftovers. The
// project column is left out on a project's own list. The columns are
// MilestoneList's.
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
const num = 'text-right whitespace-nowrap text-(--text-2) tabular-nums'
</script>

<template>
  <!-- From tablet width a subgrid of MilestoneList's columns; on phones
       title and state on one line, the figures wrapping below. -->
  <li
    class="flex flex-wrap items-center gap-x-3 gap-y-1.5 border-t border-(--border) px-3.5 py-[9px] text-[13px] first:border-t-0 tablet:col-span-full tablet:grid tablet:grid-cols-subgrid"
  >
    <RouterLink v-if="showProject" class="basis-full truncate text-xs font-semibold text-(--text-3)" :to="project">
      {{ row.project }}
    </RouterLink>
    <span class="flex min-w-0 flex-1 flex-wrap items-baseline gap-2 font-semibold [overflow-wrap:anywhere]">
      <RouterLink :to="to">{{ row.title || `%${row.iid}` }}</RouterLink>
      <span v-if="leftovers" class="text-xs font-medium whitespace-nowrap text-(--tone-warning-fg)">{{ leftovers }}</span>
    </span>
    <span class="flex shrink-0 items-center gap-1.5">
      <Badge :tone="stateTone(row.state)" :title="row.needsYou ? 'Needs you: see the Inbox' : undefined">
        {{ row.state }}
      </Badge>
      <span v-if="row.needsYou" class="text-xs whitespace-nowrap text-(--tone-warning-fg)">needs you</span>
    </span>
    <span class="flex basis-full flex-wrap items-center gap-x-3 gap-y-1.5 tablet:contents">
      <span class="flex flex-[1_1_10rem] items-center gap-2" :title="`${pct}% merged`">
        <span
          class="h-1.5 flex-1 overflow-hidden rounded-full bg-(--surface-3)"
          role="progressbar"
          :aria-valuenow="pct"
          aria-valuemin="0"
          aria-valuemax="100"
          :aria-label="`${row.title || row.iid}: ${pct}% merged`"
        >
          <span class="block h-full bg-(--tone-success-fg)" :style="{ width: `${pct}%` }" />
        </span>
        <span :class="num" class="min-w-9">{{ row.progress.merged }}/{{ row.progress.total }}</span>
      </span>
      <span :class="num" :title="`${row.progress.open} open issues`">
        <span class="text-(--text-3) tablet:hidden">open </span>{{ row.progress.open }}
      </span>
      <span :class="num" :title="costTooltip(row)">{{ rowCost(row) }}</span>
      <span :class="num" :title="`${row.tokens} tokens`">
        <span class="text-(--text-3) tablet:hidden">tok </span>{{ rowTokens(row) }}
      </span>
    </span>
  </li>
</template>
