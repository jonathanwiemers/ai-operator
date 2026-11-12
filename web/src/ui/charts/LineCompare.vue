<script setup lang="ts">
// A line with a dashed comparison line (docs/ui.md, "Charts"): series 0 is
// solid, series 1 the comparison, dashed and in its muted token. The
// Chart.js options of every such chart live here; the view passes the shaped
// data (chartData.ts's cumulativeMonth). Same options policy as
// StackedBars.vue: no animation, tooltips on hover and tap, fewer ticks on
// phones; the canvas is role="img" with the summary as its aria-label.
import './register'
import type { ChartData, ChartOptions } from 'chart.js'
import { computed, onBeforeUnmount, ref } from 'vue'
import { Line } from 'vue-chartjs'
import { formatTokens, formatUSD, type ChartView } from '../../chartData'
import { useChartColors } from './useChartColors'

const props = withDefaults(
  defineProps<{
    chart: ChartView
    // summary is the canvas's aria-label (chartData.ts's monthSummary).
    summary: string
    // title prefixes a tooltip's label ("Day 12").
    title?: string
    unit?: 'usd' | 'tokens'
  }>(),
  { title: '', unit: 'usd' },
)

const colors = useChartColors()
const font = getComputedStyle(document.body).fontFamily

// Fewer ticks below the tablet width (760 px, docs/design.md "Widths").
const phoneQuery = window.matchMedia('(max-width: 759.98px)')
const phone = ref(phoneQuery.matches)
const onPhone = (e: MediaQueryListEvent) => (phone.value = e.matches)
phoneQuery.addEventListener('change', onPhone)
onBeforeUnmount(() => phoneQuery.removeEventListener('change', onPhone))

const format = computed(() => (props.unit === 'usd' ? formatUSD : formatTokens))

const data = computed<ChartData<'line'>>(() => ({
  labels: props.chart.labels,
  datasets: props.chart.series.map((s, i) => {
    const color = colors.value.series[s.token]
    return {
      label: s.label,
      data: s.values,
      borderColor: color,
      backgroundColor: color,
      borderWidth: i === 0 ? 2.5 : 2,
      borderDash: s.dashed ? [6, 4] : [],
      pointRadius: 0,
      pointHoverRadius: 3,
      pointHitRadius: 6,
      spanGaps: false,
      // The solid line is drawn over the dashed one.
      order: i === 0 ? 0 : 1,
    }
  }),
}))

const options = computed<ChartOptions<'line'>>(() => {
  const c = colors.value
  const fmt = format.value
  const ticks = { color: c.muted, font: { family: font, size: 12 } }
  return {
    animation: false,
    responsive: true,
    maintainAspectRatio: false,
    // Hover and tap show both lines of the day.
    interaction: { mode: 'index', intersect: false },
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
        // A line without a point that day (after today) has no line.
        filter: (item) => item.parsed.y !== null && item.parsed.y !== undefined,
        callbacks: {
          title: (items) => (items[0] ? `${props.title}${items[0].label}` : ''),
          label: (item) => ` ${item.dataset.label}: ${fmt(item.parsed.y ?? 0)}`,
          footer: (items) => (items[0] ? (props.chart.notes?.[items[0].dataIndex] ?? '') : ''),
        },
      },
    },
    scales: {
      x: {
        grid: { display: false },
        border: { color: c.grid },
        ticks: { ...ticks, maxRotation: 0, autoSkip: true, maxTicksLimit: phone.value ? 6 : 16 },
      },
      y: {
        beginAtZero: true,
        grid: { color: c.grid },
        border: { display: false },
        ticks: { ...ticks, maxTicksLimit: phone.value ? 4 : 6, callback: (v) => fmt(Number(v)) },
      },
    },
  }
})
</script>

<template>
  <!-- Chart.js fills the container: the height is fixed, the width follows it. -->
  <div class="relative h-60 min-w-0 tablet:h-72" data-chart-canvas>
    <Line :data="data" :options="options" :aria-label="summary" />
  </div>
</template>
