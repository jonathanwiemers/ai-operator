<script setup lang="ts">
import { computed } from 'vue'
import { chartBox, contextChart, lineColor, markerColor } from '../contextChart'
import type { SpendChild } from '../types'

// ContextChart is a run's context-growth chart on the Task page: context
// tokens per turn, one line per implementer step, markers where a subagent
// ran. It renders nothing when no step has a series (older runs).
const props = defineProps<{ calls: SpendChild[] }>()
const chart = computed(() => contextChart(props.calls))
const b = chartBox
</script>

<template>
  <figure v-if="chart" class="ctx">
    <svg class="chart" :viewBox="`0 0 ${b.width} ${b.height}`" role="img" :aria-label="chart.label">
      <g class="grid">
        <line v-for="t in chart.yTicks" :key="`y${t.label}`" :x1="b.left" :x2="b.width - b.right" :y1="t.pos" :y2="t.pos" />
      </g>
      <g class="axis">
        <text v-for="t in chart.yTicks" :key="`yl${t.label}`" :x="b.left - 6" :y="t.pos" text-anchor="end" dominant-baseline="middle">
          {{ t.label }}
        </text>
        <text v-for="t in chart.xTicks" :key="`xl${t.label}`" :x="t.pos" :y="b.height - 6" text-anchor="middle">{{ t.label }}</text>
      </g>
      <g v-for="s in chart.series" :key="s.key">
        <polyline
          fill="none"
          :stroke="lineColor"
          :stroke-opacity="s.opacity"
          stroke-width="2"
          stroke-linejoin="round"
          :points="s.points"
        />
        <polyline class="hit" fill="none" stroke="transparent" stroke-width="10" :points="s.points">
          <title>{{ s.end.title }}</title>
        </polyline>
        <circle :cx="s.end.x" :cy="s.end.y" r="3" :fill="lineColor" :fill-opacity="s.opacity" stroke="var(--surface)" stroke-width="1.5">
          <title>{{ s.end.title }}</title>
        </circle>
        <circle
          v-for="m in s.markers"
          :key="m.turn"
          :cx="m.x"
          :cy="m.y"
          r="4"
          :fill="markerColor"
          stroke="var(--surface)"
          stroke-width="2"
        >
          <title>{{ m.title }}</title>
        </circle>
      </g>
    </svg>
    <figcaption class="legend">
      <span class="faint">Context tokens per turn</span>
      <span v-for="s in chart.series" :key="s.key"><i :style="{ background: lineColor, opacity: s.opacity }" />{{ s.label }}</span>
      <span v-if="chart.series.some((s) => s.markers.length)"><i class="dot" :style="{ background: markerColor }" />subagent ran</span>
    </figcaption>
  </figure>
</template>

<style scoped>
.ctx {
  margin: 0 0 10px;
  max-width: 720px;
}
.chart {
  width: 100%;
  height: auto;
  display: block;
}
.grid line {
  stroke: var(--border);
  stroke-width: 1;
}
.axis text {
  font-size: 11px;
  fill: var(--text-3);
  font-variant-numeric: tabular-nums;
}
.legend {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 14px;
  font-size: 12px;
  color: var(--text-2);
}
.legend i {
  display: inline-block;
  width: 12px;
  height: 3px;
  border-radius: 2px;
  margin-right: 5px;
  vertical-align: middle;
}
.legend i.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}
</style>
