<script setup lang="ts">
import { barColors, type ModelBarData } from '../spend'

// ModelBar is a drill-down row's stacked bar, one segment per model (main
// first, subagents after), on the table's shared scale (see modelBars).
defineProps<{ bar: ModelBarData }>()
</script>

<template>
  <div class="flex min-w-35 items-center gap-2" :title="bar.title || undefined">
    <svg
      class="block h-2.5 flex-1 rounded-[3px]"
      viewBox="0 0 100 10"
      preserveAspectRatio="none"
      role="img"
      :aria-label="`Tokens and cost by model: ${bar.label}`"
    >
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
    <span class="min-w-[3.5em] text-right text-xs whitespace-nowrap text-(--text-2) tabular-nums" :class="{ italic: bar.fallback }">{{
      bar.label
    }}</span>
  </div>
</template>
