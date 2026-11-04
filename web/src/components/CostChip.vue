<script setup lang="ts">
import { computed } from 'vue'
import { costChip, costTooltip } from '../format'
import type { Cost } from '../types'

// CostChip shows what a project, milestone or task cost: "$12.34 · 1.2M
// tok", "n/a" when the cost is unknown, with the breakdown as its tooltip.
const props = defineProps<{ cost?: Cost | null; small?: boolean }>()

const text = computed(() => costChip(props.cost))
</script>

<template>
  <span v-if="cost && text" class="cost-chip" :class="{ small, unknown: !cost.costKnown }" :title="costTooltip(cost)">
    {{ text }}
  </span>
</template>

<style scoped>
.cost-chip {
  display: inline-flex;
  align-items: center;
  height: 22px;
  padding: 0 8px;
  border-radius: 999px;
  background: var(--surface-3);
  color: var(--text-2);
  font-size: 12px;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  cursor: default;
}
.cost-chip.small {
  height: 18px;
  padding: 0 6px;
  font-size: 11px;
}
.cost-chip.unknown {
  font-style: italic;
}
</style>
