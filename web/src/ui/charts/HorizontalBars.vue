<script setup lang="ts">
// Horizontal bars (docs/ui.md, "Charts"): one bar per row, its known cost in
// USD. The Chart.js options of every such chart live here; the view passes
// the shaped rows (chartData.ts's breakdownRows). A click on a bar with a
// link emits `select` with its index; the view navigates. Same options
// policy as StackedBars.vue: no animation, tooltip on hover and tap, the
// canvas is role="img" with the summary as its aria-label.
import './register'
import type { ChartData, ChartOptions } from 'chart.js'
import { computed } from 'vue'
import { Bar } from 'vue-chartjs'
import { formatUSD, unknownNote, type BreakdownRow } from '../../chartData'
import { useChartColors } from './useChartColors'

const props = defineProps<{ rows: BreakdownRow[]; summary: string }>()
const emit = defineEmits<{ select: [index: number] }>()

const colors = useChartColors()
const font = getComputedStyle(document.body).fontFamily

// Rows are 34 px high plus the axis, so the canvas grows with them.
const height = computed(() => `${props.rows.length * 34 + 36}px`)

const data = computed<ChartData<'bar'>>(() => ({
  labels: props.rows.map((r) => r.label),
  datasets: [
    {
      data: props.rows.map((r) => r.value),
      backgroundColor: props.rows.map((r) => colors.value.series[r.token]),
      borderWidth: 0,
      maxBarThickness: 22,
    },
  ],
}))

const options = computed<ChartOptions<'bar'>>(() => {
  const c = colors.value
  const ticks = { color: c.muted, font: { family: font, size: 12 } }
  return {
    indexAxis: 'y',
    animation: false,
    responsive: true,
    maintainAspectRatio: false,
    onClick: (_e, elements) => {
      const i = elements[0]?.index
      if (i !== undefined && props.rows[i]?.link) emit('select', i)
    },
    onHover: (e, elements, chart) => {
      const i = elements[0]?.index
      const target = (e.native?.target ?? chart.canvas) as HTMLElement
      target.style.cursor = i !== undefined && props.rows[i]?.link ? 'pointer' : 'default'
    },
    plugins: {
      tooltip: {
        backgroundColor: c.surface,
        borderColor: c.grid,
        borderWidth: 1,
        titleColor: c.text,
        bodyColor: c.text,
        footerColor: c.muted,
        titleFont: { family: font },
        bodyFont: { family: font },
        footerFont: { family: font, weight: 'normal' },
        callbacks: {
          label: (item) => ` ${formatUSD(item.parsed.x ?? 0)}`,
          footer: (items) => (items[0] ? unknownNote(props.rows[items[0].dataIndex]?.unknown ?? 0) : ''),
        },
      },
    },
    scales: {
      x: {
        beginAtZero: true,
        grid: { color: c.grid },
        border: { display: false },
        ticks: { ...ticks, maxTicksLimit: 4, callback: (v) => formatUSD(Number(v)) },
      },
      y: {
        grid: { display: false },
        border: { color: c.grid },
        // Long keys (a project's namespace/name) are cut; the legend has them whole.
        ticks: {
          ...ticks,
          callback(v) {
            const s = String(this.getLabelForValue(Number(v)))
            return s.length > 18 ? `${s.slice(0, 17)}…` : s
          },
        },
      },
    },
  }
})
</script>

<template>
  <!-- Chart.js fills the container: the width follows it, the height the rows. -->
  <div class="relative min-w-0" :style="{ height }" data-chart-canvas>
    <Bar :data="data" :options="options" :aria-label="summary" />
  </div>
</template>
