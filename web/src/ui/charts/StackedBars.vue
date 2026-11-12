<script setup lang="ts">
// Stacked bars (docs/ui.md, "Charts"): one bar per bucket, a segment per
// series. The Chart.js options of every stacked bar chart live here; the
// view passes the shaped data (chartData.ts's stackedChart). The canvas is
// role="img" with the summary as its aria-label (vue-chartjs); the legend
// and the table view carry the values as text.
import './register'
import type { ChartData, ChartOptions, TooltipItem } from 'chart.js'
import { computed, onBeforeUnmount, ref } from 'vue'
import { Bar } from 'vue-chartjs'
import { formatTokens, formatUSD, unknownNote, type StackedChart } from '../../chartData'
import { useChartColors } from './useChartColors'

const props = withDefaults(
  defineProps<{
    chart: StackedChart
    // summary is the canvas's aria-label (chartData.ts's chartSummary).
    summary: string
    unit?: 'usd' | 'tokens'
  }>(),
  { unit: 'usd' },
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

const data = computed<ChartData<'bar'>>(() => ({
  labels: props.chart.labels,
  datasets: props.chart.series.map((s) => ({
    label: s.label,
    data: s.values,
    backgroundColor: colors.value.series[s.token],
    borderWidth: 0,
    maxBarThickness: 40,
  })),
}))

// zeroBucket: no series has a value in the bar (only calls of unknown cost).
function zeroBucket(item: TooltipItem<'bar'>): boolean {
  return item.chart.data.datasets.every((d) => !d.data[item.dataIndex])
}

const options = computed<ChartOptions<'bar'>>(() => {
  const c = colors.value
  const fmt = format.value
  const ticks = { color: c.muted, font: { family: font, size: 12 } }
  return {
    animation: false,
    responsive: true,
    maintainAspectRatio: false,
    // Hover and tap show every series of the bucket.
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
        // The series with a value; a bucket of unknown cost only keeps one
        // line, which says so.
        filter: (item, _i, items) => !!item.parsed.y || (zeroBucket(item) && item === items[0]),
        callbacks: {
          label: (item) => (zeroBucket(item) ? 'No known cost' : ` ${item.dataset.label}: ${fmt(item.parsed.y ?? 0)}`),
          footer: (items) => (items[0] ? unknownNote(props.chart.unknown[items[0].dataIndex] ?? 0) : ''),
        },
      },
    },
    scales: {
      x: {
        stacked: true,
        grid: { display: false },
        border: { color: c.grid },
        ticks: { ...ticks, maxRotation: 0, autoSkip: true, maxTicksLimit: phone.value ? 4 : 10 },
      },
      y: {
        stacked: true,
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
    <Bar :data="data" :options="options" :aria-label="summary" />
  </div>
</template>
