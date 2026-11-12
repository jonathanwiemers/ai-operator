<script setup lang="ts">
// Stacked bars (docs/ui.md, "Charts"): one bar per bucket, a segment per
// series. The Chart.js options of every stacked bar chart live here; the
// view passes the shaped data (chartData.ts's stackedChart). The canvas is
// role="img" with the summary as its aria-label (vue-chartjs); the legend
// and the table view carry the values as text.
import './register'
import type { ChartData, ChartOptions, TooltipItem } from 'chart.js'
import { computed, onBeforeUnmount, ref } from 'vue'
import { Chart } from 'vue-chartjs'
import { formatPercent, formatTokens, formatUSD, unknownNote, type ShareLine, type StackedChart } from '../../chartData'
import { useChartColors } from './useChartColors'

const props = withDefaults(
  defineProps<{
    chart: StackedChart
    // summary is the canvas's aria-label (chartData.ts's chartSummary).
    summary: string
    unit?: 'usd' | 'tokens'
    // line is an optional percentage line (0 to 100) on a second y axis on
    // the right, the cache-read share of the Tokens chart.
    line?: ShareLine
  }>(),
  { unit: 'usd', line: undefined },
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

const data = computed<ChartData<'bar' | 'line'>>(() => {
  const bars = props.chart.series.map((s) => ({
    type: 'bar' as const,
    label: s.label,
    data: s.values,
    backgroundColor: colors.value.series[s.token],
    borderWidth: 0,
    maxBarThickness: 40,
  }))
  const l = props.line
  if (!l) return { labels: props.chart.labels, datasets: bars }
  const color = colors.value.series[l.token]
  const line = {
    type: 'line' as const,
    label: l.label,
    data: l.values,
    yAxisID: 'share',
    borderColor: color,
    backgroundColor: color,
    borderWidth: 2.5,
    pointRadius: 2,
    pointHoverRadius: 4,
    spanGaps: false,
    // Drawn over the bars.
    order: -1,
  }
  return { labels: props.chart.labels, datasets: [...bars, line] }
})

const isShare = (item: TooltipItem<'bar' | 'line'>) => item.dataset.yAxisID === 'share'

// zeroBucket: no series has a value in the bar (only calls of unknown cost).
function zeroBucket(item: TooltipItem<'bar' | 'line'>): boolean {
  return item.chart.data.datasets.every((d) => !d.data[item.dataIndex])
}

const options = computed<ChartOptions<'bar' | 'line'>>(() => {
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
        // line, which says so. The share shows whenever it has a point.
        filter: (item, _i, items) => (isShare(item) ? item.parsed.y != null : !!item.parsed.y) || (zeroBucket(item) && item === items[0]),
        callbacks: {
          label: (item) =>
            isShare(item)
              ? ` ${item.dataset.label}: ${formatPercent(item.parsed.y ?? 0)}`
              : zeroBucket(item)
                ? props.unit === 'usd' ? 'No known cost' : 'No metered tokens'
                : ` ${item.dataset.label}: ${fmt(item.parsed.y ?? 0)}`,
          footer: (items) => {
            const i = items[0]?.dataIndex
            if (i === undefined) return ''
            return props.chart.notes ? (props.chart.notes[i] ?? '') : unknownNote(props.chart.unknown[i] ?? 0)
          },
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
      ...(props.line
        ? {
            share: {
              type: 'linear' as const,
              position: 'right' as const,
              min: 0,
              max: 100,
              grid: { display: false },
              border: { display: false },
              ticks: { ...ticks, maxTicksLimit: phone.value ? 3 : 5, callback: (v: string | number) => `${v}%` },
            },
          }
        : {}),
    },
  }
})
</script>

<template>
  <!-- Chart.js fills the container: the height is fixed, the width follows it. -->
  <div class="relative h-60 min-w-0 tablet:h-72" data-chart-canvas>
    <Chart type="bar" :data="data" :options="options" :aria-label="summary" />
  </div>
</template>
