<script setup lang="ts">
// A chart's table view (docs/ui.md, "Charts"): a row per bucket, a column per
// series, the total and the calls of unknown cost. The "Table" toggle shows
// it; hidden, it is visually hidden only, so screen readers always reach the
// values a canvas cannot give them.
import UButton from '@nuxt/ui/components/Button.vue'
import { computed, ref } from 'vue'
import { formatTokens, formatUSD, unknownNote, type StackedChart } from '../../chartData'
import DataList, { type DataListColumn } from '../DataList.vue'

const props = withDefaults(defineProps<{ chart: StackedChart; label: string; unit?: 'usd' | 'tokens' }>(), { unit: 'usd' })

const open = ref(false)
const format = computed(() => (props.unit === 'usd' ? formatUSD : formatTokens))

// Series keys may hold any character; the columns are numbered.
const columns = computed<DataListColumn[]>(() => [
  { key: 'bucket', label: 'Period' },
  ...props.chart.series.map((s, i) => ({ key: `s${i}`, label: s.label, numeric: true })),
  { key: 'total', label: 'Total', numeric: true, class: 'font-semibold' },
  { key: 'unknown', label: 'Unknown cost' },
])

const rows = computed(() =>
  props.chart.labels.map((bucket, b) => {
    const row: Record<string, string> = { id: String(b), bucket }
    let total = 0
    props.chart.series.forEach((s, i) => {
      const v = s.values[b] ?? 0
      total += v
      row[`s${i}`] = format.value(v)
    })
    row.total = format.value(total)
    row.unknown = unknownNote(props.chart.unknown[b] ?? 0)
    return row
  }),
)
</script>

<template>
  <div data-chart-table>
    <UButton
      label="Table"
      icon="i-lucide-table"
      color="neutral"
      :variant="open ? 'soft' : 'outline'"
      size="sm"
      class="min-h-11 tablet:min-h-0"
      :aria-pressed="open"
      data-chart-table-toggle
      @click="open = !open"
    />
    <div :class="open ? 'mt-3 max-h-96 overflow-y-auto' : 'sr-only'" role="group" :aria-label="label">
      <DataList :columns="columns" :rows="rows" :row-key="(r) => r.id!" scroll />
    </div>
  </div>
</template>
