<script setup lang="ts">
import { barColors, type ModelBarData } from '../spend'

// ModelBar is a drill-down row's stacked bar, one segment per model (main
// first, subagents after), on the table's shared scale (see modelBars).
defineProps<{ bar: ModelBarData }>()
</script>

<template>
  <div class="mbar" :class="{ fallback: bar.fallback }" :title="bar.title || undefined">
    <svg class="chart" viewBox="0 0 100 10" preserveAspectRatio="none" role="img" :aria-label="`Tokens and cost by model: ${bar.label}`">
      <rect x="0" y="0" width="100" height="10" fill="var(--surface-3)" />
      <rect
        v-for="(s, i) in bar.segments"
        :key="i"
        :x="s.x"
        y="0"
        :width="s.width"
        height="10"
        :fill="barColors[s.slot]"
        :opacity="bar.fallback ? 0.35 : 0.85"
        stroke="var(--surface)"
        stroke-width="2"
        vector-effect="non-scaling-stroke"
      >
        <title>{{ s.title }}</title>
      </rect>
    </svg>
    <span class="lbl">{{ bar.label }}</span>
  </div>
</template>

<style scoped>
.mbar {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 140px;
}
.chart {
  flex: 1;
  height: 10px;
  display: block;
  border-radius: 3px;
}
.lbl {
  min-width: 3.5em;
  text-align: right;
  font-size: 12px;
  color: var(--text-2);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.fallback .lbl {
  font-style: italic;
}
</style>
