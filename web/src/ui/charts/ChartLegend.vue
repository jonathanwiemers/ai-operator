<script setup lang="ts">
// A chart's legend (docs/ui.md, "Charts"): swatch, label and range total per
// series, as HTML below the chart, so it wraps on phones and screen readers
// read it. Colour is never alone: the label names the series.
import { computed } from 'vue'
import { formatTokens, formatUSD, type ChartToken, type StackedChart } from '../../chartData'

const props = withDefaults(defineProps<{ chart: StackedChart; unit?: 'usd' | 'tokens' }>(), { unit: 'usd' })

// Whole class names, so Tailwind finds them; no inline style (the CSP).
const swatch: Record<ChartToken, string> = {
  '--chart-1': 'bg-(--chart-1)',
  '--chart-2': 'bg-(--chart-2)',
  '--chart-3': 'bg-(--chart-3)',
  '--chart-4': 'bg-(--chart-4)',
  '--chart-5': 'bg-(--chart-5)',
  '--chart-6': 'bg-(--chart-6)',
  '--chart-other': 'bg-(--chart-other)',
}

const format = computed(() => (props.unit === 'usd' ? formatUSD : formatTokens))
</script>

<template>
  <ul class="m-0 flex list-none flex-wrap gap-x-4 gap-y-1 p-0 text-[13px]" aria-label="Legend" data-chart-legend>
    <li v-for="s in chart.series" :key="s.key" class="flex min-w-0 items-baseline gap-1.5">
      <i class="inline-block size-2.5 shrink-0 rounded-xs" :class="swatch[s.token]" aria-hidden="true" />
      <span class="min-w-0 [overflow-wrap:anywhere]">{{ s.label }}</span>
      <span class="font-semibold tabular-nums">{{ format(s.total) }}</span>
    </li>
  </ul>
</template>
