<script setup lang="ts">
import { computed } from 'vue'
import { progressPct } from '../milestoneRows'

// A milestone's progress: a bar of merged out of total, then "merged/total".
// label names what progresses for screen readers (the milestone's title).
// Shared by MilestoneRow and PlanRow.
const props = defineProps<{ merged: number; total: number; label?: string }>()

const pct = computed(() => progressPct({ progress: { merged: props.merged, total: props.total, open: 0 } }))
const aria = computed(() => (props.label ? `${props.label}: ${pct.value}% merged` : `${pct.value}% merged`))
</script>

<template>
  <span class="flex flex-[1_1_10rem] items-center gap-2" :title="`${pct}% merged`">
    <span
      class="h-1.5 flex-1 overflow-hidden rounded-full bg-(--surface-3)"
      role="progressbar"
      :aria-valuenow="pct"
      aria-valuemin="0"
      aria-valuemax="100"
      :aria-label="aria"
    >
      <span class="block h-full bg-(--tone-success-fg)" :style="{ width: `${pct}%` }" />
    </span>
    <span class="min-w-9 text-right whitespace-nowrap text-(--text-2) tabular-nums">{{ merged }}/{{ total }}</span>
  </span>
</template>
