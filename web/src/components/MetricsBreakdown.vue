<script setup lang="ts">
// Cost per 100 written lines (docs/contributing/web-ui.md, "Costs page"): the
// range's issues by experiment arm (one table per project, none without
// experiment data), by model, by effort and by step session. An issue counts
// whole for one row; a row with fewer than 5 merged issues is greyed out
// with the hint. The cost per 100 lines carries its merged-issue count. Its
// lines are the implement calls' written lines of the merged issues (the
// /costs/metrics field mergedLines), not the Code section's merged lines.
import { computed } from 'vue'
import { formatUSD } from '../chartData'
import { tokenCount } from '../format'
import type { MetricsGroup, MetricsView } from '../generated/views'
import { metricsTables, issuesLabel, keyLabel, lowHint, per100Label } from '../metrics'
import DataList, { type DataListColumn } from '../ui/DataList.vue'

const props = defineProps<{ metrics: MetricsView; filtered: boolean }>()

const tables = computed(() => metricsTables(props.metrics))

function columns(name: string): DataListColumn[] {
  return [
    { key: 'key', label: name },
    { key: 'issues', label: 'Merged issues', numeric: true },
    { key: 'lines', label: 'Written lines', numeric: true },
    { key: 'cost', label: 'Known cost', numeric: true },
    { key: 'tokens', label: 'Tokens', numeric: true },
    { key: 'per100', label: 'Per 100 written lines', numeric: true },
  ]
}

const rowKey = (r: MetricsGroup) => `${r.project ?? ''}/${r.key}`
const dim = (r: MetricsGroup) => (r.low ? 'text-(--text-3)' : '')
</script>

<template>
  <section class="section" aria-labelledby="costs-metrics" data-costs-metrics>
    <div class="section-head flex flex-wrap items-baseline gap-x-3 gap-y-1">
      <h2 id="costs-metrics">Cost per 100 written lines</h2>
      <span class="faint">
        Each issue with calls in the range counts whole, all its calls<template v-if="filtered">; the kind and engine filters do not apply</template>
      </span>
    </div>
    <div class="grid min-w-0 gap-3">
      <div v-for="t in tables" :key="t.id" class="card min-w-0 overflow-hidden" :data-metrics-table="t.id">
        <h3 class="m-0 px-3.5 pt-3 text-[13px] font-semibold [overflow-wrap:anywhere]">{{ t.title }}</h3>
        <p v-if="!t.rows.length" class="faint m-0 px-3.5 py-3">No issues in this range.</p>
        <DataList v-else :columns="columns(t.keyLabel)" :rows="t.rows" :row-key="rowKey">
          <template #cell-key="{ row }">
            <span :class="dim(row)" class="[overflow-wrap:anywhere]" :data-low="row.low || undefined">{{ keyLabel(row.key) }}</span>
            <span v-if="row.low" class="block text-xs text-(--text-3)">{{ lowHint }}</span>
          </template>
          <template #cell-issues="{ row }"><span :class="dim(row)">{{ row.issues }}</span></template>
          <template #cell-lines="{ row }"><span :class="dim(row)">{{ row.mergedLines.toLocaleString('en-US') }}</span></template>
          <template #cell-cost="{ row }"><span :class="dim(row)">{{ formatUSD(row.costUSD) }}</span></template>
          <template #cell-tokens="{ row }"><span :class="dim(row)">{{ tokenCount(row.tokens) }}</span></template>
          <template #cell-per100="{ row }">
            <span :class="dim(row)">{{ per100Label(row) }} <span class="faint">· {{ issuesLabel(row.issues) }}</span></span>
          </template>
        </DataList>
      </div>
    </div>
  </section>
</template>
